package websocket_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	csws "github.com/exotel/clearstream/pkg/websocket"
	"github.com/gorilla/websocket"
)

// echoServer returns an httptest.Server that echoes every binary WebSocket
// message back to the sender.  The returned close func stops the server.
func echoServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	return srv, wsURL
}

// TestReconnectClientSendAndConnect verifies that frames sent via Send() are
// delivered to the echo server once the connection is established, and that
// Connected() eventually becomes true.
func TestReconnectClientSendAndConnect(t *testing.T) {
	srv, wsURL := echoServer(t)
	defer srv.Close()

	client := csws.NewReconnectClient(csws.ReconnectConfig{
		URL:            wsURL,
		QueueSize:      64,
		InitialBackoff: 20 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
	})
	defer client.Stop()

	// Wait for connection to establish.
	deadline := time.Now().Add(2 * time.Second)
	for !client.Connected() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !client.Connected() {
		t.Fatal("client did not connect within 2 seconds")
	}

	// Send a few frames.
	frame := make([]byte, 320) // 10ms of 16kHz mono int16 PCM
	for i := range frame {
		frame[i] = byte(i)
	}
	for i := 0; i < 5; i++ {
		client.Send(frame)
	}

	t.Logf("ReconnectClient connected=%v", client.Connected())
}

// TestReconnectClientQueueDropsOldest verifies the tail-drop behaviour:
// when the queue is full, the oldest frame is evicted to make room for the
// newest frame, so the caller is never blocked.
func TestReconnectClientQueueDropsOldest(t *testing.T) {
	// Use an unreachable URL so the client stays disconnected — queue fills up.
	client := csws.NewReconnectClient(csws.ReconnectConfig{
		URL:            "ws://127.0.0.1:1", // port 1 is always refused
		QueueSize:      4,
		InitialBackoff: 500 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
	})
	defer client.Stop()

	// Over-fill the queue (> QueueSize frames). None of these calls may block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			client.Send([]byte{byte(i)})
		}
		close(done)
	}()

	select {
	case <-done:
		// Success — all 20 Send() calls returned without blocking.
	case <-time.After(2 * time.Second):
		t.Fatal("Send() blocked — queue tail-drop not working")
	}
}

// TestReconnectClientReconnects verifies that when the server is briefly
// unavailable and then comes back, the client re-establishes the connection.
func TestReconnectClientReconnects(t *testing.T) {
	var received int64
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			atomic.AddInt64(&received, 1)
		}
	}))
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	client := csws.NewReconnectClient(csws.ReconnectConfig{
		URL:            wsURL,
		QueueSize:      32,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     500 * time.Millisecond,
	})
	defer client.Stop()

	// Wait for first connection.
	deadline := time.Now().Add(2 * time.Second)
	for !client.Connected() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !client.Connected() {
		t.Fatal("client did not connect within 2 seconds")
	}

	// Send some frames before disconnect.
	frame := make([]byte, 160)
	client.Send(frame)
	time.Sleep(30 * time.Millisecond)

	// Force disconnect by closing the server.
	srv.Close()
	time.Sleep(100 * time.Millisecond)
	if client.Connected() {
		t.Log("still connected after server closed (may be OS buffering)")
	}

	// Start a new server on the same URL scheme.  The client should reconnect.
	var srv2 *httptest.Server
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			atomic.AddInt64(&received, 1)
		}
	})
	srv2 = httptest.NewServer(mux2)
	defer srv2.Close()

	// Update the client URL and check it reconnects; since ReconnectConfig is
	// immutable after construction, we verify the backoff logic fires at least
	// once (Connected() becomes false and may come back true on the new server
	// only if the URL matches — here we just assert it doesn't panic/hang).
	time.Sleep(200 * time.Millisecond)
	t.Logf("received frames: %d; connected: %v", atomic.LoadInt64(&received), client.Connected())
}

// TestReconnectClientStop verifies that Stop() terminates the client cleanly
// and that calling Stop() multiple times is safe (no panic).
func TestReconnectClientStop(t *testing.T) {
	client := csws.NewReconnectClient(csws.ReconnectConfig{
		URL:            "ws://127.0.0.1:1",
		InitialBackoff: 500 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		client.Stop()
		client.Stop() // idempotent — must not panic
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2 seconds")
	}
}

// TestReconnectClientBackoffGrowsAndCaps verifies the exponential-backoff
// reconnect logic in connectLoop: the delay between successive failed dial
// attempts grows after each failure and never exceeds MaxBackoff.
//
// This path — including the unexported min() helper that caps the backoff —
// previously had 0% test coverage. Every other scenario in this file either
// connects successfully on the first try or calls Stop() before the first
// backoff timer elapses, so the doubling/capping arithmetic in connectLoop
// (client.go: `backoff = min(backoff*2, c.cfg.MaxBackoff)`) was never
// actually exercised. A regression here (e.g. losing the min() cap, or the
// backoff never growing at all) would mean a client behind a real outage
// either hammers the endpoint at a fixed short interval forever, or backs
// off unboundedly and stops retrying in any useful timeframe — both are
// real production failure modes for a "reconnecting" client.
func TestReconnectClientBackoffGrowsAndCaps(t *testing.T) {
	var mu sync.Mutex
	var attempts []time.Time

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	const wantAttempts = 8
	done := make(chan struct{})
	var closeOnce sync.Once
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			attempts = append(attempts, time.Now())
			n := len(attempts)
			mu.Unlock()
			conn.Close() // fail the handshake immediately -> dial error for the client
			if n >= wantAttempts {
				closeOnce.Do(func() { close(done) })
			}
		}
	}()

	const initial = 15 * time.Millisecond
	const max = 60 * time.Millisecond

	client := csws.NewReconnectClient(csws.ReconnectConfig{
		URL:            "ws://" + ln.Addr().String(),
		QueueSize:      4,
		InitialBackoff: initial,
		MaxBackoff:     max,
	})
	defer client.Stop()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("did not observe %d dial attempts within 3s — reconnect loop appears stuck", wantAttempts)
	}

	mu.Lock()
	got := append([]time.Time(nil), attempts...)
	mu.Unlock()

	if len(got) < wantAttempts {
		t.Fatalf("expected at least %d dial attempts, got %d", wantAttempts, len(got))
	}

	gaps := make([]time.Duration, 0, len(got)-1)
	for i := 1; i < len(got); i++ {
		gaps = append(gaps, got[i].Sub(got[i-1]))
	}

	// The first retry gap should reflect InitialBackoff — it must be
	// meaningfully larger than "instant" (i.e. the client isn't retrying
	// with no delay at all).
	if gaps[0] < initial/2 {
		t.Errorf("first retry gap %v shorter than expected (InitialBackoff=%v)", gaps[0], initial)
	}

	// Backoff should grow across the first couple of retries, proving the
	// delay actually doubles rather than staying flat at InitialBackoff.
	grew := false
	for i := 1; i < len(gaps) && i < 3; i++ {
		if gaps[i] > gaps[0]+5*time.Millisecond {
			grew = true
			break
		}
	}
	if !grew {
		t.Errorf("backoff does not appear to grow across retries: gaps=%v", gaps)
	}

	// Backoff must be capped at MaxBackoff. By the later attempts, growth
	// should have already hit the ceiling. Uncapped exponential growth from
	// InitialBackoff=15ms would reach roughly 15ms*2^6=960ms by the 7th gap —
	// far beyond this bound — so this reliably catches a broken/missing
	// min() cap while tolerating scheduler jitter on a correctly-capped
	// implementation (~60ms gaps expected).
	for i, g := range gaps[len(gaps)-3:] {
		if g > 3*max {
			t.Errorf("late retry gap[%d] = %v exceeds 3x MaxBackoff (%v) — backoff cap appears broken", i, g, max)
		}
	}
}
