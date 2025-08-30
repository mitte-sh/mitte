package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mitteapp/mitteapp/pkg/actions"
	"github.com/mitteapp/mitteapp/pkg/state"
)

// configCmd is the base group for configuration commands.
var configCmd = &cobra.Command{
	Use:     "config",
	Short:   "Manage environment variables for an application",
	Aliases: []string{"env"},
}

// configListCmd lists all environment variables for an app.
var configListCmd = &cobra.Command{
	Use:   "list <app-name>",
	Short: "List environment variables for an application",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]
		app, err := state.Load(appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading application state: %v\n", err)
			os.Exit(1)
		}

		if len(app.EnvVars) == 0 {
			fmt.Printf("No environment variables are set for '%s'.\n", appName)
			return
		}

		fmt.Printf("=== Environment variables for %s ===\n", appName)
		for key, value := range app.EnvVars {
			fmt.Printf("%s=%s\n", key, value)
		}
	},
}

// configSetCmd sets one or more environment variables.
var configSetCmd = &cobra.Command{
	Use:   "set <app-name> KEY1=VALUE1 [KEY2=VALUE2 ...]",
	Short: "Set one or more environment variables for an application",
	Long:  "Sets environment variables for an application. By default, the application is redeployed to apply changes. Use the --no-restart flag to prevent this.",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]
		varsToSet := args[1:]
		noRestart, _ := cmd.Flags().GetBool("no-restart")

		app, err := state.Load(appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading application state: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "Setting environment variables for %s... ", appName)
		for _, v := range varsToSet {
			parts := strings.SplitN(v, "=", 2)
			if len(parts) != 2 {
				fmt.Fprintf(os.Stderr, "\nError: variable format must be KEY=VALUE, but got '%s'.\n", v)
				os.Exit(1)
			}
			app.EnvVars[parts[0]] = parts[1]
		}

		if err := app.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving configuration: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "done.")

		if !noRestart {
			fmt.Fprintln(os.Stderr, "Redeploying application to apply changes...")
			if err := actions.RestartApp(appName); err != nil {
				fmt.Fprintf(os.Stderr, "Error redeploying application: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Configuration updated for '%s'. The application is now restarting.\n", appName)
		} else {
			fmt.Printf("Configuration updated for '%s'. Run a deploy or restart for changes to take effect.\n", appName)
		}
	},
}

// configUnsetCmd unsets one or more environment variables.
var configUnsetCmd = &cobra.Command{
	Use:   "unset <app-name> KEY1 [KEY2 ...]",
	Short: "Unset one or more environment variables from an application",
	Long:  "Unsets environment variables from an application. By default, the application is redeployed to apply changes. Use the --no-restart flag to prevent this.",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		appName := args[0]
		keysToUnset := args[1:]
		noRestart, _ := cmd.Flags().GetBool("no-restart")

		app, err := state.Load(appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading application state: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "Unsetting environment variables from %s... ", appName)
		for _, key := range keysToUnset {
			delete(app.EnvVars, key)
		}

		if err := app.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving configuration: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "done.")

		if !noRestart {
			fmt.Fprintln(os.Stderr, "Redeploying application to apply changes...")
			if err := actions.RestartApp(appName); err != nil {
				fmt.Fprintf(os.Stderr, "Error redeploying application: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Configuration updated for '%s'. The application is now restarting.\n", appName)
		} else {
			fmt.Printf("Configuration updated for '%s'. Run a deploy or restart for changes to take effect.\n", appName)
		}
	},
}

func init() {
	configSetCmd.Flags().Bool("no-restart", false, "Set the variable(s) without restarting the application")
	configUnsetCmd.Flags().Bool("no-restart", false, "Unset the variable(s) without restarting the application")

	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configUnsetCmd)
	rootCmd.AddCommand(configCmd)
}
