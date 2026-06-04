package audio

import (
	"testing"
)

// TestPeakLimiter_ClampsClipping verifies that a frame whose samples are at
// full-scale int16 (32767) is attenuated so the output peak is <= 32000.
func TestPeakLimiter_ClampsClipping(t *testing.T) {
	lim := NewPeakLimiter()

	// Frame of maximum-amplitude samples.
	frame := make([]int16, FrameSizeSamples)
	for i := range frame {
		if i%2 == 0 {
			frame[i] = 32767
		} else {
			frame[i] = -32767
		}
	}

	out := lim.Process(frame)
	if len(out) != FrameSizeSamples {
		t.Fatalf("expected %d samples, got %d", FrameSizeSamples, len(out))
	}

	const maxAllowed = 32000
	for i, s := range out {
		abs := s
		if abs < 0 {
			abs = -abs
		}
		if abs > maxAllowed {
			t.Errorf("sample[%d] = %d exceeds allowed ceiling %d", i, s, maxAllowed)
		}
	}
}

// TestPeakLimiter_PassesNormal verifies that a normal speech-level frame
// (approx 1000 RMS) passes through the limiter nearly unchanged (less than 5%
// amplitude reduction).
func TestPeakLimiter_PassesNormal(t *testing.T) {
	lim := NewPeakLimiter()

	// Sine wave at amplitude 1000 -- well below the 28000 threshold.
	frame := makeSineFrame(1000, 3)
	inRMS := rmsInt16(frame)

	out := lim.Process(frame)
	outRMS := rmsInt16(out)

	// Expect the limiter to be transparent at this level: < 5% change.
	minAllowed := inRMS * 0.95
	if outRMS < minAllowed {
		t.Errorf("normal frame attenuated too much: outRMS %.2f < %.2f (95%% of inRMS %.2f)",
			outRMS, minAllowed, inRMS)
	}
}

// TestPeakLimiter_Reset verifies that Reset zeroes the peak envelope so a
// subsequent normal frame is not inadvertently attenuated by a stale peak.
func TestPeakLimiter_Reset(t *testing.T) {
	lim := NewPeakLimiter()

	// Drive the limiter to a high peak.
	bigFrame := make([]int16, FrameSizeSamples)
	for i := range bigFrame {
		bigFrame[i] = 32767
	}
	lim.Process(bigFrame)

	if lim.peak == 0 {
		t.Fatal("expected non-zero peak after processing loud frame")
	}

	lim.Reset()
	if lim.peak != 0 {
		t.Errorf("peak = %v after Reset, want 0", lim.peak)
	}

	// A moderate frame should now pass through without attenuation.
	normalFrame := makeSineFrame(1000, 2)
	inRMS := rmsInt16(normalFrame)
	out := lim.Process(normalFrame)
	outRMS := rmsInt16(out)

	if outRMS < inRMS*0.95 {
		t.Errorf("frame after Reset was attenuated: outRMS %.2f vs inRMS %.2f", outRMS, inRMS)
	}
}
