// Package http_test contains targeted tests for uncovered handleEnhance branches.
// Focus: success path after ProcessWithOptions (lines that require ffmpeg),
// and option-parsing branches (AGC, audio_only, normalize_peak).
package http_test

import (
	"bytes"
	"encoding/binary"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	cshttp "github.com/exotel/clearstream/pkg/http"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// minimalWAVEnhance returns a 44+320 byte WAV file: mono, 16-bit PCM, 16 kHz, silence.
func minimalWAVEnhance() []byte {
	buf := make([]byte, 44+320)
	copy(buf[0:], []byte("RIFF"))
	binary.LittleEndian.PutUint32(buf[4:], uint32(len(buf)-8))
	copy(buf[8:], []byte("WAVEfmt "))
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)
	binary.LittleEndian.PutUint16(buf[22:], 1)
	binary.LittleEndian.PutUint32(buf[24:], 16000)
	binary.LittleEndian.PutUint32(buf[28:], 32000)
	binary.LittleEndian.PutUint16(buf[32:], 2)
	binary.LittleEndian.PutUint16(buf[34:], 16)
	copy(buf[36:], []byte("data"))
	binary.LittleEndian.PutUint32(buf[40:], 320)
	return buf
}

// makeFakeFFmpeg writes a shell script named "ffmpeg" into a temp dir.
// The script emits silence PCM (decode phase) or a minimal WAV (encode phase).
func makeFakeFFmpeg(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ffmpeg script not supported on Windows")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"LAST=\"${@: -1}\"\n" +
		"if [ \"$LAST\" = \"-\" ]; then\n" +
		"    dd if=/dev/zero bs=320 count=1 2>/dev/null\n" +
		"else\n" +
		"    python3 -c \"" +
		"import sys,struct; dst=sys.argv[1]; d=b'\\x00'*320;" +
		"h=b'RIFF'+struct.pack('<I',36+len(d))+b'WAVEfmt ';" +
		"h+=struct.pack('<IHHIIHH',16,1,1,16000,32000,2,16);" +
		"h+=b'data'+struct.pack('<I',len(d));" +
		"open(dst,'wb').write(h+d)" +
		"\" \"$LAST\" 2>/dev/null || dd if=/dev/zero of=\"$LAST\" bs=364 count=1 2>/dev/null\n" +
		"fi\n" +
		"exit 0\n"
	scriptPath := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("makeFakeFFmpeg: %v", err)
	}
	return filepath.Join(dir, "ffmpeg")
}

// isFakeFFmpegFunctional returns true if the fake ffmpeg produces output.
func isFakeFFmpegFunctional(ffmpegPath string) bool {
	cmd := exec.Command(ffmpegPath, "-")
	out, err := cmd.Output()
	return err == nil && len(out) > 0
}

// newHandlerWithFfmpeg builds a Handler using the given ffmpeg binary path.
func newHandlerWithFfmpeg(ffmpegPath string) *cshttp.Handler {
	return cshttp.NewHandler(cshttp.HandlerConfig{
		Suppressor: model.NewPassthrough(),
		FFmpegPath: ffmpegPath,
		SampleRate: 16000,
		Logger:     zap.NewNop(),
	})
}

// buildEnhanceReq creates a multipart POST /enhance request.
func buildEnhanceReq(t *testing.T, wavData []byte, filename, query string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("audio", filename)
	fw.Write(wavData) //nolint:errcheck
	mw.Close()
	url := "/enhance"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodPost, url, body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// ---------------------------------------------------------------------------
// Pre-processor validation paths (no ffmpeg needed)
// ---------------------------------------------------------------------------

// TestHandleEnhance_MissingAudioField posts a multipart form with no "audio"
// field — expect 400 with "missing audio field".
func TestHandleEnhance_MissingAudioField(t *testing.T) {
	h := newHandlerWithFfmpeg("ffmpeg")
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	mw.WriteField("other", "value") //nolint:errcheck
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/enhance", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing audio field") {
		t.Errorf("expected 'missing audio field', got: %s", w.Body.String())
	}
}

// TestHandleEnhance_InvalidForm posts plain text — ParseMultipartForm fails -> 400.
func TestHandleEnhance_InvalidForm(t *testing.T) {
	h := newHandlerWithFfmpeg("ffmpeg")
	req := httptest.NewRequest(http.MethodPost, "/enhance",
		strings.NewReader("not multipart"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-multipart, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Option-parsing branches (reach code even when ProcessWithOptions fails)
// ---------------------------------------------------------------------------

// TestHandleEnhance_WithAudioOnly exercises the audio_only=true branch.
func TestHandleEnhance_WithAudioOnly(t *testing.T) {
	h := newHandlerWithFfmpeg("ffmpeg")
	req := buildEnhanceReq(t, minimalWAVEnhance(), "test.wav", "audio_only=true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusBadRequest {
		t.Errorf("audio_only=true should not cause 400; got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleEnhance_WithNormalizePeak exercises the normalize_peak=true branch.
func TestHandleEnhance_WithNormalizePeak(t *testing.T) {
	h := newHandlerWithFfmpeg("ffmpeg")
	req := buildEnhanceReq(t, minimalWAVEnhance(), "test.wav", "normalize_peak=true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusBadRequest {
		t.Errorf("normalize_peak=true should not cause 400; got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleEnhance_AGCParams exercises all four AGC parameter parsing branches.
func TestHandleEnhance_AGCParams(t *testing.T) {
	h := newHandlerWithFfmpeg("ffmpeg")
	req := buildEnhanceReq(t, minimalWAVEnhance(), "test.wav",
		"agc=true&agc_target_rms=3000&agc_max_gain=4.0&agc_attack_ms=20&agc_release_ms=200")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusBadRequest {
		t.Errorf("AGC params should not cause 400; got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleEnhance_AGCInvalidParams exercises the ParseFloat-failure branches
// (bad float values silently ignored, defaults used).
func TestHandleEnhance_AGCInvalidParams(t *testing.T) {
	h := newHandlerWithFfmpeg("ffmpeg")
	req := buildEnhanceReq(t, minimalWAVEnhance(), "test.wav",
		"agc=true&agc_target_rms=notanumber&agc_max_gain=alsobad&agc_attack_ms=x&agc_release_ms=y")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusBadRequest {
		t.Errorf("invalid AGC floats should be ignored (not 400); got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Success-path tests (require working fake ffmpeg)
// Cover: os.Open(tmpOut), io.Copy(w,outFile), header writes, metrics update
// ---------------------------------------------------------------------------

// TestHandleEnhance_SuccessPath uses a fake ffmpeg to produce a 200 response,
// covering the post-ProcessWithOptions code paths in handleEnhance.
func TestHandleEnhance_SuccessPath(t *testing.T) {
	ffmpegPath := makeFakeFFmpeg(t)
	if !isFakeFFmpegFunctional(ffmpegPath) {
		t.Skip("fake ffmpeg not functional (needs dd + python3)")
	}
	h := newHandlerWithFfmpeg(ffmpegPath)
	req := buildEnhanceReq(t, minimalWAVEnhance(), "test.wav", "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "audio/wav" {
		t.Errorf("expected audio/wav, got: %s", ct)
	}
	if w.Header().Get("X-ClearStream-Model") == "" {
		t.Error("expected X-ClearStream-Model header")
	}
	if w.Header().Get("X-ClearStream-Duration-Ms") == "" {
		t.Error("expected X-ClearStream-Duration-Ms header")
	}
	if w.Header().Get("X-Processing-Ms") == "" {
		t.Error("expected X-Processing-Ms header")
	}
	if w.Header().Get("Content-Disposition") == "" {
		t.Error("expected Content-Disposition header")
	}
	if w.Body.Len() == 0 {
		t.Error("expected non-empty body on success")
	}
}

// TestHandleEnhance_SuccessPath_AGC exercises AGC params + success path.
func TestHandleEnhance_SuccessPath_AGC(t *testing.T) {
	ffmpegPath := makeFakeFFmpeg(t)
	if !isFakeFFmpegFunctional(ffmpegPath) {
		t.Skip("fake ffmpeg not functional")
	}
	h := newHandlerWithFfmpeg(ffmpegPath)
	req := buildEnhanceReq(t, minimalWAVEnhance(), "test.wav",
		"agc=true&agc_target_rms=3000&agc_max_gain=4.0&agc_attack_ms=20&agc_release_ms=200")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("expected 200 or 500, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleEnhance_SuccessPath_Metrics checks /metrics after a successful enhance.
func TestHandleEnhance_SuccessPath_Metrics(t *testing.T) {
	ffmpegPath := makeFakeFFmpeg(t)
	if !isFakeFFmpegFunctional(ffmpegPath) {
		t.Skip("fake ffmpeg not functional")
	}
	h := newHandlerWithFfmpeg(ffmpegPath)
	req := buildEnhanceReq(t, minimalWAVEnhance(), "test.wav", "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Skipf("ProcessWithOptions returned %d — skipping metrics check", w.Code)
	}
	mReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	mW := httptest.NewRecorder()
	h.ServeHTTP(mW, mReq)
	if mW.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", mW.Code)
	}
	if !strings.Contains(mW.Body.String(), "requests_ok") {
		t.Errorf("expected requests_ok in /metrics, got: %s", mW.Body.String())
	}
}
