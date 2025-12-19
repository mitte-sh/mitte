package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const servicesDir = "/var/lib/mitte/services"

type Service struct {
	InstanceName    string `json:"instance_name"`
	Type            string `json:"type"`
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version,omitempty"`
	RootPassword    string `json:"root_password"`
	UserPassword    string `json:"user_password"`
	DatabaseName    string `json:"database_name"`
	InternalHost    string `json:"internal_host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	ConfigFile      string `json:"config_file"`
	LastBackupPath  string `json:"last_backup_path,omitempty"`
	UpgradedAt      string `json:"upgraded_at,omitempty"`

	// Connection pooling configuration
	MaxConnections       int    `json:"max_connections,omitempty"`
	ThreadCacheSize      int    `json:"thread_cache_size,omitempty"`
	TableOpenCache       int    `json:"table_open_cache,omitempty"`
	InnoDBBufferPoolSize string `json:"innodb_buffer_pool_size,omitempty"`
	QueryCacheSize       string `json:"query_cache_size,omitempty"`
	PoolingPreset        string `json:"pooling_preset,omitempty"`
}

// LoadService reads the state file for a specific service instance.
func LoadService(serviceType, instanceName string) (*Service, error) {
	filePath := filepath.Join(servicesDir, serviceType, instanceName+".json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return &Service{
			InstanceName: instanceName,
			Type:         serviceType,
		}, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("could not read service state for %s: %w", instanceName, err)
	}

	var service Service
	if err := json.Unmarshal(data, &service); err != nil {
		return nil, fmt.Errorf("could not parse service state for %s: %w", instanceName, err)
	}
	return &service, nil
}

// Save writes the service's state to a JSON file.
func (s *Service) Save() error {
	dirPath := filepath.Join(servicesDir, s.Type)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("could not create service directory: %w", err)
	}
	filePath := filepath.Join(dirPath, s.InstanceName+".json")

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode service state: %w", err)
	}
	// WARNING: Note: This needs to be run with sudo, which your main.go should handle.
	return os.WriteFile(filePath, data, 0644)
}

// Delete removes the service's state file.
func (s *Service) Delete() error {
	filePath := filepath.Join(servicesDir, s.Type, s.InstanceName+".json")
	return os.Remove(filePath)
}
