//go:build ignore

package main

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
)

func main() {
	sampleRate := 16000
	duration := 5
	numSamples := sampleRate * duration

	if err := os.MkdirAll("testdata", 0755); err != nil {
		panic(err)
	}

	f, err := os.Create("testdata/sample_noisy.wav")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	dataSize := uint32(numSamples * 2)

	// RIFF header
	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(36+dataSize))
	f.Write([]byte("WAVE"))

	// fmt chunk
	f.Write([]byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))
	binary.Write(f, binary.LittleEndian, uint16(1))     // PCM
	binary.Write(f, binary.LittleEndian, uint16(1))     // mono
	binary.Write(f, binary.LittleEndian, uint32(16000)) // sample rate
	binary.Write(f, binary.LittleEndian, uint32(32000)) // byte rate
	binary.Write(f, binary.LittleEndian, uint16(2))     // block align
	binary.Write(f, binary.LittleEndian, uint16(16))    // bits per sample

	// data chunk
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, dataSize)

	rng := rand.New(rand.NewSource(42))
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		speech := math.Sin(2*math.Pi*440*t) * 16000.0
		noise := (rng.Float64()*2 - 1) * 3200.0 // ~20% of 16000
		sample := int16(speech + noise)
		binary.Write(f, binary.LittleEndian, sample)
	}
}
