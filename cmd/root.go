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

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Whoops. There was an error while executing your command: '%s'", err)
		os.Exit(1)
	}
}
