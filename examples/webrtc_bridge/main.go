// WebRTC/WebSocket bridge example.
// Starts ClearStream as a WSS-compatible audio processing endpoint.
//
// Connect from browser:
//
//	ws := new WebSocket("ws://localhost:8081/stream")
//	// send binary frames: raw PCM 16kHz mono int16 little-endian
//	// receive binary frames: noise-suppressed PCM, same format
//
// Health check: GET http://localhost:8081/health
package main

import (
	"log"
	"net/http"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/model"
	csws "github.com/exotel/clearstream/pkg/websocket"
)

func main() {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		log.Fatalf("clearstream init: %v", err)
	}
	defer cs.Close()
	_ = cs // cs available for ProcessFile / NewRTPSession alongside the bridge

	// Build a suppressor directly; each WebSocket connection gets its own
	// stateful Pipeline wrapping this shared, goroutine-safe suppressor.
	sup, err := model.NewSuppressor(model.SuppressorConfig{Backend: "rnnoise"})
	if err != nil {
		log.Fatalf("suppressor init: %v", err)
	}
	defer sup.Close()

	bridge := csws.NewBridge(csws.BridgeConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: sup,
		Logger:     nil, // uses zap.NewNop() internally; swap for zap.NewProduction()
	})

	http.Handle("/stream", bridge.Handler())
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"clearstream-webrtc-bridge"}`))
	})

	log.Println("ClearStream WebRTC Bridge listening on :8081")
	log.Println("Connect: ws://localhost:8081/stream")
	log.Fatal(http.ListenAndServe(":8081", nil))
}
