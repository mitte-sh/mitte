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
		return "", fmt.Errorf("pack CLI not found. Please install it from https://buildpacks.io/docs/tools/pack/")
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

	cmd := exec.CommandContext(ctx, "pack", cmdArgs...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pack build failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n-----> Successfully built image %s with buildpacks\n", imageTag)
	return imageTag, nil
}
