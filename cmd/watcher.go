package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/logger"
)

var watcherCmd = &cobra.Command{
	Use:   "watcher",
	Short: "Manage the container watcher service",
	Long:  "Manage the container watcher service that automatically fixes Caddy routes after Docker restarts.",
}

var watcherStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check status of container watcher service",
	Run: func(cmd *cobra.Command, args []string) {
		logger.Info("Checking container watcher service status...")

		statusCmd := exec.Command("sudo", "systemctl", "status", "mitte-watcher.service")
		statusCmd.Stdout = os.Stdout
		statusCmd.Stderr = os.Stderr

		if err := statusCmd.Run(); err != nil {
			logger.Info(fmt.Sprintf("Error checking watcher status: %v", err))
			os.Exit(1)
		}
	},
}

var watcherRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart container watcher service",
	Run: func(cmd *cobra.Command, args []string) {
		logger.Info("Restarting container watcher service...")

		restartCmd := exec.Command("sudo", "systemctl", "restart", "mitte-watcher.service")
		restartCmd.Stdout = os.Stdout
		restartCmd.Stderr = os.Stderr

		if err := restartCmd.Run(); err != nil {
			logger.Info(fmt.Sprintf("Error restarting watcher: %v", err))
			os.Exit(1)
		}

		logger.Info("✅ Container watcher service restarted")
	},
}

var watcherLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show logs from container watcher service",
	Run: func(cmd *cobra.Command, args []string) {
		logger.Info("Showing container watcher logs...")

		logsCmd := exec.Command("sudo", "journalctl", "-u", "mitte-watcher.service", "-f")
		logsCmd.Stdout = os.Stdout
		logsCmd.Stderr = os.Stderr

		if err := logsCmd.Run(); err != nil {
			logger.Info(fmt.Sprintf("Error showing watcher logs: %v", err))
			os.Exit(1)
		}
	},
}

func init() {
	watcherCmd.AddCommand(watcherStatusCmd)
	watcherCmd.AddCommand(watcherRestartCmd)
	watcherCmd.AddCommand(watcherLogsCmd)
	rootCmd.AddCommand(watcherCmd)
}
