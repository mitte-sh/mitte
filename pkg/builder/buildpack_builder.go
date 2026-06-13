package builder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mitte-sh/mitte/pkg/logger"
)

// BuildpackConfig holds configuration for buildpack builds
type BuildpackConfig struct {
	BuildpackID  string
	BuildpackURI string
	EnvVars      map[string]string
	JavaVersion  string // Java version detected from pom.xml (e.g., "17", "21")
	NodePort     string // Node.js port detected from package.json (e.g., "3000")
	RubyVersion  string // Ruby version detected from Gemfile (e.g., "2.3.0", "3.2.0")
}

// DetectBuildpack attempts to detect which buildpack should be used for the given app directory
func DetectBuildpack(appDir string) (*BuildpackConfig, error) {
	// Check for common buildpack indicators

	// Ruby detection (check first for Rails apps that may also have package.json)
	if hasFile(appDir, "Gemfile") {
		rubyVersion := "3.2" // default
		gemfilePath := filepath.Join(appDir, "Gemfile")
		if detectedVersion := DetectRubyVersion(gemfilePath); detectedVersion != "" {
			rubyVersion = detectedVersion
		}

		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/ruby",
			BuildpackURI: "docker://paketobuildpacks/ruby:latest",
			RubyVersion:  rubyVersion,
		}, nil
	}

	// Node.js detection
	if hasFile(appDir, "package.json") {
		nodePort := "3000" // default
		packageJsonPath := filepath.Join(appDir, "package.json")
		if detectedPort := DetectNodeJSPort(packageJsonPath); detectedPort != "" {
			nodePort = detectedPort
		}

		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/nodejs",
			BuildpackURI: "docker://paketobuildpacks/nodejs:latest",
			NodePort:     nodePort,
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
		javaVersion := "17" // default
		if hasFile(appDir, "pom.xml") {
			// Detect Java version from pom.xml
			pomPath := filepath.Join(appDir, "pom.xml")
			detectedVersion := DetectJavaVersionFromPom(pomPath)
			if detectedVersion != "" {
				javaVersion = detectedVersion
			}
		}

		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/java",
			BuildpackURI: "docker://paketobuildpacks/java:latest",
			JavaVersion:  javaVersion,
		}, nil
	}

	// .NET detection
	if hasFile(appDir, "*.csproj") || hasFile(appDir, "*.fsproj") || hasFile(appDir, "project.json") {
		return &BuildpackConfig{
			BuildpackID:  "paketo-buildpacks/dotnet-core",
			BuildpackURI: "docker://paketobuildpacks/dotnet-core:latest",
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

// DetectJavaVersionFromPom extracts the Java version from pom.xml
func DetectJavaVersionFromPom(pomPath string) string {
	// Default to Java 17 if detection fails
	defaultVersion := "17"

	// Read the pom.xml file
	content, err := os.ReadFile(pomPath)
	if err != nil {
		return defaultVersion
	}

	// Use regex to find <java.version> tag
	re := regexp.MustCompile(`<java\.version>([^<]+)</java\.version>`)
	matches := re.FindStringSubmatch(string(content))
	if len(matches) > 1 {
		version := strings.TrimSpace(matches[1])
		// Validate that it's a reasonable version number
		if version != "" && len(version) <= 3 {
			return version
		}
	}

	return defaultVersion
}

// DetectNodeJSPort extracts the port from package.json or returns default
func DetectNodeJSPort(packageJsonPath string) string {
	// Default Node.js port
	defaultPort := "3000"

	// Read the package.json file
	content, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return defaultPort
	}

	// Simple port detection - look for PORT= in scripts or environment
	contentStr := string(content)

	// Check for PORT environment variable in scripts
	portRegex := regexp.MustCompile(`PORT=(\d+)`)
	matches := portRegex.FindStringSubmatch(contentStr)
	if len(matches) > 1 {
		return matches[1]
	}

	// Could add more sophisticated detection here
	// For now, return default
	return defaultPort
}

// DetectRubyVersion extracts the Ruby version from Gemfile
func DetectRubyVersion(gemfilePath string) string {
	// Default to latest Ruby version if detection fails
	defaultVersion := "3.2"

	// Read the Gemfile
	content, err := os.ReadFile(gemfilePath)
	if err != nil {
		return defaultVersion
	}

	// Use regex to find ruby directive
	// Matches: ruby '2.3.0', ruby "2.3.0", ruby('2.3.0'), etc.
	re := regexp.MustCompile(`ruby\s*[\('"]\s*([^'"\)\s]+)`)
	matches := re.FindStringSubmatch(string(content))
	if len(matches) > 1 {
		version := strings.TrimSpace(matches[1])
		// Validate that it's a reasonable version number (X.Y.Z format)
		if version != "" && len(version) >= 3 && strings.Count(version, ".") >= 1 {
			// Ruby versions before 2.7 don't have reliable slim images
			// Upgrade old versions to minimum supported LTS
			if strings.HasPrefix(version, "2.3.") || strings.HasPrefix(version, "2.4.") ||
				strings.HasPrefix(version, "2.5.") || strings.HasPrefix(version, "2.6.") {
				return "2.7" // LTS version with reliable slim images
			}
			return version
		}
	}

	return defaultVersion
}

// updatePackCLI attempts to update pack CLI to latest version for better Docker 29.x compatibility
func updatePackCLI() error {
	logger.Info("Downloading latest pack CLI...")

	// Download v0.39.0 which has Docker API version negotiation fix
	url := "https://github.com/buildpacks/pack/releases/download/v0.39.0/pack-v0.39.0-linux.tgz"
	tmpFile := "/tmp/pack-v0.39.0.tgz"

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
	defer os.Remove(tmpFile)

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

	// Install to user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Can't update pack CLI without home directory
		logger.Warn("Could not determine home directory, skipping pack CLI update")
		return nil
	}

	mitteBinDir := filepath.Join(homeDir, ".mitte", "bin")
	if err := os.MkdirAll(mitteBinDir, 0755); err != nil {
		return fmt.Errorf("failed to create mitte bin directory: %w", err)
	}

	installPath := filepath.Join(mitteBinDir, "pack")

	// Copy the binary
	data, err := os.ReadFile(packBinary)
	if err != nil {
		return fmt.Errorf("failed to read pack binary: %w", err)
	}

	if err := os.WriteFile(installPath, data, 0755); err != nil {
		return fmt.Errorf("failed to write pack binary: %w", err)
	}

	logger.Info(fmt.Sprintf("Pack CLI updated to %s", installPath))
	logger.Info("Note: You may need to add ~/.mitte/bin to your PATH")

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
		// Java application - detect build tool and build properly
		dockerfileContent = `FROM eclipse-temurin:17-jdk-jammy AS builder
WORKDIR /app
COPY . .

# Detect and run build tool
RUN if [ -f pom.xml ]; then \
      echo "Building with Maven..." && \
      apt-get update && apt-get install -y maven && \
      mvn clean package -DskipTests && \
      find . -name "*.jar" -not -path "*/target/dependency/*" | head -1 | xargs -I {} cp {} app.jar; \
    elif [ -f build.gradle ] || [ -f build.gradle.kts ]; then \
      echo "Building with Gradle..." && \
      apt-get update && apt-get install -y gradle && \
      gradle build -x test && \
      find . -name "*.jar" -not -path "*/build/libs/*-plain.jar" | head -1 | xargs -I {} cp {} app.jar; \
    else \
      echo "No build tool detected, looking for pre-built JAR..." && \
      find . -name "*.jar" -type f | head -1 | xargs -I {} cp {} app.jar || echo "No JAR file found"; \
    fi

FROM eclipse-temurin:17-jre-jammy
WORKDIR /app
COPY --from=builder /app/app.jar .
RUN if [ ! -f app.jar ]; then echo "No JAR file was built or found" && exit 1; fi
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

	logger.Info(fmt.Sprintf("Created Dockerfile for %s", buildpackID))
	return nil
}

// BuildWithBuildpack builds an application using Cloud Native Buildpacks
func BuildWithBuildpack(ctx context.Context, appName, buildDir, repoPath, branchName string, buildpackConfig *BuildpackConfig, envVars map[string]string) (string, error) {
	logger.Info("Building with Cloud Native Buildpacks...")

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

	logger.Info(fmt.Sprintf("Creating image tag: %s", imageTag))
	logger.Info(fmt.Sprintf("Using buildpack: %s", buildpackConfig.BuildpackID))
	logger.Info(fmt.Sprintf("Buildpack URI: %s", buildpackConfig.BuildpackURI))

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

	// Try lifecycle builder first (direct buildpack execution)
	logger.Info("Attempting direct lifecycle build (no pack CLI dependency)...")
	lifecycleImageTag, lifecycleErr := BuildWithLifecycle(ctx, appName, buildDir, repoPath, branchName, buildpackConfig, envVars)
	if lifecycleErr == nil {
		return lifecycleImageTag, nil
	}

	logger.Error(fmt.Sprintf("Lifecycle build failed: %v", lifecycleErr))
	logger.Info("Falling back to Dockerfile creation...")

	// Create Dockerfile based on detected buildpack
	if buildpackConfig != nil {
		if err := createDockerfileForBuildpack(buildDir, buildpackConfig.BuildpackID, envVars); err != nil {
			logger.Error(fmt.Sprintf("Could not create Dockerfile: %v", err))
			return "", fmt.Errorf("failed to create Dockerfile from buildpack: %w", err)
		}

		// Use Docker builder with the created Dockerfile
		logger.Info("Created Dockerfile, using Docker builder...")
		dockerImageTag, err := BuildImage(ctx, appName, buildDir, repoPath, branchName, envVars)
		if err != nil {
			return "", fmt.Errorf("Docker builder with created Dockerfile failed: %w", err)
		}
		return dockerImageTag, nil
	}

	return "", fmt.Errorf("no buildpack configuration available")
}
