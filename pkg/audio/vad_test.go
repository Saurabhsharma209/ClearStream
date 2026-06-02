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

func TestVADEmptyFrame(t *testing.T) {
	v := DefaultVAD()
	// Should not panic and should return false for empty frame
	result := v.IsSpeech([]int16{})
	if result {
		t.Error("expected false for empty frame")
	}
}

func TestVADHangoverExpiry(t *testing.T) {
	v := &VAD{ThresholdRMS: 300, HangoverFrames: 3}
	speech := make([]int16, FrameSizeSamples)
	for i := range speech {
		speech[i] = 5000
	}
	silence := make([]int16, FrameSizeSamples) // all zeros

	// One speech frame to trigger hangover
	v.IsSpeech(speech)

	// First 3 silence frames should still be true (hangover active)
	for i := 0; i < 3; i++ {
		if !v.IsSpeech(silence) {
			t.Errorf("hangover silence frame %d should return true", i)
		}
	}
	// 4th silence frame: hangover expired, should be false
	if v.IsSpeech(silence) {
		t.Error("4th silence frame after hangover should return false")
	}
}

func TestAdaptiveVADSingleFrame(t *testing.T) {
	v := &AdaptiveVAD{
		VAD:               VAD{ThresholdRMS: 300, HangoverFrames: 8},
		CalibrationFrames: 1,
		SensitivityFactor: 3.0,
	}
	// Feed exactly 1 frame (value=0 noise)
	silence := make([]int16, FrameSizeSamples)
	v.IsSpeech(silence)

	if !v.IsCalibrated() {
		t.Error("should be calibrated after 1 frame when CalibrationFrames=1")
	}

	// Loud frame should be detected as speech
	loud := make([]int16, FrameSizeSamples)
	for i := range loud {
		loud[i] = 30000
	}
	if !v.IsSpeech(loud) {
		t.Error("loud frame should be detected as speech after calibration")
	}
}

func TestAdaptiveVADNoisyCalibration(t *testing.T) {
	v := &AdaptiveVAD{
		VAD:               VAD{ThresholdRMS: 300, HangoverFrames: 8},
		CalibrationFrames: 10,
		SensitivityFactor: 2.0,
	}

	// Feed 10 frames of moderate noise (all samples=200, RMS=200)
	noiseFrame := make([]int16, FrameSizeSamples)
	for i := range noiseFrame {
		noiseFrame[i] = 200
	}
	for i := 0; i < 10; i++ {
		v.IsSpeech(noiseFrame)
	}

	if !v.IsCalibrated() {
		t.Error("should be calibrated after 10 frames")
	}

	nf := v.NoiseFloor()
	if nf < 195 || nf > 205 {
		t.Errorf("expected NoiseFloor~200, got %.2f", nf)
	}

	// threshold ~ 200 * 2.0 = 400; frame with value=800 should be speech
	loudFrame := make([]int16, FrameSizeSamples)
	for i := range loudFrame {
		loudFrame[i] = 800
	}
	if !v.IsSpeech(loudFrame) {
		t.Error("frame with RMS=800 should be speech (threshold~400)")
	}

	// Reset hangover before silence test
	v.VAD.Reset()

	// Frame with value=100 (below threshold) should be silence
	quietFrame := make([]int16, FrameSizeSamples)
	for i := range quietFrame {
		quietFrame[i] = 100
	}
	if v.IsSpeech(quietFrame) {
		t.Error("frame with RMS=100 should be silence (threshold~400)")
	}
}

func TestAdaptiveVADReset(t *testing.T) {
	v := DefaultAdaptiveVAD()
	frame := make([]int16, FrameSizeSamples)
	for i := 0; i < 50; i++ {
		v.IsSpeech(frame)
	}
	if !v.IsCalibrated() {
		t.Error("should be calibrated after 50 frames")
	}

	v.Reset()

	if v.IsCalibrated() {
		t.Error("should not be calibrated after Reset")
	}
	if v.NoiseFloor() != 0 {
		t.Errorf("NoiseFloor should be 0 after Reset, got %.2f", v.NoiseFloor())
	}

	// Re-calibrate with 50 more frames
	for i := 0; i < 50; i++ {
		v.IsSpeech(frame)
	}
	if !v.IsCalibrated() {
		t.Error("should be calibrated again after 50 frames post-Reset")
	}
}

func TestVADRMSEnergyCorrectnessConstant(t *testing.T) {
	frame := make([]int16, FrameSizeSamples)
	for i := range frame {
		frame[i] = 1000
	}

	// ThresholdRMS=999: RMS=1000 > 999 -> speech
	v1 := &VAD{ThresholdRMS: 999, HangoverFrames: 0}
	if !v1.IsSpeech(frame) {
		t.Error("expected IsSpeech=true with threshold 999 and RMS=1000")
	}

	// ThresholdRMS=1001: RMS=1000 < 1001 -> silence
	v2 := &VAD{ThresholdRMS: 1001, HangoverFrames: 0}
	if v2.IsSpeech(frame) {
		t.Error("expected IsSpeech=false with threshold 1001 and RMS=1000")
	}
}
