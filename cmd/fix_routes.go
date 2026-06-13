package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/client"
	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/deployer"
	"github.com/mitte-sh/mitte/pkg/logger"
	"github.com/mitte-sh/mitte/pkg/router"
	"github.com/mitte-sh/mitte/pkg/state"
)

var fixRoutesCmd = &cobra.Command{
	Use:   "fix-routes",
	Short: "Fix broken Caddy routes after Docker restart",
	Long: `Fix broken Caddy routes after Docker restart.

This command scans all deployed applications, detects their current running ports,
and updates Caddy configuration to point to the correct ports.

Use this after Docker daemon restart or when Caddy shows "connection refused" errors.`,
	Run: func(cmd *cobra.Command, args []string) {
		logger.Error("-----> Scanning for broken routes...")

		// Get all app state files
		appsDir := "/var/lib/mitte/apps"
		files, err := os.ReadDir(appsDir)
		if err != nil {
			logger.Error("Could not read apps directory", "err", err)
			os.Exit(1)
		}

		fixedCount := 0
		ctx := context.Background()

		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}

			appName := strings.TrimSuffix(file.Name(), ".json")
			logger.Error(fmt.Sprintf("-----> Checking app: %s", appName))

			// Load app state
			app, err := state.Load(appName)
			if err != nil {
				logger.Warn(fmt.Sprintf("Could not load state for %s", appName), "err", err)
				continue
			}

			// Check if container exists and get current port
			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				logger.Error("Could not create Docker client", "err", err)
				continue
			}
			defer cli.Close()

			containerName := app.ContainerName
			if containerName == "" {
				containerName = strings.ToLower(appName)
			}

			// Get current host port
			currentPort, err := deployer.GetContainerHostPort(ctx, cli, containerName)
			if err != nil {
				logger.Warn(fmt.Sprintf("Container not running or no port found for %s", appName), "err", err)
				continue
			}

			// Check if port has changed
			if app.HostPort == currentPort {
				logger.Error(fmt.Sprintf("  ✓ Route is correct (port %s)", currentPort))
				continue
			}

			logger.Error(fmt.Sprintf("  ! Port changed: %s -> %s", app.HostPort, currentPort))

			// Update app state
			app.HostPort = currentPort
			if err := app.Save(); err != nil {
				logger.Warn("Could not save app state", "err", err)
			}

			// Update Caddy route
			if len(app.Domains) > 0 {
				authEnabled := app.Auth != nil && app.Auth.Enabled
				authPolicy := ""
				if authEnabled {
					authPolicy = app.Auth.Policy
				}
				if err := router.SetAppRoutesWithAuth(appName, app.Domains, currentPort, authEnabled, authPolicy); err != nil {
					logger.Error("Could not update Caddy route", "err", err)
				} else {
					logger.Error(fmt.Sprintf("  ✓ Updated Caddy route to port %s", currentPort))
					fixedCount++
				}
			} else {
				logger.Error("  ! No domains configured for this app")
			}
		}

		if fixedCount > 0 {
			logger.Info(fmt.Sprintf("Fixed %d broken route(s).", fixedCount))
		} else {
			logger.Info("No broken routes found.")
		}
	},
}

func init() {
	rootCmd.AddCommand(fixRoutesCmd)
}
