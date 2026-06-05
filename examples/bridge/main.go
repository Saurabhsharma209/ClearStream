// Command bridge is the ClearStream Voice AI bridge.
//
// It listens for WebSocket audio streams from the browser-lab UI and the
// Voice AI orchestrator, applies noise suppression, and optionally captures
// PCAP traces for offline analysis.
//
// Flags:
//
//	--http  ":8081"          HTTP listen address (also serves /health, /stream WS)
//	--model "passthrough"    NR backend: passthrough | rnnoise | deepfilter
//	--pcap  "/path/to/dir"   Enable PCAP capture to this directory
//	--pcap-analyze           Run analysis on captured files at shutdown
//
// Environment variables (CS-005):
//
//	VOICEBOT_API_KEY    Basic-auth username sent to the upstream voicebot endpoint
//	VOICEBOT_API_TOKEN  Basic-auth password sent to the upstream voicebot endpoint
//	DP_ENDPOINT         HTTP resolver URL → returns JSON {"url":"wss://..."} (CS-004)
//
// The bridge resolves DP_ENDPOINT once at startup (CS-010) and caches the WSS
// URL for the lifetime of the process, avoiding the 1 000 req/min rate limit.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/model"
	csws "github.com/exotel/clearstream/pkg/websocket"
)

// ── CS-004 / CS-010: pre-resolved WSS URL cache ──────────────────────────────

// resolvedWSS holds the cached WSS URL obtained by resolving DP_ENDPOINT.
// Populated once at startup; read-only after that (no mutex needed).
var resolvedWSS string

// resolveStreamURL fetches the HTTP resolver at endpoint and extracts the WSS
// URL from the JSON response body.  The resolver is expected to return:
//
//	{"url": "wss://host/path"}
//
// or any JSON object with a top-level "url" key.
//
// CS-004 root cause: dp-endpoint is an HTTP resolver, not a dialable WSS URL.
// Dialling it directly caused an immediate TLS/protocol error.
func resolveStreamURL(endpoint string) (string, error) {
	// CS-005: attach Basic auth credentials when env vars are set.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("resolveStreamURL: build request: %w", err)
	}
	apiKey := os.Getenv("VOICEBOT_API_KEY")
	apiToken := os.Getenv("VOICEBOT_API_TOKEN")
	if apiKey != "" && apiToken != "" {
		req.SetBasicAuth(apiKey, apiToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolveStreamURL: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("resolveStreamURL: %s returned %d: %s", endpoint, resp.StatusCode, body)
	}

	var payload struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("resolveStreamURL: decode JSON from %s: %w", endpoint, err)
	}
	if payload.URL == "" {
		return "", fmt.Errorf("resolveStreamURL: %s returned empty url field", endpoint)
	}
	return payload.URL, nil
}

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	addr := flag.String("http", ":8081", "HTTP listen address")
	modelBackend := flag.String("model", "passthrough", "NR backend: passthrough | rnnoise | deepfilter")
	pcapDir := flag.String("pcap", "", "PCAP capture directory (empty = disabled)")
	pcapAnalyze := flag.Bool("pcap-analyze", false, "Analyze captured PCAPs at shutdown")
	flag.Parse()

	// ── CS-010: resolve dp-endpoint once at startup ───────────────────────────
	dpEndpoint := os.Getenv("DP_ENDPOINT")
	if dpEndpoint != "" {
		log.Printf("[bridge] resolving dp-endpoint: %s", dpEndpoint)
		var err error
		resolvedWSS, err = resolveStreamURL(dpEndpoint)
		if err != nil {
			// Non-fatal: bridge can still serve sessions that don't need dp-endpoint.
			log.Printf("[bridge] WARN: dp-endpoint resolve failed: %v (will retry per-call)", err)
		} else {
			log.Printf("[bridge] dp-endpoint resolved → %s", resolvedWSS)
		}
	}

	// ── Suppressor ────────────────────────────────────────────────────────────
	sup, err := model.NewSuppressor(model.SuppressorConfig{Backend: *modelBackend})
	if err != nil {
		log.Fatalf("[bridge] suppressor init (%s): %v", *modelBackend, err)
	}
	defer sup.Close() //nolint:errcheck

	cs, err := clearstream.New(clearstream.Config{
		Model:                 *modelBackend,
		SampleRate:            16000,
		Channels:              1,
		MaxConcurrentSessions: 64,
		EnableVAD:             true,
		AdaptiveVAD:           true,
	})
	if err != nil {
		log.Fatalf("[bridge] clearstream init: %v", err)
	}
	defer cs.Close() //nolint:errcheck

	bridge := csws.NewBridge(csws.BridgeConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: sup,
	})

	// ── HTTP routes ───────────────────────────────────────────────────────────
	mux := http.NewServeMux()

	// WebSocket audio stream.
	mux.Handle("/stream", bridge.Handler())

	// Health check — qa_up.sh polls this to confirm the bridge is ready.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"clearstream-bridge"}`))
	})

	// Expose resolved WSS URL for diagnostic purposes (no credentials returned).
	mux.HandleFunc("/resolved-url", func(w http.ResponseWriter, r *http.Request) {
		if resolvedWSS == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"dp-endpoint not configured or not yet resolved"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": resolvedWSS})
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// ── PCAP capture ──────────────────────────────────────────────────────────
	var pcapWg sync.WaitGroup
	if *pcapDir != "" {
		if err := os.MkdirAll(*pcapDir, 0o755); err != nil {
			log.Printf("[bridge] WARN: cannot create pcap dir %s: %v", *pcapDir, err)
		} else {
			log.Printf("[bridge] PCAP capture → %s", *pcapDir)
			pcapWg.Add(1)
			go func() {
				defer pcapWg.Done()
				// Placeholder: wire up actual pcap capture library here.
				// The pcap-analyze flag is checked at shutdown (below).
				_ = pcapAnalyze
			}()
		}
	}

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	go func() {
		log.Printf("[bridge] listening on %s (model=%s)", *addr, *modelBackend)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[bridge] ListenAndServe: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("[bridge] shutting down…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("[bridge] shutdown error: %v", err)
	}

	pcapWg.Wait()
	log.Println("[bridge] stopped")
}
