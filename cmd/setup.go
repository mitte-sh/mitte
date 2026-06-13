package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/logger"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Prepares a server to be a mitte host. Must be run as root.",
	Long: `The setup command installs all necessary dependencies like Docker and Caddy,
and configures the 'mitte' system user and SSH access. This is the first
command that should be run on a fresh server.`,
	Run: func(cmd *cobra.Command, args []string) {
		selectedProxy := "caddy"
		containerizationPlatform := "docker"
		versionControlSystem := "git"

		// --- 1. Pre-flight Checks ---
		logger.Info("🚀 Starting mitte setup...")
		if os.Geteuid() != 0 {
			logger.Error("Error: 'mitte setup' must be run as root or with sudo.")
			os.Exit(1)
		}

		// --- 2. Install OS Updates ---
		logger.Info("-- Installing OS Updates --")
		installOSUpdate()

		// --- 3. Install Container Runtime ---
		logger.Info("-- Installing Container Runtime --")
		installContainerRuntime(containerizationPlatform)

		// --- 4. Create 'mitte' User and Environment ---
		logger.Info("-- Configuring 'mitte' user and environment --")
		createUser()

		// --- 5. Configure SSH ---
		logger.Info("-- Configuring SSH for git push deployment --")
		configureSSH()

		// --- 6. Install and Configure Reverse Proxy ---
		logger.Info("-- Installing and configuring Reverse Proxy --")
		installReverseProxy(selectedProxy)
		configureReverseProxy(selectedProxy)

		// --- 7. Configure Sudoers ---
		logger.Info("-- Configuring sudoers for 'mitte' user --")
		configureSudoers()

		// --- 8. Install Version Control System ---
		logger.Info("-- Installing Version Control System --")
		installVersionControlSystem(versionControlSystem)

		// --- 9. Install Buildpack CLI ---
		logger.Info("-- Installing Buildpack CLI (pack) --")
		installBuildpackCLI()

		// --- 10. Configure Base Domain ---
		logger.Info("-- Configuring Base Domain --")
		if err := configureBaseDomain(); err != nil {
			logger.Fatal("Error configuring base domain", "err", err)
		}

		// --- 11. Create Mitte's Docker Network ---
		logger.Info("-- Creating 'mitte' Docker network --")
		err := exec.Command("docker", "network", "inspect", "mitte").Run()
		if err != nil {
			logger.Info("Network 'mitte' not found, creating...")
			if err := runCommand("docker", "network", "create", "mitte"); err != nil {
				logger.Error("Failed to create 'mitte' network", "err", err)
				os.Exit(1)
			}
		} else {
			logger.Info("   Network 'mitte' already exists.")
		}

		// --- 12. Install Container Watcher Service ---
		logger.Info("-- Installing Container Watcher Service --")
		installContainerWatcher()

		// --- 13. Optional: Set up Authentication ---
		logger.Info("-- Optional: Authentication Service --")
		fmt.Print("Do you want to set up the authentication service? (y/N): ")
		reader := bufio.NewReader(os.Stdin)
		setupAuth, _ := reader.ReadString('\n')
		setupAuth = strings.TrimSpace(strings.ToLower(setupAuth))
		if setupAuth == "y" || setupAuth == "yes" {
			logger.Info("Run 'mitte auth setup' as the mitte user to configure authentication.")
		} else {
			logger.Info("Skipping authentication setup. You can run 'mitte auth setup' later.")
		}

		// --- 14. Final Steps ---
		logger.Info("-- Finalizing --")
		installMitteBinary()

		logger.Info("✅ Mitte setup complete!")
		logger.Info("Your server is now ready to host applications.")
		logger.Info("Next step: Add your SSH key using 'mitte keys add <name>'")
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func getOS() (string, error) {
	distributionID := ""
	file, err := os.Open("/etc/os-release")
	if err != nil {
		logger.Error("Error opening file", "err", err)
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "ID=") {
			// Extract the distribution ID
			distributionID = strings.TrimPrefix(line, "ID=")
			distributionID = strings.Trim(distributionID, "\"") // Remove quotes
			logger.Info(fmt.Sprintf("Distribution ID: %s", distributionID))
		} else if strings.HasPrefix(line, "NAME=") {
			// Extract the distribution name
			distributionName := strings.TrimPrefix(line, "NAME=")
			distributionName = strings.Trim(distributionName, "\"") // Remove quotes
			logger.Info(fmt.Sprintf("Distribution Name: %s", distributionName))
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Error("Error reading file", "err", err)
	}

	return distributionID, nil
}

func installOSUpdate() {
	osID, err := getOS()
	if err != nil {
		logger.Error("Error getting OS", "err", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if err := runCommand("dnf", "update", "-y"); err != nil {
			logger.Error("Error updating dnf", "err", err)
			os.Exit(1)
		}
		if err := runCommand("dnf", "install", "-y", "git", "curl", "ca-certificates"); err != nil {
			logger.Error("Error installing dependencies", "err", err)
			os.Exit(1)
		}
	}

	if osID == "ubuntu" {
		if err := runCommand("apt-get", "update"); err != nil {
			logger.Error("Error updating apt", "err", err)
			os.Exit(1)
		}
		if err := runCommand("apt-get", "install", "-y", "git", "curl", "ca-certificates"); err != nil {
			logger.Error("Error installing dependencies", "err", err)
			os.Exit(1)
		}
	}
}

func installContainerRuntime(containerTool string) {
	if containerTool != "docker" && containerTool != "podman" {
		logger.Error("Unsupported container runtime", "containerTool", containerTool)
		os.Exit(1)
	}

	osID, err := getOS()
	if err != nil {
		logger.Error("Error getting OS", "err", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if containerTool == "docker" {
			// Remove any existing Docker packages to avoid conflicts
			if err := runCommand("dnf", "remove", "-y", "docker", "docker-client", "docker-client-latest", "docker-common", "docker-latest", "docker-latest-logrotate", "docker-logrotate", "docker-engine", "docker-ce", "docker-ce-cli", "containerd.io"); err != nil {
				logger.Warn("Failed to remove existing Docker packages", "err", err)
			}

			if err := runCommand("dnf", "config-manager", "--add-repo", "https://download.docker.com/linux/centos/docker-ce.repo"); err != nil {
				logger.Error("Error adding Docker repository", "err", err)
				os.Exit(1)
			}

			// Clean cache and update to get latest versions
			if err := runCommand("dnf", "clean", "all"); err != nil {
				logger.Warn("Failed to clean DNF cache", "err", err)
			}

			if err := runCommand("dnf", "makecache"); err != nil {
				logger.Warn("Failed to update DNF cache", "err", err)
			}

			// Install latest Docker version
			if err := runCommand("dnf", "install", "-y", "--refresh", "docker-ce", "docker-ce-cli", "containerd.io", "--nobest"); err != nil {
				logger.Error("Error installing Docker", "err", err)
				os.Exit(1)
			}

			if err := runCommand("systemctl", "enable", "--now", "docker"); err != nil {
				logger.Error("Error enabling Docker", "err", err)
				os.Exit(1)
			}
		}
	}

	if osID == "ubuntu" {
		if containerTool == "docker" {
			if err := runCommand("curl", "-fsSL", "https://get.docker.com", "-o", "get-docker.sh"); err != nil {
				logger.Error("Error downloading Docker", "err", err)
				os.Exit(1)
			}

			if err := runCommand("sh", "get-docker.sh"); err != nil {
				logger.Error("Error installing Docker", "err", err)
				os.Exit(1)
			}

			if err := runCommand("systemctl", "enable", "--now", "docker"); err != nil {
				logger.Error("Error enabling Docker", "err", err)
				os.Exit(1)
			}

		}
	}
}

func installReverseProxy(proxy string) {
	if proxy != "caddy" {
		logger.Error("Unsupported reverse proxy", "proxy", proxy)
		os.Exit(1)
	}

	osID, err := getOS()
	if err != nil {
		logger.Error("Error getting OS", "err", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if err := runCommand("dnf", "install", "-y", proxy); err != nil {
			logger.Error("Error installing proxy", "proxy", proxy, "err", err)
			os.Exit(1)
		}
	}

	if osID == "ubuntu" {
		if err := runCommand("apt-get", "install", "-y", proxy); err != nil {
			logger.Error("Error installing proxy", "proxy", proxy, "err", err)
			os.Exit(1)
		}
	}
}

func installVersionControlSystem(vcs string) {
	osID, err := getOS()
	if err != nil {
		logger.Error("Error getting OS", "err", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if err := runCommand("dnf", "install", "-y", vcs); err != nil {
			logger.Error("Error installing VCS", "vcs", vcs, "err", err)
			os.Exit(1)
		}
	}

	if osID == "ubuntu" {
		if err := runCommand("apt-get", "install", "-y", vcs); err != nil {
			logger.Error("Error installing VCS", "vcs", vcs, "err", err)
			os.Exit(1)
		}
	}
}

func installBuildpackCLI() {
	// Check if pack CLI is already installed
	if _, err := exec.LookPath("pack"); err == nil {
		logger.Info("   pack CLI is already installed")
		return
	}

	logger.Info("   Installing pack CLI...")

	// For Rocky Linux, use a direct binary download approach
	// The official script has issues on some systems
	// Try to find a pack CLI version that works with newer Docker
	// v0.39.0+ includes Docker API version negotiation (fixes Docker 29.x compatibility)
	// Try newer versions first for better Docker 29.x compatibility
	versions := []string{"v0.40.0", "v0.39.1", "v0.39.0", "v0.38.2", "v0.38.1", "v0.37.0"}
	var url string
	var selectedVersion string

	for _, v := range versions {
		testURL := fmt.Sprintf("https://github.com/buildpacks/pack/releases/download/%s/pack-%s-linux.tgz", v, v)
		// Check if the URL exists
		cmd := exec.Command("curl", "-sSL", "-I", "-f", "-L", testURL)
		if cmd.Run() == nil {
			url = testURL
			selectedVersion = v
			break
		}
	}

	if url == "" {
		logger.Error("Could not find a valid pack CLI release")
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("   Using pack CLI version: %s", selectedVersion))

	// Download the tarball
	if err := runCommand("curl", "-sSL", "-L", url, "-o", "/tmp/pack.tgz"); err != nil {
		logger.Error("Error downloading pack CLI", "err", err)
		os.Exit(1)
	}

	// Create a temporary directory for extraction
	tmpDir := "/tmp/pack-install"
	if err := os.RemoveAll(tmpDir); err != nil {
		logger.Warn("Failed to clean up temp directory", "err", err)
	}
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		logger.Error("Error creating temp directory", "err", err)
		os.Exit(1)
	}

	// Extract the tarball
	if err := runCommand("tar", "-xzf", "/tmp/pack.tgz", "-C", tmpDir); err != nil {
		logger.Error("Error extracting pack CLI", "err", err)
		os.Exit(1)
	}

	// List extracted files for debugging
	listCmd := exec.Command("ls", "-la", tmpDir)
	if output, err := listCmd.Output(); err == nil {
		logger.Info(fmt.Sprintf("   Extracted files in %s:\n%s", tmpDir, string(output)))
	}

	// Find and install the binary
	// First, check what was extracted
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		logger.Error("Error reading temp directory", "err", err)
		os.Exit(1)
	}

	var packBinary string
	found := false

	// Look for the pack binary
	for _, entry := range entries {
		if entry.Name() == "pack" && !entry.IsDir() {
			packBinary = filepath.Join(tmpDir, entry.Name())
			found = true
			break
		}
		if entry.IsDir() && strings.Contains(entry.Name(), "pack") {
			// Check inside directory
			dirPath := filepath.Join(tmpDir, entry.Name())
			subEntries, err := os.ReadDir(dirPath)
			if err != nil {
				continue
			}
			for _, subEntry := range subEntries {
				if subEntry.Name() == "pack" && !subEntry.IsDir() {
					packBinary = filepath.Join(dirPath, subEntry.Name())
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}

	if !found {
		logger.Error("Could not find pack binary in extracted files")
		os.Exit(1)
	}

	if err := runCommand("cp", packBinary, "/usr/local/bin/pack"); err != nil {
		logger.Error("Error copying pack binary", "err", err)
		os.Exit(1)
	}

	if err := runCommand("chmod", "+x", "/usr/local/bin/pack"); err != nil {
		logger.Error("Error setting executable permissions", "err", err)
		os.Exit(1)
	}

	// Debug: Check if the file exists and is executable
	if stat, err := os.Stat("/usr/local/bin/pack"); err != nil {
		logger.Error("pack binary not found at /usr/local/bin/pack", "err", err)
		os.Exit(1)
	} else {
		logger.Info(fmt.Sprintf("   pack binary installed: size=%d, mode=%v", stat.Size(), stat.Mode()))
		// Check if it's executable
		if stat.Mode()&0111 == 0 {
			logger.Error("pack binary is not executable")
			os.Exit(1)
		}
	}

	// Clean up
	if err := os.RemoveAll(tmpDir); err != nil {
		logger.Warn("Failed to clean up temp directory", "err", err)
	}
	if err := os.Remove("/tmp/pack.tgz"); err != nil {
		logger.Warn("Failed to clean up pack archive", "err", err)
	}

	// Verify installation by trying to run the binary directly
	// Instead of using exec.LookPath(), which might have PATH issues
	cmd := exec.Command("/usr/local/bin/pack", "--version")
	if output, err := cmd.Output(); err != nil {
		logger.Error("pack CLI verification failed", "err", err)
		// Try to get stderr for more info
		if exitErr, ok := err.(*exec.ExitError); ok {
			logger.Error(fmt.Sprintf("Stderr: %s", exitErr.Stderr))
		}
		os.Exit(1)
	} else {
		logger.Info(fmt.Sprintf("   ✅ pack CLI installed successfully: %s", string(output)))
	}
}

func createUser() {
	osID, err := getOS()
	if err != nil {
		logger.Error("Error getting OS", "err", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if err := runCommand("useradd", "--system", "--shell", "/bin/bash", "--home", "/home/mitte", "--create-home", "mitte", "--groups", "docker"); err != nil {
			logger.Warn("'mitte' user may already exist. Skipping.", "err", err)
		}

		if err := runCommand("passwd", "-l", "mitte"); err != nil {
			logger.Error("Error locking user", "err", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/var/lib/mitte/apps"); err != nil {
			logger.Error("Error creating apps directory", "err", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/var/lib/mitte/repos"); err != nil {
			logger.Error("Error creating repos directory", "err", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/etc/mitte"); err != nil {
			logger.Error("Error creating configuration directory", "err", err)
			os.Exit(1)
		}

		if err := runCommand("chown", "-R", "mitte:mitte", "/var/lib/mitte", "/home/mitte", "/etc/mitte"); err != nil {
			logger.Error("Error creating configuration directory", "err", err)
			os.Exit(1)
		}
	}

	if osID == "ubuntu" {
		if err := runCommand("adduser", "--system", "--shell", "/bin/bash", "--home", "/home/mitte", "--create-home", "mitte"); err != nil {
			logger.Warn("'mitte' user may already exist. Skipping.", "err", err)
		}

		if err := runCommand("usermod", "-aG", "docker", "mitte"); err != nil {
			logger.Error("Error adding 'mitte' user to docker group", "err", err)
			os.Exit(1)
		}

		if err := runCommand("passwd", "-l", "mitte"); err != nil {
			logger.Error("Error locking user", "err", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/var/lib/mitte/apps"); err != nil {
			logger.Error("Error creating apps directory", "err", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/var/lib/mitte/repos"); err != nil {
			logger.Error("Error creating repos directory", "err", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/etc/mitte"); err != nil {
			logger.Error("Error creating configuration directory", "err", err)
			os.Exit(1)
		}

		if err := runCommand("chown", "-R", "mitte:mitte", "/var/lib/mitte", "/home/mitte", "/etc/mitte"); err != nil {
			logger.Error("Error creating configuration directory", "err", err)
			os.Exit(1)
		}
	}
}

func configureSudoers() {
	// Chown permissions
	sudoersChownContent := "mitte ALL=(ALL) NOPASSWD: /usr/bin/chown\n"
	sudoersChownFilePath := "/etc/sudoers.d/mitte-chown"
	if err := os.WriteFile(sudoersChownFilePath, []byte(sudoersChownContent), 0440); err != nil {
		logger.Error("Error creating sudoers file", "path", sudoersChownFilePath, "err", err)
		os.Exit(1)
	}

	// Caddy-related permissions
	sudoersCaddyContent := "mitte ALL=(ALL) NOPASSWD: /usr/bin/mkdir -p /etc/caddy/Caddyfile.d\n" +
		"mitte ALL=(ALL) NOPASSWD: /usr/bin/tee /etc/caddy/Caddyfile.d/*\n" +
		"mitte ALL=(ALL) NOPASSWD: /usr/bin/systemctl reload caddy\n" +
		"mitte ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart mitte-watcher\n" +
		"mitte ALL=(ALL) NOPASSWD: /usr/bin/systemctl status mitte-watcher\n"
	sudoersCaddyFilePath := "/etc/sudoers.d/mitte-caddy"
	if err := os.WriteFile(sudoersCaddyFilePath, []byte(sudoersCaddyContent), 0440); err != nil {
		logger.Error("Error creating sudoers file", "path", sudoersCaddyFilePath, "err", err)
		os.Exit(1)
	}

	// Mitte command permissions
	sudoersMitteContent := "mitte ALL=(ALL) NOPASSWD: /usr/bin/mitte\n"
	sudoersMitteFilePath := "/etc/sudoers.d/mitte-cmd"
	if err := os.WriteFile(sudoersMitteFilePath, []byte(sudoersMitteContent), 0440); err != nil {
		logger.Error("Error creating sudoers file", "path", sudoersMitteFilePath, "err", err)
		os.Exit(1)
	}

	logger.Info("Sudoers configured for 'mitte' user.")
}

func configureReverseProxy(proxy string) {
	osID, err := getOS()
	if err != nil {
		logger.Error("Error getting OS", "err", err)
		os.Exit(1)
	}

	if proxy != "caddy" {
		logger.Error("Unsupported reverse proxy", "proxy", proxy)
		os.Exit(1)
	}

	if osID == "rocky" || osID == "ubuntu" {
		installAndConfigureCaddy()
	}
}

func installMitteBinary() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	srcFile, err := os.Open(exePath)
	if err != nil {
		return fmt.Errorf("failed to open source executable: %w", err)
	}
	defer srcFile.Close()

	destPath := "/usr/bin/mitte"
	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}

	_, copyErr := io.Copy(destFile, srcFile)
	closeErr := destFile.Close()

	if copyErr != nil {
		return fmt.Errorf("failed to copy binary: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close destination file: %w", closeErr)
	}

	err = os.Chmod(destPath, 0755)
	if err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	logger.Info(fmt.Sprintf("mitte installed successfully to %s", destPath))

	symlinkPath := "/usr/bin/mitte"
	// Attempt to remove an existing symlink to make the operation idempotent.
	// Ignore "not exist" errors.
	if err := os.Remove(symlinkPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing symlink at %s: %w", symlinkPath, err)
	}

	if err := os.Symlink(destPath, symlinkPath); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	logger.Info(fmt.Sprintf("Successfully created symlink at %s", symlinkPath))

	return nil
}

// enableAdminMode ensures that the Caddy admin API is enabled in the Caddyfile.
// It checks for an existing admin directive. If not found, it either adds it
// to an existing global options block or creates a new one.
// This function is idempotent.
func enableAdminMode() {
	const caddyfilePath = "/etc/caddy/Caddyfile"
	const adminDirective = "admin localhost:2019"
	const adminBlock = "{\n\t" + adminDirective + "\n}\n"

	logger.Info(fmt.Sprintf("Checking Caddy configuration at %s", caddyfilePath))

	contentBytes, err := os.ReadFile(caddyfilePath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Info("Caddyfile not found. Creating a new one with admin mode...")
			err = os.WriteFile(caddyfilePath, []byte(adminBlock), 0644)
			if err != nil {
				logger.Fatal("Failed to create and write to Caddyfile", "err", err)
			}
			logger.Info("Successfully created Caddyfile with admin mode enabled.")
			return
		}
		logger.Fatal("Failed to read Caddyfile", "err", err)
	}

	content := string(contentBytes)
	lines := strings.Split(content, "\n")

	hasAdminDirective := false
	for _, line := range lines {
		if strings.Contains(strings.TrimSpace(line), "admin ") {
			hasAdminDirective = true
			logger.Info(fmt.Sprintf("Admin mode is already configured: \"%s\"", strings.TrimSpace(line)))
			break
		}
	}

	if hasAdminDirective {
		logger.Info("No changes needed.")
		return
	}

	logger.Info("Admin directive not found. Modifying Caddyfile...")

	firstNonEmptyLineIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			firstNonEmptyLineIdx = i
			break
		}
	}

	var newContent string
	if firstNonEmptyLineIdx != -1 && strings.HasPrefix(strings.TrimSpace(lines[firstNonEmptyLineIdx]), "{") {
		// A global options block exists. Add the admin directive inside it.
		lineWithBrace := lines[firstNonEmptyLineIdx]
		pos := strings.Index(lineWithBrace, "{")
		restOfLine := lineWithBrace[pos+1:]

		var newLines []string
		newLines = append(newLines, lines[:firstNonEmptyLineIdx]...)
		newLines = append(newLines, lineWithBrace[:pos+1])
		newLines = append(newLines, "\t"+adminDirective)
		if strings.TrimSpace(restOfLine) != "" {
			newLines = append(newLines, restOfLine)
		}
		newLines = append(newLines, lines[firstNonEmptyLineIdx+1:]...)
		newContent = strings.Join(newLines, "\n")
	} else {
		// No global options block found, prepend a new one.
		newContent = adminBlock + "\n" + content
	}

	err = os.WriteFile(caddyfilePath, []byte(newContent), 0644)
	if err != nil {
		logger.Fatal("Failed to write updated Caddyfile", "err", err)
	}

	logger.Info("Successfully enabled admin mode in Caddyfile.")
}

func configureSSH() {
	if err := runCommand("mkdir", "-p", "/home/mitte/.ssh"); err != nil {
		logger.Error("Error creating SSH directory", "err", err)
		os.Exit(1)
	}

	if err := runCommand("touch", "/home/mitte/.ssh/authorized_keys"); err != nil {
		logger.Error("Error creating SSH authorized keys file", "err", err)
		os.Exit(1)
	}

	if err := runCommand("chown", "-R", "mitte:mitte", "/home/mitte/.ssh"); err != nil {
		logger.Error("Error creating SSH authorized keys file", "err", err)
		os.Exit(1)
	}

	if err := runCommand("chmod", "700", "/home/mitte/.ssh"); err != nil {
		logger.Error("Error creating SSH authorized keys file", "err", err)
		os.Exit(1)
	}

	if err := runCommand("chmod", "600", "/home/mitte/.ssh/authorized_keys"); err != nil {
		logger.Error("Error creating SSH authorized keys file", "err", err)
		os.Exit(1)
	}
}

func installAndConfigureCaddy() {
	proxy := "caddy"
	if err := runCommand("mkdir", "-p", "/etc/caddy"); err != nil {
		logger.Error("Error creating Caddy directory", "err", err)
		os.Exit(1)
	}

	if err := runCommand("touch", "/etc/caddy/Caddyfile"); err != nil {
		logger.Error("Error creating Caddy file", "err", err)
		os.Exit(1)
	}

	if err := runCommand("chmod", "644", "/etc/caddy/Caddyfile"); err != nil {
		logger.Error("Error setting permissions for Caddyfile", "err", err)
		os.Exit(1)
	}

	if err := runCommand("chown", "root:root", "/etc/caddy/Caddyfile"); err != nil {
		logger.Error("Error setting ownership for Caddyfile", "err", err)
		os.Exit(1)
	}

	enableAdminMode()

	if err := runCommand("systemctl", "enable", proxy); err != nil {
		logger.Error("Error enabling proxy", "proxy", proxy, "err", err)
		os.Exit(1)
	}

	if err := runCommand("systemctl", "reload-or-restart", proxy); err != nil {
		logger.Error("Error starting or reloading proxy", "proxy", proxy, "err", err)
		os.Exit(1)
	}
}

func configureBaseDomain() error {
	fmt.Print("Enter the base domain for your apps (e.g., example.com): ")
	reader := bufio.NewReader(os.Stdin)
	domain, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read domain from input: %w", err)
	}
	domain = strings.TrimSpace(domain)

	if domain == "" {
		return fmt.Errorf("domain cannot be empty")
	}

	domainFilePath := "/etc/mitte/domain"
	if err := os.WriteFile(domainFilePath, []byte(domain), 0644); err != nil {
		return fmt.Errorf("failed to write domain to %s: %w", domainFilePath, err)
	}

	if err := runCommand("chown", "mitte:mitte", domainFilePath); err != nil {
		return fmt.Errorf("failed to set ownership of %s: %w", domainFilePath, err)
	}

	logger.Info(fmt.Sprintf("✅ Base domain set to '%s'.", domain))
	return nil
}

func installContainerWatcher() {
	logger.Info("   Installing container watcher service...")

	// Copy the service file
	serviceContent := `[Unit]
Description=Mitte Docker Container Watcher
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/bin/mitte watch-containers
Restart=always
RestartSec=10
User=root
Group=root
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/mitte /etc/caddy

[Install]
WantedBy=multi-user.target`

	servicePath := "/etc/systemd/system/mitte-watcher.service"
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		logger.Error("Error creating watcher service file", "err", err)
		os.Exit(1)
	}

	// Reload systemd
	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		logger.Error("Error reloading systemd", "err", err)
		os.Exit(1)
	}

	// Enable and start the service
	if err := runCommand("systemctl", "enable", "mitte-watcher.service"); err != nil {
		logger.Error("Error enabling watcher service", "err", err)
		os.Exit(1)
	}

	if err := runCommand("systemctl", "start", "mitte-watcher.service"); err != nil {
		logger.Error("Error starting watcher service", "err", err)
		os.Exit(1)
	}

	logger.Info("   ✅ Container watcher service installed and started")
}
