package model

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// deepFilterServerSuppressor sends PCM frames to a local Python DeepFilterNet
// HTTP server (scripts/df_server.py) and returns the enhanced audio.
//
// This is the primary integration path for DeepFilterNet since the model's
// STFT preprocessing uses Rust extensions that cannot be exported to ONNX
// without reimplementing the filterbank in pure PyTorch.
//
// Usage in Config:
//
//	SuppressorConfig{
//	    Backend:       "deepfilter-server",
//	    ServerURL:     "http://127.0.0.1:7878",   // optional, default
//	    AutoStartPath: "scripts/df_server.py",     // optional: auto-start if not running
//	}
//
// To start the server manually:
//
//	python3 scripts/df_server.py --port 7878
type deepFilterServerSuppressor struct {
	mu        sync.Mutex
	serverURL string
	client    *http.Client
	logger    *zap.Logger
	cmd       *exec.Cmd // non-nil if we auto-started the server
}

// newDeepFilterServerSuppressor creates a suppressor that calls the Python server.
// If autoStartPath is set and the server isn't reachable, it auto-starts df_server.py.
func newDeepFilterServerSuppressor(serverURL, autoStartPath string, logger *zap.Logger) (Suppressor, error) {
	if serverURL == "" {
		serverURL = "http://127.0.0.1:7878"
	}

	s := &deepFilterServerSuppressor{
		serverURL: serverURL,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		logger: logger,
	}

	// Check if server is already running
	if err := s.ping(); err != nil {
		if autoStartPath == "" {
			return nil, fmt.Errorf(
				"deepfilter-server: server not reachable at %s. "+
					"Start it with: python3 scripts/df_server.py\n"+
					"Or set AutoStartPath to auto-start.", serverURL)
		}

		// Auto-start the Python server
		logger.Info("starting DeepFilterNet server",
			zap.String("script", autoStartPath),
			zap.String("url", serverURL))
		if err := s.startServer(autoStartPath); err != nil {
			return nil, fmt.Errorf("deepfilter-server: auto-start failed: %w", err)
		}
	}

	logger.Info("DeepFilterNet server ready", zap.String("url", serverURL))
	return s, nil
}

// startServer launches df_server.py as a subprocess and waits until it's ready.
func (s *deepFilterServerSuppressor) startServer(scriptPath string) error {
	// Resolve to absolute path
	if !filepath.IsAbs(scriptPath) {
		if abs, err := filepath.Abs(scriptPath); err == nil {
			scriptPath = abs
		}
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return fmt.Errorf("script not found: %s", scriptPath)
	}

	cmd := exec.Command("python3", scriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start python3: %w", err)
	}
	s.cmd = cmd

	// Wait up to 30s for the server to become ready (model loading takes ~5s)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if err := s.ping(); err == nil {
			return nil
		}
	}
	_ = cmd.Process.Kill()
	return fmt.Errorf("server did not become ready within 30s")
}

func (s *deepFilterServerSuppressor) ping() error {
	resp, err := s.client.Get(s.serverURL + "/health")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	return nil
}

// Process sends a 16kHz mono int16 PCM frame to the server and returns the enhanced frame.
// The server handles 16kHz→48kHz resampling, DeepFilterNet inference, and 48kHz→16kHz resampling.
func (s *deepFilterServerSuppressor) Process(frame []int16) ([]int16, error) {
	// Encode []int16 → little-endian bytes
	buf := make([]byte, len(frame)*2)
	for i, s16 := range frame {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s16))
	}

	resp, err := s.client.Post(
		s.serverURL+"/enhance",
		"application/octet-stream",
		bytes.NewReader(buf),
	)
	if err != nil {
		// Graceful degradation: return original frame on server error
		s.logger.Warn("deepfilter-server request failed, passing through",
			zap.Error(err))
		return frame, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.logger.Warn("deepfilter-server non-200 response, passing through",
			zap.Int("status", resp.StatusCode))
		return frame, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return frame, nil
	}

	// Decode bytes → []int16
	result := make([]int16, len(body)/2)
	for i := range result {
		result[i] = int16(binary.LittleEndian.Uint16(body[i*2:]))
	}

	// Pad or trim to match input length (resampling can cause ±1 sample difference)
	if len(result) < len(frame) {
		padded := make([]int16, len(frame))
		copy(padded, result)
		return padded, nil
	}
	return result[:len(frame)], nil
}

func (s *deepFilterServerSuppressor) Name() string { return "deepfilter-server" }
func (s *deepFilterServerSuppressor) Reset()       {} // stateless: server holds no per-call state

func (s *deepFilterServerSuppressor) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Gracefully stop the auto-started server (if any)
	if s.cmd != nil && s.cmd.Process != nil {
		_, _ = s.client.Post(s.serverURL+"/shutdown", "application/json", nil)
		time.Sleep(200 * time.Millisecond)
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
		s.cmd = nil
	}
	return nil
}
