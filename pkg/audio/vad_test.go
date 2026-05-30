package audio

import (
	"testing"
)

func TestVADSilenceDetection(t *testing.T) {
	v := DefaultVAD()
	// All-zero frame = silence
	frame := make([]int16, FrameSizeSamples)
	if v.IsSpeech(frame) {
		t.Error("expected silence for zero frame")
	}
}

func TestVADSpeechDetection(t *testing.T) {
	v := DefaultVAD()
	frame := make([]int16, FrameSizeSamples)
	// Fill with high-energy signal
	for i := range frame {
		frame[i] = 8000
	}
	if !v.IsSpeech(frame) {
		t.Error("expected speech for high-energy frame")
	}
}

func TestVADHangover(t *testing.T) {
	v := &VAD{ThresholdRMS: 300, HangoverFrames: 3}
	speech := make([]int16, FrameSizeSamples)
	for i := range speech {
		speech[i] = 8000
	}
	silence := make([]int16, FrameSizeSamples)

	// One speech frame
	v.IsSpeech(speech)

	// Next 3 silent frames should still be speech (hangover)
	for i := 0; i < 3; i++ {
		if !v.IsSpeech(silence) {
			t.Errorf("hangover frame %d should be speech", i)
		}
	}
	// Frame 4 after hangover expires should be silence
	if v.IsSpeech(silence) {
		t.Error("frame after hangover should be silence")
	}
}

func TestVADReset(t *testing.T) {
	v := &VAD{ThresholdRMS: 300, HangoverFrames: 5}
	speech := make([]int16, FrameSizeSamples)
	for i := range speech {
		speech[i] = 8000
	}
	v.IsSpeech(speech) // triggers hangover
	v.Reset()
	silence := make([]int16, FrameSizeSamples)
	if v.IsSpeech(silence) {
		t.Error("after reset, silence should not be speech")
	}
}

func TestRMSEnergy(t *testing.T) {
	frame := make([]int16, 160)
	for i := range frame {
		frame[i] = 1000
	}
	rms := rmsEnergy(frame)
	if rms != 1000 {
		t.Errorf("expected rms=1000 for constant frame, got %.2f", rms)
	}
}

func TestAdaptiveVADCalibration(t *testing.T) {
	v := DefaultAdaptiveVAD()
	// Feed 50 silence frames to trigger calibration
	silence := make([]int16, FrameSizeSamples)
	for i := 0; i < 50; i++ {
		v.IsSpeech(silence)
	}
	if !v.IsCalibrated() {
		t.Error("should be calibrated after 50 frames")
	}
	if v.NoiseFloor() != 0 {
		t.Logf("noise floor: %.2f", v.NoiseFloor())
	}
}

func TestAdaptiveVADDetectsAfterCalibration(t *testing.T) {
	v := DefaultAdaptiveVAD()
	silence := make([]int16, FrameSizeSamples)
	for i := 0; i < 50; i++ {
		v.IsSpeech(silence)
	}
	speech := make([]int16, FrameSizeSamples)
	for i := range speech {
		speech[i] = 5000
	}
	if !v.IsSpeech(speech) {
		t.Error("should detect speech after calibration")
	}
}
