package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/actions"
	"github.com/mitte-sh/mitte/pkg/builder"
	"github.com/mitte-sh/mitte/pkg/config"
	"github.com/mitte-sh/mitte/pkg/deployer"
	"github.com/mitte-sh/mitte/pkg/logger"
	"github.com/mitte-sh/mitte/pkg/registry"
	"github.com/mitte-sh/mitte/pkg/router"
	"github.com/mitte-sh/mitte/pkg/state"
)

var appsCmd = &cobra.Command{
	Use:     "apps",
	Short:   "Manage your applications",
	Aliases: []string{"app"},
}

var appsListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all deployed applications",
	Aliases: []string{"ls"},
	Run:     runAppsList,
}

const placeholderImage = "nginxdemos/hello:plain-text"

var appsCreateCmd = &cobra.Command{
	Use:   "create <app-name>",
	Short: "Create a new application placeholder",
	Args:  cobra.ExactArgs(1),
	Run:   runAppsCreate,
}

// appsDestroyCmd handles `mitte apps destroy <app-name>`
var appsDestroyCmd = &cobra.Command{
	Use:   "destroy <app-name>",
	Short: "Permanently destroy an application",
	Long: `Permanently destroy an application.
This will stop and remove the container, delete all built images,
remove networking routes, and delete the git repository.
THIS ACTION IS IRREVERSIBLE.`,
	Aliases: []string{"remove"},
	Args:    cobra.ExactArgs(1),
	Run:     runAppsDestroy,
}

var appsBuildCmd = &cobra.Command{
	Use:   "build <app-name>",
	Short: "Build and deploy an application",
	Long: `Build an application from its latest git code and deploy it.
This will check out the latest code from the git repository, build a new Docker image
using the current environment variables, and deploy the application.`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsBuild,
}

var appsSetImageCmd = &cobra.Command{
	Use:   "set-image <app-name> <image>",
	Short: "Set the Docker image for an application",
	Long: `Set a pre-built Docker image for an application.
This allows deploying applications using existing Docker images instead of building from source code.
Example: mitte apps set-image myapp nginx:latest`,
	Args: cobra.ExactArgs(2),
	Run:  runAppsSetImage,
}

var appsSetVolumesCmd = &cobra.Command{
	Use:   "set-volumes <app-name> <volume>...",
	Short: "Set volume mounts for an application",
	Long: `Set volume mounts for an application.
Volumes are specified as host:container pairs.
Examples:
  mitte apps set-volumes myapp /host/path:/container/path
  mitte apps set-volumes myapp /host/path:/container/path:ro $HOME/data:/app/data
  mitte apps set-volumes myapp /tmp/cache:/app/cache:rw`,
	Args: cobra.MinimumNArgs(2),
	Run:  runAppsSetVolumes,
}

var appsUnsetVolumesCmd = &cobra.Command{
	Use:   "unset-volumes <app-name> [volume...]",
	Short: "Unset volume mounts for an application",
	Long: `Unset volume mounts for an application.
Volumes are specified as host:container pairs.
Use the --all flag to remove all volume mounts.
By default, the application is redeployed to apply changes. Use the --no-restart flag to prevent this.
Examples:
  mitte apps unset-volumes myapp /host/path:/container/path
  mitte apps unset-volumes myapp --all`,
	Run: runAppsUnsetVolumes,
}

var appsSetPortsCmd = &cobra.Command{
	Use:   "set-ports <app-name> <port>...",
	Short: "Set port mappings for an application",
	Long: `Set port mappings for an application.
Ports are specified as host:container pairs.
Examples:
  mitte apps set-ports myapp 8080:80
  mitte apps set-ports myapp 9200:9200 3000:80
  mitte apps set-ports myapp 8443:443 8080:8080`,
	Args: cobra.MinimumNArgs(2),
	Run:  runAppsSetPorts,
}

var appsUnsetPortsCmd = &cobra.Command{
	Use:   "unset-ports <app-name> [port...]",
	Short: "Unset port mappings for an application",
	Long: `Unset port mappings for an application.
Ports are specified as host:container pairs.
Use the --all flag to remove all port mappings.
By default, the application is redeployed to apply changes. Use the --no-restart flag to prevent this.
Examples:
  mitte apps unset-ports myapp 8080:80
  mitte apps unset-ports myapp --all`,
	Run: runAppsUnsetPorts,
}

var appsListVolumesCmd = &cobra.Command{
	Use:     "list-volumes <app-name>",
	Short:   "List volume mounts for an application",
	Aliases: []string{"volumes"},
	Args:    cobra.ExactArgs(1),
	Run:     runAppsListVolumes,
}

var appsListPortsCmd = &cobra.Command{
	Use:     "list-ports <app-name>",
	Short:   "List port mappings for an application",
	Aliases: []string{"ports"},
	Args:    cobra.ExactArgs(1),
	Run:     runAppsListPorts,
}

var appsDeployImageCmd = &cobra.Command{
	Use:   "deploy-image <app-name>",
	Short: "Deploy an application using a pre-built Docker image",
	Long: `Deploy an application using a pre-built Docker image that has been configured with set-image.
This will pull the image if it's not available locally, create and start a container with
the configured volumes, ports, and container name, then update the routing configuration.

⚠️  IMPORTANT: Environment variables are applied at runtime, not during image build.
For pre-built images, env vars will be available to the running container but not during
the image build process. If your app needs environment variables during build, use the
git push method with a Dockerfile instead.

Before running this command, you must:
1. Create the app: mitte apps create <app-name>
2. Set the image: mitte apps set-image <app-name> <image>
3. Optionally configure volumes: mitte apps set-volumes <app-name> <volumes...>
4. Optionally configure ports: mitte apps set-ports <app-name> <ports...>

Examples:
  mitte apps deploy-image myapp`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsDeployImage,
}

var appsSetBuildpackCmd = &cobra.Command{
	Use:   "set-buildpack <app-name> <buildpack-id>",
	Short: "Set the buildpack to use for an application",
	Args:  cobra.ExactArgs(2),
	Run:   runAppsSetBuildpack,
}

var appsDetectBuildpackCmd = &cobra.Command{
	Use:   "detect-buildpack <app-name>",
	Short: "Detect and suggest a buildpack for an application",
	Args:  cobra.ExactArgs(1),
	Run:   runAppsDetectBuildpack,
}

var appsSetCommandCmd = &cobra.Command{
	Use:   "set-command <app-name> <command>...",
	Short: "Set the custom command/entrypoint for an application",
	Long: `Set the custom command or entrypoint arguments for an application.
This is equivalent to the command passed at the end of 'docker run'.
Example: mitte apps set-command myapp serve --port 8080`,
	Args: cobra.MinimumNArgs(2),
	Run:  runAppsSetCommand,
}

var appsSetUserCmd = &cobra.Command{
	Use:   "set-user <app-name> <user>",
	Short: "Set the custom user to run the application",
	Long: `Set the custom user (UID:GID or username) to run the application.
This is equivalent to the -u flag in 'docker run'.
Example: mitte apps set-user myapp 1000:1000`,
	Args: cobra.ExactArgs(2),
	Run:  runAppsSetUser,
}

var appsEnableCmd = &cobra.Command{
	Use:   "enable <app-name>",
	Short: "Enable an application",
	Args:  cobra.ExactArgs(1),
	Run:   runAppsEnable,
}

var appsDisableCmd = &cobra.Command{
	Use:   "disable <app-name>",
	Short: "Disable an application",
	Args:  cobra.ExactArgs(1),
	Run:   runAppsDisable,
}

func init() {
	appsCmd.AddCommand(appsListCmd)
	appsCmd.AddCommand(appsCreateCmd)
	appsCmd.AddCommand(appsDestroyCmd)
	appsCmd.AddCommand(appsBuildCmd)
	appsCmd.AddCommand(appsSetImageCmd)

	appsSetVolumesCmd.Flags().Bool("no-restart", false, "Set the volumes without restarting the application")
	appsCmd.AddCommand(appsSetVolumesCmd)

	appsUnsetVolumesCmd.Flags().Bool("all", false, "Remove all volume mounts")
	appsUnsetVolumesCmd.Flags().Bool("no-restart", false, "Unset the volumes without restarting the application")
	appsCmd.AddCommand(appsUnsetVolumesCmd)

	appsCmd.AddCommand(appsListVolumesCmd)

	appsSetPortsCmd.Flags().Bool("no-restart", false, "Set the ports without restarting the application")
	appsCmd.AddCommand(appsSetPortsCmd)

	appsUnsetPortsCmd.Flags().Bool("all", false, "Remove all port mappings")
	appsUnsetPortsCmd.Flags().Bool("no-restart", false, "Unset the ports without restarting the application")
	appsCmd.AddCommand(appsUnsetPortsCmd)

	appsCmd.AddCommand(appsListPortsCmd)
	appsCmd.AddCommand(appsDeployImageCmd)
	appsCmd.AddCommand(appsSetBuildpackCmd)
	appsCmd.AddCommand(appsDetectBuildpackCmd)
	appsCmd.AddCommand(appsSetCommandCmd)
	appsCmd.AddCommand(appsSetUserCmd)
	appsCmd.AddCommand(appsEnableCmd)
	appsCmd.AddCommand(appsDisableCmd)
	rootCmd.AddCommand(appsCmd)
}

func runAppsList(cmd *cobra.Command, args []string) {
	const appsDir = "/var/lib/mitte/apps"
	files, err := os.ReadDir(appsDir)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("No applications have been deployed yet.")
			return
		}
		logger.Error("Could not read the application directory", "err", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		logger.Info("No applications found.")
		return
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Error("Could not connect to Docker daemon", "err", err)
		os.Exit(1)
	}
	defer cli.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	defer w.Flush()
	fmt.Fprintln(w, "APP\tSTATUS\tCREATED\tIMAGE TAG\tURLS")

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		appName := strings.TrimSuffix(file.Name(), ".json")

		var status, created, imageTag, urls string

		appState, err := state.Load(appName)
		if err != nil {
			urls = "error"
		} else {
			urls = strings.Join(appState.Domains, ", ")
			if appState.Disabled {
				status = "disabled"
			}
		}

		if status != "disabled" {
			inspect, err := cli.ContainerInspect(context.Background(), appName)
			if err != nil {
				if errdefs.IsNotFound(err) {
					status = "stopped"
					created = "-"
					imageTag = "(no image)"
				} else {
					status = "error"
				}
			} else {
				status = inspect.State.Status
				imageTag = filepath.Base(inspect.Config.Image)
				createdTime, _ := time.Parse(time.RFC3339Nano, inspect.Created)
				created = formatTimeAgo(createdTime)
			}
		} else {
			created = "-"
			imageTag = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", appName, status, created, imageTag, urls)
	}
}

func runAppsCreate(cmd *cobra.Command, args []string) {
	appName := args[0]
	logger.Info(fmt.Sprintf("Creating app '%s'... ", appName))

	// --- 1. Check if app already exists ---
	app, err := state.Load(appName)
	if err != nil {
		logger.Error("Could not check app state", "err", err)
		os.Exit(1)
	}
	// An "existing" app is one that already has domains.
	if len(app.Domains) > 0 {
		logger.Error(fmt.Sprintf("Application '%s' already exists.", appName))
		os.Exit(1)
	}
	logger.Info("done.")

	// --- 2. Create app state with a default domain ---
	baseDomain, err := config.GetBaseDomain()
	if err != nil {
		logger.Error(fmt.Sprintf("Error: %v", err))
		os.Exit(1)
	}
	defaultDomain := fmt.Sprintf("%s.%s", appName, baseDomain)
	logger.Info(fmt.Sprintf("Assigning default domain: %s", defaultDomain))
	app.Domains = append(app.Domains, defaultDomain)
	if err = app.Save(); err != nil {
		logger.Error("Could not save app state", "err", err)
		os.Exit(1)
	}

	// --- 3. Pull the placeholder image ---
	logger.Info(fmt.Sprintf("Pulling placeholder image '%s'... ", placeholderImage))
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Error("Could not connect to Docker", "err", err)
		os.Exit(1)
	}
	defer cli.Close()

	reader, err := cli.ImagePull(context.Background(), placeholderImage, image.PullOptions{})
	if err != nil {
		logger.Error("Could not pull placeholder image", "err", err)
		os.Exit(1)
	}
	io.Copy(io.Discard, reader) // Wait for the pull to complete but discard the noisy output
	reader.Close()
	logger.Info("done.")

	// --- 4. Deploy the placeholder image ---
	logger.Info("Deploying placeholder application...")
	deployResult, err := deployer.Deploy(context.Background(), appName, placeholderImage, []string{}, []string{}, "", []string{}, "")
	if err != nil {
		logger.Error("Could not deploy placeholder", "err", err)
		os.Exit(1)
	}

	// --- 4.5. Save host port to app state ---
	app.HostPort = deployResult.HostPort
	if err = app.Save(); err != nil {
		logger.Warn("Could not save host port", "err", err)
	}

	// --- 5. Route traffic ---
	logger.Info("Routing traffic...")
	authEnabled := app.Auth != nil && app.Auth.Enabled
	authPolicy := ""
	if authEnabled {
		authPolicy = app.Auth.Policy
	}
	if err := router.SetAppRoutesWithAuth(appName, app.Domains, deployResult.HostPort, authEnabled, authPolicy); err != nil {
		logger.Error("Could not update routes", "err", err)
		// Don't exit here, the app is running, just not routable.
	}

	// --- Final Success Message ---
	logger.Info(fmt.Sprintf("Success! Your new application '%s' is ready.", appName))
	logger.Info(fmt.Sprintf("You can view it at: http://%s", defaultDomain))
	logger.Info("To deploy your own code, add the git remote and push:")
	logger.Info(fmt.Sprintf("  git remote add mitte mitte@%s:%s", baseDomain, appName))
	logger.Info("  git push mitte main")
}

func runAppsDestroy(cmd *cobra.Command, args []string) {
	userInput := args[0]
	ctx := context.Background()

	appName, err := state.ResolveAppName(userInput)
	if err != nil {
		logger.Error(fmt.Sprintf("Error: %v", err))
		os.Exit(1)
	}

	// Define color functions
	redBold := color.New(color.FgRed, color.Bold)
	greenBold := color.New(color.FgGreen, color.Bold)
	yellowBold := color.New(color.FgYellow, color.Bold)
	whiteBold := color.New(color.FgWhite, color.Bold)
	cyan := color.New(color.FgCyan)
	blue := color.New(color.FgBlue)
	purple := color.New(color.FgMagenta)
	yellow := color.New(color.FgYellow)

	// --- 1. SAFETY: User Confirmation ---
	redBold.Print("⚠️  WARNING: This action is irreversible and will destroy the application '")
	yellowBold.Print(appName)
	redBold.Println("'.")
	redBold.Println("⚠️  This will remove the container, all built images, networking, and the git repository.")
	yellowBold.Print("💀 Please type '")
	whiteBold.Print(appName)
	yellowBold.Print("' to confirm: ")

	reader := bufio.NewReader(os.Stdin)
	confirmation, _ := reader.ReadString('\n')
	confirmation = strings.TrimSpace(confirmation)

	if confirmation != appName {
		redBold.Println("❌ Confirmation failed. Aborting destruction.")
		os.Exit(1)
	}

	fmt.Println()
	redBold.Println("💥 Starting destruction process...")

	// --- 2. Remove Routes ---
	cyan.Println("🔗 Removing network routes...")
	if err := router.SetAppRoutes(appName, []string{}, ""); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not remove Caddy routes: %v\n", err)
	}

	// --- 3. Stop and Remove Container ---
	blue.Println("🐳 Stopping and removing container...")
	app, err := state.Load(appName)
	containerName := ""
	if err == nil && app != nil {
		containerName = app.ContainerName
	}
	if err := deployer.StopAndRemoveContainer(ctx, appName, containerName); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not stop/remove container: %v\n", err)
	}

	// --- 4. Prune Images ---
	purple.Println("🖼️ Pruning Docker images...")
	if err := deployer.PruneAppImages(ctx, appName); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not prune images: %v\n", err)
	}

	// --- 5. Delete Git Repository ---
	repoPath := filepath.Join("/var/lib/mitte/repos", appName+".git")
	cyan.Printf("📂 Deleting git repository at %s...\n", repoPath)
	if err := exec.Command("sudo", "rm", "-rf", repoPath).Run(); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not delete git repository: %v\n", err)
	}

	// --- 6. Delete State File ---
	statePath := filepath.Join("/var/lib/mitte/apps", appName+".json")
	cyan.Printf("🗑️ Deleting state file at %s...\n", statePath)
	if err := exec.Command("sudo", "rm", "-f", statePath).Run(); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not delete state file: %v\n", err)
	}

	fmt.Println()
	greenBold.Print("✅ Success! Application '")
	whiteBold.Print(appName)
	greenBold.Println("' has been destroyed.")
}

func runAppsBuild(cmd *cobra.Command, args []string) {
	appName := args[0]
	ctx := context.Background()

	logger.Info(fmt.Sprintf("-----> Building app '%s'...", appName))

	// 1. Load app state
	app, err := state.Load(appName)
	if err != nil {
		logger.Error("Could not load app state", "err", err)
		os.Exit(1)
	}
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist. Create it first with 'mitte apps create %s'", appName, appName))
		os.Exit(1)
	}

	// Enable the app if it was disabled
	if app.Disabled {
		logger.Info("-----> App was disabled, enabling it for deployment...")
		app.Disabled = false
		if err := app.Save(); err != nil {
			logger.Warn("Could not save app state", "err", err)
		}
	}

	// Enable the app if it was disabled
	if app.Disabled {
		logger.Info("-----> App was disabled, enabling it for build...")
		app.Disabled = false
		if err := app.Save(); err != nil {
			logger.Warn("Could not save app state", "err", err)
		}
	}

	// 2. Check if git repo exists
	repoPath := filepath.Join("/var/lib/mitte/repos", appName+".git")
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		logger.Error(fmt.Sprintf("Git repository for '%s' does not exist. Please push code first.", appName))
		os.Exit(1)
	}

	// 3. Add the repository to git safe directories to avoid ownership issues
	logger.Info("-----> Configuring git safe directory...")
	safeDirCmd := exec.Command("git", "config", "--global", "--add", "safe.directory", repoPath)
	safeDirOutput, err := safeDirCmd.CombinedOutput()
	if err != nil {
		logger.Warn(fmt.Sprintf("Failed to add safe directory: %v\n%s", err, string(safeDirOutput)))
		// Continue anyway, as this might work on some systems
	}

	// 4. Determine the branch to deploy
	getBranchCmd := exec.Command("sh", "-c", "git for-each-ref --sort=-committerdate refs/heads/ --format='%(refname:short)' | head -n 1")
	getBranchCmd.Dir = repoPath
	branchOutput, err := getBranchCmd.CombinedOutput()
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to determine deployment branch: %v\n%s", err, string(branchOutput)))
		os.Exit(1)
	}
	branchToDeploy := strings.TrimSpace(string(branchOutput))
	if branchToDeploy == "" {
		logger.Error("No branch found to deploy.")
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("-----> Using branch '%s'", branchToDeploy))

	// 5. Create temporary build directory
	buildDir, err := os.MkdirTemp("", "mitte-build-"+appName+"-")
	if err != nil {
		logger.Error("Failed to create temporary build directory", "err", err)
		os.Exit(1)
	}
	defer os.RemoveAll(buildDir)

	// 6. Check out the code
	logger.Info("-----> Checking out latest code...")
	archiveCmdString := fmt.Sprintf("git archive %s | tar -x -C %s", branchToDeploy, buildDir)
	archiveCmd := exec.Command("sh", "-c", archiveCmdString)
	archiveCmd.Dir = repoPath
	archiveOutput, err := archiveCmd.CombinedOutput()
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to archive code: %v\n%s", err, string(archiveOutput)))
		os.Exit(1)
	}

	// 7. Build the image
	logger.Info("-----> Building Docker image...")
	imageTag, err := builder.BuildImage(ctx, appName, buildDir, repoPath, branchToDeploy, app.EnvVars)
	if err != nil {
		logger.Error("Build failed", "err", err)
		os.Exit(1)
	}

	// 8. Deploy the new image
	logger.Info("-----> Deploying new image...")
	deployResult, err := deployer.Deploy(ctx, appName, imageTag, app.Volumes, app.Ports, app.ContainerName, app.Command, app.User)
	if err != nil {
		logger.Error("Deployment failed", "err", err)
		os.Exit(1)
	}

	// 9. Save host port to app state
	app.HostPort = deployResult.HostPort
	if err := app.Save(); err != nil {
		logger.Warn("Failed to save host port", "err", err)
	}

	// 10. Update routes
	logger.Info("-----> Updating routes...")
	authEnabled := app.Auth != nil && app.Auth.Enabled
	authPolicy := ""
	if authEnabled {
		authPolicy = app.Auth.Policy
	}
	if err := router.SetAppRoutesWithAuth(appName, app.Domains, deployResult.HostPort, authEnabled, authPolicy); err != nil {
		logger.Error("Failed to update routes", "err", err)
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Success! App '%s' rebuilt and redeployed.", appName))
	logger.Info(fmt.Sprintf("Container ID: %s", deployResult.ContainerID[:12]))
}

func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%d seconds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func runAppsSetImage(cmd *cobra.Command, args []string) {
	appName := args[0]
	imageName := args[1]

	// Validate image name format (basic validation)
	if imageName == "" {
		logger.Error("Image name cannot be empty")
		os.Exit(1)
	}

	// Basic validation - should contain at least one slash or be a simple name
	if !strings.Contains(imageName, "/") && !strings.Contains(imageName, ":") {
		// Allow simple names like "nginx" but warn about best practices
		logger.Warn(fmt.Sprintf("Using simple image name '%s'. Consider using a fully qualified name like '%s:latest'", imageName, imageName))
	}

	logger.Info(fmt.Sprintf("Setting image for app '%s' to '%s'... ", appName, imageName))

	// Load the app
	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	// Check if app exists (has domains)
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist. Create it first with 'mitte apps create %s'", appName, appName))
		os.Exit(1)
	}

	// Set the image
	app.Image = imageName

	// Save the app
	if err := app.Save(); err != nil {
		logger.Error("Could not save app configuration", "err", err)
		os.Exit(1)
	}

	logger.Info("done.")

	logger.Info(fmt.Sprintf("Success! App '%s' is now configured to use image '%s'", appName, imageName))
	logger.Info(fmt.Sprintf("To deploy the app, run: mitte apps deploy-image %s", appName))
}

func runAppsSetVolumes(cmd *cobra.Command, args []string) {
	appName := args[0]
	volumeArgs := args[1:]

	noRestart, _ := cmd.Flags().GetBool("no-restart")

	// Validate that we have at least one volume
	if len(volumeArgs) == 0 {
		logger.Error("At least one volume mapping is required")
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Setting volumes for app '%s'... ", appName))

	// Load the app
	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	// Check if app exists (has domains)
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist. Create it first with 'mitte apps create %s'", appName, appName))
		os.Exit(1)
	}

	// Validate and parse volume mappings
	var volumes []string
	for _, volume := range volumeArgs {
		if volume == "" {
			continue // Skip empty volumes
		}

		// Validate volume format (should contain at least one colon)
		if !strings.Contains(volume, ":") {
			logger.Error(fmt.Sprintf("Invalid volume format '%s'. Use format: host:container[:options]", volume))
			logger.Info("Examples: /host/path:/container/path, /host/path:/container/path:ro")
			os.Exit(1)
		}

		// Basic validation - should have 2 or 3 parts when split by colon
		parts := strings.Split(volume, ":")
		if len(parts) < 2 || len(parts) > 3 {
			logger.Error(fmt.Sprintf("Invalid volume format '%s'. Expected 2 or 3 parts separated by ':'", volume))
			os.Exit(1)
		}

		// Check for empty host or container paths
		if parts[0] == "" || parts[1] == "" {
			logger.Error(fmt.Sprintf("Empty host or container path in volume '%s'", volume))
			os.Exit(1)
		}

		volumes = append(volumes, volume)
	}

	// Set the volumes
	app.Volumes = volumes

	// Save the app
	if err := app.Save(); err != nil {
		logger.Error("Could not save app configuration", "err", err)
		os.Exit(1)
	}

	logger.Info("done.")

	logger.Info(fmt.Sprintf("Success! App '%s' is now configured with %d volume mount(s)", appName, len(volumes)))
	for i, volume := range volumes {
		logger.Info(fmt.Sprintf("  %d. %s", i+1, volume))
	}

	if !noRestart {
		logger.Info("Redeploying application to apply changes...")
		if err := actions.RestartApp(appName); err != nil {
			logger.Error("Error redeploying application", "err", err)
			os.Exit(1)
		}
		logger.Info(fmt.Sprintf("Volumes updated for '%s'. The application is now restarting.", appName))
	} else {
		logger.Info(fmt.Sprintf("To deploy the app, run: mitte apps deploy-image %s", appName))
	}
}

func runAppsUnsetVolumes(cmd *cobra.Command, args []string) {
	if len(args) < 1 {
		cmd.Help()
		os.Exit(1)
	}
	appName := args[0]
	volumeArgs := args[1:]

	removeAll, _ := cmd.Flags().GetBool("all")
	noRestart, _ := cmd.Flags().GetBool("no-restart")

	if len(volumeArgs) == 0 && !removeAll {
		logger.Error("At least one volume mapping is required, or use --all")
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Unsetting volumes for app '%s'... ", appName))

	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist.", appName))
		os.Exit(1)
	}

	if removeAll {
		app.Volumes = []string{}
	} else {
		var newVolumes []string
		for _, existingVolume := range app.Volumes {
			if slices.Contains(volumeArgs, existingVolume) {
				continue
			}
			newVolumes = append(newVolumes, existingVolume)
		}
		app.Volumes = newVolumes
	}

	if err := app.Save(); err != nil {
		logger.Error("Could not save app configuration", "err", err)
		os.Exit(1)
	}

	logger.Info("done.")

	if !noRestart {
		logger.Info("Redeploying application to apply changes...")
		if err := actions.RestartApp(appName); err != nil {
			logger.Error("Error redeploying application", "err", err)
			os.Exit(1)
		}
		logger.Info(fmt.Sprintf("Volumes updated for '%s'. The application is now restarting.", appName))
	} else {
		logger.Info(fmt.Sprintf("To deploy the app with the updated volumes, run: mitte apps deploy-image %s", appName))
	}
}

func runAppsSetPorts(cmd *cobra.Command, args []string) {
	appName := args[0]
	portArgs := args[1:]

	noRestart, _ := cmd.Flags().GetBool("no-restart")

	// Validate that we have at least one port
	if len(portArgs) == 0 {
		logger.Error("At least one port mapping is required")
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Setting ports for app '%s'... ", appName))

	// Load the app
	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	// Check if app exists (has domains)
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist. Create it first with 'mitte apps create %s'", appName, appName))
		os.Exit(1)
	}

	// Validate and parse port mappings
	var ports []string
	for _, port := range portArgs {
		if port == "" {
			continue // Skip empty ports
		}

		// Validate port format (should contain exactly one colon)
		if !strings.Contains(port, ":") {
			logger.Error(fmt.Sprintf("Invalid port format '%s'. Use format: host:container", port))
			logger.Info("Examples: 8080:80, 9200:9200, 3000:8080")
			os.Exit(1)
		}

		// Basic validation - should have exactly 2 parts when split by colon
		parts := strings.Split(port, ":")
		if len(parts) != 2 {
			logger.Error(fmt.Sprintf("Invalid port format '%s'. Expected exactly 2 parts separated by ':'", port))
			os.Exit(1)
		}

		// Check for empty host or container ports
		if parts[0] == "" || parts[1] == "" {
			logger.Error(fmt.Sprintf("Empty host or container port in mapping '%s'", port))
			os.Exit(1)
		}

		// Basic validation for numeric ports (optional, but helpful)
		// We don't enforce this strictly since Docker allows named ports
		if len(parts[0]) > 0 && len(parts[1]) > 0 {
			// Could add more sophisticated port validation here if needed
		}

		ports = append(ports, port)
	}

	// Set the ports
	app.Ports = ports

	// Save the app
	if err := app.Save(); err != nil {
		logger.Error("Could not save app configuration", "err", err)
		os.Exit(1)
	}

	logger.Info("done.")

	logger.Info(fmt.Sprintf("Success! App '%s' is now configured with %d port mapping(s)", appName, len(ports)))
	for i, port := range ports {
		logger.Info(fmt.Sprintf("  %d. %s", i+1, port))
	}

	if !noRestart {
		logger.Info("Redeploying application to apply changes...")
		if err := actions.RestartApp(appName); err != nil {
			logger.Error("Error redeploying application", "err", err)
			os.Exit(1)
		}
		logger.Info(fmt.Sprintf("Ports updated for '%s'. The application is now restarting.", appName))
	} else {
		logger.Info(fmt.Sprintf("To deploy the app, run: mitte apps deploy-image %s", appName))
	}
}

func runAppsUnsetPorts(cmd *cobra.Command, args []string) {
	if len(args) < 1 {
		cmd.Help()
		os.Exit(1)
	}
	appName := args[0]
	portArgs := args[1:]

	removeAll, _ := cmd.Flags().GetBool("all")
	noRestart, _ := cmd.Flags().GetBool("no-restart")

	if len(portArgs) == 0 && !removeAll {
		logger.Error("At least one port mapping is required, or use --all")
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Unsetting ports for app '%s'... ", appName))

	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist.", appName))
		os.Exit(1)
	}

	if removeAll {
		app.Ports = []string{}
	} else {
		var newPorts []string
		for _, existingPort := range app.Ports {
			if slices.Contains(portArgs, existingPort) {
				continue
			}
			newPorts = append(newPorts, existingPort)
		}
		app.Ports = newPorts
	}

	if err := app.Save(); err != nil {
		logger.Error("Could not save app configuration", "err", err)
		os.Exit(1)
	}

	logger.Info("done.")

	if !noRestart {
		logger.Info("Redeploying application to apply changes...")
		if err := actions.RestartApp(appName); err != nil {
			logger.Error("Error redeploying application", "err", err)
			os.Exit(1)
		}
		logger.Info(fmt.Sprintf("Ports updated for '%s'. The application is now restarting.", appName))
	} else {
		logger.Info(fmt.Sprintf("To deploy the app with the updated ports, run: mitte apps deploy-image %s", appName))
	}
}

func runAppsDeployImage(cmd *cobra.Command, args []string) {
	appName := args[0]
	ctx := context.Background()

	logger.Info(fmt.Sprintf("-----> Deploying app '%s' with pre-built image...", appName))

	// 1. Load app state
	app, err := state.Load(appName)
	if err != nil {
		logger.Error("Could not load app state", "err", err)
		os.Exit(1)
	}
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist. Create it first with 'mitte apps create %s'", appName, appName))
		os.Exit(1)
	}

	// 2. Check if image is configured
	if app.Image == "" {
		logger.Error(fmt.Sprintf("No image configured for app '%s'. Set an image first with 'mitte apps set-image %s <image>'", appName, appName))
		os.Exit(1)
	}

	// 3. Warn about environment variables for pre-built images
	if len(app.EnvVars) > 0 {
		logger.Warn("Environment variables will be applied at runtime, not during image build.")
		logger.Info("       If your app needs env vars during build, use git push with a Dockerfile instead.")
	}

	// 4. Pull the image if it's not available locally
	logger.Info(fmt.Sprintf("-----> Ensuring image '%s' is available...", app.Image))

	// 3. Pull the image if it's not available locally
	logger.Info(fmt.Sprintf("-----> Ensuring image '%s' is available...", app.Image))
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Error("Could not connect to Docker daemon", "err", err)
		os.Exit(1)
	}

	// Check if image exists locally
	_, _, err = cli.ImageInspectWithRaw(ctx, app.Image)
	if err != nil {
		// Image doesn't exist locally, pull it
		logger.Info(fmt.Sprintf("-----> Pulling image '%s'...", app.Image))

		// Create auth resolver for private registries
		authResolver := registry.DefaultResolver()

		// Get auth config for this image
		authConfig, err := authResolver.GetAuthConfigForImage(app.Image)
		if err != nil {
			logger.Warn("Could not get registry auth", "err", err)
		}

		pullOpts := image.PullOptions{}

		// If we have auth config, encode it for the pull
		if authConfig != nil && (authConfig.Username != "" || authConfig.Password != "") {
			authHeader, err := registry.EncodeRegistryAuth(authConfig)
			if err == nil {
				pullOpts.RegistryAuth = authHeader
			}
		}

		reader, err := cli.ImagePull(ctx, app.Image, pullOpts)
		if err != nil {
			logger.Error(fmt.Sprintf("Could not pull image '%s'", app.Image), "err", err)
			os.Exit(1)
		}
		io.Copy(io.Discard, reader) // Wait for the pull to complete but discard the noisy output
		reader.Close()
		logger.Info("-----> Image pulled successfully")
	} else {
		logger.Info("-----> Image already available locally")
	}
	cli.Close()

	// 4. Deploy the image
	logger.Info("-----> Deploying container...")
	deployResult, err := deployer.Deploy(ctx, appName, app.Image, app.Volumes, app.Ports, app.ContainerName, app.Command, app.User)
	if err != nil {
		logger.Error("Deployment failed", "err", err)
		os.Exit(1)
	}

	// 4.5. Save host port to app state
	app.HostPort = deployResult.HostPort
	if err := app.Save(); err != nil {
		logger.Warn("Failed to save host port", "err", err)
	}

	// 5. Update routes
	logger.Info("-----> Updating routes...")
	authEnabled := app.Auth != nil && app.Auth.Enabled
	authPolicy := ""
	if authEnabled {
		authPolicy = app.Auth.Policy
	}
	if err := router.SetAppRoutesWithAuth(appName, app.Domains, deployResult.HostPort, authEnabled, authPolicy); err != nil {
		logger.Error("Failed to update routes", "err", err)
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Success! App '%s' deployed successfully.", appName))
	logger.Info(fmt.Sprintf("Container ID: %s", deployResult.ContainerID[:12]))
	logger.Info(fmt.Sprintf("Host Port: %s", deployResult.HostPort))
	if len(app.Domains) > 0 {
		logger.Info(fmt.Sprintf("URL: http://%s", app.Domains[0]))
	}
}

func runAppsSetBuildpack(cmd *cobra.Command, args []string) {
	appName := args[0]
	buildpackID := args[1]

	// Validate buildpack ID
	if buildpackID == "" {
		logger.Error("Buildpack ID cannot be empty")
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Setting buildpack for app '%s' to '%s'... ", appName, buildpackID))

	// Load the app
	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	// Check if app exists (has domains)
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist. Create it first with 'mitte apps create %s'", appName, appName))
		os.Exit(1)
	}

	// Set the buildpack
	app.Buildpack = buildpackID

	// Save the app
	if err := app.Save(); err != nil {
		logger.Error("Could not save app configuration", "err", err)
		os.Exit(1)
	}

	logger.Info("done.")

	logger.Info(fmt.Sprintf("Success! App '%s' is now configured to use buildpack '%s'", appName, buildpackID))
	logger.Info("The next git push will use this buildpack for building.")
}

func runAppsDetectBuildpack(cmd *cobra.Command, args []string) {
	appName := args[0]

	logger.Info(fmt.Sprintf("Detecting buildpack for app '%s'...", appName))

	// Load the app
	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	// Check if app exists (has domains)
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist. Create it first with 'mitte apps create %s'", appName, appName))
		os.Exit(1)
	}

	// For detection, we need to check out the latest code
	// This is similar to what we do in git_receive.go

	// Get the repo path
	repoPath := filepath.Join("/var/lib/mitte/repos", appName+".git")

	// Check if repo exists
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		logger.Error(fmt.Sprintf("Repository for app '%s' does not exist. Push code first with 'git push mitte main'", appName))
		os.Exit(1)
	}

	// Determine the branch to check
	getBranchCmd := exec.Command("sh", "-c", "git for-each-ref --sort=-committerdate refs/heads/ --format='%(refname:short)' | head -n 1")
	getBranchCmd.Dir = repoPath
	branchOutput, err := getBranchCmd.CombinedOutput()
	if err != nil {
		logger.Error(fmt.Sprintf("Could not determine branch: %v\n%s", err, string(branchOutput)))
		os.Exit(1)
	}
	branchName := strings.TrimSpace(string(branchOutput))

	if branchName == "" {
		logger.Error("No branches found in repository")
		os.Exit(1)
	}

	// Create temporary directory to check out code
	buildDir, err := os.MkdirTemp("", "mitte-detect-"+appName+"-")
	if err != nil {
		logger.Error("Failed to create temporary directory", "err", err)
		os.Exit(1)
	}
	defer os.RemoveAll(buildDir)

	// Archive and extract the code
	archiveCmdString := fmt.Sprintf("git archive %s | tar -x -C %s", branchName, buildDir)
	archiveCmd := exec.Command("sh", "-c", archiveCmdString)
	archiveCmd.Dir = repoPath
	archiveOutput, err := archiveCmd.CombinedOutput()
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to extract code: %v\n%s", err, string(archiveOutput)))
		os.Exit(1)
	}

	// Detect buildpack
	buildpackConfig, err := builder.DetectBuildpack(buildDir)
	if err != nil {
		logger.Error(fmt.Sprintf("No buildpack detected for app '%s'", appName), "err", err)
		logger.Info("To configure a buildpack manually, run:")
		logger.Info(fmt.Sprintf("  mitte apps set-buildpack %s <buildpack-id>", appName))
		logger.Info("Common buildpack IDs:")
		logger.Info("  - paketobuildpacks/nodejs")
		logger.Info("  - paketobuildpacks/python")
		logger.Info("  - paketobuildpacks/go")
		logger.Info("  - paketobuildpacks/java")
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Detected buildpack for app '%s': %s", appName, buildpackConfig.BuildpackID))
	logger.Info(fmt.Sprintf("Buildpack URI: %s", buildpackConfig.BuildpackURI))
	logger.Info("To use this buildpack, run:")
	logger.Info(fmt.Sprintf("  mitte apps set-buildpack %s %s", appName, buildpackConfig.BuildpackID))
}

func runAppsSetCommand(cmd *cobra.Command, args []string) {
	appName := args[0]
	commandArgs := args[1:]

	logger.Info(fmt.Sprintf("Setting command for app '%s' to '%s'... ", appName, strings.Join(commandArgs, " ")))

	// Load the app
	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	// Check if app exists (has domains)
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist. Create it first with 'mitte apps create %s'", appName, appName))
		os.Exit(1)
	}

	// Set the command
	app.Command = commandArgs

	// Save the app
	if err := app.Save(); err != nil {
		logger.Error("Could not save app configuration", "err", err)
		os.Exit(1)
	}

	logger.Info("done.")

	logger.Info(fmt.Sprintf("Success! App '%s' is now configured to use command: %s", appName, strings.Join(commandArgs, " ")))
	logger.Info(fmt.Sprintf("To deploy the app, run: mitte apps deploy-image %s", appName))
}

func runAppsSetUser(cmd *cobra.Command, args []string) {
	appName := args[0]
	user := args[1]

	logger.Info(fmt.Sprintf("Setting user for app '%s' to '%s'... ", appName, user))

	// Load the app
	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	// Check if app exists (has domains)
	if len(app.Domains) == 0 {
		logger.Error(fmt.Sprintf("App '%s' does not exist. Create it first with 'mitte apps create %s'", appName, appName))
		os.Exit(1)
	}

	// Set the user
	app.User = user

	// Save the app
	if err := app.Save(); err != nil {
		logger.Error("Could not save app configuration", "err", err)
		os.Exit(1)
	}

	logger.Info("done.")

	logger.Info(fmt.Sprintf("Success! App '%s' is now configured to run as user: %s", appName, user))
	logger.Info(fmt.Sprintf("To deploy the app, run: mitte apps deploy-image %s", appName))
}

func runAppsEnable(cmd *cobra.Command, args []string) {
	appName := args[0]
	ctx := context.Background()

	app, err := state.Load(appName)
	if err != nil {
		logger.Error("Could not load app state", "err", err)
		os.Exit(1)
	}

	if !app.Disabled {
		logger.Info(fmt.Sprintf("App '%s' is already enabled.", appName))
		return
	}

	app.Disabled = false
	if err := app.Save(); err != nil {
		logger.Error("Could not save app state", "err", err)
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("App '%s' enabled. Starting deployment...", appName))

	// Determine what to deploy
	imageToDeploy := app.Image
	if imageToDeploy == "" {
		// Try to find the latest built image
		latestImage, err := deployer.GetLatestImageForApp(ctx, appName)
		if err == nil {
			imageToDeploy = latestImage
		} else {
			// Fallback to placeholder if nothing else
			imageToDeploy = placeholderImage
		}
	}

	// Deploy
	deployResult, err := deployer.Deploy(ctx, appName, imageToDeploy, app.Volumes, app.Ports, app.ContainerName, app.Command, app.User)
	if err != nil {
		logger.Error("Deployment failed", "err", err)
		os.Exit(1)
	}

	app.HostPort = deployResult.HostPort
	app.Save()

	// Update routes
	authEnabled := app.Auth != nil && app.Auth.Enabled
	authPolicy := ""
	if authEnabled {
		authPolicy = app.Auth.Policy
	}
	if err := router.SetAppRoutesWithAuth(appName, app.Domains, deployResult.HostPort, authEnabled, authPolicy); err != nil {
		logger.Error("Failed to update routes", "err", err)
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Success! App '%s' is now enabled and running.", appName))
}

func runAppsDisable(cmd *cobra.Command, args []string) {
	appName := args[0]
	ctx := context.Background()

	app, err := state.Load(appName)
	if err != nil {
		logger.Error("Could not load app state", "err", err)
		os.Exit(1)
	}

	if app.Disabled {
		logger.Info(fmt.Sprintf("App '%s' is already disabled.", appName))
		return
	}

	app.Disabled = true
	if err := app.Save(); err != nil {
		logger.Error("Could not save app state", "err", err)
		os.Exit(1)
	}

	// Stop and remove container
	if err := deployer.StopAndRemoveContainer(ctx, appName, app.ContainerName); err != nil {
		logger.Warn("Could not stop container", "err", err)
	}

	// Remove routes
	if err := router.DeleteRouteFile(appName); err != nil {
		logger.Warn("Could not remove routes", "err", err)
	}

	logger.Info(fmt.Sprintf("Success! App '%s' is now disabled.", appName))
}

func runAppsListVolumes(cmd *cobra.Command, args []string) {
	appName, err := state.ResolveAppName(args[0])
	if err != nil {
		logger.Error(fmt.Sprintf("Error: %v", err))
		os.Exit(1)
	}

	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	if len(app.Volumes) == 0 {
		logger.Info(fmt.Sprintf("No volumes mounted for app '%s'.", appName))
		return
	}

	logger.Info(fmt.Sprintf("Volume mounts for app '%s':", appName))
	for i, volume := range app.Volumes {
		logger.Info(fmt.Sprintf("  %d. %s", i+1, volume))
	}
}

func runAppsListPorts(cmd *cobra.Command, args []string) {
	appName, err := state.ResolveAppName(args[0])
	if err != nil {
		logger.Error(fmt.Sprintf("Error: %v", err))
		os.Exit(1)
	}

	app, err := state.Load(appName)
	if err != nil {
		logger.Error(fmt.Sprintf("Could not load app '%s'", appName), "err", err)
		os.Exit(1)
	}

	if len(app.Ports) == 0 {
		logger.Info(fmt.Sprintf("No port mappings for app '%s'.", appName))
		return
	}

	logger.Info(fmt.Sprintf("Port mappings for app '%s':", appName))
	for i, port := range app.Ports {
		logger.Info(fmt.Sprintf("  %d. %s", i+1, port))
	}
}
