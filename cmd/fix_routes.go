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

var fixRoutesCmd = &cobra.Command{
	Use:   "fix-routes",
	Short: "Fix broken Caddy routes after Docker restart",
	Long: `Fix broken Caddy routes after Docker restart.

This command scans all deployed applications, detects their current running ports,
and updates Caddy configuration to point to the correct ports.

Use this after Docker daemon restart or when Caddy shows "connection refused" errors.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(os.Stderr, "-----> Scanning for broken routes...")

		// Get all app state files
		appsDir := "/var/lib/mitte/apps"
		files, err := os.ReadDir(appsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not read apps directory: %v\n", err)
			os.Exit(1)
		}

		fixedCount := 0
		ctx := context.Background()

		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}

			appName := strings.TrimSuffix(file.Name(), ".json")
			fmt.Fprintf(os.Stderr, "-----> Checking app: %s\n", appName)

			// Load app state
			app, err := state.Load(appName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  !! Warning: Could not load state for %s: %v\n", appName, err)
				continue
			}

			// Check if container exists and get current port
			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				fmt.Fprintf(os.Stderr, "  !! Error: Could not create Docker client: %v\n", err)
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
				fmt.Fprintf(os.Stderr, "  !! Warning: Container not running or no port found for %s: %v\n", appName, err)
				continue
			}

			// Check if port has changed
			if app.HostPort == currentPort {
				fmt.Fprintf(os.Stderr, "  ✓ Route is correct (port %s)\n", currentPort)
				continue
			}

			fmt.Fprintf(os.Stderr, "  ! Port changed: %s -> %s\n", app.HostPort, currentPort)

			// Update app state
			app.HostPort = currentPort
			if err := app.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "  !! Warning: Could not save app state: %v\n", err)
			}

			// Update Caddy route
			if len(app.Domains) > 0 {
				authEnabled := app.Auth != nil && app.Auth.Enabled
				authPolicy := ""
				if authEnabled {
					authPolicy = app.Auth.Policy
				}
				if err := router.SetAppRoutesWithAuth(appName, app.Domains, currentPort, authEnabled, authPolicy); err != nil {
					fmt.Fprintf(os.Stderr, "  !! Error: Could not update Caddy route: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "  ✓ Updated Caddy route to port %s\n", currentPort)
					fixedCount++
				}
			} else {
				fmt.Fprintf(os.Stderr, "  ! No domains configured for this app\n")
			}
		}

		if fixedCount > 0 {
			fmt.Printf("\nFixed %d broken route(s).\n", fixedCount)
		} else {
			fmt.Println("No broken routes found.")
		}
	},
}

func init() {
	rootCmd.AddCommand(fixRoutesCmd)
}
