//go:build rnnoise

// rnnoise_process is a command-line tool that reads a 16kHz mono PCM16 WAV file,
// runs every 160-sample frame through the real CGO RNNoise suppressor, and writes
// the denoised audio as a 16kHz mono PCM16 WAV file.
//
// Usage:
//
//	CGO_ENABLED=1 go build -tags rnnoise -o /tmp/rnnoise_process ./tools/rnnoise_process/
//	/tmp/rnnoise_process -in raw_audio.wav -out eval_out/sprint27/rnnoise_real.wav
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/exotel/clearstream/pkg/model"
)

const (
	sampleRate    = 16000
	frameSamples  = 160 // 10ms @ 16kHz — matches audio.FrameSizeSamples
	wavHeaderSize = 44
)

func main() {
	inPath := flag.String("in", "", "Input WAV file (16kHz mono PCM16)")
	outPath := flag.String("out", "", "Output WAV file path")
	flag.Parse()

	if *inPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: rnnoise_process -in <input.wav> -out <output.wav>")
		os.Exit(1)
	}

	// Read input WAV
	rawSamples, err := readWAV(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rnnoise_process: read %q: %v\n", *inPath, err)
		os.Exit(1)
	}
	fmt.Printf("Input: %s — %d samples (%.1fs @ %dHz)\n",
		*inPath, len(rawSamples), float64(len(rawSamples))/sampleRate, sampleRate)

	// Create RNNoise suppressor (real CGO, requires -tags rnnoise)
	rnn, err := model.NewRNNoise()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rnnoise_process: init RNNoise: %v\n", err)
		os.Exit(1)
	}
	defer rnn.Close()
	fmt.Printf("RNNoise backend: %s\n", rnn.Name())

	// Process frame by frame (160 samples = 10ms)
	out := make([]int16, 0, len(rawSamples))
	nFrames := len(rawSamples) / frameSamples
	for i := 0; i < nFrames; i++ {
		frame := rawSamples[i*frameSamples : (i+1)*frameSamples]
		processed, err := rnn.Process(frame)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rnnoise_process: frame %d: %v\n", i, err)
			os.Exit(1)
		}
		out = append(out, processed...)
	}
	// Append any remaining partial frame unchanged (< 160 samples)
	remainder := rawSamples[nFrames*frameSamples:]
	out = append(out, remainder...)

	// Write output WAV
	if err := os.MkdirAll(filepath.Dir(*outPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "rnnoise_process: mkdir %q: %v\n", filepath.Dir(*outPath), err)
		os.Exit(1)
	}
	if err := writeWAV(*outPath, out); err != nil {
		fmt.Fprintf(os.Stderr, "rnnoise_process: write %q: %v\n", *outPath, err)
		os.Exit(1)
	}
	fmt.Printf("Output: %s — %d samples written\n", *outPath, len(out))
}

// readWAV reads a WAV file and returns int16 PCM samples.
// It parses RIFF chunks to find the 'data' chunk offset (handles non-44-byte headers).
func readWAV(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 {
		return nil, fmt.Errorf("file too small (%d bytes) to be a WAV", len(data))
	}
	// Basic RIFF/WAVE validation
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a RIFF/WAVE file")
	}
	// Parse chunks to find 'data' chunk offset
	offset := 12
	dataStart := -1
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if chunkID == "data" {
			dataStart = offset + 8
			break
		}
		offset += 8 + chunkSize
		// Align to even byte boundary per RIFF spec
		if chunkSize%2 != 0 {
			offset++
		}
	}
	if dataStart < 0 {
		return nil, fmt.Errorf("no 'data' chunk found in WAV file")
	}
	pcmBytes := data[dataStart:]
	n := len(pcmBytes) / 2
	samples := make([]int16, n)
	r := bytes.NewReader(pcmBytes)
	if err := binary.Read(r, binary.LittleEndian, samples); err != nil {
		return nil, fmt.Errorf("decode PCM samples: %w", err)
	}
	return samples, nil
}

// writeWAV writes int16 PCM samples to a WAV file with a standard 44-byte header.
func writeWAV(path string, samples []int16) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	n := len(samples)
	dataSize := uint32(n * 2)

	// RIFF header
	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(36+dataSize))
	f.Write([]byte("WAVE"))

	// fmt chunk
	f.Write([]byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))    // chunk size
	binary.Write(f, binary.LittleEndian, uint16(1))     // PCM format
	binary.Write(f, binary.LittleEndian, uint16(1))     // mono
	binary.Write(f, binary.LittleEndian, uint32(16000)) // sample rate
	binary.Write(f, binary.LittleEndian, uint32(32000)) // byte rate (16000*2)
	binary.Write(f, binary.LittleEndian, uint16(2))     // block align
	binary.Write(f, binary.LittleEndian, uint16(16))    // bits per sample

	// data chunk
	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, dataSize)

	return binary.Write(f, binary.LittleEndian, samples)
}
