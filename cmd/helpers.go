package cmd

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"strings"

	"github.com/flynn/go-shlex"
)

// Helper to open content in the user's default editor.
func openInEditor(content string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	tmpfile, err := os.CreateTemp("", "mitte-env-*.env")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpfile.Name())

	if _, err = tmpfile.WriteString(content); err != nil {
		return "", err
	}
	if err = tmpfile.Close(); err != nil {
		return "", err
	}

	cmd := exec.Command(editor, tmpfile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err = cmd.Run(); err != nil {
		return "", err
	}

	newContent, err := os.ReadFile(tmpfile.Name())
	if err != nil {
		return "", err
	}

	return string(newContent), nil
}

// Helper to parse .env file content into a map.
func parseEnv(content string) map[string]string {
	vars := make(map[string]string)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		// Ignore comments and empty lines
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		parts, err := shlex.Split(line)
		if err != nil || len(parts) == 0 {
			continue
		}
		keyVal := strings.SplitN(parts[0], "=", 2)
		if len(keyVal) == 2 {
			vars[keyVal[0]] = keyVal[1]
		}
	}
	return vars
}

// Helper to calculate the difference between two environment maps.
func diffEnv(oldVars, newVars map[string]string) (varsToSet, varsToUnset []string) {
	for key, newVal := range newVars {
		if oldVal, ok := oldVars[key]; !ok || oldVal != newVal {
			varsToSet = append(varsToSet, fmt.Sprintf("%s=%s", key, newVal))
		}
	}

	var unsetKeys []string
	for key := range oldVars {
		if _, ok := newVars[key]; !ok {
			unsetKeys = append(unsetKeys, key)
		}
	}
	varsToUnset = unsetKeys
	return
}

func runMitteRemoteCommand(args ...string) error {
	cmd := exec.Command("mitte", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func GeneratePassword(length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		result[i] = chars[idx.Int64()]
	}
	return string(result), nil
}
