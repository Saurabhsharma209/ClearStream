// Package agentstream provides a ClearStream client for the Exotel AgentStream
// pipeline.  Drop this file into your AgentStream service and call
// EnhanceAudio before forwarding audio to the STT engine.
//
// Typical usage in AgentStream:
//
//	cs := agentstream.NewClearStreamClient("http://clearstream.internal:8080")
//	clean, err := cs.EnhanceAudio(rawWAV, "call-abc123.wav")
//	if err != nil {
//	    log.Printf("clearstream unavailable, using raw audio: %v", err)
//	    clean = rawWAV
//	}
//	sendToSTT(clean)
package agentstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"time"
)

// ClearStreamClient enhances audio via the ClearStream HTTP API before STT.
type ClearStreamClient struct {
	// BaseURL is the ClearStream server base URL, e.g. "http://clearstream:8080".
	BaseURL string

	// Client is the underlying HTTP client.  Override to set custom timeouts,
	// TLS config, or tracing instrumentation.
	Client *http.Client
}

// NewClearStreamClient returns a ClearStreamClient with sensible defaults.
// baseURL should not have a trailing slash.
func NewClearStreamClient(baseURL string) *ClearStreamClient {
	return &ClearStreamClient{
		BaseURL: baseURL,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// EnhanceAudio posts audioData to ClearStream POST /enhance and returns the
// clean audio bytes (WAV).  filename is used only to set the multipart
// Content-Disposition filename; it does not need to exist on disk.
//
// On success the returned bytes are a valid WAV file ready to be forwarded to
// the STT engine.  On error the caller should fall back to the original audio.
func (c *ClearStreamClient) EnhanceAudio(audioData []byte, filename string) ([]byte, error) {
	return c.EnhanceAudioContext(context.Background(), audioData, filename)
}

// EnhanceAudioContext is like EnhanceAudio but accepts a context for
// cancellation and deadline propagation.
func (c *ClearStreamClient) EnhanceAudioContext(ctx context.Context, audioData []byte, filename string) ([]byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	fw, err := w.CreateFormFile("audio", filepath.Base(filename))
	if err != nil {
		return nil, fmt.Errorf("clearstream: create form file: %w", err)
	}
	if _, err := fw.Write(audioData); err != nil {
		return nil, fmt.Errorf("clearstream: write audio data: %w", err)
	}
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/enhance", &buf)
	if err != nil {
		return nil, fmt.Errorf("clearstream: build request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clearstream: POST /enhance: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("clearstream: /enhance returned HTTP %d: %s", resp.StatusCode, body)
	}

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("clearstream: read response body: %w", err)
	}
	return out, nil
}

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status    string  `json:"status"`
	Model     string  `json:"model"`
	UptimeSec float64 `json:"uptime_s"`
}

// Health calls GET /health and returns the parsed response.
// Use this in your readiness probe / dependency check.
func (c *ClearStreamClient) Health(ctx context.Context) (*HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return nil, fmt.Errorf("clearstream: health request: %w", err)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clearstream: GET /health: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clearstream: /health returned HTTP %d", resp.StatusCode)
	}

	var h HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, fmt.Errorf("clearstream: decode health response: %w", err)
	}
	return &h, nil
}

// IsHealthy is a convenience wrapper around Health that returns true only when
// ClearStream reports status "ok".  Suitable for use in a simple boolean gate:
//
//	if cs.IsHealthy(ctx) {
//	    audio, _ = cs.EnhanceAudio(audio, name)
//	}
func (c *ClearStreamClient) IsHealthy(ctx context.Context) bool {
	h, err := c.Health(ctx)
	return err == nil && h.Status == "ok"
}

// EnhanceWithFallback calls EnhanceAudio but returns the original audioData if
// ClearStream is unavailable or returns an error.  Logs the error to errOut if
// non-nil.  This is the recommended integration pattern for non-critical
// enhancement where call continuity must be preserved.
func (c *ClearStreamClient) EnhanceWithFallback(ctx context.Context, audioData []byte, filename string, errOut io.Writer) []byte {
	clean, err := c.EnhanceAudioContext(ctx, audioData, filename)
	if err != nil {
		if errOut != nil {
			fmt.Fprintf(errOut, "clearstream: enhancement skipped (%v), using original audio\n", err)
		}
		return audioData
	}
	return clean
}
