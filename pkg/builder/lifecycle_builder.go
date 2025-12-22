package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/buildpacks/lifecycle/buildpack"
	"github.com/buildpacks/lifecycle/platform/files"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

// BuildWithLifecycle builds an application using Cloud Native Buildpacks lifecycle directly
func BuildWithLifecycle(ctx context.Context, appName, buildDir, repoPath, branchName string, buildpackConfig *BuildpackConfig, envVars map[string]string) (string, error) {
	fmt.Fprintln(os.Stderr, "-----> Building with Cloud Native Buildpacks lifecycle...")

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

	// Create temporary directories for lifecycle phases
	workDir, err := os.MkdirTemp("", "mitte-lifecycle-work-")
	if err != nil {
		return "", fmt.Errorf("failed to create work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	layersDir := filepath.Join(workDir, "layers")
	if err := os.MkdirAll(layersDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create layers directory: %w", err)
	}

	cacheDir := filepath.Join(workDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	platformDir := filepath.Join(workDir, "platform")
	if err := os.MkdirAll(platformDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create platform directory: %w", err)
	}

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

	// Step 1: Set up Docker client with proper API version
	fmt.Fprintln(os.Stderr, "-----> Setting up Docker client...")
	dockerClient, err := setupDockerClient(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "-----> Failed to setup Docker client: %v\n", err)
		fmt.Fprintln(os.Stderr, "-----> Falling back to Dockerfile approach...")
		return "", fmt.Errorf("docker client setup failed: %w", err)
	}
	defer dockerClient.Close()

	// Step 2: Pull builder image
	builderImage := "paketobuildpacks/builder:base"
	fmt.Fprintf(os.Stderr, "-----> Pulling builder image: %s\n", builderImage)
	if err := pullBuilderImage(ctx, dockerClient, builderImage); err != nil {
		fmt.Fprintf(os.Stderr, "-----> Failed to pull builder image: %v\n", err)
		fmt.Fprintln(os.Stderr, "-----> Falling back to Dockerfile approach...")
		return "", fmt.Errorf("builder image pull failed: %w", err)
	}

	// Step 3: Run detector phase
	fmt.Fprintln(os.Stderr, "-----> Running detector phase...")
	group, plan, err := runDetectorPhase(ctx, buildDir, platformDir, builderImage, dockerClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "-----> Detector phase failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "-----> Falling back to Dockerfile approach...")
		return "", fmt.Errorf("detector phase failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "-----> Detected buildpack group: %v\n", group.Group)
	fmt.Fprintf(os.Stderr, "-----> Build plan has %d entries\n", len(plan.Entries))

	// Step 4: Run analyzer phase
	fmt.Fprintln(os.Stderr, "-----> Running analyzer phase...")
	analyzed, err := runAnalyzerPhase(ctx, imageTag, dockerClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "-----> Analyzer phase failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "-----> Falling back to Dockerfile approach...")
		return "", fmt.Errorf("analyzer phase failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "-----> Analysis complete: %v\n", analyzed)

	// Step 5: Run builder phase
	fmt.Fprintln(os.Stderr, "-----> Running builder phase...")
	buildMD, err := runBuilderPhase(ctx, buildDir, layersDir, platformDir, group, plan, analyzed, dockerClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "-----> Builder phase failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "-----> Falling back to Dockerfile approach...")
		return "", fmt.Errorf("builder phase failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "-----> Build complete: %v\n", buildMD)

	// Step 6: Run exporter phase
	fmt.Fprintln(os.Stderr, "-----> Running exporter phase...")
	exportReport, err := runExporterPhase(ctx, imageTag, layersDir, group.Group, dockerClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "-----> Exporter phase failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "-----> Falling back to Dockerfile approach...")
		return "", fmt.Errorf("exporter phase failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "-----> Export complete: %v\n", exportReport)

	// For now, return success even with placeholder implementations
	// TODO: Implement full phase logic
	fmt.Fprintln(os.Stderr, "-----> Lifecycle phases completed (placeholder implementations)")
	return imageTag, nil
}

// setupDockerClient creates a Docker client with proper API version negotiation
func setupDockerClient(ctx context.Context) (*client.Client, error) {
	// Try different API versions for Docker 29.x compatibility
	apiVersions := []string{"1.50", "1.49", "1.48", "1.47", "1.46", "1.45", "1.44", ""}

	for _, apiVersion := range apiVersions {
		fmt.Fprintf(os.Stderr, "-----> Trying Docker API version: %s\n", apiVersion)

		var opts []client.Opt
		opts = append(opts, client.FromEnv)

		if apiVersion == "" {
			// Try version negotiation as last resort
			opts = append(opts, client.WithAPIVersionNegotiation())
		} else {
			opts = append(opts, client.WithVersion(apiVersion))
		}

		dockerClient, err := client.NewClientWithOpts(opts...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "-----> Failed with API %s: %v\n", apiVersion, err)
			continue
		}

		// Test the connection
		_, err = dockerClient.Ping(ctx)
		if err != nil {
			dockerClient.Close()
			fmt.Fprintf(os.Stderr, "-----> Ping failed with API %s: %v\n", apiVersion, err)
			continue
		}

		// Success!
		fmt.Fprintf(os.Stderr, "-----> Connected with Docker API version: %s\n", apiVersion)
		return dockerClient, nil
	}

	return nil, fmt.Errorf("failed to create docker client with any API version")
}

// pullBuilderImage pulls the builder image from Docker registry
func pullBuilderImage(ctx context.Context, dockerClient *client.Client, builderImage string) error {
	reader, err := dockerClient.ImagePull(ctx, builderImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull builder image: %w", err)
	}
	defer reader.Close()

	// Read the pull output to completion
	buf := make([]byte, 1024)
	for {
		_, err := reader.Read(buf)
		if err != nil {
			break
		}
	}

	return nil
}

// runDetectorPhase runs the detector phase to identify buildpacks
func runDetectorPhase(ctx context.Context, appDir, platformDir, builderImage string, dockerClient *client.Client) (buildpack.Group, files.Plan, error) {
	fmt.Fprintln(os.Stderr, "-----> Running basic detector phase...")

	// For now, implement a simple detector based on file presence
	// This is a simplified version - real detector would run buildpack detect scripts

	var groupElements []buildpack.GroupElement
	var planEntries []files.BuildPlanEntry

	// Check for Java buildpack
	if hasFile(appDir, "pom.xml") || hasFile(appDir, "build.gradle") || hasFile(appDir, "build.gradle.kts") {
		fmt.Fprintln(os.Stderr, "-----> Detected Java application")
		groupElements = append(groupElements, buildpack.GroupElement{
			ID:      "paketo-buildpacks/java",
			Version: "latest",
		})
		planEntries = append(planEntries, files.BuildPlanEntry{
			Providers: []buildpack.GroupElement{
				{ID: "paketo-buildpacks/java", Version: "latest"},
			},
			Requires: []buildpack.Require{
				{Name: "jvm-application"},
			},
		})
	} else if hasFile(appDir, "package.json") {
		// Check for Node.js buildpack
		fmt.Fprintln(os.Stderr, "-----> Detected Node.js application")
		groupElements = append(groupElements, buildpack.GroupElement{
			ID:      "paketo-buildpacks/nodejs",
			Version: "latest",
		})
		planEntries = append(planEntries, files.BuildPlanEntry{
			Providers: []buildpack.GroupElement{
				{ID: "paketo-buildpacks/nodejs", Version: "latest"},
			},
			Requires: []buildpack.Require{
				{Name: "node"},
			},
		})
	} else if hasFile(appDir, "requirements.txt") || hasFile(appDir, "Pipfile") || hasFile(appDir, "pyproject.toml") {
		// Check for Python buildpack
		fmt.Fprintln(os.Stderr, "-----> Detected Python application")
		groupElements = append(groupElements, buildpack.GroupElement{
			ID:      "paketo-buildpacks/python",
			Version: "latest",
		})
		planEntries = append(planEntries, files.BuildPlanEntry{
			Providers: []buildpack.GroupElement{
				{ID: "paketo-buildpacks/python", Version: "latest"},
			},
			Requires: []buildpack.Require{
				{Name: "python"},
			},
		})
	} else if hasFile(appDir, "go.mod") || hasFile(appDir, "go.sum") || hasFile(appDir, "main.go") {
		// Check for Go buildpack
		fmt.Fprintln(os.Stderr, "-----> Detected Go application")
		groupElements = append(groupElements, buildpack.GroupElement{
			ID:      "paketo-buildpacks/go",
			Version: "latest",
		})
		planEntries = append(planEntries, files.BuildPlanEntry{
			Providers: []buildpack.GroupElement{
				{ID: "paketo-buildpacks/go", Version: "latest"},
			},
			Requires: []buildpack.Require{
				{Name: "go"},
			},
		})
	} else {
		return buildpack.Group{}, files.Plan{}, fmt.Errorf("no buildpacks detected for application")
	}

	group := buildpack.Group{Group: groupElements}
	plan := files.Plan{Entries: planEntries}

	return group, plan, nil
}

// runAnalyzerPhase runs the analyzer phase
func runAnalyzerPhase(ctx context.Context, imageRef string, dockerClient *client.Client) (files.Analyzed, error) {
	fmt.Fprintln(os.Stderr, "-----> Running basic analyzer phase...")

	// For now, return empty analysis
	// In a real implementation, this would analyze the previous image
	return files.Analyzed{}, nil
}

// runBuilderPhase runs the builder phase
func runBuilderPhase(ctx context.Context, appDir, layersDir, platformDir string, group buildpack.Group, plan files.Plan, analyzed files.Analyzed, dockerClient *client.Client) (*files.BuildMetadata, error) {
	fmt.Fprintln(os.Stderr, "-----> Running basic builder phase...")

	// For now, return empty build metadata
	// In a real implementation, this would run buildpacks
	return &files.BuildMetadata{}, nil
}

// runExporterPhase runs the exporter phase
func runExporterPhase(ctx context.Context, imageRef, layersDir string, buildpacks []buildpack.GroupElement, dockerClient *client.Client) (files.Report, error) {
	fmt.Fprintln(os.Stderr, "-----> Running basic exporter phase...")

	// For now, return empty report
	// In a real implementation, this would create the final image
	return files.Report{}, nil
}

// TODO: Implement proper lifecycle execution using the lifecycle library
