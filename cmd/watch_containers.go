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
	"github.com/mitte-sh/mitte/pkg/logger"
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
		logger.Error("-----> Starting Docker container watcher...")
		logger.Error("-----> Monitoring for container start/restart events")

		ctx := context.Background()

		// Create Docker client
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			logger.Error("Could not create Docker client", "err", err)
			os.Exit(1)
		}
		defer cli.Close()

		// Initial scan to fix any existing broken routes
		logger.Error("-----> Performing initial route check...")
		fixAllRoutes(ctx, cli)

		// Start watching Docker events
		eventsChan, errChan := cli.Events(ctx, events.ListOptions{})

		for {
			select {
			case event := <-eventsChan:
				handleDockerEvent(ctx, cli, event)
			case err := <-errChan:
				logger.Error(fmt.Sprintf("Error receiving Docker events: %v", err))
				// Wait a bit before trying to reconnect
				time.Sleep(5 * time.Second)
				// Try to reconnect
				cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
				if err != nil {
					logger.Error(fmt.Sprintf("Error reconnecting to Docker: %v", err))
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

	logger.Error(fmt.Sprintf("-----> Container event: %s %s (%s)", event.Action, containerName, containerID[:12]))

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
			logger.Warn(fmt.Sprintf("Could not get port for %s", containerName), "err", err)
			return
		}

		// Check if port has changed
		if app.HostPort == currentPort {
			logger.Error(fmt.Sprintf("  ✓ Port unchanged for %s: %s", appName, currentPort))
			return
		}

		logger.Error(fmt.Sprintf("  ! Port changed for %s: %s -> %s", appName, app.HostPort, currentPort))

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
				logger.Error(fmt.Sprintf("  ✓ Updated Caddy route for %s to port %s", appName, currentPort))
			}
		}
	}
}

func fixAllRoutes(ctx context.Context, cli *client.Client) {
	// Get all app state files
	appsDir := "/var/lib/mitte/apps"
	files, err := os.ReadDir(appsDir)
	if err != nil {
		logger.Error("Could not read apps directory", "err", err)
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
			logger.Warn(fmt.Sprintf("Could not load state for %s", appName), "err", err)
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
			logger.Error(fmt.Sprintf("  ! Fixing route for %s: %s -> %s", appName, app.HostPort, currentPort))

			// Update app state
			app.HostPort = currentPort
			if err := app.Save(); err != nil {
				logger.Warn("Could not save app state", "err", err)
			}

			// Update Caddy route
			authEnabled := app.Auth != nil && app.Auth.Enabled
			authPolicy := ""
			if authEnabled {
				authPolicy = app.Auth.Policy
			}
			if err := router.SetAppRoutesWithAuth(appName, app.Domains, currentPort, authEnabled, authPolicy); err != nil {
				logger.Error("Could not update Caddy route", "err", err)
			} else {
				logger.Error(fmt.Sprintf("  ✓ Fixed route for %s", appName))
			}
		}
	}
}

func init() {
	rootCmd.AddCommand(watchContainersCmd)
}
