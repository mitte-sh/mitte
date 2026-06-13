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

	"github.com/mitte-sh/mitte/pkg/builder"
	"github.com/mitte-sh/mitte/pkg/config"
	"github.com/mitte-sh/mitte/pkg/deployer"
	"github.com/mitte-sh/mitte/pkg/logger"
	"github.com/mitte-sh/mitte/pkg/router"
	"github.com/mitte-sh/mitte/pkg/state"
)

// gitReceiveCmd represents the command triggered by SSH for a git push.
var gitReceiveCmd = &cobra.Command{
	Use:    "git-receive",
	Short:  "Handles git pushes over SSH (internal command)",
	Hidden: true,
	Run:    runGitReceive,
}

func runGitReceive(cmd *cobra.Command, args []string) {
	var appState *state.App

	// --- 1. Identify the App from the Environment ---
	originalCmd := os.Getenv("SSH_ORIGINAL_COMMAND")
	if originalCmd == "" {
		logger.Error("git-receive command must be run via SSH.")
		os.Exit(1)
	}

	// This regex handles app names with or without the '.git' suffix.
	re := regexp.MustCompile(`'([^']+)(?:\.git)?'`)
	matches := re.FindStringSubmatch(originalCmd)
	if len(matches) < 2 {
		logger.Error(fmt.Sprintf("could not parse app name from SSH_ORIGINAL_COMMAND: %s", originalCmd))
		os.Exit(1)
	}
	appName := matches[1]
	logger.Info(fmt.Sprintf("-----> Mitte received push for app: %s", appName))

	// --- 2. Load App State Early ---
	// Load app state to check if pre-built image is configured
	appState, err := state.Load(appName)
	if err != nil {
		// If app doesn't exist yet, create with empty state
		appState = &state.App{
			AppName:       appName,
			Domains:       []string{},
			EnvVars:       make(map[string]string),
			Image:         "",
			Volumes:       []string{},
			Ports:         []string{},
			ContainerName: "",
			HostPort:      "",
		}
	}

	// Check if app has pre-built image configured
	hasPrebuiltImage := appState.Image != ""
	if hasPrebuiltImage {
		logger.Info(fmt.Sprintf("-----> Pre-built image configured: %s", appState.Image))
		logger.Info("-----> Skipping build process, will deploy pre-built image")
	} else {
		logger.Info("-----> No pre-built image configured, will build from source")
	}

	// --- 3. Ensure Repository Exists ---
	// Define the path for the bare git repository for this app.
	repoPath := filepath.Join("/var/lib/mitte/repos", appName+".git")

	// Check if this is the first push for this app.
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		logger.Info(fmt.Sprintf("-----> First push for '%s', creating new bare repository.", appName))
		if err := os.MkdirAll(repoPath, 0755); err != nil {
			logger.Error(fmt.Sprintf("FATAL: failed to create repository directory: %v", err))
			os.Exit(1)
		}

		// Initialize bare repo
		gitInitCmd := exec.Command("git", "init", "--bare")
		gitInitCmd.Dir = repoPath
		if err := gitInitCmd.Run(); err != nil {
			logger.Error(fmt.Sprintf("FATAL: failed to initialize bare repository: %v", err))
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("-----> Setting repository ownership for user '%s'...", mitteSystemUser))
		chownCmd := exec.Command("sudo", "chown", "-R", mitteSystemUser+":"+mitteSystemUser, repoPath)
		if output, err := chownCmd.CombinedOutput(); err != nil {
			// If this fails, the deployment cannot succeed. We must exit.
			logger.Error(fmt.Sprintf("FATAL: failed to set ownership on repository: %v", err))
			logger.Error("This usually means the 'mitte' user needs passwordless sudo access for 'chown'.")
			logger.Error(fmt.Sprintf("Output: %s", string(output)))
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
		logger.Error("git-receive-pack appears to have failed.")
		os.Exit(1)
	}

	// --- 4. Check Out the Fresh Code (skip for pre-built images) ---
	var buildDir string
	var branchToDeploy string

	if !hasPrebuiltImage {
		// Create a temporary directory to check out the source code for building.
		buildDir, err = os.MkdirTemp("", "mitte-build-"+appName+"-")
		if err != nil {
			logger.Error(fmt.Sprintf("failed to create temporary build directory: %v", err))
			os.Exit(1)
		}
		defer os.RemoveAll(buildDir) // Clean up the build directory when we're done.

		// Determine the branch to deploy by finding the most recently updated one.
		getBranchCmd := exec.Command("sh", "-c", "git for-each-ref --sort=-committerdate refs/heads/ --format='%(refname:short)' | head -n 1")
		getBranchCmd.Dir = repoPath
		branchOutput, err := getBranchCmd.CombinedOutput()
		if err != nil {
			logger.Error(fmt.Sprintf("failed to determine deployment branch: %v\n%s", err, string(branchOutput)))
			os.Exit(1)
		}
		branchToDeploy = strings.TrimSpace(string(branchOutput))

		// If the push contained no branches (e.g., only tags), there's nothing to deploy.
		if branchToDeploy == "" {
			logger.Info("-----> No branch to deploy. Push a branch to trigger a deployment.")
			os.Exit(0) // Exit gracefully, as this is not an error condition.
		}

		logger.Info(fmt.Sprintf("-----> Archiving branch '%s' for deployment...", branchToDeploy))

		// This command creates a tar archive of the detected branch and pipes it
		// to tar, which extracts it into our build directory.
		archiveCmdString := fmt.Sprintf("git archive %s | tar -x -C %s", branchToDeploy, buildDir)
		archiveCmd := exec.Command("sh", "-c", archiveCmdString)

		// We execute this command from within the bare repository's directory.
		archiveCmd.Dir = repoPath

		archiveOutput, err := archiveCmd.CombinedOutput()
		if err != nil {
			logger.Error(fmt.Sprintf("failed to archive and extract code: %v", err))
			logger.Error(string(archiveOutput)) // Print the detailed error from git/tar
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("-----> Code extracted to %s", buildDir))
	} else {
		logger.Info("-----> Skipping code extraction (using pre-built image)")
	}

	// --- 5. Build or Use Pre-built Image ---
	var imageTag string

	if hasPrebuiltImage {
		// Use pre-built image directly
		logger.Info(fmt.Sprintf("-----> Using pre-built image: %s", appState.Image))
		imageTag = appState.Image
	} else {
		// Determine build method: buildpack or Dockerfile
		logger.Info("-----> Starting build process...")

		// Load app state to get environment variables and buildpack config
		appState, err = state.Load(appName)
		if err != nil {
			// If app doesn't exist yet, create with empty env vars
			appState = &state.App{
				AppName:      appName,
				EnvVars:      make(map[string]string),
				BuildpackEnv: make(map[string]string),
				HostPort:     "",
			}
		}

		// Check build method priority:
		// 1. Explicitly configured buildpack
		// 2. Dockerfile (traditional)
		// 3. Auto-detected buildpack
		if appState.Buildpack != "" {
			// Use configured buildpack
			logger.Info(fmt.Sprintf("-----> Using configured buildpack: %s", appState.Buildpack))

			buildpackConfig := &builder.BuildpackConfig{
				BuildpackID:  appState.Buildpack,
				BuildpackURI: appState.Buildpack, // For now, assume it's a URI
				EnvVars:      appState.BuildpackEnv,
			}

			imageTag, err = builder.BuildWithBuildpack(context.Background(), appName, buildDir, repoPath, branchToDeploy, buildpackConfig, appState.EnvVars)
			if err != nil {
				logger.Error(fmt.Sprintf("Buildpack build failed: %v", err))
				os.Exit(1)
			}
		} else if hasDockerfile(buildDir) {
			// Use traditional Dockerfile build
			logger.Info("-----> Building with Dockerfile...")
			imageTag, err = builder.BuildImage(context.Background(), appName, buildDir, repoPath, branchToDeploy, appState.EnvVars)
			if err != nil {
				logger.Error(fmt.Sprintf("Dockerfile build failed: %v", err))
				os.Exit(1)
			}
		} else {
			// Try to auto-detect buildpack
			logger.Info("-----> No Dockerfile found, attempting buildpack detection...")
			buildpackConfig, err := builder.DetectBuildpack(buildDir)
			if err != nil {
				logger.Error(fmt.Sprintf("No suitable build method found: %v", err))
				logger.Error("Please either:")
				logger.Error("- Add a Dockerfile to your repository, or")
				logger.Error(fmt.Sprintf("- Configure a buildpack with: mitte apps set-buildpack %s <buildpack-id>", appName))
				os.Exit(1)
			}

			logger.Info(fmt.Sprintf("-----> Auto-detected buildpack: %s", buildpackConfig.BuildpackID))
			imageTag, err = builder.BuildWithBuildpack(context.Background(), appName, buildDir, repoPath, branchToDeploy, buildpackConfig, appState.EnvVars)
			if err != nil {
				logger.Error(fmt.Sprintf("Buildpack build failed: %v", err))
				os.Exit(1)
			}
		}

		logger.Info(fmt.Sprintf("-----> imageTag: %s", imageTag))
	}

	// --- 6. Deploy the new image ---
	logger.Info("-----> Starting deployment...")
	deployResult, err := deployer.Deploy(context.Background(), appName, imageTag, appState.Volumes, appState.Ports, appState.ContainerName, appState.Command, appState.User)
	if err != nil {
		logger.Error(fmt.Sprintf("Deployment failed: %v", err))
		os.Exit(1)
	}

	// Create or update the application's state file.
	// Use the appState we loaded earlier to preserve pre-built image configuration
	app := appState

	// If this is the first deployment, the app won't have any domains assigned.
	// We create the default domain for it.
	if len(app.Domains) == 0 {
		baseDomain, err := config.GetBaseDomain()
		if err != nil {
			logger.Error("", "err", err)
			os.Exit(1)
		}
		defaultDomain := fmt.Sprintf("%s.%s", appName, baseDomain)
		logger.Info(fmt.Sprintf("-----> Assigning default domain: %s", defaultDomain))
		app.Domains = append(app.Domains, defaultDomain)
	}

	// Save host port to app state
	app.HostPort = deployResult.HostPort

	// Save the state to disk. This creates/updates the .json file.
	if err := app.Save(); err != nil {
		logger.Warn("Failed to save application state", "err", err)
		// We still continue, because the app is running. This is a critical warning, though.
	}

	// --- 8. Update the routing layer ---
	// Use SetAppRoutesWithAuth which handles both creating and updating routes
	authEnabled := app.Auth != nil && app.Auth.Enabled
	authPolicy := ""
	if authEnabled {
		authPolicy = app.Auth.Policy
	}
	logger.Info("-----> Updating routes")
	if err := router.SetAppRoutesWithAuth(appName, app.Domains, deployResult.HostPort, authEnabled, authPolicy); err != nil {
		logger.Error(fmt.Sprintf("Routing update failed: %v", err))
	}

	// --- Step 9: Final Success Message ---
	logger.Info("-----> ✨ Deployment complete! ✨")
	logger.Info(fmt.Sprintf("-----> App '%s' is live and running in container %s", appName, deployResult.ContainerID[:12]))
	// You should be able to access it at http://<appName>.<your_base_domain>
}

// hasDockerfile checks if a Dockerfile exists in the given directory
func hasDockerfile(dir string) bool {
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); err == nil {
		return true
	}

	// Also check for Dockerfile with different cases/extensions
	alternatives := []string{"dockerfile", "Dockerfile.dockerfile", "Containerfile"}
	for _, alt := range alternatives {
		if _, err := os.Stat(filepath.Join(dir, alt)); err == nil {
			return true
		}
	}

	return false
}

func init() {
	rootCmd.AddCommand(gitReceiveCmd)
}
