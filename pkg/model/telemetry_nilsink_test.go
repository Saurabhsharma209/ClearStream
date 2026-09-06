package model

import (
	"testing"

	"github.com/exotel/clearstream/pkg/telemetry"
)

// TestNewInstrumentedSuppressor_NilSinkDefaultsToNoop locks in the
// documented nil-sink defaulting behavior of NewInstrumentedSuppressor: a
// nil sink must be replaced with telemetry.NoopSink{} so Process and Reset
// never panic on a nil Sink field, and struct literals built without this
// constructor (Sink is exported and easy to leave zero-valued) hit the same
// fallback inside Reset. Before this test, NewInstrumentedSuppressor's
// nil-check branch had no direct coverage.
func TestNewInstrumentedSuppressor_NilSinkDefaultsToNoop(t *testing.T) {
	inner := NewMockSuppressor()
	instrumented := NewInstrumentedSuppressor(inner, nil, nil)

	if _, ok := instrumented.Sink.(telemetry.NoopSink); !ok {
		t.Fatalf("expected Sink to default to telemetry.NoopSink{}, got %T", instrumented.Sink)
	}

	// Process and Reset must not panic with the defaulted nil sink.
	if _, err := instrumented.Process([]int16{1, 2, 3}); err != nil {
		t.Fatalf("Process with defaulted nil sink: unexpected error: %v", err)
	}
	instrumented.Reset()

	if inner.ProcessCalls != 1 {
		t.Errorf("expected wrapped Suppressor.Process called once, got %d", inner.ProcessCalls)
	}
	if inner.ResetCalls != 1 {
		t.Errorf("expected wrapped Suppressor.Reset called once, got %d", inner.ResetCalls)
	}
}
