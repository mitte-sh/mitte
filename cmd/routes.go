package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/client"
	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/deployer"
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
			fmt.Fprintf(os.Stderr, "Error: Could not load app state: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("App: %s\n", appName)
		fmt.Printf("Domains: %v\n", app.Domains)
		fmt.Printf("Stored Port: %s\n", app.HostPort)

		// Check if route file exists
		exists, err := router.RouteExistsFile(appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error checking route: %v\n", err)
			os.Exit(1)
		}

		if exists {
			fmt.Println("Route file: ✅ Exists")
		} else {
			fmt.Println("Route file: ❌ Missing")
		}

		// Check if container is running and get current port
		ctx := context.Background()
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not create Docker client: %v\n", err)
			os.Exit(1)
		}
		defer cli.Close()

		containerName := app.ContainerName
		if containerName == "" {
			containerName = strings.ToLower(appName)
		}

		currentPort, err := deployer.GetContainerHostPort(ctx, cli, containerName)
		if err != nil {
			fmt.Printf("Container status: ❌ Not running or no port found: %v\n", err)
		} else {
			fmt.Printf("Container status: ✅ Running on port %s\n", currentPort)

			if app.HostPort == currentPort {
				fmt.Println("Port match: ✅ Stored port matches current port")
			} else {
				fmt.Printf("Port match: ❌ Mismatch (stored: %s, current: %s)\n", app.HostPort, currentPort)
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
			fmt.Fprintf(os.Stderr, "Error: Could not load app state: %v\n", err)
			os.Exit(1)
		}

		if len(app.Domains) == 0 {
			fmt.Fprintf(os.Stderr, "Error: App %s has no domains configured\n", appName)
			os.Exit(1)
		}

		fmt.Printf("Updating route for %s to port %s...\n", appName, newPort)

		// Update app state
		app.HostPort = newPort
		if err := app.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not save app state: %v\n", err)
		}

		// Update Caddy route
		authEnabled := app.Auth != nil && app.Auth.Enabled
		authPolicy := ""
		if authEnabled {
			authPolicy = app.Auth.Policy
		}
		if err := router.SetAppRoutesWithAuth(appName, app.Domains, newPort, authEnabled, authPolicy); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not update Caddy route: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("✅ Route updated successfully\n")
	},
}

func init() {
	routesCmd.AddCommand(routesCheckCmd)
	routesCmd.AddCommand(routesUpdateCmd)
	rootCmd.AddCommand(routesCmd)
}
