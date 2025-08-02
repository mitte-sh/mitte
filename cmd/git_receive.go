package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"
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
			fmt.Fprintf(os.Stderr, "Error: failed to create repository directory: %v\n", err)
			os.Exit(1)
		}
		// Initialize a bare git repository
		gitInitCmd := exec.Command("git", "init", "--bare")
		gitInitCmd.Dir = repoPath
		if err := gitInitCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to initialize bare repository: %v\n", err)
			os.Exit(1)
		}

		// The `mitte` system user must own the repository.
		chownCmd := exec.Command("chown", "-R", mitteSystemUser+":"+mitteSystemUser, repoPath)
		if err := chownCmd.Run(); err != nil {
			// This might fail if the current user doesn't have permissions to chown.
			// The git-receive process should run as the 'mitte' user in a perfect setup,
			// but for now we proceed assuming root/sudo context.
			fmt.Fprintf(os.Stderr, "Warning: failed to chown repository: %v. This may cause issues later.\n", err)
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

	// IMPORTANT: For now, we assume the user is pushing the 'main' branch.
	// A future improvement would be to dynamically detect the default branch.
	branchToDeploy := "main"
	fmt.Fprintf(os.Stderr, "-----> Archiving branch '%s' for deployment...\n", branchToDeploy)

	// This is the new, robust command. It creates a tar archive of the 'main'
	// branch and pipes it to tar, which extracts it into our build directory.
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
	// imageTag := buildApplication(appName, buildDir) // TODO: Implement this function!
	// fmt.Printf("-----> Built image: %s\n", imageTag)

	// --- 6. Deploy and Route (Placeholder) ---
	fmt.Fprintln(os.Stderr, "-----> Deploying new container...")
	// containerID := runApplication(appName, imageTag) // TODO: Implement this function!
	// updateRouting(appName, containerID) // TODO: Implement this function!

	// --- 7. Cleanup (Placeholder) ---
	// cleanupOldContainers(appName, containerID) // TODO: Implement this function!

	fmt.Fprintln(os.Stderr, "-----> Deployment complete (placeholders).")
	fmt.Fprintln(os.Stderr, "-----> Next steps: Implement building and running containers.")
}

func init() {
	rootCmd.AddCommand(gitReceiveCmd)
}

// Helper to gracefully run commands and stream their output
func runStreamingCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	// Setting Stderr and Stdout to os.Stderr and os.Stdout will stream the output
	// of the command directly to the user's terminal.
	cmd.Stderr = os.Stdout
	cmd.Stdout = os.Stdout
	return cmd.Run()
}
