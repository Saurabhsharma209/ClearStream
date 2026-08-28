package model

import (
	"time"

	"github.com/exotel/clearstream/pkg/telemetry"
)

// InstrumentedSuppressor wraps a Suppressor with telemetry instrumentation:
// each Process call is timed and reported as MetricModelInferenceLatencyMS,
// isolating model inference cost from any resample/codec overhead in the
// caller; each Reset increments MetricSuppressorResetsTotal and fires
// EventSuppressorReset. It is a thin, additive decorator: it does not
// change suppression behavior or delegate calls other than Process/Reset.
type InstrumentedSuppressor struct {
	Suppressor
	Sink telemetry.Sink
	Tags map[string]string
}

// NewInstrumentedSuppressor wraps s so Process latency and Reset calls are
// reported to sink. If sink is nil, telemetry.NoopSink{} is used, matching
// the zero-observable-cost default used throughout ClearStream. tags are
// attached to every metric/event emitted (e.g. {"backend": s.Name()}).
func NewInstrumentedSuppressor(s Suppressor, sink telemetry.Sink, tags map[string]string) *InstrumentedSuppressor {
	if sink == nil {
		sink = telemetry.NoopSink{}
	}
	return &InstrumentedSuppressor{Suppressor: s, Sink: sink, Tags: tags}
}

// Process runs the wrapped Suppressor Process method, recording wall-clock
// inference latency as MetricModelInferenceLatencyMS.
func (i *InstrumentedSuppressor) Process(frame []int16) ([]int16, error) {
	stop := telemetry.StartTimer(i.Sink, telemetry.MetricModelInferenceLatencyMS, i.Tags)
	defer stop()
	return i.Suppressor.Process(frame)
}

// Reset clears the wrapped Suppressor state, then records a
// MetricSuppressorResetsTotal counter increment and fires EventSuppressorReset.
func (i *InstrumentedSuppressor) Reset() {
	i.Suppressor.Reset()
	sink := i.Sink
	if sink == nil {
		// InstrumentedSuppressor can be constructed as a struct literal
		// (its fields are exported) without going through
		// NewInstrumentedSuppressor's nil-to-NoopSink default, unlike
		// Process (which is protected by telemetry.StartTimer's own nil
		// check). Mirror that same nil-safety here instead of panicking.
		sink = telemetry.NoopSink{}
	}
	sink.RecordMetric(telemetry.Metric{
		Name:      telemetry.MetricSuppressorResetsTotal,
		Value:     1,
		Unit:      "count",
		Kind:      telemetry.MetricCounter,
		Tags:      i.Tags,
		Timestamp: time.Now(),
	})
	sink.RecordEvent(telemetry.Event{
		Name:      telemetry.EventSuppressorReset,
		Severity:  telemetry.SeverityInfo,
		Message:   "suppressor reset",
		Fields:    map[string]interface{}{"reason": "reset"},
		Timestamp: time.Now(),
	})
}

// NewSuppressorWithTelemetry constructs a Suppressor via NewSuppressor and
// wraps the result with telemetry instrumentation (see InstrumentedSuppressor).
// If construction fails, it fires EventSuppressorInitFailed on sink before
// returning the error unwrapped, so callers can react to init failure exactly
// as they would with plain NewSuppressor.
func NewSuppressorWithTelemetry(cfg SuppressorConfig, sink telemetry.Sink, tags map[string]string) (Suppressor, error) {
	if sink == nil {
		sink = telemetry.NoopSink{}
	}
	s, err := NewSuppressor(cfg)
	if err != nil {
		sink.RecordEvent(telemetry.Event{
			Name:      telemetry.EventSuppressorInitFailed,
			Severity:  telemetry.SeverityError,
			Message:   err.Error(),
			Fields:    map[string]interface{}{"backend": cfg.Backend},
			Timestamp: time.Now(),
		})
		return nil, err
	}
	return NewInstrumentedSuppressor(s, sink, tags), nil
}
