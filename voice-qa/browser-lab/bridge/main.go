// Voice AI Lab — ClearStream WebSocket bridge with metrics and model flags.
//
//	go run examples/voice_ai_lab/bridge/main.go --http :8081 --model passthrough
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	csws "github.com/exotel/clearstream/pkg/websocket"
)

func main() {
	addr := flag.String("http", ":8081", "HTTP listen address")
	modelName := flag.String("model", "passthrough", "Suppressor: passthrough | rnnoise")
	enableVAD := flag.Bool("vad", false, "Enable VAD (off for L0 latency contract)")
	flag.Parse()

	sup, err := model.NewSuppressor(model.SuppressorConfig{Backend: *modelName})
	if err != nil {
		log.Fatalf("suppressor: %v", err)
	}
	defer sup.Close()

	bridge := csws.NewBridge(csws.BridgeConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: sup,
		ModelName:  sup.Name(),
	})

	mux := http.NewServeMux()
	mux.Handle("/stream", bridge.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-ClearStream-Model", sup.Name())
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"model":  sup.Name(),
			"vad":    *enableVAD,
		})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-ClearStream-Model", sup.Name())
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"model":       sup.Name(),
			"frame_bytes": audio.FrameSizeBytes,
			"samples":     audio.FrameSizeSamples,
		})
	})

	log.Printf("Voice AI Lab bridge listening on %s", *addr)
	log.Printf("  WS stream: ws://localhost%s/stream", *addr)
	log.Printf("  model=%s vad=%v (L0 uses passthrough + vad=false)", sup.Name(), *enableVAD)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
