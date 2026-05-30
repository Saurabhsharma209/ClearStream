// Package file provides post-processing of audio and video files.
// It decodes audio via FFmpeg, runs noise suppression, and re-encodes
// back to the original (or a specified) codec and container.
package file

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
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

	// NormalizePeak applies peak normalization to the output (-1 dBFS target).
	NormalizePeak bool

	// OnProgress is called with values 0.0–1.0 as processing advances.
	// It is called from the processing goroutine; keep it non-blocking.
	OnProgress func(pct float64)
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
//   src → ffmpeg decode → 16kHz PCM → AI suppress → re-encode → mux → dst
//
// For video files, the video track passes through untouched.
func (p *Processor) ProcessWithOptions(src, dst string, opts Options) error {
	logger := p.cfg.Logger.With(
		zap.String("src", src),
		zap.String("dst", dst),
	)

	if opts.OnProgress != nil {
		opts.OnProgress(0.0)
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
	if err := p.decodeAndSuppress(src, tmpAudio.Name(), info, logger); err != nil {
		return fmt.Errorf("file: decode+suppress: %w", err)
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

// ProcessDir enhances all audio/video files in srcDir and writes results to dstDir.
// Supported extensions: .mp3 .wav .flac .ogg .aac .mp4 .mkv .mov .avi .webm .m4a
// Files are processed concurrently up to runtime.NumCPU() goroutines.
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
		jobs = append(jobs, job{
			src: filepath.Join(srcDir, e.Name()),
			dst: filepath.Join(dstDir, e.Name()),
		})
	}

	if len(jobs) == 0 {
		return nil
	}

	errs := make([]error, len(jobs))
	sem := make(chan struct{}, runtime.NumCPU())
	var wg sync.WaitGroup

	for i, j := range jobs {
		wg.Add(1)
		go func(idx int, jb job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			errs[idx] = p.ProcessWithOptions(jb.src, jb.dst, opts)
		}(i, j)
	}
	wg.Wait()
	return errs
}

// decodeAndSuppress decodes audio from src to 16kHz mono PCM,
// runs it through the suppressor, and writes raw PCM to pcmPath.
func (p *Processor) decodeAndSuppress(src, pcmPath string, info *audio.MediaInfo, logger *zap.Logger) error {
	// FFmpeg decode command: any input → 16kHz mono signed 16-bit PCM on stdout
	decodeCmd := exec.Command(p.cfg.FFmpegPath,
		"-i", src,
		"-vn",                                        // drop video
		"-ar", fmt.Sprintf("%d", p.cfg.SampleRate),  // resample to 16kHz
		"-ac", fmt.Sprintf("%d", p.cfg.Channels),    // mono
		"-f", "s16le",                                // raw signed 16-bit little-endian PCM
		"-",                                          // pipe to stdout
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
	})

	// Pipe FFmpeg stdout → suppressor → pcmFile
	pr, pw := io.Pipe()
	decodeCmd.Stdout = pw
	var stderrBuf bytes.Buffer
	decodeCmd.Stderr = &stderrBuf

	if err := decodeCmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}

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

	// Wait for FFmpeg to finish, then close the write end of the pipe
	ffmpegErr := decodeCmd.Wait()
	pw.Close()

	suppressErr := <-errCh

	if ffmpegErr != nil {
		if typed := parseFFmpegError(stderrBuf.String()); typed != nil {
			return fmt.Errorf("ffmpeg decode: %w", typed)
		}
		return fmt.Errorf("ffmpeg decode: %w\nstderr: %s", ffmpegErr, stderrBuf.String())
	}
	return suppressErr
}

// encodeAndMux re-encodes the cleaned PCM and muxes it with the original video (if any).
func (p *Processor) encodeAndMux(pcmPath, originalSrc, dst string, info *audio.MediaInfo, outCodec string, outRate int, opts Options, logger *zap.Logger) error {
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

	cmd := exec.Command(p.cfg.FFmpegPath, args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	logger.Debug("ffmpeg encode", zap.Strings("args", args))

	if err := cmd.Run(); err != nil {
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
