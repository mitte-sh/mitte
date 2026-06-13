package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/auth"
	"github.com/mitte-sh/mitte/pkg/config"
	"github.com/mitte-sh/mitte/pkg/logger"
	"github.com/mitte-sh/mitte/pkg/router"
	"github.com/mitte-sh/mitte/pkg/state"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication for your applications",
}

var authSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up the Authelia authentication service",
	Long: `Set up the Authelia authentication service.
This will create the Authelia container, generate configuration files,
and create the login portal route at auth.<your-domain>.`,
	Run: runAuthSetup,
}

var authEnableCmd = &cobra.Command{
	Use:   "enable <app-name>",
	Short: "Enable authentication for an application",
	Long: `Enable authentication for an application.
Users will need to authenticate via the login portal before accessing the app.
By default, uses one_factor (password only). Use --two-factor for 2FA.`,
	Args: cobra.ExactArgs(1),
	Run:  runAuthEnable,
}

var authDisableCmd = &cobra.Command{
	Use:   "disable <app-name>",
	Short: "Disable authentication for an application",
	Args:  cobra.ExactArgs(1),
	Run:   runAuthDisable,
}

var authStatusCmd = &cobra.Command{
	Use:   "status <app-name>",
	Short: "Show authentication status for an application",
	Args:  cobra.ExactArgs(1),
	Run:   runAuthStatus,
}

var authInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show Authelia service information",
	Run:   runAuthInfo,
}

var authAddUserCmd = &cobra.Command{
	Use:   "add-user <username>",
	Short: "Add a user to the authentication system",
	Args:  cobra.ExactArgs(1),
	Run:   runAuthAddUser,
}

var authRemoveUserCmd = &cobra.Command{
	Use:   "remove-user <username>",
	Short: "Remove a user from the authentication system",
	Args:  cobra.ExactArgs(1),
	Run:   runAuthRemoveUser,
}

var authListUsersCmd = &cobra.Command{
	Use:   "list-users",
	Short: "List all users in the authentication system",
	Run:   runAuthListUsers,
}

func init() {
	authSetupCmd.Flags().Bool("force", false, "Force re-run setup even if already configured")
	authEnableCmd.Flags().Bool("two-factor", false, "Require two-factor authentication (TOTP)")

	authCmd.AddCommand(authSetupCmd)
	authCmd.AddCommand(authEnableCmd)
	authCmd.AddCommand(authDisableCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authInfoCmd)
	authCmd.AddCommand(authAddUserCmd)
	authCmd.AddCommand(authRemoveUserCmd)
	authCmd.AddCommand(authListUsersCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthSetup(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	force, _ := cmd.Flags().GetBool("force")

	if auth.IsSetup() && !force {
		logger.Info("Auth is already configured. Run 'mitte auth info' to see status.")
		logger.Info("Use --force to re-run setup.")
		return
	}

	// If forcing, stop and remove existing container so it gets recreated with new config
	if force {
		logger.Info("-----> Force mode: removing existing Authelia container and data...")
		if err := auth.StopContainer(ctx); err != nil {
			logger.Warn("", "err", err)
		}
		// Remove the SQLite database so Authelia creates a fresh one with the new encryption key
		dbPath := auth.AuthDir + "/db.sqlite3"
		if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
			logger.Warn("could not remove database", "err", err)
		} else {
			logger.Info("-----> Removed old auth database.")
		}
	}

	logger.Info("-----> Setting up Authelia authentication service...")

	// Get base domain
	baseDomain, err := config.GetBaseDomain()
	if err != nil {
		logger.Error("", "err", err)
		os.Exit(1)
	}

	// Generate or reuse secrets
	var cfg auth.Config
	if force && auth.IsSetup() {
		// Reuse existing secrets so the users database remains valid
		logger.Info("-----> Reusing existing secrets...")
		existingCfg, err := auth.LoadConfig()
		if err != nil {
			logger.Warn("could not load existing config, generating new secrets", "err", err)
			existingCfg = nil
		}
		if existingCfg != nil && existingCfg.JWTSecret != "" {
			cfg = *existingCfg
		}
	}

	// Generate new secrets if we don't have them
	if cfg.JWTSecret == "" {
		logger.Info("-----> Generating secrets...")
		cfg.JWTSecret, err = auth.GenerateSecret(64)
		if err != nil {
			logger.Error(fmt.Sprintf("Error generating JWT secret: %v", err))
			os.Exit(1)
		}
		cfg.SessionSecret, err = auth.GenerateSecret(64)
		if err != nil {
			logger.Error(fmt.Sprintf("Error generating session secret: %v", err))
			os.Exit(1)
		}
		cfg.EncryptionKey, err = auth.GenerateSecret(64)
		if err != nil {
			logger.Error(fmt.Sprintf("Error generating encryption key: %v", err))
			os.Exit(1)
		}
	}

	// Set domain-related config
	cfg.BaseDomain = baseDomain
	cfg.AutheliaURL = auth.LoginPortalURL(baseDomain)
	cfg.CookieDomain = auth.CookieDomain(baseDomain)

	if err := auth.GenerateConfig(cfg); err != nil {
		logger.Error(fmt.Sprintf("Error generating config: %v", err))
		os.Exit(1)
	}

	// Generate empty users file
	logger.Info("-----> Creating users database...")
	if err := auth.GenerateUsersFile(); err != nil {
		logger.Error(fmt.Sprintf("Error creating users file: %v", err))
		os.Exit(1)
	}

	// Start Authelia container
	logger.Info("-----> Starting Authelia container...")
	if err := auth.EnsureContainer(ctx); err != nil {
		logger.Error(fmt.Sprintf("Error starting Authelia: %v", err))
		os.Exit(1)
	}

	// Create auth portal route
	logger.Info("-----> Creating auth portal route...")
	if err := router.CreateAuthPortalRoute(baseDomain); err != nil {
		logger.Warn("Could not create auth portal route", "err", err)
		logger.Info(fmt.Sprintf("You may need to manually create a DNS record for auth.%s", baseDomain))
	}

	// Prompt for first user
	logger.Info("")
	logger.Info("-----> Create your first admin user:")
	reader := bufio.NewReader(os.Stdin)

	fmt.Fprintf(os.Stderr, "Username: ")
	username, _ := reader.ReadString('\n')
	username = strings.TrimSpace(username)

	fmt.Fprintf(os.Stderr, "Email: ")
	email, _ := reader.ReadString('\n')
	email = strings.TrimSpace(email)

	fmt.Fprintf(os.Stderr, "Password: ")
	password, _ := reader.ReadString('\n')
	password = strings.TrimSpace(password)

	if username != "" && password != "" {
		displayName := username
		if err := auth.AddUser(auth.UserEntry{
			Username:    username,
			DisplayName: displayName,
			Email:       email,
			Groups:      []string{"admins", "users"},
		}, password); err != nil {
			logger.Warn("Could not create user", "err", err)
			logger.Info("You can create users later with: mitte auth add-user <username>")
		} else {
			logger.Info(fmt.Sprintf("-----> User '%s' created successfully.", username))
		}
	} else {
		logger.Info("-----> Skipped user creation. Use 'mitte auth add-user <username>' to create users.")
	}

	logger.Info("")
	logger.Info("-----> Auth setup complete!")
	logger.Info(fmt.Sprintf("       Login portal: %s", auth.LoginPortalURL(baseDomain)))
	logger.Info("       To protect an app: mitte auth enable <app-name>")
}

func runAuthEnable(cmd *cobra.Command, args []string) {
	appName := args[0]
	ctx := context.Background()

	// Check if auth is set up
	if !auth.IsSetup() {
		logger.Error("Auth not configured. Run 'mitte auth setup' first.")
		os.Exit(1)
	}

	// Check if authelia is running
	running, err := auth.IsRunning(ctx)
	if err != nil {
		logger.Error(fmt.Sprintf("Error checking Authelia status: %v", err))
		os.Exit(1)
	}
	if !running {
		logger.Error("Authelia container is not running. Run 'mitte auth setup' to start it.")
		os.Exit(1)
	}

	// Load app state
	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Error loading app '%s': %v", appName, err))
		os.Exit(1)
	}
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist.", appName))
		os.Exit(1)
	}

	// Determine policy
	policy := "one_factor"
	twoFactor, _ := cmd.Flags().GetBool("two-factor")
	if twoFactor {
		policy = "two_factor"
	}

	// Set auth config
	app.Auth = &state.AuthConfig{
		Enabled: true,
		Policy:  policy,
	}
	if err := app.Save(); err != nil {
		logger.Error(fmt.Sprintf("Error saving app state: %v", err))
		os.Exit(1)
	}

	// Update routes with auth
	if app.HostPort != "" {
		if err := router.SetAppRoutesWithAuth(appName, app.Domains, app.HostPort, true, policy); err != nil {
			logger.Error(fmt.Sprintf("Error updating routes: %v", err))
			os.Exit(1)
		}
	}

	// Regenerate Authelia ACL rules
	if err := updateAutheliaACL(ctx); err != nil {
		logger.Warn("Could not update Authelia ACL rules", "err", err)
		logger.Error("You may need to restart Authelia manually: docker restart mitte-authelia")
	}

	logger.Info(fmt.Sprintf("Auth enabled for app '%s' with policy '%s'.", appName, policy))
	logger.Info(fmt.Sprintf("Users will be redirected to %s to authenticate.", auth.LoginPortalURL(getBaseDomain())))
}

func runAuthDisable(cmd *cobra.Command, args []string) {
	appName := args[0]
	ctx := context.Background()

	// Load app state
	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Error loading app '%s': %v", appName, err))
		os.Exit(1)
	}
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist.", appName))
		os.Exit(1)
	}

	// Disable auth
	app.Auth = nil
	if err := app.Save(); err != nil {
		logger.Error(fmt.Sprintf("Error saving app state: %v", err))
		os.Exit(1)
	}

	// Update routes without auth
	if app.HostPort != "" {
		if err := router.SetAppRoutes(appName, app.Domains, app.HostPort); err != nil {
			logger.Error(fmt.Sprintf("Error updating routes: %v", err))
			os.Exit(1)
		}
	}

	// Regenerate Authelia ACL rules
	if auth.IsSetup() {
		if err := updateAutheliaACL(ctx); err != nil {
			logger.Warn("Could not update Authelia ACL rules", "err", err)
		}
	}

	logger.Info(fmt.Sprintf("Auth disabled for app '%s'.", appName))
}

func runAuthStatus(cmd *cobra.Command, args []string) {
	appName := args[0]

	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Error loading app '%s': %v", appName, err))
		os.Exit(1)
	}
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist.", appName))
		os.Exit(1)
	}

	if app.Auth == nil || !app.Auth.Enabled {
		logger.Info("Auth: disabled")
	} else {
		logger.Info("Auth: enabled")
		logger.Info(fmt.Sprintf("Policy: %s", app.Auth.Policy))
	}
}

func runAuthInfo(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	if !auth.IsSetup() {
		logger.Info("Auth: not configured")
		logger.Info("Run 'mitte auth setup' to get started.")
		return
	}

	status := auth.Status(ctx)
	logger.Info(fmt.Sprintf("Authelia status: %s", status))

	baseDomain, err := config.GetBaseDomain()
	if err == nil {
		logger.Info(fmt.Sprintf("Login portal: %s", auth.LoginPortalURL(baseDomain)))
	}

	// List users
	users, err := auth.ListUsers()
	if err == nil && len(users) > 0 {
		logger.Info("\nUsers:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "USERNAME\tEMAIL\tGROUPS")
		for _, u := range users {
			fmt.Fprintf(w, "%s\t%s\t%s\n", u.Username, u.Email, strings.Join(u.Groups, ", "))
		}
		w.Flush()
	} else {
		logger.Info("\nNo users configured. Use 'mitte auth add-user <username>' to add users.")
	}
}

func runAuthAddUser(cmd *cobra.Command, args []string) {
	username := args[0]

	if !auth.IsSetup() {
		logger.Error("Auth not configured. Run 'mitte auth setup' first.")
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Fprintf(os.Stderr, "Display name [%s]: ", username)
	displayName, _ := reader.ReadString('\n')
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = username
	}

	fmt.Fprintf(os.Stderr, "Email: ")
	email, _ := reader.ReadString('\n')
	email = strings.TrimSpace(email)

	fmt.Fprintf(os.Stderr, "Password: ")
	password, _ := reader.ReadString('\n')
	password = strings.TrimSpace(password)

	if password == "" {
		logger.Error("Password cannot be empty.")
		os.Exit(1)
	}

	if err := auth.AddUser(auth.UserEntry{
		Username:    username,
		DisplayName: displayName,
		Email:       email,
		Groups:      []string{"users"},
	}, password); err != nil {
		logger.Error(fmt.Sprintf("Error adding user: %v", err))
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("User '%s' created successfully.", username))
}

func runAuthRemoveUser(cmd *cobra.Command, args []string) {
	username := args[0]

	if !auth.IsSetup() {
		logger.Error("Auth not configured. Run 'mitte auth setup' first.")
		os.Exit(1)
	}

	if err := auth.RemoveUser(username); err != nil {
		logger.Error(fmt.Sprintf("Error removing user: %v", err))
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("User '%s' removed successfully.", username))
}

func runAuthListUsers(cmd *cobra.Command, args []string) {
	if !auth.IsSetup() {
		logger.Error("Auth not configured. Run 'mitte auth setup' first.")
		os.Exit(1)
	}

	users, err := auth.ListUsers()
	if err != nil {
		logger.Error(fmt.Sprintf("Error listing users: %v", err))
		os.Exit(1)
	}

	if len(users) == 0 {
		logger.Info("No users configured.")
		logger.Info("Use 'mitte auth add-user <username>' to add users.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "USERNAME\tDISPLAY NAME\tEMAIL\tGROUPS")
	for _, u := range users {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", u.Username, u.DisplayName, u.Email, strings.Join(u.Groups, ", "))
	}
	w.Flush()
}

// getBaseDomain is a helper to get the base domain, returning a fallback on error.
func getBaseDomain() string {
	domain, err := config.GetBaseDomain()
	if err != nil {
		return "localhost"
	}
	return domain
}

// updateAutheliaACL collects ACL rules from all protected apps and regenerates
// the Authelia config, then restarts the container.
func updateAutheliaACL(ctx context.Context) error {
	// Collect ACL rules from all apps
	rules, err := auth.CollectACLRules()
	if err != nil {
		return fmt.Errorf("failed to collect ACL rules: %w", err)
	}

	// Load existing config to preserve secrets
	cfg, err := auth.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load Authelia config: %w", err)
	}

	// Regenerate config with ACL rules
	logger.Info(fmt.Sprintf("-----> Updating Authelia ACL rules (%d domain rules)...", len(rules)))
	if err := auth.GenerateConfigWithACL(*cfg, rules); err != nil {
		return fmt.Errorf("failed to regenerate Authelia config: %w", err)
	}

	// Restart Authelia to pick up config changes
	if err := auth.RestartContainer(ctx); err != nil {
		return fmt.Errorf("failed to restart Authelia: %w", err)
	}

	return nil
}
