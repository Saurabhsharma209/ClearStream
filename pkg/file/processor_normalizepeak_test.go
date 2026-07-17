// Package file -- tests for Options.NormalizePeak / normalizePeakPCM.
//
// Prior to this change, Options.NormalizePeak was accepted by
// ProcessWithOptions (and plumbed all the way from pkg/http's
// normalize_peak form field) but nothing in the pipeline ever read it, so
// requesting peak normalization silently had no effect on the output. These
// tests cover the new normalizePeakPCM helper directly (unit level) and
// prove ProcessWithOptions actually invokes it end-to-end (integration
// level, via a fake ffmpeg that lets us inspect exactly what PCM
// encodeAndMux received).
package file

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writePCMInt16 writes samples as raw signed 16-bit little-endian PCM to a
// fresh temp file and returns its path.
func writePCMInt16(t *testing.T, samples []int16) string {
	t.Helper()
	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(s))
	}
	f := filepath.Join(t.TempDir(), "test.pcm")
	if err := os.WriteFile(f, data, 0644); err != nil {
		t.Fatalf("writePCMInt16: %v", err)
	}
	return f
}

// readPCMInt16 reads a raw signed 16-bit little-endian PCM file back into samples.
func readPCMInt16(t *testing.T, path string) []int16 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readPCMInt16: %v", err)
	}
	n := len(data) / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return out
}

func peakOf(samples []int16) int {
	var peak int
	for _, s := range samples {
		v := int(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

// ---------------------------------------------------------------------------
// normalizePeakPCM -- unit tests
// ---------------------------------------------------------------------------

func TestNormalizePeakPCM_AmplifiesQuietSignal(t *testing.T) {
	path := writePCMInt16(t, []int16{500, -500, 250, -250})
	if err := normalizePeakPCM(path); err != nil {
		t.Fatalf("normalizePeakPCM: %v", err)
	}
	got := readPCMInt16(t, path)

	target := 32767.0 * math.Pow(10, targetPeakDBFS/20.0)
	gotPeak := peakOf(got)
	if math.Abs(float64(gotPeak)-target) > 1 {
		t.Errorf("peak = %d, want ~%.0f (-1 dBFS)", gotPeak, target)
	}
	if gotPeak <= 500 {
		t.Errorf("expected amplification above the original peak (500), got %d", gotPeak)
	}
	// Relative proportions should be preserved: sample 0 was 2x sample 2.
	if math.Abs(float64(got[0])-2*float64(got[2])) > 2 {
		t.Errorf("gain should scale all samples uniformly: got[0]=%d got[2]=%d", got[0], got[2])
	}
	// Sign preserved.
	if got[0] <= 0 || got[1] >= 0 {
		t.Errorf("expected sign to be preserved: got[0]=%d got[1]=%d", got[0], got[1])
	}
}

func TestNormalizePeakPCM_AttenuatesLoudSignal(t *testing.T) {
	path := writePCMInt16(t, []int16{32767, -32768, 100})
	if err := normalizePeakPCM(path); err != nil {
		t.Fatalf("normalizePeakPCM: %v", err)
	}
	got := readPCMInt16(t, path)

	target := 32767.0 * math.Pow(10, targetPeakDBFS/20.0)
	gotPeak := peakOf(got)
	if math.Abs(float64(gotPeak)-target) > 1 {
		t.Errorf("peak = %d, want ~%.0f (-1 dBFS)", gotPeak, target)
	}
	if gotPeak >= 32767 {
		t.Errorf("expected attenuation below the original peak (32767/32768), got %d", gotPeak)
	}
}

func TestNormalizePeakPCM_SilenceLeftUntouched(t *testing.T) {
	path := writePCMInt16(t, []int16{0, 0, 0, 0})
	if err := normalizePeakPCM(path); err != nil {
		t.Fatalf("normalizePeakPCM: %v", err)
	}
	got := readPCMInt16(t, path)
	for i, v := range got {
		if v != 0 {
			t.Errorf("expected silence to remain untouched, got[%d]=%d", i, v)
		}
	}
}

func TestNormalizePeakPCM_EmptyFile(t *testing.T) {
	path := writePCMInt16(t, nil)
	if err := normalizePeakPCM(path); err != nil {
		t.Fatalf("expected no error on empty pcm file, got: %v", err)
	}
}

func TestNormalizePeakPCM_OddTrailingByteNoPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "odd.pcm")
	// Two full samples (500, -500) plus one stray trailing byte.
	raw := []byte{0xF4, 0x01, 0x0C, 0xFE, 0xFF}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatalf("write odd pcm: %v", err)
	}
	if err := normalizePeakPCM(path); err != nil {
		t.Fatalf("normalizePeakPCM should not error/panic on odd trailing byte: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(data) != len(raw) {
		t.Errorf("expected file length to be preserved (%d), got %d", len(raw), len(data))
	}
	// Trailing odd byte must be preserved unmodified.
	if data[len(data)-1] != raw[len(raw)-1] {
		t.Errorf("trailing odd byte was modified: got 0x%02x, want 0x%02x", data[len(data)-1], raw[len(raw)-1])
	}
}

func TestNormalizePeakPCM_ReadFileError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.pcm")
	if err := normalizePeakPCM(path); err == nil {
		t.Fatal("expected an error for a nonexistent pcm file, got nil")
	}
}

// ---------------------------------------------------------------------------
// ProcessWithOptions -- end-to-end proof that NormalizePeak now has effect
// ---------------------------------------------------------------------------

// makeFakeFFmpegConstAmplitude builds a fake ffmpeg+ffprobe pair where the
// decode phase emits `numSamples` samples of a constant amplitude (a known,
// low, non-silent signal) and the encode phase copies the first "-i"
// argument's file (the cleaned PCM that decodeAndSuppress produced,
// optionally peak-normalized) verbatim to the destination -- letting the
// test inspect exactly what PCM reached the "encode" stage.
func makeFakeFFmpegConstAmplitude(t *testing.T, amplitude int16, numSamples int) (ffmpegPath string) {
	t.Helper()
	skipOnWindows(t)

	dir := t.TempDir()
	probeJSON := `{"streams":[{"codec_type":"audio","codec_name":"pcm_s16le","sample_rate":"16000","channels":1,"duration":"0.040000","bit_rate":"256000"}],"format":{"format_name":"wav","duration":"0.040000","bit_rate":"256000"}}`
	pyDecode := fmt.Sprintf(`import sys,struct;sys.stdout.buffer.write(struct.pack('<%dh',*([%d]*%d)))`, numSamples, amplitude, numSamples)

	script := "#!/bin/sh\n" +
		"case \"$0\" in\n" +
		"  *ffprobe*)\n" +
		"    echo '" + probeJSON + "'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"for a in \"$@\"; do LAST=\"$a\"; done\n" +
		"if [ \"$LAST\" = \"-\" ]; then\n" +
		"    python3 -c \"" + pyDecode + "\"\n" +
		"    exit 0\n" +
		"fi\n" +
		// Encode phase: copy the first -i argument's file verbatim to the
		// destination so the test can read back exactly what encodeAndMux
		// received as its cleaned PCM input.
		"PREV=\"\"\n" +
		"INPUT=\"\"\n" +
		"for a in \"$@\"; do\n" +
		"    if [ \"$PREV\" = \"-i\" ] && [ -z \"$INPUT\" ]; then INPUT=\"$a\"; fi\n" +
		"    PREV=\"$a\"\n" +
		"done\n" +
		"cp \"$INPUT\" \"$LAST\"\n" +
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

func TestProcessWithOptionsNormalizePeakEndToEnd(t *testing.T) {
	skipOnWindows(t)
	const amplitude = int16(500)
	const numSamples = 640 // 4 whole frames at FrameSizeSamples=160; no partial-frame flush noise.

	ffmpeg := makeFakeFFmpegConstAmplitude(t, amplitude, numSamples)
	src := makeDummyWAV(t)

	t.Run("true_amplifies_quiet_signal", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "out.pcm")
		p := newProcWithPath(ffmpeg)
		if err := p.ProcessWithOptions(src, dst, Options{OutputCodec: "pcm_s16le", NormalizePeak: true}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotPeak := peakOf(readPCMInt16(t, dst))
		target := 32767.0 * math.Pow(10, targetPeakDBFS/20.0)
		if math.Abs(float64(gotPeak)-target) > 2 {
			t.Errorf("peak = %d, want ~%.0f (-1 dBFS)", gotPeak, target)
		}
		if gotPeak <= int(amplitude) {
			t.Errorf("NormalizePeak=true should raise peak above the original amplitude %d, got %d", amplitude, gotPeak)
		}
	})

	t.Run("false_leaves_output_unchanged", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "out.pcm")
		p := newProcWithPath(ffmpeg)
		if err := p.ProcessWithOptions(src, dst, Options{OutputCodec: "pcm_s16le"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotPeak := peakOf(readPCMInt16(t, dst))
		if gotPeak != int(amplitude) {
			t.Errorf("without NormalizePeak, expected peak to remain %d, got %d", amplitude, gotPeak)
		}
	})
}
