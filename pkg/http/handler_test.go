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

// buildWAVBytes produces a minimal valid WAV file from int16 PCM samples.
// Format: mono, 16-bit PCM, 16000 Hz sample rate.
func buildWAVBytes(samples []int16) []byte {
	dataSize := uint32(len(samples) * 2)
	fileSize := 36 + dataSize

	buf := new(bytes.Buffer)
	// RIFF chunk
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, fileSize) //nolint:errcheck
	buf.WriteString("WAVE")
	// fmt sub-chunk
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))    // chunk size
	binary.Write(buf, binary.LittleEndian, uint16(1))     // PCM
	binary.Write(buf, binary.LittleEndian, uint16(1))     // channels
	binary.Write(buf, binary.LittleEndian, uint32(16000)) // sample rate
	binary.Write(buf, binary.LittleEndian, uint32(32000)) // byte rate
	binary.Write(buf, binary.LittleEndian, uint16(2))     // block align
	binary.Write(buf, binary.LittleEndian, uint16(16))    // bits per sample
	// data sub-chunk
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, dataSize) //nolint:errcheck
	for _, s := range samples {
		binary.Write(buf, binary.LittleEndian, s) //nolint:errcheck
	}
	return buf.Bytes()
}

// TestEnhanceWithWAVFile posts a real WAV file (100ms of 440 Hz sine at 16 kHz)
// and verifies the handler returns 200 or 500 (ffmpeg may be absent in CI).
// When 200, it checks the audio Content-Type and a non-empty body.
func TestEnhanceWithWAVFile(t *testing.T) {
	const (
		numSamples = 1600 // 100 ms at 16 kHz
		sampleRate = 16000
		freq       = 440.0
		amplitude  = 5000.0
	)
	samples := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / sampleRate
		samples[i] = int16(amplitude * math.Sin(2*math.Pi*freq*t))
	}
	wavBytes := buildWAVBytes(samples)

	body, ct := buildMultipartBody(wavBytes, "test.wav")
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/enhance", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("expected 200 or 500, got %d; body: %s", w.Code, w.Body.String())
	}
	if w.Code == http.StatusOK {
		ct := w.Header().Get("Content-Type")
		if !strings.Contains(ct, "audio") && !strings.Contains(ct, "wav") {
			t.Errorf("expected audio Content-Type, got: %s", ct)
		}
		if w.Body.Len() == 0 {
			t.Error("expected non-empty response body on success")
		}
	}
}

// TestEnhanceResponseHeaders verifies that X-ClearStream-Model and
// X-ClearStream-Duration-Ms headers are set on a successful POST /enhance.
func TestEnhanceResponseHeaders(t *testing.T) {
	const numSamples = 1600
	samples := make([]int16, numSamples)
	wavBytes := buildWAVBytes(samples)

	body, ct := buildMultipartBody(wavBytes, "test.wav")
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/enhance", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Skipf("skipping header assertions: got %d (ffmpeg unavailable)", w.Code)
	}
	if got := w.Header().Get("X-ClearStream-Model"); got == "" {
		t.Error("expected X-ClearStream-Model header to be non-empty")
	}
	if got := w.Header().Get("X-ClearStream-Duration-Ms"); got == "" {
		t.Error("expected X-ClearStream-Duration-Ms header to be present")
	}
}

// TestCORSPreflightHeaders sends OPTIONS to /enhance and verifies the CORS
// response headers required for browser preflight checks.
func TestCORSPreflightHeaders(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodOptions, "/enhance", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS preflight, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected Access-Control-Allow-Origin: *, got: %s", got)
	}
	methods := w.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "POST") {
		t.Errorf("expected Access-Control-Allow-Methods to contain POST, got: %s", methods)
	}
}

// TestEnhanceStreamEndpoint posts 3200 bytes of raw PCM (silence) to
// /enhance/stream and verifies a 200 response with body length == 3200.
func TestEnhanceStreamEndpoint(t *testing.T) {
	h := newTestHandler()
	pcm := make([]byte, 3200) // silence: 10 frames × 160 samples × 2 bytes
	req := httptest.NewRequest(http.MethodPost, "/enhance/stream", bytes.NewReader(pcm))
	req.Header.Set("Content-Type", "audio/pcm")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 3200 {
		t.Errorf("expected 3200 bytes in response, got %d", w.Body.Len())
	}
}

// TestEnhanceStreamMethodNotAllowed verifies that GET /enhance/stream returns 405.
func TestEnhanceStreamMethodNotAllowed(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/enhance/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
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
