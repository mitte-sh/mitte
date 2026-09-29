package state

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const appsDir = "/var/lib/mitte/apps"

// Exists checks whether an app state file already exists on disk.
func Exists(appName string) bool {
	filePath := filepath.Join(appsDir, appName+".json")
	_, err := os.Stat(filePath)
	return err == nil
}

type App struct {
	AppName       string            `json:"app_name"`
	Domains       []string          `json:"domains"`
	EnvVars       map[string]string `json:"env_vars"`
	RawEnv        string            `json:"raw_env,omitempty"`        // Raw environment variables with comments
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
	Internal      bool              `json:"internal,omitempty"`       // Whether the app is internal (not exposed to the internet)
	Auth          *AuthConfig       `json:"auth,omitempty"`           // Auth protection config
	StreamPaths   []string          `json:"stream_paths,omitempty"`   // Path prefixes that require streaming (SSE); get flush_interval -1 in Caddy
}

// AuthConfig holds authentication settings for an app.
type AuthConfig struct {
	Enabled bool   `json:"enabled"`
	Policy  string `json:"policy"` // "one_factor" or "two_factor"
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

	// Write via `sudo tee` so that the file is owned by root, matching the
	// `sudo rm` used elsewhere to delete state files. This lets the SSH
	// `git-receive` flow (which runs as an unprivileged user) persist app
	// state to /var/lib/mitte/apps without needing write access there.
	cmd := exec.Command("sudo", "tee", filePath)
	cmd.Stdin = strings.NewReader(string(data))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("could not write app state for %s: %w\nOutput: %s", a.AppName, err, string(output))
	}
	return nil
}

// SyncEnv synchronizes EnvVars from RawEnv. It parses RawEnv and updates
// the EnvVars map, removing any keys that are no longer present in RawEnv.
func (a *App) SyncEnv() {
	if a.EnvVars == nil {
		a.EnvVars = make(map[string]string)
	}

	// Parse RawEnv
	newVars := make(map[string]string)
	lines := strings.Split(a.RawEnv, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			newVars[parts[0]] = parts[1]
		}
	}

	// Update EnvVars
	a.EnvVars = newVars
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
