//go:build ignore

// Run: go run tools/gen_test_audio/main.go

package main

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
)

const (
	sampleRate = 16000
	duration   = 5
)

func writeWAV(path string, samples []int16) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dataSize := uint32(len(samples) * 2)
	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, 36+dataSize)
	f.Write([]byte("WAVEfmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))
	binary.Write(f, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(f, binary.LittleEndian, uint16(1)) // mono
	binary.Write(f, binary.LittleEndian, uint32(sampleRate))
	binary.Write(f, binary.LittleEndian, uint32(sampleRate*2))
	binary.Write(f, binary.LittleEndian, uint16(2))
	binary.Write(f, binary.LittleEndian, uint16(16))
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, dataSize)
	return binary.Write(f, binary.LittleEndian, samples)
}

func main() {
	os.MkdirAll("testdata", 0755)
	n := sampleRate * duration
	rng := rand.New(rand.NewSource(42))

	// 1. Clean: pure 440Hz + 1000Hz dual-tone (simulates clean speech)
	clean := make([]int16, n)
	for i := range clean {
		t := float64(i) / sampleRate
		v := math.Sin(2*math.Pi*440*t)*0.4 + math.Sin(2*math.Pi*1000*t)*0.3
		clean[i] = int16(v * 32767)
	}
	writeWAV("testdata/sample_clean.wav", clean)

	// 2. Noisy: same tones + heavy white noise at 40% amplitude (SNR ~5dB)
	noisy := make([]int16, n)
	for i := range noisy {
		t := float64(i) / sampleRate
		speech := math.Sin(2*math.Pi*440*t)*0.4 + math.Sin(2*math.Pi*1000*t)*0.3
		noise := (rng.Float64()*2 - 1) * 0.4
		v := speech + noise
		if v > 1 {
			v = 1
		}
		if v < -1 {
			v = -1
		}
		noisy[i] = int16(v * 32767)
	}
	writeWAV("testdata/sample_noisy.wav", noisy)

	// 3. Office noise: tones + keyboard clicks (sine bursts) + white noise at 15%
	office := make([]int16, n)
	for i := range office {
		t := float64(i) / sampleRate
		speech := math.Sin(2*math.Pi*440*t)*0.4 + math.Sin(2*math.Pi*1000*t)*0.3
		noise := (rng.Float64()*2 - 1) * 0.15
		// Keyboard clicks: 50ms burst every ~800ms
		click := 0.0
		if i%(sampleRate*800/1000) < sampleRate*50/1000 && rng.Float32() < 0.3 {
			click = (rng.Float64()*2 - 1) * 0.6
		}
		v := speech + noise + click
		if v > 1 {
			v = 1
		}
		if v < -1 {
			v = -1
		}
		office[i] = int16(v * 32767)
	}
	writeWAV("testdata/sample_office.wav", office)

	println("Generated: testdata/sample_clean.wav, sample_noisy.wav, sample_office.wav")
}
