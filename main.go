package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mitteapp/mitteapp/cmd"
	"github.com/mitteapp/mitteapp/pkg/config"
)

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
		// TODO: Add `version` command
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

	// 1. Manually quote each argument to make it safe for the remote shell.
	quotedArgs := []string{}
	for _, arg := range args {
		// This wraps each argument in single quotes, and correctly handles
		// any single quotes that might be inside the argument itself.
		quotedArg := fmt.Sprintf("'%s'", strings.ReplaceAll(arg, "'", `'\''`))
		quotedArgs = append(quotedArgs, quotedArg)
	}

	// 2. Join the *quoted* arguments and prefix with the command name.
	// The result will be: "mitte 'config' 'set' 'my-app' 'GREETING=Hello from Mitte'"
	remoteCommand := "mitte " + strings.Join(quotedArgs, " ")

	// 3. Pass this single, fully-formed command string to SSH.
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
