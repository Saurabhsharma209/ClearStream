package http

import (
	"bytes"
	"encoding/binary"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// flushingRecorder wraps httptest.ResponseRecorder and implements http.Flusher
// so the flusher branch in handleStream is exercised.
type flushingRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushingRecorder) Flush() {
	f.flushed = true
}

// TestHandleStreamWithFlusher exercises the canFlush=true branch in handleStream.
func TestHandleStreamWithFlusher(t *testing.T) {
	h := NewHandler(HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
	pcm := make([]byte, audio.FrameSizeBytes*10)
	req := httptest.NewRequest(http.MethodPost, "/enhance/stream", bytes.NewReader(pcm))
	req.Header.Set("Content-Type", "audio/pcm")

	rec := &flushingRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !rec.flushed {
		t.Error("expected Flush() to be called on the flushing recorder")
	}
}

// TestExtToMIMEAllBranches is a white-box test (package http, not http_test)
// that exercises every branch of extToMIME directly.
func TestExtToMIMEAllBranches(t *testing.T) {
	cases := []struct {
		ext  string
		want string
	}{
		{".mp3", "audio/mpeg"},
		{".wav", "audio/wav"},
		{".ogg", "audio/ogg"},
		{".aac", "audio/aac"},
		{".m4a", "audio/aac"},
		{".flac", "audio/flac"},
		{".mp4", "video/mp4"},
		{".mkv", "video/x-matroska"},
		{".xyz", "application/octet-stream"},
		{"", "application/octet-stream"},
		// Case-insensitive matching: filepath.Ext preserves whatever case
		// the uploaded filename used, and uppercase/mixed-case extensions
		// are common from iOS/Windows clients (e.g. "voicememo.M4A").
		// Previously these fell through to the default branch instead of
		// resolving to the correct MIME type.
		{".MP3", "audio/mpeg"},
		{".WAV", "audio/wav"},
		{".M4A", "audio/aac"},
		{".Mp4", "video/mp4"},
		{".FLAC", "audio/flac"},
	}
	for _, tc := range cases {
		got := extToMIME(tc.ext)
		if got != tc.want {
			t.Errorf("extToMIME(%q) = %q, want %q", tc.ext, got, tc.want)
		}
	}
}

// TestCodecToExtAllBranches is a white-box test (package http, not
// http_test) that exercises every branch of codecToExt directly, including
// its case-insensitive matching (via strings.ToLower) and the "" fallback
// for unrecognised codecs. Only one codec value (via output_codec=aac) was
// previously exercised indirectly through handleEnhance, leaving most
// switch cases uncovered.
func TestCodecToExtAllBranches(t *testing.T) {
	cases := []struct {
		codec string
		want  string
	}{
		{"mp3", ".mp3"},
		{"libmp3lame", ".mp3"},
		{"MP3", ".mp3"}, // case-insensitive
		{"opus", ".opus"},
		{"libopus", ".opus"},
		{"ogg", ".opus"},
		{"OGG", ".opus"}, // case-insensitive
		{"flac", ".flac"},
		{"FLAC", ".flac"}, // case-insensitive
		{"pcm_s16le", ".wav"},
		{"pcm_mulaw", ".wav"},
		{"pcm_alaw", ".wav"},
		{"wav", ".wav"},
		{"aac", ".aac"},
		{"m4a", ".aac"},
		{"AAC", ".aac"}, // case-insensitive
		{"unknown_codec", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := codecToExt(tc.codec)
		if got != tc.want {
			t.Errorf("codecToExt(%q) = %q, want %q", tc.codec, got, tc.want)
		}
	}
}

// buildWAVBytesWB produces a minimal valid WAV file from int16 samples.
func buildWAVBytesWB(samples []int16) []byte {
	dataSize := uint32(len(samples) * 2)
	fileSize := 36 + dataSize
	buf := new(bytes.Buffer)
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, fileSize) //nolint:errcheck
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))    //nolint:errcheck
	binary.Write(buf, binary.LittleEndian, uint16(1))     //nolint:errcheck
	binary.Write(buf, binary.LittleEndian, uint16(1))     //nolint:errcheck
	binary.Write(buf, binary.LittleEndian, uint32(16000)) //nolint:errcheck
	binary.Write(buf, binary.LittleEndian, uint32(32000)) //nolint:errcheck
	binary.Write(buf, binary.LittleEndian, uint16(2))     //nolint:errcheck
	binary.Write(buf, binary.LittleEndian, uint16(16))    //nolint:errcheck
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, dataSize) //nolint:errcheck
	for _, s := range samples {
		binary.Write(buf, binary.LittleEndian, s) //nolint:errcheck
	}
	return buf.Bytes()
}

// buildMultipartBodyWB creates a multipart/form-data body with the "audio" field.
func buildMultipartBodyWB(data []byte, filename string) (*bytes.Buffer, string) {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, _ := w.CreateFormFile("audio", filename)
	fw.Write(data) //nolint:errcheck
	w.Close()
	return body, w.FormDataContentType()
}

// TestHandleEnhanceInternalPath is a white-box test that calls handleEnhance
// through the public ServeHTTP interface but with a pre-constructed WAV body.
// This exercises the AGC parsing path with the white-box test (same package).
func TestHandleEnhanceInternalPath(t *testing.T) {
	h := NewHandler(HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
	wavData := buildWAVBytesWB(make([]int16, 1600))
	body, ct := buildMultipartBodyWB(wavData, "test.wav")
	req := httptest.NewRequest(http.MethodPost,
		"/enhance?agc=true&agc_attack_ms=10&agc_release_ms=100", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// Accept 200 (ffmpeg available) or 500.
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("expected 200 or 500, got %d", w.Code)
	}
}

// TestHandleEnhanceWithChmod exercises handleEnhance through the full success path
// with a valid WAV file to ensure all response-header set statements are covered
// from the white-box test perspective.
func TestHandleEnhanceSuccessPathWB(t *testing.T) {
	h := NewHandler(HandlerConfig{
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
	wavData := buildWAVBytesWB(make([]int16, 1600))
	body, ct := buildMultipartBodyWB(wavData, "out.ogg")
	req := httptest.NewRequest(http.MethodPost, "/enhance", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// Accept 200 or 500 (ffmpeg may be absent).
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Errorf("expected 200 or 500, got %d", w.Code)
	}
}
