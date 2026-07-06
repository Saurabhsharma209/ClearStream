package model

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

// closingErrSuppressor is a Suppressor whose Close() always returns an error.
// Used to exercise the error-propagation path in SuppressorPool.Close().
type closingErrSuppressor struct {
	Passthrough
}

func (c *closingErrSuppressor) Close() error {
	return errors.New("deliberate close error")
}

func (c *closingErrSuppressor) Name() string { return "closingerr" }

// TestPassthrough_Reset_Coverage exercises Reset() on Passthrough directly.
// The empty-body function needs a direct struct-type call to register coverage.
func TestPassthrough_Reset_Coverage(t *testing.T) {
	p := &Passthrough{}
	p.Reset()
	p.Reset()

	frame := []int16{1, 2, 3}
	out, err := p.Process(frame)
	if err != nil {
		t.Fatalf("Process after Reset: %v", err)
	}
	if len(out) != len(frame) {
		t.Fatalf("Process after Reset: got %d samples, want %d", len(out), len(frame))
	}
	for i, v := range frame {
		if out[i] != v {
			t.Errorf("out[%d] = %d, want %d", i, out[i], v)
		}
	}
}

// TestDeepFilterServer_Reset_Coverage exercises Reset() on deepFilterServerSuppressor.
func TestDeepFilterServer_Reset_Coverage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &deepFilterServerSuppressor{
		serverURL: srv.URL,
		client:    &http.Client{Timeout: 2 * time.Second},
		logger:    makeTestLogger(),
	}
	s.Reset()
	s.Reset()
}

// TestDeepFilterServer_Close_WithCmd exercises the cmd != nil branch in Close().
func TestDeepFilterServer_Close_WithCmd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep process: %v", err)
	}

	s := &deepFilterServerSuppressor{
		serverURL: srv.URL,
		client:    &http.Client{Timeout: 2 * time.Second},
		logger:    makeTestLogger(),
		cmd:       cmd,
	}

	if err := s.Close(); err != nil {
		t.Errorf("Close() with cmd: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close() after cmd: %v", err)
	}
}

// TestNewRNNoiseONNX_Stub covers rnnoise_onnx_stub.go (0% coverage).
func TestNewRNNoiseONNX_Stub(t *testing.T) {
	logger := makeTestLogger()
	s, err := NewRNNoiseONNX("/any/path/model.onnx", logger)
	if err == nil {
		if s != nil {
			s.Close()
		}
		t.Fatal("NewRNNoiseONNX without onnx build tag: expected error, got nil")
	}
	if s != nil {
		t.Errorf("NewRNNoiseONNX: expected nil suppressor, got %T", s)
	}
}

// TestNewSuppressor_RNNoiseONNX_WithPath exercises the rnnoise-onnx + ModelPath branch.
func TestNewSuppressor_RNNoiseONNX_WithPath(t *testing.T) {
	cfg := SuppressorConfig{Backend: "rnnoise-onnx", ModelPath: "/tmp/fake_model.onnx"}
	s, err := NewSuppressor(cfg)
	if err == nil && s != nil {
		s.Close()
	}
	// Without -tags onnx, error is expected. Either outcome is fine for coverage.
}

// TestNewSuppressorPool_InitError covers the error path in NewSuppressorPool
// when NewSuppressor fails for one of the pool entries.
func TestNewSuppressorPool_InitError(t *testing.T) {
	cfg := SuppressorConfig{Backend: "rnnoise-onnx", ModelPath: ""}
	pool, err := NewSuppressorPool(cfg, 2)
	if err == nil {
		if pool != nil {
			pool.Close()
		}
		t.Fatal("NewSuppressorPool with broken config: expected error, got nil")
	}
	if pool != nil {
		t.Errorf("expected nil pool on init error, got non-nil")
	}
}

// TestSuppressorPool_Close_ErrorPropagation exercises the firstErr path in Close()
// when a pooled suppressor's Close() returns an error.
func TestSuppressorPool_Close_ErrorPropagation(t *testing.T) {
	errSup := &closingErrSuppressor{}
	p := &SuppressorPool{
		pool: make(chan Suppressor, 1),
		cfg:  SuppressorConfig{Backend: "passthrough"},
		size: 1,
	}
	p.pool <- errSup

	if err := p.Close(); err == nil {
		t.Error("SuppressorPool.Close(): expected error from closingErrSuppressor, got nil")
	}
}

// TestMockSuppressor_ProcessBatch_MultiFrame verifies ProcessBatch applies
// gain to every frame and increments ProcessCalls once per frame.
func TestMockSuppressor_ProcessBatch_MultiFrame(t *testing.T) {
	m := &MockSuppressor{Gain: 2.0}

	frames := [][]int16{
		{100, 200, 300},
		{-100, -200, -300},
		{0, 500, 1000},
	}
	out, err := m.ProcessBatch(frames)
	if err != nil {
		t.Fatalf("ProcessBatch: unexpected error: %v", err)
	}
	if len(out) != len(frames) {
		t.Fatalf("ProcessBatch: got %d frames, want %d", len(out), len(frames))
	}
	want0 := []int16{200, 400, 600}
	for i, v := range want0 {
		if out[0][i] != v {
			t.Errorf("frame[0][%d] = %d, want %d", i, out[0][i], v)
		}
	}
	want1 := []int16{-200, -400, -600}
	for i, v := range want1 {
		if out[1][i] != v {
			t.Errorf("frame[1][%d] = %d, want %d", i, out[1][i], v)
		}
	}
	if m.ProcessCalls != len(frames) {
		t.Errorf("ProcessCalls = %d, want %d", m.ProcessCalls, len(frames))
	}
}

// TestMockSuppressor_ProcessBatch_Empty verifies nil batch returns empty output.
func TestMockSuppressor_ProcessBatch_Empty(t *testing.T) {
	m := NewMockSuppressor()
	out, err := m.ProcessBatch(nil)
	if err != nil {
		t.Fatalf("ProcessBatch(nil): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("ProcessBatch(nil): got %d frames, want 0", len(out))
	}
	if m.ProcessCalls != 0 {
		t.Errorf("ProcessCalls = %d, want 0", m.ProcessCalls)
	}
}

// TestBatchWrapper_ProcessBatch_MidError verifies partial-result semantics:
// processed frames before the error are returned, remaining frames fall back to originals.
func TestBatchWrapper_ProcessBatch_MidError(t *testing.T) {
	inner := &errSuppressor{failAt: 1}
	bw := &BatchWrapper{s: inner}

	frames := [][]int16{
		{10, 20},
		{30, 40},
		{50, 60},
	}
	out, err := bw.ProcessBatch(frames)
	if err == nil {
		t.Fatal("expected error from ProcessBatch, got nil")
	}
	if len(out) != len(frames) {
		t.Fatalf("output length = %d, want %d", len(out), len(frames))
	}
	if out[0] == nil {
		t.Error("out[0] should be processed (non-nil)")
	}
	for i := 1; i < len(frames); i++ {
		if len(out[i]) != len(frames[i]) {
			t.Errorf("out[%d]: got len %d, want %d (original)", i, len(out[i]), len(frames[i]))
		}
	}
}
