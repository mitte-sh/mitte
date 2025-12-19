package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/moby/term"
	"github.com/spf13/cobra"

	"github.com/mitteapp/mitteapp/pkg/actions"
	"github.com/mitteapp/mitteapp/pkg/services"
	"github.com/mitteapp/mitteapp/pkg/state"
)

var mariadbCmd = &cobra.Command{
	Use:   "mariadb",
	Short: "Manage MariaDB database services",
}

var mariadbListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all managed MariaDB instances",
	Aliases: []string{"ls"},
	Run: func(cmd *cobra.Command, args []string) {
		serviceType := "mariadb"
		serviceDir := filepath.Join("/var/lib/mitte/services", serviceType)

		files, err := os.ReadDir(serviceDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No MariaDB services have been created yet.")
				return
			}
			fmt.Fprintf(os.Stderr, "Error: Could not read the services directory: %v\n", err)
			os.Exit(1)
		}

		if len(files) == 0 {
			fmt.Println("No MariaDB services have been created yet.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tVERSION\tDATABASE\tUSER\tCONFIG FILE")
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			if !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			instanceName := strings.TrimSuffix(file.Name(), ".json")
			svc, err := state.LoadService(serviceType, instanceName)
			if err != nil {
				continue
			}
			configFile := "none"
			if svc.ConfigFile != "" {
				configFile = filepath.Base(svc.ConfigFile)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				instanceName,
				svc.Version,
				svc.DatabaseName,
				svc.Username,
				configFile,
			)
		}
		w.Flush()
	},
}

var mariadbCreateCmd = &cobra.Command{
	Use:   "create <instance-name>",
	Short: "Create a new MariaDB instance",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		ctx := context.Background()

		version, _ := cmd.Flags().GetString("version")
		initialDatabase, _ := cmd.Flags().GetString("database")
		user, _ := cmd.Flags().GetString("user")
		userPassword, _ := cmd.Flags().GetString("password")
		configFile, _ := cmd.Flags().GetString("config-file")

		fmt.Fprintf(os.Stderr, "-----> Creating MariaDB instance '%s'...\n", instanceName)

		// Check if it already exists
		svc, _ := state.LoadService("mariadb", instanceName)
		if svc.RootPassword != "" {
			fmt.Fprintf(os.Stderr, "Error: A MariaDB service named '%s' already exists.\n", instanceName)
			os.Exit(1)
		}

		// Set database password
		var dbPassword string
		if userPassword != "" {
			dbPassword = userPassword
		} else {
			var err error
			dbPassword, err = generatePassword(32)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Could not generate a secure password: %v\n", err)
				os.Exit(1)
			}
		}

		if user == "" {
			user = "root"
		}

		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not connect to Docker daemon: %v\n", err)
			os.Exit(1)
		}
		defer cli.Close()

		// 1. Pull Image
		imageName := "mariadb:" + version
		fmt.Fprintf(os.Stderr, "-----> Pulling image %s...\n", imageName)
		out, err := cli.ImagePull(ctx, imageName, image.PullOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to pull image: %v\n", err)
			os.Exit(1)
		}
		defer out.Close()
		termFd, isTerm := term.GetFdInfo(os.Stderr)
		jsonmessage.DisplayJSONMessagesStream(out, os.Stderr, termFd, isTerm, nil)

		// 2. Create Volume
		var volumeName string
		if _, err := cli.VolumeInspect(ctx, "mitte-mariadb-data-"+instanceName); err != nil {
			if errdefs.IsNotFound(err) {
				fmt.Fprintln(os.Stderr, "-----> Creating persistent data volume...")
				_, err := cli.VolumeCreate(ctx, volume.CreateOptions{
					Name: "mitte-mariadb-data-" + instanceName,
					Labels: map[string]string{
						"app.mitte.type":       "mariadb",
						"app.mitte.instance":   instanceName,
						"app.mitte.created-by": "mitte",
					},
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: Failed to create volume: %v\n", err)
					os.Exit(1)
				}
				volumeName = "mitte-mariadb-data-" + instanceName
			} else {
				fmt.Fprintf(os.Stderr, "Error: Could not inspect volume: %v\n", err)
				os.Exit(1)
			}
		} else {
			volumeName = "mitte-mariadb-data-" + instanceName
		}

		// 3. Create Container
		envVars := []string{
			"MARIADB_ROOT_PASSWORD=" + dbPassword,
			"MARIADB_ROOT_HOST=%",
		}
		if user != "root" {
			envVars = append(envVars, "MARIADB_USER="+user, "MARIADB_PASSWORD="+dbPassword)
		}
		if initialDatabase != "" {
			envVars = append(envVars, "MARIADB_DATABASE="+initialDatabase)
		}
		containerConfig := &container.Config{
			Image: imageName,
			Env:   envVars,
		}
		hostConfig := &container.HostConfig{
			RestartPolicy: container.RestartPolicy{
				Name: "always",
			},
		}

		// Only mount volume if we created one
		if volumeName != "" {
			hostConfig.Mounts = []mount.Mount{
				{
					Type:   mount.TypeVolume,
					Source: volumeName,
					Target: "/var/lib/mysql",
				},
			}
		}

		// Mount custom config file if provided
		if configFile != "" {
			// Validate config file exists
			if _, err := os.Stat(configFile); os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Error: Config file does not exist: %s\n", configFile)
				os.Exit(1)
			}

			// Add bind mount for config file
			hostConfig.Binds = append(hostConfig.Binds, fmt.Sprintf("%s:/etc/mysql/conf.d/custom.cnf:ro", configFile))
		}

		networkingConfig := &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				"mitte": {},
			},
		}

		resp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, instanceName)
		if err != nil {
			if errdefs.IsConflict(err) {
				fmt.Fprintf(os.Stderr, "Error: A container named '%s' already exists. Please choose a different name or remove the existing container.\n", instanceName)
			} else {
				fmt.Fprintf(os.Stderr, "Error: Failed to create MariaDB container: %v\n", err)
			}
			os.Exit(1)
		}

		// 4. Start Container
		if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to start MariaDB container: %v\n", err)
			os.Exit(1)
		}

		// 5. Wait for health check
		fmt.Fprintln(os.Stderr, "-----> Waiting for MariaDB to become healthy...")
		for i := 0; i < 60; i++ {
			inspect, err := cli.ContainerInspect(ctx, resp.ID)
			if err != nil {
				break
			}
			if inspect.State.Running && inspect.State.Health != nil && inspect.State.Health.Status == "healthy" {
				break
			}
			time.Sleep(1 * time.Second)
		}

		// Save the state
		svc.RootPassword = dbPassword
		svc.UserPassword = dbPassword
		svc.DatabaseName = initialDatabase
		svc.InternalHost = instanceName
		svc.Version = version
		svc.Port = 3306
		svc.Username = user
		svc.ConfigFile = configFile
		if err := svc.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to save service state: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\nSuccess! MariaDB instance '%s' created.\n", instanceName)
		if initialDatabase != "" {
			fmt.Printf("Initial database '%s' has also been created.\n", initialDatabase)
		}
		if user != "root" {
			fmt.Printf("The user '%s' password is: %s\n", user, dbPassword)
		} else {
			fmt.Printf("The root password is: %s\n", dbPassword)
		}
		fmt.Println("NOTE: This is the only time the password will be displayed. Please save it securely.")
	},
}

var mariadbDestroyCmd = &cobra.Command{
	Use:     "destroy <instance-name>",
	Short:   "Permanently destroy a MariaDB instance",
	Long:    "This will stop and remove the container AND permanently delete its data volume. THIS ACTION IS IRREVERSIBLE.",
	Aliases: []string{"remove"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		ctx := context.Background()

		// Safety confirmation
		fmt.Printf(" !    WARNING: This will permanently delete the MariaDB instance '%s' and all of its data.\n", instanceName)
		fmt.Printf(" >    Please type '%s' to confirm: ", instanceName)
		reader := bufio.NewReader(os.Stdin)
		confirmation, _ := reader.ReadString('\n')
		if strings.TrimSpace(confirmation) != instanceName {
			fmt.Println("Cancelled.")
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "-----> Destroying MariaDB instance '%s'...\n", instanceName)

		// Delete the service state first
		svc, err := state.LoadService("mariadb", instanceName)
		if err == nil {
			svc.Delete()
		}

		// Destroy the container and volume
		err = services.DestroyMariaDB(ctx, instanceName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to destroy MariaDB instance: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Success! MariaDB instance '%s' has been destroyed.\n", instanceName)
	},
}

var mariadbLinkCmd = &cobra.Command{
	Use:   "link <instance-name> <app-name> [VARIABLE_NAME]",
	Short: "Link a MariaDB instance to an application",
	Long: `This will set a database connection string as an environment variable on the application and redeploy it.
By default, the variable is named DATABASE_URL. You can specify a custom name as the third argument.`,
	Args: cobra.RangeArgs(2, 3),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		appName := args[1]
		serviceType := "mariadb"

		// 1. Determine the environment variable name to use.
		var envVarName string
		if len(args) == 3 {
			envVarName = args[2] // Use the custom name provided by the user.
		} else {
			envVarName = "DATABASE_URL" // Fall back to the default.
		}

		fmt.Fprintf(os.Stderr, "-----> Linking MariaDB instance '%s' to app '%s'...\n", instanceName, appName)

		// 2. Load the service state to get connection details
		service, err := state.LoadService(serviceType, instanceName)
		if err != nil || service.RootPassword == "" {
			fmt.Fprintf(os.Stderr, "Error: Could not find MariaDB instance '%s'.\n", instanceName)
			os.Exit(1)
		}

		// 3. Load the application state
		app, err := state.Load(appName)
		if err != nil || len(app.Domains) == 0 { // Check domains to see if app exists
			fmt.Fprintf(os.Stderr, "Error: Could not find application '%s'.\n", appName)
			os.Exit(1)
		}

		// 4. Construct the DATABASE_URL
		dbToUse := service.DatabaseName
		if dbToUse == "" {
			dbToUse = "mariadb"
		}

		// Format: mysql://user:password@host:port/database
		passwordToUse := service.UserPassword
		if passwordToUse == "" {
			passwordToUse = service.RootPassword
		}
		databaseURL := fmt.Sprintf("mysql://%s:%s@%s:%d/%s",
			service.Username,
			passwordToUse,
			service.InternalHost,
			service.Port,
			dbToUse,
		)

		// 5. Set the environment variable on the app
		fmt.Fprintln(os.Stderr, "-----> Setting DATABASE_URL config variable...")
		app.EnvVars[envVarName] = databaseURL
		if err := app.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to save application state with new DATABASE_URL: %v\n", err)
			os.Exit(1)
		}

		// 6. Redeploy the application to apply the change
		fmt.Fprintln(os.Stderr, "-----> Redeploying application to apply changes...")
		if err := actions.RestartApp(appName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to redeploy application '%s': %v\n", appName, err)
			os.Exit(1)
		}

		fmt.Println("Success! Linked and redeployed. Your app can now connect to the database via the DATABASE_URL environment variable.")
	},
}

var mariadbBackupCmd = &cobra.Command{
	Use:   "backup <instance-name> <output-file>",
	Short: "Create a backup of all databases in a MariaDB instance",
	Long: `This command creates a SQL dump of all databases in the specified MariaDB instance
and saves it to the specified output file on the host system.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		outputFile := args[1]

		fmt.Fprintf(os.Stderr, "-----> Creating backup of MariaDB instance '%s'...\n", instanceName)

		err := services.BackupMariaDB(context.Background(), instanceName, outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to create backup: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Success! Backup saved to %s\n", outputFile)
	},
}

var mariadbRestoreCmd = &cobra.Command{
	Use:   "restore <instance-name> <backup-file>",
	Short: "Restore databases from a backup file into a MariaDB instance",
	Long: `This command restores databases from a SQL dump file into the specified MariaDB instance.
WARNING: This will overwrite existing data in the databases.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		backupFile := args[1]

		fmt.Fprintf(os.Stderr, "-----> Restoring MariaDB instance '%s' from backup...\n", instanceName)

		err := services.RestoreMariaDB(context.Background(), instanceName, backupFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to restore from backup: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Success! Database restored from backup.")
	},
}

var mariadbUsersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage database users for MariaDB instances",
}

var mariadbUsersCreateCmd = &cobra.Command{
	Use:   "create <instance-name> <username>",
	Short: "Create a new database user in a MariaDB instance",
	Long: `This command creates a new database user with the specified privileges.
If no password is provided, a secure password will be generated automatically.
If no database is specified, the user will have no database access by default.
If no privileges are specified, the user will have no privileges by default.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		username := args[1]

		password, _ := cmd.Flags().GetString("password")
		database, _ := cmd.Flags().GetString("database")
		privileges, _ := cmd.Flags().GetStringSlice("privileges")

		fmt.Fprintf(os.Stderr, "-----> Creating user '%s' in MariaDB instance '%s'...\n", username, instanceName)

		// Generate password if not provided
		if password == "" {
			var err error
			password, err = generatePassword(32)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Could not generate a secure password: %v\n", err)
				os.Exit(1)
			}
		}

		// Default to all databases if not specified
		if database == "" {
			database = "*"
		}

		err := services.CreateMariaDBUser(context.Background(), instanceName, username, password, database, privileges)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to create user: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Success! User '%s' created in MariaDB instance '%s'.\n", username, instanceName)
		fmt.Printf("Password: %s\n", password)
		fmt.Printf("Database: %s\n", database)
		if len(privileges) > 0 {
			fmt.Printf("Privileges: %s\n", strings.Join(privileges, ", "))
		}
		fmt.Println("NOTE: This is the only time the password will be displayed. Please save it securely.")
	},
}

var mariadbUsersDeleteCmd = &cobra.Command{
	Use:     "delete <instance-name> <username>",
	Short:   "Delete a database user from a MariaDB instance",
	Aliases: []string{"remove", "rm"},
	Args:    cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		username := args[1]

		fmt.Fprintf(os.Stderr, "-----> Deleting user '%s' from MariaDB instance '%s'...\n", username, instanceName)

		err := services.DeleteMariaDBUser(context.Background(), instanceName, username)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to delete user: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Success! User '%s' deleted from MariaDB instance '%s'.\n", username, instanceName)
	},
}

var mariadbUsersListCmd = &cobra.Command{
	Use:     "list <instance-name>",
	Short:   "List all database users in a MariaDB instance",
	Aliases: []string{"ls"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]

		fmt.Fprintf(os.Stderr, "-----> Listing users in MariaDB instance '%s'...\n", instanceName)

		users, err := services.ListMariaDBUsers(context.Background(), instanceName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to list users: %v\n", err)
			os.Exit(1)
		}

		if len(users) == 0 {
			fmt.Println("No database users found (excluding system users).")
			return
		}

		fmt.Println("Database users:")
		for _, user := range users {
			fmt.Printf("  • %s\n", user)
		}
	},
}

func init() {
	mariadbCreateCmd.Flags().String("version", "latest", "The version tag of the MariaDB Docker image to use (e.g., 10.11)")
	mariadbCreateCmd.Flags().String("database", "", "The name of a database to create on first startup")
	mariadbCreateCmd.Flags().String("user", "", "The username for the database user (optional, defaults to root)")
	mariadbCreateCmd.Flags().String("password", "", "The password for the database user (optional, will be generated if not provided)")
	mariadbCreateCmd.Flags().String("config-file", "", "Path to a custom MariaDB configuration file (.cnf) to mount into the container")
	mariadbCmd.AddCommand(mariadbListCmd)
	mariadbCmd.AddCommand(mariadbCreateCmd)
	mariadbCmd.AddCommand(mariadbDestroyCmd)
	mariadbCmd.AddCommand(mariadbLinkCmd)
	mariadbCmd.AddCommand(mariadbBackupCmd)
	mariadbCmd.AddCommand(mariadbRestoreCmd)

	// User management commands
	mariadbUsersCreateCmd.Flags().String("password", "", "Password for the new user (will be generated if not provided)")
	mariadbUsersCreateCmd.Flags().String("database", "", "Database to grant access to (default: all databases)")
	mariadbUsersCreateCmd.Flags().StringSlice("privileges", []string{"ALL PRIVILEGES"}, "Privileges to grant (e.g., SELECT,INSERT,UPDATE,DELETE,CREATE,DROP)")

	mariadbUsersCmd.AddCommand(mariadbUsersCreateCmd)
	mariadbUsersCmd.AddCommand(mariadbUsersDeleteCmd)
	mariadbUsersCmd.AddCommand(mariadbUsersListCmd)
	mariadbCmd.AddCommand(mariadbUsersCmd)

	rootCmd.AddCommand(mariadbCmd)
}

func generatePassword(length int) (string, error) {
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
