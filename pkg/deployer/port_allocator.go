package deployer

import (
	"context"
	"fmt"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"github.com/mitte-sh/mitte/pkg/logger"
)

const (
	// Port range for mitte apps: 30000-39999
	minPort = 30000
	maxPort = 39999
)

var (
	portMutex sync.Mutex
	usedPorts = make(map[int]bool)
)

// allocatePort finds an available port in the range 30000-39999
func allocatePort(ctx context.Context, cli *client.Client) (int, error) {
	portMutex.Lock()
	defer portMutex.Unlock()

	// First, scan existing containers to see which ports are in use
	if err := scanUsedPorts(ctx, cli); err != nil {
		return 0, err
	}

	// Find first available port
	for port := minPort; port <= maxPort; port++ {
		if !usedPorts[port] {
			usedPorts[port] = true
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available ports in range %d-%d", minPort, maxPort)
}

// scanUsedPorts scans all Docker containers to see which ports are in use
func scanUsedPorts(ctx context.Context, cli *client.Client) error {
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	// Reset used ports map
	usedPorts = make(map[int]bool)

	for _, c := range containers {
		for _, p := range c.Ports {
			if p.PublicPort >= minPort && p.PublicPort <= maxPort {
				usedPorts[int(p.PublicPort)] = true
			}
		}
	}

	return nil
}

// getDeterministicPort returns a deterministic port for an app
// Uses a simple hash of the app name to generate a port in our range
func getDeterministicPort(appName string) int {
	// Simple hash function to convert app name to a port
	hash := 0
	for _, char := range appName {
		hash = (hash*31 + int(char)) % (maxPort - minPort + 1)
	}
	return minPort + hash
}

// GetAppPort returns a port for an app, trying deterministic first, then fallback to allocation
func GetAppPort(ctx context.Context, cli *client.Client, appName string) (int, error) {
	// Try deterministic port first
	deterministicPort := getDeterministicPort(appName)

	// Check if deterministic port is available
	portMutex.Lock()
	defer portMutex.Unlock()

	if err := scanUsedPorts(ctx, cli); err != nil {
		return 0, err
	}

	if !usedPorts[deterministicPort] {
		usedPorts[deterministicPort] = true
		logger.Info(fmt.Sprintf("Using deterministic port %d for %s", deterministicPort, appName))
		return deterministicPort, nil
	}

	// Deterministic port is taken, find next available
	logger.Info(fmt.Sprintf("Deterministic port %d for %s is in use, finding alternative", deterministicPort, appName))
	for port := deterministicPort + 1; port <= maxPort; port++ {
		if !usedPorts[port] {
			usedPorts[port] = true
			logger.Info(fmt.Sprintf("Using alternative port %d for %s", port, appName))
			return port, nil
		}
	}

	// Wrap around to beginning of range
	for port := minPort; port < deterministicPort; port++ {
		if !usedPorts[port] {
			usedPorts[port] = true
			logger.Info(fmt.Sprintf("Using wrapped port %d for %s", port, appName))
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available ports for app %s", appName)
}
