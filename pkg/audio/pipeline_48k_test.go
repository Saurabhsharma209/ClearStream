package audio

import (
	"math"
	"testing"

	"github.com/exotel/clearstream/pkg/model"
)

func TestProcess48kPassthrough(t *testing.T) {
	p := NewPipeline(PipelineConfig{Suppressor: nil})
	frame := make([]int16, Frame48kSamples)
	for i := range frame {
		frame[i] = int16(i % 1000)
	}
	out, err := p.Process48k(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != Frame48kSamples {
		t.Fatalf("want %d samples, got %d", Frame48kSamples, len(out))
	}
}

func TestProcess48kWithMock(t *testing.T) {
	mock := model.NewMockSuppressor()
	p := NewPipeline(PipelineConfig{Suppressor: mock})
	frame := make([]int16, Frame48kSamples)
	for i := range frame {
		frame[i] = 3000
	}
	out, err := p.Process48k(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != Frame48kSamples {
		t.Fatalf("want %d samples, got %d", Frame48kSamples, len(out))
	}
	if mock.ProcessCalls != 1 {
		t.Errorf("want 1 suppressor call, got %d", mock.ProcessCalls)
	}
	s := p.Stats()
	if s.FramesProcessed != 1 {
		t.Errorf("FramesProcessed: want 1, got %d", s.FramesProcessed)
	}
	if s.FramesSuppressed != 1 {
		t.Errorf("FramesSuppressed: want 1, got %d", s.FramesSuppressed)
	}
}

type silenceVAD struct{}

func (s *silenceVAD) IsSpeech(_ []int16) bool { return false }
func (s *silenceVAD) Reset()                  {}

func TestProcess48kVAD(t *testing.T) {
	mock := model.NewMockSuppressor()
	p := NewPipeline(PipelineConfig{Suppressor: mock, VAD: &silenceVAD{}})
	frame := make([]int16, Frame48kSamples)
	out, err := p.Process48k(frame)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != Frame48kSamples {
		t.Fatalf("want %d samples, got %d", Frame48kSamples, len(out))
	}
	if mock.ProcessCalls != 0 {
		t.Errorf("suppressor should not be called on silence, got %d calls", mock.ProcessCalls)
	}
	if p.Stats().FramesSilent != 1 {
		t.Errorf("FramesSilent: want 1, got %d", p.Stats().FramesSilent)
	}
}

func TestProcess48kWrongLength(t *testing.T) {
	p := NewPipeline(PipelineConfig{})
	_, err := p.Process48k(make([]int16, 160))
	if err == nil {
		t.Fatal("expected error for wrong-length input")
	}
}

// rmsInt16 and addNoise-style helpers already exist in noise_reducer_test.go
// and diarize_test.go respectively (same package); reused below.

// peakAbs48k returns the maximum absolute sample value in samples.
func peakAbs48k(samples []int16) int {
	max := 0
	for _, s := range samples {
		v := int(s)
		if v < 0 {
			v = -v
		}
		if v > max {
			max = v
		}
	}
	return max
}

// make48kSineFrame generates a 480-sample (10ms @ 48kHz) sine wave at the
// given amplitude with cyclesPerFrame full cycles per 10ms frame (e.g.
// cyclesPerFrame=3 -> 300Hz). A low frequency is used deliberately so the
// tone survives Process48k's internal 3x Kaiser-FIR downsample to 16kHz
// (which low-pass filters at ~8kHz) without being attenuated away.
func make48kSineFrame(amp float64, cyclesPerFrame float64) []int16 {
	out := make([]int16, Frame48kSamples)
	for i := range out {
		out[i] = int16(amp * math.Sin(2*math.Pi*cyclesPerFrame*float64(i)/float64(Frame48kSamples)))
	}
	return out
}

// TestProcess48kNoiseReducerWired proves that PipelineConfig.UseNoiseReducer
// actually takes effect on the 48kHz code path. Before this fix, Process48k
// never consulted p.noiseReducer/p.tieredNR at all, so a caller enabling
// UseNoiseReducer (or TieredNR) for 48kHz audio (e.g. a WebRTC/Opus leg) got
// silently zero noise reduction, unlike the 8/16kHz ProcessFrames path where
// the same config field is honoured.
func TestProcess48kNoiseReducerWired(t *testing.T) {
	noiseFrame := make([]int16, Frame48kSamples)
	var state uint32 = 0xdeadbeef
	for i := range noiseFrame {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		noiseFrame[i] = int16((float64(int32(state)>>16) / 32768.0) * 150)
	}

	withNR := NewPipeline(PipelineConfig{UseNoiseReducer: true})
	withoutNR := NewPipeline(PipelineConfig{})

	var lastWith, lastWithout []int16
	var err error
	for i := 0; i < 80; i++ {
		lastWith, err = withNR.Process48k(noiseFrame)
		if err != nil {
			t.Fatalf("withNR Process48k: %v", err)
		}
		lastWithout, err = withoutNR.Process48k(noiseFrame)
		if err != nil {
			t.Fatalf("withoutNR Process48k: %v", err)
		}
	}

	rmsWith := rmsInt16(lastWith)
	rmsWithout := rmsInt16(lastWithout)
	if rmsWith >= rmsWithout*0.85 {
		t.Errorf("expected UseNoiseReducer to measurably reduce Process48k output RMS: with=%.2f without=%.2f (want with < 85%% of without)", rmsWith, rmsWithout)
	}
}

// TestProcess48kAGCWired proves that PipelineConfig.AGC actually takes
// effect on the 48kHz code path. Before this fix, Process48k never applied
// p.agc at all.
func TestProcess48kAGCWired(t *testing.T) {
	agcCfg := DefaultAGCConfig()
	p := NewPipeline(PipelineConfig{AGC: &agcCfg})
	quiet := make48kSineFrame(500, 3) // ~300Hz, well below TargetRMS=3000

	for i := 0; i < 150; i++ {
		if _, err := p.Process48k(quiet); err != nil {
			t.Fatalf("Process48k: %v", err)
		}
	}

	gain := p.agc.CurrentGain()
	if gain <= 1.2 {
		t.Errorf("expected AGC to have boosted gain well above 1.0 after 150 quiet frames on the 48kHz path, got %.3f", gain)
	}
}

// TestProcess48kLimiterWired proves that PipelineConfig.UseLimiter actually
// takes effect on the 48kHz code path. Before this fix, Process48k never
// applied p.limiter at all, so bursts/DTMF/AGC-overshoot on a 48kHz leg were
// never guarded against.
func TestProcess48kLimiterWired(t *testing.T) {
	loud := make48kSineFrame(32000, 3) // ~300Hz, near full-scale

	withLimiter := NewPipeline(PipelineConfig{UseLimiter: true})
	withoutLimiter := NewPipeline(PipelineConfig{})

	var lastWith, lastWithout []int16
	var err error
	for i := 0; i < 20; i++ {
		lastWith, err = withLimiter.Process48k(loud)
		if err != nil {
			t.Fatalf("withLimiter Process48k: %v", err)
		}
		lastWithout, err = withoutLimiter.Process48k(loud)
		if err != nil {
			t.Fatalf("withoutLimiter Process48k: %v", err)
		}
	}

	peakWith := peakAbs48k(lastWith)
	peakWithout := peakAbs48k(lastWithout)
	if peakWith >= peakWithout {
		t.Errorf("expected UseLimiter to reduce Process48k output peak: with=%d without=%d", peakWith, peakWithout)
	}
}

// TestProcess48kDiarizerWired proves that PipelineConfig.Diarizer actually
// takes effect on the 48kHz code path. Before this fix, Process48k never
// called p.diarizer.ProcessFrame at all, so a Diarizer configured alongside
// a 48kHz leg silently produced no segments/labels whatsoever.
func TestProcess48kDiarizerWired(t *testing.T) {
	d := NewEnergyDiarizer(DefaultEnergyDiarizerConfig())
	p := NewPipeline(PipelineConfig{Diarizer: d})

	speech := make48kSineFrame(16000, 3) // ~300Hz, well above SilenceThreshold

	for i := 0; i < 5; i++ {
		if _, err := p.Process48k(speech); err != nil {
			t.Fatalf("Process48k: %v", err)
		}
	}

	cur := d.CurrentSegment()
	if cur.Speaker != SpeakerNearEnd {
		t.Errorf("expected diarizer fed via Process48k to classify sustained loud frames as SpeakerNearEnd, got %s", cur.Speaker)
	}
}
