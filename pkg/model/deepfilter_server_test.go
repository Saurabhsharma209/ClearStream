package model

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

// makeTestLogger returns a development zap logger for tests.
func makeTestLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

// makeTestServer creates an httptest server simulating the DeepFilterNet Python server.
// healthStatus is the HTTP status for /health, enhanceStatus for /enhance.
// If enhancePayload is non-nil it is returned as the enhance response body.
func makeTestServer(t *testing.T, healthStatus, enhanceStatus int, enhancePayload []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(healthStatus)
	})
	mux.HandleFunc("/enhance", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(enhanceStatus)
		if enhancePayload != nil {
			w.Write(enhancePayload) //nolint:errcheck
		}
	})
	mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewServer(mux)
}

// encodeInt16Slice encodes []int16 as little-endian bytes (matching the server wire format).
func encodeInt16Slice(samples []int16) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}

// TestNewDeepFilterServerSuppressor_ServerReachable verifies successful construction
// when the health endpoint returns 200.
func TestNewDeepFilterServerSuppressor_ServerReachable(t *testing.T) {
	srv := makeTestServer(t, http.StatusOK, http.StatusOK, nil)
	defer srv.Close()

	s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger())
	if err != nil {
		t.Fatalf("newDeepFilterServerSuppressor: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil suppressor")
	}
	if s.Name() != "deepfilter-server" {
		t.Errorf("Name() = %q, want %q", s.Name(), "deepfilter-server")
	}
	s.Reset() // stateless no-op — must not panic
	if err := s.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

// TestNewDeepFilterServerSuppressor_ServerUnreachable_NoAutoStart verifies that
// construction fails when the server is not reachable and no autoStartPath is given.
func TestNewDeepFilterServerSuppressor_ServerUnreachable_NoAutoStart(t *testing.T) {
	s, err := newDeepFilterServerSuppressor("http://127.0.0.1:19999", "", makeTestLogger())
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
		_ = s.Close()
	}
}

// TestNewDeepFilterServerSuppressor_DefaultURL exercises the "serverURL == """ branch
// (defaults to http://127.0.0.1:7878). Expected to fail unless that port is open.
func TestNewDeepFilterServerSuppressor_DefaultURL(t *testing.T) {
	_, err := newDeepFilterServerSuppressor("", "", makeTestLogger())
	// Error is expected since no server is running on the default port in CI.
	if err == nil {
		t.Log("default URL happened to connect — CI environment has a live server?")
	}
}

// TestNewDeepFilterServerSuppressor_HealthNon200 verifies that a non-200 health
// response is treated as unreachable (no autoStartPath → error).
func TestNewDeepFilterServerSuppressor_HealthNon200(t *testing.T) {
	srv := makeTestServer(t, http.StatusServiceUnavailable, http.StatusOK, nil)
	defer srv.Close()

	_, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger())
	if err == nil {
		t.Fatal("expected error when health returns 503, got nil")
	}
}

// TestNewDeepFilterServerSuppressor_AutoStartPath_ScriptNotFound verifies auto-start
// returns an error when the script file doesn't exist on disk.
func TestNewDeepFilterServerSuppressor_AutoStartPath_ScriptNotFound(t *testing.T) {
	_, err := newDeepFilterServerSuppressor(
		"http://127.0.0.1:19998",
		"/nonexistent/path/df_server.py",
		makeTestLogger(),
	)
	if err == nil {
		t.Fatal("expected error for missing script, got nil")
	}
}

// TestDeepFilterServerSuppressor_ping_Success verifies ping on a healthy server.
func TestDeepFilterServerSuppressor_ping_Success(t *testing.T) {
	srv := makeTestServer(t, http.StatusOK, http.StatusOK, nil)
	defer srv.Close()

	s := &deepFilterServerSuppressor{
		serverURL: srv.URL,
		client:    &http.Client{Timeout: 2 * time.Second},
		logger:    makeTestLogger(),
	}
	if err := s.ping(); err != nil {
		t.Errorf("ping() on healthy server: %v", err)
	}
}

// TestDeepFilterServerSuppressor_ping_Failure verifies ping returns error when server is down.
func TestDeepFilterServerSuppressor_ping_Failure(t *testing.T) {
	s := &deepFilterServerSuppressor{
		serverURL: "http://127.0.0.1:19997",
		client:    &http.Client{Timeout: 300 * time.Millisecond},
		logger:    makeTestLogger(),
	}
	if err := s.ping(); err == nil {
		t.Error("ping() on non-existent server: expected error, got nil")
	}
}

// TestDeepFilterServerSuppressor_ping_Non200 verifies ping returns error on non-200 health.
func TestDeepFilterServerSuppressor_ping_Non200(t *testing.T) {
	srv := makeTestServer(t, http.StatusInternalServerError, http.StatusOK, nil)
	defer srv.Close()

	s := &deepFilterServerSuppressor{
		serverURL: srv.URL,
		client:    &http.Client{Timeout: 2 * time.Second},
		logger:    makeTestLogger(),
	}
	if err := s.ping(); err == nil {
		t.Error("ping() with 500 health: expected error, got nil")
	}
}

// TestDeepFilterServerSuppressor_Process_Success verifies the happy path.
func TestDeepFilterServerSuppressor_Process_Success(t *testing.T) {
	frame := []int16{100, 200, -100, -200, 0, 32767}
	responsePayload := encodeInt16Slice(frame) // echo back same samples

	srv := makeTestServer(t, http.StatusOK, http.StatusOK, responsePayload)
	defer srv.Close()

	s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer s.Close()

	out, err := s.Process(frame)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != len(frame) {
		t.Errorf("Process: got %d samples, want %d", len(out), len(frame))
	}
}

// TestDeepFilterServerSuppressor_Process_Non200 verifies graceful degradation on non-200 enhance.
func TestDeepFilterServerSuppressor_Process_Non200(t *testing.T) {
	frame := []int16{1, 2, 3, 4}
	srv := makeTestServer(t, http.StatusOK, http.StatusInternalServerError, nil)
	defer srv.Close()

	s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer s.Close()

	out, err := s.Process(frame)
	if err != nil {
		t.Fatalf("Process should not error on non-200: %v", err)
	}
	if len(out) != len(frame) {
		t.Errorf("got %d samples, want %d", len(out), len(frame))
	}
}

// TestDeepFilterServerSuppressor_Process_RequestFails verifies graceful degradation
// when the HTTP request itself fails (server closed mid-flight).
func TestDeepFilterServerSuppressor_Process_RequestFails(t *testing.T) {
	srv := makeTestServer(t, http.StatusOK, http.StatusOK, nil)

	s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer s.Close()

	// Shut down the server so the next request fails at the network level.
	srv.Close()

	frame := []int16{10, 20, 30}
	out, err := s.Process(frame)
	if err != nil {
		t.Fatalf("Process should not return error on network failure (graceful degradation): %v", err)
	}
	if len(out) != len(frame) {
		t.Errorf("got %d samples, want %d", len(out), len(frame))
	}
}

// TestDeepFilterServerSuppressor_Process_ShorterResponse verifies padding when
// the server returns fewer samples than the input frame.
func TestDeepFilterServerSuppressor_Process_ShorterResponse(t *testing.T) {
	frame := []int16{1, 2, 3, 4, 5, 6}
	shortPayload := encodeInt16Slice([]int16{10, 20, 30, 40}) // only 4 samples

	srv := makeTestServer(t, http.StatusOK, http.StatusOK, shortPayload)
	defer srv.Close()

	s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer s.Close()

	out, err := s.Process(frame)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != len(frame) {
		t.Errorf("got %d samples, want %d (zero-padding expected)", len(out), len(frame))
	}
}

// TestDeepFilterServerSuppressor_Process_LongerResponse verifies trimming when
// the server returns more samples than the input frame.
func TestDeepFilterServerSuppressor_Process_LongerResponse(t *testing.T) {
	frame := []int16{1, 2, 3}
	longPayload := encodeInt16Slice([]int16{10, 20, 30, 40, 50, 60}) // 6 samples

	srv := makeTestServer(t, http.StatusOK, http.StatusOK, longPayload)
	defer srv.Close()

	s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer s.Close()

	out, err := s.Process(frame)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != len(frame) {
		t.Errorf("got %d samples, want %d (trim expected)", len(out), len(frame))
	}
}

// TestDeepFilterServerSuppressor_Name_Reset tests Name and Reset via the interface.
func TestDeepFilterServerSuppressor_Name_Reset(t *testing.T) {
	srv := makeTestServer(t, http.StatusOK, http.StatusOK, nil)
	defer srv.Close()

	s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer s.Close()

	if got := s.Name(); got != "deepfilter-server" {
		t.Errorf("Name() = %q, want %q", got, "deepfilter-server")
	}
	s.Reset() // stateless no-op — must not panic
}

// TestDeepFilterServerSuppressor_Close_Idempotent verifies Close can be called twice safely.
func TestDeepFilterServerSuppressor_Close_Idempotent(t *testing.T) {
	srv := makeTestServer(t, http.StatusOK, http.StatusOK, nil)
	defer srv.Close()

	s, err := newDeepFilterServerSuppressor(srv.URL, "", makeTestLogger())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("first Close(): %v", err)
	}
	// Second Close should not panic or return error.
	if err := s.Close(); err != nil {
		t.Errorf("second Close(): %v", err)
	}
}

// TestNewSuppressor_DeepfilterServerBackend exercises the "deepfilter-server" branch
// in NewSuppressor (interface.go). Requires a running httptest server.
func TestNewSuppressor_DeepfilterServerBackend(t *testing.T) {
	srv := makeTestServer(t, http.StatusOK, http.StatusOK, nil)
	defer srv.Close()

	cfg := SuppressorConfig{
		Backend:   "deepfilter-server",
		ServerURL: srv.URL,
	}
	s, err := NewSuppressor(cfg)
	if err != nil {
		t.Fatalf("NewSuppressor(deepfilter-server): %v", err)
	}
	if s == nil {
		t.Fatal("got nil suppressor")
	}
	defer s.Close()
	if s.Name() != "deepfilter-server" {
		t.Errorf("Name() = %q, want deepfilter-server", s.Name())
	}
}
