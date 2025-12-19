package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/mitteapp/mitteapp/pkg/state"
)

func CreateMariaDB(ctx context.Context, instanceName, rootPassword, version, configFile string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	theImage := fmt.Sprintf("mariadb:%s", version)

	fmt.Printf("Pulling image %s...\n", theImage)
	out, err := cli.ImagePull(ctx, theImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer out.Close()
	io.Copy(os.Stdout, out)

	config := &container.Config{
		Image: theImage,
		Env: []string{
			fmt.Sprintf("MARIADB_ROOT_PASSWORD=%s", rootPassword),
			// TODO: Add more config vars here, like MARIADB_DATABASE
		},
		Healthcheck: &container.HealthConfig{
			Test:        []string{"mysqladmin", "ping", "-h", "localhost"},
			Interval:    10 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     3,
			StartPeriod: 30 * time.Second,
		},
	}

	hostConfig := &container.HostConfig{
		Binds:         []string{fmt.Sprintf("mitte-db-%s:/var/lib/mysql", instanceName)},
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}

	// Mount custom config file if provided
	if configFile != "" {
		// Validate config file exists
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			return fmt.Errorf("config file does not exist: %s", configFile)
		}

		// Add bind mount for config file
		hostConfig.Binds = append(hostConfig.Binds, fmt.Sprintf("%s:/etc/mysql/conf.d/custom.cnf:ro", configFile))
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"mitte": {},
		},
	}

	resp, err := cli.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, instanceName)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	return cli.ContainerStart(ctx, resp.ID, container.StartOptions{})
}

// DestroyMariaDB stops and removes the container and its associated data volume.
func DestroyMariaDB(ctx context.Context, instanceName string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	// 1. Stop and remove the container.
	// We use Force to remove it even if it's running.
	if err := cli.ContainerRemove(ctx, instanceName, container.RemoveOptions{Force: true}); err != nil {
		// Ignore "not found" errors, as our goal is to ensure it's gone.
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove container: %w", err)
		}
	}

	// 2. Remove the persistent volume. This is the crucial step to delete the data.
	volumeName := fmt.Sprintf("mitte-db-%s", instanceName)
	if err := cli.VolumeRemove(ctx, volumeName, true); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove volume '%s': %w", volumeName, err)
		}
	}

	return nil
}

// BackupMariaDB creates a backup of all databases in the MariaDB container
func BackupMariaDB(ctx context.Context, instanceName, outputPath string) error {
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	if svc.RootPassword == "" {
		return fmt.Errorf("root password not found in service state")
	}

	// Run mysqldump inside the container
	cmd := exec.Command("docker", "exec", instanceName, "mysqldump", "-u", "root", "-p"+svc.RootPassword, "--all-databases")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to run mysqldump: %w", err)
	}

	// Write output to file
	err = os.WriteFile(outputPath, output, 0644)
	if err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}

	return nil
}

// RestoreMariaDB restores databases from a backup file into the MariaDB container
func RestoreMariaDB(ctx context.Context, instanceName, backupPath string) error {
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	if svc.RootPassword == "" {
		return fmt.Errorf("root password not found in service state")
	}

	// Read backup file
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	// Run mysql restore inside the container
	cmd := exec.Command("docker", "exec", "-i", instanceName, "mysql", "-u", "root", "-p"+svc.RootPassword)
	cmd.Stdin = bytes.NewReader(backupData)
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run mysql restore: %w", err)
	}

	return nil
}

// CreateMariaDBUser creates a new database user in a MariaDB instance
func CreateMariaDBUser(ctx context.Context, instanceName, username, password, database string, privileges []string) error {
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	if svc.RootPassword == "" {
		return fmt.Errorf("root password not found in service state")
	}

	// Build SQL commands
	var sqlCommands []string

	// Create user
	sqlCommands = append(sqlCommands, fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s';", username, password))

	// Grant privileges
	if len(privileges) > 0 {
		privs := strings.Join(privileges, ", ")
		if database == "*" {
			sqlCommands = append(sqlCommands, fmt.Sprintf("GRANT %s ON *.* TO '%s'@'%%';", privs, username))
		} else {
			sqlCommands = append(sqlCommands, fmt.Sprintf("GRANT %s ON `%s`.* TO '%s'@'%%';", privs, database, username))
		}
	}

	// Flush privileges
	sqlCommands = append(sqlCommands, "FLUSH PRIVILEGES;")

	// Execute SQL commands
	for _, sql := range sqlCommands {
		cmd := exec.Command("docker", "exec", instanceName, "mariadb", "-u", "root", "-p"+svc.RootPassword, "-e", sql)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to execute SQL '%s': %w\nOutput: %s", sql, err, output)
		}
	}

	return nil
}

// DeleteMariaDBUser deletes a database user from a MariaDB instance
func DeleteMariaDBUser(ctx context.Context, instanceName, username string) error {
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	if svc.RootPassword == "" {
		return fmt.Errorf("root password not found in service state")
	}

	// Build SQL command to drop user
	sql := fmt.Sprintf("DROP USER IF EXISTS '%s'@'%%';", username)

	// Execute SQL command
	cmd := exec.Command("docker", "exec", instanceName, "mariadb", "-u", "root", "-p"+svc.RootPassword, "-e", sql)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete user: %w\nOutput: %s", err, output)
	}

	return nil
}

// ListMariaDBUsers lists all database users in a MariaDB instance
func ListMariaDBUsers(ctx context.Context, instanceName string) ([]string, error) {
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		return nil, fmt.Errorf("failed to load service state: %w", err)
	}

	if svc.RootPassword == "" {
		return nil, fmt.Errorf("root password not found in service state")
	}

	// SQL to list users (excluding system users)
	sql := "SELECT CONCAT('''', user, '''@''', host, '''') as user FROM mysql.user WHERE user NOT IN ('root', 'mariadb.sys', 'mysql');"

	// Execute SQL command
	cmd := exec.Command("docker", "exec", instanceName, "mariadb", "-u", "root", "-p"+svc.RootPassword, "-N", "-e", sql)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	// Parse output
	users := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(users) == 1 && users[0] == "" {
		return []string{}, nil
	}

	return users, nil
}
