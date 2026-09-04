// Package clearstream whitebox tests — access unexported fields.
package clearstream

import (
	"io"
	"testing"

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
