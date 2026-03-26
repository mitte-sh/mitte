package actions

import (
	"context"
	"fmt"
	"os"

	"github.com/mitte-sh/mitte/pkg/deployer"
	"github.com/mitte-sh/mitte/pkg/router"
	"github.com/mitte-sh/mitte/pkg/state"
)

// RestartApp takes an existing application and redeploys it with its latest image
// and the most recent configuration.
func RestartApp(appName string) error {
	ctx := context.Background()

	// 1. Load app state to get configuration and check for pre-built image
	appState, err := state.Load(appName)
	if err != nil {
		return err
	}

	// Determine which image to use
	var imageTag string
	if appState.Image != "" {
		// App has a pre-built image configured, use it directly
		imageTag = appState.Image
	} else {
		// App uses built images, find the latest one
		imageTag, err = deployer.GetLatestImageForApp(context.Background(), appName)
		if err != nil {
			return fmt.Errorf("could not find the latest image for %s. You may need to 'git push' first: %w", appName, err)
		}
	}

	// 2. Deploy the new container. This function handles stopping the old one,
	// creating the new one, and injecting the latest environment variables from state.
	deployResult, err := deployer.Deploy(ctx, appName, imageTag, appState.Volumes, appState.Ports, appState.ContainerName, appState.Command, appState.User)
	if err != nil {
		return err
	}

	// Save the new host port to state
	appState.HostPort = deployResult.HostPort
	if err = appState.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not save host port: %v\n", err)
	}

	// 3. Update the router to point the app's domains to the new container's port.
	authEnabled := appState.Auth != nil && appState.Auth.Enabled
	authPolicy := ""
	if authEnabled {
		authPolicy = appState.Auth.Policy
	}
	return router.SetAppRoutesWithAuth(appName, appState.Domains, deployResult.HostPort, authEnabled, authPolicy)
}
