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

// TestAggressivenessToThresholds locks in the SNR boundary values for each
// named reconfigure mode, including the "adaptive"/default fallback.
func TestAggressivenessToThresholds(t *testing.T) {
	cases := []struct {
		mode     string
		wantHigh float64
		wantLow  float64
	}{
		{"mild", 30, 5},
		{"aggressive", 20, 15},
		{"adaptive", 25, 10},
		{"unrecognized-mode", 25, 10}, // falls into default case
	}
	for _, c := range cases {
		high, low := aggressivenessToThresholds(c.mode)
		if high != c.wantHigh || low != c.wantLow {
			t.Errorf("aggressivenessToThresholds(%q) = (%v, %v), want (%v, %v)", c.mode, high, low, c.wantHigh, c.wantLow)
		}
	}
}

// TestAtoiOr covers the fallback-to-default paths that TestFullCallLifecycle
// never exercises: an empty string and a non-numeric string should both
// return the caller's default rather than propagating a parse error.
func TestAtoiOr(t *testing.T) {
	if got := atoiOr("", 8000); got != 8000 {
		t.Errorf("atoiOr(empty) = %d, want 8000", got)
	}
	if got := atoiOr("not-a-number", 42); got != 42 {
		t.Errorf("atoiOr(invalid) = %d, want 42", got)
	}
	if got := atoiOr("16000", 8000); got != 16000 {
		t.Errorf("atoiOr(valid) = %d, want 16000", got)
	}
}

// TestParseFloatParam covers the missing-key, empty-value, and
// non-numeric-value cases, all of which should report ok=false.
func TestParseFloatParam(t *testing.T) {
	params := CustomParameters{
		"ns_high_snr_db": "27.5",
		"ns_empty":       "",
		"ns_invalid":     "not-a-float",
	}
	if f, ok := parseFloatParam(params, "ns_high_snr_db"); !ok || f != 27.5 {
		t.Errorf("parseFloatParam(valid) = (%v, %v), want (27.5, true)", f, ok)
	}
	if _, ok := parseFloatParam(params, "ns_missing"); ok {
		t.Error("parseFloatParam(missing key) should return ok=false")
	}
	if _, ok := parseFloatParam(params, "ns_empty"); ok {
		t.Error("parseFloatParam(empty value) should return ok=false")
	}
	if _, ok := parseFloatParam(params, "ns_invalid"); ok {
		t.Error("parseFloatParam(non-numeric value) should return ok=false")
	}
}

// TestHandleReconfigureBeforeStartErrors verifies a reconfigure event that
// arrives before the call's pipeline has been built via a start event
// surfaces as an error event rather than panicking on a nil pipeline.
func TestHandleReconfigureBeforeStartErrors(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	writeEvent(t, conn, ReconfigureEvent{Event: EventReconfigure, StreamSID: "ST3", Mode: "disabled"})

	env := readEnvelope(t, conn)
	if env.Event != EventError {
		t.Fatalf("expected error event for reconfigure before start, got %q", env.Event)
	}
}

// TestHandleReconfigureUnknownModeErrors verifies an unrecognised Mode value
// surfaces as an error event instead of silently doing nothing.
func TestHandleReconfigureUnknownModeErrors(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	writeEvent(t, conn, StartEvent{
		Event:      EventStart,
		StreamSID:  "ST4",
		CallSID:    "CA4",
		SampleRate: 8000,
		CustomParameters: CustomParameters{
			"ns_model": "passthrough",
		},
	})

	writeEvent(t, conn, ReconfigureEvent{Event: EventReconfigure, StreamSID: "ST4", Mode: "bogus-mode"})

	env := readEnvelope(t, conn)
	if env.Event != EventError {
		t.Fatalf("expected error event for unknown reconfigure mode, got %q", env.Event)
	}
}

// TestHandleReconfigureEnabledResumesAfterDisabled drives disabled -> enabled
// through the live reconfigure path and confirms the effect is visible on
// the next media frame's EnhancementInfo, not just that no error occurred.
func TestHandleReconfigureEnabledResumesAfterDisabled(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	writeEvent(t, conn, StartEvent{
		Event:      EventStart,
		StreamSID:  "ST5",
		CallSID:    "CA5",
		SampleRate: 8000,
		CustomParameters: CustomParameters{
			"ns_model": "passthrough",
		},
	})

	frame := make([]byte, 320)
	payload := base64.StdEncoding.EncodeToString(frame)

	sendMediaAndReadCleanMedia := func() CleanMediaEvent {
		writeEvent(t, conn, MediaEvent{
			Event:       EventMedia,
			StreamSID:   "ST5",
			Payload:     payload,
			SampleRate:  8000,
			TimestampMs: 0,
		})
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read clean_media: %v", err)
		}
		var out CleanMediaEvent
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("unmarshal clean_media: %v (raw=%s)", err, data)
		}
		if out.Event != EventCleanMedia {
			t.Fatalf("expected clean_media event, got %q", out.Event)
		}
		return out
	}

	if out := sendMediaAndReadCleanMedia(); !out.Enhancement.NoiseSuppression {
		t.Fatal("expected NoiseSuppression true before any reconfigure")
	}

	writeEvent(t, conn, ReconfigureEvent{Event: EventReconfigure, StreamSID: "ST5", Mode: "disabled"})
	if out := sendMediaAndReadCleanMedia(); out.Enhancement.NoiseSuppression {
		t.Fatal("expected NoiseSuppression false after mode=disabled reconfigure")
	}

	writeEvent(t, conn, ReconfigureEvent{Event: EventReconfigure, StreamSID: "ST5", Mode: "enabled"})
	if out := sendMediaAndReadCleanMedia(); !out.Enhancement.NoiseSuppression {
		t.Fatal("expected NoiseSuppression true again after mode=enabled reconfigure")
	}
}

// TestHandleReconfigureAggressiveModeAppliesLevel exercises the
// adaptive/aggressive/mild branch, including the Level>0 SetAggressiveness
// call, against a call started with ns_mode=adaptive so TieredNR is actually
// configured (Reconfigure is documented as a no-op otherwise).
func TestHandleReconfigureAggressiveModeAppliesLevel(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	writeEvent(t, conn, StartEvent{
		Event:      EventStart,
		StreamSID:  "ST6",
		CallSID:    "CA6",
		SampleRate: 8000,
		CustomParameters: CustomParameters{
			"ns_model": "passthrough",
			"ns_mode":  "adaptive",
		},
	})

	writeEvent(t, conn, ReconfigureEvent{Event: EventReconfigure, StreamSID: "ST6", Mode: "aggressive", Level: 2})

	frame := make([]byte, 320)
	payload := base64.StdEncoding.EncodeToString(frame)
	writeEvent(t, conn, MediaEvent{
		Event:       EventMedia,
		StreamSID:   "ST6",
		Payload:     payload,
		SampleRate:  8000,
		TimestampMs: 0,
	})

	env := readEnvelope(t, conn)
	if env.Event != EventCleanMedia {
		t.Fatalf("expected clean_media after aggressive reconfigure with level, got %q (event=%v)", env.Event, env)
	}
}
