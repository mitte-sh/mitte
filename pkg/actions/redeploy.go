package actions

import (
	"context"
	"fmt"

	"github.com/mitteapp/mitteapp/pkg/deployer"
	"github.com/mitteapp/mitteapp/pkg/router"
	"github.com/mitteapp/mitteapp/pkg/state"
)

// RestartApp takes an existing application and redeploys it with its latest image
// and the most recent configuration.
func RestartApp(appName string) error {
	ctx := context.Background()

	// 1. Find the latest Docker image tag for the application.
	imageTag, err := deployer.GetLatestImageForApp(context.Background(), appName)
	if err != nil {
		return fmt.Errorf("could not find the latest image for %s. You may need to 'git push' first: %w", appName, err)
	}

	// 2. Load app state to get volumes configuration
	appState, err := state.Load(appName)
	if err != nil {
		return err
	}

	// 3. Deploy the new container. This function handles stopping the old one,
	// creating the new one, and injecting the latest environment variables from state.
	deployResult, err := deployer.Deploy(ctx, appName, imageTag, appState.Volumes, appState.Ports, appState.ContainerName)
	if err != nil {
		return err
	}

	// 4. Update the router to point the app's domains to the new container's port.
	return router.SetAppRoutes(appName, appState.Domains, deployResult.HostPort)
}
