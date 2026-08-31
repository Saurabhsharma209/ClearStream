// Command agentstream runs a standalone Exotel AgentStream-compatible
// WebSocket server backed by ClearStream's noise suppression pipeline.
//
// Point an AgentStream stream URL -- via the Legs start_stream action, the
// /calls/connect Voice API, or the VoiceBot Applet's dynamic-URL mode -- at
// this server's /media endpoint. Per-call configuration (which backend,
// whether to enable VAD/AGC/adaptive tiering) comes entirely from that
// call's CustomParameters; nothing here is hardcoded per customer or call.
//
// See docs/exotel-agentstream-integration.md for the exact parameter names
// and example requests.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/exotel/clearstream/pkg/agentstream"
	"go.uber.org/zap"
)

func main() {
	addr := flag.String("http", ":8090", "HTTP/WS listen address")
	defaultBackend := flag.String("default-backend", "passthrough", "Backend used when a call's start event omits ns_model")
	flag.Parse()

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

	server := agentstream.NewAgentStreamServer(agentstream.ServerConfig{
		Logger:         logger,
		DefaultBackend: *defaultBackend,
	})

	mux := http.NewServeMux()
	mux.Handle("/media", server.Handler())

	logger.Info("agentstream server listening", zap.String("addr", *addr))
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
