// Package clearstream whitebox tests — access unexported fields.
package clearstream

import (
	"errors"
	"io"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/exotel/clearstream/pkg/telemetry"
	"go.uber.org/zap"
)

// TestPoolSize_NilPool exercises the nil pool branch of PoolSize().
func TestPoolSize_NilPool(t *testing.T) {
	cs := &ClearStream{pool: nil}
	if got := cs.PoolSize(); got != 0 {
		t.Errorf("PoolSize() with nil pool = %d, want 0", got)
	}
}

// TestClose_NilPool exercises Close() when pool is nil (model only).
func TestClose_NilPool(t *testing.T) {
	logger, _ := zap.NewProduction()
	sup := model.NewPassthrough()
	cs := &ClearStream{
		cfg:    DefaultConfig(),
		model:  sup,
		pool:   nil,
		logger: logger,
	}
	if err := cs.Close(); err != nil {
		t.Errorf("Close() with nil pool returned error: %v", err)
	}
}

// TestNew_ZeroValueConfigAppliesDefaults verifies New() fills in SampleRate,
// Channels, and FFmpegPath when a caller passes a Config with those fields at
// their zero value -- as opposed to going through DefaultConfig() first. This
// matters because IndiaTelephonyConfig(), WidebandConfig(), and
// CallCenterConfig() are all built as struct literals (not derived from
// DefaultConfig()) and intentionally leave FFmpegPath unset, relying on this
// exact defaulting behavior in New() to end up with a working ffmpeg path.
func TestNew_ZeroValueConfigAppliesDefaults(t *testing.T) {
	cfg := Config{Model: "passthrough"} // SampleRate, Channels, FFmpegPath all zero-value
	cs, err := New(cfg)
	if err != nil {
		t.Fatalf("New() with zero-value Config failed: %v", err)
	}
	defer cs.Close() //nolint:errcheck

	if cs.cfg.SampleRate != 16000 {
		t.Errorf("cfg.SampleRate = %d, want default 16000", cs.cfg.SampleRate)
	}
	if cs.cfg.Channels != 1 {
		t.Errorf("cfg.Channels = %d, want default 1", cs.cfg.Channels)
	}
	if cs.cfg.FFmpegPath != "ffmpeg" {
		t.Errorf("cfg.FFmpegPath = %q, want default \"ffmpeg\"", cs.cfg.FFmpegPath)
	}
}

// TestConfigTelemetry_ReturnsConfiguredSink exercises the non-nil branch of
// Config.telemetry(): when Telemetry is set, the accessor must return that
// exact Sink rather than silently substituting the no-op default. Every SDK
// entry point (ProcessFile, ProcessFileWithOptions, ProcessDirWithOptions,
// NewHTTPHandler) routes through cfg.telemetry(), so a regression here would
// silently drop a caller's configured telemetry backend.
func TestConfigTelemetry_ReturnsConfiguredSink(t *testing.T) {
	sink := telemetry.NewLoggingSink(io.Discard)
	cfg := Config{Telemetry: sink}
	got := cfg.telemetry()
	if got != telemetry.Sink(sink) {
		t.Errorf("telemetry() = %v, want the configured sink %v", got, sink)
	}
}

// errCloseSuppressor is a model.Suppressor whose Close always fails, so
// Close()'s model-error propagation branch can be exercised without a real
// audio backend.
type errCloseSuppressor struct {
	err error
}

func (e *errCloseSuppressor) Process(frame []int16) ([]int16, error) { return frame, nil }
func (e *errCloseSuppressor) Reset()                                 {}
func (e *errCloseSuppressor) Close() error                           { return e.err }
func (e *errCloseSuppressor) Name() string                           { return "err-close" }

// fakePool is a minimal suppressorPool whose Close is scriptable, so
// Close()'s pool-error propagation/aggregation branches can be exercised
// without needing a real *model.SuppressorPool to fail (its Suppressors
// never error on Close in practice).
type fakePool struct {
	closeErr error
}

func (p *fakePool) Acquire() model.Suppressor { return model.NewMockSuppressor() }
func (p *fakePool) Close() error              { return p.closeErr }

// TestClose_ModelCloseError exercises the branch where cs.model.Close()
// itself fails, with no pool present. Prior to this test the error path was
// untested.
func TestClose_ModelCloseError(t *testing.T) {
	wantErr := errors.New("model close failed")
	cs := &ClearStream{
		cfg:   DefaultConfig(),
		model: &errCloseSuppressor{err: wantErr},
		pool:  nil,
	}
	if err := cs.Close(); !errors.Is(err, wantErr) {
		t.Errorf("Close() = %v, want error wrapping %v", err, wantErr)
	}
}

// TestClose_PoolCloseError exercises the branch where the model closes
// cleanly but the pool's Close fails. Prior to this test, a non-nil perr was
// never observed because real Suppressor backends never error on Close.
func TestClose_PoolCloseError(t *testing.T) {
	wantErr := errors.New("pool close failed")
	cs := &ClearStream{
		cfg:   DefaultConfig(),
		model: model.NewPassthrough(),
		pool:  &fakePool{closeErr: wantErr},
	}
	if err := cs.Close(); !errors.Is(err, wantErr) {
		t.Errorf("Close() = %v, want error wrapping %v", err, wantErr)
	}
}

// TestClose_BothCloseErrorsAggregate verifies that when both cs.model.Close()
// and cs.pool.Close() fail, Close() returns an error that wraps both --
// rather than silently dropping the pool error, which is what the previous
// "only assign if err == nil" implementation did.
func TestClose_BothCloseErrorsAggregate(t *testing.T) {
	modelErr := errors.New("model close failed")
	poolErr := errors.New("pool close failed")
	cs := &ClearStream{
		cfg:   DefaultConfig(),
		model: &errCloseSuppressor{err: modelErr},
		pool:  &fakePool{closeErr: poolErr},
	}
	err := cs.Close()
	if !errors.Is(err, modelErr) {
		t.Errorf("Close() = %v, want error wrapping model error %v", err, modelErr)
	}
	if !errors.Is(err, poolErr) {
		t.Errorf("Close() = %v, want error wrapping pool error %v", err, poolErr)
	}
}

// TestPipeline_NegativeVADThresholdDefaultsToSane verifies that a negative
// Config.VADThreshold falls back to the sane default (300) instead of being
// used as-is. RMS energy is never negative, so a negative threshold used
// directly would make VAD.IsSpeech always return true, silently defeating
// VAD (every frame, including pure silence, gets routed through the
// suppressor -- exactly the CPU-saving bypass VAD exists to provide). This
// mirrors the AGC zero vs non-positive defaulting-guard bug fixed in pkg/audio/agc.go.
func TestPipeline_NegativeVADThresholdDefaultsToSane(t *testing.T) {
	cs := &ClearStream{
		cfg: Config{
			SampleRate:   16000,
			Channels:     1,
			EnableVAD:    true,
			VADThreshold: -50,
		},
		model: model.NewPassthrough(),
	}
	p := cs.Pipeline()
	defer p.Close()

	silence := make([]byte, audio.FrameSizeBytes)
	if err := p.ProcessFrames(silence, io.Discard); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}
	stats := p.Stats()
	if stats.FramesSilent != 1 {
		t.Errorf("FramesSilent = %d, want 1 (negative VADThreshold should default to 300)", stats.FramesSilent)
	}
}

// TestConfig_Validate exercises Config.Validate()'s error branches end to
// end, most of which had zero test coverage before this test existed
// (go tool cover reported Validate() at 47.6% statement coverage).
func TestConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"zero value config", Config{}, false},
		{"valid sample rate", Config{SampleRate: 48000}, false},
		{"invalid sample rate", Config{SampleRate: 22050}, true},
		{"valid channels mono", Config{Channels: 1}, false},
		{"valid channels stereo", Config{Channels: 2}, false},
		{"invalid channels zero-negative-like", Config{Channels: 3}, true},
		{"negative max concurrent sessions", Config{MaxConcurrentSessions: -1}, true},
		{"zero max concurrent sessions means default", Config{MaxConcurrentSessions: 0}, false},
		{"unknown model", Config{Model: "not-a-real-model"}, true},
		{"deepfilter without model path", Config{Model: "deepfilter"}, true},
		{"deepfilter with model path", Config{Model: "deepfilter", ModelPath: "/tmp/model.onnx"}, false},
		{"rnnoise-onnx without model path", Config{Model: "rnnoise-onnx"}, true},
		{"rnnoise-onnx with model path", Config{Model: "rnnoise-onnx", ModelPath: "/tmp/model.onnx"}, false},
		{"PCMU at 8000 ok", Config{Codec: "PCMU", SampleRate: 8000}, false},
		{"PCMA at 8000 ok", Config{Codec: "PCMA", SampleRate: 8000}, false},
		{"PCMU at 16000 mismatch", Config{Codec: "PCMU", SampleRate: 16000}, true},
		{"G722 at 16000 ok", Config{Codec: "G722", SampleRate: 16000}, false},
		{"G722 at 8000 mismatch", Config{Codec: "G722", SampleRate: 8000}, true},
		{
			// Regression test: Codec set with SampleRate left unset used to
			// skip the codec/rate cross-check entirely (the old guard was
			// `c.SampleRate != 0 && c.Codec != ""`), so this Config passed
			// Validate() with no error and then silently broke once New()
			// defaulted SampleRate to 16000 -- a mismatch for PCMU, which
			// requires 8000. Validate() must now catch this at the
			// zero-value config stage by checking against the same default
			// New() applies.
			name:    "PCMU with unset SampleRate defaults to 16000 and mismatches",
			cfg:     Config{Codec: "PCMU"},
			wantErr: true,
		},
		{
			// G722 requires 16000, which is exactly the default New()
			// applies when SampleRate is left unset, so this must pass.
			name:    "G722 with unset SampleRate matches the 16000 default",
			cfg:     Config{Codec: "G722"},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Errorf("Validate() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}
