package eval

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
)

// BatchConfig configures a batch eval run over a directory of audio files.
type BatchConfig struct {
	// InputDir is the directory containing audio files to evaluate.
	// Supported formats: any format ffmpeg can decode (wav, mp3, ogg, flac, …).
	// Sub-directories are NOT walked; only top-level files are processed.
	InputDir string

	// OutputDir is where JSON, CSV, and config YAML outputs are written.
	// Created if it does not exist.
	OutputDir string

	// Workers is the number of parallel pipeline instances.
	// Default: runtime.NumCPU().
	Workers int

	// Suppressor is the noise suppression backend used for each worker.
	// Use model.NewPassthrough() for baseline latency measurement, or a real
	// suppressor (RNNoise, DeepFilterNet) for quality evaluation.
	// Required.
	Suppressor model.Suppressor

	// TargetSampleRate is the rate to resample all audio to before suppression.
	// Default: 16000 (ClearStream native processing rate).
	TargetSampleRate int

	// AGC enables Automatic Gain Control on each worker pipeline.
	// Set to nil to disable.
	AGC *audio.AGCConfig

	// FFmpegPath is the path to the ffmpeg binary.
	// Default: "ffmpeg" (resolved from PATH).
	FFmpegPath string

	// OnProgress is called after each file completes, with (filesCompleted, totalFiles).
	OnProgress func(done, total int)

	// FileFilter is an optional predicate that returns true for files to process.
	// If nil, all files in InputDir are included.
	FileFilter func(path string) bool
}

// BatchRunner executes a parallel batch evaluation over a corpus of audio files.
type BatchRunner struct {
	cfg BatchConfig
}

// NewBatchRunner creates a BatchRunner. Panics if Suppressor is nil.
func NewBatchRunner(cfg BatchConfig) *BatchRunner {
	if cfg.Suppressor == nil {
		panic("eval: BatchConfig.Suppressor must not be nil")
	}
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.NumCPU()
	}
	if cfg.TargetSampleRate == 0 {
		cfg.TargetSampleRate = 16000
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}
	return &BatchRunner{cfg: cfg}
}

// Run processes every audio file in InputDir concurrently.
// Returns a BatchSummary with per-file FileResults.
// Cancelled via ctx.
func (r *BatchRunner) Run(ctx context.Context) (BatchSummary, error) {
	if err := os.MkdirAll(r.cfg.OutputDir, 0o755); err != nil {
		return BatchSummary{}, fmt.Errorf("eval: create output dir: %w", err)
	}

	files, err := collectFiles(r.cfg.InputDir, r.cfg.FileFilter)
	if err != nil {
		return BatchSummary{}, fmt.Errorf("eval: collect files: %w", err)
	}

	total := len(files)
	results := make([]FileResult, total)
	var doneCount atomic.Int64

	sem := make(chan struct{}, r.cfg.Workers)
	var wg sync.WaitGroup

	wallStart := time.Now()

	for i, f := range files {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, path string) {
			defer wg.Done()
			defer func() { <-sem }()

			results[idx] = r.evalFile(ctx, path)

			n := int(doneCount.Add(1))
			if r.cfg.OnProgress != nil {
				r.cfg.OnProgress(n, total)
			}
		}(i, f)
	}
	wg.Wait()

	wallMs := float64(time.Since(wallStart).Milliseconds())
	summary := AggregateResults(results, wallMs)
	return summary, nil
}

// evalFile decodes one audio file, runs it through the pipeline, and returns
// a populated FileResult.
func (r *BatchRunner) evalFile(ctx context.Context, path string) FileResult {
	result := FileResult{File: path}

	// ── Decode to raw 16kHz mono int16 PCM via ffmpeg ───────────────────────
	rawPCM, sampleRate, durationMs, err := decodeToRawPCM(ctx, r.cfg.FFmpegPath, path, r.cfg.TargetSampleRate)
	if err != nil {
		result.Error = fmt.Sprintf("decode: %v", err)
		return result
	}
	result.SampleRate = sampleRate
	result.Channels = 1 // always mono after ffmpeg -ac 1
	result.DurationMs = durationMs

	inputSamples := bytesToInt16(rawPCM)

	// ── Build per-file pipeline ──────────────────────────────────────────────
	pipeCfg := audio.PipelineConfig{
		SampleRate: sampleRate,
		Channels:   1,
		Suppressor: r.cfg.Suppressor,
		AGC:        r.cfg.AGC,
	}
	pipeline := audio.NewPipeline(pipeCfg)

	// ── Feed frames and collect metrics ──────────────────────────────────────
	frameBytes := audio.FrameSizeBytes // 320 bytes = 160 samples = 10ms @ 16kHz
	if sampleRate != 16000 {
		// Scale frame size to the actual sample rate so each chunk is still 10ms.
		frameBytes = (sampleRate / 100) * 2
		if frameBytes <= 0 {
			frameBytes = audio.FrameSizeBytes
		}
	}

	var (
		outBuf    bytes.Buffer
		latAcc    LatencyAccumulator
		agcFrames int
		agcDone   bool
	)
	targetRMS := float64(0)
	if r.cfg.AGC != nil {
		targetRMS = float64(r.cfg.AGC.TargetRMS)
	}
	agcConvergedAt := -1

	for offset := 0; offset+frameBytes <= len(rawPCM); offset += frameBytes {
		if ctx.Err() != nil {
			result.Error = "cancelled"
			return result
		}
		chunk := rawPCM[offset : offset+frameBytes]
		outBuf.Reset()

		t0 := time.Now()
		if err := pipeline.ProcessFrames(chunk, &outBuf); err != nil {
			result.Error = fmt.Sprintf("pipeline: %v", err)
			return result
		}
		latAcc.Add(float64(time.Since(t0).Microseconds()) / 1000.0)
		agcFrames++

		// AGC convergence: check if output RMS reached targetRMS ± 20%.
		if !agcDone && targetRMS > 0 && outBuf.Len() > 0 {
			outSamples := bytesToInt16(outBuf.Bytes())
			rms := RMSLevel(outSamples)
			if math.Abs(rms-targetRMS)/targetRMS <= 0.20 {
				agcConvergedAt = agcFrames
				agcDone = true
			}
		}
	}

	// ── Run full input and output through SNR computation ────────────────────
	// Re-process fully to get the complete output for SNR comparison.
	pipeline.Reset()
	var fullOut bytes.Buffer
	_ = pipeline.ProcessFrames(rawPCM, &fullOut)
	outputSamples := bytesToInt16(fullOut.Bytes())
	result.SNR = ComputeSNRPair(inputSamples, outputSamples)

	// ── Pipeline stats for VAD ───────────────────────────────────────────────
	stats := pipeline.Stats()
	result.VAD = ComputeVADStats(stats.FramesProcessed, stats.FramesSilent)

	// ── Latency ──────────────────────────────────────────────────────────────
	result.Latency = latAcc.Stats()

	// ── AGC convergence ──────────────────────────────────────────────────────
	if targetRMS > 0 {
		finalRMS := float64(0)
		if len(outputSamples) > 0 {
			finalRMS = RMSLevel(outputSamples[max(0, len(outputSamples)-audio.FrameSizeSamples):])
		}
		result.AGC = AGCConvergence{
			TargetRMS:        targetRMS,
			FramesToConverge: agcConvergedAt,
			ConvergedMs:      float64(agcConvergedAt) * 10,
			FinalRMS:         finalRMS,
		}
	}

	return result
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// collectFiles lists audio files in dir matching filter (or all if nil).
func collectFiles(dir string, filter func(string) bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	audioExts := map[string]bool{
		".wav": true, ".mp3": true, ".ogg": true, ".flac": true,
		".aac": true, ".m4a": true, ".opus": true, ".wma": true,
		".pcm": true, ".raw": true, ".gsm": true, ".g711": true,
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !audioExts[ext] {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if filter != nil && !filter(path) {
			continue
		}
		files = append(files, path)
	}
	return files, nil
}

// decodeToRawPCM uses ffmpeg to decode any audio format to mono int16 PCM at
// targetRate.  Returns raw bytes, actual sample rate, duration in ms, error.
func decodeToRawPCM(ctx context.Context, ffmpegPath, inputFile string, targetRate int) ([]byte, int, float64, error) {
	// ffmpeg -i <in> -ac 1 -ar <rate> -f s16le -
	args := []string{
		"-y",             // overwrite
		"-i", inputFile,  // input
		"-vn",            // no video
		"-ac", "1",       // mono
		"-ar", fmt.Sprintf("%d", targetRate), // sample rate
		"-f", "s16le",    // raw signed 16-bit LE PCM
		"-",              // stdout
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// If ctx is cancelled, don't wrap the stderr noise.
		if ctx.Err() != nil {
			return nil, 0, 0, ctx.Err()
		}
		return nil, 0, 0, fmt.Errorf("ffmpeg: %v — %s", err, lastLine(stderr.String()))
	}

	rawPCM := stdout.Bytes()
	if len(rawPCM) == 0 {
		return nil, 0, 0, fmt.Errorf("ffmpeg produced no output for %s", inputFile)
	}

	// Duration = samples / sampleRate, samples = bytes / 2 (int16).
	durationMs := float64(len(rawPCM)/2) / float64(targetRate) * 1000.0
	return rawPCM, targetRate, durationMs, nil
}

// bytesToInt16 converts a little-endian byte slice to int16 samples.
func bytesToInt16(b []byte) []int16 {
	n := len(b) / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return out
}

// lastLine returns the last non-empty line of a string (for ffmpeg error messages).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

