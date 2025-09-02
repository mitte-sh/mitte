package cmd

import (
	"fmt"
	"os"
	"os/exec"
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
		userInput := args[0]
		varsToSet := args[1:]
		noRestart, _ := cmd.Flags().GetBool("no-restart")

		appName, err := state.ResolveAppName(userInput)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

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
		userInput := args[0]
		keysToUnset := args[1:]
		noRestart, _ := cmd.Flags().GetBool("no-restart")

		appName, err := state.ResolveAppName(userInput)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

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

var configEditCmd = &cobra.Command{
	Use:   "edit <app-name>",
	Short: "Edit environment variables in a text editor",
	Long: `Opens your default editor to bulk-edit environment variables.
Variables are presented in a KEY=VALUE format. Save and close the editor
to apply the changes, which will trigger a redeployment.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		appName, err := state.ResolveAppName(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// 1. Fetch current environment variables by running `mitte config list`.
		fmt.Fprintf(os.Stderr, "-----> Fetching current environment for '%s'...\n", appName)
		listCmd := exec.Command("mitte", "config", "list", appName)
		currentEnvBytes, err := listCmd.Output()
		if err != nil {
			// Handle cases where the app has no env vars yet.
			if !strings.Contains(string(currentEnvBytes), "No environment variables") {
				fmt.Fprintf(os.Stderr, "Error fetching current config: %v\n", err)
				os.Exit(1)
			}
		}

		// 2. Open the user's default editor with the current env vars.
		newEnvContent, err := openInEditor(string(currentEnvBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening editor: %v\n", err)
			os.Exit(1)
		}

		// If the user didn't change anything, we're done.
		if newEnvContent == string(currentEnvBytes) {
			fmt.Println("No changes detected. Aborting.")
			return
		}

		// 3. Calculate the difference between the old and new env vars.
		oldVars := parseEnv(string(currentEnvBytes))
		newVars := parseEnv(newEnvContent)

		varsToSet, varsToUnset := diffEnv(oldVars, newVars)

		// 4. Call `mitte config set` and `mitte config unset` to apply changes.
		if len(varsToSet) > 0 {
			fmt.Fprintf(os.Stderr, "-----> Setting %d variable(s)...\n", len(varsToSet))
			setArgs := []string{"config", "set", appName}
			setArgs = append(setArgs, "--no-restart")
			setArgs = append(setArgs, varsToSet...)

			if err := runMitteRemoteCommand(setArgs...); err != nil {
				fmt.Fprintf(os.Stderr, "Error setting variables: %v\n", err)
				os.Exit(1)
			}
		}

		if len(varsToUnset) > 0 {
			fmt.Fprintf(os.Stderr, "-----> Unsetting %d variable(s)...\n", len(varsToUnset))
			unsetArgs := []string{"config", "unset", appName}
			unsetArgs = append(unsetArgs, "--no-restart")
			unsetArgs = append(unsetArgs, varsToUnset...)
			if err := runMitteRemoteCommand(unsetArgs...); err != nil {
				fmt.Fprintf(os.Stderr, "Error unsetting variables: %v\n", err)
				os.Exit(1)
			}
		}

		// 5. Trigger a final restart to apply all batched changes.
		fmt.Fprintln(os.Stderr, "-----> Applying changes by restarting the application...")
		if err := runMitteRemoteCommand("restart", appName); err != nil {
			fmt.Fprintf(os.Stderr, "Error restarting application: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Environment successfully updated.")
	},
}

func init() {
	configSetCmd.Flags().Bool("no-restart", false, "Set the variable(s) without restarting the application")
	configUnsetCmd.Flags().Bool("no-restart", false, "Unset the variable(s) without restarting the application")

	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configUnsetCmd)
	configCmd.AddCommand(configEditCmd)
	rootCmd.AddCommand(configCmd)
}
