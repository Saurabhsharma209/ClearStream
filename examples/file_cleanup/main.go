// Example: post-process a noisy audio/video file.
//
// Run:
//   go run main.go -i noisy.mp4 -o clean.mp4
//   go run main.go -i call_recording.wav -o clean_recording.wav
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/exotel/clearstream"
)

func main() {
	input := flag.String("i", "", "Input file (audio or video)")
	output := flag.String("o", "", "Output file")
	model := flag.String("model", "rnnoise", "rnnoise | deepfilter | passthrough")
	modelPath := flag.String("model-path", "", "ONNX model path (deepfilter only)")
	flag.Parse()

	if *input == "" || *output == "" {
		log.Fatal("usage: -i <input> -o <output>")
	}

	// Initialize SDK
	cs, err := clearstream.New(clearstream.Config{
		Model:     *model,
		ModelPath: *modelPath,
	})
	if err != nil {
		log.Fatalf("init: %v", err)
	}
	defer cs.Close()

	fmt.Printf("ClearStream: cleaning %s → %s\n", *input, *output)
	start := time.Now()

	if err := cs.ProcessFile(*input, *output); err != nil {
		log.Fatalf("process: %v", err)
	}

	fmt.Printf("Done in %.2fs\n", time.Since(start).Seconds())
}
