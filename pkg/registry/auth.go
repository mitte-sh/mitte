package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/registry"
)

// RegistryAuthResolver handles registry authentication for image pulls.
type RegistryAuthResolver struct {
	configDir string
}

// NewRegistryAuthResolver creates a new auth resolver with the given Docker config directory.
func NewRegistryAuthResolver(configDir string) *RegistryAuthResolver {
	return &RegistryAuthResolver{
		configDir: configDir,
	}
}

// DefaultResolver creates a resolver using the default Docker config directory.
func DefaultResolver() *RegistryAuthResolver {
	configDir := os.Getenv("DOCKER_CONFIG")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".docker")
	}
	return NewRegistryAuthResolver(configDir)
}

// GetAuthConfigForImage returns the registry auth config for a given image.
// It extracts the registry host from the image name and looks up credentials.
func (r *RegistryAuthResolver) GetAuthConfigForImage(image string) (*registry.AuthConfig, error) {
	registryHost := extractRegistryHost(image)
	if registryHost == "" {
		return &registry.AuthConfig{}, nil // No auth needed for Docker Hub
	}

	// Load Docker config
	configPath := filepath.Join(r.configDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		// Try legacy config location
		configPath = filepath.Join(r.configDir, "config.json")
		data, err = os.ReadFile(configPath)
		if err != nil {
			return &registry.AuthConfig{}, nil
		}
	}

	var dockerConfig struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}

	if err := json.Unmarshal(data, &dockerConfig); err != nil {
		return &registry.AuthConfig{}, nil
	}

	// Try exact match first
	if creds, ok := dockerConfig.Auths[registryHost]; ok {
		return decodeAuth(creds.Auth)
	}

	// Try without port
	hostWithoutPort := strings.TrimPrefix(registryHost, "https://")
	hostWithoutPort = strings.TrimPrefix(hostWithoutPort, "http://")
	hostWithoutPort = strings.Split(hostWithoutPort, ":")[0]

	for key, creds := range dockerConfig.Auths {
		keyHost := strings.TrimPrefix(key, "https://")
		keyHost = strings.TrimPrefix(keyHost, "http://")
		keyHostBase := strings.Split(keyHost, ":")[0]

		if keyHostBase == hostWithoutPort {
			return decodeAuth(creds.Auth)
		}
	}

	return &registry.AuthConfig{}, nil
}

// EncodeRegistryAuth encodes an AuthConfig for use in ImagePull options.
func EncodeRegistryAuth(auth *registry.AuthConfig) (string, error) {
	return registry.EncodeAuthConfig(*auth)
}

// PrivilegeFunc returns a function suitable for PullOptions.PrivilegeFunc
// that dynamically resolves auth for the given image.
func (r *RegistryAuthResolver) PrivilegeFunc(image string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		authConfig, err := r.GetAuthConfigForImage(image)
		if err != nil {
			return "", err
		}
		return registry.EncodeAuthConfig(*authConfig)
	}
}

// extractRegistryHost extracts the registry host from an image name.
// Examples:
//   - "nginx" -> ""
//   - "library/nginx" -> ""
//   - "docker.io/library/nginx" -> "https://index.docker.io"
//   - "ghcr.io/user/repo" -> "https://ghcr.io"
//   - "myregistry.com:5000/user/repo" -> "https://myregistry.com:5000"
func extractRegistryHost(image string) string {
	// Remove tag/digest if present
	image = strings.Split(image, "@")[0]
	image = strings.Split(image, ":")[0]

	parts := strings.Split(image, "/")
	if len(parts) == 1 {
		return "" // No registry, just library image
	}

	registryPart := parts[0]
	if !strings.Contains(registryPart, ".") && registryPart != "localhost" {
		return "" // No registry, just official image name like "library/nginx"
	}

	// It's a registry
	registry := registryPart
	if !strings.HasPrefix(registry, "http://") && !strings.HasPrefix(registry, "https://") {
		registry = "https://" + registry
	}

	return registry
}

// decodeAuth decodes base64 auth string into username/password.
func decodeAuth(authString string) (*registry.AuthConfig, error) {
	if authString == "" {
		return &registry.AuthConfig{}, nil
	}

	decoded, err := base64.StdEncoding.DecodeString(authString)
	if err != nil {
		// Try URL-safe encoding
		decoded, err = base64.URLEncoding.DecodeString(authString)
		if err != nil {
			return nil, fmt.Errorf("failed to decode auth: %w", err)
		}
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return &registry.AuthConfig{}, nil
	}

	return &registry.AuthConfig{
		Username: parts[0],
		Password: parts[1],
	}, nil
}

// ConfigManager handles Docker registry credentials in ~/.docker/config.json
type ConfigManager struct {
	configDir string
}

// NewConfigManager creates a new config manager with the specified Docker config directory.
func NewConfigManager(configDir string) *ConfigManager {
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".docker")
	}
	return &ConfigManager{configDir: configDir}
}

// AddCredentials adds or updates credentials for a registry in Docker config.json
func (m *ConfigManager) AddCredentials(reg, username, password string) error {
	authString := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", username, password)))

	configPath := filepath.Join(m.configDir, "config.json")
	type authEntry struct {
		Auth string `json:"auth"`
	}
	var dockerConfig struct {
		Auths map[string]authEntry `json:"auths"`
	}

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err == nil {
		json.Unmarshal(data, &dockerConfig)
	}

	if dockerConfig.Auths == nil {
		dockerConfig.Auths = make(map[string]authEntry)
	}

	// Normalize registry URL
	registryURL := reg
	if !strings.HasPrefix(registryURL, "https://") && !strings.HasPrefix(registryURL, "http://") {
		registryURL = "https://" + registryURL
	}

	dockerConfig.Auths[registryURL] = authEntry{Auth: authString}

	// Write back
	output, err := json.MarshalIndent(dockerConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(configPath, output, 0644)
}

// RemoveCredentials removes credentials for a registry
func (m *ConfigManager) RemoveCredentials(reg string) error {
	configPath := filepath.Join(m.configDir, "config.json")
	type authEntry struct {
		Auth string `json:"auth"`
	}
	var dockerConfig struct {
		Auths map[string]authEntry `json:"auths"`
	}

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("could not read config: %w", err)
	}

	json.Unmarshal(data, &dockerConfig)

	// Normalize registry URL
	registryURL := reg
	if !strings.HasPrefix(registryURL, "https://") && !strings.HasPrefix(registryURL, "http://") {
		registryURL = "https://" + registryURL
	}

	delete(dockerConfig.Auths, registryURL)

	// Write back
	output, err := json.MarshalIndent(dockerConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(configPath, output, 0644)
}

// ListCredentials returns all configured registry credentials
func (m *ConfigManager) ListCredentials() ([]string, error) {
	configPath := filepath.Join(m.configDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("could not read config: %w", err)
	}

	type authEntry struct {
		Auth string `json:"auth"`
	}
	var dockerConfig struct {
		Auths map[string]authEntry `json:"auths"`
	}

	if err := json.Unmarshal(data, &dockerConfig); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	var registries []string
	for reg := range dockerConfig.Auths {
		registries = append(registries, reg)
	}

	return registries, nil
}

