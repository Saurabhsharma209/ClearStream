// Package websocket provides a WebSocket bridge for real-time audio enhancement.
// Browser/mobile clients send raw PCM audio frames and receive cleaned frames back.
// This enables ClearStream integration with WebRTC, Exotel WebRTC SDK, and any
// WSS-based media gateway without SIP or RTP dependencies.
//
// # Exotel WSS media gateway integration
//
// ClearStream can act as a transparent WSS media entry point in Exotel's existing
// WebSocket audio pipeline. Insert it between the Exotel WebRTC SDK and your
// downstream STT/recording systems:
//
//	Browser/Exotel SDK -> wss://your-host/audio -> ClearStream bridge -> clean PCM -> STT / recording
//
// The bridge is stateless per-connection (each WebSocket connection gets its own
// Pipeline instance), making it safe for horizontal scaling behind a load balancer.
//
// To place it behind nginx as a WSS endpoint, add to your nginx config:
//
//	location /audio {
//	    proxy_pass http://clearstream:8081;
//	    proxy_http_version 1.1;
//	    proxy_set_header Upgrade $http_upgrade;
//	    proxy_set_header Connection "upgrade";
//	    proxy_set_header Host $host;
//	}
//
// Protocol: clients send binary WebSocket messages containing raw 16kHz mono
// signed 16-bit PCM (little-endian). The bridge responds with binary messages
// of the same format containing noise-suppressed audio.
package websocket

import (
	"bytes"
	"net/http"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const defaultMaxFrameBytes int64 = 65536 // 64 KiB -- about 2 seconds of 16kHz mono PCM

// BridgeConfig configures the WebSocket bridge.
type BridgeConfig struct {
	// SampleRate is the PCM sample rate expected from clients. Default: 16000.
	SampleRate int
	// Channels is the number of audio channels expected. Default: 1 (mono).
	Channels int
	// Suppressor is the noise suppression backend to use per connection.
	// Required.
	Suppressor model.Suppressor
	// Logger is an optional zap logger. If nil, a no-op logger is used.
	Logger *zap.Logger
	// MaxFrameBytes is the maximum size of a single incoming WebSocket message.
	// Messages larger than this are rejected. Default: 65536.
	MaxFrameBytes int64

	// AGC enables Automatic Gain Control for every connection on this bridge.
	// Each connection gets its own independent AGC state.
	// Use audio.DefaultAGCConfig() as a starting point.
	// Set to nil to disable (default).
	AGC *audio.AGCConfig
}

// Bridge is a WebSocket server that accepts raw PCM audio, runs it through the
// ClearStream noise suppression pipeline, and streams clean PCM back.
type Bridge struct {
	cfg      BridgeConfig
	upgrader websocket.Upgrader
	logger   *zap.Logger
}

// NewBridge creates a new Bridge with the given config.
func NewBridge(cfg BridgeConfig) *Bridge {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.Channels == 0 {
		cfg.Channels = 1
	}
	if cfg.MaxFrameBytes == 0 {
		cfg.MaxFrameBytes = defaultMaxFrameBytes
	}
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Bridge{
		cfg:    cfg,
		logger: logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  int(cfg.MaxFrameBytes),
			WriteBufferSize: int(cfg.MaxFrameBytes),
			// Allow all origins for browser compatibility.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Handler returns an http.Handler that upgrades incoming HTTP connections to
// WebSocket and begins audio processing. Mount this on your ServeMux:
//
//	http.Handle("/stream", bridge.Handler())
func (b *Bridge) Handler() http.Handler {
	return http.HandlerFunc(b.ServeWS)
}

// ServeWS is the net/http handler form of the bridge endpoint.
// It upgrades the connection to WebSocket and processes audio until the
// client disconnects.
func (b *Bridge) ServeWS(w http.ResponseWriter, r *http.Request) {
	// CORS header for browser WebRTC clients.
	w.Header().Set("Access-Control-Allow-Origin", "*")

	conn, err := b.upgrader.Upgrade(w, r, nil)
	if err != nil {
		b.logger.Warn("websocket upgrade failed", zap.Error(err))
		return
	}
	defer conn.Close()

	remoteAddr := r.RemoteAddr
	b.logger.Info("client connected", zap.String("remote", remoteAddr))

	// Each connection gets its own stateful pipeline instance (including AGC state).
	pipeline := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: b.cfg.SampleRate,
		Channels:   b.cfg.Channels,
		Suppressor: b.cfg.Suppressor,
		Logger:     b.logger,
		AGC:        b.cfg.AGC,
	})

	conn.SetReadLimit(b.cfg.MaxFrameBytes)

	var outBuf bytes.Buffer

	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
				websocket.CloseNoStatusReceived,
			) {
				b.logger.Warn("unexpected close", zap.String("remote", remoteAddr), zap.Error(err))
			} else {
				b.logger.Info("client disconnected", zap.String("remote", remoteAddr))
			}
			break
		}

		if msgType != websocket.BinaryMessage {
			b.logger.Warn("non-binary message ignored",
				zap.String("remote", remoteAddr), zap.Int("type", msgType))
			continue
		}

		outBuf.Reset()
		if err := pipeline.ProcessFrames(data, &outBuf); err != nil {
			b.logger.Error("pipeline error", zap.String("remote", remoteAddr), zap.Error(err))
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "pipeline error"))
			return
		}

		if outBuf.Len() > 0 {
			if err := conn.WriteMessage(websocket.BinaryMessage, outBuf.Bytes()); err != nil {
				b.logger.Warn("write error", zap.String("remote", remoteAddr), zap.Error(err))
				return
			}
		}
	}

	// Drain any partial frame buffered in the pipeline.
	outBuf.Reset()
	if err := pipeline.Flush(&outBuf); err != nil {
		b.logger.Warn("flush error", zap.String("remote", remoteAddr), zap.Error(err))
		return
	}
	if outBuf.Len() > 0 {
		_ = conn.WriteMessage(websocket.BinaryMessage, outBuf.Bytes())
	}

	b.logger.Info("connection closed cleanly", zap.String("remote", remoteAddr),
		zap.Any("stats", pipeline.Stats()))
}
