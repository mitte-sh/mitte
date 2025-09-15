package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mitteapp/mitteapp/pkg/builder"
	"github.com/mitteapp/mitteapp/pkg/config"
	"github.com/mitteapp/mitteapp/pkg/deployer"
	"github.com/mitteapp/mitteapp/pkg/router"
	"github.com/mitteapp/mitteapp/pkg/state"
)

// gitReceiveCmd represents the command triggered by SSH for a git push.
var gitReceiveCmd = &cobra.Command{
	Use:    "git-receive",
	Short:  "Handles git pushes over SSH (internal command)",
	Hidden: true,
	Run:    runGitReceive,
}

func runGitReceive(cmd *cobra.Command, args []string) {
	// --- 1. Identify the App from the Environment ---
	originalCmd := os.Getenv("SSH_ORIGINAL_COMMAND")
	if originalCmd == "" {
		fmt.Fprintln(os.Stderr, "Error: git-receive command must be run via SSH.")
		os.Exit(1)
	}

	// This regex handles app names with or without the '.git' suffix.
	re := regexp.MustCompile(`'([^']+)(?:\.git)?'`)
	matches := re.FindStringSubmatch(originalCmd)
	if len(matches) < 2 {
		fmt.Fprintf(os.Stderr, "Error: could not parse app name from SSH_ORIGINAL_COMMAND: %s\n", originalCmd)
		os.Exit(1)
	}
	appName := matches[1]
	fmt.Fprintf(os.Stderr, "-----> Mitte received push for app: %s\n", appName)

	// --- 2. Ensure Repository Exists ---
	// Define the path for the bare git repository for this app.
	repoPath := filepath.Join("/var/lib/mitte/repos", appName+".git")

	// Check if this is the first push for this app.
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "-----> First push for '%s', creating new bare repository.\n", appName)
		if err := os.MkdirAll(repoPath, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: failed to create repository directory: %v\n", err)
			os.Exit(1)
		}

		// Initialize bare repo
		gitInitCmd := exec.Command("git", "init", "--bare")
		gitInitCmd.Dir = repoPath
		if err := gitInitCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: failed to initialize bare repository: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "-----> Setting repository ownership for user '%s'...\n", mitteSystemUser)
		chownCmd := exec.Command("sudo", "chown", "-R", mitteSystemUser+":"+mitteSystemUser, repoPath)
		if output, err := chownCmd.CombinedOutput(); err != nil {
			// If this fails, the deployment cannot succeed. We must exit.
			fmt.Fprintf(os.Stderr, "FATAL: failed to set ownership on repository: %v\n", err)
			fmt.Fprintf(os.Stderr, "       This usually means the 'mitte' user needs passwordless sudo access for 'chown'.\n")
			fmt.Fprintf(os.Stderr, "       Output: %s\n", string(output))
			os.Exit(1)
		}
	}

	// --- 3. Handle the Git Data Transfer ---
	// Hand off control to git's standard receive-pack tool.
	// This will handle the protocol and write the new objects to our repo.
	// This command inherits stdin, stdout, and stderr to communicate with the user's git client.
	receivePackCmd := exec.Command("git-receive-pack", repoPath)
	receivePackCmd.Stdin = os.Stdin
	receivePackCmd.Stdout = os.Stdout
	receivePackCmd.Stderr = os.Stderr

	if err := receivePackCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: git-receive-pack appears to have failed.\n")
		os.Exit(1)
	}

	// --- 4. Check Out the Fresh Code ---
	// Create a temporary directory to check out the source code for building.
	buildDir, err := os.MkdirTemp("", "mitte-build-"+appName+"-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create temporary build directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(buildDir) // Clean up the build directory when we're done.

	// Determine the branch to deploy by finding the most recently updated one.
	getBranchCmd := exec.Command("sh", "-c", "git for-each-ref --sort=-committerdate refs/heads/ --format='%(refname:short)' | head -n 1")
	getBranchCmd.Dir = repoPath
	branchOutput, err := getBranchCmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to determine deployment branch: %v\n%s", err, string(branchOutput))
		os.Exit(1)
	}
	branchToDeploy := strings.TrimSpace(string(branchOutput))

	// If the push contained no branches (e.g., only tags), there's nothing to deploy.
	if branchToDeploy == "" {
		fmt.Fprintln(os.Stderr, "-----> No branch to deploy. Push a branch to trigger a deployment.")
		os.Exit(0) // Exit gracefully, as this is not an error condition.
	}

	fmt.Fprintf(os.Stderr, "-----> Archiving branch '%s' for deployment...\n", branchToDeploy)

	// This command creates a tar archive of the detected branch and pipes it
	// to tar, which extracts it into our build directory.
	archiveCmdString := fmt.Sprintf("git archive %s | tar -x -C %s", branchToDeploy, buildDir)
	archiveCmd := exec.Command("sh", "-c", archiveCmdString)

	// We execute this command from within the bare repository's directory.
	archiveCmd.Dir = repoPath

	archiveOutput, err := archiveCmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to archive and extract code: %v\n", err)
		fmt.Fprintln(os.Stderr, string(archiveOutput)) // Print the detailed error from git/tar
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "-----> Code extracted to %s\n", buildDir)

	// --- 5. Trigger the Build (Placeholder) ---
	fmt.Fprintln(os.Stderr, "-----> Starting build process...")

	// Load app state to get environment variables for build args
	appState, err := state.Load(appName)
	if err != nil {
		// If app doesn't exist yet, create with empty env vars
		appState = &state.App{AppName: appName, EnvVars: make(map[string]string)}
	}

	imageTag, err := builder.BuildImage(context.Background(), appName, buildDir, repoPath, branchToDeploy, appState.EnvVars)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n!! Building failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "-----> imageTag:", imageTag)

	// --- 6. Deploy the new image ---
	fmt.Fprintln(os.Stderr, "-----> Starting deployment...")
	deployResult, err := deployer.Deploy(context.Background(), appName, imageTag, appState.Volumes, appState.Ports, appState.ContainerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n!! Deployment failed: %v\n", err)
		os.Exit(1)
	}

	// Create or update the application's state file.
	app, err := state.Load(appName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n!! Warning: Could not load application state: %v\n", err)
		// We create a new empty app struct to proceed
		app = &state.App{AppName: appName, EnvVars: make(map[string]string)}
	}

	// If this is the first deployment, the app won't have any domains assigned.
	// We create the default domain for it.
	if len(app.Domains) == 0 {
		baseDomain, err := config.GetBaseDomain()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
			os.Exit(1)
		}
		defaultDomain := fmt.Sprintf("%s.%s", appName, baseDomain)
		fmt.Fprintf(os.Stderr, "-----> Assigning default domain: %s\n", defaultDomain)
		app.Domains = append(app.Domains, defaultDomain)
	}

	// Save the state to disk. This creates/updates the .json file.
	if err := app.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "\n!! Warning: Failed to save application state: %v\n", err)
		// We still continue, because the app is running. This is a critical warning, though.
	}

	// --- 8. Update the routing layer ---
	// Check if route exists
	exists, err := router.RouteExistsFile(appName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n!! Routing check failed: %v\n", err)
	}

	if exists {
		fmt.Fprintf(os.Stderr, "-----> Updating existing route\n")
		if err := router.CreateRouteFile(appName, deployResult.HostPort); err != nil {
			fmt.Fprintf(os.Stderr, "\n!! Routing update failed: %v\n", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "-----> Creating new route\n")
		if err := router.CreateRouteFile(appName, deployResult.HostPort); err != nil {
			fmt.Fprintf(os.Stderr, "\n!! Routing creation failed: %v\n", err)
		}
	}

	// --- Step 9: Final Success Message ---
	fmt.Fprintln(os.Stderr, "-----> ✨ Deployment complete! ✨")
	fmt.Fprintf(os.Stderr, "-----> App '%s' is live and running in container %s\n", appName, deployResult.ContainerID[:12])
	// You should be able to access it at http://<appName>.<your_base_domain>
}

func init() {
	rootCmd.AddCommand(gitReceiveCmd)
}
