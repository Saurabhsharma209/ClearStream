// Command noise_load reads WAV files and feeds them through the ClearStream
// noise suppression pipeline as a load/accuracy test fixture.
//
// CS-007 fix: the previous WAV parser read the blockAlign field of the fmt
// chunk as uint32 (4 bytes).  blockAlign is a uint16 field (bytes 12–13 of the
// fmt chunk per RIFF spec), so reading 4 bytes consumed both blockAlign AND
// bitsPerSample, leaving the reader 2 bytes off for every subsequent field and
// producing an unexpected EOF on all WAV test fixtures.
//
// Corrected fmt chunk layout (PCM, little-endian):
//   offset  size  field
//        0     4  "fmt "
//        4     4  chunkSize (uint32) — 16 for PCM
//        8     2  audioFormat (uint16) — 1 = PCM
//       10     2  numChannels (uint16)
//       12     4  sampleRate (uint32)
//       16     4  byteRate (uint32)
//       20     2  blockAlign (uint16) ← was incorrectly read as uint32
//       22     2  bitsPerSample (uint16)
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/audio"
)

// wavHeader holds the decoded fields from a standard PCM WAV file.
type wavHeader struct {
	AudioFormat   uint16
	NumChannels   uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16 // CS-007: uint16, NOT uint32
	BitsPerSample uint16
	DataSize      uint32
}

// readWAVHeader parses the RIFF/WAVE header and positions r at the start of the
// PCM data chunk.  Returns an error for non-PCM or non-WAV files.
func readWAVHeader(r io.Reader) (*wavHeader, error) {
	// RIFF chunk descriptor (12 bytes)
	var riffID [4]byte
	if _, err := io.ReadFull(r, riffID[:]); err != nil {
		return nil, fmt.Errorf("readWAVHeader: read RIFF id: %w", err)
	}
	if string(riffID[:]) != "RIFF" {
		return nil, errors.New("readWAVHeader: not a RIFF file")
	}

	var chunkSize uint32
	if err := binary.Read(r, binary.LittleEndian, &chunkSize); err != nil {
		return nil, fmt.Errorf("readWAVHeader: read RIFF chunk size: %w", err)
	}

	var waveID [4]byte
	if _, err := io.ReadFull(r, waveID[:]); err != nil {
		return nil, fmt.Errorf("readWAVHeader: read WAVE id: %w", err)
	}
	if string(waveID[:]) != "WAVE" {
		return nil, errors.New("readWAVHeader: RIFF type is not WAVE")
	}

	// Sub-chunk search: find "fmt " and "data" in order (tolerates extra chunks).
	var hdr wavHeader
	foundFmt := false

	for {
		var subID [4]byte
		if _, err := io.ReadFull(r, subID[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return nil, fmt.Errorf("readWAVHeader: read sub-chunk id: %w", err)
		}

		var subSize uint32
		if err := binary.Read(r, binary.LittleEndian, &subSize); err != nil {
			return nil, fmt.Errorf("readWAVHeader: read sub-chunk size: %w", err)
		}

		switch string(subID[:]) {
		case "fmt ":
			// fmt chunk — minimum 16 bytes for PCM.
			if subSize < 16 {
				return nil, fmt.Errorf("readWAVHeader: fmt chunk too small (%d)", subSize)
			}

			if err := binary.Read(r, binary.LittleEndian, &hdr.AudioFormat); err != nil {
				return nil, fmt.Errorf("readWAVHeader: audioFormat: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &hdr.NumChannels); err != nil {
				return nil, fmt.Errorf("readWAVHeader: numChannels: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &hdr.SampleRate); err != nil {
				return nil, fmt.Errorf("readWAVHeader: sampleRate: %w", err)
			}
			if err := binary.Read(r, binary.LittleEndian, &hdr.ByteRate); err != nil {
				return nil, fmt.Errorf("readWAVHeader: byteRate: %w", err)
			}

			// CS-007: blockAlign is uint16 (2 bytes).
			// Reading it as uint32 (4 bytes) consumed bitsPerSample too, causing
			// a 2-byte misalignment for all subsequent reads → EOF on every fixture.
			if err := binary.Read(r, binary.LittleEndian, &hdr.BlockAlign); err != nil {
				return nil, fmt.Errorf("readWAVHeader: blockAlign: %w", err)
			}

			if err := binary.Read(r, binary.LittleEndian, &hdr.BitsPerSample); err != nil {
				return nil, fmt.Errorf("readWAVHeader: bitsPerSample: %w", err)
			}

			// Skip any extra bytes in the fmt chunk (e.g. extensible format).
			if subSize > 16 {
				extra := make([]byte, subSize-16)
				if _, err := io.ReadFull(r, extra); err != nil {
					return nil, fmt.Errorf("readWAVHeader: skip fmt extension: %w", err)
				}
			}
			foundFmt = true

		case "data":
			if !foundFmt {
				return nil, errors.New("readWAVHeader: data chunk before fmt chunk")
			}
			hdr.DataSize = subSize
			return &hdr, nil

		default:
			// Unknown/optional chunk — skip it.
			if subSize > 0 {
				discard := make([]byte, subSize)
				if _, err := io.ReadFull(r, discard); err != nil {
					return nil, fmt.Errorf("readWAVHeader: skip chunk %q: %w", subID, err)
				}
			}
		}
	}

	return nil, errors.New("readWAVHeader: data chunk not found")
}

// loadWAVSamples reads a 16-bit PCM WAV file and returns the raw int16 samples.
func loadWAVSamples(path string) ([]int16, *wavHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close() //nolint:errcheck

	hdr, err := readWAVHeader(f)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if hdr.AudioFormat != 1 {
		return nil, nil, fmt.Errorf("%s: unsupported audio format %d (only PCM=1 supported)", path, hdr.AudioFormat)
	}
	if hdr.BitsPerSample != 16 {
		return nil, nil, fmt.Errorf("%s: unsupported bit depth %d (only 16-bit supported)", path, hdr.BitsPerSample)
	}

	numSamples := int(hdr.DataSize) / 2
	samples := make([]int16, numSamples)
	if err := binary.Read(f, binary.LittleEndian, samples); err != nil {
		return nil, nil, fmt.Errorf("%s: read samples: %w", path, err)
	}
	return samples, hdr, nil
}

func main() {
	inputGlob := flag.String("i", "testdata/*.wav", "Glob pattern for input WAV files")
	modelBackend := flag.String("model", "passthrough", "NR backend: passthrough | rnnoise | deepfilter")
	outDir := flag.String("o", "", "Output directory for processed WAVs (empty = discard)")
	flag.Parse()

	cs, err := clearstream.New(clearstream.Config{
		Model:      *modelBackend,
		SampleRate: 16000,
		Channels:   1,
	})
	if err != nil {
		log.Fatalf("clearstream init: %v", err)
	}
	defer cs.Close() //nolint:errcheck

	files, err := filepath.Glob(*inputGlob)
	if err != nil || len(files) == 0 {
		log.Fatalf("no files matched %q", *inputGlob)
	}

	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			log.Fatalf("create output dir: %v", err)
		}
	}

	var totalFrames, totalFiles int
	for _, path := range files {
		if !strings.HasSuffix(strings.ToLower(path), ".wav") {
			continue
		}

		samples, hdr, err := loadWAVSamples(path)
		if err != nil {
			log.Printf("SKIP %s: %v", path, err)
			continue
		}

		log.Printf("LOAD %s: %dHz %dch %d-bit %d samples (%.2fs)",
			filepath.Base(path),
			hdr.SampleRate, hdr.NumChannels, hdr.BitsPerSample,
			len(samples), float64(len(samples))/float64(hdr.SampleRate),
		)

		// Feed through the pipeline in 10ms frames (160 samples at 16kHz).
		pipe := cs.Pipeline()
		const frameSize = 160
		var processed []int16
		for i := 0; i+frameSize <= len(samples); i += frameSize {
			frame := samples[i : i+frameSize]
			out := pipe.ProcessFrame(frame)
			processed = append(processed, out...)
			totalFrames++
		}

		if *outDir != "" {
			outPath := filepath.Join(*outDir, filepath.Base(path))
			if err := writeWAV(outPath, processed, hdr); err != nil {
				log.Printf("WARN write %s: %v", outPath, err)
			} else {
				log.Printf("WRITE %s (%d samples)", outPath, len(processed))
			}
		}

		totalFiles++
	}

	stats := audio.PipelineStats{}
	_ = stats
	log.Printf("Done: %d files, %d frames processed", totalFiles, totalFrames)
}

// writeWAV writes int16 samples as a 16-bit mono PCM WAV.
func writeWAV(path string, samples []int16, src *wavHeader) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck

	dataSize := uint32(len(samples) * 2)
	sampleRate := src.SampleRate
	numCh := uint16(1)
	bitsPerSample := uint16(16)
	blockAlign := numCh * bitsPerSample / 8
	byteRate := sampleRate * uint32(blockAlign)

	// RIFF header
	f.Write([]byte("RIFF"))                                          //nolint:errcheck
	binary.Write(f, binary.LittleEndian, 36+dataSize)               //nolint:errcheck
	f.Write([]byte("WAVE"))                                          //nolint:errcheck
	f.Write([]byte("fmt "))                                          //nolint:errcheck
	binary.Write(f, binary.LittleEndian, uint32(16))                 //nolint:errcheck — PCM fmt size
	binary.Write(f, binary.LittleEndian, uint16(1))                  //nolint:errcheck — PCM
	binary.Write(f, binary.LittleEndian, numCh)                      //nolint:errcheck
	binary.Write(f, binary.LittleEndian, sampleRate)                 //nolint:errcheck
	binary.Write(f, binary.LittleEndian, byteRate)                   //nolint:errcheck
	binary.Write(f, binary.LittleEndian, blockAlign)                 //nolint:errcheck — uint16
	binary.Write(f, binary.LittleEndian, bitsPerSample)              //nolint:errcheck
	f.Write([]byte("data"))                                          //nolint:errcheck
	binary.Write(f, binary.LittleEndian, dataSize)                   //nolint:errcheck
	return binary.Write(f, binary.LittleEndian, samples)
}
