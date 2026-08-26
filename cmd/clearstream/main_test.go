// Package main -- regression test for the `dir` CLI subcommand.
//
// Historical bug: runDir built its own file.Processor directly instead of
// reusing the ClearStream instance it had just constructed from the
// -model/-model-path flags, so file.ProcessorConfig.Suppressor was left nil
// no matter what -model was passed. Since file-based processing never
// wires a VAD (VAD is a Pipeline-only concept, not a field on
// file.ProcessorConfig), audio.Pipeline.ProcessFrames unconditionally calls
// Suppressor.Process on every frame -- so a nil Suppressor made
// `clearstream dir` panic on the very first frame of every file, 100% of
// the time, regardless of -model. Fixed by routing through the new
// cs.ProcessDirWithOptions (clearstream.go), which always wires cs.model.
package main

import (
	"github.com/exotel/clearstream/pkg/audio"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// makeFakeFFmpegPair writes a fake ffmpeg+ffprobe shell-script pair to a new
// temp dir and returns that dir, for prepending onto PATH. Mirrors the
// fake-ffmpeg pattern used throughout pkg/file's and the top-level SDK's
// own tests (each package keeps its own copy since the helpers are
// unexported).
func makeFakeFFmpegPair(t *testing.T) (dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake-ffmpeg shell script not supported on Windows")
	}
	dir = t.TempDir()

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

	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte(script), 0755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	return dir
}

// TestRunDir_WiresConfiguredSuppressor drives the real `dir` subcommand code
// path end-to-end (the exact function invoked by `clearstream dir ...`) and
// verifies it completes successfully instead of panicking on a nil
// Suppressor, which was the previous real-world behaviour of every
// `clearstream dir` invocation regardless of the requested -model.
func TestRunDir_WiresConfiguredSuppressor(t *testing.T) {
	fakeDir := makeFakeFFmpegPair(t)
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	srcDir := t.TempDir()
	dstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "call.wav"), []byte("dummy-src-bytes"), 0644); err != nil {
		t.Fatalf("write src file: %v", err)
	}

	runDir([]string{"-i", srcDir, "-o", dstDir, "-model", "passthrough"})

	outPath := filepath.Join(dstDir, "call.wav")
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected enhanced output at %s, got: %v", outPath, err)
	}
}

// TestResolveRTPCodec_PTOverridesCodecWhenExplicitlySet exercises runRTP's
// documented -pt/-codec precedence contract in isolation, without the
// subprocess machinery main_subprocess_test.go needs for runRTP itself:
// -pt "overrides --codec if set" only when the caller actually passed -pt
// (its zero value, 0, is also a legitimate PCMU payload type and can't
// double as an "unset" sentinel, which is why runRTP tracks this via
// fs.Visit rather than checking *pt != 0).
func TestResolveRTPCodec_PTOverridesCodecWhenExplicitlySet(t *testing.T) {
	cases := []struct {
		name            string
		codec           string
		ptExplicitlySet bool
		want            audio.Codec
	}{
		{"auto codec always defers to payload type", "auto", false, ""},
		{"auto codec defers even when pt also set", "auto", true, ""},
		{"explicit codec applies when pt not set", "pcma", false, audio.Codec("pcma")},
		{"explicit pt overrides an explicit codec", "pcma", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveRTPCodec(tc.codec, tc.ptExplicitlySet)
			if got != tc.want {
				t.Fatalf("resolveRTPCodec(%q, %v) = %q, want %q", tc.codec, tc.ptExplicitlySet, got, tc.want)
			}
		})
	}
}
