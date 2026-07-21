// Package file provides post-processing of audio and video files.
// It decodes audio via FFmpeg, runs noise suppression, and re-encodes
// back to the original (or a specified) codec and container.
package file

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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
	"github.com/exotel/clearstream/pkg/telemetry"
	"go.uber.org/zap"
)

// ErrCodecNotFound is returned when FFmpeg cannot find the required codec.
var ErrCodecNotFound = errors.New("codec not found")

// ErrFileNotFound is returned when the input file does not exist.
var ErrFileNotFound = errors.New("file not found")

// ErrPermission is returned when the file cannot be read or written.
var ErrPermission = errors.New("permission denied")

// Options controls per-call processing behaviour.
type Options struct {
	// OutputCodec overrides the output audio codec (e.g. "aac", "opus").
	// If empty, the input codec is preserved.
	OutputCodec string

	// OutputSampleRate overrides the output sample rate.
	// If 0, the input sample rate is preserved.
	OutputSampleRate int

	// AudioOnly strips video and outputs audio-only when true.
	AudioOnly bool

	// NormalizePeak, if true, rescales the cleaned audio (after noise
	// suppression and optional AGC, before re-encoding) so its single
	// largest-magnitude sample reaches -1 dBFS, leaving a small amount of
	// headroom for downstream lossy re-encoding. A fully silent file is
	// left untouched. Default: false (output level is whatever the
	// suppressor/AGC produced).
	NormalizePeak bool

	// AGC enables Automatic Gain Control on this file processing job.
	// When set, output level is adaptively adjusted toward AGC.TargetRMS
	// after noise suppression. Use audio.DefaultAGCConfig() as a starting point.
	// Set to nil to disable (default).
	AGC *audio.AGCConfig

	// Suppressor is the noise suppressor used by StreamProcess.
	// If nil, StreamProcess will return an error.
	Suppressor model.Suppressor
	// Logger is used by StreamProcess. If nil, a nop logger is used.
	Logger *zap.Logger
	// OnProgress is called with values 0.0–1.0 as processing advances.
	// It is called from the processing goroutine; keep it non-blocking.
	OnProgress func(pct float64)

	// MaxConcurrency caps the number of files processed in parallel by
	// ProcessDir and ProcessDirFull. If zero or negative, runtime.NumCPU()
	// is used as the default.
	MaxConcurrency int

	// Telemetry receives worker-pool gauges, batch progress, and per-file
	// failure events recorded by ProcessDir/ProcessDirFull. Optional --
	// defaults to a no-op sink when unset.
	Telemetry telemetry.Sink

	// SkipExisting, if true, causes ProcessDir and ProcessDirFull to skip
	// files whose destination already exists and is newer than or equal
	// in modification time to the source (i.e. the file appears to have
	// already been processed by a prior run). This makes it safe to
	// re-run ProcessDir/ProcessDirFull over a large directory after a
	// partial failure without reprocessing files that already succeeded.
	// Default: false (always reprocess every matching file).
	SkipExisting bool

	// Context, if set, allows cancelling in-progress processing -- including
	// killing any currently-running FFmpeg child process -- via ctx.Done().
	// This applies to Process/ProcessWithOptions, and to every file handled
	// by ProcessDir/ProcessDirFull: once cancelled, the in-flight file's
	// FFmpeg process is killed and any not-yet-started files in the same
	// batch fail fast with ctx.Err() instead of being processed. If nil,
	// context.Background() is used and processing cannot be cancelled once
	// started. StreamProcess is unaffected (it already takes its own ctx).
	Context context.Context
}

// telemetry returns o.Telemetry if set, otherwise a no-op sink, so callers
// never need to nil-check before recording.
func (o Options) telemetry() telemetry.Sink {
	if o.Telemetry != nil {
		return o.Telemetry
	}
	return telemetry.NoopSink{}
}

// context returns o.Context if set, otherwise context.Background(), so
// callers never need to nil-check before use. A nil Context means
// processing (including any in-progress FFmpeg child process) cannot be
// cancelled once started.
func (o Options) context() context.Context {
	if o.Context != nil {
		return o.Context
	}
	return context.Background()
}

// ProcessorConfig holds configuration for a Processor.
type ProcessorConfig struct {
	FFmpegPath string
	SampleRate int // internal processing rate (16000)
	Channels   int
	Suppressor model.Suppressor
	Logger     *zap.Logger
}

// Processor handles file-level audio enhancement.
type Processor struct {
	cfg ProcessorConfig
}

// NewProcessor creates a new file Processor.
func NewProcessor(cfg ProcessorConfig) *Processor {
	return &Processor{cfg: cfg}
}

// Process is shorthand for ProcessWithOptions with default options.
func (p *Processor) Process(src, dst string) error {
	return p.ProcessWithOptions(src, dst, Options{})
}

// ProcessWithOptions enhances audio in src and writes the result to dst.
//
// Pipeline:
//
//	src → ffmpeg decode → 16kHz PCM → AI suppress → re-encode → mux → dst
//
// For video files, the video track passes through untouched.
func (p *Processor) ProcessWithOptions(src, dst string, opts Options) error {
	ctx := opts.context()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("file: %w", err)
	}

	logger := p.cfg.Logger.With(
		zap.String("src", src),
		zap.String("dst", dst),
	)

	if opts.OnProgress != nil {
		opts.OnProgress(0.0)
	}

	// 0. Stat the source up front. audio.Probe's FFmpeg-based fallback path
	// does not reliably surface a missing/unreadable src as an error (it can
	// return a zero-value MediaInfo with a nil error), which previously let
	// a bad src fall all the way through to decodeAndSuppress -- spawning a
	// full FFmpeg subprocess just to fail there, with the specific error
	// depending on scraping FFmpeg's stderr text. Stat-ing first makes the
	// common typed-error cases (ErrFileNotFound, ErrPermission) deterministic
	// and avoids the wasted subprocess spawn. This runs after OnProgress(0.0)
	// so that contract (progress(0.0) fires even when src is missing) holds.
	if fi, statErr := os.Stat(src); statErr != nil {
		if os.IsNotExist(statErr) {
			return fmt.Errorf("file: %q: %w", src, ErrFileNotFound)
		}
		if os.IsPermission(statErr) {
			return fmt.Errorf("file: %q: %w", src, ErrPermission)
		}
		return fmt.Errorf("file: stat %q: %w", src, statErr)
	} else if fi.IsDir() {
		return fmt.Errorf("file: %q is a directory, not a media file: %w", src, ErrFileNotFound)
	}

	// 1. Probe source
	info, err := audio.Probe(p.cfg.FFmpegPath, src)
	if err != nil {
		return fmt.Errorf("file: probe %q: %w", src, err)
	}
	logger.Info("probed source",
		zap.String("audio_codec", string(info.AudioCodec)),
		zap.Bool("has_video", info.HasVideo),
		zap.Int("sample_rate", info.SampleRate),
		zap.Int("channels", info.Channels),
	)

	if opts.OnProgress != nil {
		opts.OnProgress(0.1)
	}

	// 2. Create a temp file for the cleaned audio
	tmpAudio, err := os.CreateTemp("", "clearstream-audio-*.pcm")
	if err != nil {
		return fmt.Errorf("file: create temp: %w", err)
	}
	tmpAudio.Close()
	defer os.Remove(tmpAudio.Name())

	// 3. Decode audio to raw 16kHz mono PCM via FFmpeg pipe
	if err := p.decodeAndSuppress(ctx, src, tmpAudio.Name(), info, opts.AGC, logger, opts.OnProgress); err != nil {
		return fmt.Errorf("file: decode+suppress: %w", err)
	}

	// 3b. Optional peak normalization of the cleaned PCM, applied after
	// suppression/AGC and before re-encoding.
	if opts.NormalizePeak {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("file: %w", err)
		}
		if err := normalizePeakPCM(tmpAudio.Name()); err != nil {
			return fmt.Errorf("file: normalize peak: %w", err)
		}
	}

	if opts.OnProgress != nil {
		opts.OnProgress(0.7)
	}

	// 4. Re-encode and mux output
	outCodec := opts.OutputCodec
	if outCodec == "" {
		outCodec = string(info.AudioCodec)
		if outCodec == "unknown" {
			outCodec = inferOutputCodec(dst)
		}
	}
	outRate := opts.OutputSampleRate
	if outRate == 0 {
		outRate = info.SampleRate
	}

	if err := p.encodeAndMux(tmpAudio.Name(), src, dst, info, outCodec, outRate, opts, logger); err != nil {
		return fmt.Errorf("file: encode+mux: %w", err)
	}

	if opts.OnProgress != nil {
		opts.OnProgress(1.0)
	}

	logger.Info("processing complete", zap.String("dst", dst))
	return nil
}

// concurrencyLimit returns the number of worker goroutines ProcessDir and
// ProcessDirFull should use, honouring opts.MaxConcurrency when set to a
// positive value and falling back to runtime.NumCPU() otherwise.
func concurrencyLimit(opts Options) int {
	if opts.MaxConcurrency > 0 {
		return opts.MaxConcurrency
	}
	return runtime.NumCPU()
}

// isAlreadyProcessed reports whether dst exists and its modification time
// is greater than or equal to src's modification time, which indicates
// src has already been processed into dst by a prior run. It is used to
// implement Options.SkipExisting. Any stat error (missing src, missing
// dst, permission issues, etc.) is treated as "not already processed" so
// the file is (re)processed as normal.
func isAlreadyProcessed(src, dst string) bool {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return false
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		return false
	}
	return !dstInfo.ModTime().Before(srcInfo.ModTime())
}

// ProcessDir enhances all audio/video files in srcDir and writes results to dstDir.
// Supported extensions: .mp3 .wav .flac .ogg .aac .mp4 .mkv .mov .avi .webm .m4a
// Files are processed concurrently, bounded by opts.MaxConcurrency
// (default runtime.NumCPU() when unset/zero/negative).
// If opts.Context is cancelled, the in-flight FFmpeg process for any file
// currently processing is killed and every not-yet-started file fails fast
// with ctx.Err() instead of being processed.
// Returns a slice of errors (one per failed file; nil entries = success).
func (p *Processor) ProcessDir(srcDir, dstDir string, opts Options) []error {
	supported := map[string]bool{
		".mp3": true, ".wav": true, ".flac": true, ".ogg": true,
		".aac": true, ".mp4": true, ".mkv": true, ".mov": true,
		".avi": true, ".webm": true, ".m4a": true,
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return []error{fmt.Errorf("processdir: read src: %w", err)}
	}

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return []error{fmt.Errorf("processdir: create dst: %w", err)}
	}

	type job struct {
		src, dst string
	}
	var jobs []job
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !supported[ext] {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		if opts.SkipExisting && isAlreadyProcessed(src, dst) {
			continue
		}
		jobs = append(jobs, job{src: src, dst: dst})
	}

	if len(jobs) == 0 {
		return nil
	}

	errs := make([]error, len(jobs))
	sem := make(chan struct{}, concurrencyLimit(opts))
	var wg sync.WaitGroup

	sink := opts.telemetry()
	poolTags := map[string]string{"pool": "file.ProcessDir"}
	var active, queued, completed int64
	total := len(jobs)
	report := func() {
		now := time.Now()
		sink.RecordMetric(telemetry.Metric{Name: telemetry.MetricWorkerPoolActive, Value: float64(atomic.LoadInt64(&active)), Unit: "count", Kind: telemetry.MetricGauge, Tags: poolTags, Timestamp: now})
		sink.RecordMetric(telemetry.Metric{Name: telemetry.MetricWorkerPoolQueueDepth, Value: float64(atomic.LoadInt64(&queued)), Unit: "count", Kind: telemetry.MetricGauge, Tags: poolTags, Timestamp: now})
	}

	for i, j := range jobs {
		wg.Add(1)
		atomic.AddInt64(&queued, 1)
		go func(idx int, jb job) {
			defer wg.Done()
			report()
			sem <- struct{}{}
			atomic.AddInt64(&queued, -1)
			atomic.AddInt64(&active, 1)
			report()
			defer func() {
				atomic.AddInt64(&active, -1)
				<-sem
				report()
			}()
			err := p.ProcessWithOptions(jb.src, jb.dst, opts)
			errs[idx] = err
			if err != nil {
				sink.RecordEvent(telemetry.Event{
					Name:      telemetry.EventBatchFileFailed,
					Severity:  telemetry.SeverityError,
					Message:   err.Error(),
					Fields:    map[string]interface{}{"path": jb.src, "error": err.Error()},
					Timestamp: time.Now(),
				})
			}
			done := atomic.AddInt64(&completed, 1)
			pct := float64(done) / float64(total) * 100.0
			sink.RecordMetric(telemetry.Metric{Name: telemetry.MetricBatchProgressPercent, Value: pct, Unit: "percent", Kind: telemetry.MetricGauge, Tags: poolTags, Timestamp: time.Now()})
		}(i, j)
	}
	wg.Wait()
	return errs
}

// decodeAndSuppress decodes audio from src to 16kHz mono PCM,
// runs it through the suppressor (and optional AGC), and writes raw PCM to pcmPath.
func (p *Processor) decodeAndSuppress(ctx context.Context, src, pcmPath string, info *audio.MediaInfo, agc *audio.AGCConfig, logger *zap.Logger, onProgress func(float64)) error {
	// FFmpeg decode command: any input → 16kHz mono signed 16-bit PCM on stdout.
	// exec.CommandContext ensures the child process is killed if ctx is
	// cancelled while decoding is in progress (e.g. a caller-triggered abort
	// of a long-running batch job).
	decodeCmd := exec.CommandContext(ctx, p.cfg.FFmpegPath,
		"-i", src,
		"-vn",                                      // drop video
		"-ar", fmt.Sprintf("%d", p.cfg.SampleRate), // resample to 16kHz
		"-ac", fmt.Sprintf("%d", p.cfg.Channels), // mono
		"-f", "s16le", // raw signed 16-bit little-endian PCM
		"-", // pipe to stdout
	)

	// Open output PCM file
	pcmFile, err := os.Create(pcmPath)
	if err != nil {
		return fmt.Errorf("open pcm file: %w", err)
	}
	defer pcmFile.Close()

	// Create pipeline
	pipe := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: p.cfg.SampleRate,
		Channels:   p.cfg.Channels,
		Suppressor: p.cfg.Suppressor,
		Logger:     logger,
		AGC:        agc,
	})

	// Pipe FFmpeg stdout → suppressor → pcmFile
	pr, pw := io.Pipe()
	decodeCmd.Stdout = pw

	// Capture stderr for both error detection and real-time progress parsing.
	stderrPipe, err := decodeCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stderr pipe: %w", err)
	}

	if err := decodeCmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}

	// Stderr goroutine: accumulate for error messages and parse time= for progress.
	var stderrBuf bytes.Buffer
	stderrErrCh := make(chan struct{})
	go func() {
		defer close(stderrErrCh)
		sc := bufio.NewScanner(stderrPipe)
		for sc.Scan() {
			line := sc.Text()
			stderrBuf.WriteString(line + "\n")
			if onProgress != nil {
				if pct, ok := parseFFmpegProgressLine(line, info.DurationSec); ok {
					onProgress(pct)
				}
			}
		}
	}()

	// Reader goroutine: pull PCM from FFmpeg, suppress, write to file
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, audio.FrameSizeBytes*64) // 64 frames per read
		for {
			n, rerr := pr.Read(buf)
			if n > 0 {
				if perr := pipe.ProcessFrames(buf[:n], pcmFile); perr != nil {
					errCh <- perr
					return
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				errCh <- rerr
				return
			}
		}
		errCh <- pipe.Flush(pcmFile)
	}()

	// Drain the stderr-reading goroutine BEFORE calling Wait(). Per os/exec's
	// documented contract for StderrPipe(): "it is incorrect to call Wait
	// before all reads from the pipe have completed" -- Wait() may close the
	// underlying pipe out from under a still-reading goroutine, truncating
	// output or racing with the concurrent Read(). The scanner below only
	// returns once FFmpeg closes its stderr fd (on normal exit, or once the
	// process is killed following ctx cancellation), so waiting on it first
	// cannot deadlock.
	<-stderrErrCh // drain stderr goroutine

	// Now it is safe to reap FFmpeg's exit status, then close the write end
	// of the stdout pipe so the PCM reader goroutine below observes EOF.
	ffmpegErr := decodeCmd.Wait()
	pw.Close()

	suppressErr := <-errCh

	// If ctx was cancelled, that's the real cause of any FFmpeg/pipe error
	// above (the process was just killed) -- surface ctx.Err() directly so
	// callers can detect cancellation via errors.Is(err, context.Canceled)
	// rather than parsing a generic "signal: killed" message.
	if err := ctx.Err(); err != nil {
		return err
	}

	if ffmpegErr != nil {
		if typed := parseFFmpegError(stderrBuf.String()); typed != nil {
			return fmt.Errorf("ffmpeg decode: %w", typed)
		}
		return fmt.Errorf("ffmpeg decode: %w\nstderr: %s", ffmpegErr, stderrBuf.String())
	}
	return suppressErr
}

const targetPeakDBFS = -1.0

// normalizePeakPCM reads pcmPath (raw signed 16-bit little-endian mono PCM,
// as written by decodeAndSuppress after noise suppression/AGC), finds the
// single largest-magnitude sample, and rescales every sample by one gain
// factor so that peak lands exactly at targetPeakDBFS.
//
// This implements Options.NormalizePeak. Previously the field was accepted
// by ProcessWithOptions (and plumbed all the way from pkg/http's
// normalize_peak form field) but nothing in the pipeline ever read it, so
// requesting peak normalization silently had no effect on the output.
//
// A fully silent file (peak == 0) is left untouched: there is no peak to
// normalize to, and scaling would either divide by zero or amplify the
// noise floor/quantization error with no audible benefit.
func normalizePeakPCM(pcmPath string) error {
	data, err := os.ReadFile(pcmPath)
	if err != nil {
		return fmt.Errorf("read pcm: %w", err)
	}
	// Ignore a single trailing odd byte, if any -- shouldn't normally happen
	// for s16le PCM, but guards against a truncated file rather than
	// panicking on an out-of-range slice index below.
	n := (len(data) - (len(data) % 2)) / 2
	if n == 0 {
		return nil
	}

	var peak int32
	for i := 0; i < n; i++ {
		v := int32(int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2])))
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	if peak == 0 {
		return nil // silence; nothing to normalize
	}

	target := 32767.0 * math.Pow(10, targetPeakDBFS/20.0)
	gain := target / float64(peak)

	for i := 0; i < n; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(data[i*2:i*2+2]))) * gain
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(data[i*2:i*2+2], uint16(int16(v)))
	}

	return os.WriteFile(pcmPath, data, 0644)
}

// encodeAndMux re-encodes the cleaned PCM and muxes it with the original video (if any).
func (p *Processor) encodeAndMux(pcmPath, originalSrc, dst string, info *audio.MediaInfo, outCodec string, outRate int, opts Options, logger *zap.Logger) error {
	ctx := opts.context()
	args := []string{"-y"} // overwrite output

	// Input 1: clean PCM
	args = append(args,
		"-f", "s16le",
		"-ar", fmt.Sprintf("%d", p.cfg.SampleRate),
		"-ac", fmt.Sprintf("%d", p.cfg.Channels),
		"-i", pcmPath,
	)

	if info.HasVideo && !opts.AudioOnly {
		// Input 2: original file for video stream
		args = append(args, "-i", originalSrc)
		// Map: audio from input 0, video from input 1
		args = append(args, "-map", "0:a", "-map", "1:v")
		args = append(args, "-c:v", "copy") // copy video track unchanged
	}

	// Audio encoding
	args = append(args, "-ar", fmt.Sprintf("%d", outRate))
	args = append(args, "-ac", fmt.Sprintf("%d", p.cfg.Channels))

	switch outCodec {
	case "pcm_s16le", "pcm_mulaw", "pcm_alaw":
		args = append(args, "-c:a", outCodec)
	case "opus":
		args = append(args, "-c:a", "libopus", "-b:a", "64k")
	case "aac":
		args = append(args, "-c:a", "aac", "-b:a", "128k")
	case "mp3":
		args = append(args, "-c:a", "libmp3lame", "-b:a", "128k", "-q:a", "2")
	case "flac":
		args = append(args, "-c:a", "flac")
	default:
		args = append(args, "-c:a", outCodec)
	}

	args = append(args, dst)

	// exec.CommandContext ensures the child process is killed if ctx is
	// cancelled while encoding is in progress.
	cmd := exec.CommandContext(ctx, p.cfg.FFmpegPath, args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	logger.Debug("ffmpeg encode", zap.Strings("args", args))

	if err := cmd.Run(); err != nil {
		// If ctx was cancelled, surface that directly rather than a generic
		// "signal: killed" error, mirroring decodeAndSuppress's behaviour.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if typed := parseFFmpegError(stderrBuf.String()); typed != nil {
			return fmt.Errorf("ffmpeg decode: %w", typed)
		}
		return fmt.Errorf("ffmpeg encode: %w\nstderr: %s", err, stderrBuf.String())
	}
	return nil
}

// inferOutputCodec guesses an output codec from the destination file extension.
func inferOutputCodec(dst string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(dst), "."))
	switch ext {
	case "mp3":
		return "mp3"
	case "opus", "ogg":
		return "opus"
	case "flac":
		return "flac"
	case "wav":
		return "pcm_s16le"
	case "aac", "m4a", "mp4", "mov", "mkv", "webm":
		return "aac"
	default:
		return "aac"
	}
}

// parseFFmpegTime parses an FFmpeg time string "HH:MM:SS.ms" into seconds.
// Returns 0 on parse failure.
func parseFFmpegTime(s string) float64 {
	var h, m int
	var sec float64
	if n, _ := fmt.Sscanf(s, "%d:%d:%f", &h, &m, &sec); n == 3 {
		return float64(h*3600+m*60) + sec
	}
	return 0
}

// parseFFmpegProgressLine extracts a normalized progress fraction from an
// FFmpeg stderr stats line such as "size=   256kB time=00:00:01.23 bitrate=...",
// scaled against totalDurationSec into the decode phase's 10%-69% progress
// range. It returns ok=false whenever the line carries no usable "time="
// value -- including a truncated/malformed line where "time=" is not
// followed by any token (e.g. a partial final line flushed just before the
// ffmpeg process exits, which happens on cancellation/kill). Previously this
// logic indexed strings.Fields(...)[0] directly without checking length,
// which panics on an empty slice -- an unrecovered panic in this goroutine
// would crash the whole process, not just fail the current file.
func parseFFmpegProgressLine(line string, totalDurationSec float64) (pct float64, ok bool) {
	if totalDurationSec <= 0 {
		return 0, false
	}
	idx := strings.Index(line, "time=")
	if idx < 0 {
		return 0, false
	}
	fields := strings.Fields(line[idx+5:])
	if len(fields) == 0 {
		return 0, false
	}
	secs := parseFFmpegTime(fields[0])
	if secs <= 0 {
		return 0, false
	}
	pct = 0.1 + (secs/totalDurationSec)*0.6
	if pct > 0.69 {
		pct = 0.69
	}
	return pct, true
}

// parseFFmpegError maps common FFmpeg stderr patterns to typed errors.
func parseFFmpegError(stderr string) error {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "no such file"):
		return ErrFileNotFound
	case strings.Contains(s, "permission denied"):
		return ErrPermission
	case strings.Contains(s, "unknown encoder") || strings.Contains(s, "encoder not found") ||
		strings.Contains(s, "decoder not found"):
		return ErrCodecNotFound
	default:
		return nil
	}
}

// StreamProcess reads raw 16kHz mono PCM from r, applies noise suppression,
// and writes clean PCM to w. No temp files are used.
// opts.Suppressor must be set; opts.Logger is optional.
func StreamProcess(ctx context.Context, r io.Reader, w io.Writer, opts Options) error {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	pipe := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: opts.Suppressor,
		Logger:     logger,
		AGC:        opts.AGC,
	})

	buf := make([]byte, audio.FrameSizeBytes*64)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := r.Read(buf)
		if n > 0 {
			if perr := pipe.ProcessFrames(buf[:n], w); perr != nil {
				return fmt.Errorf("stream: process frames: %w", perr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("stream: read: %w", err)
		}
	}

	if ferr := pipe.Flush(w); ferr != nil {
		return fmt.Errorf("stream: flush: %w", ferr)
	}
	return nil
}

// Skip reason values reported in DirResult.SkipReason. Existing callers
// that only check DirResult.Skipped (without looking at SkipReason)
// continue to work unmodified: Skipped is true for both reasons below,
// exactly as it was true-for-unsupported-extensions before SkipReason
// existed.
const (
	// SkipReasonUnsupportedExt marks files whose extension is not in the
	// set of extensions ProcessDir/ProcessDirFull know how to process.
	SkipReasonUnsupportedExt = "unsupported_ext"
	// SkipReasonAlreadyProcessed marks files skipped because
	// Options.SkipExisting was true and the destination already existed
	// with a modification time >= the source's (i.e. already processed
	// by a prior run).
	SkipReasonAlreadyProcessed = "already_processed"
)

// DirResult holds the outcome of processing a single file inside ProcessDirFull.
type DirResult struct {
	Src     string
	Dst     string
	Skipped bool // true when the file was not processed; see SkipReason for why
	// SkipReason explains why Skipped is true. One of SkipReasonUnsupportedExt
	// or SkipReasonAlreadyProcessed. Empty when Skipped is false.
	SkipReason string
	Err        error
}

// ProcessDirFull is like ProcessDir but returns a DirResult for every file
// in srcDir (including unsupported files, which are marked Skipped=true).
// If opts.Context is cancelled, the in-flight FFmpeg process for any file
// currently processing is killed and every not-yet-started file fails fast
// with ctx.Err() instead of being processed.
func (p *Processor) ProcessDirFull(srcDir, dstDir string, opts Options) []DirResult {
	supported := map[string]bool{
		".mp3": true, ".wav": true, ".flac": true, ".ogg": true,
		".aac": true, ".mp4": true, ".mkv": true, ".mov": true,
		".avi": true, ".webm": true, ".m4a": true,
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return []DirResult{{Err: fmt.Errorf("processdir: read src: %w", err)}}
	}

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return []DirResult{{Err: fmt.Errorf("processdir: create dst: %w", err)}}
	}

	var results []DirResult
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		if !supported[ext] {
			results = append(results, DirResult{Src: src, Dst: dst, Skipped: true, SkipReason: SkipReasonUnsupportedExt})
			continue
		}
		if opts.SkipExisting && isAlreadyProcessed(src, dst) {
			results = append(results, DirResult{Src: src, Dst: dst, Skipped: true, SkipReason: SkipReasonAlreadyProcessed})
			continue
		}
		results = append(results, DirResult{Src: src, Dst: dst, Skipped: false})
	}

	// Process non-skipped files concurrently.
	sem := make(chan struct{}, concurrencyLimit(opts))
	var wg sync.WaitGroup
	sink := opts.telemetry()
	poolTags := map[string]string{"pool": "file.ProcessDirFull"}
	var active, queued, completed int64
	var total int64
	for i := range results {
		if !results[i].Skipped {
			total++
		}
	}
	report := func() {
		now := time.Now()
		sink.RecordMetric(telemetry.Metric{Name: telemetry.MetricWorkerPoolActive, Value: float64(atomic.LoadInt64(&active)), Unit: "count", Kind: telemetry.MetricGauge, Tags: poolTags, Timestamp: now})
		sink.RecordMetric(telemetry.Metric{Name: telemetry.MetricWorkerPoolQueueDepth, Value: float64(atomic.LoadInt64(&queued)), Unit: "count", Kind: telemetry.MetricGauge, Tags: poolTags, Timestamp: now})
	}
	for i := range results {
		if results[i].Skipped {
			continue
		}
		wg.Add(1)
		atomic.AddInt64(&queued, 1)
		go func(idx int) {
			defer wg.Done()
			report()
			sem <- struct{}{}
			atomic.AddInt64(&queued, -1)
			atomic.AddInt64(&active, 1)
			report()
			defer func() {
				atomic.AddInt64(&active, -1)
				<-sem
				report()
			}()
			err := p.ProcessWithOptions(results[idx].Src, results[idx].Dst, opts)
			results[idx].Err = err
			if err != nil {
				sink.RecordEvent(telemetry.Event{
					Name:      telemetry.EventBatchFileFailed,
					Severity:  telemetry.SeverityError,
					Message:   err.Error(),
					Fields:    map[string]interface{}{"path": results[idx].Src, "error": err.Error()},
					Timestamp: time.Now(),
				})
			}
			done := atomic.AddInt64(&completed, 1)
			if total > 0 {
				pct := float64(done) / float64(total) * 100.0
				sink.RecordMetric(telemetry.Metric{Name: telemetry.MetricBatchProgressPercent, Value: pct, Unit: "percent", Kind: telemetry.MetricGauge, Tags: poolTags, Timestamp: time.Now()})
			}
		}(i)
	}
	wg.Wait()
	return results
}
