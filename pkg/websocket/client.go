package websocket

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// ReconnectConfig configures the reconnecting WebSocket client.
type ReconnectConfig struct {
	// URL is the WebSocket endpoint to connect to (e.g. "wss://stt.example.com/stream").
	URL string

	// QueueSize is the maximum number of frames to buffer while disconnected.
	// When the queue is full the oldest frame is dropped to make room.
	// Default: 256 (~2.56 seconds at 10ms/frame).
	QueueSize int

	// InitialBackoff is the first reconnect delay. Default: 100ms.
	InitialBackoff time.Duration

	// MaxBackoff caps the reconnect delay. Default: 8s.
	MaxBackoff time.Duration

	// Logger is an optional zap logger.
	Logger *zap.Logger
}

// ReconnectClient is a WebSocket client that automatically reconnects with
// exponential backoff when the upstream connection drops.  Callers push binary
// audio frames via Send(); frames are queued while disconnected and drained in
// order once the connection is re-established.
//
// Typical use: forward clean PCM from the ClearStream pipeline to a downstream
// STT or recording service over WebSocket, tolerating brief network glitches
// without dropping more audio than the queue can hold.
type ReconnectClient struct {
	cfg       ReconnectConfig
	queue     chan []byte
	logger    *zap.Logger
	stopCh    chan struct{}
	once      sync.Once
	connected uint32 // 0=false 1=true, accessed via sync/atomic
}

// NewReconnectClient creates a ReconnectClient and immediately begins
// attempting to connect in the background.  Call Stop() to shut it down.
func NewReconnectClient(cfg ReconnectConfig) *ReconnectClient {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 256
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 8 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	c := &ReconnectClient{
		cfg:    cfg,
		queue:  make(chan []byte, cfg.QueueSize),
		logger: logger,
		stopCh: make(chan struct{}),
	}
	go c.connectLoop()
	return c
}

// Send enqueues a binary frame for delivery to the upstream WebSocket.
// If the queue is full, the oldest frame is dropped to make room for the new
// one, preserving recency (newer audio is always preferred over stale frames).
// Non-blocking: never delays the caller's audio pipeline.
func (c *ReconnectClient) Send(frame []byte) {
	// Fast path: buffer has space.
	select {
	case c.queue <- frame:
		return
	default:
	}
	// Queue full — drop the oldest frame to make room.
	select {
	case <-c.queue:
	default:
	}
	select {
	case c.queue <- frame:
	default:
	}
}

// Connected reports whether the client currently has an active connection.
func (c *ReconnectClient) Connected() bool {
	return atomic.LoadUint32(&c.connected) == 1
}

// Stop shuts down the reconnect loop and closes the underlying connection.
// It is safe to call Stop multiple times.
func (c *ReconnectClient) Stop() {
	c.once.Do(func() { close(c.stopCh) })
}

// connectLoop dials the WebSocket URL, drains the queue, reconnects with
// exponential backoff on failure, and exits when Stop() is called.
func (c *ReconnectClient) connectLoop() {
	backoff := c.cfg.InitialBackoff
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		conn, _, err := websocket.DefaultDialer.Dial(c.cfg.URL, nil)
		if err != nil {
			atomic.StoreUint32(&c.connected, 0)
			c.logger.Warn("websocket dial failed, retrying",
				zap.String("url", c.cfg.URL),
				zap.Duration("backoff", backoff),
				zap.Error(err),
			)
			select {
			case <-time.After(backoff):
			case <-c.stopCh:
				return
			}
			backoff = min(backoff*2, c.cfg.MaxBackoff)
			continue
		}

		// Connected — reset backoff.
		atomic.StoreUint32(&c.connected, 1)
		backoff = c.cfg.InitialBackoff
		c.logger.Info("websocket connected", zap.String("url", c.cfg.URL))

		disconnected := c.drainQueue(conn)

		conn.Close()
		atomic.StoreUint32(&c.connected, 0)

		if !disconnected {
			// Stop() was called; exit cleanly.
			return
		}
		c.logger.Info("websocket disconnected, will reconnect",
			zap.String("url", c.cfg.URL),
			zap.Duration("backoff", backoff),
		)
	}
}

// drainQueue reads frames from the queue and writes them to conn until conn
// errors (disconnection) or Stop() is called.  Returns true if the disconnect
// was due to a network error (caller should reconnect), false if Stop() fired.
func (c *ReconnectClient) drainQueue(conn *websocket.Conn) (shouldReconnect bool) {
	for {
		select {
		case <-c.stopCh:
			// Graceful shutdown: send a close frame.
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"))
			return false

		case frame := <-c.queue:
			if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				c.logger.Warn("websocket write error", zap.Error(err))
				return true // network error → reconnect
			}
		}
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
