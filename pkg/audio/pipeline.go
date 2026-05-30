package audio

import (
	"fmt"
	"io"

	"github.com/exotel/clearstream/pkg/model"
	"go.uber.org/zap"
)

const (
	// FrameSizeSamples is the number of PCM samples per processing frame (10ms @ 16kHz).
	FrameSizeSamples = 160
	// FrameSizeBytes is FrameSizeSamples * 2 (int16 = 2 bytes).
	FrameSizeBytes = FrameSizeSamples * 2
)

// PipelineConfig configures a Pipeline.
type PipelineConfig struct {
	SampleRate int
	Channels   int
	Suppressor model.Suppressor
	Logger     *zap.Logger
	// VAD is an optional Voice Activity Detector. When set, silence frames
	// bypass the suppressor entirely, saving ~30% CPU on typical calls.
	VAD *VAD
}

// Pipeline processes raw 16kHz mono PCM frames through the AI suppressor.
// Feed frames via Write; read clean frames via Read.
// This is the inner loop used by both the file processor and RTP session.
type Pipeline struct {
	cfg    PipelineConfig
	buf    []byte  // partial frame accumulator
	vad    *VAD
	logger *zap.Logger
}

// NewPipeline creates a new Pipeline.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	return &Pipeline{
		cfg:    cfg,
		buf:    make([]byte, 0, FrameSizeBytes*4),
		vad:    cfg.VAD,
		logger: cfg.Logger,
	}
}

// ProcessFrames reads all available complete frames from in, runs suppression,
// and writes clean PCM to out. Partial trailing data is buffered for the next call.
// If VAD is configured, silence frames bypass suppression to save CPU.
func (p *Pipeline) ProcessFrames(in []byte, out io.Writer) error {
	// Prepend any leftover bytes from last call
	data := append(p.buf, in...)
	p.buf = p.buf[:0]

	offset := 0
	for offset+FrameSizeBytes <= len(data) {
		frame := data[offset : offset+FrameSizeBytes]
		offset += FrameSizeBytes

		// Convert bytes -> int16 samples
		samples := bytesToInt16(frame)

		var cleaned []int16
		if p.vad != nil && !p.vad.IsSpeech(samples) {
			// silence -- pass through without suppression (saves CPU)
			cleaned = samples
		} else {
			var err error
			cleaned, err = p.cfg.Suppressor.Process(samples)
			if err != nil {
				return fmt.Errorf("pipeline: suppress frame: %w", err)
			}
		}

		// Write cleaned frame
		if _, err := out.Write(int16ToBytes(cleaned)); err != nil {
			return fmt.Errorf("pipeline: write frame: %w", err)
		}
	}

	// Buffer leftover bytes
	if offset < len(data) {
		p.buf = append(p.buf, data[offset:]...)
	}
	return nil
}

// Flush processes any buffered partial frame (zero-padded to FrameSizeBytes).
// Call after the last ProcessFrames to drain the buffer.
func (p *Pipeline) Flush(out io.Writer) error {
	if len(p.buf) == 0 {
		return nil
	}
	// Zero-pad to full frame
	frame := make([]byte, FrameSizeBytes)
	copy(frame, p.buf)
	p.buf = p.buf[:0]

	samples := bytesToInt16(frame)
	cleaned, err := p.cfg.Suppressor.Process(samples)
	if err != nil {
		return fmt.Errorf("pipeline: flush suppress: %w", err)
	}
	_, err = out.Write(int16ToBytes(cleaned))
	return err
}

// Reset clears internal state (call when starting a new stream/file).
func (p *Pipeline) Reset() {
	p.buf = p.buf[:0]
	p.cfg.Suppressor.Reset()
	if p.vad != nil {
		p.vad.Reset()
	}
}

// ---- helpers ----------------------------------------------------------------

func bytesToInt16(b []byte) []int16 {
	n := len(b) / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(b[2*i]) | int16(b[2*i+1])<<8
	}
	return out
}

func int16ToBytes(s []int16) []byte {
	out := make([]byte, len(s)*2)
	for i, v := range s {
		out[2*i] = byte(v)
		out[2*i+1] = byte(v >> 8)
	}
	return out
}
