package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/logger"
	"github.com/mitte-sh/mitte/pkg/registry"
)

var registriesCmd = &cobra.Command{
	Use:     "registries",
	Short:   "Manage Docker registry credentials",
	Aliases: []string{"registry"},
}

var registriesAddCmd = &cobra.Command{
	Use:   "add <registry> <username> <password>",
	Short: "Add credentials for a Docker registry",
	Args:  cobra.ExactArgs(3),
	Run:   runRegistriesAdd,
}

var registriesRemoveCmd = &cobra.Command{
	Use:   "remove <registry>",
	Short: "Remove credentials for a Docker registry",
	Args:  cobra.ExactArgs(1),
	Run:   runRegistriesRemove,
}

var registriesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured registry credentials",
	Run:   runRegistriesList,
}

func init() {
	registriesCmd.AddCommand(registriesAddCmd)
	registriesCmd.AddCommand(registriesRemoveCmd)
	registriesCmd.AddCommand(registriesListCmd)
	rootCmd.AddCommand(registriesCmd)
}

func runRegistriesAdd(cmd *cobra.Command, args []string) {
	registryHost := args[0]
	username := args[1]
	password := args[2]

	// Validate registry host
	if registryHost == "" {
		logger.Error("Error: Registry host cannot be empty")
		os.Exit(1)
	}

	// Create registry manager
	manager := registry.NewConfigManager("")

	// Add credentials
	if err := manager.AddCredentials(registryHost, username, password); err != nil {
		logger.Error("Could not add credentials", "err", err)
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Credentials added for registry '%s'", registryHost))
	logger.Info(fmt.Sprintf("You can now pull images from '%s' using 'mitte apps deploy-image'", registryHost))
}

func runRegistriesRemove(cmd *cobra.Command, args []string) {
	registryHost := args[0]

	// Create registry manager
	manager := registry.NewConfigManager("")

	// Remove credentials
	if err := manager.RemoveCredentials(registryHost); err != nil {
		logger.Error("Could not remove credentials", "err", err)
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Credentials removed for registry '%s'", registryHost))
}

func runRegistriesList(cmd *cobra.Command, args []string) {
	// Create registry manager
	manager := registry.NewConfigManager("")

	// List credentials
	registries, err := manager.ListCredentials()
	if err != nil {
		logger.Error("Could not list credentials", "err", err)
		os.Exit(1)
	}

	if len(registries) == 0 {
		logger.Info("No registry credentials configured.")
		logger.Info("Add credentials using: mitte registries add <registry> <username> <password>")
		return
	}

	logger.Info("Configured registry credentials:")
	for _, reg := range registries {
		logger.Info(fmt.Sprintf("  - %s", reg))
	}
}
