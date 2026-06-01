// Package main demonstrates ClearStream integration with Exotel Contact Center (ECC).
//
// Architecture:
//
//	SIP Trunk → Kamailio/Twilix → [ClearStream RTP Proxy] → AgentStream STT
//	                                                       → Agent Desktop (clean audio)
//
// This example starts:
//  1. ClearStream HTTP server on :8080 (for file enhancement API)
//  2. ClearStream SIP proxy on :8081 (HTTP control API for live call enhancement)
//  3. Prints integration instructions for Exotel ECC
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/exotel/clearstream"
	cshttp "github.com/exotel/clearstream/pkg/http"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/exotel/clearstream/pkg/sip"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewProduction()
	defer logger.Sync() //nolint:errcheck

	cfg := clearstream.DefaultConfig()
	cs, err := clearstream.New(cfg)
	if err != nil {
		log.Fatal("init:", err)
	}
	defer cs.Close()

	// 1. Start HTTP API for file enhancement and AgentStream integration.
	httpSrv := &http.Server{
		Addr: ":8080",
		Handler: cshttp.NewHandler(cshttp.HandlerConfig{
			FFmpegPath: cfg.FFmpegPath,
			SampleRate: cfg.SampleRate,
			Logger:     logger,
		}),
	}
	go func() {
		log.Println("[ECC] HTTP API on :8080")
		log.Println("[ECC]   POST /enhance             — clean recorded calls before STT")
		log.Println("[ECC]   GET  /health              — health check for load balancer")
		log.Println("[ECC]   GET  /metrics             — JSON metrics")
		log.Println("[ECC]   GET  /metrics/prometheus  — Prometheus scrape endpoint")
		if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal("HTTP:", err)
		}
	}()

	// 2. Start SIP proxy HTTP control API for live RTP interception.
	sup, _ := model.NewSuppressor(model.SuppressorConfig{Backend: "passthrough"})
	proxy := sip.NewProxy(sup, logger)
	proxySrv := &http.Server{
		Addr:    ":8081",
		Handler: proxy,
	}
	go func() {
		log.Println("[ECC] SIP Proxy control API on :8081")
		log.Println("[ECC]   POST /session/start — begin noise suppression for a call leg")
		log.Println("[ECC]   POST /session/stop  — end session")
		log.Println("[ECC]   GET  /sessions      — list active sessions")
		if err := proxySrv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal("SIP proxy:", err)
		}
	}()

	printIntegrationGuide()

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)  //nolint:errcheck
	proxySrv.Shutdown(ctx) //nolint:errcheck
	log.Println("[ECC] Shutdown complete")
}

func printIntegrationGuide() {
	log.Print(`
+--------------------------------------------------------------+
|         ClearStream x Exotel ECC Integration Guide          |
+--------------------------------------------------------------+
|                                                              |
|  Step 1: Exotel ECC -> ClearStream (live call enhancement)  |
|    When a call starts, POST to ClearStream SIP proxy:        |
|    curl -X POST http://clearstream:8081/session/start \      |
|      -d '{"call_id":"<ECC_CALL_ID>",                         |
|           "sdp":"<SDP_FROM_INVITE>",                         |
|           "inbound_addr":"0.0.0.0:5004",                     |
|           "agentstream_addr":"<AGENTSTREAM_RTP>",            |
|           "outbound_addr":"0.0.0.0:5006",                    |
|           "caller_addr":"<CALLER_RTP>"}'                     |
|                                                              |
|  Step 2: ECC Recording pipeline (post-processing)            |
|    curl -X POST http://clearstream:8080/enhance \            |
|      -F "audio=@recording.wav" -o clean.wav                  |
|                                                              |
|  Step 3: Monitor via Prometheus                              |
|    scrape: http://clearstream:8080/metrics/prometheus        |
|                                                              |
+--------------------------------------------------------------+`)
}
