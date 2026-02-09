package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const appsDir = "/var/lib/mitte/apps"

type App struct {
	AppName       string            `json:"app_name"`
	Domains       []string          `json:"domains"`
	EnvVars       map[string]string `json:"env_vars"`
	Image         string            `json:"image,omitempty"`          // Pre-built Docker image
	Volumes       []string          `json:"volumes,omitempty"`        // Volume mounts (host:container)
	Ports         []string          `json:"ports,omitempty"`          // Port mappings (host:container)
	ContainerName string            `json:"container_name,omitempty"` // Custom container name
	Buildpack     string            `json:"buildpack,omitempty"`      // Buildpack to use (CNB)
	BuildpackEnv  map[string]string `json:"buildpack_env,omitempty"`  // Buildpack-specific env vars
	HostPort      string            `json:"host_port,omitempty"`      // Current host port for routing
	Command       []string          `json:"command,omitempty"`        // Custom entrypoint/command
	User          string            `json:"user,omitempty"`           // Custom user to run as
	Disabled      bool              `json:"disabled,omitempty"`       // Whether the app is disabled
}

func Load(appName string) (*App, error) {
	filePath := filepath.Join(appsDir, appName+".json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// App doesn't exist yet, return a new, empty struct
		return &App{
			AppName:       appName,
			Domains:       []string{},
			EnvVars:       make(map[string]string),
			Image:         "",
			Volumes:       []string{},
			Ports:         []string{},
			ContainerName: "",
			Buildpack:     "",
			BuildpackEnv:  make(map[string]string),
			HostPort:      "",
			Command:       []string{},
		}, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("could not read app state for %s: %w", appName, err)
	}

	var app App
	if err := json.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("could not parse app state for %s: %w", appName, err)
	}
	return &app, nil
}

func (a *App) Save() error {
	filePath := filepath.Join(appsDir, a.AppName+".json")
	// ensure the directory exists
	if err := os.MkdirAll(appsDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode app state for %s: %w", a.AppName, err)
	}

	return os.WriteFile(filePath, data, 0644)
}

// ResolveAppName takes a string that is either a short app name or a domain
// and returns the canonical short app name. It returns an empty string and an
// error if no matching app can be found.
func ResolveAppName(nameOrDomain string) (string, error) {
	const appsDir = "/var/lib/mitte/apps"

	// First, check if the provided name is a direct match for an app.
	// This is the most common case and is very fast.
	filePath := filepath.Join(appsDir, nameOrDomain+".json")
	if _, err := os.Stat(filePath); err == nil {
		return nameOrDomain, nil
	}

	// If not a direct match, search through all apps to match by domain.
	files, err := os.ReadDir(appsDir)
	if err != nil {
		return "", fmt.Errorf("could not read apps directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		appName := strings.TrimSuffix(file.Name(), ".json")
		app, err := Load(appName)
		if err != nil {
			// Silently ignore corrupted files during search, but maybe log this.
			continue
		}

		for _, domain := range app.Domains {
			if domain == nameOrDomain {
				// Found a match! Return the app's short name.
				return appName, nil
			}
		}
	}

	// If we finish the loop and find nothing, the app/domain doesn't exist.
	return "", fmt.Errorf("application with name or domain '%s' not found", nameOrDomain)
}
