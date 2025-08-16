package cmd

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
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
		fmt.Println("🚀 Starting mitte setup...")
		if os.Geteuid() != 0 {
			fmt.Println("Error: 'mitte setup' must be run as root or with sudo.")
			os.Exit(1)
		}

		// --- 2. Install OS Updates ---
		fmt.Println("\n-- Installing OS Updates --")
		installOSUpdate()

		// --- 3. Install Container Runtime ---
		fmt.Println("\n-- Installing Container Runtime --")
		installContainerRuntime(containerizationPlatform)

		// --- 4. Create 'mitte' User and Environment ---
		fmt.Println("\n-- Configuring 'mitte' user and environment --")
		createUser()

		// --- 5. Configure SSH ---
		fmt.Println("\n-- Configuring SSH for git push deployments --")
		configureSSH()

		// --- 6. Install and Configure Reverse Proxy ---
		fmt.Println("\n-- Installing and configuring Reverse Proxy --")
		installReverseProxy(selectedProxy)
		configureReverseProxy(selectedProxy)

		// --- 7. Configure Sudoers ---
		fmt.Println("\n-- Configuring sudoers for 'mitte' user --")
		configureSudoers()

		// --- 8. Install Version Control System ---
		fmt.Println("\n-- Installing Version Control System --")
		installVersionControlSystem(versionControlSystem)

		// --- 9. Configure Base Domain ---
		fmt.Println("\n-- Configuring Base Domain --")
		if err := configureBaseDomain(); err != nil {
			log.Fatalf("Error configuring base domain: %v", err)
		}

		// --- 10. Final Steps ---
		fmt.Println("\n-- Finalizing --")
		installMitteBinary()

		fmt.Println("\n✅ Mitte setup complete!")
		fmt.Println("\nYour server is now ready to host applications.")
		fmt.Println("Next step: Add your SSH key using 'mitte keys add <name>'")
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func getOS() (string, error) {
	distributionID := ""
	file, err := os.Open("/etc/os-release")
	if err != nil {
		fmt.Println("Error opening file:", err)
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
			fmt.Println("Distribution ID:", distributionID)
		} else if strings.HasPrefix(line, "NAME=") {
			// Extract the distribution name
			distributionName := strings.TrimPrefix(line, "NAME=")
			distributionName = strings.Trim(distributionName, "\"") // Remove quotes
			fmt.Println("Distribution Name:", distributionName)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading file:", err)
	}

	return distributionID, nil
}

func installOSUpdate() {
	osID, err := getOS()
	if err != nil {
		fmt.Println("Error getting OS:", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if err := runCommand("dnf", "update", "-y"); err != nil {
			fmt.Println("Error updating dnf:", err)
			os.Exit(1)
		}
		if err := runCommand("dnf", "install", "-y", "git", "curl", "ca-certificates"); err != nil {
			fmt.Println("Error installing dependencies:", err)
			os.Exit(1)
		}
	}

	if osID == "ubuntu" {
		if err := runCommand("apt-get", "update"); err != nil {
			fmt.Println("Error updating apt:", err)
			os.Exit(1)
		}
		if err := runCommand("apt-get", "install", "-y", "git", "curl", "ca-certificates"); err != nil {
			fmt.Println("Error installing dependencies:", err)
			os.Exit(1)
		}
	}
}

func installContainerRuntime(containerTool string) {
	if containerTool != "docker" && containerTool != "podman" {
		fmt.Println("Error: Unsupported container runtime:", containerTool)
		os.Exit(1)
	}

	osID, err := getOS()
	if err != nil {
		fmt.Println("Error getting OS:", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if containerTool == "docker" {
			if err := runCommand("dnf", "config-manager", "--add-repo", "https://download.docker.com/linux/centos/docker-ce.repo"); err != nil {
				fmt.Println("Error adding Docker repository:", err)
				os.Exit(1)
			}

			if err := runCommand("dnf", "install", "-y", "docker-ce", "docker-ce-cli", "containerd.io"); err != nil {
				fmt.Println("Error installing DNF plugins:", err)
				os.Exit(1)
			}

			if err := runCommand("systemctl", "enable", "--now", "docker"); err != nil {
				fmt.Println("Error enabling Docker:", err)
				os.Exit(1)
			}
		}
	}

	if osID == "ubuntu" {
		if containerTool == "docker" {
			if err := runCommand("curl", "-fsSL", "https://get.docker.com", "-o", "get-docker.sh"); err != nil {
				fmt.Println("Error downloading Docker:", err)
				os.Exit(1)
			}

			if err := runCommand("sh", "get-docker.sh"); err != nil {
				fmt.Println("Error installing Docker:", err)
				os.Exit(1)
			}

			if err := runCommand("systemctl", "enable", "--now", "docker"); err != nil {
				fmt.Println("Error enabling Docker:", err)
				os.Exit(1)
			}

		}
	}
}

func installReverseProxy(proxy string) {
	if proxy != "caddy" {
		fmt.Println("Error: Unsupported reverse proxy:", proxy)
		os.Exit(1)
	}

	osID, err := getOS()
	if err != nil {
		fmt.Println("Error getting OS:", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if err := runCommand("dnf", "install", "-y", proxy); err != nil {
			fmt.Println("Error installing "+proxy+":", err)
			os.Exit(1)
		}
	}

	if osID == "ubuntu" {
		if err := runCommand("apt-get", "install", "-y", proxy); err != nil {
			fmt.Println("Error installing "+proxy+":", err)
			os.Exit(1)
		}
	}
}

func installVersionControlSystem(vcs string) {
	osID, err := getOS()
	if err != nil {
		fmt.Println("Error getting OS:", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if err := runCommand("dnf", "install", "-y", vcs); err != nil {
			fmt.Println("Error installing ", vcs, ":", err)
			os.Exit(1)
		}
	}

	if osID == "ubuntu" {
		if err := runCommand("apt-get", "install", "-y", vcs); err != nil {
			fmt.Println("Error installing ", vcs, ":", err)
			os.Exit(1)
		}
	}
}

func createUser() {
	osID, err := getOS()
	if err != nil {
		fmt.Println("Error getting OS:", err)
		os.Exit(1)
	}

	if osID == "rocky" {
		if err := runCommand("useradd", "--system", "--shell", "/bin/bash", "--home", "/home/mitte", "--create-home", "mitte", "--groups", "docker"); err != nil {
			fmt.Println("Warning: 'mitte' user may already exist. Skipping.", err)
		}

		if err := runCommand("passwd", "-l", "mitte"); err != nil {
			fmt.Println("Error locking user:", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/var/lib/mitte/apps"); err != nil {
			fmt.Println("Error creating apps directory:", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/var/lib/mitte/repos"); err != nil {
			fmt.Println("Error creating repos directory:", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/etc/mitte"); err != nil {
			fmt.Println("Error creating configuration directory:", err)
			os.Exit(1)
		}

		if err := runCommand("chown", "-R", "mitte:mitte", "/var/lib/mitte", "/home/mitte", "/etc/mitte"); err != nil {
			fmt.Println("Error creating configuration directory:", err)
			os.Exit(1)
		}
	}

	if osID == "ubuntu" {
		if err := runCommand("adduser", "--system", "--shell", "/bin/bash", "--home", "/home/mitte", "--create-home", "mitte"); err != nil {
			fmt.Println("Warning: 'mitte' user may already exist. Skipping.", err)
		}

		if err := runCommand("usermod", "-aG", "docker", "mitte"); err != nil {
			fmt.Println("Error adding 'mitte' user to docker group:", err)
			os.Exit(1)
		}

		if err := runCommand("passwd", "-l", "mitte"); err != nil {
			fmt.Println("Error locking user:", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/var/lib/mitte/apps"); err != nil {
			fmt.Println("Error creating apps directory:", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/var/lib/mitte/repos"); err != nil {
			fmt.Println("Error creating repos directory:", err)
			os.Exit(1)
		}

		if err := runCommand("mkdir", "-p", "/etc/mitte"); err != nil {
			fmt.Println("Error creating configuration directory:", err)
			os.Exit(1)
		}

		if err := runCommand("chown", "-R", "mitte:mitte", "/var/lib/mitte", "/home/mitte", "/etc/mitte"); err != nil {
			fmt.Println("Error creating configuration directory:", err)
			os.Exit(1)
		}
	}
}

func configureSudoers() {
	// Chown permissions
	sudoersChownContent := "mitte ALL=(ALL) NOPASSWD: /usr/bin/chown\n"
	sudoersChownFilePath := "/etc/sudoers.d/mitte-chown"
	if err := os.WriteFile(sudoersChownFilePath, []byte(sudoersChownContent), 0440); err != nil {
		fmt.Printf("Error creating sudoers file %s: %v\n", sudoersChownFilePath, err)
		os.Exit(1)
	}

	// Caddy-related permissions
	sudoersCaddyContent := "mitte ALL=(ALL) NOPASSWD: /usr/bin/mkdir -p /etc/caddy/Caddyfile.d\n" +
		"mitte ALL=(ALL) NOPASSWD: /usr/bin/tee /etc/caddy/Caddyfile.d/*\n" +
		"mitte ALL=(ALL) NOPASSWD: /usr/bin/systemctl reload caddy\n"
	sudoersCaddyFilePath := "/etc/sudoers.d/mitte-caddy"
	if err := os.WriteFile(sudoersCaddyFilePath, []byte(sudoersCaddyContent), 0440); err != nil {
		fmt.Printf("Error creating sudoers file %s: %v\n", sudoersCaddyFilePath, err)
		os.Exit(1)
	}

	fmt.Println("Sudoers configured for 'mitte' user.")
}

func configureReverseProxy(proxy string) {
	osID, err := getOS()
	if err != nil {
		fmt.Println("Error getting OS:", err)
		os.Exit(1)
	}

	if proxy != "caddy" {
		fmt.Println("Error: Unsupported reverse proxy:", proxy)
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

	destPath := "/usr/local/bin/mitte"
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

	fmt.Printf("mitte installed successfully to %s\n", destPath)

	symlinkPath := "/usr/bin/mitte"
	// Attempt to remove an existing symlink to make the operation idempotent.
	// Ignore "not exist" errors.
	if err := os.Remove(symlinkPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing symlink at %s: %w", symlinkPath, err)
	}

	if err := os.Symlink(destPath, symlinkPath); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	fmt.Printf("Successfully created symlink at %s\n", symlinkPath)

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

	fmt.Println("INFO: Checking Caddy configuration at", caddyfilePath)

	contentBytes, err := os.ReadFile(caddyfilePath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("INFO: Caddyfile not found. Creating a new one with admin mode...\n")
			err = os.WriteFile(caddyfilePath, []byte(adminBlock), 0644)
			if err != nil {
				log.Fatalf("FATAL: Failed to create and write to Caddyfile: %v\n", err)
			}
			fmt.Println("SUCCESS: Successfully created Caddyfile with admin mode enabled.")
			return
		}
		log.Fatalf("FATAL: Failed to read Caddyfile: %v\n", err)
	}

	content := string(contentBytes)
	lines := strings.Split(content, "\n")

	hasAdminDirective := false
	for _, line := range lines {
		if strings.Contains(strings.TrimSpace(line), "admin ") {
			hasAdminDirective = true
			fmt.Printf("INFO: Admin mode is already configured: \"%s\"\n", strings.TrimSpace(line))
			break
		}
	}

	if hasAdminDirective {
		fmt.Println("INFO: No changes needed.")
		return
	}

	fmt.Println("INFO: Admin directive not found. Modifying Caddyfile...")

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
		log.Fatalf("FATAL: Failed to write updated Caddyfile: %v", err)
	}

	fmt.Println("SUCCESS: Successfully enabled admin mode in Caddyfile.")
}

func configureSSH() {
	if err := runCommand("mkdir", "-p", "/home/mitte/.ssh"); err != nil {
		fmt.Println("Error creating SSH directory:", err)
		os.Exit(1)
	}

	if err := runCommand("touch", "/home/mitte/.ssh/authorized_keys"); err != nil {
		fmt.Println("Error creating SSH authorized keys file:", err)
		os.Exit(1)
	}

	if err := runCommand("chown", "-R", "mitte:mitte", "/home/mitte/.ssh"); err != nil {
		fmt.Println("Error creating SSH authorized keys file:", err)
		os.Exit(1)
	}

	if err := runCommand("chmod", "700", "/home/mitte/.ssh"); err != nil {
		fmt.Println("Error creating SSH authorized keys file:", err)
		os.Exit(1)
	}

	if err := runCommand("chmod", "600", "/home/mitte/.ssh/authorized_keys"); err != nil {
		fmt.Println("Error creating SSH authorized keys file:", err)
		os.Exit(1)
	}
}

func installAndConfigureCaddy() {
	proxy := "caddy"
	if err := runCommand("mkdir", "-p", "/etc/caddy"); err != nil {
		fmt.Println("Error creating Caddy directory:", err)
		os.Exit(1)
	}

	if err := runCommand("touch", "/etc/caddy/Caddyfile"); err != nil {
		fmt.Println("Error creating Caddy file:", err)
		os.Exit(1)
	}

	if err := runCommand("chmod", "644", "/etc/caddy/Caddyfile"); err != nil {
		fmt.Println("Error setting permissions for Caddyfile:", err)
		os.Exit(1)
	}

	if err := runCommand("chown", "root:root", "/etc/caddy/Caddyfile"); err != nil {
		fmt.Println("Error setting ownership for Caddyfile:", err)
		os.Exit(1)
	}

	enableAdminMode()

	if err := runCommand("systemctl", "enable", proxy); err != nil {
		fmt.Println("Error enabling "+proxy+":", err)
		os.Exit(1)
	}

	if err := runCommand("systemctl", "reload-or-restart", proxy); err != nil {
		fmt.Println("Error starting or reloading "+proxy+":", err)
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

	fmt.Printf("✅ Base domain set to '%s'.\n", domain)
	return nil
}
