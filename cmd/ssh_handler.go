package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/logger"
)

// sshHandlerCmd is the entry point for all non-interactive commands via SSH.
// It is not intended to be executed directly by a human.
var sshHandlerCmd = &cobra.Command{
	Use:                "ssh-handler",
	Short:              "Internal SSH command handler for mitte",
	Hidden:             true,
	DisableFlagParsing: true,
	Run:                runSshHandler,
}

func init() {
	rootCmd.AddCommand(sshHandlerCmd)
}

func runSshHandler(cmd *cobra.Command, args []string) {
	originalCmd := os.Getenv("SSH_ORIGINAL_COMMAND")

	// CASE 1: Git push. This uses git's standard command format.
	if strings.HasPrefix(originalCmd, "git-receive-pack") {
		// We re-execute mitte as the git-receive command.
		c := exec.Command("mitte", "git-receive")
		c.Env = append(os.Environ(), "MITTE_IS_SERVER_SIDE=true")
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			// Don't exit silently, we need to know the exit code.
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			os.Exit(1)
		}
		return
	}

	// CASE 2: Mitte command, sent from our client.
	// The `originalCmd` will be a shell-safe string like:
	// "mitte 'config' 'set' 'app' 'GREETING=Hello from Mitte'"

	// We check if the first "word" is "mitte". This is robust against the quotes.
	fields := strings.Fields(originalCmd)
	if len(fields) > 0 && fields[0] == "mitte" {
		// We use a shell (`sh -c`) to execute the entire original command string.
		// The shell is an expert at parsing this quoted format. It will correctly
		// un-quote each argument before executing the final `mitte` command.
		c := exec.Command("sh", "-c", originalCmd)
		c.Env = append(os.Environ(), "MITTE_IS_SERVER_SIDE=true")
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			os.Exit(1)
		}
		return
	}

	// CASE 3: Unrecognized command.
	logger.Error(fmt.Sprintf("Mitte: Unrecognized or not allowed command: '%s'", originalCmd))
	logger.Error("This SSH key can only be used for 'git push' or 'mitte' commands.")
	os.Exit(1)
}
