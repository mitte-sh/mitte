package builder

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

// ErrorLine represents a single line of error detail from the Docker daemon
type ErrorLine struct {
	Error       string      `json:"error"`
	ErrorDetail ErrorDetail `json:"errorDetail"`
}

// ErrorDetail is a sub-field of ErrorLine
type ErrorDetail struct {
	Message string `json:"message"`
}

// StreamLine represents a single line of progress from the Docker daemon
type StreamLine struct {
	Stream string `json:"stream"`
}

// BuildImage uses the Docker SDK to build an image from a given directory.
// It returns the unique image tag and any error that occurred.
func BuildImage(ctx context.Context, appName, buildDir, repoPath string, branchName string) (string, error) {
	fmt.Fprintln(os.Stderr, "-----> Connecting to Docker daemon...")
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("failed to create docker client: %w", err)
	}
	defer cli.Close()

	// --- 1. Get the Git SHA for tagging ---
	commitHash, err := getGitCommitHash(repoPath, branchName)
	if err != nil {
		return "", fmt.Errorf("could not get git commit hash: %w", err)
	}

	// Safely create a short hash for the tag
	shortHash := commitHash
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	imageTag := fmt.Sprintf("%s:%s", appName, shortHash)

	fmt.Fprintf(os.Stderr, "-----> Creating image tag: %s\n", imageTag)

	// --- 2. Create the build context (a tarball of the build directory) ---
	var buf bytes.Buffer
	tarWriter := tar.NewWriter(&buf)

	fmt.Fprintln(os.Stderr, "-----> Creating build context tarball...")
	err = filepath.Walk(buildDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		// Get relative path for tar header
		relPath, err := filepath.Rel(buildDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}

		header, err := tar.FileInfoHeader(info, relPath)
		if err != nil {
			return err
		}
		// Docker requires paths to be in UNIX format
		header.Name = filepath.ToSlash(relPath)

		if err = tarWriter.WriteHeader(header); err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tarWriter, file)
		return err
	})

	if err != nil {
		return "", fmt.Errorf("failed during tarball creation: %w", err)
	}
	if err = tarWriter.Close(); err != nil {
		return "", fmt.Errorf("failed to close tar writer: %w", err)
	}
	buildContext := bytes.NewReader(buf.Bytes())

	// --- 3. Build the Docker image ---
	fmt.Fprintln(os.Stderr, "-----> Building image... (This may take a moment)")
	buildOptions := types.ImageBuildOptions{
		Tags:        []string{imageTag},
		Dockerfile:  "Dockerfile",
		Remove:      true, // Remove intermediate containers after a successful build
		ForceRemove: true,
	}

	buildResponse, err := cli.ImageBuild(ctx, buildContext, buildOptions)
	if err != nil {
		return "", fmt.Errorf("failed to build image: %w", err)
	}
	defer buildResponse.Body.Close()

	// --- 4. Stream build output to the user ---
	// The Docker daemon's response is a stream of JSON objects. We need to parse
	// them and print the 'stream' field to give the user real-time feedback.
	decoder := json.NewDecoder(buildResponse.Body)
	for {
		// Read one JSON object from the stream
		var line interface{}
		if err := decoder.Decode(&line); err == io.EOF {
			break
		} else if err != nil {
			return "", fmt.Errorf("error reading build response stream: %w", err)
		}

		// The JSON object can be a progress line or an error line
		lineMap := line.(map[string]interface{})
		if errorMsg, ok := lineMap["error"]; ok {
			return "", fmt.Errorf("build failed: %v", errorMsg)
		}
		if streamMsg, ok := lineMap["stream"]; ok {
			fmt.Fprint(os.Stderr, streamMsg) // Print the build step (e.g., "Step 1/5 : FROM ...")
		}
	}

	fmt.Fprintf(os.Stderr, "\n-----> Successfully built image %s\n", imageTag)
	return imageTag, nil
}

func getGitCommitHash(repoPath string, branchName string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--short", branchName)
	cmd.Dir = repoPath

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed for branch '%s': %s", branchName, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}
