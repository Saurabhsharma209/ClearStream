package clearstream_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/exotel/clearstream"
)

// TestSDKLifecycle verifies the full create → use → close lifecycle.
func TestSDKLifecycle(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New(DefaultConfig()) failed: %v", err)
	}

	if cs.Pipeline() == nil {
		t.Error("Pipeline() returned nil")
	}

	if cs.NewHTTPHandler() == nil {
		t.Error("NewHTTPHandler() returned nil")
	}

	if err := cs.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}

	// Second Close() must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("second Close() panicked: %v", r)
		}
	}()
	cs.Close() //nolint:errcheck
}

// buildSilencePCM returns n bytes of silence (all-zero raw PCM).
func buildSilencePCM(n int) []byte {
	return make([]byte, n)
}

// buildMultipartPCM wraps raw PCM bytes in a multipart/form-data body
// using the "audio" field name expected by the handler.
func buildMultipartPCM(pcm []byte, filename string) (*bytes.Buffer, string) {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, _ := w.CreateFormFile("audio", filename)
	fw.Write(pcm) //nolint:errcheck
	w.Close()
	return body, w.FormDataContentType()
}

// TestSDKHTTPEndToEnd exercises the full HTTP stack through httptest.Server.
func TestSDKHTTPEndToEnd(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close() //nolint:errcheck

	srv := httptest.NewServer(cs.NewHTTPHandler())
	defer srv.Close()

	// GET /health → 200 with "ok" in body.
	t.Run("health", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), "ok") {
			t.Errorf("body missing 'ok': %s", string(b))
		}
	})

	// GET /info → 200 or 404 (graceful either way).
	t.Run("info", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/info")
		if err != nil {
			t.Fatalf("GET /info: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 200 or 404, got %d", resp.StatusCode)
		}
	})

	// GET /metrics/prometheus → 200 with clearstream_ prefix.
	t.Run("prometheus", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/metrics/prometheus")
		if err != nil {
			t.Fatalf("GET /metrics/prometheus: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), "clearstream_") {
			t.Errorf("body missing 'clearstream_': %s", string(b))
		}
	})

	// POST /enhance with 3200 bytes of silence → must not 500 due to SDK panic.
	// Accept 200 (success) or 500 (ffmpeg unavailable in test env), not anything else.
	t.Run("enhance_silence", func(t *testing.T) {
		pcm := buildSilencePCM(3200)
		body, ct := buildMultipartPCM(pcm, "silence.wav")

		resp, err := http.Post(srv.URL+"/enhance", ct, body)
		if err != nil {
			t.Fatalf("POST /enhance: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected 200 or 500, got %d", resp.StatusCode)
		}
	})
}

// TestSDKValidationIntegration verifies New() enforces config validation.
func TestSDKValidationIntegration(t *testing.T) {
	// Invalid config: SampleRate=1 is below the valid range [8000, 48000].
	cfg := clearstream.DefaultConfig()
	cfg.SampleRate = 1
	_, err := clearstream.New(cfg)
	if err == nil {
		t.Fatal("New() with SampleRate=1 should return an error")
	}
	if !strings.Contains(err.Error(), "SampleRate") {
		t.Errorf("error message should mention SampleRate, got: %v", err)
	}

	// Valid config must succeed.
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New(DefaultConfig()) should succeed, got: %v", err)
	}
	cs.Close() //nolint:errcheck
}

// TestSDKConcurrentHTTP verifies concurrent GET /health requests are race-free.
func TestSDKConcurrentHTTP(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close() //nolint:errcheck

	srv := httptest.NewServer(cs.NewHTTPHandler())
	defer srv.Close()

	const workers = 10
	results := make([]int, workers)
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			resp, err := http.Get(srv.URL + "/health")
			if err != nil {
				results[i] = -1
				return
			}
			resp.Body.Close()
			results[i] = resp.StatusCode
		}()
	}
	wg.Wait()

	for i, code := range results {
		if code != http.StatusOK {
			t.Errorf("goroutine %d: expected 200, got %d", i, code)
		}
	}
}
