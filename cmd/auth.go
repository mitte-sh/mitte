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
		fmt.Fprintf(os.Stderr, "Auth is already configured. Run 'mitte auth info' to see status.\n")
		fmt.Fprintf(os.Stderr, "Use --force to re-run setup.\n")
		return
	}

	// If forcing, stop and remove existing container so it gets recreated with new config
	if force {
		fmt.Fprintln(os.Stderr, "-----> Force mode: removing existing Authelia container and data...")
		if err := auth.StopContainer(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		// Remove the SQLite database so Authelia creates a fresh one with the new encryption key
		dbPath := auth.AuthDir + "/db.sqlite3"
		if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove database: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "-----> Removed old auth database.")
		}
	}

	fmt.Fprintln(os.Stderr, "-----> Setting up Authelia authentication service...")

	// Get base domain
	baseDomain, err := config.GetBaseDomain()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Generate or reuse secrets
	var cfg auth.Config
	if force && auth.IsSetup() {
		// Reuse existing secrets so the users database remains valid
		fmt.Fprintln(os.Stderr, "-----> Reusing existing secrets...")
		existingCfg, err := auth.LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load existing config, generating new secrets: %v\n", err)
			existingCfg = nil
		}
		if existingCfg != nil && existingCfg.JWTSecret != "" {
			cfg = *existingCfg
		}
	}

	// Generate new secrets if we don't have them
	if cfg.JWTSecret == "" {
		fmt.Fprintln(os.Stderr, "-----> Generating secrets...")
		cfg.JWTSecret, err = auth.GenerateSecret(64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating JWT secret: %v\n", err)
			os.Exit(1)
		}
		cfg.SessionSecret, err = auth.GenerateSecret(64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating session secret: %v\n", err)
			os.Exit(1)
		}
		cfg.EncryptionKey, err = auth.GenerateSecret(64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating encryption key: %v\n", err)
			os.Exit(1)
		}
	}

	// Set domain-related config
	cfg.BaseDomain = baseDomain
	cfg.AutheliaURL = auth.LoginPortalURL(baseDomain)
	cfg.CookieDomain = auth.CookieDomain(baseDomain)

	if err := auth.GenerateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating config: %v\n", err)
		os.Exit(1)
	}

	// Generate empty users file
	fmt.Fprintln(os.Stderr, "-----> Creating users database...")
	if err := auth.GenerateUsersFile(); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating users file: %v\n", err)
		os.Exit(1)
	}

	// Start Authelia container
	fmt.Fprintln(os.Stderr, "-----> Starting Authelia container...")
	if err := auth.EnsureContainer(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting Authelia: %v\n", err)
		os.Exit(1)
	}

	// Create auth portal route
	fmt.Fprintln(os.Stderr, "-----> Creating auth portal route...")
	if err := router.CreateAuthPortalRoute(baseDomain); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not create auth portal route: %v\n", err)
		fmt.Fprintf(os.Stderr, "You may need to manually create a DNS record for auth.%s\n", baseDomain)
	}

	// Prompt for first user
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "-----> Create your first admin user:")
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
			fmt.Fprintf(os.Stderr, "Warning: Could not create user: %v\n", err)
			fmt.Fprintf(os.Stderr, "You can create users later with: mitte auth add-user <username>\n")
		} else {
			fmt.Fprintf(os.Stderr, "-----> User '%s' created successfully.\n", username)
		}
	} else {
		fmt.Fprintf(os.Stderr, "-----> Skipped user creation. Use 'mitte auth add-user <username>' to create users.\n")
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "-----> Auth setup complete!\n")
	fmt.Fprintf(os.Stderr, "       Login portal: %s\n", auth.LoginPortalURL(baseDomain))
	fmt.Fprintf(os.Stderr, "       To protect an app: mitte auth enable <app-name>\n")
}

func runAuthEnable(cmd *cobra.Command, args []string) {
	appName := args[0]
	ctx := context.Background()

	// Check if auth is set up
	if !auth.IsSetup() {
		fmt.Fprintf(os.Stderr, "Error: Auth not configured. Run 'mitte auth setup' first.\n")
		os.Exit(1)
	}

	// Check if authelia is running
	running, err := auth.IsRunning(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking Authelia status: %v\n", err)
		os.Exit(1)
	}
	if !running {
		fmt.Fprintf(os.Stderr, "Error: Authelia container is not running. Run 'mitte auth setup' to start it.\n")
		os.Exit(1)
	}

	// Load app state
	app, err := state.Load(appName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading app '%s': %v\n", appName, err)
		os.Exit(1)
	}
	if len(app.Domains) == 0 {
		fmt.Fprintf(os.Stderr, "Error: App '%s' does not exist.\n", appName)
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
		fmt.Fprintf(os.Stderr, "Error saving app state: %v\n", err)
		os.Exit(1)
	}

	// Update routes with auth
	if app.HostPort != "" {
		if err := router.SetAppRoutesWithAuth(appName, app.Domains, app.HostPort, true, policy); err != nil {
			fmt.Fprintf(os.Stderr, "Error updating routes: %v\n", err)
			os.Exit(1)
		}
	}

	// Regenerate Authelia ACL rules
	if err := updateAutheliaACL(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not update Authelia ACL rules: %v\n", err)
		fmt.Fprintf(os.Stderr, "You may need to restart Authelia manually: docker restart mitte-authelia\n")
	}

	fmt.Printf("Auth enabled for app '%s' with policy '%s'.\n", appName, policy)
	fmt.Printf("Users will be redirected to %s to authenticate.\n", auth.LoginPortalURL(getBaseDomain()))
}

func runAuthDisable(cmd *cobra.Command, args []string) {
	appName := args[0]
	ctx := context.Background()

	// Load app state
	app, err := state.Load(appName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading app '%s': %v\n", appName, err)
		os.Exit(1)
	}
	if len(app.Domains) == 0 {
		fmt.Fprintf(os.Stderr, "Error: App '%s' does not exist.\n", appName)
		os.Exit(1)
	}

	// Disable auth
	app.Auth = nil
	if err := app.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving app state: %v\n", err)
		os.Exit(1)
	}

	// Update routes without auth
	if app.HostPort != "" {
		if err := router.SetAppRoutes(appName, app.Domains, app.HostPort); err != nil {
			fmt.Fprintf(os.Stderr, "Error updating routes: %v\n", err)
			os.Exit(1)
		}
	}

	// Regenerate Authelia ACL rules
	if auth.IsSetup() {
		if err := updateAutheliaACL(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not update Authelia ACL rules: %v\n", err)
		}
	}

	fmt.Printf("Auth disabled for app '%s'.\n", appName)
}

func runAuthStatus(cmd *cobra.Command, args []string) {
	appName := args[0]

	app, err := state.Load(appName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading app '%s': %v\n", appName, err)
		os.Exit(1)
	}
	if len(app.Domains) == 0 {
		fmt.Fprintf(os.Stderr, "Error: App '%s' does not exist.\n", appName)
		os.Exit(1)
	}

	if app.Auth == nil || !app.Auth.Enabled {
		fmt.Printf("Auth: disabled\n")
	} else {
		fmt.Printf("Auth: enabled\n")
		fmt.Printf("Policy: %s\n", app.Auth.Policy)
	}
}

func runAuthInfo(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	if !auth.IsSetup() {
		fmt.Println("Auth: not configured")
		fmt.Println("Run 'mitte auth setup' to get started.")
		return
	}

	status := auth.Status(ctx)
	fmt.Printf("Authelia status: %s\n", status)

	baseDomain, err := config.GetBaseDomain()
	if err == nil {
		fmt.Printf("Login portal: %s\n", auth.LoginPortalURL(baseDomain))
	}

	// List users
	users, err := auth.ListUsers()
	if err == nil && len(users) > 0 {
		fmt.Printf("\nUsers:\n")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "USERNAME\tEMAIL\tGROUPS")
		for _, u := range users {
			fmt.Fprintf(w, "%s\t%s\t%s\n", u.Username, u.Email, strings.Join(u.Groups, ", "))
		}
		w.Flush()
	} else {
		fmt.Printf("\nNo users configured. Use 'mitte auth add-user <username>' to add users.\n")
	}
}

func runAuthAddUser(cmd *cobra.Command, args []string) {
	username := args[0]

	if !auth.IsSetup() {
		fmt.Fprintf(os.Stderr, "Error: Auth not configured. Run 'mitte auth setup' first.\n")
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
		fmt.Fprintf(os.Stderr, "Error: Password cannot be empty.\n")
		os.Exit(1)
	}

	if err := auth.AddUser(auth.UserEntry{
		Username:    username,
		DisplayName: displayName,
		Email:       email,
		Groups:      []string{"users"},
	}, password); err != nil {
		fmt.Fprintf(os.Stderr, "Error adding user: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("User '%s' created successfully.\n", username)
}

func runAuthRemoveUser(cmd *cobra.Command, args []string) {
	username := args[0]

	if !auth.IsSetup() {
		fmt.Fprintf(os.Stderr, "Error: Auth not configured. Run 'mitte auth setup' first.\n")
		os.Exit(1)
	}

	if err := auth.RemoveUser(username); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing user: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("User '%s' removed successfully.\n", username)
}

func runAuthListUsers(cmd *cobra.Command, args []string) {
	if !auth.IsSetup() {
		fmt.Fprintf(os.Stderr, "Error: Auth not configured. Run 'mitte auth setup' first.\n")
		os.Exit(1)
	}

	users, err := auth.ListUsers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing users: %v\n", err)
		os.Exit(1)
	}

	if len(users) == 0 {
		fmt.Println("No users configured.")
		fmt.Println("Use 'mitte auth add-user <username>' to add users.")
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
	fmt.Fprintf(os.Stderr, "-----> Updating Authelia ACL rules (%d domain rules)...\n", len(rules))
	if err := auth.GenerateConfigWithACL(*cfg, rules); err != nil {
		return fmt.Errorf("failed to regenerate Authelia config: %w", err)
	}

	// Restart Authelia to pick up config changes
	if err := auth.RestartContainer(ctx); err != nil {
		return fmt.Errorf("failed to restart Authelia: %w", err)
	}

	return nil
}
