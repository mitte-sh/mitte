package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

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
		fmt.Fprintf(os.Stderr, "Error: Registry host cannot be empty\n")
		os.Exit(1)
	}

	// Create registry manager
	manager := registry.NewConfigManager("")

	// Add credentials
	if err := manager.AddCredentials(registryHost, username, password); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not add credentials: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Credentials added for registry '%s'\n", registryHost)
	fmt.Printf("You can now pull images from '%s' using 'mitte apps deploy-image'\n", registryHost)
}

func runRegistriesRemove(cmd *cobra.Command, args []string) {
	registryHost := args[0]

	// Create registry manager
	manager := registry.NewConfigManager("")

	// Remove credentials
	if err := manager.RemoveCredentials(registryHost); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not remove credentials: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Credentials removed for registry '%s'\n", registryHost)
}

func runRegistriesList(cmd *cobra.Command, args []string) {
	// Create registry manager
	manager := registry.NewConfigManager("")

	// List credentials
	registries, err := manager.ListCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not list credentials: %v\n", err)
		os.Exit(1)
	}

	if len(registries) == 0 {
		fmt.Println("No registry credentials configured.")
		fmt.Println("Add credentials using: mitte registries add <registry> <username> <password>")
		return
	}

	fmt.Println("Configured registry credentials:")
	for _, reg := range registries {
		fmt.Printf("  - %s\n", reg)
	}
}

