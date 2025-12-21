package builder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BuildpackConfig holds configuration for buildpack builds
type BuildpackConfig struct {
	BuildpackID  string
	BuildpackURI string
	EnvVars      map[string]string
}

// DetectBuildpack attempts to detect which buildpack should be used for the given app directory
func DetectBuildpack(appDir string) (*BuildpackConfig, error) {
	// Check for common buildpack indicators

	// Node.js detection
	if hasFile(appDir, "package.json") {
		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/nodejs",
			BuildpackURI: "docker://paketobuildpacks/nodejs:latest",
		}, nil
	}

	// Python detection
	if hasFile(appDir, "requirements.txt") || hasFile(appDir, "Pipfile") || hasFile(appDir, "pyproject.toml") {
		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/python",
			BuildpackURI: "docker://paketobuildpacks/python:latest",
		}, nil
	}

	// Go detection
	if hasFile(appDir, "go.mod") || hasFile(appDir, "go.sum") || hasFile(appDir, "main.go") {
		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/go",
			BuildpackURI: "docker://paketobuildpacks/go:latest",
		}, nil
	}

	// Java detection
	if hasFile(appDir, "pom.xml") || hasFile(appDir, "build.gradle") || hasFile(appDir, "build.gradle.kts") {
		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/java",
			BuildpackURI: "docker://paketobuildpacks/java:latest",
		}, nil
	}

	// .NET detection
	if hasFile(appDir, "*.csproj") || hasFile(appDir, "*.fsproj") || hasFile(appDir, "project.json") {
		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/dotnet-core",
			BuildpackURI: "docker://paketobuildpacks/dotnet-core:latest",
		}, nil
	}

	// Ruby detection
	if hasFile(appDir, "Gemfile") {
		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/ruby",
			BuildpackURI: "docker://paketobuildpacks/ruby:latest",
		}, nil
	}

	// PHP detection
	if hasFile(appDir, "composer.json") {
		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/php",
			BuildpackURI: "docker://paketobuildpacks/php:latest",
		}, nil
	}

	return nil, fmt.Errorf("no suitable buildpack detected for the application")
}

// hasFile checks if a file exists in the given directory
// Supports glob patterns for files like *.csproj
func hasFile(dir, pattern string) bool {
	if strings.Contains(pattern, "*") {
		// Handle glob patterns
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		return err == nil && len(matches) > 0
	}

	// Check for exact file match
	_, err := os.Stat(filepath.Join(dir, pattern))
	return !os.IsNotExist(err)
}

// updatePackCLI attempts to update pack CLI to v0.40.0 for better Docker 29.x compatibility
func updatePackCLI() error {
	fmt.Fprintln(os.Stderr, "-----> Downloading pack CLI v0.40.0...")

	// Download v0.40.0
	url := "https://github.com/buildpacks/pack/releases/download/v0.40.0/pack-v0.40.0-linux.tgz"
	tmpFile := "/tmp/pack-v0.40.0.tgz"

	// Download
	cmd := exec.Command("curl", "-sSL", "-L", url, "-o", tmpFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to download pack CLI: %v, output: %s", err, output)
	}

	// Extract
	tmpDir := "/tmp/pack-update"
	os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cmd = exec.Command("tar", "-xzf", tmpFile, "-C", tmpDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to extract pack CLI: %v, output: %s", err, output)
	}

	// Find the binary
	var packBinary string
	err := filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "pack" {
			packBinary = path
			return filepath.SkipAll
		}
		return nil
	})

	if err != nil || packBinary == "" {
		return fmt.Errorf("could not find pack binary in archive")
	}

	// Install
	cmd = exec.Command("cp", packBinary, "/usr/local/bin/pack")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to copy pack binary: %v, output: %s", err, output)
	}

	cmd = exec.Command("chmod", "+x", "/usr/local/bin/pack")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set executable permissions: %v, output: %s", err, output)
	}

	// Clean up
	os.Remove(tmpFile)

	return nil
}

// createDockerfileForBuildpack creates a simple Dockerfile based on the detected buildpack
func createDockerfileForBuildpack(buildDir, buildpackID string, envVars map[string]string) error {
	dockerfilePath := filepath.Join(buildDir, "Dockerfile")

	var dockerfileContent string

	// Create Dockerfile based on buildpack type
	switch {
	case strings.Contains(buildpackID, "java"):
		// Java application
		dockerfileContent = `FROM eclipse-temurin:17-jre-jammy
WORKDIR /app
COPY . .
# Try to find and run the JAR file
RUN find . -name "*.jar" -type f | head -1 | xargs -I {} cp {} app.jar
EXPOSE 8080
CMD ["java", "-jar", "app.jar"]`

	case strings.Contains(buildpackID, "nodejs"):
		// Node.js application
		dockerfileContent = `FROM node:18-alpine
WORKDIR /app
COPY package*.json ./
RUN npm ci --only=production
COPY . .
EXPOSE 3000
CMD ["npm", "start"]`

	case strings.Contains(buildpackID, "python"):
		// Python application
		dockerfileContent = `FROM python:3.11-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
EXPOSE 8000
CMD ["python", "app.py"]`

	case strings.Contains(buildpackID, "go"):
		// Go application
		dockerfileContent = `FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o main .

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/main .
EXPOSE 8080
CMD ["./main"]`

	default:
		return fmt.Errorf("cannot create Dockerfile for unknown buildpack: %s", buildpackID)
	}

	// Add environment variables to Dockerfile
	if len(envVars) > 0 {
		lines := strings.Split(dockerfileContent, "\n")
		var newLines []string
		for _, line := range lines {
			newLines = append(newLines, line)
			if strings.Contains(line, "FROM ") {
				// Add ENV directives after FROM
				for key, value := range envVars {
					newLines = append(newLines, fmt.Sprintf("ENV %s=%s", key, value))
				}
			}
		}
		dockerfileContent = strings.Join(newLines, "\n")
	}

	// Write the Dockerfile
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	fmt.Fprintf(os.Stderr, "-----> Created Dockerfile for %s\n", buildpackID)
	return nil
}

// BuildWithBuildpack builds an application using Cloud Native Buildpacks
func BuildWithBuildpack(ctx context.Context, appName, buildDir, repoPath, branchName string, buildpackConfig *BuildpackConfig, envVars map[string]string) (string, error) {
	fmt.Fprintln(os.Stderr, "-----> Building with Cloud Native Buildpacks...")

	// Get Git commit hash for tagging
	commitHash, err := getGitCommitHash(repoPath, branchName)
	if err != nil {
		return "", fmt.Errorf("could not get git commit hash: %w", err)
	}

	shortHash := commitHash
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	imageTag := fmt.Sprintf("%s:%s", appName, shortHash)

	fmt.Fprintf(os.Stderr, "-----> Creating image tag: %s\n", imageTag)
	fmt.Fprintf(os.Stderr, "-----> Using buildpack: %s\n", buildpackConfig.BuildpackID)
	fmt.Fprintf(os.Stderr, "-----> Buildpack URI: %s\n", buildpackConfig.BuildpackURI)

	// Create temporary directories for buildpack lifecycle
	layersDir, err := os.MkdirTemp("", "mitte-buildpack-layers-")
	if err != nil {
		return "", fmt.Errorf("failed to create layers directory: %w", err)
	}
	defer os.RemoveAll(layersDir)

	platformDir, err := os.MkdirTemp("", "mitte-buildpack-platform-")
	if err != nil {
		return "", fmt.Errorf("failed to create platform directory: %w", err)
	}
	defer os.RemoveAll(platformDir)

	// Create platform environment files
	envDir := filepath.Join(platformDir, "env")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create env directory: %w", err)
	}

	// Write environment variables to platform/env
	for key, value := range envVars {
		envFile := filepath.Join(envDir, key)
		if err := os.WriteFile(envFile, []byte(value), 0644); err != nil {
			return "", fmt.Errorf("failed to write env var %s: %w", key, err)
		}
	}

	// Check if pack CLI is available
	if _, err := exec.LookPath("pack"); err != nil {
		return "", fmt.Errorf("pack CLI not found. Buildpack support requires pack CLI.\n" +
			"Please run 'sudo mitte setup' to install all dependencies, or install pack CLI manually:\n" +
			"  https://buildpacks.io/docs/tools/pack/")
	}

	// Check pack version for compatibility warning
	if output, err := exec.Command("pack", "--version").Output(); err == nil {
		version := strings.TrimSpace(string(output))
		fmt.Fprintf(os.Stderr, "-----> Using pack CLI version: %s\n", version)
		// Check if version is too old for Docker 29.x
		if strings.HasPrefix(version, "v0.38.") || strings.HasPrefix(version, "v0.37.") || strings.HasPrefix(version, "v0.36.") {
			fmt.Fprintln(os.Stderr, "⚠️  Warning: pack CLI version may not be compatible with Docker 29.x")
			fmt.Fprintln(os.Stderr, "   Consider running 'sudo mitte setup' to update to v0.39.0+")
		}
		// Also warn if using v0.39.x with Docker 29.x (known issues)
		if strings.HasPrefix(version, "v0.39.") {
			fmt.Fprintln(os.Stderr, "⚠️  Note: pack v0.39.x may have issues with Docker 29.x")
			fmt.Fprintln(os.Stderr, "   Trying API version fallback...")
		}
		// Try to update pack CLI if it's v0.39.x (which has Docker 29.x issues)
		// Check for any v0.39.x version including custom builds
		if strings.Contains(version, "0.39.") {
			fmt.Fprintln(os.Stderr, "-----> Attempting to update pack CLI to v0.40.0 for better Docker 29.x compatibility...")
			if err := updatePackCLI(); err != nil {
				fmt.Fprintf(os.Stderr, "-----> Could not update pack CLI: %v\n", err)
				fmt.Fprintln(os.Stderr, "-----> Continuing with fallback strategies...")
			} else {
				fmt.Fprintln(os.Stderr, "-----> Pack CLI updated successfully, retrying...")
				// Re-check version after update
				if newOutput, err := exec.Command("pack", "--version").Output(); err == nil {
					newVersion := strings.TrimSpace(string(newOutput))
					fmt.Fprintf(os.Stderr, "-----> Now using pack CLI version: %s\n", newVersion)
				}
			}
		}
	}

	// Use pack CLI to build the application
	// This is a simpler approach than directly using the lifecycle library
	fmt.Fprintln(os.Stderr, "-----> Running pack build...")

	cmdArgs := []string{
		"build",
		imageTag,
		"--path", buildDir,
		"--builder", "paketobuildpacks/builder:base",
		"--trust-builder",
		"--verbose",
	}

	// If a specific buildpack is configured, use it
	if buildpackConfig.BuildpackID != "" {
		cmdArgs = append(cmdArgs, "--buildpack", buildpackConfig.BuildpackID)
	}

	// Try building with pack CLI
	// For Docker 29.x compatibility, try newer API versions first
	// Docker 29.x requires API 1.44+, but pack v0.39.1 defaults to 1.42
	attempts := []struct {
		envVars []string
		desc    string
	}{
		// Try 1-6: Newer API versions first (Docker 29.x compatibility)
		{envVars: []string{"DOCKER_API_VERSION=1.50"}, desc: "Docker API 1.50"},
		{envVars: []string{"DOCKER_API_VERSION=1.49"}, desc: "Docker API 1.49"},
		{envVars: []string{"DOCKER_API_VERSION=1.48"}, desc: "Docker API 1.48"},
		{envVars: []string{"DOCKER_API_VERSION=1.47"}, desc: "Docker API 1.47"},
		{envVars: []string{"DOCKER_API_VERSION=1.46"}, desc: "Docker API 1.46"},
		{envVars: []string{"DOCKER_API_VERSION=1.45"}, desc: "Docker API 1.45"},
		{envVars: []string{"DOCKER_API_VERSION=1.44"}, desc: "Docker API 1.44"},
		// Try 8: Let pack use API version negotiation (v0.39.0+ feature) as last resort
		{envVars: []string{}, desc: "API version negotiation"},
	}

	var lastErr error

	for _, attempt := range attempts {
		cmd := exec.CommandContext(ctx, "pack", cmdArgs...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr

		// Set environment variables for this attempt
		env := os.Environ()
		env = append(env, attempt.envVars...)
		cmd.Env = env

		fmt.Fprintf(os.Stderr, "-----> Building with %s...\n", attempt.desc)

		if err := cmd.Run(); err == nil {
			// Success!
			fmt.Fprintf(os.Stderr, "\n-----> Successfully built image %s with buildpacks\n", imageTag)
			return imageTag, nil
		} else {
			lastErr = err
			// Check if error is due to API version mismatch
			if exitErr, ok := err.(*exec.ExitError); ok {
				errOutput := string(exitErr.Stderr)
				fmt.Fprintf(os.Stderr, "-----> Pack build attempt failed: %s\n", errOutput)
				if strings.Contains(errOutput, "client version") && strings.Contains(errOutput, "is too old") {
					// Try next API version
					fmt.Fprintf(os.Stderr, "-----> API version too old, trying next...\n")
					continue
				}
			}
			// Other error, return it
			return "", fmt.Errorf("pack build failed: %w", err)
		}
	}

	// If we get here, all attempts failed
	fmt.Fprintln(os.Stderr, "-----> All pack build attempts failed, trying fallback strategies...")

	// Try to build with Docker directly as a fallback
	// This requires a Dockerfile, so check if one exists or create one
	dockerfilePath := filepath.Join(buildDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); err == nil {
		fmt.Fprintln(os.Stderr, "-----> Found Dockerfile, trying Docker builder...")
		// Use the Docker builder as fallback
		dockerImageTag, err := BuildImage(ctx, appName, buildDir, repoPath, branchName, envVars)
		if err != nil {
			return "", fmt.Errorf("both pack build and Docker builder failed. Last pack error: %w", lastErr)
		}
		return dockerImageTag, nil
	}

	// No Dockerfile found, try to create one based on detected buildpack
	fmt.Fprintln(os.Stderr, "-----> No Dockerfile found, attempting to create one...")
	if buildpackConfig != nil {
		if err := createDockerfileForBuildpack(buildDir, buildpackConfig.BuildpackID, envVars); err != nil {
			fmt.Fprintf(os.Stderr, "-----> Could not create Dockerfile: %v\n", err)
			return "", fmt.Errorf("pack build failed and could not create Dockerfile fallback: %w", lastErr)
		}

		// Try Docker builder with the created Dockerfile
		fmt.Fprintln(os.Stderr, "-----> Created Dockerfile, trying Docker builder...")
		dockerImageTag, err := BuildImage(ctx, appName, buildDir, repoPath, branchName, envVars)
		if err != nil {
			return "", fmt.Errorf("pack build failed and Docker builder with created Dockerfile also failed: %w", lastErr)
		}
		return dockerImageTag, nil
	}

	return "", fmt.Errorf("pack build failed after trying all Docker API compatibility options: %w", lastErr)
}
