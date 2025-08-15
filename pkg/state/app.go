package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const appsDir = "/var/lib/mitte/apps"

type App struct {
	AppName string            `json:"app_name"`
	Domains []string          `json:"domains"`
	EnvVars map[string]string `json:"env_vars"`
}

func Load(appName string) (*App, error) {
	filePath := filepath.Join(appsDir, appName+".json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// App doesn't exist yet, return a new, empty struct
		return &App{
			AppName: appName,
			Domains: []string{},
			EnvVars: make(map[string]string),
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
