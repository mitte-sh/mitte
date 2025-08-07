package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const caddyAdminAPI = "http://localhost:2019"

const baseDomain = "hh.com.py"

func UpdateRoute(appName, hostPort string) error {
	appURL := fmt.Sprintf("%s.%s", appName, baseDomain)
	fmt.Fprintf(os.Stderr, "-----> Updating Caddy to route %s -> localhost:%s\n", appURL, hostPort)

	route := map[string]interface{}{
		"@id": appName, // This unique ID is key for Caddy to manage the route
		"match": []map[string]interface{}{
			{
				"host": []string{appURL},
			},
		},
		"handle": []map[string]interface{}{
			{
				"handler": "reverse_proxy",
				"upstreams": []map[string]interface{}{
					{
						"dial": "localhost:" + hostPort,
					},
				},
			},
		},
		"terminal": true,
	}

	jsonPayload, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("failed to marshal Caddy route config: %w", err)
	}

	apiPath := "/config/apps/http/servers/srv0/routes"
	requestURL := caddyAdminAPI + apiPath

	// The method is POST. Caddy will see the @id in the payload and do a
	// create-or-replace operation automatically.
	req, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create Caddy API request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request to Caddy API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy API returned error status: %s - %s", resp.Status, string(body))
	}

	fmt.Fprintln(os.Stderr, "-----> Caddy route updated successfully.")
	return nil
}
