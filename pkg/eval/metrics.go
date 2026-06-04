// Package eval provides batch and real-time quality evaluation for the
// ClearStream audio enhancement SDK.
//
// # Batch eval — process 1 000 recordings
//
//	runner := eval.NewBatchRunner(eval.BatchConfig{
//	    InputDir:    "./recordings",
//	    OutputDir:   "./eval-out",
//	    Workers:     runtime.NumCPU(),
//	    Suppressor:  model.NewPassthrough(),
//	    OnProgress:  func(done, total int) { fmt.Printf("%d/%d\n", done, total) },
//	})
//	summary, err := runner.Run(ctx)
//
// # Real-time RTP eval
//
//	monitor := eval.NewRTPMonitor(eval.RTPMonitorConfig{
//	    Session:   rtpSess,
//	    OutputDir: "./eval-out",
//	})
//	monitor.Start()
//	// ... call runs ...
//	report, err := monitor.Stop()   // writes tuned config YAML + JSON
package eval

import (
	"math"
)

// ─── SNR ────────────────────────────────────────────────────────────────────

// SNRResult holds signal-to-noise measurements for one audio file.
type SNRResult struct {
	// BeforeDB is the estimated SNR of the raw input (dB).
	BeforeDB float64 `json:"before_db"`
	// AfterDB is the estimated SNR of the suppressed output (dB).
	AfterDB float64 `json:"after_db"`
	// ImprovementDB is AfterDB - BeforeDB (positive = better).
	ImprovementDB float64 `json:"improvement_db"`
}

// ComputeSNR estimates the SNR of samples by treating the signal power as the
// per-sample RMS energy and the noise as the deviation from a smoothed
// reference.  This is a blind estimate (no ground-truth clean file needed):
//
//	SNR = 10 · log10(signal_power / noise_power)
//
// where signal = long-time-averaged RMS, noise = frame-level deviation from
// that average.  Works well for stationary noise (AWGN, hum); less accurate
// for highly non-stationary noise like street ambience.
func ComputeSNR(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	// 1. Compute long-time RMS (signal estimate).
	var sumSq float64
	for _, s := range samples {
		v := float64(s)
		sumSq += v * v
	}
	rms := math.Sqrt(sumSq / float64(len(samples)))
	if rms < 1e-9 {
		return 0 // silence
	}

	// 2. Noise = per-sample deviation from the long-time RMS envelope.
	//    Use a 32-sample (2ms) sliding RMS as the local reference; deviation
	//    from that is our noise estimate.
	const winSize = 32
	var noiseSumSq float64
	for i := 0; i < len(samples); i += winSize {
		end := i + winSize
		if end > len(samples) {
			end = len(samples)
		}
		win := samples[i:end]
		var wSumSq float64
		for _, s := range win {
			v := float64(s)
			wSumSq += v * v
		}
		localRMS := math.Sqrt(wSumSq / float64(len(win)))
		diff := localRMS - rms
		noiseSumSq += diff * diff * float64(len(win))
	}
	noisePow := noiseSumSq / float64(len(samples))
	if noisePow < 1e-9 {
		return 60 // essentially perfect
	}
	return 10 * math.Log10((rms*rms)/noisePow)
}

// ComputeSNRPair computes SNRResult given raw (before) and cleaned (after) samples.
func ComputeSNRPair(before, after []int16) SNRResult {
	r := SNRResult{
		BeforeDB: ComputeSNR(before),
		AfterDB:  ComputeSNR(after),
	}
	r.ImprovementDB = r.AfterDB - r.BeforeDB
	return r
}

// ─── RMS ─────────────────────────────────────────────────────────────────────

// RMSLevel computes the root-mean-square amplitude of samples (0..32767 scale).
func RMSLevel(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// ─── Latency ─────────────────────────────────────────────────────────────────

// LatencyStats holds per-file or per-session processing latency metrics.
type LatencyStats struct {
	// Samples is the number of frame latency measurements.
	Samples int `json:"samples"`
	// MinMs is the fastest frame processing time observed.
	MinMs float64 `json:"min_ms"`
	// MaxMs is the slowest frame processing time observed.
	MaxMs float64 `json:"max_ms"`
	// MeanMs is the arithmetic mean of all frame latencies.
	MeanMs float64 `json:"mean_ms"`
	// P95Ms is the 95th-percentile latency (a.k.a. tail latency).
	P95Ms float64 `json:"p95_ms"`
	// RealTimeFactor is MeanMs / 10.0 (< 1.0 means faster than real-time).
	// A 10ms frame budget requires processing in ≤10ms to be real-time.
	RealTimeFactor float64 `json:"real_time_factor"`
}

// LatencyAccumulator collects raw latency samples for later analysis.
type LatencyAccumulator struct {
	samples []float64
	sum     float64
	min     float64
	max     float64
}

// Add records one frame latency measurement (in milliseconds).
func (a *LatencyAccumulator) Add(ms float64) {
	if len(a.samples) == 0 || ms < a.min {
		a.min = ms
	}
	if ms > a.max {
		a.max = ms
	}
	a.sum += ms
	a.samples = append(a.samples, ms)
}

// Stats returns the computed LatencyStats. Returns zero value if no samples.
func (a *LatencyAccumulator) Stats() LatencyStats {
	n := len(a.samples)
	if n == 0 {
		return LatencyStats{}
	}
	mean := a.sum / float64(n)

	// P95 via insertion sort on a copy (n is typically ≤ few thousand per file).
	sorted := make([]float64, n)
	copy(sorted, a.samples)
	for i := 1; i < n; i++ {
		key := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > key {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = key
	}
	p95 := sorted[int(math.Ceil(float64(n)*0.95))-1]

	return LatencyStats{
		Samples:        n,
		MinMs:          a.min,
		MaxMs:          a.max,
		MeanMs:         mean,
		P95Ms:          p95,
		RealTimeFactor: mean / 10.0, // 10ms = one frame duration
	}
}

// ─── VAD accuracy ─────────────────────────────────────────────────────────────

// VADStats tracks pipeline VAD decisions across frames.
type VADStats struct {
	// TotalFrames is the total number of frames evaluated.
	TotalFrames int `json:"total_frames"`
	// SpeechFrames is the number of frames the VAD classified as speech.
	SpeechFrames int `json:"speech_frames"`
	// SilenceFrames is the number of frames the VAD classified as silence.
	SilenceFrames int `json:"silence_frames"`
	// SpeechRatio is SpeechFrames / TotalFrames (0–1).
	SpeechRatio float64 `json:"speech_ratio"`
	// CPUSavedPct is the estimated CPU saving from skipping suppression on silence.
	// Modelled as SilenceFrames/TotalFrames × 30% (empirical from Day-2 measurements).
	CPUSavedPct float64 `json:"cpu_saved_pct"`
}

// ComputeVADStats builds a VADStats from a PipelineStats snapshot.
func ComputeVADStats(framesProcessed, framesSilent uint64) VADStats {
	if framesProcessed == 0 {
		return VADStats{}
	}
	silenceRatio := float64(framesSilent) / float64(framesProcessed)
	return VADStats{
		TotalFrames:   int(framesProcessed),
		SpeechFrames:  int(framesProcessed - framesSilent),
		SilenceFrames: int(framesSilent),
		SpeechRatio:   1 - silenceRatio,
		CPUSavedPct:   silenceRatio * 30.0,
	}
}

// ─── AGC convergence ─────────────────────────────────────────────────────────

// AGCConvergence tracks how many frames the AGC needed to reach target RMS.
type AGCConvergence struct {
	// TargetRMS is the configured AGC target.
	TargetRMS float64 `json:"target_rms"`
	// FramesToConverge is the number of frames until output RMS was within 20%
	// of TargetRMS. -1 means it never converged within the file.
	FramesToConverge int `json:"frames_to_converge"`
	// ConvergedMs is FramesToConverge × 10ms.
	ConvergedMs float64 `json:"converged_ms"`
	// FinalRMS is the output RMS of the last frame processed.
	FinalRMS float64 `json:"final_rms"`
}

// ─── Per-file result ─────────────────────────────────────────────────────────

// FileResult holds all quality metrics for a single audio file evaluation.
type FileResult struct {
	// File is the input file path.
	File string `json:"file"`
	// DurationMs is the audio duration in milliseconds.
	DurationMs float64 `json:"duration_ms"`
	// SampleRate is the detected sample rate of the file.
	SampleRate int `json:"sample_rate"`
	// Channels is the number of audio channels.
	Channels int `json:"channels"`
	// SNR holds before/after SNR measurements.
	SNR SNRResult `json:"snr"`
	// Latency holds per-frame processing latency stats.
	Latency LatencyStats `json:"latency"`
	// VAD holds voice activity detection stats.
	VAD VADStats `json:"vad"`
	// AGC holds gain convergence stats (zero if AGC disabled).
	AGC AGCConvergence `json:"agc"`
	// Error is non-empty if processing failed.
	Error string `json:"error,omitempty"`
}

// ─── Batch summary ────────────────────────────────────────────────────────────

// BatchSummary aggregates FileResult metrics across all processed files.
type BatchSummary struct {
	// TotalFiles is the number of input files.
	TotalFiles int `json:"total_files"`
	// ProcessedFiles is the number that completed without error.
	ProcessedFiles int `json:"processed_files"`
	// FailedFiles is the number that errored out.
	FailedFiles int `json:"failed_files"`
	// TotalDurationMs is cumulative audio duration processed.
	TotalDurationMs float64 `json:"total_duration_ms"`
	// WallClockMs is the real elapsed time for the whole batch.
	WallClockMs float64 `json:"wall_clock_ms"`
	// AvgSNRBeforeDB is the mean input SNR across all files.
	AvgSNRBeforeDB float64 `json:"avg_snr_before_db"`
	// AvgSNRAfterDB is the mean output SNR across all files.
	AvgSNRAfterDB float64 `json:"avg_snr_after_db"`
	// AvgSNRImprovementDB is the mean SNR gain (positive = better).
	AvgSNRImprovementDB float64 `json:"avg_snr_improvement_db"`
	// AvgLatencyMeanMs is the mean per-frame latency across all files.
	AvgLatencyMeanMs float64 `json:"avg_latency_mean_ms"`
	// AvgLatencyP95Ms is the mean P95 latency across all files.
	AvgLatencyP95Ms float64 `json:"avg_latency_p95_ms"`
	// AvgSpeechRatio is the mean fraction of frames classified as speech.
	AvgSpeechRatio float64 `json:"avg_speech_ratio"`
	// AvgCPUSavedPct is the estimated CPU saving from VAD silence bypass.
	AvgCPUSavedPct float64 `json:"avg_cpu_saved_pct"`
	// SpeedRatio is TotalDurationMs / WallClockMs (> 1 = faster than real-time).
	SpeedRatio float64 `json:"speed_ratio"`
	// Files contains per-file results.
	Files []FileResult `json:"files"`
}

// AggregateResults computes a BatchSummary from a slice of FileResults and
// the wall-clock duration of the whole batch run.
func AggregateResults(results []FileResult, wallClockMs float64) BatchSummary {
	s := BatchSummary{
		TotalFiles:  len(results),
		WallClockMs: wallClockMs,
		Files:       results,
	}
	var (
		snrBefore, snrAfter, snrImprove float64
		latMean, latP95                 float64
		speechRatio, cpuSaved           float64
		totalDuration                   float64
		n                               float64 // processed (non-error) count
	)
	for _, r := range results {
		if r.Error != "" {
			s.FailedFiles++
			continue
		}
		s.ProcessedFiles++
		n++
		totalDuration += r.DurationMs
		snrBefore += r.SNR.BeforeDB
		snrAfter += r.SNR.AfterDB
		snrImprove += r.SNR.ImprovementDB
		latMean += r.Latency.MeanMs
		latP95 += r.Latency.P95Ms
		speechRatio += r.VAD.SpeechRatio
		cpuSaved += r.VAD.CPUSavedPct
	}
	s.TotalDurationMs = totalDuration
	if n > 0 {
		s.AvgSNRBeforeDB = snrBefore / n
		s.AvgSNRAfterDB = snrAfter / n
		s.AvgSNRImprovementDB = snrImprove / n
		s.AvgLatencyMeanMs = latMean / n
		s.AvgLatencyP95Ms = latP95 / n
		s.AvgSpeechRatio = speechRatio / n
		s.AvgCPUSavedPct = cpuSaved / n
	}
	if wallClockMs > 0 {
		s.SpeedRatio = totalDuration / wallClockMs
	}
	return s
}
