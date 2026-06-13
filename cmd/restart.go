package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/actions"
	"github.com/mitte-sh/mitte/pkg/logger"
	"github.com/mitte-sh/mitte/pkg/state"
)

var appsRestartCmd = &cobra.Command{
	Use:   "restart <app-name>",
	Short: "Restart an application",
	Long:  "Restarts an application by redeploying its most recent image with the latest configuration.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		userInput := args[0]

		appName, err := state.ResolveAppName(userInput)
		if err != nil {
			logger.Error("", "err", err)
			os.Exit(1)
		}

		logger.Error(fmt.Sprintf("-----> Restarting application '%s'...", appName))

		if err := actions.RestartApp(appName); err != nil {
			logger.Error("Failed to restart application", "err", err)
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("Application '%s' restarted successfully.", appName))
	},
}

// For a new cmd/restart.go:
func init() {
	rootCmd.AddCommand(appsRestartCmd)
}
