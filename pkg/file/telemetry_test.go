package file_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/exotel/clearstream/pkg/file"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/exotel/clearstream/pkg/telemetry"
	"go.uber.org/zap"
)

// fakeSink records every metric/event it receives; safe for concurrent use.
type fakeSink struct {
	mu      sync.Mutex
	metrics []telemetry.Metric
	events  []telemetry.Event
}

func (f *fakeSink) RecordMetric(m telemetry.Metric) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metrics = append(f.metrics, m)
}

func (f *fakeSink) RecordEvent(e telemetry.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakeSink) hasMetric(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.metrics {
		if m.Name == name {
			return true
		}
	}
	return false
}

func (f *fakeSink) hasEvent(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.Name == name {
			return true
		}
	}
	return false
}

func newTelemetryTestProcessor() *file.Processor {
	return file.NewProcessor(file.ProcessorConfig{
		FFmpegPath: "ffmpeg",
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Logger:     zap.NewNop(),
	})
}

// TestProcessDirRecordsTelemetry verifies that ProcessDir records worker-pool
// gauges, batch progress, and a batch-file-failed event when a file fails to
// probe. A garbage file with a supported extension fails ffmpeg probing
// regardless of whether ffmpeg is actually installed, so this test does not
// require ffmpeg to be present.
func TestProcessDirRecordsTelemetry(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	badFile := filepath.Join(src, "bad.wav")
	if err := os.WriteFile(badFile, []byte("not audio"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	sink := &fakeSink{}
	p := newTelemetryTestProcessor()

	errs := p.ProcessDir(src, dst, file.Options{Telemetry: sink})
	if len(errs) != 1 || errs[0] == nil {
		t.Fatalf("expected one error for the bad file, got %v", errs)
	}

	if !sink.hasEvent(telemetry.EventBatchFileFailed) {
		t.Error("expected EventBatchFileFailed to be recorded")
	}
	if !sink.hasMetric(telemetry.MetricWorkerPoolActive) {
		t.Error("expected MetricWorkerPoolActive to be recorded")
	}
	if !sink.hasMetric(telemetry.MetricWorkerPoolQueueDepth) {
		t.Error("expected MetricWorkerPoolQueueDepth to be recorded")
	}
	if !sink.hasMetric(telemetry.MetricBatchProgressPercent) {
		t.Error("expected MetricBatchProgressPercent to be recorded")
	}
}

// TestProcessDirFullRecordsTelemetry is the ProcessDirFull equivalent of
// TestProcessDirRecordsTelemetry.
func TestProcessDirFullRecordsTelemetry(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	badFile := filepath.Join(src, "bad.mp3")
	if err := os.WriteFile(badFile, []byte("not audio"), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	sink := &fakeSink{}
	p := newTelemetryTestProcessor()

	results := p.ProcessDirFull(src, dst, file.Options{Telemetry: sink})
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected one failed result, got %+v", results)
	}

	if !sink.hasEvent(telemetry.EventBatchFileFailed) {
		t.Error("expected EventBatchFileFailed to be recorded")
	}
	if !sink.hasMetric(telemetry.MetricWorkerPoolQueueDepth) {
		t.Error("expected MetricWorkerPoolQueueDepth to be recorded")
	}
}

// TestOptionsTelemetryDefaultsToNoop verifies ProcessDir does not panic when
// no Telemetry sink is configured (falls back to telemetry.NoopSink).
func TestOptionsTelemetryDefaultsToNoop(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	p := newTelemetryTestProcessor()
	if errs := p.ProcessDir(src, dst, file.Options{}); len(errs) != 0 {
		t.Errorf("expected no errors for empty dir, got %v", errs)
	}
}
