package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

// TestEncodeAndMuxStartFailureCleansUpTempFile exercises encodeAndMux's
// cmd.Start() error branch directly. Every other encodeAndMux failure test
// in this package (TestEncodeAndMuxFailureLeavesNoPartialDst and friends)
// drives the failure through ProcessWithOptions with an invalid FFmpegPath,
// but that path fails earlier in decodeAndSuppress's own ffmpeg invocation
// and never reaches encodeAndMux's cmd.Start() call at all -- so that
// specific branch (ffmpeg binary missing/unexecutable at the encode stage)
// had no direct coverage. This calls the unexported method directly with a
// valid decoded PCM input but a broken FFmpegPath, so cmd.Start() itself is
// what fails.
func TestEncodeAndMuxStartFailureCleansUpTempFile(t *testing.T) {
	p := NewProcessor(ProcessorConfig{
		FFmpegPath: "/nonexistent/ffmpeg-binary-for-test",
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})

	pcmFile := filepath.Join(t.TempDir(), "in.pcm")
	if err := os.WriteFile(pcmFile, make([]byte, 640), 0644); err != nil {
		t.Fatalf("write pcm fixture: %v", err)
	}

	dstDir := t.TempDir()
	dst := filepath.Join(dstDir, "out.wav")
	info := &audio.MediaInfo{HasVideo: false}

	err := p.encodeAndMux(pcmFile, "unused-src", dst, info, "pcm_s16le", 16000, Options{}, zap.NewNop())
	if err == nil {
		t.Fatal("expected error when ffmpeg binary does not exist")
	}

	entries, readErr := os.ReadDir(dstDir)
	if readErr != nil {
		t.Fatalf("read dst dir: %v", readErr)
	}
	for _, e := range entries {
		t.Errorf("expected no leaked temp file after cmd.Start() failure, found: %s", e.Name())
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("expected dst to not exist, stat err: %v", statErr)
	}
}
