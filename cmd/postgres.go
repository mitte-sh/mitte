package cmd

import (
	"bufio"
	"context"
	"fmt"
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

var postgresCmd = &cobra.Command{
	Use:   "postgres",
	Short: "Manage PostgreSQL database services",
}

var postgresListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all managed PostgreSQL instances",
	Aliases: []string{"ls"},
	Run: func(cmd *cobra.Command, args []string) {
		serviceType := "postgres"
		serviceDir := filepath.Join("/var/lib/mitte/services", serviceType)

		files, err := os.ReadDir(serviceDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No PostgreSQL services have been created yet.")
				return
			}
			fmt.Fprintf(os.Stderr, "Error: Could not read the services directory: %v\n", err)
			os.Exit(1)
		}

		if len(files) == 0 {
			fmt.Println("No PostgreSQL services have been created yet.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tVERSION\tDATABASE\tUSER")
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			instanceName := strings.TrimSuffix(file.Name(), ".json")
			svc, err := state.LoadService(serviceType, instanceName)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				instanceName,
				svc.Version,
				svc.DatabaseName,
				svc.Username,
			)
		}
		w.Flush()
	},
}

var postgresCreateCmd = &cobra.Command{
	Use:   "create <instance-name>",
	Short: "Create a new PostgreSQL instance",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		ctx := context.Background()

		version, _ := cmd.Flags().GetString("version")
		initialDatabase, _ := cmd.Flags().GetString("database")
		user, _ := cmd.Flags().GetString("user")
		userPassword, _ := cmd.Flags().GetString("password")

		fmt.Fprintf(os.Stderr, "-----> Creating PostgreSQL instance '%s'...\n", instanceName)

		// Check if it already exists
		svc, _ := state.LoadService("postgres", instanceName)
		if svc.RootPassword != "" {
			fmt.Fprintf(os.Stderr, "Error: A PostgreSQL service named '%s' already exists.\n", instanceName)
			os.Exit(1)
		}

		// Set database password
		var dbPassword string
		if userPassword != "" {
			dbPassword = userPassword
		} else {
			var err error
			dbPassword, err = GeneratePassword(32)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Could not generate a secure password: %v\n", err)
				os.Exit(1)
			}
		}

		if user == "" {
			user = "postgres"
		}

		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not connect to Docker daemon: %v\n", err)
			os.Exit(1)
		}
		defer cli.Close()

		// 1. Pull Image
		imageName := "postgres:" + version
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
		volumeName := "mitte-postgres-data-" + instanceName
		if _, err := cli.VolumeInspect(ctx, volumeName); err != nil {
			if errdefs.IsNotFound(err) {
				fmt.Fprintln(os.Stderr, "-----> Creating persistent data volume...")
				_, err := cli.VolumeCreate(ctx, volume.CreateOptions{
					Name: volumeName,
					Labels: map[string]string{
						"app.mitte.type":       "postgres",
						"app.mitte.instance":   instanceName,
						"app.mitte.created-by": "mitte",
					},
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: Failed to create volume: %v\n", err)
					os.Exit(1)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Error: Could not inspect volume: %v\n", err)
				os.Exit(1)
			}
		}

		// 3. Create Container
		envVars := []string{
			"POSTGRES_PASSWORD=" + dbPassword,
			"POSTGRES_USER=" + user,
		}
		if initialDatabase != "" {
			envVars = append(envVars, "POSTGRES_DB="+initialDatabase)
		}

		containerConfig := &container.Config{
			Image: imageName,
			Env:   envVars,
			Healthcheck: &container.HealthConfig{
				Test:        []string{"CMD-SHELL", fmt.Sprintf("pg_isready -U %s", user)},
				Interval:    10 * time.Second,
				Timeout:     5 * time.Second,
				Retries:     5,
				StartPeriod: 30 * time.Second,
			},
		}
		hostConfig := &container.HostConfig{
			RestartPolicy: container.RestartPolicy{
				Name: "always",
			},
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeVolume,
					Source: volumeName,
					Target: "/var/lib/postgresql/data",
				},
			},
		}

		networkingConfig := &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				"mitte": {},
			},
		}

		resp, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, instanceName)
		if err != nil {
			if errdefs.IsConflict(err) {
				fmt.Fprintf(os.Stderr, "Error: A container named '%s' already exists.\n", instanceName)
			} else {
				fmt.Fprintf(os.Stderr, "Error: Failed to create PostgreSQL container: %v\n", err)
			}
			os.Exit(1)
		}

		// 4. Start Container
		if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to start PostgreSQL container: %v\n", err)
			os.Exit(1)
		}

		// 5. Wait for health check
		fmt.Fprintln(os.Stderr, "-----> Waiting for PostgreSQL to become healthy...")
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
		if initialDatabase == "" {
			svc.DatabaseName = user
		}
		svc.InternalHost = instanceName
		svc.Version = version
		svc.Port = 5432
		svc.Username = user

		if err := svc.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to save service state: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("\nSuccess! PostgreSQL instance '%s' created.\n", instanceName)
		fmt.Printf("User: %s\n", user)
		fmt.Printf("Password: %s\n", dbPassword)
		fmt.Printf("Database: %s\n", svc.DatabaseName)
		fmt.Println("NOTE: This is the only time the password will be displayed. Please save it securely.")
	},
}

var postgresDestroyCmd = &cobra.Command{
	Use:     "destroy <instance-name>",
	Short:   "Permanently destroy a PostgreSQL instance",
	Long:    "This will stop and remove the container AND permanently delete its data volume. THIS ACTION IS IRREVERSIBLE.",
	Aliases: []string{"remove"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		ctx := context.Background()

		// Safety confirmation
		fmt.Printf(" !    WARNING: This will permanently delete the PostgreSQL instance '%s' and all of its data.\n", instanceName)
		fmt.Printf(" >    Please type '%s' to confirm: ", instanceName)
		reader := bufio.NewReader(os.Stdin)
		confirmation, _ := reader.ReadString('\n')
		if strings.TrimSpace(confirmation) != instanceName {
			fmt.Println("Cancelled.")
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "-----> Destroying PostgreSQL instance '%s'...\n", instanceName)

		// Delete the service state first
		svc, err := state.LoadService("postgres", instanceName)
		if err == nil {
			svc.Delete()
		}

		// Destroy the container and volume
		err = services.DestroyPostgres(ctx, instanceName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to destroy PostgreSQL instance: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Success! PostgreSQL instance '%s' has been destroyed.\n", instanceName)
	},
}

var postgresLinkCmd = &cobra.Command{
	Use:   "link <instance-name> <app-name> [VARIABLE_NAME]",
	Short: "Link a PostgreSQL instance to an application",
	Long: `This will set a database connection string as an environment variable on the application and redeploy it.
By default, the variable is named DATABASE_URL. You can specify a custom name as the third argument.`,
	Args: cobra.RangeArgs(2, 3),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		appName := args[1]
		serviceType := "postgres"

		var envVarName string
		if len(args) == 3 {
			envVarName = args[2]
		} else {
			envVarName = "DATABASE_URL"
		}

		fmt.Fprintf(os.Stderr, "-----> Linking PostgreSQL instance '%s' to app '%s'...\n", instanceName, appName)

		service, err := state.LoadService(serviceType, instanceName)
		if err != nil || service.RootPassword == "" {
			fmt.Fprintf(os.Stderr, "Error: Could not find PostgreSQL instance '%s'.\n", instanceName)
			os.Exit(1)
		}

		app, err := state.Load(appName)
		if err != nil || len(app.Domains) == 0 {
			fmt.Fprintf(os.Stderr, "Error: Could not find application '%s'.\n", appName)
			os.Exit(1)
		}

		// Format: postgres://user:password@host:port/database
		databaseURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
			service.Username,
			service.RootPassword,
			service.InternalHost,
			service.Port,
			service.DatabaseName,
		)

		fmt.Fprintf(os.Stderr, "-----> Setting %s config variable...\n", envVarName)
		app.EnvVars[envVarName] = databaseURL
		if err := app.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to save application state: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintln(os.Stderr, "-----> Redeploying application to apply changes...")
		if err := actions.RestartApp(appName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to redeploy application '%s': %v\n", appName, err)
			os.Exit(1)
		}

		fmt.Println("Success! Linked and redeployed.")
	},
}

var postgresBackupCmd = &cobra.Command{
	Use:   "backup <instance-name> <output-file>",
	Short: "Create a backup of a PostgreSQL instance",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		outputFile := args[1]

		fmt.Fprintf(os.Stderr, "-----> Creating backup of PostgreSQL instance '%s'...\n", instanceName)

		err := services.BackupPostgres(context.Background(), instanceName, outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to create backup: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Success! Backup saved to %s\n", outputFile)
	},
}

var postgresRestoreCmd = &cobra.Command{
	Use:   "restore <instance-name> <backup-file>",
	Short: "Restore a PostgreSQL instance from backup",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		backupFile := args[1]

		fmt.Fprintf(os.Stderr, "-----> Restoring PostgreSQL instance '%s' from backup...\n", instanceName)

		err := services.RestorePostgres(context.Background(), instanceName, backupFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to restore from backup: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Success! Database restored from backup.")
	},
}

var postgresUsersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage database users for PostgreSQL instances",
}

var postgresUsersCreateCmd = &cobra.Command{
	Use:   "create <instance-name> <username>",
	Short: "Create a new database user in a PostgreSQL instance",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		username := args[1]

		password, _ := cmd.Flags().GetString("password")
		database, _ := cmd.Flags().GetString("database")

		if password == "" {
			var err error
			password, err = GeneratePassword(32)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Could not generate a secure password: %v\n", err)
				os.Exit(1)
			}
		}

		fmt.Fprintf(os.Stderr, "-----> Creating user '%s' in PostgreSQL instance '%s'...\n", username, instanceName)

		err := services.CreatePostgresUser(context.Background(), instanceName, username, password, database)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to create user: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Success! User '%s' created.\n", username)
		fmt.Printf("Password: %s\n", password)
		if database != "" {
			fmt.Printf("Privileges granted on database: %s\n", database)
		}
	},
}

var postgresUsersDeleteCmd = &cobra.Command{
	Use:     "delete <instance-name> <username>",
	Short:   "Delete a database user from a PostgreSQL instance",
	Aliases: []string{"remove", "rm"},
	Args:    cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		username := args[1]

		fmt.Fprintf(os.Stderr, "-----> Deleting user '%s' from PostgreSQL instance '%s'...\n", username, instanceName)

		err := services.DeletePostgresUser(context.Background(), instanceName, username)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to delete user: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Success! User '%s' deleted.\n", username)
	},
}

var postgresUsersListCmd = &cobra.Command{
	Use:     "list <instance-name>",
	Short:   "List all database users in a PostgreSQL instance",
	Aliases: []string{"ls"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]

		fmt.Fprintf(os.Stderr, "-----> Listing users in PostgreSQL instance '%s'...\n", instanceName)

		users, err := services.ListPostgresUsers(context.Background(), instanceName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to list users: %v\n", err)
			os.Exit(1)
		}

		if len(users) == 0 {
			fmt.Println("No database users found.")
			return
		}

		fmt.Println("Database users:")
		for _, user := range users {
			fmt.Printf("  • %s\n", user)
		}
	},
}

func init() {
	postgresCreateCmd.Flags().String("version", "latest", "PostgreSQL version tag")
	postgresCreateCmd.Flags().String("database", "", "Initial database name")
	postgresCreateCmd.Flags().String("user", "", "Initial user name")
	postgresCreateCmd.Flags().String("password", "", "User password")

	postgresUsersCreateCmd.Flags().String("password", "", "User password")
	postgresUsersCreateCmd.Flags().String("database", "", "Database to grant access to")

	postgresCmd.AddCommand(postgresListCmd)
	postgresCmd.AddCommand(postgresCreateCmd)
	postgresCmd.AddCommand(postgresDestroyCmd)
	postgresCmd.AddCommand(postgresLinkCmd)
	postgresCmd.AddCommand(postgresBackupCmd)
	postgresCmd.AddCommand(postgresRestoreCmd)

	postgresUsersCmd.AddCommand(postgresUsersCreateCmd)
	postgresUsersCmd.AddCommand(postgresUsersDeleteCmd)
	postgresUsersCmd.AddCommand(postgresUsersListCmd)
	postgresCmd.AddCommand(postgresUsersCmd)

	rootCmd.AddCommand(postgresCmd)
}
