package telemetry

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNoopSinkDoesNotPanic(t *testing.T) {
	var s Sink = NoopSink{}
	s.RecordMetric(Metric{Name: "x", Value: 1})
	s.RecordEvent(Event{Name: "y"})
}

func TestMultiSinkFansOutAndSkipsNil(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	multi := MultiSink{Sinks: []Sink{NewLoggingSink(&buf1), nil, NewLoggingSink(&buf2)}}

	multi.RecordMetric(Metric{Name: "m1", Value: 42, Kind: MetricGauge})
	multi.RecordEvent(Event{Name: "e1", Severity: SeverityWarn})

	for i, buf := range []*bytes.Buffer{&buf1, &buf2} {
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		if len(lines) != 2 {
			t.Fatalf("sink %d: expected 2 lines, got %d: %q", i, len(lines), buf.String())
		}
	}
}

func TestLoggingSinkRecordMetric(t *testing.T) {
	var buf bytes.Buffer
	sink := NewLoggingSink(&buf)
	sink.RecordMetric(Metric{
		Name: MetricIRQ, Value: 0.02, Unit: "ratio", Kind: MetricGauge,
		Tags: map[string]string{"call_id": "abc123"},
	})

	var decoded logLine
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "metric" || decoded.Name != MetricIRQ {
		t.Fatalf("unexpected decoded line: %+v", decoded)
	}
	if decoded.Value != 0.02 || decoded.Kind != "gauge" {
		t.Fatalf("unexpected value/kind: %+v", decoded)
	}
	if decoded.Tags["call_id"] != "abc123" {
		t.Fatalf("missing tag: %+v", decoded)
	}
	if decoded.Timestamp.IsZero() {
		t.Fatal("expected auto-filled timestamp")
	}
}

func TestLoggingSinkRecordEvent(t *testing.T) {
	var buf bytes.Buffer
	sink := NewLoggingSink(&buf)
	sink.RecordEvent(Event{
		Name: EventAudioInterruption, Severity: SeverityWarn, Message: "PLC fired",
		Fields: map[string]interface{}{"cause": "plc", "duration_ms": 20},
	})

	var decoded logLine
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "event" || decoded.Name != EventAudioInterruption {
		t.Fatalf("unexpected decoded line: %+v", decoded)
	}
	if decoded.Severity != "warn" {
		t.Fatalf("unexpected severity: %+v", decoded)
	}
	if decoded.Fields["cause"] != "plc" {
		t.Fatalf("missing field: %+v", decoded)
	}
}

func TestLoggingSinkPreservesExplicitTimestamp(t *testing.T) {
	var buf bytes.Buffer
	sink := NewLoggingSink(&buf)
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sink.RecordMetric(Metric{Name: "x", Timestamp: ts})

	var decoded logLine
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !decoded.Timestamp.Equal(ts) {
		t.Fatalf("expected preserved timestamp %v, got %v", ts, decoded.Timestamp)
	}
}

func TestStartTimerRecordsHistogramMetric(t *testing.T) {
	var buf bytes.Buffer
	sink := NewLoggingSink(&buf)
	stop := StartTimer(sink, MetricFrameLatencyMS, map[string]string{"call_id": "abc"})
	time.Sleep(2 * time.Millisecond)
	stop()

	var decoded logLine
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Name != MetricFrameLatencyMS || decoded.Kind != "histogram" {
		t.Fatalf("unexpected decoded line: %+v", decoded)
	}
	if decoded.Value <= 0 {
		t.Fatalf("expected positive elapsed ms, got %v", decoded.Value)
	}
}

func TestStartTimerNilSinkDoesNotPanic(t *testing.T) {
	stop := StartTimer(nil, MetricFrameLatencyMS, nil)
	stop() // must not panic
}

func TestMetricKindString(t *testing.T) {
	cases := map[MetricKind]string{
		MetricCounter:   "counter",
		MetricGauge:     "gauge",
		MetricHistogram: "histogram",
		MetricKind(99):  "unknown",
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Errorf("MetricKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}

func TestSeverityString(t *testing.T) {
	cases := map[Severity]string{
		SeverityInfo:     "info",
		SeverityWarn:     "warn",
		SeverityError:    "error",
		SeverityCritical: "critical",
		Severity(99):     "unknown",
	}
	for sev, want := range cases {
		if got := sev.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", sev, got, want)
		}
	}
}
