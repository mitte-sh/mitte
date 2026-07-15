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

	"github.com/mitte-sh/mitte/pkg/actions"
	"github.com/mitte-sh/mitte/pkg/logger"
	"github.com/mitte-sh/mitte/pkg/services"
	"github.com/mitte-sh/mitte/pkg/state"
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
				logger.Info("No MariaDB services have been created yet.")
				return
			}
			logger.Error("Could not read the services directory", "err", err)
			os.Exit(1)
		}

		if len(files) == 0 {
			logger.Info("No MariaDB services have been created yet.")
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
		dataDir, _ := cmd.Flags().GetString("data-dir")
		maxConnections, _ := cmd.Flags().GetInt("max-connections")
		threadCacheSize, _ := cmd.Flags().GetInt("thread-cache-size")
		tableOpenCache, _ := cmd.Flags().GetInt("table-open-cache")
		innodbBufferPoolSize, _ := cmd.Flags().GetString("innodb-buffer-pool-size")
		queryCacheSize, _ := cmd.Flags().GetString("query-cache-size")
		poolingPreset, _ := cmd.Flags().GetString("pooling-preset")

		logger.Info(fmt.Sprintf("-----> Creating MariaDB instance '%s'...", instanceName))

		// Check if it already exists
		svc, _ := state.LoadService("mariadb", instanceName)
		if svc.RootPassword != "" {
			logger.Error(fmt.Sprintf("A MariaDB service named '%s' already exists.", instanceName))
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
				logger.Error("Could not generate a secure password", "err", err)
				os.Exit(1)
			}
		}

		if user == "" {
			user = "root"
		}

		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			logger.Error("Could not connect to Docker daemon", "err", err)
			os.Exit(1)
		}
		defer cli.Close()

		// 1. Pull Image
		imageName := "mariadb:" + version
		logger.Info(fmt.Sprintf("-----> Pulling image %s...", imageName))
		out, err := cli.ImagePull(ctx, imageName, image.PullOptions{})
		if err != nil {
			logger.Error("Failed to pull image", "err", err)
			os.Exit(1)
		}
		defer out.Close()
		termFd, isTerm := term.GetFdInfo(os.Stderr)
		jsonmessage.DisplayJSONMessagesStream(out, os.Stderr, termFd, isTerm, nil)

		// 2. Setup storage (Volume or Bind Mount)
		var volumeName string
		var bindSource string

		if dataDir != "" {
			// Use custom host directory
			absPath, err := filepath.Abs(dataDir)
			if err != nil {
				logger.Error("Failed to resolve data directory path", "err", err)
				os.Exit(1)
			}
			bindSource = absPath

			// Ensure directory exists
			if err := os.MkdirAll(bindSource, 0755); err != nil {
				logger.Error("Failed to create data directory", "err", err)
				os.Exit(1)
			}
			logger.Info(fmt.Sprintf("Using custom data directory: %s", bindSource))
		} else {
			// Use managed volume
			volName := "mitte-mariadb-data-" + instanceName
			if _, err := cli.VolumeInspect(ctx, volName); err != nil {
				if errdefs.IsNotFound(err) {
					logger.Info("-----> Creating persistent data volume...")
					_, err := cli.VolumeCreate(ctx, volume.CreateOptions{
						Name: volName,
						Labels: map[string]string{
							"app.mitte.type":       "mariadb",
							"app.mitte.instance":   instanceName,
							"app.mitte.created-by": "mitte",
						},
					})
					if err != nil {
						logger.Error("Failed to create volume", "err", err)
						os.Exit(1)
					}
					volumeName = volName
				} else {
					logger.Error("Could not inspect volume", "err", err)
					os.Exit(1)
				}
			} else {
				volumeName = volName
			}
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

		// Mount volume or directory
		if volumeName != "" {
			hostConfig.Mounts = []mount.Mount{
				{
					Type:   mount.TypeVolume,
					Source: volumeName,
					Target: "/var/lib/mysql",
				},
			}
		} else if bindSource != "" {
			hostConfig.Mounts = []mount.Mount{
				{
					Type:   mount.TypeBind,
					Source: bindSource,
					Target: "/var/lib/mysql",
				},
			}
		}

		// Generate pooling configuration if specified
		var poolingConfigFile string
		if poolingPreset != "" || maxConnections > 0 || threadCacheSize > 0 || tableOpenCache > 0 || innodbBufferPoolSize != "" || queryCacheSize != "" {
			logger.Info("-----> Generating connection pooling configuration...")

			configContent, err := services.GeneratePoolingConfig(poolingPreset, maxConnections, threadCacheSize, tableOpenCache, innodbBufferPoolSize, queryCacheSize)
			if err != nil {
				logger.Error("Failed to generate pooling configuration", "err", err)
				os.Exit(1)
			}

			// Create temporary config file
			tmpFile, err := os.CreateTemp("", fmt.Sprintf("mitte-pooling-%s-*.cnf", instanceName))
			if err != nil {
				logger.Error("Failed to create temporary config file", "err", err)
				os.Exit(1)
			}
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(configContent); err != nil {
				logger.Error("Failed to write config file", "err", err)
				os.Exit(1)
			}
			tmpFile.Close()

			poolingConfigFile = tmpFile.Name()
			hostConfig.Binds = append(hostConfig.Binds, fmt.Sprintf("%s:/etc/mysql/conf.d/pooling.cnf:ro", poolingConfigFile))
		}

		// Mount custom config file if provided
		if configFile != "" {
			// Validate config file exists
			if _, err := os.Stat(configFile); os.IsNotExist(err) {
				logger.Error(fmt.Sprintf("Config file does not exist: %s", configFile))
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
				logger.Error(fmt.Sprintf("A container named '%s' already exists. Please choose a different name or remove the existing container.", instanceName))
			} else {
				logger.Error("Failed to create MariaDB container", "err", err)
			}
			os.Exit(1)
		}

		// 4. Start Container
		if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			logger.Error("Failed to start MariaDB container", "err", err)
			os.Exit(1)
		}

		// 5. Wait for health check
		logger.Info("-----> Waiting for MariaDB to become healthy...")
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
		svc.DataDir = bindSource

		// Save pooling configuration
		if maxConnections > 0 {
			svc.MaxConnections = maxConnections
		}
		if threadCacheSize > 0 {
			svc.ThreadCacheSize = threadCacheSize
		}
		if tableOpenCache > 0 {
			svc.TableOpenCache = tableOpenCache
		}
		if innodbBufferPoolSize != "" {
			svc.InnoDBBufferPoolSize = innodbBufferPoolSize
		}
		if queryCacheSize != "" {
			svc.QueryCacheSize = queryCacheSize
		}
		if poolingPreset != "" {
			svc.PoolingPreset = poolingPreset
		}

		if err := svc.Save(); err != nil {
			logger.Error("Failed to save service state", "err", err)
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("\nSuccess! MariaDB instance '%s' created.", instanceName))
		if initialDatabase != "" {
			logger.Info(fmt.Sprintf("Initial database '%s' has also been created.", initialDatabase))
		}
		if user != "root" {
			logger.Info(fmt.Sprintf("The user '%s' password is: %s", user, dbPassword))
		} else {
			logger.Info(fmt.Sprintf("The root password is: %s", dbPassword))
		}
		logger.Info("NOTE: This is the only time the password will be displayed. Please save it securely.")
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
		logger.Warn(fmt.Sprintf(" !    WARNING: This will permanently delete the MariaDB instance '%s' and all of its data.", instanceName))
		fmt.Printf(" >    Please type '%s' to confirm: ", instanceName)
		reader := bufio.NewReader(os.Stdin)
		confirmation, _ := reader.ReadString('\n')
		if strings.TrimSpace(confirmation) != instanceName {
			logger.Info("Cancelled.")
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("-----> Destroying MariaDB instance '%s'...", instanceName))

		// Delete the service state first
		svc, err := state.LoadService("mariadb", instanceName)
		if err == nil {
			svc.Delete()
		}

		// Destroy the container and volume
		err = services.DestroyMariaDB(ctx, instanceName)
		if err != nil {
			logger.Error("Failed to destroy MariaDB instance", "err", err)
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("Success! MariaDB instance '%s' has been destroyed.", instanceName))
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

		logger.Info(fmt.Sprintf("-----> Linking MariaDB instance '%s' to app '%s'...", instanceName, appName))

		// 2. Load the service state to get connection details
		service, err := state.LoadService(serviceType, instanceName)
		if err != nil || service.RootPassword == "" {
			logger.Error(fmt.Sprintf("Could not find MariaDB instance '%s'.", instanceName))
			os.Exit(1)
		}

		// 3. Load the application state
		app, err := state.Load(appName)
		if err != nil || (len(app.Domains) == 0 && !app.Internal) {
			logger.Error(fmt.Sprintf("Could not find application '%s'.", appName))
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
		logger.Info("-----> Setting DATABASE_URL config variable...")
		app.EnvVars[envVarName] = databaseURL
		if err := app.Save(); err != nil {
			logger.Error("Failed to save application state with new DATABASE_URL", "err", err)
			os.Exit(1)
		}

		// 6. Redeploy the application to apply the change
		logger.Info("-----> Redeploying application to apply changes...")
		if err := actions.RestartApp(appName); err != nil {
			logger.Error(fmt.Sprintf("Failed to redeploy application '%s'", appName), "err", err)
			os.Exit(1)
		}

		logger.Info("Success! Linked and redeployed. Your app can now connect to the database via the DATABASE_URL environment variable.")
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

		logger.Info(fmt.Sprintf("-----> Creating backup of MariaDB instance '%s'...", instanceName))

		err := services.BackupMariaDB(context.Background(), instanceName, outputFile)
		if err != nil {
			logger.Error("Failed to create backup", "err", err)
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("Success! Backup saved to %s", outputFile))
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

		logger.Info(fmt.Sprintf("-----> Restoring MariaDB instance '%s' from backup...", instanceName))

		err := services.RestoreMariaDB(context.Background(), instanceName, backupFile)
		if err != nil {
			logger.Error("Failed to restore from backup", "err", err)
			os.Exit(1)
		}

		logger.Info("Success! Database restored from backup.")
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

		logger.Info(fmt.Sprintf("-----> Creating user '%s' in MariaDB instance '%s'...", username, instanceName))

		// Generate password if not provided
		if password == "" {
			var err error
			password, err = GeneratePassword(32)
			if err != nil {
				logger.Error("Could not generate a secure password", "err", err)
				os.Exit(1)
			}
		}

		// Default to all databases if not specified
		if database == "" {
			database = "*"
		}

		err := services.CreateMariaDBUser(context.Background(), instanceName, username, password, database, privileges)
		if err != nil {
			logger.Error("Failed to create user", "err", err)
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("Success! User '%s' created in MariaDB instance '%s'.", username, instanceName))
		logger.Info(fmt.Sprintf("Password: %s", password))
		logger.Info(fmt.Sprintf("Database: %s", database))
		if len(privileges) > 0 {
			logger.Info(fmt.Sprintf("Privileges: %s", strings.Join(privileges, ", ")))
		}
		logger.Info("NOTE: This is the only time the password will be displayed. Please save it securely.")
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

		logger.Info(fmt.Sprintf("-----> Deleting user '%s' from MariaDB instance '%s'...", username, instanceName))

		err := services.DeleteMariaDBUser(context.Background(), instanceName, username)
		if err != nil {
			logger.Error("Failed to delete user", "err", err)
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("Success! User '%s' deleted from MariaDB instance '%s'.", username, instanceName))
	},
}

var mariadbUsersListCmd = &cobra.Command{
	Use:     "list <instance-name>",
	Short:   "List all database users in a MariaDB instance",
	Aliases: []string{"ls"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]

		logger.Info(fmt.Sprintf("-----> Listing users in MariaDB instance '%s'...", instanceName))

		users, err := services.ListMariaDBUsers(context.Background(), instanceName)
		if err != nil {
			logger.Error("Failed to list users", "err", err)
			os.Exit(1)
		}

		if len(users) == 0 {
			logger.Info("No database users found (excluding system users).")
			return
		}

		logger.Info("Database users:")
		for _, user := range users {
			logger.Info(fmt.Sprintf("  • %s", user))
		}
	},
}

var mariadbUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Manage MariaDB version upgrades",
}

var mariadbUpgradeCheckCmd = &cobra.Command{
	Use:   "check <instance-name>",
	Short: "Check if an upgrade is available for a MariaDB instance",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]

		logger.Info(fmt.Sprintf("-----> Checking upgrade status for MariaDB instance '%s'...", instanceName))

		currentVersion, configuredVersion, err := services.CheckMariaDBUpgrade(context.Background(), instanceName)
		if err != nil {
			logger.Error("Failed to check upgrade status", "err", err)
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("MariaDB instance: %s", instanceName))
		logger.Info(fmt.Sprintf("Configured version: %s", configuredVersion))
		logger.Info(fmt.Sprintf("Current running version: %s", currentVersion))

		if currentVersion != "" && configuredVersion != "" {
			// Simple version comparison
			if currentVersion != configuredVersion {
				logger.Info("\n⚠️  Version mismatch detected!")
				logger.Info("The running version differs from the configured version.")
				logger.Info("Run 'mitte mariadb upgrade <instance-name> --to-version=<version>' to upgrade.")
			} else {
				logger.Info("\n✅ Version matches configured version.")
			}
		}
	},
}

var mariadbUpgradePerformCmd = &cobra.Command{
	Use:   "perform <instance-name>",
	Short: "Perform a version upgrade on a MariaDB instance",
	Long: `This command upgrades a MariaDB instance to a new version while preserving all data.
It will:
1. Create a backup of all databases
2. Stop and remove the current container
3. Start a new container with the target version using the same data volume
4. Run mysql_upgrade if needed
5. Update the service state with the new version

WARNING: This will cause temporary downtime for the database.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		instanceName := args[0]
		targetVersion, _ := cmd.Flags().GetString("to-version")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if targetVersion == "" {
			logger.Error("Target version is required. Use --to-version flag.")
			os.Exit(1)
		}

		logger.Info(fmt.Sprintf("-----> Planning upgrade of MariaDB instance '%s'...", instanceName))

		if dryRun {
			logger.Info("DRY RUN MODE: No changes will be made.")
		}

		// Safety confirmation
		if !dryRun {
			logger.Warn(fmt.Sprintf(" !    WARNING: This will upgrade MariaDB instance '%s' to version %s.", instanceName, targetVersion))
			logger.Warn(" !    The database will be temporarily unavailable during the upgrade.")
			fmt.Printf(" >    Type 'yes' to confirm: ")
			reader := bufio.NewReader(os.Stdin)
			confirmation, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(confirmation)) != "yes" {
				logger.Info("Cancelled.")
				os.Exit(1)
			}
		}

		err := services.UpgradeMariaDB(context.Background(), instanceName, targetVersion, dryRun)
		if err != nil {
			logger.Error("Failed to upgrade MariaDB instance", "err", err)
			os.Exit(1)
		}

		if dryRun {
			logger.Info("Dry run completed successfully. No changes were made.")
		} else {
			logger.Info(fmt.Sprintf("Success! MariaDB instance '%s' has been upgraded to version %s.", instanceName, targetVersion))
		}
	},
}

func init() {
	mariadbCreateCmd.Flags().String("version", "latest", "The version tag of the MariaDB Docker image to use (e.g., 10.11)")
	mariadbCreateCmd.Flags().String("database", "", "The name of a database to create on first startup")
	mariadbCreateCmd.Flags().String("user", "", "The username for the database user (optional, defaults to root)")
	mariadbCreateCmd.Flags().String("password", "", "The password for the database user (optional, will be generated if not provided)")
	mariadbCreateCmd.Flags().String("config-file", "", "Path to a custom MariaDB configuration file (.cnf) to mount into the container")
	mariadbCreateCmd.Flags().String("data-dir", "", "Host directory to store database data (optional)")
	mariadbCreateCmd.Flags().Int("max-connections", 0, "Maximum number of concurrent connections (default: MariaDB default)")
	mariadbCreateCmd.Flags().Int("thread-cache-size", 0, "Number of threads to cache for reuse (default: MariaDB default)")
	mariadbCreateCmd.Flags().Int("table-open-cache", 0, "Number of table descriptors to cache (default: MariaDB default)")
	mariadbCreateCmd.Flags().String("innodb-buffer-pool-size", "", "Size of InnoDB buffer pool (e.g., 1G, 512M)")
	mariadbCreateCmd.Flags().String("query-cache-size", "", "Size of query cache (e.g., 128M, 256M)")
	mariadbCreateCmd.Flags().String("pooling-preset", "", "Connection pooling preset (small, medium, large, high-traffic)")
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

	// Upgrade commands
	mariadbUpgradePerformCmd.Flags().String("to-version", "", "Target MariaDB version (e.g., 10.11, latest)")
	mariadbUpgradePerformCmd.Flags().Bool("dry-run", false, "Simulate upgrade without making changes")
	mariadbUpgradePerformCmd.MarkFlagRequired("to-version")

	mariadbUpgradeCmd.AddCommand(mariadbUpgradeCheckCmd)
	mariadbUpgradeCmd.AddCommand(mariadbUpgradePerformCmd)
	mariadbCmd.AddCommand(mariadbUpgradeCmd)

	// Connections management commands
	var mariadbConnectionsCmd = &cobra.Command{
		Use:   "connections",
		Short: "Manage MariaDB connection pooling",
	}

	var mariadbConnectionsStatsCmd = &cobra.Command{
		Use:   "stats <instance-name>",
		Short: "Show connection statistics for a MariaDB instance",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			instanceName := args[0]

			logger.Info(fmt.Sprintf("-----> Getting connection statistics for MariaDB instance '%s'...", instanceName))

			stats, err := services.AnalyzeConnections(context.Background(), instanceName)
			if err != nil {
				logger.Error("Failed to get connection statistics", "err", err)
				os.Exit(1)
			}

			logger.Info("Connection Statistics:")
			logger.Info(fmt.Sprintf("  Max Connections: %d", stats.MaxConnections))
			logger.Info(fmt.Sprintf("  Threads Connected: %d", stats.ThreadsConnected))
			logger.Info(fmt.Sprintf("  Threads Running: %d", stats.ThreadsRunning))
			logger.Info(fmt.Sprintf("  Threads Cached: %d", stats.ThreadsCached))
			logger.Info(fmt.Sprintf("  Threads Created: %d", stats.ThreadsCreated))
			logger.Info(fmt.Sprintf("  Connection Usage: %.1f%%", stats.ConnectionUsage))
			logger.Info(fmt.Sprintf("  Connection Churn: %.2f", stats.ConnectionChurn))
		},
	}

	var mariadbConnectionsAnalyzeCmd = &cobra.Command{
		Use:   "analyze <instance-name>",
		Short: "Analyze connection usage in a MariaDB instance",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			instanceName := args[0]

			logger.Info(fmt.Sprintf("-----> Analyzing connections for MariaDB instance '%s'...", instanceName))

			stats, err := services.AnalyzeConnections(context.Background(), instanceName)
			if err != nil {
				logger.Error("Failed to analyze connections", "err", err)
				os.Exit(1)
			}

			logger.Info("Connection Statistics:")
			logger.Info(fmt.Sprintf("  Max Connections: %d", stats.MaxConnections))
			logger.Info(fmt.Sprintf("  Threads Connected: %d", stats.ThreadsConnected))
			logger.Info(fmt.Sprintf("  Threads Running: %d", stats.ThreadsRunning))
			logger.Info(fmt.Sprintf("  Threads Cached: %d", stats.ThreadsCached))
			logger.Info(fmt.Sprintf("  Threads Created: %d", stats.ThreadsCreated))
			logger.Info(fmt.Sprintf("  Connection Usage: %.1f%%", stats.ConnectionUsage))
			logger.Info(fmt.Sprintf("  Connection Churn: %.2f", stats.ConnectionChurn))

			logger.Info("\nAnalysis:")
			if stats.ConnectionUsage > 80 {
				logger.Warn("  ⚠️  High connection usage! Consider increasing max_connections.")
			} else if stats.ConnectionUsage > 50 {
				logger.Warn("  ⚠️  Moderate connection usage. Monitor for growth.")
			} else {
				logger.Info("  ✅ Connection usage is healthy.")
			}

			if stats.ConnectionChurn > 10 {
				logger.Warn("  ⚠️  High connection churn! Consider increasing thread_cache_size.")
			} else if stats.ConnectionChurn > 5 {
				logger.Warn("  ⚠️  Moderate connection churn. Monitor thread creation.")
			} else {
				logger.Info("  ✅ Connection caching is effective.")
			}

			if stats.ThreadsRunning > stats.ThreadsConnected/2 {
				logger.Warn("  ⚠️  High number of running threads. Check for long-running queries.")
			}
		},
	}

	var mariadbConnectionsOptimizeCmd = &cobra.Command{
		Use:   "optimize <instance-name>",
		Short: "Optimize connection pooling configuration",
		Long: `This command analyzes current connection usage and suggests optimal
pooling configuration based on the workload patterns.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			instanceName := args[0]
			preset, _ := cmd.Flags().GetString("preset")

			logger.Info(fmt.Sprintf("-----> Optimizing connection pooling for MariaDB instance '%s'...", instanceName))

			// Load current service to get existing configuration
			svc, err := state.LoadService("mariadb", instanceName)
			if err != nil {
				logger.Error("Failed to load service state", "err", err)
				os.Exit(1)
			}

			// Use preset if provided, otherwise analyze and suggest
			if preset == "" {
				// Analyze current usage to suggest preset
				stats, err := services.AnalyzeConnections(context.Background(), instanceName)
				if err != nil {
					logger.Error("Failed to analyze connections", "err", err)
					os.Exit(1)
				}

				// Suggest preset based on usage
				if stats.ConnectionUsage > 70 || stats.ThreadsConnected > 200 {
					preset = "high-traffic"
				} else if stats.ThreadsConnected > 100 {
					preset = "large"
				} else if stats.ThreadsConnected > 50 {
					preset = "medium"
				} else {
					preset = "small"
				}

				logger.Info(fmt.Sprintf("Based on current usage (%d connections), suggesting '%s' preset.", stats.ThreadsConnected, preset))
			}

			// Generate configuration using existing values or preset defaults with resource detection
			config, err := services.GeneratePoolingConfigWithResources(
				preset,
				svc.MaxConnections,
				svc.ThreadCacheSize,
				svc.TableOpenCache,
				svc.InnoDBBufferPoolSize,
				svc.QueryCacheSize,
				instanceName,
			)
			if err != nil {
				logger.Error("Failed to generate pooling configuration", "err", err)
				os.Exit(1)
			}

			logger.Info("Recommended Pooling Configuration:")
			logger.Info(config)
			logger.Info("To apply this configuration:")
			logger.Info("  1. Save the above configuration to a file (e.g., pooling.cnf)")
			logger.Info(fmt.Sprintf("  2. Run: mitte mariadb create %s --config-file=pooling.cnf", instanceName))
			logger.Info("     (Note: This will recreate the instance. For existing instances, manually update the config file.)")
		},
	}

	var mariadbConnectionsApplyCmd = &cobra.Command{
		Use:   "apply <instance-name>",
		Short: "Apply connection pooling configuration to an existing MariaDB instance",
		Long: `This command applies connection pooling configuration to an existing MariaDB instance.
You can either provide a configuration file or use command-line flags to specify
pooling parameters. The configuration will be applied immediately.

Some parameters require a container restart to take effect. Use --restart flag
to automatically restart the container after applying configuration.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			instanceName := args[0]
			configFile, _ := cmd.Flags().GetString("config-file")
			maxConnections, _ := cmd.Flags().GetInt("max-connections")
			threadCacheSize, _ := cmd.Flags().GetInt("thread-cache-size")
			tableOpenCache, _ := cmd.Flags().GetInt("table-open-cache")
			innodbBufferPoolSize, _ := cmd.Flags().GetString("innodb-buffer-pool-size")
			queryCacheSize, _ := cmd.Flags().GetString("query-cache-size")
			poolingPreset, _ := cmd.Flags().GetString("pooling-preset")
			restart, _ := cmd.Flags().GetBool("restart")

			logger.Info(fmt.Sprintf("-----> Applying connection pooling configuration to MariaDB instance '%s'...", instanceName))

			var configContent string
			var err error

			if configFile != "" {
				// Read configuration from file
				content, err := os.ReadFile(configFile)
				if err != nil {
					logger.Error("Failed to read config file", "err", err)
					os.Exit(1)
				}
				configContent = string(content)
			} else {
				// Generate configuration from flags/preset with resource detection
				configContent, err = services.GeneratePoolingConfigWithResources(
					poolingPreset,
					maxConnections,
					threadCacheSize,
					tableOpenCache,
					innodbBufferPoolSize,
					queryCacheSize,
					instanceName,
				)
				if err != nil {
					logger.Error("Failed to generate pooling configuration", "err", err)
					os.Exit(1)
				}
			}

			// Apply the configuration
			if err := services.ApplyPoolingConfig(context.Background(), instanceName, configContent, restart); err != nil {
				logger.Error("Failed to apply pooling configuration", "err", err)
				os.Exit(1)
			}

			logger.Info("Success! Connection pooling configuration has been applied.")
			if restart {
				logger.Info("Container was restarted to apply changes.")
			} else {
				logger.Info("Configuration was reloaded without restart.")
				logger.Info("Note: Some parameters may require a restart to take full effect.")
			}
		},
	}

	mariadbConnectionsApplyCmd.Flags().String("config-file", "", "Path to a MariaDB configuration file (.cnf) to apply")
	mariadbConnectionsApplyCmd.Flags().Int("max-connections", 0, "Maximum number of concurrent connections")
	mariadbConnectionsApplyCmd.Flags().Int("thread-cache-size", 0, "Number of threads to cache for reuse")
	mariadbConnectionsApplyCmd.Flags().Int("table-open-cache", 0, "Number of table descriptors to cache")
	mariadbConnectionsApplyCmd.Flags().String("innodb-buffer-pool-size", "", "Size of InnoDB buffer pool (e.g., 1G, 512M)")
	mariadbConnectionsApplyCmd.Flags().String("query-cache-size", "", "Size of query cache (e.g., 128M, 256M)")
	mariadbConnectionsApplyCmd.Flags().String("pooling-preset", "", "Connection pooling preset (small, medium, large, high-traffic)")
	mariadbConnectionsApplyCmd.Flags().Bool("restart", false, "Restart container after applying configuration")

	mariadbConnectionsOptimizeCmd.Flags().String("preset", "", "Connection pooling preset to use (small, medium, large, high-traffic)")

	mariadbConnectionsCmd.AddCommand(mariadbConnectionsStatsCmd)
	mariadbConnectionsCmd.AddCommand(mariadbConnectionsAnalyzeCmd)
	mariadbConnectionsCmd.AddCommand(mariadbConnectionsOptimizeCmd)
	mariadbConnectionsCmd.AddCommand(mariadbConnectionsApplyCmd)
	mariadbCmd.AddCommand(mariadbConnectionsCmd)

	rootCmd.AddCommand(mariadbCmd)
}
