package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/mitteapp/mitteapp/pkg/config"
	"github.com/mitteapp/mitteapp/pkg/deployer"
	"github.com/mitteapp/mitteapp/pkg/router"
	"github.com/mitteapp/mitteapp/pkg/state"
)

var appsCmd = &cobra.Command{
	Use:     "apps",
	Short:   "Manage your applications",
	Aliases: []string{"app"},
}

var appsListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all deployed applications",
	Aliases: []string{"ls"},
	Run:     runAppsList,
}

const placeholderImage = "nginxdemos/hello:plain-text"

var appsCreateCmd = &cobra.Command{
	Use:   "create <app-name>",
	Short: "Create a new application placeholder",
	Args:  cobra.ExactArgs(1),
	Run:   runAppsCreate,
}

// appsDestroyCmd handles `mitte apps destroy <app-name>`
var appsDestroyCmd = &cobra.Command{
	Use:   "destroy <app-name>",
	Short: "Permanently destroy an application",
	Long: `Permanently destroy an application.
This will stop and remove the container, delete all built images,
remove networking routes, and delete the git repository.
THIS ACTION IS IRREVERSIBLE.`,
	Aliases: []string{"remove"},
	Args:    cobra.ExactArgs(1),
	Run:     runAppsDestroy,
}

func init() {
	appsCmd.AddCommand(appsListCmd)
	appsCmd.AddCommand(appsCreateCmd)
	appsCmd.AddCommand(appsDestroyCmd)
	rootCmd.AddCommand(appsCmd)
}

func runAppsList(cmd *cobra.Command, args []string) {
	const appsDir = "/var/lib/mitte/apps"
	files, err := os.ReadDir(appsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No applications have been deployed yet.")
			return
		}
		fmt.Fprintf(os.Stderr, "Error: Could not read the application directory: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("No applications found.")
		return
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not connect to Docker daemon: %v\n", err)
		os.Exit(1)
	}
	defer cli.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	defer w.Flush()
	fmt.Fprintln(w, "APP\tSTATUS\tCREATED\tIMAGE TAG\tURLS")

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		appName := strings.TrimSuffix(file.Name(), ".json")

		var status, created, imageTag, urls string

		appState, err := state.Load(appName)
		if err != nil {
			urls = "error"
		} else {
			urls = strings.Join(appState.Domains, ", ")
		}

		inspect, err := cli.ContainerInspect(context.Background(), appName)
		if err != nil {
			if errdefs.IsNotFound(err) {
				status = "stopped"
				created = "-"
				imageTag = "(no image)"
			} else {
				status = "error"
			}
		} else {
			status = inspect.State.Status
			imageTag = filepath.Base(inspect.Config.Image)
			createdTime, _ := time.Parse(time.RFC3339Nano, inspect.Created)
			created = formatTimeAgo(createdTime)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", appName, status, created, imageTag, urls)
	}
}

func runAppsCreate(cmd *cobra.Command, args []string) {
	appName := args[0]
	fmt.Fprintf(os.Stderr, "Creating app '%s'... ", appName)

	// --- 1. Check if app already exists ---
	app, err := state.Load(appName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: Could not check app state: %v\n", err)
		os.Exit(1)
	}
	// An "existing" app is one that already has domains.
	if len(app.Domains) > 0 {
		fmt.Fprintf(os.Stderr, "\nError: Application '%s' already exists.\n", appName)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "done.")

	// --- 2. Create app state with a default domain ---
	baseDomain, err := config.GetBaseDomain()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	defaultDomain := fmt.Sprintf("%s.%s", appName, baseDomain)
	fmt.Fprintf(os.Stderr, "Assigning default domain: %s\n", defaultDomain)
	app.Domains = append(app.Domains, defaultDomain)
	if err = app.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not save app state: %v\n", err)
		os.Exit(1)
	}

	// --- 3. Pull the placeholder image ---
	fmt.Fprintf(os.Stderr, "Pulling placeholder image '%s'... ", placeholderImage)
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: Could not connect to Docker: %v\n", err)
		os.Exit(1)
	}
	defer cli.Close()

	reader, err := cli.ImagePull(context.Background(), placeholderImage, image.PullOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: Could not pull placeholder image: %v\n", err)
		os.Exit(1)
	}
	io.Copy(io.Discard, reader) // Wait for the pull to complete but discard the noisy output
	reader.Close()
	fmt.Fprintln(os.Stderr, "done.")

	// --- 4. Deploy the placeholder image ---
	fmt.Fprintln(os.Stderr, "Deploying placeholder application...")
	deployResult, err := deployer.Deploy(context.Background(), appName, placeholderImage)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not deploy placeholder: %v\n", err)
		os.Exit(1)
	}

	// --- 5. Route traffic ---
	fmt.Fprintln(os.Stderr, "Routing traffic...")
	if err := router.SetAppRoutes(appName, app.Domains, deployResult.HostPort); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Could not update routes: %v\n", err)
		// Don't exit here, the app is running, just not routable.
	}

	// --- Final Success Message ---
	fmt.Printf("\nSuccess! Your new application '%s' is ready.\n", appName)
	fmt.Printf("You can view it at: http://%s\n", defaultDomain)
	fmt.Println("To deploy your own code, add the git remote and push:")
	fmt.Printf("  git remote add mitte mitte@%s:%s\n", baseDomain, appName)
	fmt.Println("  git push mitte main")
}

func runAppsDestroy(cmd *cobra.Command, args []string) {
	userInput := args[0]
	ctx := context.Background()

	appName, err := state.ResolveAppName(userInput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Define color functions
	redBold := color.New(color.FgRed, color.Bold)
	greenBold := color.New(color.FgGreen, color.Bold)
	yellowBold := color.New(color.FgYellow, color.Bold)
	whiteBold := color.New(color.FgWhite, color.Bold)
	cyan := color.New(color.FgCyan)
	blue := color.New(color.FgBlue)
	purple := color.New(color.FgMagenta)
	yellow := color.New(color.FgYellow)

	// --- 1. SAFETY: User Confirmation ---
	redBold.Print("⚠️  WARNING: This action is irreversible and will destroy the application '")
	yellowBold.Print(appName)
	redBold.Println("'.")
	redBold.Println("⚠️  This will remove the container, all built images, networking, and the git repository.")
	yellowBold.Print("💀 Please type '")
	whiteBold.Print(appName)
	yellowBold.Print("' to confirm: ")

	reader := bufio.NewReader(os.Stdin)
	confirmation, _ := reader.ReadString('\n')
	confirmation = strings.TrimSpace(confirmation)

	if confirmation != appName {
		redBold.Println("❌ Confirmation failed. Aborting destruction.")
		os.Exit(1)
	}

	fmt.Println()
	redBold.Println("💥 Starting destruction process...")

	// --- 2. Remove Routes ---
	cyan.Println("🔗 Removing network routes...")
	if err := router.SetAppRoutes(appName, []string{}, ""); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not remove Caddy routes: %v\n", err)
	}

	// --- 3. Stop and Remove Container ---
	blue.Println("🐳 Stopping and removing container...")
	if err := deployer.StopAndRemoveContainer(ctx, appName); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not stop/remove container: %v\n", err)
	}

	// --- 4. Prune Images ---
	purple.Println("🖼️ Pruning Docker images...")
	if err := deployer.PruneAppImages(ctx, appName); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not prune images: %v\n", err)
	}

	// --- 5. Delete Git Repository ---
	repoPath := filepath.Join("/var/lib/mitte/repos", appName+".git")
	cyan.Printf("📂 Deleting git repository at %s...\n", repoPath)
	if err := exec.Command("sudo", "rm", "-rf", repoPath).Run(); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not delete git repository: %v\n", err)
	}

	// --- 6. Delete State File ---
	statePath := filepath.Join("/var/lib/mitte/apps", appName+".json")
	cyan.Printf("🗑️ Deleting state file at %s...\n", statePath)
	if err := exec.Command("sudo", "rm", "-f", statePath).Run(); err != nil {
		yellow.Fprintf(os.Stderr, "⚠️  Warning: could not delete state file: %v\n", err)
	}

	fmt.Println()
	greenBold.Print("✅ Success! Application '")
	whiteBold.Print(appName)
	greenBold.Println("' has been destroyed.")
}

func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%d seconds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
