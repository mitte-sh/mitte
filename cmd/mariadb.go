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
			fmt.Println("No MariaDB services found.")
			return
		}

		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Could not connect to Docker daemon: %v\n", err)
			os.Exit(1)
		}
		defer cli.Close()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		defer w.Flush()
		fmt.Fprintln(w, "INSTANCE NAME\tSTATUS\tINTERNAL HOST\tVERSION")

		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			instanceName := strings.TrimSuffix(file.Name(), ".json")

			var status, version string

			inspect, err := cli.ContainerInspect(context.Background(), instanceName)
			if err != nil {
				if errdefs.IsNotFound(err) {
					status = "stopped"
					version = "(unknown)"
				} else {
					status = "error"
				}
			} else {
				status = inspect.State.Status
				version = filepath.Base(inspect.Config.Image)
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", instanceName, status, instanceName, version)
		}
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
			fmt.Fprintf(os.Stderr, "Error: Failed to pull MariaDB image: %v\n", err)
			os.Exit(1)
		}
		defer out.Close()

		// Show friendly progress
		fd, isTerminal := term.GetFdInfo(os.Stderr)
		if err := jsonmessage.DisplayJSONMessagesStream(out, os.Stderr, fd, isTerminal, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to read image pull progress: %v\n", err)
			os.Exit(1)
		}

		// 2. Create Volume (optional - only if we want persistent data)
		var volumeName string
		if initialDatabase != "" {
			fmt.Fprintf(os.Stderr, "-----> Creating data volume...\n")
			volumeName = "mitte-mariadb-data-" + instanceName
			_, err = cli.VolumeCreate(ctx, volume.CreateOptions{
				Name: volumeName,
				Labels: map[string]string{
					"app.mitte.managed-by": "mitte",
					"app.mitte.service":    instanceName,
					"app.mitte.type":       "mariadb",
				},
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Failed to create data volume: %v\n", err)
				os.Exit(1)
			}
		}

		// 3. Create Container
		fmt.Fprintf(os.Stderr, "-----> Creating container...\n")
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

		// 5. Wait for MariaDB to be ready (up to 30 seconds)
		fmt.Fprintf(os.Stderr, "-----> Waiting for MariaDB to start...\n")
		for i := 0; i < 30; i++ {
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
			fmt.Println(" !    Confirmation failed. Aborting.")
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "-----> Destroying MariaDB instance '%s'...\n", instanceName)

		// 1. Destroy Docker resources (container and volume)
		if err := services.DestroyMariaDB(ctx, instanceName); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Failed to destroy MariaDB resources: %v\n", err)
			os.Exit(1)
		}

		// 2. Delete the state file
		svc, _ := state.LoadService("mariadb", instanceName)
		if err := svc.Delete(); err != nil {
			// This is not a fatal error, but we should warn the user.
			fmt.Fprintf(os.Stderr, "Warning: Failed to delete service state file: %v\n", err)
		}

		fmt.Printf("Success! MariaDB instance '%s' destroyed.\n", instanceName)
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

func init() {
	mariadbCreateCmd.Flags().String("version", "latest", "The version tag of the MariaDB Docker image to use (e.g., 10.11)")
	mariadbCreateCmd.Flags().String("database", "", "The name of a database to create on first startup")
	mariadbCreateCmd.Flags().String("user", "", "The username for the database user (optional, defaults to root)")
	mariadbCreateCmd.Flags().String("password", "", "The password for the database user (optional, will be generated if not provided)")
	mariadbCmd.AddCommand(mariadbListCmd)
	mariadbCmd.AddCommand(mariadbCreateCmd)
	mariadbCmd.AddCommand(mariadbDestroyCmd)
	mariadbCmd.AddCommand(mariadbLinkCmd)
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
