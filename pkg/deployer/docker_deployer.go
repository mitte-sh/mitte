package deployer

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/mitteapp/mitteapp/pkg/state"
)

// DeployResult holds information about a deployed container.
type DeployResult struct {
	ContainerID string
	HostPort    string
}

// Deploy creates and starts a new container for the given app and image.
// It also stops and removes any previous container for that app.
// It returns the new container's ID and its published host port.
func Deploy(ctx context.Context, appName, imageTag string, volumes []string, ports []string, containerName string, command []string, user string) (*DeployResult, error) {
	fmt.Fprintln(os.Stderr, "-----> Starting deployment...")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	appState, err := state.Load(appName)
	if err != nil {
		return nil, fmt.Errorf("no se pudo cargar el estado para la app %s: %w", appName, err)
	}

	// Use custom container name if provided, otherwise use app name
	actualContainerName := containerName
	if actualContainerName == "" {
		actualContainerName = strings.ToLower(appName)
	}

	// --- 1. Stop and remove any existing container for this app ---
	fmt.Fprintf(os.Stderr, "-----> Checking for existing container '%s' to stop...\n", actualContainerName)
	// We ignore the error here because the container might not exist on the first deploy.
	_ = cli.ContainerStop(ctx, actualContainerName, container.StopOptions{})
	_ = cli.ContainerRemove(ctx, actualContainerName, container.RemoveOptions{Force: true})

	envVars := []string{}
	for key, value := range appState.EnvVars {
		envVars = append(envVars, fmt.Sprintf("%s=%s", key, value))
	}

	// --- 1.5. Parse volume bindings ---
	var binds []string
	for _, volume := range volumes {
		if volume != "" {
			// Support formats: "host:container" or "host:container:options"
			parts := strings.Split(volume, ":")
			if len(parts) >= 2 {
				// Expand environment variables in host path
				hostPath := os.ExpandEnv(parts[0])
				containerPath := parts[1]

				if len(parts) == 2 {
					// Format: host:container
					binds = append(binds, fmt.Sprintf("%s:%s", hostPath, containerPath))
				} else if len(parts) == 3 {
					// Format: host:container:options
					options := parts[2]
					binds = append(binds, fmt.Sprintf("%s:%s:%s", hostPath, containerPath, options))
				}
			}
		}
	}

	// --- 1.6. Parse port bindings ---
	portBindings := make(nat.PortMap)

	// First, handle explicitly configured ports
	for _, port := range ports {
		if port != "" {
			// Support format: "host:container" (e.g., "9200:9200")
			parts := strings.Split(port, ":")
			if len(parts) == 2 {
				hostPort := parts[0]
				containerPort := parts[1]

				// Create the container port with /tcp suffix
				containerPortWithProto := nat.Port(containerPort + "/tcp")

				// Add to port bindings
				portBindings[containerPortWithProto] = []nat.PortBinding{
					{
						HostIP:   "0.0.0.0",
						HostPort: hostPort,
					},
				}
			}
		}
	}

	// If no custom ports are configured, automatically bind exposed ports
	if len(portBindings) == 0 {
		exposedPorts, err := getExposedPorts(ctx, cli, imageTag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not inspect image ports: %v\n", err)
		}

		// If no ports are exposed, default to 8080 for CNB/web applications
		if len(exposedPorts) == 0 {
			fmt.Fprintf(os.Stderr, "-----> No exposed ports found, defaulting to port 8080 for web applications\n")
			defaultPort, _ := nat.NewPort("tcp", "8080")
			exposedPorts = []nat.Port{defaultPort}
		}

		if len(exposedPorts) > 0 {
			fmt.Fprintf(os.Stderr, "-----> Auto-binding exposed ports: %v\n", exposedPorts)

			// Try to use previously assigned port if available
			usePreviousPort := false
			previousPort := ""

			// Load app state to check for previous port
			appState, err := state.Load(appName)
			if err == nil && appState.HostPort != "" {
				previousPort = appState.HostPort
				usePreviousPort = true
				fmt.Fprintf(os.Stderr, "-----> Found previous port: %s\n", previousPort)
			}

			for i, port := range exposedPorts {
				// For the first exposed port, try to use previous port if available
				if i == 0 && usePreviousPort {
					portBindings[port] = []nat.PortBinding{
						{
							HostIP:   "0.0.0.0",
							HostPort: previousPort,
						},
					}
					fmt.Fprintf(os.Stderr, "-----> Using previous port %s for %s\n", previousPort, port)
				} else {
					// Bind to a random host port (empty HostPort means random)
					portBindings[port] = []nat.PortBinding{
						{
							HostIP:   "0.0.0.0",
							HostPort: "", // Random port
						},
					}
				}
			}
		}
	}

	// --- 2. Create the new container ---
	fmt.Fprintf(os.Stderr, "-----> Creating new container from image %s\n", imageTag)
	containerConfig := &container.Config{
		Image: imageTag,
		Env:   envVars,
		Cmd:   command,
		User:  user,
	}

	hostConfig := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: container.RestartPolicy{Name: "always"},
		Binds:         binds,
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"mitte": {},
		},
	}

	createResp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, actualContainerName)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())

	// --- 3. Start the container ---
	fmt.Fprintf(os.Stderr, "-----> Starting container %s\n", createResp.ID[:12])
	if err = cli.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// --- 4. Get the host port using our helper function ---
	hostPort, err := GetContainerHostPort(ctx, cli, createResp.ID)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "-----> Container is running. First mapped port: %s\n", hostPort)

	return &DeployResult{
		ContainerID: createResp.ID,
		HostPort:    hostPort,
	}, nil
}

// GetContainerHostPort inspects a running container and returns the host port
// that is mapped to the specified internal container port (e.g., "80/tcp").
func GetContainerHostPort(ctx context.Context, cli *client.Client, containerIDOrName string) (string, error) {
	// --- 1. Inspect the container ---
	inspectResp, err := cli.ContainerInspect(ctx, containerIDOrName)
	if err != nil {
		return "", fmt.Errorf("failed to inspect container '%s': %w", containerIDOrName, err)
	}

	// // The key for the port map is of type nat.Port, not a simple string.
	// // We must create it correctly.
	// port, err := nat.NewPort("tcp", strings.Split(containerPort, "/")[0])
	// if err != nil {
	// 	return "", fmt.Errorf("invalid containerPort format '%s': %w", containerPort, err)
	// }
	//
	// // --- 2. Find the port binding ---
	// portBindings := inspectResp.NetworkSettings.Ports[port]
	// if len(portBindings) == 0 {
	// 	return "", fmt.Errorf("container '%s' is running, but no host port mapping was found for container port %s", containerIDOrName, containerPort)
	// }
	//
	// // The first binding is the one we want.
	// hostPort := portBindings[0].HostPort
	// return hostPort, nil

	// 2. Discover the port
	// The Ports map contains all port bindings. We'll iterate through it and grab the first one we find.
	// This is a robust strategy for single-port web applications.
	for _, portBindings := range inspectResp.NetworkSettings.Ports {
		if len(portBindings) > 0 && portBindings[0].HostPort != "" {
			// Found a valid binding, return its host port
			return portBindings[0].HostPort, nil
		}
	}

	// 3. If we finish the loop and find nothing, the container has no exposed ports.
	return "", fmt.Errorf("container '%s' is running, but no exposed ports were found to be mapped to the host", containerIDOrName)

}

// getExposedPorts inspects a Docker image and returns its exposed ports
func getExposedPorts(ctx context.Context, cli *client.Client, imageName string) ([]nat.Port, error) {
	inspect, _, err := cli.ImageInspectWithRaw(ctx, imageName)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect image %s: %w", imageName, err)
	}

	var exposedPorts []nat.Port
	for portStr := range inspect.Config.ExposedPorts {
		port, err := nat.NewPort("tcp", strings.TrimSuffix(portStr, "/tcp"))
		if err != nil {
			continue // Skip invalid ports
		}
		exposedPorts = append(exposedPorts, port)
	}

	return exposedPorts, nil
}

// GetLatestImageForApp finds the most recently built Docker image for a given app.
func GetLatestImageForApp(ctx context.Context, appName string) (string, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	// The filter argument tells the Docker daemon to only return images
	// with a tag that matches the app's name (e.g., "my-app:*").
	filterArgs := filters.NewArgs()
	filterArgs.Add("reference", fmt.Sprintf("%s:*", appName))

	images, err := cli.ImageList(ctx, image.ListOptions{Filters: filterArgs})
	if err != nil {
		return "", fmt.Errorf("failed to list images for app '%s': %w", appName, err)
	}

	if len(images) == 0 {
		return "", fmt.Errorf("no images found matching app name '%s'", appName)
	}

	// We assume the first image in the list is the most recent one.
	// The RepoTags field can contain multiple tags; we use the first one.
	if len(images[0].RepoTags) == 0 {
		return "", fmt.Errorf("image %s has no tags", images[0].ID)
	}
	latestImageTag := images[0].RepoTags[0]

	return latestImageTag, nil
}

// StopAndRemoveContainer forcefully stops and removes a container by name.
// It does not return an error if the container does not exist.
func StopAndRemoveContainer(ctx context.Context, appName string, containerName string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	// Use custom container name if provided, otherwise use app name
	actualContainerName := containerName
	if actualContainerName == "" {
		actualContainerName = strings.ToLower(appName)
	}
	fmt.Fprintf(os.Stderr, "-----> Stopping and removing container '%s'...\n", actualContainerName)

	// We don't care about errors here, as the container might already be gone.
	_ = cli.ContainerStop(ctx, actualContainerName, container.StopOptions{})
	_ = cli.ContainerRemove(ctx, actualContainerName, container.RemoveOptions{Force: true})

	fmt.Fprintln(os.Stderr, "-----> Container stopped and removed.")
	return nil
}

// PruneAppImages removes all Docker images associated with a given application.
func PruneAppImages(ctx context.Context, appName string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	fmt.Fprintf(os.Stderr, "-----> Pruning images for app '%s'...\n", appName)

	// Create a filter to find all images with a tag like "appname:*"
	filterArgs := filters.NewArgs()
	filterArgs.Add("reference", fmt.Sprintf("%s:*", appName))

	images, err := cli.ImageList(ctx, image.ListOptions{Filters: filterArgs})
	if err != nil {
		return fmt.Errorf("failed to list images for pruning: %w", err)
	}

	if len(images) == 0 {
		fmt.Fprintln(os.Stderr, "-----> No images to prune.")
		return nil
	}

	for _, img := range images {
		fmt.Fprintf(os.Stderr, "       - Removing image %s\n", img.ID[:12])
		// We don't stop on the first error, try to delete as many as possible.
		_, err := cli.ImageRemove(ctx, img.ID, image.RemoveOptions{Force: true})
		if err != nil {
			fmt.Fprintf(os.Stderr, "         Warning: could not remove image %s: %v\n", img.ID[:12], err)
		}
	}

	fmt.Fprintln(os.Stderr, "-----> Image pruning complete.")
	return nil
}
