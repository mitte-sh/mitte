package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mitte-sh/mitte/pkg/actions"
	"github.com/mitte-sh/mitte/pkg/state"
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

		if len(app.EnvVars) == 0 && app.RawEnv == "" {
			fmt.Fprintf(os.Stderr, "No environment variables are set for '%s'.\n", appName)
			return
		}

		if app.RawEnv != "" {
			fmt.Print(app.RawEnv)
			if !strings.HasSuffix(app.RawEnv, "\n") {
				fmt.Println()
			}
			return
		}

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
			key := parts[0]
			value := parts[1]
			app.EnvVars[key] = value

			// Update RawEnv if it exists
			if app.RawEnv != "" {
				lines := strings.Split(app.RawEnv, "\n")
				found := false
				for i, line := range lines {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, key+"=") {
						lines[i] = fmt.Sprintf("%s=%s", key, value)
						found = true
						break
					}
				}
				if !found {
					if len(lines) > 0 && lines[len(lines)-1] != "" {
						app.RawEnv += "\n"
					}
					app.RawEnv += fmt.Sprintf("%s=%s\n", key, value)
				} else {
					app.RawEnv = strings.Join(lines, "\n")
				}
			}
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

			// Update RawEnv if it exists
			if app.RawEnv != "" {
				lines := strings.Split(app.RawEnv, "\n")
				var newLines []string
				for _, line := range lines {
					trimmed := strings.TrimSpace(line)
					if !strings.HasPrefix(trimmed, key+"=") {
						newLines = append(newLines, line)
					}
				}
				app.RawEnv = strings.Join(newLines, "\n")
			}
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

		// 1. Fetch current environment variables.
		fmt.Fprintf(os.Stderr, "-----> Fetching current environment for '%s'...\n", appName)
		app, err := state.Load(appName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading application state: %v\n", err)
			os.Exit(1)
		}

		currentEnv := app.RawEnv
		if currentEnv == "" && len(app.EnvVars) > 0 {
			// Fallback to EnvVars if RawEnv is not set
			var sb strings.Builder
			for key, value := range app.EnvVars {
				sb.WriteString(fmt.Sprintf("%s=%s\n", key, value))
			}
			currentEnv = sb.String()
		}

		// 2. Open the user's default editor with the current env vars.
		newEnvContent, err := openInEditor(currentEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening editor: %v\n", err)
			os.Exit(1)
		}

		// If the user didn't change anything, we're done.
		if newEnvContent == currentEnv {
			fmt.Println("No changes detected. Aborting.")
			return
		}

		// 3. Update the app state.
		app.RawEnv = newEnvContent
		app.SyncEnv()

		if err := app.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving configuration: %v\n", err)
			os.Exit(1)
		}

		// 4. Trigger a restart to apply all changes.
		fmt.Fprintln(os.Stderr, "-----> Applying changes by restarting the application...")
		if err := actions.RestartApp(appName); err != nil {
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
