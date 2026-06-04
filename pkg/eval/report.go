package eval

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ─── JSON ────────────────────────────────────────────────────────────────────

// WriteSummaryJSON writes a BatchSummary to <outputDir>/eval_summary_<ts>.json.
// Returns the written file path.
func WriteSummaryJSON(dir string, summary BatchSummary) (string, error) {
	ts := time.Now().Format("20060102_150405")
	path := filepath.Join(dir, fmt.Sprintf("eval_summary_%s.json", ts))
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("eval: create summary JSON: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(summary); err != nil {
		return "", fmt.Errorf("eval: encode summary JSON: %w", err)
	}
	return path, nil
}

// WriteFilesJSON writes the per-file results slice to <outputDir>/eval_files_<ts>.json.
func WriteFilesJSON(dir string, files []FileResult) (string, error) {
	ts := time.Now().Format("20060102_150405")
	path := filepath.Join(dir, fmt.Sprintf("eval_files_%s.json", ts))
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("eval: create files JSON: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(files); err != nil {
		return "", fmt.Errorf("eval: encode files JSON: %w", err)
	}
	return path, nil
}

// ─── CSV ─────────────────────────────────────────────────────────────────────

var csvHeaders = []string{
	"file",
	"duration_ms",
	"sample_rate",
	"channels",
	"snr_before_db",
	"snr_after_db",
	"snr_improvement_db",
	"latency_mean_ms",
	"latency_p95_ms",
	"latency_rtf",
	"vad_speech_frames",
	"vad_silence_frames",
	"vad_speech_ratio",
	"vad_cpu_saved_pct",
	"agc_target_rms",
	"agc_frames_to_converge",
	"agc_converged_ms",
	"agc_final_rms",
	"error",
}

// WriteCSV writes per-file results as CSV to <outputDir>/eval_files_<ts>.csv.
// Returns the written file path.
func WriteCSV(dir string, files []FileResult) (string, error) {
	ts := time.Now().Format("20060102_150405")
	path := filepath.Join(dir, fmt.Sprintf("eval_files_%s.csv", ts))
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("eval: create CSV: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(csvHeaders); err != nil {
		return "", fmt.Errorf("eval: write CSV header: %w", err)
	}
	for _, r := range files {
		row := []string{
			r.File,
			f64(r.DurationMs),
			strconv.Itoa(r.SampleRate),
			strconv.Itoa(r.Channels),
			f64(r.SNR.BeforeDB),
			f64(r.SNR.AfterDB),
			f64(r.SNR.ImprovementDB),
			f64(r.Latency.MeanMs),
			f64(r.Latency.P95Ms),
			f64(r.Latency.RealTimeFactor),
			strconv.Itoa(r.VAD.SpeechFrames),
			strconv.Itoa(r.VAD.SilenceFrames),
			f64(r.VAD.SpeechRatio),
			f64(r.VAD.CPUSavedPct),
			f64(r.AGC.TargetRMS),
			strconv.Itoa(r.AGC.FramesToConverge),
			f64(r.AGC.ConvergedMs),
			f64(r.AGC.FinalRMS),
			r.Error,
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("eval: write CSV row: %w", err)
		}
	}
	w.Flush()
	return path, w.Error()
}

func f64(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

// ─── Config recommendation YAML ──────────────────────────────────────────────

// TunedConfig holds recommended ClearStream configuration derived from
// observed quality metrics.  Serialised to YAML for easy copy-paste into
// production config.
type TunedConfig struct {
	// Comment is a human-readable explanation of why each value was chosen.
	Comment string `yaml:"# generated_by"`

	// VADThreshold is the recommended energy threshold for the voice activity detector.
	// Lower → more frames pass to suppressor (better quality, more CPU).
	// Higher → more frames skipped (saves CPU, may clip quiet speech).
	VADThreshold float64 `yaml:"vad_threshold"`

	// AGCTargetRMS is the recommended AGC output level.
	AGCTargetRMS float64 `yaml:"agc_target_rms,omitempty"`

	// JitterDepthFrames is the recommended jitter buffer depth in frames.
	JitterDepthFrames int `yaml:"jitter_depth_frames,omitempty"`

	// SuppressorAggressiveness is the recommended suppressor level (0–3).
	// 0 = off / passthrough, 1 = mild, 2 = medium (default), 3 = aggressive.
	SuppressorAggressiveness int `yaml:"suppressor_aggressiveness"`

	// Rationale maps each field to a one-line reason.
	Rationale map[string]string `yaml:"rationale"`
}

// TuneFromBatchSummary derives a TunedConfig from batch evaluation results.
// Rules:
//   - SNR improvement < 3 dB  → increase aggressiveness
//   - SNR improvement > 10 dB → decrease aggressiveness (over-suppressing risk)
//   - SpeechRatio > 0.80      → lower VAD threshold (lots of speech, don't skip)
//   - SpeechRatio < 0.40      → raise VAD threshold (lots of silence, save CPU)
//   - latency P95 > 8ms       → decrease aggressiveness (real-time budget risk)
func TuneFromBatchSummary(s BatchSummary) TunedConfig {
	c := TunedConfig{
		Comment:   fmt.Sprintf("clearstream-eval — generated from %d files (%.0f ms audio)", s.ProcessedFiles, s.TotalDurationMs),
		Rationale: make(map[string]string),
	}

	// ── Suppressor aggressiveness ────────────────────────────────────────────
	agg := 2 // start at medium
	snrImprove := s.AvgSNRImprovementDB
	switch {
	case snrImprove < 2.0:
		agg = 3
		c.Rationale["suppressor_aggressiveness"] = fmt.Sprintf(
			"avg SNR improvement=%.1f dB (< 2 dB) → increase to aggressive", snrImprove)
	case snrImprove > 10.0:
		agg = 1
		c.Rationale["suppressor_aggressiveness"] = fmt.Sprintf(
			"avg SNR improvement=%.1f dB (> 10 dB) → reduce to mild (avoid over-suppression)", snrImprove)
	default:
		c.Rationale["suppressor_aggressiveness"] = fmt.Sprintf(
			"avg SNR improvement=%.1f dB — medium aggressiveness is appropriate", snrImprove)
	}
	if s.AvgLatencyP95Ms > 8.0 {
		if agg > 1 {
			agg--
		}
		c.Rationale["suppressor_aggressiveness"] += fmt.Sprintf(
			"; P95 latency=%.1f ms > 8ms budget → backed off one level", s.AvgLatencyP95Ms)
	}
	c.SuppressorAggressiveness = agg

	// ── VAD threshold ────────────────────────────────────────────────────────
	switch {
	case s.AvgSpeechRatio > 0.80:
		c.VADThreshold = 0.15
		c.Rationale["vad_threshold"] = fmt.Sprintf(
			"speech ratio=%.0f%% (> 80%%) — low threshold, most frames are speech", s.AvgSpeechRatio*100)
	case s.AvgSpeechRatio < 0.40:
		c.VADThreshold = 0.35
		c.Rationale["vad_threshold"] = fmt.Sprintf(
			"speech ratio=%.0f%% (< 40%%) — high threshold, save CPU on long silences", s.AvgSpeechRatio*100)
	default:
		c.VADThreshold = 0.25
		c.Rationale["vad_threshold"] = fmt.Sprintf(
			"speech ratio=%.0f%% — balanced threshold", s.AvgSpeechRatio*100)
	}

	// ── Jitter buffer depth (RTP sessions) ───────────────────────────────────
	// Derived from latency P95: each jitter slot ≈ 10ms, aim for 2× tail latency headroom.
	depth := int(s.AvgLatencyP95Ms/10.0*2) + 2
	if depth < 2 {
		depth = 2
	}
	if depth > 16 {
		depth = 16
	}
	c.JitterDepthFrames = depth
	c.Rationale["jitter_depth_frames"] = fmt.Sprintf(
		"P95 latency=%.1f ms → depth=%d frames (2× headroom)", s.AvgLatencyP95Ms, depth)

	return c
}

// WriteConfigYAML writes a TunedConfig to <outputDir>/tuned_config_<ts>.yaml.
// Uses a hand-rolled YAML emitter to avoid adding a yaml dependency; the output
// is a simple flat YAML document that is valid YAML 1.2 and can be copy-pasted
// directly into a ClearStream config file.
// Returns the written file path.
func WriteConfigYAML(dir string, cfg TunedConfig) (string, error) {
	ts := time.Now().Format("20060102_150405")
	path := filepath.Join(dir, fmt.Sprintf("tuned_config_%s.yaml", ts))
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("eval: create config YAML: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	sb.WriteString("# ClearStream tuned configuration\n")
	sb.WriteString("# Generated by clearstream-eval\n")
	if cfg.Comment != "" {
		sb.WriteString("# ")
		sb.WriteString(cfg.Comment)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("vad_threshold: %s\n", f64(cfg.VADThreshold)))
	if cfg.AGCTargetRMS > 0 {
		sb.WriteString(fmt.Sprintf("agc_target_rms: %s\n", f64(cfg.AGCTargetRMS)))
	}
	if cfg.JitterDepthFrames > 0 {
		sb.WriteString(fmt.Sprintf("jitter_depth_frames: %d\n", cfg.JitterDepthFrames))
	}
	sb.WriteString(fmt.Sprintf("suppressor_aggressiveness: %d\n", cfg.SuppressorAggressiveness))

	if len(cfg.Rationale) > 0 {
		sb.WriteString("\nrationale:\n")
		for k, v := range cfg.Rationale {
			sb.WriteString(fmt.Sprintf("  %s: %q\n", k, v))
		}
	}

	_, err = f.WriteString(sb.String())
	return path, err
}

// WriteAllReports is a convenience wrapper that writes CSV + summary JSON +
// per-file JSON + tuned config YAML in one call.
// Returns paths to each written file.
func WriteAllReports(outputDir string, summary BatchSummary) (csvPath, summaryPath, filesPath, configPath string, err error) {
	csvPath, err = WriteCSV(outputDir, summary.Files)
	if err != nil {
		return
	}
	summaryPath, err = WriteSummaryJSON(outputDir, summary)
	if err != nil {
		return
	}
	filesPath, err = WriteFilesJSON(outputDir, summary.Files)
	if err != nil {
		return
	}
	tuned := TuneFromBatchSummary(summary)
	configPath, err = WriteConfigYAML(outputDir, tuned)
	return
}
