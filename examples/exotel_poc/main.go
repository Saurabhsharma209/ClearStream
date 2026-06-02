// examples/exotel_poc/main.go
// ClearStream + Exotel vSIP Integration POC
//
// This example shows how to drop ClearStream into the Exotel media path:
//  1. Exotel sends RTP (G.711 µ-law or A-law) to ClearStream on :5004
//  2. ClearStream suppresses noise, forwards clean RTP to the agent's SIP phone
//  3. A companion HTTP server accepts webhook callbacks from Exotel
//
// Run: go run examples/exotel_poc/main.go --rtp-listen :5004 --rtp-forward AGENT:5004
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/rtp"
	"go.uber.org/zap"
)

func main() {
	rtpListen := flag.String("rtp-listen", ":5004", "RTP listen address (Exotel sends here)")
	rtpForward := flag.String("rtp-forward", "", "RTP forward address (agent SIP phone, e.g. 192.168.1.10:5004)")
	httpAddr := flag.String("http", ":8080", "HTTP webhook + metrics address")
	model := flag.String("model", "passthrough", "Suppressor backend: passthrough | rnnoise")
	flag.Parse()

	if *rtpForward == "" {
		fmt.Fprintln(os.Stderr, "error: --rtp-forward required (e.g. 192.168.1.10:5004)")
		os.Exit(1)
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync() //nolint:errcheck

	// Build SDK config for Exotel PSTN path:
	//  - PCMA (G.711 A-law) preferred by Exotel PSTN trunks
	//  - AGC off: PSTN levels are normalised at the carrier
	//  - VAD on: skip suppression on silence frames (~30% CPU savings)
	cfg := clearstream.DefaultConfig()
	cfg.Model = *model
	cfg.EnableVAD = true

	cs, err := clearstream.New(cfg)
	if err != nil {
		logger.Fatal("clearstream init failed", zap.Error(err))
	}
	defer cs.Close()

	// RTP session: Exotel → ClearStream → Agent SIP phone
	// PayloadType 8 = PCMA (G.711 A-law) — Exotel's preferred PSTN codec.
	// Switch to PayloadType 0 (PCMU) if your Exotel trunk uses µ-law.
	sess, err := cs.NewRTPSession(rtp.Config{
		ListenAddr:  *rtpListen,
		ForwardAddr: *rtpForward,
		Codec:       audio.CodecG711A, // PCMA preferred for Exotel PSTN
		PayloadType: 8,                // RTP PT 8 = PCMA
		JitterDepth: 4,                // ~40ms jitter buffer
		OnStats: func(s rtp.Stats) {
			logger.Info("rtp stats",
				zap.Uint64("rx", s.PacketsReceived),
				zap.Uint64("tx", s.PacketsSent),
				zap.Uint64("lost", s.PacketsLost),
				zap.Float64("latency_ms", s.LatencyAvgMs))
		},
	})
	if err != nil {
		logger.Fatal("rtp session create failed", zap.Error(err))
	}
	sess.Start()
	logger.Info("RTP session started",
		zap.String("listen", *rtpListen),
		zap.String("forward", *rtpForward))

	// HTTP server: health check + Exotel webhook stub + Prometheus metrics
	mux := http.NewServeMux()

	// /health — quick liveness probe
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		stats := cs.PipelineStats()
		fmt.Fprintf(w, `{"status":"ok","pipeline":"%s"}`, stats.String())
	})

	// /webhook/exotel — receives Exotel call events (answered, completed, etc.)
	// Exotel posts form-encoded or JSON bodies; respond 200 to acknowledge.
	mux.HandleFunc("/webhook/exotel", func(w http.ResponseWriter, r *http.Request) {
		callSID := r.FormValue("CallSid")
		status := r.FormValue("Status")
		logger.Info("exotel webhook",
			zap.String("method", r.Method),
			zap.String("call_sid", callSID),
			zap.String("status", status))
		w.WriteHeader(http.StatusOK)
	})

	// /metrics — Prometheus metrics exposed by ClearStream
	mux.Handle("/metrics", cs.NewHTTPHandler())

	srv := &http.Server{
		Addr:         *httpAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	go func() {
		logger.Info("HTTP server started", zap.String("addr", *httpAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", zap.Error(err))
		}
	}()

	// Block until SIGINT / SIGTERM
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	logger.Info("shutting down gracefully")
	sess.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Warn("http shutdown error", zap.Error(err))
	}

	final := sess.Stats()
	logger.Info("final session stats",
		zap.Uint64("rx", final.PacketsReceived),
		zap.Uint64("tx", final.PacketsSent),
		zap.Uint64("lost", final.PacketsLost),
		zap.Float64("avg_latency_ms", final.LatencyAvgMs))
}
