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

func CreatePostgres(ctx context.Context, instanceName, rootPassword, user, database, version string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	theImage := fmt.Sprintf("postgres:%s", version)

	fmt.Printf("Pulling image %s...\n", theImage)
	out, err := cli.ImagePull(ctx, theImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer out.Close()
	io.Copy(os.Stdout, out)

	env := []string{
		fmt.Sprintf("POSTGRES_PASSWORD=%s", rootPassword),
	}
	if user != "" {
		env = append(env, fmt.Sprintf("POSTGRES_USER=%s", user))
	} else {
		user = "postgres" // Default for healthcheck
	}
	if database != "" {
		env = append(env, fmt.Sprintf("POSTGRES_DB=%s", database))
	}

	config := &container.Config{
		Image: theImage,
		Env:   env,
		Healthcheck: &container.HealthConfig{
			Test:        []string{"CMD-SHELL", fmt.Sprintf("pg_isready -U %s", user)},
			Interval:    10 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     5,
			StartPeriod: 30 * time.Second,
		},
	}

	hostConfig := &container.HostConfig{
		Binds:         []string{fmt.Sprintf("mitte-postgres-data-%s:/var/lib/postgresql/data", instanceName)},
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
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

func DestroyPostgres(ctx context.Context, instanceName string) error {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	if err := cli.ContainerRemove(ctx, instanceName, container.RemoveOptions{Force: true}); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove container: %w", err)
		}
	}

	volumeName := fmt.Sprintf("mitte-postgres-data-%s", instanceName)
	if err := cli.VolumeRemove(ctx, volumeName, true); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove volume '%s': %w", volumeName, err)
		}
	}

	return nil
}

func BackupPostgres(ctx context.Context, instanceName, outputPath string) error {
	svc, err := state.LoadService("postgres", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	user := svc.Username
	if user == "" {
		user = "postgres"
	}

	// Run pg_dumpall inside the container
	cmd := exec.Command("docker", "exec", "-t", instanceName, "pg_dumpall", "-U", user)
	// We need the password. pg_dumpall usually looks at PGPASSWORD env var
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", svc.RootPassword))

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("failed to run pg_dumpall: %w, stderr: %s", err, string(exitErr.Stderr))
		}
		return fmt.Errorf("failed to run pg_dumpall: %w", err)
	}

	err = os.WriteFile(outputPath, output, 0644)
	if err != nil {
		return fmt.Errorf("failed to write backup file: %w", err)
	}

	return nil
}

func RestorePostgres(ctx context.Context, instanceName, backupPath string) error {
	svc, err := state.LoadService("postgres", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	user := svc.Username
	if user == "" {
		user = "postgres"
	}

	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	cmd := exec.Command("docker", "exec", "-i", instanceName, "psql", "-U", user)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", svc.RootPassword))
	cmd.Stdin = bytes.NewReader(backupData)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run psql restore: %w, output: %s", err, string(output))
	}

	return nil
}

func CreatePostgresUser(ctx context.Context, instanceName, username, password, database string) error {
	svc, err := state.LoadService("postgres", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	adminUser := svc.Username
	if adminUser == "" {
		adminUser = "postgres"
	}

	sql := fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s';", username, password)
	if database != "" && database != "*" {
		sql += fmt.Sprintf(" GRANT ALL PRIVILEGES ON DATABASE %s TO %s;", database, username)
	}

	cmd := exec.Command("docker", "exec", instanceName, "psql", "-U", adminUser, "-c", sql)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", svc.RootPassword))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create user: %w, output: %s", err, string(output))
	}

	return nil
}

func DeletePostgresUser(ctx context.Context, instanceName, username string) error {
	svc, err := state.LoadService("postgres", instanceName)
	if err != nil {
		return fmt.Errorf("failed to load service state: %w", err)
	}

	adminUser := svc.Username
	if adminUser == "" {
		adminUser = "postgres"
	}

	sql := fmt.Sprintf("DROP USER %s;", username)
	cmd := exec.Command("docker", "exec", instanceName, "psql", "-U", adminUser, "-c", sql)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", svc.RootPassword))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete user: %w, output: %s", err, string(output))
	}

	return nil
}

func ListPostgresUsers(ctx context.Context, instanceName string) ([]string, error) {
	svc, err := state.LoadService("postgres", instanceName)
	if err != nil {
		return nil, fmt.Errorf("failed to load service state: %w", err)
	}

	adminUser := svc.Username
	if adminUser == "" {
		adminUser = "postgres"
	}

	sql := "SELECT usename FROM pg_catalog.pg_user WHERE usename != 'postgres';"
	cmd := exec.Command("docker", "exec", instanceName, "psql", "-U", adminUser, "-t", "-A", "-c", sql)
	cmd.Env = append(os.Environ(), fmt.Sprintf("PGPASSWORD=%s", svc.RootPassword))
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	users := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(users) == 1 && users[0] == "" {
		return []string{}, nil
	}

	return users, nil
}
