package telemetry

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// NoopSink discards all metrics and events. It is the zero-value default
// used throughout ClearStream when no Sink is configured, so telemetry has
// no observable cost unless an application opts in.
type NoopSink struct{}

// RecordMetric discards m.
func (NoopSink) RecordMetric(m Metric) {}

// RecordEvent discards e.
func (NoopSink) RecordEvent(e Event) {}

// MultiSink fans out every metric/event to multiple Sinks — useful for e.g.
// sending to both a real backend adapter and a debug LoggingSink
// simultaneously. Nil entries in Sinks are skipped.
type MultiSink struct {
	Sinks []Sink
}

// RecordMetric forwards m to every non-nil Sink in Sinks.
func (m MultiSink) RecordMetric(metric Metric) {
	for _, s := range m.Sinks {
		if s != nil {
			s.RecordMetric(metric)
		}
	}
}

// RecordEvent forwards e to every non-nil Sink in Sinks.
func (m MultiSink) RecordEvent(e Event) {
	for _, s := range m.Sinks {
		if s != nil {
			s.RecordEvent(e)
		}
	}
}

// LoggingSink is a minimal, dependency-free reference Sink implementation
// that writes one JSON object per line to an io.Writer. It's suitable for
// local debugging or as a starting template for a real backend adapter
// (Prometheus remote-write, StatsD, CloudWatch EMF, etc.) — copy the
// RecordMetric/RecordEvent bodies and swap the json.Marshal+Write for your
// backend's SDK call.
type LoggingSink struct {
	mu sync.Mutex
	w  io.Writer
}

// NewLoggingSink returns a LoggingSink writing newline-delimited JSON to w.
// Writes are serialized with an internal mutex, so w does not need to be
// concurrency-safe itself.
func NewLoggingSink(w io.Writer) *LoggingSink {
	return &LoggingSink{w: w}
}

type logLine struct {
	Type      string                 `json:"type"`
	Name      string                 `json:"name"`
	Value     float64                `json:"value,omitempty"`
	Unit      string                 `json:"unit,omitempty"`
	Kind      string                 `json:"kind,omitempty"`
	Severity  string                 `json:"severity,omitempty"`
	Message   string                 `json:"message,omitempty"`
	Tags      map[string]string      `json:"tags,omitempty"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// RecordMetric writes m as one JSON line.
func (l *LoggingSink) RecordMetric(m Metric) {
	ts := m.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	l.write(logLine{
		Type: "metric", Name: m.Name, Value: m.Value, Unit: m.Unit,
		Kind: m.Kind.String(), Tags: m.Tags, Timestamp: ts,
	})
}

// RecordEvent writes e as one JSON line.
func (l *LoggingSink) RecordEvent(e Event) {
	ts := e.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	l.write(logLine{
		Type: "event", Name: e.Name, Severity: e.Severity.String(),
		Message: e.Message, Fields: e.Fields, Timestamp: ts,
	})
}

func (l *LoggingSink) write(line logLine) {
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w.Write(b)
	l.w.Write([]byte("\n"))
}
