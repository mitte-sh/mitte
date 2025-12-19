package services

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

func CreateMariaDB(ctx context.Context, instanceName, rootPassword, version string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	theImage := fmt.Sprintf("mariadb:%s", version)

	fmt.Printf("Pulling image %s...\n", theImage)
	out, err := cli.ImagePull(ctx, theImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer out.Close()
	io.Copy(os.Stdout, out)

	config := &container.Config{
		Image: theImage,
		Env: []string{
			fmt.Sprintf("MARIADB_ROOT_PASSWORD=%s", rootPassword),
			// TODO: Add more config vars here, like MARIADB_DATABASE
		},
		Healthcheck: &container.HealthConfig{
			Test:        []string{"mysqladmin", "ping", "-h", "localhost"},
			Interval:    10 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     3,
			StartPeriod: 30 * time.Second,
		},
	}

	hostConfig := &container.HostConfig{
		Binds:         []string{fmt.Sprintf("mitte-db-%s:/var/lib/mysql", instanceName)},
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"mitte": {},
		},
	}

	resp, err := cli.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, instanceName)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	return cli.ContainerStart(ctx, resp.ID, container.StartOptions{})
}

// DestroyMariaDB stops and removes the container and its associated data volume.
func DestroyMariaDB(ctx context.Context, instanceName string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	// 1. Stop and remove the container.
	// We use Force to remove it even if it's running.
	if err := cli.ContainerRemove(ctx, instanceName, container.RemoveOptions{Force: true}); err != nil {
		// Ignore "not found" errors, as our goal is to ensure it's gone.
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove container: %w", err)
		}
	}

	// 2. Remove the persistent volume. This is the crucial step to delete the data.
	volumeName := fmt.Sprintf("mitte-db-%s", instanceName)
	if err := cli.VolumeRemove(ctx, volumeName, true); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove volume '%s': %w", volumeName, err)
		}
	}

	return nil
}
