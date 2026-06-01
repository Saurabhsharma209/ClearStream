// SNR Benchmark tool for ClearStream POC.
// Measures SNR before and after enhancement for testdata/ WAV files.
//
// Usage:
//
//	go run tools/snr_benchmark/main.go
//	go run tools/snr_benchmark/main.go --server http://localhost:8080
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/audio"
)

func main() {
	serverURL := flag.String("server", "", "ClearStream HTTP server URL (e.g. http://localhost:8080); omit to use SDK directly")
	flag.Parse()

	cleanSamples, err := readWAV("testdata/sample_clean.wav")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading clean reference: %v\n", err)
		os.Exit(1)
	}

	files := []string{"sample_noisy.wav", "sample_office.wav"}

	fmt.Println("=== ClearStream SNR Benchmark ===")
	fmt.Printf("%-22s | %-11s | %-10s | %-11s | %s\n",
		"File", "Before (dB)", "After (dB)", "Improvement", "Status")
	fmt.Println("----------------------|-----------|-----------|-----------|---------")

	snr := &audio.SNREstimator{}
	improved := 0

	var cs *clearstream.ClearStream
	if *serverURL == "" {
		cfg := clearstream.DefaultConfig()
		cfg.Model = "passthrough"
		cs, err = clearstream.New(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error init clearstream: %v\n", err)
			os.Exit(1)
		}
		defer cs.Close()
	}

	for _, fname := range files {
		inPath := filepath.Join("testdata", fname)
		noisySamples, err := readWAV(inPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", fname, err)
			continue
		}

		// SNR before: noisy vs clean reference
		snrBefore := snrVsReference(snr, noisySamples, cleanSamples)

		// Enhance
		var enhancedSamples []int16
		if *serverURL != "" {
			enhancedSamples, err = enhanceViaHTTP(*serverURL, inPath)
		} else {
			enhancedSamples, err = enhanceViaPipeline(cs, noisySamples)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "enhance error for %s: %v\n", fname, err)
			continue
		}

		// SNR after: enhanced vs clean reference
		snrAfter := snrVsReference(snr, enhancedSamples, cleanSamples)
		improvement := snrAfter - snrBefore

		status := "PASS"
		if improvement <= 0 {
			status = "(no-op)"
		} else {
			improved++
		}

		fmt.Printf("%-22s | %8.1f dB | %7.1f dB | %+8.1f dB | %s\n",
			fname, snrBefore, snrAfter, improvement, status)
	}

	fmt.Printf("=== Overall: %d/%d improved ===\n", improved, len(files))
}

// snrVsReference computes SNR treating the difference between signal and
// reference as noise. Higher = closer to reference = cleaner.
func snrVsReference(snr *audio.SNREstimator, signal, reference []int16) float64 {
	minLen := len(signal)
	if len(reference) < minLen {
		minLen = len(reference)
	}
	return snr.EstimateSNR(reference[:minLen], signal[:minLen])
}

// enhanceViaPipeline runs samples through the SDK pipeline directly.
func enhanceViaPipeline(cs *clearstream.ClearStream, samples []int16) ([]int16, error) {
	p := cs.Pipeline()
	// Convert int16 samples to bytes
	raw := int16SliceToBytes(samples)
	var outBuf bytes.Buffer
	if err := p.ProcessFrames(raw, &outBuf); err != nil {
		return nil, err
	}
	if err := p.Flush(&outBuf); err != nil {
		return nil, err
	}
	return bytesToInt16Slice(outBuf.Bytes()), nil
}

// enhanceViaHTTP POSTs a WAV file to POST /enhance and reads back the result.
func enhanceViaHTTP(serverURL, wavPath string) ([]int16, error) {
	f, err := os.Open(wavPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("audio", filepath.Base(wavPath))
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(part, f); err != nil {
		return nil, err
	}
	w.Close()

	resp, err := http.Post(serverURL+"/enhance", w.FormDataContentType(), &body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(data) < 44 {
		return nil, fmt.Errorf("response too small to be a WAV")
	}
	// Response is a WAV; skip 44-byte header
	return bytesToInt16Slice(data[44:]), nil
}

// readWAV reads a WAV file, skipping the 44-byte header, and returns int16 samples.
func readWAV(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 44 {
		return nil, fmt.Errorf("file too small to be a WAV")
	}
	return bytesToInt16Slice(data[44:]), nil
}

func bytesToInt16Slice(raw []byte) []int16 {
	n := len(raw) / 2
	samples := make([]int16, n)
	r := bytes.NewReader(raw)
	binary.Read(r, binary.LittleEndian, samples)
	return samples
}

func int16SliceToBytes(samples []int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, v := range samples {
		out[2*i] = byte(v)
		out[2*i+1] = byte(v >> 8)
	}
	return out
}
