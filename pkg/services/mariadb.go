package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/mitte-sh/mitte/pkg/state"
)

func CreateMariaDB(ctx context.Context, instanceName, rootPassword, version, configFile, dataDir string) error {
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
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}

	if dataDir != "" {
		hostConfig.Binds = []string{fmt.Sprintf("%s:/var/lib/mysql", dataDir)}
	} else {
		hostConfig.Binds = []string{fmt.Sprintf("mitte-mariadb-data-%s:/var/lib/mysql", instanceName)}
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

	// 2. Remove the persistent volume if it was used.
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		// If we can't load the service state, we might not know if it used a custom data dir.
		// However, we should try to remove the default volume anyway to be safe/clean if it exists.
		// But strictly speaking we should probably warn.
		// For now, let's proceed with default volume removal if state load fails, assuming default.
	}

	// Only remove volume if DataDir is empty (meaning managed volume was used)
	if svc == nil || svc.DataDir == "" {
		volumeName := fmt.Sprintf("mitte-mariadb-data-%s", instanceName)
		if err := cli.VolumeRemove(ctx, volumeName, true); err != nil {
			if !client.IsErrNotFound(err) {
				return fmt.Errorf("failed to remove volume '%s': %w", volumeName, err)
			}
		}
	} else {
		fmt.Printf("Skipping volume removal for custom data directory: %s\n", svc.DataDir)
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
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}

	if svc.DataDir != "" {
		hostConfig.Binds = []string{fmt.Sprintf("%s:/var/lib/mysql", svc.DataDir)}
	} else {
		hostConfig.Binds = []string{fmt.Sprintf("mitte-mariadb-data-%s:/var/lib/mysql", instanceName)}
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

// GeneratePoolingConfig generates MariaDB configuration for connection pooling
func GeneratePoolingConfig(preset string, maxConnections, threadCacheSize, tableOpenCache int, innodbBufferPoolSize, queryCacheSize string) (string, error) {
	return GeneratePoolingConfigWithResources(preset, maxConnections, threadCacheSize, tableOpenCache, innodbBufferPoolSize, queryCacheSize, "")
}

// GeneratePoolingConfigWithResources generates MariaDB configuration for connection pooling with resource detection
func GeneratePoolingConfigWithResources(preset string, maxConnections, threadCacheSize, tableOpenCache int, innodbBufferPoolSize, queryCacheSize, instanceName string) (string, error) {
	var config strings.Builder
	config.WriteString("[mysqld]\n")

	// Detect container memory if instanceName is provided and innodbBufferPoolSize is not specified
	if instanceName != "" && innodbBufferPoolSize == "" {
		// Try to detect container memory limits
		memoryMB := detectContainerMemory(instanceName)
		if memoryMB > 0 {
			// Calculate optimal buffer pool size (70-80% of available RAM)
			// But leave at least 256MB for OS and other processes
			optimalBufferPoolMB := int(float64(memoryMB) * 0.75)
			if optimalBufferPoolMB < 256 {
				optimalBufferPoolMB = 256
			}

			// Convert to human-readable format
			if optimalBufferPoolMB >= 1024 {
				innodbBufferPoolSize = fmt.Sprintf("%dG", optimalBufferPoolMB/1024)
			} else {
				innodbBufferPoolSize = fmt.Sprintf("%dM", optimalBufferPoolMB)
			}
		}
	}

	// Apply preset if specified
	if preset != "" {
		switch preset {
		case "small":
			if maxConnections == 0 {
				maxConnections = 100
			}
			if threadCacheSize == 0 {
				threadCacheSize = 8
			}
			if tableOpenCache == 0 {
				tableOpenCache = 400
			}
			if innodbBufferPoolSize == "" {
				innodbBufferPoolSize = "256M"
			}
			if queryCacheSize == "" {
				queryCacheSize = "64M"
			}
		case "medium":
			if maxConnections == 0 {
				maxConnections = 200
			}
			if threadCacheSize == 0 {
				threadCacheSize = 50
			}
			if tableOpenCache == 0 {
				tableOpenCache = 1000
			}
			if innodbBufferPoolSize == "" {
				innodbBufferPoolSize = "1G"
			}
			if queryCacheSize == "" {
				queryCacheSize = "128M"
			}
		case "large":
			if maxConnections == 0 {
				maxConnections = 300
			}
			if threadCacheSize == 0 {
				threadCacheSize = 75
			}
			if tableOpenCache == 0 {
				tableOpenCache = 1500
			}
			if innodbBufferPoolSize == "" {
				innodbBufferPoolSize = "2G"
			}
			if queryCacheSize == "" {
				queryCacheSize = "256M"
			}
		case "high-traffic":
			if maxConnections == 0 {
				maxConnections = 500
			}
			if threadCacheSize == 0 {
				threadCacheSize = 100
			}
			if tableOpenCache == 0 {
				tableOpenCache = 2000
			}
			if innodbBufferPoolSize == "" {
				innodbBufferPoolSize = "4G"
			}
			if queryCacheSize == "" {
				queryCacheSize = "512M"
			}
			config.WriteString("max_connect_errors = 1000000\n")
			config.WriteString("connect_timeout = 10\n")
			config.WriteString("wait_timeout = 600\n")
			config.WriteString("interactive_timeout = 600\n")
		default:
			return "", fmt.Errorf("unknown pooling preset: %s. Available presets: small, medium, large, high-traffic", preset)
		}
	}

	// Add individual settings if specified
	if maxConnections > 0 {
		config.WriteString(fmt.Sprintf("max_connections = %d\n", maxConnections))
	}
	if threadCacheSize > 0 {
		config.WriteString(fmt.Sprintf("thread_cache_size = %d\n", threadCacheSize))
	}
	if tableOpenCache > 0 {
		config.WriteString(fmt.Sprintf("table_open_cache = %d\n", tableOpenCache))
	}
	if innodbBufferPoolSize != "" {
		config.WriteString(fmt.Sprintf("innodb_buffer_pool_size = %s\n", innodbBufferPoolSize))
	}
	if queryCacheSize != "" {
		config.WriteString(fmt.Sprintf("query_cache_size = %s\n", queryCacheSize))
	}

	return config.String(), nil
}

// detectContainerMemory tries to detect the memory limit of a Docker container
func detectContainerMemory(containerName string) int {
	// Try to get memory limit from Docker inspect
	cmd := exec.Command("docker", "inspect", containerName, "--format", "{{.HostConfig.Memory}}")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	memoryStr := strings.TrimSpace(string(output))
	if memoryStr == "0" || memoryStr == "" {
		// No memory limit set, try to get from cgroups
		cmd = exec.Command("docker", "exec", containerName, "cat", "/sys/fs/cgroup/memory/memory.limit_in_bytes")
		output, err = cmd.Output()
		if err != nil {
			return 0
		}
		memoryStr = strings.TrimSpace(string(output))
	}

	// Parse memory value
	memoryBytes, err := strconv.ParseInt(memoryStr, 10, 64)
	if err != nil {
		return 0
	}

	// Convert bytes to megabytes
	memoryMB := int(memoryBytes / 1024 / 1024)

	// If memory limit is very large (like 9223372036854771712 for unlimited), return 0
	if memoryMB > 1000000 { // More than 1TB, probably unlimited
		return 0
	}

	return memoryMB
}

// ConnectionStats holds MariaDB connection statistics
type ConnectionStats struct {
	ThreadsConnected int     `json:"threads_connected"`
	ThreadsRunning   int     `json:"threads_running"`
	ThreadsCached    int     `json:"threads_cached"`
	ThreadsCreated   int     `json:"threads_created"`
	MaxConnections   int     `json:"max_connections"`
	ConnectionUsage  float64 `json:"connection_usage"`
	ConnectionChurn  float64 `json:"connection_churn"`
}

// AnalyzeConnections analyzes connection usage in a MariaDB instance
func AnalyzeConnections(ctx context.Context, instanceName string) (*ConnectionStats, error) {
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		return nil, fmt.Errorf("failed to load service state: %w", err)
	}

	if svc.RootPassword == "" {
		return nil, fmt.Errorf("root password not found in service state")
	}

	stats := &ConnectionStats{}

	// Get max_connections
	maxConnSQL := "SHOW VARIABLES LIKE 'max_connections';"
	cmd := exec.Command("docker", "exec", instanceName, "mariadb", "-u", "root", "-p"+svc.RootPassword, "-N", "-e", maxConnSQL)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get max_connections: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\t")
	if len(lines) >= 2 {
		if val, err := strconv.Atoi(lines[1]); err == nil {
			stats.MaxConnections = val
		}
	}

	// Get connection status
	statusSQL := "SHOW STATUS WHERE Variable_name IN ('Threads_connected', 'Threads_running', 'Threads_cached', 'Threads_created');"
	cmd = exec.Command("docker", "exec", instanceName, "mariadb", "-u", "root", "-p"+svc.RootPassword, "-N", "-e", statusSQL)
	output, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get connection status: %w", err)
	}

	lines = strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			val, err := strconv.Atoi(parts[1])
			if err != nil {
				continue
			}

			switch parts[0] {
			case "Threads_connected":
				stats.ThreadsConnected = val
			case "Threads_running":
				stats.ThreadsRunning = val
			case "Threads_cached":
				stats.ThreadsCached = val
			case "Threads_created":
				stats.ThreadsCreated = val
			}
		}
	}

	// Calculate derived metrics
	if stats.MaxConnections > 0 {
		stats.ConnectionUsage = float64(stats.ThreadsConnected) / float64(stats.MaxConnections) * 100
	}

	if stats.ThreadsCached > 0 && stats.ThreadsCreated > 0 {
		stats.ConnectionChurn = float64(stats.ThreadsCreated) / float64(stats.ThreadsCached)
	}

	return stats, nil
}

// ApplyPoolingConfig applies connection pooling configuration to an existing MariaDB instance
func ApplyPoolingConfig(ctx context.Context, instanceName, configContent string, restart bool) error {
	svc, err := state.LoadService("mariadb", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	if svc.RootPassword == "" {
		return fmt.Errorf("root password not found in service state")
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	// Create temporary config file
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("mitte-pooling-%s-*.cnf", instanceName))
	if err != nil {
		return fmt.Errorf("failed to create temporary config file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(configContent); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	tmpFile.Close()

	// Check if container exists
	_, err = cli.ContainerInspect(ctx, instanceName)
	if err != nil {
		return fmt.Errorf("container not found: %w", err)
	}

	// Stop container if restart is requested
	if restart {
		timeout := 30
		if err := cli.ContainerStop(ctx, instanceName, container.StopOptions{Timeout: &timeout}); err != nil {
			return fmt.Errorf("failed to stop container: %w", err)
		}
	}

	// Copy config file to container
	configFile := "/etc/mysql/conf.d/pooling.cnf"
	cmd := exec.Command("docker", "cp", tmpFile.Name(), fmt.Sprintf("%s:%s", instanceName, configFile))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to copy config to container: %w\nOutput: %s", err, output)
	}

	// Start container if it was stopped
	if restart {
		if err := cli.ContainerStart(ctx, instanceName, container.StartOptions{}); err != nil {
			return fmt.Errorf("failed to start container: %w", err)
		}

		// Wait for health check
		for i := 0; i < 60; i++ {
			inspect, err := cli.ContainerInspect(ctx, instanceName)
			if err != nil {
				break
			}
			if inspect.State.Running && inspect.State.Health != nil && inspect.State.Health.Status == "healthy" {
				break
			}
			time.Sleep(1 * time.Second)
		}
	} else {
		// Send SIGHUP to MariaDB to reload configuration without restart
		cmd := exec.Command("docker", "exec", instanceName, "kill", "-HUP", "1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to reload MariaDB configuration: %w\nOutput: %s", err, output)
		}
	}

	// Update service state with new configuration
	// Parse config content to extract values
	lines := strings.Split(configContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "max_connections = ") {
			if val, err := strconv.Atoi(strings.TrimPrefix(line, "max_connections = ")); err == nil {
				svc.MaxConnections = val
			}
		} else if strings.HasPrefix(line, "thread_cache_size = ") {
			if val, err := strconv.Atoi(strings.TrimPrefix(line, "thread_cache_size = ")); err == nil {
				svc.ThreadCacheSize = val
			}
		} else if strings.HasPrefix(line, "table_open_cache = ") {
			if val, err := strconv.Atoi(strings.TrimPrefix(line, "table_open_cache = ")); err == nil {
				svc.TableOpenCache = val
			}
		} else if strings.HasPrefix(line, "innodb_buffer_pool_size = ") {
			svc.InnoDBBufferPoolSize = strings.TrimPrefix(line, "innodb_buffer_pool_size = ")
		} else if strings.HasPrefix(line, "query_cache_size = ") {
			svc.QueryCacheSize = strings.TrimPrefix(line, "query_cache_size = ")
		}
	}

	if err := svc.Save(); err != nil {
		return fmt.Errorf("failed to update service state: %w", err)
	}

	return nil
}
