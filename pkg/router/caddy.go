package router

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const caddyAdminAPI = "http://localhost:2019"

const baseDomain = "hh.com.py"

// CaddyServerConfig maps to a server object in Caddy's config.
// We use pointers and `omitempty` so we can differentiate between a field
// being empty and not existing in the original JSON.
type CaddyServerConfig struct {
	Listen         []string        `json:"listen,omitempty"`
	AutomaticHTTPS *AutomaticHTTPS `json:"automatic_https,omitempty"`
	// We don't need to parse routes for this operation, so we can leave it generic.
	Routes []any `json:"routes,omitempty"`
}

// AutomaticHTTPS maps to the automatic_https object.
type AutomaticHTTPS struct {
	Automate []string `json:"automate,omitempty"`
}

const caddyServerName = "mitte"

const caddyfileDir = "/etc/caddy/Caddyfile.d"

// CreateRouteFile creates a new Caddyfile in the Caddyfile.d directory
// to route traffic for a given app, and then reloads Caddy.
func CreateRouteFile(appName, hostPort string) error {
	appURL := fmt.Sprintf("%s.%s", appName, baseDomain)
	fmt.Fprintf(os.Stderr, "-----> Creating Caddy route file for %s -> localhost:%s\n", appURL, hostPort)

	// Ensure the directory for Caddyfiles exists using sudo.
	if err := exec.Command("sudo", "mkdir", "-p", caddyfileDir).Run(); err != nil {
		return fmt.Errorf("failed to create caddyfile directory with sudo: %w", err)
	}

	filePath := filepath.Join(caddyfileDir, fmt.Sprintf("%s.caddyfile", appName))
	content := fmt.Sprintf("%s {\n\treverse_proxy localhost:%s\n}", appURL, hostPort)

	// Write the file using sudo and tee.
	cmd := exec.Command("sudo", "tee", filePath)
	cmd.Stdin = strings.NewReader(content)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write route file %s with sudo: %w", filePath, err)
	}
	fmt.Fprintf(os.Stderr, "-----> Route file %s created successfully.\n", filePath)

	// Reload Caddy to apply changes
	return reloadCaddy()
}

// reloadCaddy reloads the Caddy configuration using systemctl.
func reloadCaddy() error {
	fmt.Fprintln(os.Stderr, "-----> Reloading Caddy configuration...")
	cmd := exec.Command("sudo", "systemctl", "reload", "caddy")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to reload caddy: %w\nOutput: %s", err, string(output))
	}
	fmt.Fprintln(os.Stderr, "-----> Caddy reloaded successfully.")
	return nil
}

// UpdateRouteFile updates an existing Caddyfile in the Caddyfile.d directory
// to route traffic for a given app, and then reloads Caddy.
func UpdateRouteFile(appName, hostPort string) error {
	appURL := fmt.Sprintf("%s.%s", appName, baseDomain)
	fmt.Fprintf(os.Stderr, "-----> Updating Caddy route file for %s -> localhost:%s\n", appURL, hostPort)

	filePath := filepath.Join(caddyfileDir, fmt.Sprintf("%s.caddyfile", appName))
	content := fmt.Sprintf("%s {\n\treverse_proxy localhost:%s\n}", appURL, hostPort)

	// Write/overwrite the file using sudo and tee.
	cmd := exec.Command("sudo", "tee", filePath)
	cmd.Stdin = strings.NewReader(content)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write route file %s with sudo: %w", filePath, err)
	}
	fmt.Fprintf(os.Stderr, "-----> Route file %s updated successfully.\n", filePath)

	// Reload Caddy to apply changes
	return reloadCaddy()
}

// DeleteRouteFile removes a Caddyfile for a given app and reloads Caddy.
func DeleteRouteFile(appName string) error {
	fmt.Fprintf(os.Stderr, "-----> Deleting Caddy route file for %s\n", appName)
	filePath := filepath.Join(caddyfileDir, fmt.Sprintf("%s.caddyfile", appName))

	// Use `rm -f` to avoid an error if the file doesn't exist.
	cmd := exec.Command("sudo", "rm", "-f", filePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to delete route file %s with sudo: %w\nOutput: %s", filePath, err, string(output))
	}
	fmt.Fprintf(os.Stderr, "-----> Route file %s deleted successfully.\n", filePath)

	// Reload Caddy to apply changes
	return reloadCaddy()
}

// RouteExistsFile checks if a Caddyfile for a given app exists.
func RouteExistsFile(appName string) (bool, error) {
	filePath := filepath.Join(caddyfileDir, fmt.Sprintf("%s.caddyfile", appName))
	cmd := exec.Command("sudo", "stat", filePath)

	// We don't care about the output, just the exit code. Discard it.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	err := cmd.Run()
	if err == nil {
		// Exit code 0: success, file exists.
		return true, nil
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		// `stat` command returns exit code 1 if the file does not exist.
		if exitErr.ExitCode() == 1 {
			return false, nil // File does not exist, which is not an application error.
		}
	}

	// For any other error (e.g., sudo permission denied, command not found)
	// or an unexpected exit code, we return the error.
	return false, fmt.Errorf("error checking route file %s with sudo: %w", filePath, err)
}
