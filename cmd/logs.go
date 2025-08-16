package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/moby/moby/pkg/stdcopy"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs <app-name>",
	Short: "View logs for an application",
	Long: `Streams the logs from the specified application's running container.
Use the --follow flag to stream logs in real-time.`,
	Example: `  mitte logs my-cool-app
  mitte logs my-cool-app --follow
  mitte logs my-cool-app -f -n 500  # Follow last 500 lines`,
	Args: cobra.ExactArgs(1),
	Run:  runLogs,
}

func init() {
	// Add flags for follow and tail
	logsCmd.Flags().BoolP("follow", "f", false, "Follow log output.")
	logsCmd.Flags().StringP("tail", "n", "100", "Number of lines to show from the end of the logs.")

	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) {
	appName := args[0]
	follow, _ := cmd.Flags().GetBool("follow")
	tail, _ := cmd.Flags().GetString("tail")
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not connect to Docker daemon: %v\n", err)
		os.Exit(1)
	}
	defer cli.Close()

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tail,
		Timestamps: true,
	}

	// We use the appName as the containerName, as per our deployer's convention.
	logStream, err := cli.ContainerLogs(ctx, appName, options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not get logs for app '%s': %v\n", appName, err)
		os.Exit(1)
	}
	defer logStream.Close()

	// This is the crucial part. The log stream from Docker is multiplexed
	// and contains headers. stdcopy.StdCopy knows how to parse this stream
	// and write the stdout and stderr portions to the correct writers.
	_, err = stdcopy.StdCopy(os.Stdout, os.Stderr, logStream)
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "Error streaming logs: %v\n", err)
		os.Exit(1)
	}
}
