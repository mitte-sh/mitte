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

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Manage Caddy routes for applications",
	Long:  "Manage Caddy routes for applications, including checking and updating routes.",
}

var routesCheckCmd = &cobra.Command{
	Use:   "check <app-name>",
	Short: "Check if an app's route exists and is correct",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]

		// Load app state
		app, err := state.Load(appName)
		if err != nil {
			logger.Error("Could not load app state", "err", err)
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("App: %s", appName))
		logger.Info(fmt.Sprintf("Domains: %v", app.Domains))
		logger.Info(fmt.Sprintf("Stored Port: %s", app.HostPort))

		// Check if route file exists
		exists, err := router.RouteExistsFile(appName)
		if err != nil {
			logger.Error(fmt.Sprintf("Error checking route: %v", err))
			os.Exit(1)
		}

		if exists {
			logger.Info("Route file: ✅ Exists")
		} else {
			logger.Info("Route file: ❌ Missing")
		}

		// Check if container is running and get current port
		ctx := context.Background()
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			logger.Error("Could not create Docker client", "err", err)
			os.Exit(1)
		}
		defer cli.Close()

		containerName := app.ContainerName
		if containerName == "" {
			containerName = strings.ToLower(appName)
		}

		currentPort, err := deployer.GetContainerHostPort(ctx, cli, containerName)
		if err != nil {
			logger.Info(fmt.Sprintf("Container status: ❌ Not running or no port found: %v", err))
		} else {
			logger.Info(fmt.Sprintf("Container status: ✅ Running on port %s", currentPort))

			if app.HostPort == currentPort {
				logger.Info("Port match: ✅ Stored port matches current port")
			} else {
				logger.Info(fmt.Sprintf("Port match: ❌ Mismatch (stored: %s, current: %s)", app.HostPort, currentPort))
			}
		}
	},
}

var routesUpdateCmd = &cobra.Command{
	Use:   "update <app-name> <port>",
	Short: "Update an app's Caddy route to use a specific port",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]
		newPort := args[1]

		// Load app state
		app, err := state.Load(appName)
		if err != nil {
			logger.Error("Could not load app state", "err", err)
			os.Exit(1)
		}

		if len(app.Domains) == 0 && !app.Internal {
			logger.Error(fmt.Sprintf("Error: App %s has no domains configured", appName))
			os.Exit(1)
		}

		if app.Internal {
			logger.Error(fmt.Sprintf("Error: App %s is internal and has no public routes", appName))
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("Updating route for %s to port %s...", appName, newPort))

		// Update app state
		app.HostPort = newPort
		if err := app.Save(); err != nil {
			logger.Warn("Could not save app state", "err", err)
		}

		// Update Caddy route
		authEnabled := app.Auth != nil && app.Auth.Enabled
		authPolicy := ""
		if authEnabled {
			authPolicy = app.Auth.Policy
		}
		if err := router.SetAppRoutesWithAuth(appName, app.Domains, newPort, authEnabled, authPolicy); err != nil {
			logger.Error("Could not update Caddy route", "err", err)
			os.Exit(1)
		}

		logger.Info("✅ Route updated successfully")
	},
}

func init() {
	routesCmd.AddCommand(routesCheckCmd)
	routesCmd.AddCommand(routesUpdateCmd)
	rootCmd.AddCommand(routesCmd)
}
