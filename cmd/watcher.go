package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
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
		fmt.Println("Checking container watcher service status...")

		statusCmd := exec.Command("sudo", "systemctl", "status", "mitte-watcher.service")
		statusCmd.Stdout = os.Stdout
		statusCmd.Stderr = os.Stderr

		if err := statusCmd.Run(); err != nil {
			fmt.Printf("Error checking watcher status: %v\n", err)
			os.Exit(1)
		}
	},
}

var watcherRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart container watcher service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Restarting container watcher service...")

		restartCmd := exec.Command("sudo", "systemctl", "restart", "mitte-watcher.service")
		restartCmd.Stdout = os.Stdout
		restartCmd.Stderr = os.Stderr

		if err := restartCmd.Run(); err != nil {
			fmt.Printf("Error restarting watcher: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("✅ Container watcher service restarted")
	},
}

var watcherLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show logs from container watcher service",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Showing container watcher logs...")

		logsCmd := exec.Command("sudo", "journalctl", "-u", "mitte-watcher.service", "-f")
		logsCmd.Stdout = os.Stdout
		logsCmd.Stderr = os.Stderr

		if err := logsCmd.Run(); err != nil {
			fmt.Printf("Error showing watcher logs: %v\n", err)
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
