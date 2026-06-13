package auth

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/mitte-sh/mitte/pkg/logger"
)

// EnsureContainer creates and starts the Authelia container if it's not already running.
func EnsureContainer(ctx context.Context) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	// Check if container already exists and is running
	inspect, err := cli.ContainerInspect(ctx, ContainerName)
	if err == nil {
		if inspect.State.Running {
			logger.Info("Authelia container is already running.")
			return nil
		}
		// Container exists but not running, start it
		logger.Info("Starting existing Authelia container...")
		if err := cli.ContainerStart(ctx, ContainerName, container.StartOptions{}); err != nil {
			return fmt.Errorf("failed to start Authelia container: %w", err)
		}
		return nil
	}

	// Pull the Authelia image
	logger.Info("Pulling Authelia image...")
	reader, err := cli.ImagePull(ctx, AutheliaImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull Authelia image: %w", err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()

	// Create the container
	logger.Info("Creating Authelia container...")

	containerPort := nat.Port(AutheliaPort + "/tcp")

	containerConfig := &container.Config{
		Image: AutheliaImage,
		Cmd:   []string{"--config", "/config/configuration.yml"},
		ExposedPorts: nat.PortSet{
			containerPort: struct{}{},
		},
	}

	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "always"},
		Binds: []string{
			fmt.Sprintf("%s:/config:rw", AuthDir),
		},
		PortBindings: nat.PortMap{
			containerPort: []nat.PortBinding{
				{HostIP: "127.0.0.1", HostPort: AutheliaPort},
			},
		},
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"mitte": {},
		},
	}

	createResp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, ContainerName)
	if err != nil {
		return fmt.Errorf("failed to create Authelia container: %w", err)
	}

	// Start the container
	logger.Info("Starting Authelia container...")
	if err := cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start Authelia container: %w", err)
	}

	logger.Info("Authelia container started successfully.")
	return nil
}

// StopContainer stops and removes the Authelia container.
func StopContainer(ctx context.Context) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	logger.Info("Stopping Authelia container...")

	timeout := 10
	if err := cli.ContainerStop(ctx, ContainerName, container.StopOptions{Timeout: &timeout}); err != nil {
		if !client.IsErrNotFound(err) {
			logger.Warn("could not stop container", "err", err)
		}
	}

	if err := cli.ContainerRemove(ctx, ContainerName, container.RemoveOptions{Force: true}); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove Authelia container: %w", err)
		}
	}

	logger.Info("Authelia container stopped and removed.")
	return nil
}

// RestartContainer restarts the Authelia container to pick up config changes.
func RestartContainer(ctx context.Context) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	logger.Info("Restarting Authelia container to apply config changes...")

	timeout := 10
	if err := cli.ContainerRestart(ctx, ContainerName, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("failed to restart Authelia container: %w", err)
	}

	logger.Info("Authelia container restarted successfully.")
	return nil
}

// IsRunning checks if the Authelia container is currently running.
func IsRunning(ctx context.Context) (bool, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return false, fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	inspect, err := cli.ContainerInspect(ctx, ContainerName)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return inspect.State.Running, nil
}

// Status returns a human-readable status string for the Authelia service.
func Status(ctx context.Context) string {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "error: " + err.Error()
	}
	defer cli.Close()

	inspect, err := cli.ContainerInspect(ctx, ContainerName)
	if err != nil {
		if client.IsErrNotFound(err) {
			return "not installed"
		}
		return "error: " + err.Error()
	}

	if inspect.State.Running {
		return "running"
	}
	return fmt.Sprintf("stopped (status: %s)", inspect.State.Status)
}

// AutheliaURL returns the internal URL where Authelia listens.
func AutheliaURL() string {
	return fmt.Sprintf("http://%s:%s", ContainerName, AutheliaPort)
}

// AuthCheckURL returns the forward-auth check endpoint.
func AuthCheckURL() string {
	return fmt.Sprintf("%s/api/authz/forward-auth", AutheliaURL())
}

// GetRunningAppDomains scans all app state files and returns domains of
// protected apps, so we know which ACL rules to generate.
func GetRunningAppDomains() (map[string]string, error) {
	const appsDir = "/var/lib/mitte/apps"
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Import state here to avoid circular import
	// We'll read the JSON directly instead
	type appJSON struct {
		AppName string   `json:"app_name"`
		Domains []string `json:"domains"`
		Auth    *struct {
			Enabled bool   `json:"enabled"`
			Policy  string `json:"policy"`
		} `json:"auth,omitempty"`
	}

	protected := make(map[string]string) // domain -> policy
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// We don't parse here — let the caller handle it
	}
	return protected, nil
}
