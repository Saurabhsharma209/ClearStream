package http_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cshttp "github.com/exotel/clearstream/pkg/http"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

func newTestHandler() *cshttp.Handler {
	return cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
}

func TestHealthEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "ok") {
		t.Errorf("expected ok in body, got: %s", body)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestNotFound(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestEnhanceMissingField(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/enhance",
		strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// Should fail with 400 (missing audio field).
	if w.Code == http.StatusOK {
		t.Error("expected non-200 for missing audio field")
	}
}

// buildSinePCM generates 10 frames × 160 samples of 440 Hz sine wave
// at 16 kHz, little-endian int16, amplitude 8000.
func buildSinePCM() []byte {
	const (
		frames          = 10
		samplesPerFrame = 160
		sampleRate      = 16000
		freq            = 440.0
		amplitude       = 8000.0
	)
	total := frames * samplesPerFrame
	buf := make([]byte, total*2)
	for i := 0; i < total; i++ {
		t := float64(i) / sampleRate
		sample := int16(amplitude * math.Sin(2*math.Pi*freq*t))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(sample))
	}
	return buf
}

// buildMultipartBody creates a multipart/form-data body with the "audio" field.
func buildMultipartBody(pcm []byte, filename string) (body *bytes.Buffer, contentType string) {
	body = &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, _ := w.CreateFormFile("audio", filename)
	fw.Write(pcm) //nolint:errcheck
	w.Close()
	return body, w.FormDataContentType()
}

// TestEnhanceEndpointSyntheticPCM posts a synthetic PCM payload as a WAV file
// and verifies the handler returns 200 without panicking.
// Because the passthrough suppressor is used and ffmpeg may not be available
// in CI, we accept either 200 (success) or 500 (ffmpeg unavailable).
func TestEnhanceEndpointSyntheticPCM(t *testing.T) {
	h := newTestHandler()
	pcm := buildSinePCM()
	body, ct := buildMultipartBody(pcm, "test.wav")

	req := httptest.NewRequest(http.MethodPost, "/enhance", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	// Must not panic.
	h.ServeHTTP(w, req)

	// Accept 200 (ffmpeg available) or 500 (ffmpeg not in PATH in test env).
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("expected 200 or 500, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestEnhanceEndpointEmpty posts an empty body and verifies the handler
// returns a valid HTTP status without panicking.
func TestEnhanceEndpointEmpty(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/enhance", strings.NewReader(""))
	req.Header.Set("Content-Type", "audio/pcm")
	w := httptest.NewRecorder()

	// Must not panic.
	h.ServeHTTP(w, req)

	if w.Code < 100 || w.Code > 599 {
		t.Errorf("got invalid HTTP status %d", w.Code)
	}
}

func TestHealthEndpointJSON(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status"`) || !strings.Contains(body, `"uptime_sec"`) {
		t.Errorf("expected JSON health with uptime_sec, got: %s", body)
	}
}

func TestInfoEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "sample_rate") || !strings.Contains(body, "supported_codecs") {
		t.Errorf("expected info JSON, got: %s", body)
	}
}

func TestCORSHeaders(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected CORS header *, got: %s", got)
	}
}

func TestOPTIONSPreflight(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodOptions, "/enhance", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS, got %d", w.Code)
	}
}

// TestPrometheusMetricsEndpoint verifies GET /metrics/prometheus returns 200
// and a body containing the clearstream_ metric prefix.
func TestPrometheusMetricsEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/metrics/prometheus", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "clearstream_") {
		t.Errorf("expected clearstream_ metrics in body, got: %s", w.Body.String())
	}
}
