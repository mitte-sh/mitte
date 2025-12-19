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

// CheckMariaDBUpgrade checks if an upgrade is available for a MariaDB instance
func CheckMariaDBUpgrade(ctx context.Context, instanceName string) (string, string, error) {
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		return "", "", fmt.Errorf("failed to load service state: %w", err)
	}

	// Get current version from container
	cmd := exec.Command("docker", "exec", instanceName, "mariadb", "-u", "root", "-p"+svc.RootPassword, "-N", "-e", "SELECT VERSION();")
	output, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("failed to get current version: %w", err)
	}

	currentVersion := strings.TrimSpace(string(output))

	// For now, we'll just return the current version
	// In a real implementation, we would check Docker Hub for newer versions
	return currentVersion, svc.Version, nil
}

// UpgradeMariaDB upgrades a MariaDB instance to a new version
func UpgradeMariaDB(ctx context.Context, instanceName, targetVersion string, dryRun bool) error {
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	if svc.RootPassword == "" {
		return fmt.Errorf("root password not found in service state")
	}

	// Check if we're already on the target version
	if svc.Version == targetVersion {
		return fmt.Errorf("already on version %s", targetVersion)
	}

	fmt.Printf("Planning upgrade of MariaDB instance '%s' from %s to %s\n", instanceName, svc.Version, targetVersion)

	if dryRun {
		fmt.Println("Dry run mode - no changes will be made")
		return nil
	}

	// 1. Create backup
	backupFile := fmt.Sprintf("/tmp/mitte-mariadb-backup-%s-%s.sql", instanceName, time.Now().Format("20060102-150405"))
	fmt.Printf("Creating backup to %s...\n", backupFile)
	if err := BackupMariaDB(ctx, instanceName, backupFile); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// 2. Stop current container
	fmt.Println("Stopping current container...")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	timeout := 30
	if err := cli.ContainerStop(ctx, instanceName, container.StopOptions{Timeout: &timeout}); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to stop container: %w", err)
		}
	}

	// 3. Remove current container (keep volume)
	fmt.Println("Removing current container...")
	if err := cli.ContainerRemove(ctx, instanceName, container.RemoveOptions{}); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove container: %w", err)
		}
	}

	// 4. Pull new image
	newImage := fmt.Sprintf("mariadb:%s", targetVersion)
	fmt.Printf("Pulling new image %s...\n", newImage)
	out, err := cli.ImagePull(ctx, newImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull new image: %w", err)
	}
	defer out.Close()
	io.Copy(os.Stdout, out)

	// 5. Create new container with same volume and configuration
	fmt.Println("Creating new container...")

	envVars := []string{
		fmt.Sprintf("MARIADB_ROOT_PASSWORD=%s", svc.RootPassword),
		"MARIADB_ROOT_HOST=%",
	}
	if svc.Username != "root" {
		envVars = append(envVars, fmt.Sprintf("MARIADB_USER=%s", svc.Username), fmt.Sprintf("MARIADB_PASSWORD=%s", svc.UserPassword))
	}
	if svc.DatabaseName != "" {
		envVars = append(envVars, fmt.Sprintf("MARIADB_DATABASE=%s", svc.DatabaseName))
	}

	config := &container.Config{
		Image: newImage,
		Env:   envVars,
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

	// Mount custom config file if it exists
	if svc.ConfigFile != "" {
		if _, err := os.Stat(svc.ConfigFile); err == nil {
			hostConfig.Binds = append(hostConfig.Binds, fmt.Sprintf("%s:/etc/mysql/conf.d/custom.cnf:ro", svc.ConfigFile))
		}
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"mitte": {},
		},
	}

	resp, err := cli.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, instanceName)
	if err != nil {
		return fmt.Errorf("failed to create new container: %w", err)
	}

	// 6. Start new container
	fmt.Println("Starting new container...")
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start new container: %w", err)
	}

	// 7. Wait for health check
	fmt.Println("Waiting for MariaDB to become healthy...")
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

	// 8. Run mysql_upgrade if needed (for major version upgrades)
	fmt.Println("Checking if mysql_upgrade is needed...")
	cmd := exec.Command("docker", "exec", instanceName, "mysql_upgrade", "-u", "root", "-p"+svc.RootPassword)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// mysql_upgrade may fail on certain versions, log but continue
		fmt.Printf("mysql_upgrade output: %s\n", output)
	}

	// 9. Update service state with new version and migration info
	svc.PreviousVersion = svc.Version
	svc.Version = targetVersion
	svc.LastBackupPath = backupFile
	svc.UpgradedAt = time.Now().Format(time.RFC3339)
	if err := svc.Save(); err != nil {
		return fmt.Errorf("failed to update service state: %w", err)
	}

	fmt.Printf("Successfully upgraded MariaDB instance '%s' from %s to %s\n", instanceName, svc.Version, targetVersion)
	fmt.Printf("Backup saved to: %s\n", backupFile)

	return nil
}
