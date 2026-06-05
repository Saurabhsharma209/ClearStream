// Package bridge provides E2E test helpers for connecting to the ClearStream
// Voice AI bridge via WebSocket.
//
// CS-004 fix: dp-endpoint is an HTTP resolver that returns {"url":"wss://..."}.
// Dialling it directly as a WebSocket URL produces an immediate protocol error.
// ResolveStreamURL performs the HTTP GET and extracts the WSS target.
//
// CS-005 fix: the resolver endpoint requires HTTP Basic auth.
// Credentials are read from VOICEBOT_API_KEY / VOICEBOT_API_TOKEN environment
// variables; they are never hard-coded or logged.
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ResolveStreamURL fetches the HTTP resolver at endpoint and returns the WSS
// URL from the JSON response body.
//
// Expected response shape:
//
//	{"url": "wss://host/path"}
//
// Basic auth is attached when VOICEBOT_API_KEY and VOICEBOT_API_TOKEN are set.
// The function returns an error on non-200 status or missing url field.
func ResolveStreamURL(ctx context.Context, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("bridge.ResolveStreamURL: build request: %w", err)
	}

	// CS-005: attach Basic auth when credentials are present in the environment.
	apiKey := os.Getenv("VOICEBOT_API_KEY")
	apiToken := os.Getenv("VOICEBOT_API_TOKEN")
	if apiKey != "" && apiToken != "" {
		req.SetBasicAuth(apiKey, apiToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("bridge.ResolveStreamURL: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("bridge.ResolveStreamURL: %s returned HTTP %d: %s",
			endpoint, resp.StatusCode, body)
	}

	var payload struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("bridge.ResolveStreamURL: decode JSON from %s: %w", endpoint, err)
	}
	if payload.URL == "" {
		return "", fmt.Errorf("bridge.ResolveStreamURL: %s returned empty url field", endpoint)
	}
	return payload.URL, nil
}
