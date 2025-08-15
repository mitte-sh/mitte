package deployer

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// DeployResult holds information about a deployed container.
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
	fmt.Fprintf(os.Stderr, "-----> Checking for existing container '%s' to stop...\n", containerName)
	// We ignore the error here because the container might not exist on the first deploy.
	_ = cli.ContainerStop(ctx, containerName, container.StopOptions{})
	_ = cli.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	// --- 2. Create the new container ---
	fmt.Fprintf(os.Stderr, "-----> Creating new container from image %s\n", imageTag)
	containerConfig := &container.Config{
		Image: imageTag,
	}

	hostConfig := &container.HostConfig{
		PublishAllPorts: true,
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

	// --- 4. Get the host port using our new helper function ---
	// We assume port 80/tcp for now, as that's what your nginx Dockerfile exposes.
	hostPort, err := GetContainerHostPort(ctx, cli, createResp.ID, "80/tcp")
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "-----> Container is running. Port 80 is mapped to host port %s\n", hostPort)

	return &DeployResult{
		ContainerID: createResp.ID,
		HostPort:    hostPort,
	}, nil
}

// GetContainerHostPort inspects a running container and returns the host port
// that is mapped to the specified internal container port (e.g., "80/tcp").
func GetContainerHostPort(ctx context.Context, cli *client.Client, containerIDOrName, containerPort string) (string, error) {
	// --- 1. Inspect the container ---
	inspectResp, err := cli.ContainerInspect(ctx, containerIDOrName)
	if err != nil {
		return "", fmt.Errorf("failed to inspect container '%s': %w", containerIDOrName, err)
	}

	// The key for the port map is of type nat.Port, not a simple string.
	// We must create it correctly.
	port, err := nat.NewPort("tcp", strings.Split(containerPort, "/")[0])
	if err != nil {
		return "", fmt.Errorf("invalid containerPort format '%s': %w", containerPort, err)
	}

	// --- 2. Find the port binding ---
	portBindings := inspectResp.NetworkSettings.Ports[port]
	if len(portBindings) == 0 {
		return "", fmt.Errorf("container '%s' is running, but no host port mapping was found for container port %s", containerIDOrName, containerPort)
	}

	// The first binding is the one we want.
	hostPort := portBindings[0].HostPort
	return hostPort, nil
}
