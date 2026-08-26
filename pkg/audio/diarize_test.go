package audio

import (
	"strings"
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

// --- Two-channel (far-end aware) diarization tests ---

func TestFarEndAwareDiarizerInterface(t *testing.T) {
	var _ FarEndAwareDiarizer = (*EnergyDiarizer)(nil)
	// Compile-time check that EnergyDiarizer satisfies FarEndAwareDiarizer.
}

// TestEnergyDiarizerFarEndOnly verifies that when the near-end mic is
// silent but an active far-end reference is supplied via SetFarEndRMS, the
// frame is attributed to SpeakerFarEnd rather than silently staying
// SpeakerSilence -- this is the two-channel diarization gap that previously
// had no concrete implementation despite being documented on EnergyDiarizer.
func TestEnergyDiarizerFarEndOnly(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	ts := int64(0)
	for i := 0; i < 10; i++ {
		d.SetFarEndRMS(makeSpeech(160))
		label := d.ProcessFrame(makeSilence(160), ts)
		if label != SpeakerFarEnd {
			t.Fatalf("frame %d: expected SpeakerFarEnd, got %s", i, label)
		}
		ts += 10
	}
	cur := d.CurrentSegment()
	if cur.Speaker != SpeakerFarEnd {
		t.Errorf("expected ongoing segment SpeakerFarEnd, got %s", cur.Speaker)
	}
}

// TestEnergyDiarizerNearEndPriority verifies that when both near-end and
// far-end are simultaneously active, the near-end mic (post-AEC) takes
// priority -- matching the documented attribution rule on SetFarEndRMS.
func TestEnergyDiarizerNearEndPriority(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	d.SetFarEndRMS(makeSpeech(160))
	label := d.ProcessFrame(makeSpeech(160), 0)
	if label != SpeakerNearEnd {
		t.Fatalf("expected SpeakerNearEnd to take priority over active far-end, got %s", label)
	}
}

// TestEnergyDiarizerFarEndDefaultsToSilence verifies backward compatibility:
// a diarizer that never has SetFarEndRMS called behaves exactly as before
// (near-end mic energy alone determines near/silence classification).
func TestEnergyDiarizerFarEndDefaultsToSilence(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	label := d.ProcessFrame(makeSilence(160), 0)
	if label != SpeakerSilence {
		t.Fatalf("expected SpeakerSilence with no far-end reference supplied, got %s", label)
	}
}

// TestEnergyDiarizerFarEndTurnTransition verifies a direct far-end -> near-end
// handover (double-talk resolving to near-end) correctly closes the far-end
// segment and opens a near-end one, and that Reset() clears the far-end RMS
// state so a reused diarizer instance doesn't leak stale far-end energy into
// a new call leg.
func TestEnergyDiarizerFarEndTurnTransition(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	ts := int64(0)

	// Far-end only for 100ms.
	for i := 0; i < 10; i++ {
		d.SetFarEndRMS(makeSpeech(160))
		d.ProcessFrame(makeSilence(160), ts)
		ts += 10
	}
	// Direct handover to near-end speech (far-end drops out simultaneously).
	for i := 0; i < 10; i++ {
		d.SetFarEndRMS(makeSilence(160))
		label := d.ProcessFrame(makeSpeech(160), ts)
		if label != SpeakerNearEnd {
			t.Fatalf("frame %d: expected SpeakerNearEnd after handover, got %s", i, label)
		}
		ts += 10
	}

	segs := d.Segments()
	var hasFarEnd bool
	for _, s := range segs {
		if s.Speaker == SpeakerFarEnd {
			hasFarEnd = true
		}
	}
	if !hasFarEnd {
		t.Errorf("expected a completed SpeakerFarEnd segment before the handover, got %v", segs)
	}

	d.Reset()
	// After Reset, far-end RMS must not leak into the next call leg: a
	// silent near-end frame with no fresh SetFarEndRMS call must be silence.
	label := d.ProcessFrame(makeSilence(160), 0)
	if label != SpeakerSilence {
		t.Fatalf("expected SpeakerSilence after Reset (stale far-end RMS leaked), got %s", label)
	}
}

// TestPipelineDiarizerFarEndWiring verifies Pipeline.ProcessFrames feeds the
// far-end reference set via Pipeline.SetFarEnd into a FarEndAwareDiarizer
// (e.g. EnergyDiarizer) each frame, so two-channel diarization works
// end-to-end through the pipeline, not just when driving EnergyDiarizer
// directly.
func TestPipelineDiarizerFarEndWiring(t *testing.T) {
	diarizer := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	sup := &noopSuppressor{}
	cfg := PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		Diarizer:        diarizer,
	}
	p := NewPipeline(cfg)

	p.SetFarEnd(makeSpeech(160))

	silentBytes := int16ToBytes(makeSilence(160))
	var buf nopWriter
	for i := 0; i < 5; i++ {
		if err := p.ProcessFrames(silentBytes, &buf); err != nil {
			t.Fatalf("ProcessFrames error: %v", err)
		}
	}

	cur := diarizer.CurrentSegment()
	if cur.Speaker != SpeakerFarEnd {
		t.Errorf("expected pipeline to wire far-end reference into diarizer, got current segment speaker %s", cur.Speaker)
	}
}

// TestPipelineDiarizerNoFarEndUnaffected verifies that when no far-end
// reference has ever been set, a configured diarizer behaves exactly as
// before this change (single-channel, silence for a silent near-end frame).
func TestPipelineDiarizerNoFarEndUnaffected(t *testing.T) {
	diarizer := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	sup := &noopSuppressor{}
	cfg := PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		Diarizer:        diarizer,
	}
	p := NewPipeline(cfg)

	silentBytes := int16ToBytes(makeSilence(160))
	var buf nopWriter
	if err := p.ProcessFrames(silentBytes, &buf); err != nil {
		t.Fatalf("ProcessFrames error: %v", err)
	}

	cur := diarizer.CurrentSegment()
	if cur.Speaker != SpeakerSilence {
		t.Errorf("expected SpeakerSilence with no far-end reference ever set, got %s", cur.Speaker)
	}
}

// TestPipelineResetClearsDiarizerState is a regression test for a bug where
// Pipeline.Reset() reset every other stage (suppressor, VAD, AGC, AEC, noise
// reducer, tiered NR, limiter, turnEnd) but never called diarizer.Reset().
// A Pipeline reused across call legs (the documented use of Reset()) would
// silently carry the previous call's speaker/segment state into the new
// one instead of restarting at SpeakerSilence.
func TestPipelineResetClearsDiarizerState(t *testing.T) {
	diarizer := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	sup := &noopSuppressor{}
	cfg := PipelineConfig{
		SampleRate:      16000,
		InputSampleRate: 16000,
		Suppressor:      sup,
		Diarizer:        diarizer,
	}
	p := NewPipeline(cfg)

	speechBytes := int16ToBytes(makeSpeech(160))
	var buf nopWriter
	for i := 0; i < 5; i++ {
		if err := p.ProcessFrames(speechBytes, &buf); err != nil {
			t.Fatalf("ProcessFrames error: %v", err)
		}
	}

	if cur := diarizer.CurrentSegment(); cur.Speaker != SpeakerNearEnd {
		t.Fatalf("setup: expected diarizer mid-speech (SpeakerNearEnd) before Reset, got %s", cur.Speaker)
	}

	p.Reset()

	if cur := diarizer.CurrentSegment(); cur.Speaker != SpeakerSilence {
		t.Errorf("Pipeline.Reset() did not reset the diarizer: CurrentSegment().Speaker = %s, want %s (bug: Reset() never called diarizer.Reset(), so speaker state leaked across call legs)", cur.Speaker, SpeakerSilence)
	}
}

func TestDiarizedSegmentString(t *testing.T) {
	// Closed segment
	s := DiarizedSegment{Speaker: SpeakerNearEnd, StartMs: 100, EndMs: 500, EnergyRMS: 0.5}
	str := s.String()
	if !strings.Contains(str, "near") {
		t.Errorf("DiarizedSegment.String() missing speaker: %q", str)
	}
	if !strings.Contains(str, "400") { // duration 400ms
		t.Errorf("DiarizedSegment.String() missing duration: %q", str)
	}

	// Ongoing segment (EndMs == -1)
	ongoing := DiarizedSegment{Speaker: SpeakerFarEnd, StartMs: 200, EndMs: -1, EnergyRMS: 0.3}
	str2 := ongoing.String()
	if !strings.Contains(str2, "ongoing") {
		t.Errorf("ongoing DiarizedSegment.String() missing 'ongoing': %q", str2)
	}
}

func TestDiarizeReportEmpty(t *testing.T) {
	got := DiarizeReport(nil)
	if got != "no segments" {
		t.Errorf("DiarizeReport(nil) = %q, want %q", got, "no segments")
	}
	got2 := DiarizeReport([]DiarizedSegment{})
	if got2 != "no segments" {
		t.Errorf("DiarizeReport([]) = %q, want %q", got2, "no segments")
	}
}

func TestDiarizeReportWithSegments(t *testing.T) {
	segs := []DiarizedSegment{
		{Speaker: SpeakerNearEnd, StartMs: 0, EndMs: 100, EnergyRMS: 0.5},
		{Speaker: SpeakerSilence, StartMs: 100, EndMs: 500, EnergyRMS: 0.0},
	}
	got := DiarizeReport(segs)
	if !strings.Contains(got, "segments:") {
		t.Errorf("DiarizeReport expected 'segments:', got: %q", got)
	}
	if !strings.Contains(got, "near") {
		t.Errorf("DiarizeReport expected 'near' in output, got: %q", got)
	}
}

func TestTimeMs(t *testing.T) {
	ts := timeMs()
	if ts <= 0 {
		t.Errorf("timeMs() = %d, want positive int64", ts)
	}
}

func TestEnergyDiarizerStats(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	ts := int64(0)

	// 5 silence frames (50ms)
	for i := 0; i < 5; i++ {
		d.ProcessFrame(makeSilence(160), ts)
		ts += 10
	}
	// 10 speech frames (100ms) → creates completed silence + opens speech
	for i := 0; i < 10; i++ {
		d.ProcessFrame(makeSpeech(160), ts)
		ts += 10
	}
	// 32 silence frames (320ms) → closes speech, opens new silence
	for i := 0; i < 32; i++ {
		d.ProcessFrame(makeSilence(160), ts)
		ts += 10
	}

	stats := d.Stats(ts)
	if stats.Turns == 0 {
		t.Error("expected at least 1 speech turn")
	}
	if stats.TotalMs <= 0 {
		t.Errorf("TotalMs = %d, want > 0", stats.TotalMs)
	}
	if stats.AvgTurnMs <= 0 {
		t.Errorf("AvgTurnMs = %f, want > 0", stats.AvgTurnMs)
	}
}

func TestEnergyDiarizerStatsPureSilence(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	ts := int64(0)
	for i := 0; i < 10; i++ {
		d.ProcessFrame(makeSilence(160), ts)
		ts += 10
	}
	stats := d.Stats(ts)
	if stats.Turns != 0 {
		t.Errorf("expected 0 turns for pure silence, got %d", stats.Turns)
	}
	if stats.AvgTurnMs != 0 {
		t.Errorf("expected AvgTurnMs=0 for pure silence, got %f", stats.AvgTurnMs)
	}
}

func TestNewEnergyDiarizerAllDefaults(t *testing.T) {
	// Zero config → defaults applied
	d := NewEnergyDiarizer(EnergyDiarizerConfig{})
	if d.cfg.SilenceThreshold != 0.01 {
		t.Errorf("default SilenceThreshold = %f, want 0.01", d.cfg.SilenceThreshold)
	}
	if d.cfg.SpeakerChangeGapMs != 300 {
		t.Errorf("default SpeakerChangeGapMs = %d, want 300", d.cfg.SpeakerChangeGapMs)
	}
	if d.cfg.SampleRate != 16000 {
		t.Errorf("default SampleRate = %d, want 16000", d.cfg.SampleRate)
	}
}

func TestRMSEmpty(t *testing.T) {
	// rms() with empty slice should return 0
	if v := rms(nil); v != 0 {
		t.Errorf("rms(nil) = %f, want 0", v)
	}
}
