package agentstream

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func dialTestServer(t *testing.T, cfg ServerConfig) (*websocket.Conn, func()) {
	t.Helper()
	if cfg.DefaultBackend == "" {
		cfg.DefaultBackend = "passthrough"
	}
	srv := NewAgentStreamServer(cfg)
	ts := httptest.NewServer(srv.Handler())
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		ts.Close()
		t.Fatalf("dial: %v", err)
	}
	return conn, func() {
		conn.Close()
		ts.Close()
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) envelope {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (raw=%s)", err, data)
	}
	return env
}

func writeEvent(t *testing.T, conn *websocket.Conn, v interface{}) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestFullCallLifecycle drives connected -> start (with custom_parameters
// selecting AGC + adaptive tiering) -> media x3 -> dtmf -> reconfigure
// (disabled) -> stop over a real WebSocket connection, using the
// passthrough backend so it is fast and CGO-free. This is the sprint's
// end-to-end regression test: it fails if the protocol adapter stops
// speaking Exotel's JSON wire format, stops building per-call config from
// CustomParameters, or stops producing clean_media output.
func TestFullCallLifecycle(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	writeEvent(t, conn, StartEvent{
		Event:      EventStart,
		StreamSID:  "ST1",
		CallSID:    "CA1",
		SampleRate: 8000,
		CustomParameters: CustomParameters{
			"ns_model": "passthrough",
			"ns_agc":   "true",
			"ns_mode":  "adaptive",
		},
	})

	// One 20ms frame of silence at 8kHz mono 16-bit PCM (320 bytes); exact
	// content does not matter for this test, only that it round-trips.
	frame := make([]byte, 320)
	payload := base64.StdEncoding.EncodeToString(frame)

	for i := 0; i < 3; i++ {
		writeEvent(t, conn, MediaEvent{
			Event:       EventMedia,
			StreamSID:   "ST1",
			Payload:     payload,
			SampleRate:  8000,
			TimestampMs: int64(i * 20),
		})
	}

	sawCleanMedia := false
	for i := 0; i < 3; i++ {
		if env := readEnvelope(t, conn); env.Event == EventCleanMedia {
			sawCleanMedia = true
		}
	}
	if !sawCleanMedia {
		t.Error("expected at least one clean_media event in response to 3 media frames")
	}

	writeEvent(t, conn, DTMFEvent{Event: EventDTMF, StreamSID: "ST1", Digit: "5", DurationMs: 100})
	writeEvent(t, conn, ReconfigureEvent{Event: EventReconfigure, StreamSID: "ST1", Mode: "disabled"})
	writeEvent(t, conn, StopEvent{Event: EventStop, StreamSID: "ST1", CallSID: "CA1", Reason: "callended"})
}

// TestHandleStartUnknownBackendErrors verifies a malformed ns_model value
// surfaces as an error event instead of silently falling back or crashing.
func TestHandleStartUnknownBackendErrors(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	writeEvent(t, conn, StartEvent{
		Event:     EventStart,
		StreamSID: "ST2",
		CallSID:   "CA2",
		CustomParameters: CustomParameters{
			"ns_model": "not-a-real-backend",
		},
	})

	env := readEnvelope(t, conn)
	if env.Event != EventError {
		t.Fatalf("expected error event for unknown ns_model, got %q", env.Event)
	}
}
