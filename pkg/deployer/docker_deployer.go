package deployer

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type DeployResult struct {
	ContainerID string
	HostPort    string
}

// Deploy creates and starts a new container for the given app and image.
// It also stops and removes any previous container for that app.
// It returns the new container's ID and its published host port.
func Deploy(ctx context.Context, appName, imageTag string) (*DeployResult, error) {
	fmt.Fprintln(os.Stderr, "-----> Starting deployment...")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	containerName := strings.ToLower(appName)

	// --- 1. Stop and remove any existing container for this app ---
	// We ignore the error here because the container might not exist on the first deploy.
	fmt.Fprintf(os.Stderr, "-----> Checking for existing container '%s' to stop...\n", containerName)
	_ = cli.ContainerStop(ctx, containerName, container.StopOptions{})
	_ = cli.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	// --- 2. Create the new container ---
	fmt.Fprintf(os.Stderr, "-----> Creating new container from image %s\n", imageTag)
	containerConfig := &container.Config{
		Image: imageTag,
		// Tty: true, // Useful for keeping some apps alive
	}

	// This is where we configure port mapping.
	// We want Docker to assign a random, available port on the host.
	hostConfig := &container.HostConfig{
		PublishAllPorts: true, // This maps all EXPOSED ports to random host ports.
		RestartPolicy:   container.RestartPolicy{Name: "always"},
	}

	createResp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, containerName)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// --- 3. Start the container ---
	fmt.Fprintf(os.Stderr, "-----> Starting container %s\n", createResp.ID[:12])
	if err = cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// --- 4. Inspect the container to find its published port ---
	inspectResp, err := cli.ContainerInspect(ctx, createResp.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect new container: %w", err)
	}

	// The Dockerfile exposes port 80. We need to find what host port it's mapped to.
	// The format is "80/tcp".
	portBindings := inspectResp.NetworkSettings.Ports["80/tcp"]
	if len(portBindings) == 0 {
		return nil, fmt.Errorf("container started, but could not find host port mapping for container port 80")
	}

	hostPort := portBindings[0].HostPort
	fmt.Fprintf(os.Stderr, "-----> Container is running. Port 80 is mapped to host port %s\n", hostPort)

	return &DeployResult{
		ContainerID: createResp.ID,
		HostPort:    hostPort,
	}, nil
}
