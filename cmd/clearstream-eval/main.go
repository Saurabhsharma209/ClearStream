// Command clearstream-eval evaluates ClearStream audio enhancement quality.
//
// # Batch mode — process a directory of recordings
//
//	clearstream-eval batch \
//	    --input-dir  ./recordings \
//	    --output-dir ./eval-out   \
//	    --workers    8
//
// Supports any audio format ffmpeg can decode (wav, mp3, ogg, flac, m4a, …).
// Outputs:
//   - eval_files_<ts>.csv       — per-file metrics (SNR, latency, VAD, AGC)
//   - eval_summary_<ts>.json    — aggregate stats
//   - eval_files_<ts>.json      — full per-file JSON
//   - tuned_config_<ts>.yaml    — recommended ClearStream config
//
// # RTP mode — monitor a live call
//
//	clearstream-eval rtp \
//	    --listen  :5004          \
//	    --forward 10.0.0.2:5004  \
//	    --duration 60s
//
// Monitors quality in real-time, prints alerts to stderr, and writes
// rtp_eval_<ts>.json + rtp_tuned_config_<ts>.yaml after the session.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/exotel/clearstream/pkg/audio"
	"github.com/exotel/clearstream/pkg/eval"
	"github.com/exotel/clearstream/pkg/model"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "batch":
		runBatch(os.Args[2:])
	case "rtp":
		runRTP(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `clearstream-eval — ClearStream quality evaluation tool

Usage:
  clearstream-eval batch [flags]   Process a directory of audio recordings
  clearstream-eval rtp   [flags]   Monitor a live RTP session

batch flags:
  --input-dir  DIR   Directory containing audio files (wav, mp3, ogg, flac…)
  --output-dir DIR   Output directory for CSV/JSON/YAML reports (default: ./eval-out)
  --workers    N     Parallel worker count (default: NumCPU)
  --agc              Enable AGC (default: off)

rtp flags:
  --output-dir DIR   Output directory for reports (default: ./eval-out)
  --duration   DUR   How long to monitor (default: run until Ctrl-C)
  --interval   DUR   Sampling interval (default: 1s)
  --alert             Print alerts to stderr (always on)`)
}

// ─── batch ─────────────────────────────────────────────────────────────────

func runBatch(args []string) {
	fs := flag.NewFlagSet("batch", flag.ExitOnError)
	inputDir := fs.String("input-dir", "", "Directory containing audio files")
	outputDir := fs.String("output-dir", "eval-out", "Output directory for reports")
	workers := fs.Int("workers", runtime.NumCPU(), "Parallel worker count")
	useAGC := fs.Bool("agc", false, "Enable AGC on each worker pipeline")
	_ = fs.Parse(args)

	if *inputDir == "" {
		fmt.Fprintln(os.Stderr, "error: --input-dir is required")
		os.Exit(1)
	}

	var agcCfg *audio.AGCConfig
	if *useAGC {
		dflt := audio.DefaultAGCConfig()
		agcCfg = &dflt
	}

	suppressor := model.NewPassthrough() // swap for RNNoise/DeepFilter for real quality eval

	cfg := eval.BatchConfig{
		InputDir:   *inputDir,
		OutputDir:  *outputDir,
		Workers:    *workers,
		Suppressor: suppressor,
		AGC:        agcCfg,
		OnProgress: func(done, total int) {
			pct := float64(done) / float64(total) * 100
			fmt.Printf("\r[%d/%d] %.0f%%  ", done, total, pct)
		},
	}

	runner := eval.NewBatchRunner(cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("clearstream-eval batch: processing %s → %s (%d workers)\n",
		*inputDir, *outputDir, *workers)

	start := time.Now()
	summary, err := runner.Run(ctx)
	fmt.Println() // newline after progress
	if err != nil {
		fmt.Fprintf(os.Stderr, "batch error: %v\n", err)
		os.Exit(1)
	}

	elapsed := time.Since(start)
	fmt.Printf("\n── Batch complete ────────────────────────────────────────────\n")
	fmt.Printf("  Files:         %d processed, %d failed / %d total\n",
		summary.ProcessedFiles, summary.FailedFiles, summary.TotalFiles)
	fmt.Printf("  Audio time:    %.1f s\n", summary.TotalDurationMs/1000)
	fmt.Printf("  Wall clock:    %.1f s  (%.1fx real-time)\n",
		elapsed.Seconds(), summary.SpeedRatio)
	fmt.Printf("  SNR before:    %.1f dB\n", summary.AvgSNRBeforeDB)
	fmt.Printf("  SNR after:     %.1f dB  (+%.1f dB improvement)\n",
		summary.AvgSNRAfterDB, summary.AvgSNRImprovementDB)
	fmt.Printf("  Latency mean:  %.2f ms  P95: %.2f ms\n",
		summary.AvgLatencyMeanMs, summary.AvgLatencyP95Ms)
	fmt.Printf("  Speech ratio:  %.0f%%  (CPU saved: ~%.0f%%)\n",
		summary.AvgSpeechRatio*100, summary.AvgCPUSavedPct)
	fmt.Printf("──────────────────────────────────────────────────────────────\n")

	csvPath, summPath, filesPath, cfgPath, err := eval.WriteAllReports(*outputDir, summary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "write reports: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nOutputs written:\n")
	fmt.Printf("  CSV:           %s\n", csvPath)
	fmt.Printf("  Summary JSON:  %s\n", summPath)
	fmt.Printf("  Files JSON:    %s\n", filesPath)
	fmt.Printf("  Tuned config:  %s\n", cfgPath)
}

// ─── rtp ─────────────────────────────────────────────────────────────────────

func runRTP(args []string) {
	fs := flag.NewFlagSet("rtp", flag.ExitOnError)
	outputDir := fs.String("output-dir", "eval-out", "Output directory for reports")
	durationStr := fs.String("duration", "", "Monitoring duration (e.g. 60s, 5m). Empty = until Ctrl-C")
	intervalStr := fs.String("interval", "1s", "Sampling interval")
	_ = fs.Parse(args)

	interval, err := time.ParseDuration(*intervalStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --interval: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("clearstream-eval rtp: monitoring → %s (interval %s)\n",
		*outputDir, interval)
	fmt.Println("  Note: wire StatsFn in code to your live rtp.Session.Stats().")
	fmt.Println("  This CLI mode uses a synthetic no-op provider for demonstration.")
	fmt.Println("  Press Ctrl-C to stop and write reports.")

	// Synthetic stats source — replace with a real rtp.Session adapter in production.
	var rxCount, lostCount uint64
	statsFn := func() eval.RTPStats {
		rxCount += 100
		if rxCount%1000 == 0 {
			lostCount += 2 // simulate 0.2% loss
		}
		return eval.RTPStats{
			PacketsReceived: rxCount,
			PacketsLost:     lostCount,
			LatencyAvgMs:    2.5,
		}
	}

	monitor := eval.NewRTPMonitor(eval.RTPMonitorConfig{
		StatsFn:        statsFn,
		OutputDir:      *outputDir,
		SampleInterval: interval,
		OnAlert: func(msg string) {
			fmt.Fprintf(os.Stderr, "  ⚠ ALERT: %s\n", msg)
		},
	})
	monitor.Start()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *durationStr != "" {
		dur, err := time.ParseDuration(*durationStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid --duration: %v\n", err)
			os.Exit(1)
		}
		go func() {
			select {
			case <-time.After(dur):
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	<-ctx.Done()
	fmt.Println("\nStopping monitor…")

	report, err := monitor.Stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "monitor stop: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n── RTP session report ────────────────────────────────────────\n")
	fmt.Printf("  Duration:     %.0f ms\n", report.DurationMs)
	fmt.Printf("  Packets rx:   %d  lost: %d (%.1f%%)\n",
		report.PacketsReceived, report.PacketsLost, report.LossPct)
	fmt.Printf("  Avg latency:  %.2f ms\n", report.AvgLatencyMs)
	fmt.Printf("  Avg jitter:   %.2f ms\n", report.AvgJitterMs)
	fmt.Printf("  Alerts fired: %d\n", report.AlertCount)
	fmt.Printf("  Recommendations:\n")
	for _, r := range report.Recommendations {
		fmt.Printf("    • %s\n", r)
	}
	fmt.Printf("──────────────────────────────────────────────────────────────\n")
	fmt.Printf("Reports written to: %s\n", *outputDir)
}

