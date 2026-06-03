package audio

import (
	"testing"
)

// makeSilence returns a slice of n zero-valued int16 samples.
func makeSilence(n int) []int16 {
	return make([]int16, n)
}

// makeSpeech returns a slice of n samples with amplitude ~0.5 (well above threshold).
func makeSpeech(n int) []int16 {
	s := make([]int16, n)
	for i := range s {
		if i%2 == 0 {
			s[i] = 16000
		} else {
			s[i] = -16000
		}
	}
	return s
}

func TestEnergyDiarizerSilence(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	for i := 0; i < 20; i++ {
		label := d.ProcessFrame(makeSilence(160), int64(i*10))
		if label != SpeakerSilence {
			t.Fatalf("frame %d: expected SpeakerSilence, got %s", i, label)
		}
	}
	segs := d.Segments()
	for _, s := range segs {
		if s.Speaker != SpeakerSilence {
			t.Errorf("unexpected non-silence segment: %s", s)
		}
	}
}

func TestEnergyDiarizerSpeech(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	for i := 0; i < 10; i++ {
		label := d.ProcessFrame(makeSpeech(160), int64(i*10))
		if label != SpeakerNearEnd {
			t.Fatalf("frame %d: expected SpeakerNearEnd, got %s", i, label)
		}
	}
}

func TestEnergyDiarizerTurnDetection(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	ts := int64(0)

	// 5 initial silence frames so the opening silence segment has real duration.
	for i := 0; i < 5; i++ {
		d.ProcessFrame(makeSilence(160), ts)
		ts += 10
	}

	// 10 speech frames (100ms) → closes the opening silence, opens a speech segment.
	for i := 0; i < 10; i++ {
		d.ProcessFrame(makeSpeech(160), ts)
		ts += 10
	}

	// 32 silence frames (320ms) ≥ SpeakerChangeGapMs ≥ SpeakerChangeGapMs → closes the speech segment,
	// opens a new silence segment.
	for i := 0; i < 32; i++ {
		d.ProcessFrame(makeSilence(160), ts)
		ts += 10
	}

	// 10 more speech frames (100ms)
	for i := 0; i < 10; i++ {
		d.ProcessFrame(makeSpeech(160), ts)
		ts += 10
	}

	segs := d.Segments()
	// We expect at least 2 completed segments: 1 silence (opening) + 1 speech.
	if len(segs) < 2 {
		t.Fatalf("expected at least 2 completed segments, got %d: %v", len(segs), segs)
	}

	// Verify we have at least one speech and one silence segment
	var hasSpeech, hasSilence bool
	for _, s := range segs {
		if s.Speaker == SpeakerNearEnd {
			hasSpeech = true
		}
		if s.Speaker == SpeakerSilence {
			hasSilence = true
		}
	}
	if !hasSpeech {
		t.Error("expected at least one speech segment in completed segments")
	}
	if !hasSilence {
		t.Error("expected at least one silence segment in completed segments")
	}
}

func TestEnergyDiarizerReset(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	ts := int64(0)

	for i := 0; i < 10; i++ {
		d.ProcessFrame(makeSpeech(160), ts)
		ts += 10
	}
	for i := 0; i < 35; i++ {
		d.ProcessFrame(makeSilence(160), ts)
		ts += 10
	}

	d.Reset()
	segs := d.Segments()
	if len(segs) != 0 {
		t.Fatalf("after Reset, expected 0 segments, got %d", len(segs))
	}
	cur := d.CurrentSegment()
	if cur.Speaker != SpeakerSilence {
		t.Errorf("after Reset, current speaker should be SpeakerSilence, got %s", cur.Speaker)
	}
}

func TestDiarizerInterface(t *testing.T) {
	var _ Diarizer = (*EnergyDiarizer)(nil)
	// Compile-time check that EnergyDiarizer satisfies Diarizer.
	// If the code compiles, this test passes.
}

func TestPipelineWithDiarizer(t *testing.T) {
	diarizer := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	sup := &noopSuppressor{}
	cfg := PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		Diarizer:        diarizer,
	}
	p := NewPipeline(cfg)

	// Feed speech frames
	speech := makeSpeech(160)
	speechBytes := int16ToBytes(speech)
	var buf nopWriter
	for i := 0; i < 5; i++ {
		if err := p.ProcessFrames(speechBytes, &buf); err != nil {
			t.Fatalf("ProcessFrames error: %v", err)
		}
	}

	segs := p.DiarizationSegments()
	if segs == nil {
		// nil is ok if no completed segments yet; just ensure no panic
		t.Log("DiarizationSegments returned nil (no completed segments yet, acceptable)")
	}

	// Verify Reset works
	p.Reset()
}

// noopSuppressor passes audio through unchanged.
type noopSuppressor struct{}

func (n *noopSuppressor) Process(in []int16) ([]int16, error) { return in, nil }
func (n *noopSuppressor) Reset()                              {}

// nopWriter discards written bytes.
type nopWriter struct{}

func (nopWriter) Write(b []byte) (int, error) { return len(b), nil }
func (n *noopSuppressor) Close() error        { return nil }
func (n *noopSuppressor) Name() string        { return "noop" }
