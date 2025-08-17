package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

const mitteServerBinaryPath = "/usr/bin/mitte"

// sshHandlerCmd is the entry point for all non-interactive commands via SSH.
// It is not intended to be executed directly by a human.
var sshHandlerCmd = &cobra.Command{
	Use:    "ssh-handler",
	Short:  "Internal SSH command handler for mitte",
	Hidden: true,
	Run:    runSshHandler,
}

func init() {
	rootCmd.AddCommand(sshHandlerCmd)
}

func runSshHandler(cmd *cobra.Command, args []string) {
	originalCmd := os.Getenv("SSH_ORIGINAL_COMMAND")
	if originalCmd == "" {
		fmt.Fprintln(os.Stderr, "Error: This command is intended to be run only via SSH.")
		os.Exit(1)
	}

	// --- Routing logic ---

	// Case 1: It's a 'git push'
	if strings.HasPrefix(originalCmd, "git-receive-pack") {
		err := executeMitteCommand("git-receive")
		if err != nil {
			// The error will have already been printed by the subcommand.
			os.Exit(1)
		}
		return
	}

	// Case 2: It is a mitte command (e.g., '/usr/bin/mitte logs my-app')
	prefix := mitteServerBinaryPath + " "
	if strings.HasPrefix(originalCmd, prefix) {
		// Extract the actual arguments of the command, e.g., ["logs", "my-app", "--follow"]
		argsStr := strings.TrimPrefix(originalCmd, prefix)
		mitteArgs := strings.Fields(argsStr)
		err := executeMitteCommand(mitteArgs...)
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			os.Exit(1)
		}
		return
	}

	// Case 3: Unknown or disallowed command
	fmt.Fprintf(os.Stderr, "Mitte: Unrecognized or not allowed command: '%s'\n", originalCmd)
	fmt.Fprintln(os.Stderr, "This SSH key can only be used for 'git push' or 'mitte' commands.")
	os.Exit(1)
}

// executeMitteCommand is a helper function to re-execute the mitte binary
// with a different subcommand and arguments, connecting stdin/stdout/stderr.
func executeMitteCommand(args ...string) error {
	// We re-execute our own binary, but with different arguments.
	cmd := exec.Command(mitteServerBinaryPath, args...)

	// Set an environment variable to prevent the re-executed command
	// from trying to act as an SSH client again.
	cmd.Env = append(os.Environ(), "MITTE_IS_SERVER_SIDE=true")

	// Connect I/O so that the data flow (git protocol, logs, etc.) is
	// transparent between the local client and the subprocess on the server.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
