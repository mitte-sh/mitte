package config

import (
	"fmt"
	"os"
	"strings"
)

// GetBaseDomain reads the configured base domain from /etc/mitte/domain.
// This is the domain under which all applications will be hosted.
func GetBaseDomain() (string, error) {
	const domainFile = "/etc/mitte/domain"
	domainBytes, err := os.ReadFile(domainFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("base domain not configured. Please run 'sudo mitte setup'")
		}
		return "", fmt.Errorf("failed to read domain configuration from %s: %w", domainFile, err)
	}
	domain := strings.TrimSpace(string(domainBytes))
	if domain == "" {
		return "", fmt.Errorf("domain configuration file %s is empty. Please run 'sudo mitte setup'", domainFile)
	}
	return domain, nil
}
