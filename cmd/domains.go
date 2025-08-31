package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/docker/docker/client"
	"github.com/spf13/cobra"

	"github.com/mitteapp/mitteapp/pkg/deployer"
	"github.com/mitteapp/mitteapp/pkg/router"
	"github.com/mitteapp/mitteapp/pkg/state"
)

var domainsCmd = &cobra.Command{
	Use:     "domains",
	Short:   "Manage domains for an application",
	Aliases: []string{"domain"},
}

// Add Command
var domainsAddCmd = &cobra.Command{
	Use:   "add <app-name> <domain1> [domain2...]",
	Short: "Add one or more custom domains to an app",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]
		domainsToAdd := args[1:]

		// Load the app's current state
		app, err := state.Load(appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not load app state for '%s'.\n", appName)
			os.Exit(1)
		}

		// Use a map for quick lookups to avoid adding duplicate domains.
		existingDomains := make(map[string]struct{})
		for _, d := range app.Domains {
			existingDomains[d] = struct{}{}
		}

		var newDomains []string
		for _, domain := range domainsToAdd {
			if _, exists := existingDomains[domain]; !exists {
				app.Domains = append(app.Domains, domain)
				newDomains = append(newDomains, domain)
				existingDomains[domain] = struct{}{} // Also add to map to handle duplicates in the command args.
			}
		}

		if len(newDomains) == 0 {
			fmt.Printf("All specified domains already exist for '%s'. No changes made.\n", appName)
			return
		}

		if err = app.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to save app state for '%s': %v\n", appName, err)
			os.Exit(1)
		}

		// Create a docker client
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating Docker client: %v\n", err)
			os.Exit(1)
		}
		defer cli.Close()

		// Get the running container's port
		port, err := deployer.GetContainerHostPort(context.Background(), cli, appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not find a running container for app '%s'. Cannot update domain.\n", appName)
			os.Exit(1)
		}

		// Update the router with the new full list of domains
		if err := router.SetAppRoutes(appName, app.Domains, port); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to set routes for '%s': %v\n", appName, err)
			os.Exit(1)
		}

		fmt.Printf("Successfully added %v to %s.\n", newDomains, appName)
	},
}

// Remove Command
var domainsRemoveCmd = &cobra.Command{
	Use:   "remove <app-name> <domain1> [domain2...]",
	Short: "Remove one or more custom domains from an app",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]
		domainsToRemove := args[1:]

		// Load the app's current state
		app, err := state.Load(appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not load app state for '%s'.\n", appName)
			os.Exit(1)
		}

		// Create a map of domains to remove for efficient lookup.
		toRemoveSet := make(map[string]struct{})
		for _, d := range domainsToRemove {
			toRemoveSet[d] = struct{}{}
		}

		// Filter the app's domains, keeping only the ones not in the removal set.
		var keptDomains []string
		var actuallyRemoved []string
		for _, d := range app.Domains {
			if _, found := toRemoveSet[d]; found {
				actuallyRemoved = append(actuallyRemoved, d)
			} else {
				keptDomains = append(keptDomains, d)
			}
		}

		if len(actuallyRemoved) == 0 {
			fmt.Printf("None of the specified domains were found for app '%s'. No changes made.\n", appName)
			return
		}

		app.Domains = keptDomains
		if err := app.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to save app state for '%s': %v\n", appName, err)
			os.Exit(1)
		}

		// We need the port to update the routes, but only if there are any domains left.
		// If the domains list is empty, SetAppRoutes will handle deletion and won't use the port.
		var port string
		if len(app.Domains) > 0 {
			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating Docker client: %v\n", err)
				os.Exit(1)
			}
			defer cli.Close()

			port, err = deployer.GetContainerHostPort(context.Background(), cli, appName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Could not find a running container for app '%s'. Cannot update domains.\n", appName)
				os.Exit(1)
			}
		}

		// Update the router with the new list.
		if err := router.SetAppRoutes(appName, app.Domains, port); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to set routes for '%s': %v\n", appName, err)
			os.Exit(1)
		}

		fmt.Printf("Successfully removed %v from %s.\n", actuallyRemoved, appName)
	},
}

// List Command
var domainsListCmd = &cobra.Command{
	Use:   "list <app-name>",
	Short: "List all custom domains for an app",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]

		// Load the app's current state
		app, err := state.Load(appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not load app state for '%s'.\n", appName)
			os.Exit(1)
		}

		if len(app.Domains) == 0 {
			fmt.Printf("No custom domains configured for %s.\n", appName)
			return
		}

		fmt.Printf("Custom domains for %s:\n", appName)
		for _, domain := range app.Domains {
			fmt.Printf("- %s\n", domain)
		}
	},
}

func init() {
	domainsCmd.AddCommand(domainsAddCmd)
	domainsCmd.AddCommand(domainsRemoveCmd)
	domainsCmd.AddCommand(domainsListCmd)

	rootCmd.AddCommand(domainsCmd)
}
