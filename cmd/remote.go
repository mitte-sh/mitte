package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mitteapp/mitteapp/pkg/config"
)

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Manage the remote mitte server",
}

var remoteSetCmd = &cobra.Command{
	Use:   "set <ssh_target>",
	Short: "Set the SSH target for the remote mitte server (e.g., mitte@your-server.com)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		sshTarget := args[0]
		cfg, err := config.LoadLocal()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
			os.Exit(1)
		}
		cfg.RemoteSSH = sshTarget
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to save config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Remote set to %s\n", sshTarget)
	},
}

func init() {
	remoteCmd.AddCommand(remoteSetCmd)
	rootCmd.AddCommand(remoteCmd)
}
