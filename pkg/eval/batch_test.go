package eval

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// ─── NewBatchRunner tests ────────────────────────────────────────────────────

// TestNewBatchRunner_Defaults verifies that zero-value Workers, TargetSampleRate,
// and FFmpegPath are filled in with sensible defaults.
func TestNewBatchRunner_Defaults(t *testing.T) {
	sup := &passthroughSuppressor{}
	r := NewBatchRunner(BatchConfig{
		InputDir:   "/tmp",
		OutputDir:  "/tmp",
		Suppressor: sup,
		// Workers, TargetSampleRate, FFmpegPath intentionally left zero.
	})
	if r.cfg.Workers <= 0 {
		t.Errorf("Workers: want > 0 (NumCPU), got %d", r.cfg.Workers)
	}
	if r.cfg.TargetSampleRate != 16000 {
		t.Errorf("TargetSampleRate: want 16000, got %d", r.cfg.TargetSampleRate)
	}
	if r.cfg.FFmpegPath != "ffmpeg" {
		t.Errorf("FFmpegPath: want \"ffmpeg\", got %q", r.cfg.FFmpegPath)
	}
}

// TestNewBatchRunner_ExplicitValues verifies that explicit non-zero values are
// not overwritten by the defaults logic.
func TestNewBatchRunner_ExplicitValues(t *testing.T) {
	sup := &passthroughSuppressor{}
	r := NewBatchRunner(BatchConfig{
		InputDir:         "/tmp",
		OutputDir:        "/tmp",
		Suppressor:       sup,
		Workers:          4,
		TargetSampleRate: 8000,
		FFmpegPath:       "/usr/local/bin/ffmpeg",
	})
	if r.cfg.Workers != 4 {
		t.Errorf("Workers: want 4, got %d", r.cfg.Workers)
	}
	if r.cfg.TargetSampleRate != 8000 {
		t.Errorf("TargetSampleRate: want 8000, got %d", r.cfg.TargetSampleRate)
	}
	if r.cfg.FFmpegPath != "/usr/local/bin/ffmpeg" {
		t.Errorf("FFmpegPath: want /usr/local/bin/ffmpeg, got %q", r.cfg.FFmpegPath)
	}
}

// TestNewBatchRunner_PanicsOnNilSuppressor verifies that passing a nil Suppressor
// causes a panic with a descriptive message.
func TestNewBatchRunner_PanicsOnNilSuppressor(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil Suppressor, got none")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic value, got %T: %v", r, r)
		}
		if msg != "eval: BatchConfig.Suppressor must not be nil" {
			t.Errorf("unexpected panic message: %q", msg)
		}
	}()
	NewBatchRunner(BatchConfig{
		InputDir:   "/tmp",
		OutputDir:  "/tmp",
		Suppressor: nil,
	})
}

// ─── collectFiles tests ──────────────────────────────────────────────────────

// TestCollectFiles_FiltersExtensions creates a temp dir with a mix of audio
// and non-audio files and verifies only the audio files are returned.
func TestCollectFiles_FiltersExtensions(t *testing.T) {
	dir := t.TempDir()

	// Create dummy files — content doesn't matter, just the names.
	audioFiles := []string{"a.wav", "b.mp3", "c.ogg", "d.flac", "e.aac"}
	nonAudioFiles := []string{"notes.txt", "image.png", "script.sh", "data.json"}

	for _, name := range append(audioFiles, nonAudioFiles...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	got, err := collectFiles(dir, nil)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(got) != len(audioFiles) {
		t.Errorf("file count: want %d, got %d — files: %v", len(audioFiles), len(got), got)
	}
	// Every returned path must have an audio extension.
	audioExts := map[string]bool{
		".wav": true, ".mp3": true, ".ogg": true, ".flac": true,
		".aac": true, ".m4a": true, ".opus": true, ".wma": true,
		".pcm": true, ".raw": true, ".gsm": true, ".g711": true,
	}
	for _, p := range got {
		ext := filepath.Ext(p)
		if !audioExts[ext] {
			t.Errorf("unexpected non-audio file in result: %s", p)
		}
	}
}

// TestCollectFiles_RespectsFilter verifies that a FileFilter predicate is
// applied and only matching files are returned.
func TestCollectFiles_RespectsFilter(t *testing.T) {
	dir := t.TempDir()

	names := []string{"keep_me.wav", "skip_me.wav", "keep_me_too.mp3"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	filter := func(path string) bool {
		return filepath.Base(path) != "skip_me.wav"
	}

	got, err := collectFiles(dir, filter)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 files after filter, got %d: %v", len(got), got)
	}
	for _, p := range got {
		if filepath.Base(p) == "skip_me.wav" {
			t.Errorf("filter did not exclude skip_me.wav")
		}
	}
}

// TestCollectFiles_ErrorOnBadDir verifies that a non-existent directory returns
// an error.
func TestCollectFiles_ErrorOnBadDir(t *testing.T) {
	_, err := collectFiles("/this/path/does/not/exist/at/all", nil)
	if err == nil {
		t.Fatal("expected error for non-existent dir, got nil")
	}
}

// TestCollectFiles_SkipsSubdirs verifies that subdirectories inside InputDir
// are not returned (only top-level files are considered).
func TestCollectFiles_SkipsSubdirs(t *testing.T) {
	dir := t.TempDir()

	// Create a top-level audio file.
	if err := os.WriteFile(filepath.Join(dir, "top.wav"), []byte{}, 0o644); err != nil {
		t.Fatalf("create top.wav: %v", err)
	}
	// Create a subdirectory with an audio file inside it.
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested.wav"), []byte{}, 0o644); err != nil {
		t.Fatalf("create nested.wav: %v", err)
	}

	got, err := collectFiles(dir, nil)
	if err != nil {
		t.Fatalf("collectFiles: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("want 1 file (top-level only), got %d: %v", len(got), got)
	}
}

// ─── bytesToInt16 tests ──────────────────────────────────────────────────────

// int16ToBytes is a small helper to produce little-endian bytes for a value.
func int16ToBytes(v int16) []byte {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, uint16(v))
	return b
}

// TestBytesToInt16 verifies correct little-endian decoding for several known
// values: zero, a positive value, a negative value, and the int16 maximum.
func TestBytesToInt16(t *testing.T) {
	cases := []struct {
		name string
		val  int16
	}{
		{"zero", 0},
		{"positive", 1000},
		{"negative", -1000},
		{"max positive", 32767},
		{"min negative", -32768},
		{"mid positive", 256},
	}

	// Build one flat byte slice from all cases.
	var buf []byte
	for _, tc := range cases {
		buf = append(buf, int16ToBytes(tc.val)...)
	}

	got := bytesToInt16(buf)
	if len(got) != len(cases) {
		t.Fatalf("sample count: want %d, got %d", len(cases), len(got))
	}
	for i, tc := range cases {
		if got[i] != tc.val {
			t.Errorf("case %q: want %d, got %d", tc.name, tc.val, got[i])
		}
	}
}

// TestBytesToInt16_EmptyInput verifies that an empty byte slice returns an
// empty (non-nil) slice without panicking.
func TestBytesToInt16_EmptyInput(t *testing.T) {
	got := bytesToInt16([]byte{})
	if got == nil {
		t.Error("expected non-nil slice for empty input")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got len=%d", len(got))
	}
}

// TestBytesToInt16_OddBytesTruncated verifies that an odd-length input has its
// trailing byte silently dropped (len/2 samples).
func TestBytesToInt16_OddBytesTruncated(t *testing.T) {
	b := []byte{0x01, 0x00, 0xFF} // 3 bytes → 1 sample
	got := bytesToInt16(b)
	if len(got) != 1 {
		t.Errorf("want 1 sample from 3-byte input, got %d", len(got))
	}
	if got[0] != 1 {
		t.Errorf("sample value: want 1, got %d", got[0])
	}
}

// ─── lastLine tests ──────────────────────────────────────────────────────────

// TestLastLine covers normal multiline strings, empty strings, and
// strings where trailing lines are whitespace-only.
func TestLastLine(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "multiline returns last non-empty line",
			input: "line one\nline two\nline three",
			want:  "line three",
		},
		{
			name:  "trailing newline is ignored",
			input: "line one\nline two\n",
			want:  "line two",
		},
		{
			name:  "single line",
			input: "only line",
			want:  "only line",
		},
		{
			name:  "empty string returns empty",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace-only lines are skipped",
			input: "real content\n   \n\t\n",
			want:  "real content",
		},
		{
			name:  "all blank lines returns original string",
			input: "   \n\n  \n",
			want:  "   \n\n  \n",
		},
		{
			name:  "ffmpeg-style error message",
			input: "ffmpeg version 6.0\nInput #0, matroska\nError: codec not found",
			want:  "Error: codec not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lastLine(tc.input)
			if got != tc.want {
				t.Errorf("lastLine(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
