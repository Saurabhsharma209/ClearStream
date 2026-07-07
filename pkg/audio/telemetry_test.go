package audio_test

import (
	"bytes"
	"sync"
	"testing"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/model"
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

// TestPipelineRecordsFrameLatency verifies ProcessFrames reports a
// MetricFrameLatencyMS histogram observation to a configured Telemetry sink.
func TestPipelineRecordsFrameLatency(t *testing.T) {
	sink := &fakeSink{}
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Telemetry:  sink,
	})

	input := makeFrame(audio.FrameSizeBytes * 2)
	var out bytes.Buffer
	if err := p.ProcessFrames(input, &out); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}

	if !sink.hasMetric(telemetry.MetricFrameLatencyMS) {
		t.Errorf("expected %s metric to be recorded, got metrics: %+v", telemetry.MetricFrameLatencyMS, sink.metrics)
	}
}

// TestPipelineRecordsVADSpeechRatio verifies that when a VAD is configured,
// ProcessFrames reports a MetricAudioVADSpeechRatio gauge observation.
func TestPipelineRecordsVADSpeechRatio(t *testing.T) {
	sink := &fakeSink{}
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
		Telemetry:  sink,
		VADConfig:  &audio.VADConfig{EnergyThreshold: 100},
	})

	input := makeFrame(audio.FrameSizeBytes * 2)
	var out bytes.Buffer
	if err := p.ProcessFrames(input, &out); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}

	if !sink.hasMetric(telemetry.MetricAudioVADSpeechRatio) {
		t.Errorf("expected %s metric to be recorded when VAD configured, got metrics: %+v", telemetry.MetricAudioVADSpeechRatio, sink.metrics)
	}
}

// TestPipelineNoTelemetryDefaultsToNoop verifies that leaving Telemetry unset
// does not panic and behaves like telemetry.NoopSink{}.
func TestPipelineNoTelemetryDefaultsToNoop(t *testing.T) {
	p := audio.NewPipeline(audio.PipelineConfig{
		SampleRate: 16000,
		Channels:   1,
		Suppressor: model.NewPassthrough(),
	})

	input := makeFrame(audio.FrameSizeBytes * 2)
	var out bytes.Buffer
	if err := p.ProcessFrames(input, &out); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}
}

// TestABRunnerRecordsSNRImprovement verifies ABRunner.ProcessFrame reports
// MetricAudioSNRImprovementDB histogram observations for both suppressors.
func TestABRunnerRecordsSNRImprovement(t *testing.T) {
	sink := &fakeSink{}
	runner := audio.NewABRunner(model.NewPassthrough(), model.NewPassthrough(), audio.ABConfig{
		SilenceThresh:          50,
		SpeechThresh:           280,
		SpeechDegradationLimit: 0.05,
		Telemetry:              sink,
	})

	raw := make([]int16, audio.FrameSizeSamples)
	for i := range raw {
		raw[i] = int16(300 + i%50)
	}
	runner.ProcessFrame(0, raw)

	if !sink.hasMetric(telemetry.MetricAudioSNRImprovementDB) {
		t.Errorf("expected %s metric to be recorded, got metrics: %+v", telemetry.MetricAudioSNRImprovementDB, sink.metrics)
	}
}
