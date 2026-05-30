// clearstream is a CLI tool for the ClearStream audio enhancement SDK.
//
// Usage:
//
//	# Post-process a file
//	clearstream file -i noisy.mp4 -o clean.mp4
//	clearstream file -i recording.wav -o clean.wav --model deepfilter --model-path ./deepfilter.onnx
//
//	# Live RTP interception
//	clearstream rtp --listen :5004 --forward 10.0.0.2:5004 --codec pcmu
//
//	# Probe a file (show codec info without processing)
//	clearstream probe recording.mp4
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/exotel/clearstream"
	"github.com/exotel/clearstream/pkg/audio"
	rtppkg "github.com/exotel/clearstream/pkg/rtp"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "file":
		runFile(os.Args[2:])
	case "rtp":
		runRTP(os.Args[2:])
	case "probe":
		runProbe(os.Args[2:])
	case "version":
		fmt.Println("clearstream v0.1.0 (ClearStream Audio Enhancement SDK)")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func runFile(args []string) {
	fs := flag.NewFlagSet("file", flag.ExitOnError)
	input := fs.String("i", "", "Input file (audio or video)")
	output := fs.String("o", "", "Output file")
	modelBackend := fs.String("model", "rnnoise", "Model backend: rnnoise | deepfilter | passthrough")
	modelPath := fs.String("model-path", "", "Path to ONNX model file (deepfilter only)")
	ffmpegPath := fs.String("ffmpeg", "ffmpeg", "Path to ffmpeg binary")
	audioOnly := fs.Bool("audio-only", false, "Strip video track from output")
	fs.Parse(args)

	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "error: -i and -o are required")
		fs.Usage()
		os.Exit(1)
	}

	cfg := clearstream.DefaultConfig()
	cfg.Model = *modelBackend
	cfg.ModelPath = *modelPath
	cfg.FFmpegPath = *ffmpegPath

	cs, err := clearstream.New(cfg)
	must("init clearstream", err)
	defer cs.Close()

	fmt.Printf("Processing %s → %s (model: %s)\n", *input, *output, *modelBackend)
	start := time.Now()

	err = cs.ProcessFileWithOptions(*input, *output, clearstream.FileOptions(*audioOnly))
	must("process file", err)

	fmt.Printf("Done in %.1fs → %s\n", time.Since(start).Seconds(), *output)
}

func runRTP(args []string) {
	fs := flag.NewFlagSet("rtp", flag.ExitOnError)
	listen := fs.String("listen", ":5004", "UDP address to receive RTP packets")
	forward := fs.String("forward", "", "UDP address to forward clean packets")
	codec := fs.String("codec", "auto", "Codec: pcmu | pcma | opus | g722 | g729 | auto")
	pt := fs.Uint("pt", 0, "RTP payload type (overrides --codec if set)")
	jitterDepth := fs.Int("jitter", 4, "Jitter buffer depth (frames)")
	modelBackend := fs.String("model", "rnnoise", "Model backend: rnnoise | deepfilter | passthrough")
	modelPath := fs.String("model-path", "", "Path to ONNX model file (deepfilter only)")
	fs.Parse(args)

	if *forward == "" {
		fmt.Fprintln(os.Stderr, "error: --forward is required")
		fs.Usage()
		os.Exit(1)
	}

	cfg := clearstream.DefaultConfig()
	cfg.Model = *modelBackend
	cfg.ModelPath = *modelPath

	cs, err := clearstream.New(cfg)
	must("init clearstream", err)
	defer cs.Close()

	rtpCfg := rtppkg.Config{
		ListenAddr:  *listen,
		ForwardAddr: *forward,
		JitterDepth: *jitterDepth,
		PayloadType: uint8(*pt),
		OnStats: func(s rtppkg.Stats) {
			fmt.Printf("\r[stats] rx=%d tx=%d lost=%d latency=%.1fms   ",
				s.PacketsReceived, s.PacketsSent, s.PacketsLost, s.LatencyAvgMs)
		},
	}

	if *codec != "auto" {
		rtpCfg.Codec = audio.Codec(*codec)
	}

	session, err := cs.NewRTPSession(rtpCfg)
	must("create RTP session", err)

	session.Start()
	fmt.Printf("ClearStream RTP running: %s → [suppress] → %s\n", *listen, *forward)
	fmt.Println("Press Ctrl+C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Println("\nShutting down...")
	session.Stop()

	stats := session.Stats()
	fmt.Printf("\nFinal stats: rx=%d tx=%d lost=%d avg_latency=%.1fms\n",
		stats.PacketsReceived, stats.PacketsSent, stats.PacketsLost, stats.LatencyAvgMs)
}

func runProbe(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: provide a file path")
		os.Exit(1)
	}
	info, err := audio.Probe("ffmpeg", args[0])
	must("probe", err)

	fmt.Printf("File:        %s\n", args[0])
	fmt.Printf("Container:   %s\n", info.ContainerFormat)
	fmt.Printf("Has video:   %v\n", info.HasVideo)
	if info.HasVideo {
		fmt.Printf("Video codec: %s\n", info.VideoCodec)
	}
	fmt.Printf("Audio codec: %s\n", info.AudioCodec)
	fmt.Printf("Sample rate: %d Hz\n", info.SampleRate)
	fmt.Printf("Channels:    %d\n", info.Channels)
	fmt.Printf("Duration:    %.1f sec\n", info.DurationSec)
}

func must(label string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "error [%s]: %v\n", label, err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`ClearStream Audio Enhancement SDK

Commands:
  file    Process an audio or video file (post-processing)
  rtp     Intercept and clean a live RTP stream
  probe   Show codec information for a file
  version Print version

Examples:
  clearstream file -i noisy.mp4 -o clean.mp4
  clearstream file -i call.wav -o clean.wav --model deepfilter --model-path ./deepfilter.onnx
  clearstream rtp --listen :5004 --forward 10.0.0.2:5004
  clearstream probe recording.mp3`)
}

// FileOptions is a helper to build file.Options from CLI flags.
// Kept here to avoid import cycle; production code would use file.Options directly.
func init() {
	// Register as clearstream.FileOptions for CLI use
}
