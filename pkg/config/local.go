package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type LocalConfig struct {
	RemoteSSH string `json:"remote_ssh"` // e.g., "mitte@example.com"
}

// configPath returns the full path to the local config file.
func configPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "mitte", "config.json"), nil
}

// LoadLocal reads the local config file.
func LoadLocal() (*LocalConfig, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	if _, err = os.Stat(path); os.IsNotExist(err) {
		// Return an empty config if the file doesn't exist
		return &LocalConfig{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg LocalConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveLocal writes the local config file.
func (c *LocalConfig) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}

	// Ensure the directory exists
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
