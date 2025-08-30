package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mitteapp/mitteapp/pkg/actions"
)

var appsRestartCmd = &cobra.Command{
	Use:   "restart <app-name>",
	Short: "Restart an application",
	Long:  "Restarts an application by redeploying its most recent image with the latest configuration.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]

		fmt.Fprintf(os.Stderr, "-----> Restarting application '%s'...\n", appName)

		if err := actions.RestartApp(appName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to restart application: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Application '%s' restarted successfully.\n", appName)
	},
}

// For a new cmd/restart.go:
func init() {
	rootCmd.AddCommand(appsRestartCmd)
}
