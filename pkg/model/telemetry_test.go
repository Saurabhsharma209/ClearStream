package model

import (
	"sync"
	"testing"

	"github.com/exotel/clearstream/pkg/telemetry"
)

// fakeSink is a test double for telemetry.Sink that records every
// RecordMetric/RecordEvent call into slices for later inspection.
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

// TestInstrumentedSuppressorRecordsInferenceLatency verifies Process reports
// a MetricModelInferenceLatencyMS histogram observation and still delegates
// to the wrapped Suppressor.
func TestInstrumentedSuppressorRecordsInferenceLatency(t *testing.T) {
	mock := NewMockSuppressor()
	sink := &fakeSink{}
	sup := NewInstrumentedSuppressor(mock, sink, map[string]string{"backend": "mock"})

	frame := []int16{100, 200, 300}
	out, err := sup.Process(frame)
	if err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if len(out) != len(frame) {
		t.Errorf("Process output length = %d, want %d", len(out), len(frame))
	}
	if mock.ProcessCalls != 1 {
		t.Errorf("underlying ProcessCalls = %d, want 1", mock.ProcessCalls)
	}
	if !sink.hasMetric(telemetry.MetricModelInferenceLatencyMS) {
		t.Errorf("expected %s metric to be recorded, got: %+v", telemetry.MetricModelInferenceLatencyMS, sink.metrics)
	}
}

// TestInstrumentedSuppressorResetRecordsMetricAndEvent verifies Reset
// delegates to the wrapped Suppressor and records both the resets-total
// counter and the reset event.
func TestInstrumentedSuppressorResetRecordsMetricAndEvent(t *testing.T) {
	mock := NewMockSuppressor()
	sink := &fakeSink{}
	sup := NewInstrumentedSuppressor(mock, sink, nil)

	sup.Reset()

	if mock.ResetCalls != 1 {
		t.Errorf("underlying ResetCalls = %d, want 1", mock.ResetCalls)
	}
	if !sink.hasMetric(telemetry.MetricSuppressorResetsTotal) {
		t.Errorf("expected %s metric to be recorded, got: %+v", telemetry.MetricSuppressorResetsTotal, sink.metrics)
	}
	if !sink.hasEvent(telemetry.EventSuppressorReset) {
		t.Errorf("expected %s event to be recorded, got: %+v", telemetry.EventSuppressorReset, sink.events)
	}
}

// TestNewSuppressorWithTelemetryInitFailure verifies that a construction
// failure fires EventSuppressorInitFailed and still returns the underlying
// error unwrapped.
func TestNewSuppressorWithTelemetryInitFailure(t *testing.T) {
	sink := &fakeSink{}
	_, err := NewSuppressorWithTelemetry(SuppressorConfig{Backend: "unknown-backend"}, sink, nil)
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
	if !sink.hasEvent(telemetry.EventSuppressorInitFailed) {
		t.Errorf("expected %s event to be recorded, got: %+v", telemetry.EventSuppressorInitFailed, sink.events)
	}
}

// TestNewSuppressorWithTelemetrySuccess verifies a successful construction
// returns a working, instrumented Suppressor without firing the init-failed
// event.
func TestNewSuppressorWithTelemetrySuccess(t *testing.T) {
	sink := &fakeSink{}
	sup, err := NewSuppressorWithTelemetry(SuppressorConfig{Backend: "passthrough"}, sink, nil)
	if err != nil {
		t.Fatalf("NewSuppressorWithTelemetry: %v", err)
	}
	if _, ok := sup.(*InstrumentedSuppressor); !ok {
		t.Errorf("expected *InstrumentedSuppressor, got %T", sup)
	}
	if _, err := sup.Process([]int16{1, 2, 3}); err != nil {
		t.Fatalf("Process error: %v", err)
	}
	if sink.hasEvent(telemetry.EventSuppressorInitFailed) {
		t.Error("unexpected init-failed event on successful construction")
	}
	if !sink.hasMetric(telemetry.MetricModelInferenceLatencyMS) {
		t.Errorf("expected %s metric to be recorded", telemetry.MetricModelInferenceLatencyMS)
	}
}
