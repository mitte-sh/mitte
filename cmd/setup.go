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

		// --- 7. Install Version Control System ---
		fmt.Println("\n-- Installing Version Control System --")
		installVersionControlSystem(versionControlSystem)

		// --- 8. Final Steps ---
		fmt.Println("\n-- Finalizing --")
		installMitteBinary()

		fmt.Println("\n✅ Mitte setup complete!")
		fmt.Println("\nYour server is now ready to host applications.")
		fmt.Println("Next step: Add your SSH key using 'mitte keys:add <name>'")
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
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		return fmt.Errorf("failed to copy binary: %w", err)
	}

	err = os.Chmod(destPath, 0755)
	if err != nil {
		return fmt.Errorf("failed to set executable permissions: %w", err)
	}

	fmt.Printf("mitte installed successfully to %s\n", destPath)
	return nil
}

// enableAdminMode ensures that the Caddy admin API is enabled in the Caddyfile.
// It parses /etc/caddy/Caddyfile and adds the 'admin' directive if it's not present.
// This function is idempotent: running it multiple times has the same effect as running it once.
func enableAdminMode() {
	const caddyfilePath = "/etc/caddy/Caddyfile"
	const adminDirectiveToAdd = "admin localhost:2019"

	fmt.Println("INFO: Checking Caddy configuration at", caddyfilePath)

	contentBytes, err := os.ReadFile(caddyfilePath)

	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("INFO: Caddyfile not found. Creating a new one with admin mode...\n")

			newContent := adminDirectiveToAdd + "\n"

			err = os.WriteFile(caddyfilePath, []byte(newContent), 0644)
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

	isAdminDirectivePresent := false
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		if strings.HasPrefix(trimmedLine, "admin ") {
			isAdminDirectivePresent = true
			fmt.Printf("INFO: Admin mode is already configured: \"%s\"\n", trimmedLine)
			break
		}
	}

	if !isAdminDirectivePresent {
		fmt.Println("INFO: Admin directive not found. Prepending it to the Caddyfile...")

		newContent := adminDirectiveToAdd + "\n"
		if content != "" {
			newContent += "\n" + content
		}

		err = os.WriteFile(caddyfilePath, []byte(newContent), 0644)
		if err != nil {
			log.Fatalf("FATAL: Failed to write updated Caddyfile: %v", err)
		}

		fmt.Println("SUCCESS: Successfully enabled admin mode in Caddyfile.")
	} else {
		fmt.Println("INFO: No changes needed.")
	}
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
		fmt.Println("Error creating Caddy file:", err)
		os.Exit(1)
	}

	if err := runCommand("chown", "root:root", "/etc/caddy/Caddyfile"); err != nil {
		fmt.Println("Error creating Caddy file:", err)
		os.Exit(1)
	}

	enableAdminMode()

	if err := runCommand("systemctl", "reload", proxy); err != nil {
		fmt.Println("Error reloading "+proxy+":", err)
		os.Exit(1)
	}
}
