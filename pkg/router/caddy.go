package router

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mitte-sh/mitte/pkg/config"
)

const caddyAdminAPI = "http://localhost:2019"

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
	baseDomain, err := config.GetBaseDomain()
	if err != nil {
		return err
	}
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

// SetAppRoutes creates, updates, or deletes the Caddyfile for an app
// to ensure its configuration matches the provided list of domains.
func SetAppRoutes(appName string, domains []string, hostPort string) error {
	return SetAppRoutesWithAuth(appName, domains, hostPort, false, "")
}

// SetAppRoutesWithAuth creates, updates, or deletes the Caddyfile for an app.
// When authEnabled is true, a forward_auth block is added to protect the app.
func SetAppRoutesWithAuth(appName string, domains []string, hostPort string, authEnabled bool, authPolicy string) error {
	// If an app has no domains, its config file should be removed.
	if len(domains) == 0 {
		fmt.Fprintf(os.Stderr, "-----> No domains for '%s'. Removing Caddy route file.\n", appName)
		return DeleteRouteFile(appName) // DeleteRouteFile already reloads Caddy
	}

	fmt.Fprintf(os.Stderr, "-----> Setting Caddy routes for %s: %v -> localhost:%s\n", appName, domains, hostPort)

	// Ensure the directory for Caddyfiles exists.
	if err := exec.Command("sudo", "mkdir", "-p", caddyfileDir).Run(); err != nil {
		return fmt.Errorf("failed to create caddyfile directory with sudo: %w", err)
	}

	filePath := filepath.Join(caddyfileDir, fmt.Sprintf("%s.caddyfile", appName))

	// Join all domains with a space for the Caddyfile header.
	domainHeader := strings.Join(domains, " ")
	content := buildAppCaddyfile(domainHeader, hostPort, authEnabled, authPolicy)

	// Write/overwrite the file using sudo and tee.
	cmd := exec.Command("sudo", "tee", filePath)
	cmd.Stdin = strings.NewReader(content)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write route file %s with sudo: %w", filePath, err)
	}
	fmt.Fprintf(os.Stderr, "-----> Route file %s set successfully.\n", filePath)

	// Reload Caddy to apply changes.
	return reloadCaddy()
}

// buildAppCaddyfile generates the Caddyfile content for an app.
// The authPolicy is not used here because Authelia enforces the policy
// per-domain via its ACL rules in configuration.yml.
func buildAppCaddyfile(domainHeader, hostPort string, authEnabled bool, authPolicy string) string {
	var b strings.Builder

	b.WriteString(domainHeader)
	b.WriteString(" {\n")

	if authEnabled {
		b.WriteString("\tforward_auth localhost:9091 {\n")
		b.WriteString("\t\turi /api/authz/forward-auth\n")
		b.WriteString("\t\tcopy_headers Remote-User Remote-Groups Remote-Name Remote-Email\n")
		b.WriteString("\t}\n")
	}

	b.WriteString(fmt.Sprintf("\treverse_proxy localhost:%s\n", hostPort))
	b.WriteString("}")

	return b.String()
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

// CreateAuthPortalRoute creates a Caddyfile for the auth login portal.
// This routes auth.example.com to the Authelia container.
func CreateAuthPortalRoute(baseDomain string) error {
	authDomain := fmt.Sprintf("auth.%s", baseDomain)
	fmt.Fprintf(os.Stderr, "-----> Creating auth portal route for %s\n", authDomain)

	if err := exec.Command("sudo", "mkdir", "-p", caddyfileDir).Run(); err != nil {
		return fmt.Errorf("failed to create caddyfile directory with sudo: %w", err)
	}

	filePath := filepath.Join(caddyfileDir, "mitte-auth.caddyfile")
	content := fmt.Sprintf("%s {\n\treverse_proxy localhost:9091\n}", authDomain)

	cmd := exec.Command("sudo", "tee", filePath)
	cmd.Stdin = strings.NewReader(content)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to write auth portal route file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "-----> Auth portal route file %s created successfully.\n", filePath)

	return reloadCaddy()
}

// DeleteAuthPortalRoute removes the auth login portal Caddyfile.
func DeleteAuthPortalRoute() error {
	fmt.Fprintf(os.Stderr, "-----> Deleting auth portal route file\n")
	filePath := filepath.Join(caddyfileDir, "mitte-auth.caddyfile")

	cmd := exec.Command("sudo", "rm", "-f", filePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to delete auth portal route file: %w\nOutput: %s", err, string(output))
	}
	fmt.Fprintf(os.Stderr, "-----> Auth portal route file deleted successfully.\n")

	return reloadCaddy()
}
