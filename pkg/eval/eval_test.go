package eval

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ─── metrics tests ─────────────────────────────────────────────────────────

// TestComputeSNR_Silence verifies that a silent (all-zero) signal returns 0 SNR.
func TestComputeSNR_Silence(t *testing.T) {
	samples := make([]int16, 1600)
	snr := ComputeSNR(samples)
	if snr != 0 {
		t.Errorf("expected SNR=0 for silence, got %.2f", snr)
	}
}

// TestComputeSNR_PureSine verifies that a stable sine wave returns a high SNR
// (low noise variance → high SNR estimate).
func TestComputeSNR_PureSine(t *testing.T) {
	n := 1600
	samples := make([]int16, n)
	for i := range samples {
		v := math.Sin(2 * math.Pi * 440 * float64(i) / 16000)
		samples[i] = int16(v * 16000)
	}
	snr := ComputeSNR(samples)
	// Pure tone has low variance → high SNR (expect > 15 dB)
	if snr < 15.0 {
		t.Errorf("pure sine SNR=%.2f dB, expected > 15 dB", snr)
	}
}

// TestComputeSNRPair_ImprovesAfterSuppression checks that ComputeSNRPair
// reports a positive improvement when the output is cleaner than input.
func TestComputeSNRPair_ImprovesAfterSuppression(t *testing.T) {
	// Simulate: noisy input = pure sine + high-amplitude noise burst in first half.
	n := 1600
	before := make([]int16, n)
	after := make([]int16, n)
	for i := range before {
		clean := math.Sin(2 * math.Pi * 440 * float64(i) / 16000)
		noise := float64(0)
		if i < n/2 {
			noise = 0.8 * math.Sin(2*math.Pi*3000*float64(i)/16000) // high-freq noise
		}
		before[i] = int16((clean + noise) * 10000)
		after[i] = int16(clean * 10000)
	}
	result := ComputeSNRPair(before, after)
	if result.ImprovementDB <= 0 {
		t.Errorf("expected positive SNR improvement, got %.2f dB", result.ImprovementDB)
	}
	t.Logf("SNR: before=%.1f dB, after=%.1f dB, improvement=%.1f dB",
		result.BeforeDB, result.AfterDB, result.ImprovementDB)
}

// TestRMSLevel checks that RMS of a full-scale square wave is near 32767.
func TestRMSLevel(t *testing.T) {
	samples := make([]int16, 160)
	for i := range samples {
		if i%2 == 0 {
			samples[i] = 32767
		} else {
			samples[i] = -32767
		}
	}
	rms := RMSLevel(samples)
	if math.Abs(rms-32767) > 1 {
		t.Errorf("RMSLevel of square wave: want ~32767, got %.1f", rms)
	}
}

// TestLatencyAccumulator_Stats verifies min/max/mean/P95 correctness.
func TestLatencyAccumulator_Stats(t *testing.T) {
	var acc LatencyAccumulator
	for i := 1; i <= 100; i++ {
		acc.Add(float64(i)) // 1ms, 2ms, … 100ms
	}
	s := acc.Stats()
	if s.Samples != 100 {
		t.Errorf("Samples: want 100, got %d", s.Samples)
	}
	if s.MinMs != 1.0 {
		t.Errorf("MinMs: want 1.0, got %.2f", s.MinMs)
	}
	if s.MaxMs != 100.0 {
		t.Errorf("MaxMs: want 100.0, got %.2f", s.MaxMs)
	}
	if math.Abs(s.MeanMs-50.5) > 0.01 {
		t.Errorf("MeanMs: want 50.5, got %.2f", s.MeanMs)
	}
	// P95 of 1..100 = value at index 94 (0-based) = 95.
	if s.P95Ms != 95.0 {
		t.Errorf("P95Ms: want 95.0, got %.2f", s.P95Ms)
	}
	// RTF for mean 50.5ms: 50.5/10 = 5.05
	if math.Abs(s.RealTimeFactor-5.05) > 0.01 {
		t.Errorf("RealTimeFactor: want 5.05, got %.4f", s.RealTimeFactor)
	}
}

// TestLatencyAccumulator_Empty returns zero value for empty accumulator.
func TestLatencyAccumulator_Empty(t *testing.T) {
	var acc LatencyAccumulator
	s := acc.Stats()
	if s.Samples != 0 || s.MeanMs != 0 || s.P95Ms != 0 {
		t.Errorf("empty accumulator returned non-zero stats: %+v", s)
	}
}

// TestComputeVADStats_AllSpeech verifies 100% speech → 0% silence → 0% CPU saved.
func TestComputeVADStats_AllSpeech(t *testing.T) {
	s := ComputeVADStats(100, 0)
	if s.SpeechRatio != 1.0 {
		t.Errorf("SpeechRatio: want 1.0, got %.2f", s.SpeechRatio)
	}
	if s.CPUSavedPct != 0 {
		t.Errorf("CPUSavedPct: want 0, got %.2f", s.CPUSavedPct)
	}
}

// TestComputeVADStats_HalfSilence verifies 50% silence → ~15% CPU saved.
func TestComputeVADStats_HalfSilence(t *testing.T) {
	s := ComputeVADStats(100, 50)
	if s.SpeechRatio != 0.5 {
		t.Errorf("SpeechRatio: want 0.5, got %.2f", s.SpeechRatio)
	}
	if math.Abs(s.CPUSavedPct-15.0) > 0.01 {
		t.Errorf("CPUSavedPct: want 15.0, got %.2f", s.CPUSavedPct)
	}
}

// TestAggregateResults verifies aggregate arithmetic across a small result set.
func TestAggregateResults(t *testing.T) {
	files := []FileResult{
		{DurationMs: 1000, SNR: SNRResult{BeforeDB: 10, AfterDB: 20, ImprovementDB: 10},
			Latency: LatencyStats{MeanMs: 1.0, P95Ms: 2.0},
			VAD:     VADStats{SpeechRatio: 0.8, CPUSavedPct: 6}},
		{DurationMs: 2000, SNR: SNRResult{BeforeDB: 5, AfterDB: 15, ImprovementDB: 10},
			Latency: LatencyStats{MeanMs: 2.0, P95Ms: 4.0},
			VAD:     VADStats{SpeechRatio: 0.6, CPUSavedPct: 12}},
		{Error: "decode failed"}, // should not count
	}
	s := AggregateResults(files, 500)
	if s.TotalFiles != 3 {
		t.Errorf("TotalFiles: want 3, got %d", s.TotalFiles)
	}
	if s.ProcessedFiles != 2 {
		t.Errorf("ProcessedFiles: want 2, got %d", s.ProcessedFiles)
	}
	if s.FailedFiles != 1 {
		t.Errorf("FailedFiles: want 1, got %d", s.FailedFiles)
	}
	if math.Abs(s.AvgSNRImprovementDB-10.0) > 0.01 {
		t.Errorf("AvgSNRImprovementDB: want 10.0, got %.2f", s.AvgSNRImprovementDB)
	}
	if math.Abs(s.TotalDurationMs-3000) > 0.01 {
		t.Errorf("TotalDurationMs: want 3000, got %.1f", s.TotalDurationMs)
	}
	// SpeedRatio = 3000ms / 500ms = 6.
	if math.Abs(s.SpeedRatio-6.0) > 0.01 {
		t.Errorf("SpeedRatio: want 6.0, got %.2f", s.SpeedRatio)
	}
}

// ─── report tests ───────────────────────────────────────────────────────────

func makeSummary() BatchSummary {
	return BatchSummary{
		TotalFiles:          2,
		ProcessedFiles:      2,
		AvgSNRBeforeDB:      8.5,
		AvgSNRAfterDB:       16.0,
		AvgSNRImprovementDB: 7.5,
		AvgLatencyMeanMs:    1.2,
		AvgLatencyP95Ms:     3.0,
		AvgSpeechRatio:      0.65,
		AvgCPUSavedPct:      10.5,
		TotalDurationMs:     5000,
		WallClockMs:         1000,
		SpeedRatio:          5.0,
		Files: []FileResult{
			{File: "a.wav", DurationMs: 2000, SNR: SNRResult{ImprovementDB: 8}},
			{File: "b.mp3", DurationMs: 3000, SNR: SNRResult{ImprovementDB: 7}},
		},
	}
}

// TestWriteCSV verifies the CSV has a header + one row per file.
func TestWriteCSV(t *testing.T) {
	dir := t.TempDir()
	summary := makeSummary()
	path, err := WriteCSV(dir, summary.Files)
	if err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open CSV: %v", err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}
	if len(rows) != 3 { // header + 2 data rows
		t.Errorf("CSV rows: want 3, got %d", len(rows))
	}
	if rows[0][0] != "file" {
		t.Errorf("first CSV header: want 'file', got %q", rows[0][0])
	}
	if !strings.Contains(rows[1][0], "a.wav") {
		t.Errorf("first data row file: want a.wav, got %q", rows[1][0])
	}
}

// TestWriteSummaryJSON verifies the JSON is valid and contains expected fields.
func TestWriteSummaryJSON(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteSummaryJSON(dir, makeSummary())
	if err != nil {
		t.Fatalf("WriteSummaryJSON: %v", err)
	}
	data, _ := os.ReadFile(path)
	var out BatchSummary
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal summary JSON: %v", err)
	}
	if out.TotalFiles != 2 {
		t.Errorf("TotalFiles: want 2, got %d", out.TotalFiles)
	}
	if math.Abs(out.AvgSNRImprovementDB-7.5) > 0.01 {
		t.Errorf("AvgSNRImprovementDB: want 7.5, got %.2f", out.AvgSNRImprovementDB)
	}
}

// TestWriteConfigYAML verifies YAML is written and has expected keys.
func TestWriteConfigYAML(t *testing.T) {
	dir := t.TempDir()
	cfg := TuneFromBatchSummary(makeSummary())
	path, err := WriteConfigYAML(dir, cfg)
	if err != nil {
		t.Fatalf("WriteConfigYAML: %v", err)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	for _, key := range []string{"vad_threshold", "suppressor_aggressiveness", "jitter_depth_frames"} {
		if !strings.Contains(content, key) {
			t.Errorf("YAML missing key %q: %s", key, content)
		}
	}
}

// TestWriteAllReports verifies all 4 output files are created.
func TestWriteAllReports(t *testing.T) {
	dir := t.TempDir()
	csv, summ, files, cfg, err := WriteAllReports(dir, makeSummary())
	if err != nil {
		t.Fatalf("WriteAllReports: %v", err)
	}
	for _, p := range []string{csv, summ, files, cfg} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected output file %s to exist: %v", p, err)
		}
	}
}

// ─── tuner tests ────────────────────────────────────────────────────────────

// TestTuneFromBatchSummary_LowSNR verifies aggressive suppression is recommended
// when SNR improvement is below 2 dB.
func TestTuneFromBatchSummary_LowSNR(t *testing.T) {
	s := makeSummary()
	s.AvgSNRImprovementDB = 1.0 // < 2 dB → should recommend aggressiveness=3
	cfg := TuneFromBatchSummary(s)
	if cfg.SuppressorAggressiveness != 3 {
		t.Errorf("aggressiveness: want 3 for low SNR improvement, got %d", cfg.SuppressorAggressiveness)
	}
}

// TestTuneFromBatchSummary_HighSNR verifies mild suppression is recommended
// when SNR improvement exceeds 10 dB (over-suppression risk).
func TestTuneFromBatchSummary_HighSNR(t *testing.T) {
	s := makeSummary()
	s.AvgSNRImprovementDB = 12.0 // > 10 dB → mild
	cfg := TuneFromBatchSummary(s)
	if cfg.SuppressorAggressiveness != 1 {
		t.Errorf("aggressiveness: want 1 for high SNR improvement, got %d", cfg.SuppressorAggressiveness)
	}
}

// TestTuneFromBatchSummary_HighLatency verifies aggressiveness is dialled back
// when P95 latency exceeds 8ms.
func TestTuneFromBatchSummary_HighLatency(t *testing.T) {
	s := makeSummary()
	s.AvgSNRImprovementDB = 1.0  // low SNR → would push to 3
	s.AvgLatencyP95Ms = 10.0     // high latency → back off 1
	cfg := TuneFromBatchSummary(s)
	// Started at 3 (low SNR), backed off to 2 (high latency).
	if cfg.SuppressorAggressiveness != 2 {
		t.Errorf("aggressiveness: want 2 (backed off for latency), got %d", cfg.SuppressorAggressiveness)
	}
}

// TestTuneFromBatchSummary_VADThresholds verifies VAD threshold rules.
func TestTuneFromBatchSummary_VADThresholds(t *testing.T) {
	cases := []struct {
		speechRatio float64
		wantVAD     float64
	}{
		{0.85, 0.15}, // lots of speech → low threshold
		{0.30, 0.35}, // lots of silence → high threshold
		{0.60, 0.25}, // balanced
	}
	for _, tc := range cases {
		s := makeSummary()
		s.AvgSpeechRatio = tc.speechRatio
		cfg := TuneFromBatchSummary(s)
		if math.Abs(cfg.VADThreshold-tc.wantVAD) > 0.001 {
			t.Errorf("speech=%.0f%% → VADThreshold: want %.2f, got %.2f",
				tc.speechRatio*100, tc.wantVAD, cfg.VADThreshold)
		}
	}
}

// ─── RTP monitor tests ──────────────────────────────────────────────────────

// TestRTPMonitor_StartsAndStops verifies the monitor can start and stop
// cleanly without blocking.
func TestRTPMonitor_StartsAndStops(t *testing.T) {
	var rx, lost uint64
	statsFn := func() RTPStats {
		rx += 10
		return RTPStats{PacketsReceived: rx, PacketsLost: lost, LatencyAvgMs: 2.0}
	}

	dir := t.TempDir()
	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn:        statsFn,
		OutputDir:      dir,
		SampleInterval: 50 * time.Millisecond,
	})
	m.Start()
	time.Sleep(200 * time.Millisecond)
	report, err := m.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(report.Snapshots) == 0 {
		t.Error("expected at least one snapshot")
	}
	t.Logf("snapshots=%d duration=%.0fms", len(report.Snapshots), report.DurationMs)
}

// TestRTPMonitor_FiresAlert verifies that high packet loss triggers an alert.
func TestRTPMonitor_FiresAlert(t *testing.T) {
	var rx uint64 = 1000
	var lost uint64 = 50 // 5% loss > 3% threshold

	var alertFired int32 // 0=false, 1=true (atomic)
	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn: func() RTPStats {
			return RTPStats{PacketsReceived: rx, PacketsLost: lost, LatencyAvgMs: 1.0}
		},
		SampleInterval: 20 * time.Millisecond,
		OnAlert: func(msg string) {
			atomic.StoreInt32(&alertFired, 1)
		},
	})
	m.Start()
	time.Sleep(80 * time.Millisecond)
	report, _ := m.Stop()

	if atomic.LoadInt32(&alertFired) == 0 {
		t.Error("expected alert to fire on 5% packet loss")
	}
	if report.AlertCount == 0 {
		t.Error("expected AlertCount > 0")
	}
}

// TestRTPMonitor_GoodQuality verifies no alert fires when session is clean.
func TestRTPMonitor_GoodQuality(t *testing.T) {
	var rx uint64
	alertCount := 0
	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn: func() RTPStats {
			rx += 100
			return RTPStats{PacketsReceived: rx, PacketsLost: 0, LatencyAvgMs: 1.5}
		},
		SampleInterval: 20 * time.Millisecond,
		OnAlert:        func(msg string) { alertCount++ },
	})
	m.Start()
	time.Sleep(100 * time.Millisecond)
	report, _ := m.Stop()

	if alertCount != 0 {
		t.Errorf("expected 0 alerts for clean session, got %d", alertCount)
	}
	if len(report.Recommendations) == 0 {
		t.Error("expected at least one recommendation (good quality message)")
	}
	t.Logf("recommendations: %v", report.Recommendations)
}

// TestRTPMonitor_WritesOutputFiles verifies JSON and YAML are written.
func TestRTPMonitor_WritesOutputFiles(t *testing.T) {
	dir := t.TempDir()
	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn: func() RTPStats {
			return RTPStats{PacketsReceived: 100, PacketsLost: 0, LatencyAvgMs: 2.0}
		},
		OutputDir:      dir,
		SampleInterval: 30 * time.Millisecond,
	})
	m.Start()
	time.Sleep(80 * time.Millisecond)
	_, err := m.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	var hasJSON, hasYAML bool
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			hasJSON = true
		}
		if strings.HasSuffix(e.Name(), ".yaml") {
			hasYAML = true
		}
	}
	if !hasJSON {
		t.Error("expected rtp_eval_*.json to be written")
	}
	if !hasYAML {
		t.Error("expected rtp_tuned_config_*.yaml to be written")
	}
}

// TestRTPMonitor_StopIdempotent verifies Stop() can be called twice safely.
func TestRTPMonitor_StopIdempotent(t *testing.T) {
	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn:        func() RTPStats { return RTPStats{} },
		SampleInterval: 50 * time.Millisecond,
	})
	m.Start()
	time.Sleep(60 * time.Millisecond)
	m.Stop()
	m.Stop() // must not panic
}

// ─── batch runner integration test (uses testdata WAVs) ─────────────────────

// TestBatchRunner_OnTestdata runs the batch runner over the project testdata
// directory (sample_clean.wav, sample_noisy.wav, sample_office.wav).
// Requires ffmpeg in PATH; skipped otherwise.
func TestBatchRunner_OnTestdata(t *testing.T) {
	if _, err := findFFmpeg(); err != nil {
		t.Skip("ffmpeg not in PATH — skipping batch integration test")
	}

	// Find testdata relative to module root.
	inputDir := filepath.Join("..", "..", "testdata")
	if _, err := os.Stat(inputDir); err != nil {
		t.Skipf("testdata dir not found at %s — skipping", inputDir)
	}

	outputDir := t.TempDir()
	runner := NewBatchRunner(BatchConfig{
		InputDir:   inputDir,
		OutputDir:  outputDir,
		Workers:    2,
		Suppressor: &passthroughSuppressor{},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	summary, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("BatchRunner.Run: %v", err)
	}

	if summary.TotalFiles == 0 {
		t.Fatal("expected at least one WAV file in testdata")
	}
	if summary.FailedFiles != 0 {
		for _, f := range summary.Files {
			if f.Error != "" {
				t.Logf("failed file %s: %s", f.File, f.Error)
			}
		}
		t.Errorf("expected 0 failed files, got %d", summary.FailedFiles)
	}
	// Every processed file should have a non-zero duration.
	for _, f := range summary.Files {
		if f.Error != "" {
			continue
		}
		if f.DurationMs <= 0 {
			t.Errorf("file %s: DurationMs=0, expected > 0", f.File)
		}
		if f.Latency.Samples == 0 {
			t.Errorf("file %s: no latency samples recorded", f.File)
		}
	}
	t.Logf("batch summary: %d/%d ok, avgSNR=%.1f→%.1f (+%.1f dB), latencyMean=%.2f ms",
		summary.ProcessedFiles, summary.TotalFiles,
		summary.AvgSNRBeforeDB, summary.AvgSNRAfterDB, summary.AvgSNRImprovementDB,
		summary.AvgLatencyMeanMs)
}

// ─── helpers ────────────────────────────────────────────────────────────────

func findFFmpeg() (string, error) {
	// Try the default name; exec.LookPath would be cleaner but we avoid importing os/exec here.
	if _, err := os.Stat("/usr/local/bin/ffmpeg"); err == nil {
		return "/usr/local/bin/ffmpeg", nil
	}
	if _, err := os.Stat("/usr/bin/ffmpeg"); err == nil {
		return "/usr/bin/ffmpeg", nil
	}
	if _, err := os.Stat("/opt/homebrew/bin/ffmpeg"); err == nil {
		return "/opt/homebrew/bin/ffmpeg", nil
	}
	return "", os.ErrNotExist
}

// passthroughSuppressor is a minimal noise suppressor that returns input unchanged.
type passthroughSuppressor struct{}

func (p *passthroughSuppressor) Process(s []int16) ([]int16, error) {
	out := make([]int16, len(s))
	copy(out, s)
	return out, nil
}
func (p *passthroughSuppressor) Reset()       {}
func (p *passthroughSuppressor) Close() error { return nil }
func (p *passthroughSuppressor) Name() string { return "passthrough-eval-test" }
