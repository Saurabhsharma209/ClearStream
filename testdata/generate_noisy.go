//go:build ignore

package main

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
)

func writeWAVHeader(f *os.File, numSamples int) {
	dataSize := uint32(numSamples * 2)
	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(36+dataSize))
	f.Write([]byte("WAVE"))
	f.Write([]byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))
	binary.Write(f, binary.LittleEndian, uint16(1))     // PCM
	binary.Write(f, binary.LittleEndian, uint16(1))     // mono
	binary.Write(f, binary.LittleEndian, uint32(16000)) // sample rate
	binary.Write(f, binary.LittleEndian, uint32(32000)) // byte rate
	binary.Write(f, binary.LittleEndian, uint16(2))     // block align
	binary.Write(f, binary.LittleEndian, uint16(16))    // bits per sample
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, dataSize)
}

func clamp16(v float64) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func main() {
	sampleRate := 16000
	duration := 5
	numSamples := sampleRate * duration

	if err := os.MkdirAll("testdata", 0755); err != nil {
		panic(err)
	}

	rng := rand.New(rand.NewSource(42))

	// --- sample_clean.wav: pure 440Hz sine wave ---
	{
		f, err := os.Create("testdata/sample_clean.wav")
		if err != nil {
			panic(err)
		}
		writeWAVHeader(f, numSamples)
		for i := 0; i < numSamples; i++ {
			t := float64(i) / float64(sampleRate)
			sample := math.Sin(2*math.Pi*440*t) * 28000.0
			binary.Write(f, binary.LittleEndian, clamp16(sample))
		}
		f.Close()
	}

	// --- sample_noisy.wav: 440Hz sine + white noise, SNR ~10dB ---
	// SNR 10dB => noise amplitude = signal_amplitude / sqrt(10) ~ signal / 3.16
	{
		f, err := os.Create("testdata/sample_noisy.wav")
		if err != nil {
			panic(err)
		}
		writeWAVHeader(f, numSamples)
		sigAmp := 16000.0
		noiseAmp := sigAmp / math.Sqrt(10) // ~10dB SNR
		for i := 0; i < numSamples; i++ {
			t := float64(i) / float64(sampleRate)
			speech := math.Sin(2*math.Pi*440*t) * sigAmp
			noise := (rng.Float64()*2 - 1) * noiseAmp
			binary.Write(f, binary.LittleEndian, clamp16(speech+noise))
		}
		f.Close()
	}

	// --- sample_office.wav: 440Hz sine + pink-ish (low-freq biased) noise ---
	{
		f, err := os.Create("testdata/sample_office.wav")
		if err != nil {
			panic(err)
		}
		writeWAVHeader(f, numSamples)
		sigAmp := 16000.0
		noiseAmp := sigAmp / math.Sqrt(10)
		var smoothed float64
		for i := 0; i < numSamples; i++ {
			t := float64(i) / float64(sampleRate)
			speech := math.Sin(2*math.Pi*440*t) * sigAmp
			white := (rng.Float64()*2 - 1)
			// Low-freq bias: 70% smoothed (prev) + 30% new white
			smoothed = smoothed*0.7 + white*0.3
			noise := smoothed * noiseAmp
			binary.Write(f, binary.LittleEndian, clamp16(speech+noise))
		}
		f.Close()
	}
}
