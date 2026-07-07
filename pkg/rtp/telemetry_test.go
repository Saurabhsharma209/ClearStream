package rtp

import (
	"sync"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
	"github.com/exotel/clearstream/pkg/telemetry"
)

// fakeSink is a test telemetry.Sink that records every RecordMetric/RecordEvent
// call into slices, guarded by a mutex since Session may invoke it from more
// than one goroutine (receiveLoop, statsLoop).
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

func (f *fakeSink) metricCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.metrics {
		if m.Name == name {
			n++
		}
	}
	return n
}

func (f *fakeSink) eventCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.events {
		if e.Name == name {
			n++
		}
	}
	return n
}

// TestTelemetryDecodeError verifies that a codec decode failure records an
// errors-total metric (component=rtp, error_type=decode) and fires
// EventRTPDecodeError.
func TestTelemetryDecodeError(t *testing.T) {
	sink := regressSink(t)
	fake := &fakeSink{}
	sess := regressNewSession(t, Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sink.LocalAddr().String(),
		Codec:       audio.CodecG722,
		SampleRate:  16000,
		FFmpegPath:  "/nonexistent/ffmpeg",
		JitterDepth: 1,
		Logger:      silentLogger(t),
		Suppressor:  model.NewMockSuppressor(),
		Telemetry:   fake,
	})

	payload := make([]byte, 160)
	pkt := buildRTPPkt(0, 0, 0xDEAD, 9, payload)
	if err := sess.handlePacket(pkt); err == nil {
		t.Fatalf("expected decode error from bad ffmpeg path, got nil")
	}

	if got := fake.metricCount(telemetry.MetricErrorsTotal); got != 1 {
		t.Errorf("MetricErrorsTotal count = %d, want 1", got)
	}
	if got := fake.eventCount(telemetry.EventRTPDecodeError); got != 1 {
		t.Errorf("EventRTPDecodeError count = %d, want 1", got)
	}
	// Should not be misclassified as a PLC/interruption.
	if got := fake.metricCount(telemetry.MetricRTPPLCTriggeredTotal); got != 0 {
		t.Errorf("MetricRTPPLCTriggeredTotal count = %d, want 0 on decode error", got)
	}
}

// TestTelemetryPLCTriggered verifies that a packet-loss (PLC) event records
// the PLC counter, the interruptions counter tagged cause=plc, and fires
// EventAudioInterruption.
func TestTelemetryPLCTriggered(t *testing.T) {
	sink := regressSink(t)
	fake := &fakeSink{}
	sess := regressNewSession(t, Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sink.LocalAddr().String(),
		PayloadType: 0, // PCMU
		JitterDepth: 1,
		Logger:      silentLogger(t),
		Suppressor:  model.NewMockSuppressor(),
		Telemetry:   fake,
	})

	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF
	}
	const ssrc uint32 = 0x11223344

	// Prime the buffer (depth=1).
	pkt0 := buildRTPPkt(0, 0, ssrc, 0, payload)
	sess.handlePacket(pkt0) //nolint:errcheck

	// Skip seq=1 so the jitter buffer reports a gap (nil payload -> PLC).
	pkt2 := buildRTPPkt(2, 320, ssrc, 0, payload)
	sess.handlePacket(pkt2) //nolint:errcheck

	if got := fake.metricCount(telemetry.MetricRTPPLCTriggeredTotal); got != 1 {
		t.Errorf("MetricRTPPLCTriggeredTotal count = %d, want 1", got)
	}

	found := false
	fake.mu.Lock()
	for _, m := range fake.metrics {
		if m.Name == telemetry.MetricInterruptionsTotal && m.Tags["cause"] == "plc" {
			found = true
		}
	}
	fake.mu.Unlock()
	if !found {
		t.Errorf("expected MetricInterruptionsTotal with tag cause=plc")
	}

	if got := fake.eventCount(telemetry.EventAudioInterruption); got != 1 {
		t.Errorf("EventAudioInterruption count = %d, want 1", got)
	}
	// Decode-error metric should NOT fire on a pure packet-loss/PLC path.
	if got := fake.metricCount(telemetry.MetricErrorsTotal); got != 0 {
		t.Errorf("MetricErrorsTotal count = %d, want 0 on PLC path", got)
	}
}

// TestTelemetryCleanPacketNoSpuriousErrors verifies that normal, successful
// packet handling does not record any error/PLC/decode-error telemetry.
func TestTelemetryCleanPacketNoSpuriousErrors(t *testing.T) {
	sink := regressSink(t)
	fake := &fakeSink{}
	sess := regressNewSession(t, Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sink.LocalAddr().String(),
		PayloadType: 0, // PCMU
		JitterDepth: 1,
		Logger:      silentLogger(t),
		Suppressor:  model.NewMockSuppressor(),
		Telemetry:   fake,
	})

	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = 0xFF
	}
	const ssrc uint32 = 0x55667788
	for i := 0; i < 5; i++ {
		pkt := buildRTPPkt(uint16(i), uint32(i*160), ssrc, 0, payload)
		if err := sess.handlePacket(pkt); err != nil {
			t.Fatalf("handlePacket(%d): unexpected error: %v", i, err)
		}
	}

	if got := fake.metricCount(telemetry.MetricErrorsTotal); got != 0 {
		t.Errorf("MetricErrorsTotal count = %d, want 0 for clean packets", got)
	}
	if got := fake.metricCount(telemetry.MetricRTPPLCTriggeredTotal); got != 0 {
		t.Errorf("MetricRTPPLCTriggeredTotal count = %d, want 0 for clean packets", got)
	}
	if got := fake.eventCount(telemetry.EventRTPDecodeError); got != 0 {
		t.Errorf("EventRTPDecodeError count = %d, want 0 for clean packets", got)
	}
	if got := fake.eventCount(telemetry.EventAudioInterruption); got != 0 {
		t.Errorf("EventAudioInterruption count = %d, want 0 for clean packets", got)
	}
	// The frame-latency histogram should have fired once per handled packet.
	if got := fake.metricCount(telemetry.MetricFrameLatencyMS); got != 5 {
		t.Errorf("MetricFrameLatencyMS count = %d, want 5", got)
	}
}

// TestNoopSinkDefaultWhenTelemetryNil verifies that a Config with no
// Telemetry set still works fine (NoopSink default) and does not panic.
func TestNoopSinkDefaultWhenTelemetryNil(t *testing.T) {
	sink := regressSink(t)
	sess := regressNewSession(t, Config{
		ListenAddr:  "127.0.0.1:0",
		ForwardAddr: sink.LocalAddr().String(),
		PayloadType: 0,
		JitterDepth: 1,
		Logger:      silentLogger(t),
		Suppressor:  model.NewMockSuppressor(),
		// Telemetry intentionally left nil.
	})

	payload := make([]byte, 160)
	pkt := buildRTPPkt(0, 0, 0x9999, 0, payload)
	if err := sess.handlePacket(pkt); err != nil {
		t.Fatalf("handlePacket with nil Telemetry: unexpected error: %v", err)
	}
}
