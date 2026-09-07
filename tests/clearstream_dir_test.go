// Package clearstream_test -- regression test for ProcessDirWithOptions.
//
// Historical bug (fixed alongside this test): cmd/clearstream/main.go's
// `dir` CLI subcommand built its own file.Processor directly instead of
// reusing the ClearStream instance's configured model, leaving
// file.ProcessorConfig.Suppressor nil regardless of the -model flag.
// Because file-based processing (unlike the RTP/Pipeline path) never wires
// a VAD, audio.Pipeline.ProcessFrames calls Suppressor.Process
// unconditionally on every frame -- so a nil Suppressor panicked on the
// first frame of every file, 100% of the time. ProcessDirWithOptions (added
// to clearstream.go alongside this test) is the fix: it is the directory
// equivalent of ProcessFileWithOptions, and always wires cs.model through
// to the file.Processor it builds.
package clearstream_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/file"
)

// makeFakeFFmpegForDirTest writes a fake ffmpeg+ffprobe shell script to a
// temp dir and returns its path (to be used as Config.FFmpegPath). Mirrors
// the fake-ffmpeg pattern used throughout pkg/file's own tests (each
// package keeps its own copy since the helpers are unexported).
func makeFakeFFmpegForDirTest(t *testing.T) (ffmpegPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-ffmpeg shell script not supported on Windows")
	}

	dir := t.TempDir()

	probeJSON := `{"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"16000","channels":1,"duration":"0.020000","bit_rate":"256000"}],"format":{"format_name":"wav","duration":"0.020000","bit_rate":"256000"}}`

	pyWAV := `import sys,struct;` +
		`dst=sys.argv[-1];` +
		`d=b'\x00'*320;` +
		`h=b'RIFF'+struct.pack('<I',36+len(d))+b'WAVEfmt ';` +
		`h+=struct.pack('<IHHIIHH',16,1,1,16000,32000,2,16);` +
		`h+=b'data'+struct.pack('<I',len(d));` +
		`open(dst,'wb').write(h+d)`

	script := "#!/bin/sh\n" +
		"case \"$0\" in\n" +
		"  *ffprobe*)\n" +
		"    echo '" + probeJSON + "'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"for a in \"$@\"; do LAST=\"$a\"; done\n" +
		"if [ \"$LAST\" = \"-\" ]; then\n" +
		"    dd if=/dev/zero bs=320 count=1 2>/dev/null\n" +
		"    exit 0\n" +
		"fi\n" +
		"python3 -c \"" + pyWAV + "\" \"$LAST\" 2>/dev/null\n" +
		"if [ $? -ne 0 ]; then\n" +
		"    dd if=/dev/zero of=\"$LAST\" bs=364 count=1 2>/dev/null\n" +
		"fi\n" +
		"exit 0\n"

	ffmpegPath = filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(ffmpegPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	if err := os.WriteFile(ffprobePath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	return ffmpegPath
}

// TestProcessDirWithOptions_WiresConfiguredSuppressor verifies that
// ProcessDirWithOptions actually enhances every file in a directory using
// the ClearStream instance's configured model, instead of panicking on a
// nil Suppressor the way the pre-fix `clearstream dir` CLI path did.
func TestProcessDirWithOptions_WiresConfiguredSuppressor(t *testing.T) {
	ffmpegPath := makeFakeFFmpegForDirTest(t)

	cfg := clearstream.DefaultConfig()
	cfg.FFmpegPath = ffmpegPath
	cfg.Model = "passthrough"
	cs, err := clearstream.New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()

	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "call.wav")
	if err := os.WriteFile(srcPath, []byte("dummy-src-bytes"), 0644); err != nil {
		t.Fatalf("write src file: %v", err)
	}

	results := cs.ProcessDirWithOptions(srcDir, dstDir, file.Options{})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	r := results[0]
	if r.Err != nil {
		t.Fatalf("expected no error processing %s, got: %v", r.Src, r.Err)
	}
	if r.Skipped {
		t.Fatalf("expected file to be processed, was skipped (reason: %s)", r.SkipReason)
	}
	dstPath := filepath.Join(dstDir, "call.wav")
	if _, err := os.Stat(dstPath); err != nil {
		t.Fatalf("expected enhanced output at %s, got: %v", dstPath, err)
	}
}

// TestProcessDirWithOptions_MissingSrcDir verifies error propagation from
// file.ProcessDirFull is preserved through ProcessDirWithOptions.
func TestProcessDirWithOptions_MissingSrcDir(t *testing.T) {
	cs, err := clearstream.New(clearstream.DefaultConfig())
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer cs.Close()

	results := cs.ProcessDirWithOptions("/nonexistent/src/dir", t.TempDir(), file.Options{})
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected a single error result for missing src dir, got: %+v", results)
	}
}
