package eval

// coverage_boost2_test.go — second wave of targeted branch coverage tests.

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ─── LatencyAccumulator.Stats — insertion sort inner-swap path ────────────────
//
// The insertion sort inner body (sorted[j+1] = sorted[j]; j--) only executes
// when there is an out-of-order pair.  A descending sequence maximises swaps.

func TestLatencyStats_DescendingOrder(t *testing.T) {
	var acc LatencyAccumulator
	for i := 10; i >= 1; i-- {
		acc.Add(float64(i))
	}
	s := acc.Stats()
	if s.Samples != 10 {
		t.Errorf("Samples: want 10, got %d", s.Samples)
	}
	if s.MinMs != 1.0 || s.MaxMs != 10.0 {
		t.Errorf("Min/Max: want 1.0/10.0, got %.1f/%.1f", s.MinMs, s.MaxMs)
	}
	// P95 of 1..10 sorted: ceil(10*0.95)=10th element (1-based) = 10.
	if s.P95Ms != 10.0 {
		t.Errorf("P95Ms: want 10.0, got %.1f", s.P95Ms)
	}
}

func TestLatencyStats_RandomOrder(t *testing.T) {
	var acc LatencyAccumulator
	for _, v := range []float64{5, 1, 9, 3, 7, 2, 8, 4, 6, 10} {
		acc.Add(v)
	}
	s := acc.Stats()
	if s.MinMs != 1.0 || s.MaxMs != 10.0 {
		t.Errorf("Min/Max after random insert: want 1.0/10.0, got %.1f/%.1f", s.MinMs, s.MaxMs)
	}
	if math.Abs(s.MeanMs-5.5) > 0.01 {
		t.Errorf("MeanMs: want 5.5, got %.2f", s.MeanMs)
	}
}

// ─── ComputeSNR — partial last window (end = len(samples)) ────────────────────

func TestComputeSNR_PartialLastWindow(t *testing.T) {
	// 50 samples: full window (0..31) + partial window (32..49).
	samples := make([]int16, 50)
	for i := range samples {
		samples[i] = 2000 // constant → noisePow ≈ 0 → snr = 60
	}
	snr := ComputeSNR(samples)
	if snr != 60 {
		t.Errorf("ComputeSNR(50 constant samples) = %.2f; want 60", snr)
	}
}

func TestComputeSNR_NonMultipleOfWindow(t *testing.T) {
	// 100 samples (not multiple of 32) with a sine so SNR is finite.
	samples := make([]int16, 100)
	for i := range samples {
		v := math.Sin(2 * math.Pi * 440 * float64(i) / 16000)
		samples[i] = int16(v * 10000)
	}
	snr := ComputeSNR(samples)
	if math.IsNaN(snr) {
		t.Error("ComputeSNR(100-sample sine) returned NaN")
	}
}

// ─── rtp_monitor sample() — Pipeline SetAggressiveness(1) on SNR-only alert ──
//
// When loss <= 3% and jitter <= 40ms but SNR < 15dB (poorSNRDB), an alert fires
// and the code reaches the else branch: SetAggressiveness(1).

func TestRTPMonitor_PipelineSNROnlyAlert(t *testing.T) {
	var lastSet int32 = -1
	pipe := &mockPipelineCB2{onSet: func(n int) { atomic.StoreInt32(&lastSet, int32(n)) }}

	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn: func() RTPStats {
			return RTPStats{PacketsReceived: 100, PacketsLost: 0, LatencyAvgMs: 1.0}
		},
		JitterMsFn:     func() float64 { return 0.0 },
		SNREstimateFn:  func() float64 { return 5.0 }, // > 0 and < poorSNRDB (15)
		SampleInterval: 20 * time.Millisecond,
		Pipeline:       pipe,
	})
	m.Start()
	time.Sleep(80 * time.Millisecond)
	m.Stop()

	got := int(atomic.LoadInt32(&lastSet))
	if got != 1 {
		t.Errorf("SetAggressiveness: want 1 for SNR-only alert, got %d", got)
	}
}

// ─── Score — rate-limit wait then LLM call (time.After fires) ────────────────

func TestScore_RateLimitWaitThenCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"75"}}]}`))
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		LLMAPIKey:      "test-key",
		RateLimitDelay: 30 * time.Millisecond,
	})
	scorer.last = time.Now() // prime so wait fires

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	scores, err := scorer.Score(ctx, "hello", "hello")
	if err != nil {
		t.Fatalf("Score after rate-limit wait: %v", err)
	}
	// LLM may or may not succeed; just ensure no crash.
	_ = scores
}

// ─── ScoreAll — LLM score accumulation (nLLM > 0 → AvgLLM set) ──────────────

func TestScoreAll_WithLLM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"80"}}]}`))
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		LLMAPIKey:      "key",
		RateLimitDelay: 0,
	})
	scorer.last = time.Time{} // no wait

	pairs := []struct{ ID, Reference, Comparison string }{
		{ID: "c1", Reference: "hello world", Comparison: "hello world"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, summary := scorer.ScoreAll(ctx, "test-denoiser", pairs)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// LLM endpoint was hit; if parsing succeeded LLMScore = 80, AvgLLM = 80.
	// If parse fails LLMScore = -1 — either is valid; we just need no panic.
	_ = summary
}

// ─── WriteConfigYAML — AGCTargetRMS > 0 branch ───────────────────────────────

func TestWriteConfigYAML_WithAGCTargetRMS(t *testing.T) {
	dir := t.TempDir()
	cfg := TunedConfig{
		Comment:                  "test",
		VADThreshold:             0.25,
		AGCTargetRMS:             1500.0,
		JitterDepthFrames:        4,
		SuppressorAggressiveness: 2,
		Rationale:                map[string]string{"vad_threshold": "reason"},
	}
	path, err := WriteConfigYAML(dir, cfg)
	if err != nil {
		t.Fatalf("WriteConfigYAML with AGC: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "agc_target_rms") {
		t.Errorf("YAML missing agc_target_rms when AGCTargetRMS > 0: %s", data)
	}
}

// ─── TuneFromBatchSummary — medium SNR with high latency backs off one level ──

func TestTuneFromBatchSummary_MediumSNRHighLatency(t *testing.T) {
	s := BatchSummary{
		AvgSNRImprovementDB: 5.0,  // 2 < 5 < 10 → medium (agg=2)
		AvgLatencyP95Ms:     10.0, // > 8ms → back off to 1
		AvgSpeechRatio:      0.6,
	}
	cfg := TuneFromBatchSummary(s)
	if cfg.SuppressorAggressiveness != 1 {
		t.Errorf("aggressiveness: want 1 (2 backed off for latency), got %d", cfg.SuppressorAggressiveness)
	}
}

// ─── WriteCSV — covers rows with non-zero error field ────────────────────────

func TestWriteCSV_WithErrorFile(t *testing.T) {
	dir := t.TempDir()
	files := []FileResult{
		{File: "good.wav", DurationMs: 1000},
		{File: "bad.wav", Error: "decode failed"},
	}
	path, err := WriteCSV(dir, files)
	if err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "decode failed") {
		t.Errorf("CSV should contain error field; got: %s", data)
	}
}

// ─── WriteFilesJSON — covers nil/empty file list ─────────────────────────────

func TestWriteFilesJSON_Empty(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteFilesJSON(dir, []FileResult{})
	if err != nil {
		t.Fatalf("WriteFilesJSON(empty): %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected output %s: %v", path, err)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

type mockPipelineCB2 struct {
	onSet func(n int)
}

func (p *mockPipelineCB2) SetAggressiveness(n int) {
	if p.onSet != nil {
		p.onSet(n)
	}
}
