package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// gitReceiveCmd represents the command triggered by SSH for a git push.
// This command is not meant to be run directly by a user.
var gitReceiveCmd = &cobra.Command{
	Use:    "git-receive",
	Short:  "Handles git pushes over SSH (internal command)",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("--- Mitte Git Receiver ---")
		fmt.Println("TODO: This command will handle the git push process.")
		fmt.Printf("Original SSH command from environment: %s\n", os.Getenv("SSH_ORIGINAL_COMMAND"))
		fmt.Println("--------------------------")
		// TODO: parse the app name from the SSH_ORIGINAL_COMMAND,
		// check out the code, and trigger the build and deploy pipeline.
	},
}

func init() {
	rootCmd.AddCommand(gitReceiveCmd)
}
