package agentstream

import (
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// failAfterHandshakeConn wraps a net.Conn so that the very first Write
// (the HTTP/1.1 101 Switching Protocols handshake response written by
// gorilla's Upgrade) succeeds, but every Write after that fails. This lets
// a real client complete the WebSocket handshake successfully while making
// the server's very next attempt to write to the connection -- serveWS's
// initial `s.send(conn, ConnectedEvent{...})` -- fail deterministically.
//
// This targets serveWS's one remaining uncovered branch (flagged in
// DEVLOG.md 09-07 through 09-15 as "send-failure racing a concurrent
// close", never closed for lack of an injectable net.Conn): the
// `if err := s.send(conn, ConnectedEvent{...}); err != nil` handler that
// logs and returns without ever entering the read loop.
type failAfterHandshakeConn struct {
	net.Conn
	handshakeWritten int32
}

func (c *failAfterHandshakeConn) Write(b []byte) (int, error) {
	if atomic.CompareAndSwapInt32(&c.handshakeWritten, 0, 1) {
		// First write: let the real HTTP upgrade response through so the
		// client's Dial() completes normally.
		return c.Conn.Write(b)
	}
	return 0, errors.New("simulated write failure (send-failure-racing-close injection)")
}

type failAfterHandshakeListener struct {
	net.Listener
}

func (l *failAfterHandshakeListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return c, err
	}
	return &failAfterHandshakeConn{Conn: c}, nil
}

// TestServeWSConnectedEventSendFailure exercises serveWS's connected-event
// send-failure branch by injecting a net.Conn that fails every write after
// the handshake response. A real client completes the WebSocket handshake
// (Dial succeeds) but then observes the server close the connection
// without ever delivering the "connected" event, because the server's
// attempt to send it failed and serveWS returned immediately.
func TestServeWSConnectedEventSendFailure(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	srv := NewAgentStreamServer(ServerConfig{DefaultBackend: "passthrough", Logger: logger})

	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.Listener = &failAfterHandshakeListener{Listener: ts.Listener}
	ts.Start()
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial (expected handshake to succeed): %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected connection to be closed by server without a connected event, got a message instead")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		found := false
		for _, entry := range logs.All() {
			if entry.Level == zapcore.WarnLevel && strings.Contains(entry.Message, "failed to send connected event") {
				found = true
				break
			}
		}
		if found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected a \"failed to send connected event\" warning log, never observed one")
}
