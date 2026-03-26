package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/deployer"
	"github.com/mitte-sh/mitte/pkg/router"
	"github.com/mitte-sh/mitte/pkg/state"
)

var watchContainersCmd = &cobra.Command{
	Use:   "watch-containers",
	Short: "Monitor Docker container events and fix routes automatically",
	Long: `Monitor Docker container events and fix routes automatically.

This command runs as a daemon that watches for Docker container start/restart events.
When a container starts, it detects the new port and updates Caddy configuration.

Run this as a systemd service to automatically fix routes after Docker restarts.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(os.Stderr, "-----> Starting Docker container watcher...")
		fmt.Fprintln(os.Stderr, "-----> Monitoring for container start/restart events")

		ctx := context.Background()

		// Create Docker client
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not create Docker client: %v\n", err)
			os.Exit(1)
		}
		defer cli.Close()

		// Initial scan to fix any existing broken routes
		fmt.Fprintln(os.Stderr, "-----> Performing initial route check...")
		fixAllRoutes(ctx, cli)

		// Start watching Docker events
		eventsChan, errChan := cli.Events(ctx, events.ListOptions{})

		for {
			select {
			case event := <-eventsChan:
				handleDockerEvent(ctx, cli, event)
			case err := <-errChan:
				fmt.Fprintf(os.Stderr, "Error receiving Docker events: %v\n", err)
				// Wait a bit before trying to reconnect
				time.Sleep(5 * time.Second)
				// Try to reconnect
				cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error reconnecting to Docker: %v\n", err)
					os.Exit(1)
				}
				eventsChan, errChan = cli.Events(ctx, events.ListOptions{})
			}
		}
	},
}

func handleDockerEvent(ctx context.Context, cli *client.Client, event events.Message) {
	// We're interested in container start events
	if event.Type != "container" || (event.Action != "start" && event.Action != "restart") {
		return
	}

	containerID := event.Actor.ID
	containerName := event.Actor.Attributes["name"]

	fmt.Fprintf(os.Stderr, "-----> Container event: %s %s (%s)\n", event.Action, containerName, containerID[:12])

	// Check if this is a mitte app container
	if !strings.HasPrefix(containerName, "mitte-") && containerName != "mitte" {
		// Try to extract app name from container name
		// Container names are usually lowercase app names
		appName := strings.TrimPrefix(containerName, "mitte-")
		if appName == containerName {
			// Not a mitte container
			return
		}

		// Check if this app exists in our state
		app, err := state.Load(appName)
		if err != nil {
			// App doesn't exist in our state
			return
		}

		// Get current port
		currentPort, err := deployer.GetContainerHostPort(ctx, cli, containerName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  !! Warning: Could not get port for %s: %v\n", containerName, err)
			return
		}

		// Check if port has changed
		if app.HostPort == currentPort {
			fmt.Fprintf(os.Stderr, "  ✓ Port unchanged for %s: %s\n", appName, currentPort)
			return
		}

		fmt.Fprintf(os.Stderr, "  ! Port changed for %s: %s -> %s\n", appName, app.HostPort, currentPort)

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
				fmt.Fprintf(os.Stderr, "  ✓ Updated Caddy route for %s to port %s\n", appName, currentPort)
			}
		}
	}
}

func fixAllRoutes(ctx context.Context, cli *client.Client) {
	// Get all app state files
	appsDir := "/var/lib/mitte/apps"
	files, err := os.ReadDir(appsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not read apps directory: %v\n", err)
		return
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		appName := strings.TrimSuffix(file.Name(), ".json")

		// Load app state
		app, err := state.Load(appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  !! Warning: Could not load state for %s: %v\n", appName, err)
			continue
		}

		// Check if container exists
		containerName := app.ContainerName
		if containerName == "" {
			containerName = strings.ToLower(appName)
		}

		// Get current host port
		currentPort, err := deployer.GetContainerHostPort(ctx, cli, containerName)
		if err != nil {
			// Container might not be running
			continue
		}

		// Check if port has changed
		if app.HostPort != currentPort && len(app.Domains) > 0 {
			fmt.Fprintf(os.Stderr, "  ! Fixing route for %s: %s -> %s\n", appName, app.HostPort, currentPort)

			// Update app state
			app.HostPort = currentPort
			if err := app.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "  !! Warning: Could not save app state: %v\n", err)
			}

			// Update Caddy route
			authEnabled := app.Auth != nil && app.Auth.Enabled
			authPolicy := ""
			if authEnabled {
				authPolicy = app.Auth.Policy
			}
			if err := router.SetAppRoutesWithAuth(appName, app.Domains, currentPort, authEnabled, authPolicy); err != nil {
				fmt.Fprintf(os.Stderr, "  !! Error: Could not update Caddy route: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "  ✓ Fixed route for %s\n", appName)
			}
		}
	}
}

func init() {
	rootCmd.AddCommand(watchContainersCmd)
}
