package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

// A helper to make running commands easier and clearer
func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("▶️ Running: %s %v\n", name, args)
	return cmd.Run()
}

var rootCmd = &cobra.Command{
	Use:   "mitte",
	Short: "Mitte is a minimalist, self-hosted PaaS written in Go.",
	Long: `Mitte | /ˈmit.te/ | v. (Latin) "Send!"

A minimalist, self-hosted Platform-as-a-Service.
It lets you transform any server into your own private cloud deployment platform,
deploying applications with a simple 'git push'.`,
	// This 'Run' function is executed if a user runs 'mitte' with no subcommand.
	// We'll just print the help text in that case.
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

var checkDepsCmd = &cobra.Command{
	Use:   "check-deps",
	Short: "Check if all required dependencies are installed",
	Long: `Check if all required dependencies for Mitte are installed.
This command verifies that Docker, Caddy, Git, and pack CLI are available.
It's useful for troubleshooting deployment issues.`,
	Run: runCheckDeps,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Whoops. There was an error while executing your command: '%s'", err)
		os.Exit(1)
	}
}

func runCheckDeps(cmd *cobra.Command, args []string) {
	fmt.Println("🔍 Checking Mitte dependencies...")
	fmt.Println()

	deps := []struct {
		name string
		path string
		desc string
	}{
		{"Docker", "docker", "Container runtime for building and running applications"},
		{"Caddy", "caddy", "Reverse proxy for routing HTTP traffic"},
		{"Git", "git", "Version control system for code deployment"},
		{"pack CLI", "pack", "Buildpack CLI for building applications (required for buildpack support)"},
	}

	allOk := true
	for _, dep := range deps {
		if _, err := exec.LookPath(dep.path); err != nil {
			fmt.Printf("❌ %s: NOT FOUND\n", dep.name)
			fmt.Printf("   %s\n", dep.desc)
			fmt.Printf("   To install: sudo mitte setup\n")
			allOk = false
		} else {
			fmt.Printf("✅ %s: OK\n", dep.name)
		}
	}

	fmt.Println()
	if allOk {
		fmt.Println("🎉 All dependencies are installed and ready!")
	} else {
		fmt.Println("⚠️  Some dependencies are missing.")
		fmt.Println("   Run 'sudo mitte setup' to install missing dependencies.")
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(checkDepsCmd)
}
