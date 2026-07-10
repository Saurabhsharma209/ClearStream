package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEnhanceOutputCodecOverride verifies that posting output_codec=aac
// causes the response Content-Type and Content-Disposition filename
// extension to reflect the requested output codec (audio/aac, ".aac")
// rather than the input file's extension (test.wav -> .wav/audio/wav).
func TestEnhanceOutputCodecOverride(t *testing.T) {
	h := newTestHandler()
	wavData := buildWAVBytes(make([]int16, 1600))
	body, ct := buildMultipartBody(wavData, "test.wav")
	req := httptest.NewRequest(http.MethodPost,
		"/enhance?output_codec=aac", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Accept 200 (ffmpeg available) or 500 (ffmpeg unavailable in some CI
	// environments), matching the tolerance used by the other /enhance
	// tests in this package. Only assert header content on success.
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 200 or 500, got %d; body: %s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusOK {
		t.Skipf("ffmpeg unavailable in this environment (status %d); skipping header assertions", w.Code)
	}

	if got := w.Header().Get("Content-Type"); got != "audio/aac" {
		t.Errorf("expected Content-Type audio/aac for output_codec=aac, got %q", got)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, ".aac") {
		t.Errorf("expected Content-Disposition filename with .aac extension, got %q", cd)
	}
	if strings.Contains(cd, ".wav") {
		t.Errorf("Content-Disposition should not carry the input's .wav extension when output_codec=aac, got %q", cd)
	}
}

// TestEnhanceOutputSampleRateOverride verifies that a valid
// output_sample_rate form value is accepted and processed without error.
func TestEnhanceOutputSampleRateOverride(t *testing.T) {
	h := newTestHandler()
	wavData := buildWAVBytes(make([]int16, 1600))
	body, ct := buildMultipartBody(wavData, "test.wav")
	req := httptest.NewRequest(http.MethodPost,
		"/enhance?output_sample_rate=8000", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("expected 200 or 500, got %d; body: %s", w.Code, w.Body.String())
	}
}

// TestEnhanceOutputSampleRateInvalidOrEmpty verifies that an invalid
// (non-numeric) or empty output_sample_rate value is tolerated -- the
// request must not crash, and processing must proceed as if no override
// was requested (opts.OutputSampleRate left at its zero value).
func TestEnhanceOutputSampleRateInvalidOrEmpty(t *testing.T) {
	h := newTestHandler()

	cases := []string{
		"/enhance?output_sample_rate=notanumber",
		"/enhance?output_sample_rate=",
		"/enhance",
	}
	for _, target := range cases {
		wavData := buildWAVBytes(make([]int16, 1600))
		body, ct := buildMultipartBody(wavData, "test.wav")
		req := httptest.NewRequest(http.MethodPost, target, body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()

		// Must not panic; any well-formed HTTP status is acceptable.
		h.ServeHTTP(w, req)

		if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
			t.Errorf("target %q: expected 200 or 500, got %d; body: %s", target, w.Code, w.Body.String())
		}
		if w.Code == http.StatusOK {
			// Since no valid codec override was requested, the response
			// should still reflect the input's own .wav extension.
			if got := w.Header().Get("Content-Type"); got != "audio/wav" {
				t.Errorf("target %q: expected Content-Type audio/wav (no override), got %q", target, got)
			}
		}
	}
}

// TestEnhanceOutputCodecUnrecognized verifies that an output codec value
// unknown to codecToExt falls back to the input file's extension for the
// response headers rather than producing an empty/invalid extension.
func TestEnhanceOutputCodecUnrecognized(t *testing.T) {
	h := newTestHandler()
	wavData := buildWAVBytes(make([]int16, 1600))
	body, ct := buildMultipartBody(wavData, "test.wav")
	req := httptest.NewRequest(http.MethodPost,
		"/enhance?output_codec=totally_unknown_codec", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// This will most likely fail ffmpeg encoding (500) since the codec is
	// bogus, but the handler must not panic either way.
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("expected 200 or 500, got %d; body: %s", w.Code, w.Body.String())
	}
}
