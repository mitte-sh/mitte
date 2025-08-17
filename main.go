package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mitteapp/mitteapp/cmd"
	"github.com/mitteapp/mitteapp/pkg/config"
)

const mitteServerBinaryPath = "/usr/bin/mitte"

func main() {
	// If this variable is set, it means we are being re-executed by the ssh-handler
	// on the server side, so we should just run the command directly.
	if os.Getenv("MITTE_IS_SERVER_SIDE") == "true" {
		cmd.Execute()
		return
	}

	// Server-side behavior: if the SSH handler calls us, we just execute.
	if len(os.Args) > 1 && os.Args[1] == "ssh-handler" {
		cmd.Execute()
		return
	}

	// --- Logic to determine client vs. server execution ---
	args := os.Args[1:]
	if len(args) == 0 { // If only 'mitte' is run, show the help.
		cmd.Execute()
		return
	}

	// Commands that MUST be executed directly, never proxied over SSH.
	directCommands := map[string]bool{
		"remote": true,
		"setup":  true, // Server-side admin command
		"keys":   true, // Server-side admin command
		"help":   true, // Cobra handles 'help'
		"--help": true,
		"-h":     true,
		// "version": true,
	}
	if directCommands[args[0]] {
		cmd.Execute()
		return
	}

	// For all other commands, we attempt to act as a client.
	// If client configuration is missing or incomplete, we assume we are on the server
	// and execute the command directly.
	cfg, err := config.LoadLocal()
	if err != nil || cfg.RemoteSSH == "" {
		cmd.Execute()
		return
	}

	// If we get here, it means we have a valid client config, so we proxy over SSH.
	remoteCommand := mitteServerBinaryPath + " " + strings.Join(args, " ")
	sshCmd := exec.Command("ssh", cfg.RemoteSSH, remoteCommand)

	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr

	if err := sshCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		} else {
			fmt.Fprintf(os.Stderr, "Error running SSH command: %v\n", err)
			os.Exit(1)
		}
	}
}
