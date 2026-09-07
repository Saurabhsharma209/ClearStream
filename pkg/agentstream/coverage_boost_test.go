package agentstream

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// TestHandleMessageDecodeErrors exercises every per-event-type JSON decode
// failure branch in handleMessage, plus the top-level envelope decode
// failure, none of which TestFullCallLifecycle or the other tests in
// server_test.go trigger (they all send well-formed events).
func TestHandleMessageDecodeErrors(t *testing.T) {
	srv := NewAgentStreamServer(ServerConfig{DefaultBackend: "passthrough"})
	cases := []struct {
		name    string
		data    string
		wantSub string
	}{
		{"envelope", `{"event":`, "decode envelope"},
		{"start", `{"event":"start","sample_rate":"bad"}`, "decode start event"},
		{"media", `{"event":"media","sequence_number":"bad"}`, "decode media event"},
		{"reconfigure", `{"event":"reconfigure","level":"bad"}`, "decode reconfigure event"},
		{"stop", `{"event":"stop","duration_sec":"bad"}`, "decode stop event"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			call := &callState{state: StateCreated}
			err := srv.handleMessage(nil, call, []byte(c.data))
			if err == nil {
				t.Fatalf("expected error for %s, got nil", c.name)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("error = %q, want substring %q", err.Error(), c.wantSub)
			}
		})
	}
}

// TestHandleMessageNoopAndUnknownEvents covers the dtmf/clear/mark no-op
// paths (informational events the enhancement pipeline does not act on)
// and the default branch for an event type this server does not recognise
// (logged as a warning, not treated as an error).
func TestHandleMessageNoopAndUnknownEvents(t *testing.T) {
	srv := NewAgentStreamServer(ServerConfig{DefaultBackend: "passthrough"})
	cases := []string{
		`{"event":"dtmf","digit":"5"}`,
		`{"event":"clear","reason":"barge_in"}`,
		`{"event":"mark","name":"m1"}`,
		`{"event":"totally-unknown-event"}`,
	}
	for _, data := range cases {
		call := &callState{state: StateCreated}
		if err := srv.handleMessage(nil, call, []byte(data)); err != nil {
			t.Errorf("handleMessage(%s) returned unexpected error: %v", data, err)
		}
	}
}

// TestNewAgentStreamServerDefaults locks in the zero-value defaulting
// behavior (MaxFrameBytes and DefaultBackend) that every other test in this
// package sidesteps by pre-filling ServerConfig via dialTestServer.
func TestNewAgentStreamServerDefaults(t *testing.T) {
	srv := NewAgentStreamServer(ServerConfig{})
	if srv.cfg.MaxFrameBytes != 65536 {
		t.Errorf("MaxFrameBytes = %d, want 65536", srv.cfg.MaxFrameBytes)
	}
	if srv.cfg.DefaultBackend != "passthrough" {
		t.Errorf("DefaultBackend = %q, want passthrough", srv.cfg.DefaultBackend)
	}
}

// TestNewAgentStreamServerCustomValues covers the complementary branches:
// an explicit MaxFrameBytes/DefaultBackend/Logger should all be preserved
// as-is rather than defaulted.
func TestNewAgentStreamServerCustomValues(t *testing.T) {
	logger := zap.NewNop()
	srv := NewAgentStreamServer(ServerConfig{
		MaxFrameBytes:  1024,
		DefaultBackend: "rnnoise",
		Logger:         logger,
	})
	if srv.cfg.MaxFrameBytes != 1024 {
		t.Errorf("MaxFrameBytes = %d, want 1024", srv.cfg.MaxFrameBytes)
	}
	if srv.cfg.DefaultBackend != "rnnoise" {
		t.Errorf("DefaultBackend = %q, want rnnoise", srv.cfg.DefaultBackend)
	}
	if srv.logger != logger {
		t.Error("expected the provided logger to be used instead of a default")
	}
}

// TestFirstNonEmptyCoverage covers firstNonEmpty's no-args, all-empty,
// skip-leading-empties, and first-arg-wins cases.
func TestFirstNonEmptyCoverage(t *testing.T) {
	if got := firstNonEmpty(); got != "" {
		t.Errorf("firstNonEmpty() = %q, want empty", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty(empty,empty) = %q, want empty", got)
	}
	if got := firstNonEmpty("", "b"); got != "b" {
		t.Errorf("firstNonEmpty(empty,b) = %q, want b", got)
	}
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("firstNonEmpty(a,b) = %q, want a", got)
	}
}

// TestSendMarshalError covers send's json.Marshal error branch: a channel
// value cannot be marshaled to JSON, so send must return that error before
// ever attempting a WriteMessage.
func TestSendMarshalError(t *testing.T) {
	srv := NewAgentStreamServer(ServerConfig{})
	errCh := make(chan error, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := srv.upgrader.Upgrade(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		errCh <- srv.send(conn, make(chan int))
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := <-errCh; err == nil {
		t.Fatal("expected marshal error from send, got nil")
	}
}

// TestSendWriteError covers send's WriteMessage error branch by closing the
// server-side connection before send attempts to write to it.
func TestSendWriteError(t *testing.T) {
	srv := NewAgentStreamServer(ServerConfig{})
	errCh := make(chan error, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := srv.upgrader.Upgrade(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		conn.Close()
		errCh <- srv.send(conn, ConnectedEvent{Event: EventConnected})
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := <-errCh; err == nil {
		t.Fatal("expected write error from send on a closed connection, got nil")
	}
}

// TestHandleMediaBeforeStartErrors covers handleMedia's pipeline==nil guard:
// a media event arriving before any start event must surface as an error
// event rather than panicking on a nil pipeline.
func TestHandleMediaBeforeStartErrors(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	writeEvent(t, conn, MediaEvent{Event: EventMedia, StreamSID: "STX"})

	env := readEnvelope(t, conn)
	if env.Event != EventError {
		t.Fatalf("expected error event for media before start, got %q", env.Event)
	}
}

// TestHandleMediaBadBase64Errors covers handleMedia's payload decode error
// branch: a non-base64 Payload must surface as an error event.
func TestHandleMediaBadBase64Errors(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	writeEvent(t, conn, StartEvent{
		Event:            EventStart,
		StreamSID:        "STY",
		CallSID:          "CAY",
		SampleRate:       8000,
		CustomParameters: CustomParameters{"ns_model": "passthrough"},
	})

	writeEvent(t, conn, MediaEvent{Event: EventMedia, StreamSID: "STY", Payload: "not-valid-base64!!!"})

	env := readEnvelope(t, conn)
	if env.Event != EventError {
		t.Fatalf("expected error event for bad base64 payload, got %q", env.Event)
	}
}

// TestHandleMediaPartialFrameProducesNoOutput covers handleMedia's
// outBuf.Len()==0 branch: a frame smaller than one input frame (160 bytes
// at 8kHz -- see pkg/audio.FrameSizeSamples) is buffered internally by the
// pipeline and produces nothing to emit, so handleMedia must return nil
// without calling send.
func TestHandleMediaPartialFrameProducesNoOutput(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	writeEvent(t, conn, StartEvent{
		Event:            EventStart,
		StreamSID:        "STZ",
		CallSID:          "CAZ",
		SampleRate:       8000,
		CustomParameters: CustomParameters{"ns_model": "passthrough"},
	})

	tinyFrame := make([]byte, 10)
	writeEvent(t, conn, MediaEvent{
		Event:      EventMedia,
		StreamSID:  "STZ",
		Payload:    base64.StdEncoding.EncodeToString(tinyFrame),
		SampleRate: 8000,
	})

	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected a read timeout (no message sent) for a partial frame, but got a message")
	}
}

// TestHandleStartVADVariants covers handleStart's ns_vad switch (adaptive,
// static/true, and the unset default), none of which the existing
// TestFullCallLifecycle-style tests set.
func TestHandleStartVADVariants(t *testing.T) {
	for i, vad := range []string{"adaptive", "static", "true", ""} {
		vad := vad
		t.Run(vad, func(t *testing.T) {
			conn, cleanup := dialTestServer(t, ServerConfig{})
			defer cleanup()

			if env := readEnvelope(t, conn); env.Event != EventConnected {
				t.Fatalf("expected connected event first, got %q", env.Event)
			}

			sid := "STVAD"
			params := CustomParameters{"ns_model": "passthrough"}
			if vad != "" {
				params["ns_vad"] = vad
			}
			writeEvent(t, conn, StartEvent{
				Event:            EventStart,
				StreamSID:        sid,
				CallSID:          sid,
				SampleRate:       8000,
				CustomParameters: params,
			})

			frame := make([]byte, 320)
			writeEvent(t, conn, MediaEvent{
				Event:      EventMedia,
				StreamSID:  sid,
				Payload:    base64.StdEncoding.EncodeToString(frame),
				SampleRate: 8000,
			})

			env := readEnvelope(t, conn)
			if env.Event == EventError {
				t.Fatalf("case %d (ns_vad=%q): expected no error, got error event", i, vad)
			}
		})
	}
}

// TestServeWSIgnoresNonTextFrames covers serveWS's non-text-frame branch:
// AgentStream is a JSON-over-text-frames protocol, so a binary frame must
// be logged and skipped rather than fed into handleMessage.
func TestServeWSIgnoresNonTextFrames(t *testing.T) {
	conn, cleanup := dialTestServer(t, ServerConfig{})
	defer cleanup()

	if env := readEnvelope(t, conn); env.Event != EventConnected {
		t.Fatalf("expected connected event first, got %q", env.Event)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03}); err != nil {
		t.Fatalf("write binary frame: %v", err)
	}

	// Follow up with a well-formed start event; if the binary frame had
	// wedged or crashed the read loop this would time out instead of
	// getting a clean_media-free but error-free response path.
	writeEvent(t, conn, StartEvent{
		Event:            EventStart,
		StreamSID:        "STBIN",
		CallSID:          "CABIN",
		SampleRate:       8000,
		CustomParameters: CustomParameters{"ns_model": "passthrough"},
	})

	frame := make([]byte, 320)
	writeEvent(t, conn, MediaEvent{
		Event:      EventMedia,
		StreamSID:  "STBIN",
		Payload:    base64.StdEncoding.EncodeToString(frame),
		SampleRate: 8000,
	})

	env := readEnvelope(t, conn)
	if env.Event != EventCleanMedia {
		t.Fatalf("expected clean_media after ignored binary frame, got %q", env.Event)
	}
}
