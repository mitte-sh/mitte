package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const mitteBinaryPath = "/usr/bin/mitte"

const mitteSystemUser = "mitte"

// keysCmd represents the base command for key management. It doesn't do anything
// on its own, but it provides a namespace for subcommands like 'add' and 'list'.
var keysCmd = &cobra.Command{
	Use:     "keys",
	Short:   "Manage SSH keys for git push deployments",
	Aliases: []string{"key"},
}

// keysAddCmd represents the 'keys add' command
var keysAddCmd = &cobra.Command{
	Use:   "add <key-name>",
	Short: "Adds a public SSH key to allow git pushes",
	Long: `Adds a public SSH key to the 'mitte' user's authorized_keys file.
This command must be run as root. The public key data should be piped
to this command via standard input.

Example:
  KEY=$(cat ~/.ssh/id_rsa.pub); ssh -t your-user@your-server.com "echo '$KEY' | sudo mitte keys add my-laptop"`,

	Args: cobra.ExactArgs(1),
	Run:  runKeysAdd,
}

func runKeysAdd(cmd *cobra.Command, args []string) {
	keyName := args[0]

	// --- 1. Pre-flight Checks ---
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "Error: this command must be run as root.")
		os.Exit(1)
	}

	// --- 2. Read public key from standard input ---
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		fmt.Fprintln(os.Stderr, "Error: Public key data must be piped to this command via stdin.")
		os.Exit(1)
	}

	reader := bufio.NewReader(os.Stdin)
	publicKeyBytes, err := io.ReadAll(reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to read public key from stdin: %v\n", err)
		os.Exit(1)
	}
	publicKey := strings.TrimSpace(string(publicKeyBytes))

	// --- 3. Validate the key (basic check) ---
	if !strings.HasPrefix(publicKey, "ssh-rsa") && !strings.HasPrefix(publicKey, "ssh-ed25519") && !strings.HasPrefix(publicKey, "ecdsa-sha2-nistp") {
		fmt.Fprintf(os.Stderr, "Error: Invalid or unsupported public key format provided.\nReceived: %s\n", publicKey)
		os.Exit(1)
	}

	// --- 4. Construct the forced command line ---
	// This is the most critical part. It ensures that this key can ONLY be used
	// to trigger our 'mitte git-receive' command.
	forcedCommand := fmt.Sprintf(
		`command="%s ssh-handler",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty`,
		mitteBinaryPath,
	)

	// The final line includes the command, the key, and the user-provided name as a comment.
	finalLine := fmt.Sprintf("%s %s %s\n", forcedCommand, publicKey, keyName)

	// --- 5. Append the line to the authorized_keys file ---
	homeDir, err := os.UserHomeDir()
	if os.Getenv("SUDO_USER") != "" {
		homeDir = filepath.Join("/home", mitteSystemUser)
	} else if os.Geteuid() == 0 {
		homeDir = filepath.Join("/home", mitteSystemUser)
	}

	keysFile := filepath.Join(homeDir, ".ssh", "authorized_keys")

	fmt.Printf("Appending key to %s...\n", keysFile)

	f, err := os.OpenFile(keysFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to open authorized_keys file at %s: %v\n", keysFile, err)
		fmt.Fprintln(os.Stderr, "Please ensure 'mitte setup' has been run successfully.")
		os.Exit(1)
	}
	defer f.Close()

	if _, err := f.WriteString(finalLine); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to write to authorized_keys file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Success! Key '%s' added for user '%s'.\n", keyName, mitteSystemUser)
	fmt.Println("You can now add a git remote and push to this server.")
}

func init() {
	keysCmd.AddCommand(keysAddCmd)
	rootCmd.AddCommand(keysCmd)
}
