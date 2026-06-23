package eval

// coverage_boost_test.go — targeted tests to push pkg/eval coverage from ~69.5%
// toward 75%+.  Each test is aimed at a specific uncovered branch identified via
// go tool cover -func output.

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

// ─── NewRTPMonitor ───────────────────────────────────────────────────────────

// TestNewRTPMonitor_PanicsOnNilStatsFn verifies the documented panic.
func TestNewRTPMonitor_PanicsOnNilStatsFn(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when StatsFn is nil")
		}
	}()
	NewRTPMonitor(RTPMonitorConfig{StatsFn: nil})
}

// TestNewRTPMonitor_DefaultSampleInterval verifies that a zero SampleInterval
// is replaced with 1 second.
func TestNewRTPMonitor_DefaultSampleInterval(t *testing.T) {
	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn:        func() RTPStats { return RTPStats{} },
		SampleInterval: 0, // must default to 1s
	})
	if m.cfg.SampleInterval != time.Second {
		t.Errorf("SampleInterval: want 1s, got %v", m.cfg.SampleInterval)
	}
}

// TestNewRTPMonitor_ExplicitSampleInterval verifies that a non-zero value is kept.
func TestNewRTPMonitor_ExplicitSampleInterval(t *testing.T) {
	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn:        func() RTPStats { return RTPStats{} },
		SampleInterval: 200 * time.Millisecond,
	})
	if m.cfg.SampleInterval != 200*time.Millisecond {
		t.Errorf("SampleInterval: want 200ms, got %v", m.cfg.SampleInterval)
	}
}

// ─── sample() / recommend() via integration ──────────────────────────────────

// TestRTPMonitor_PipelineSetAggressiveness_HighLoss verifies that Pipeline
// receives SetAggressiveness(3) when loss > 3%.
func TestRTPMonitor_PipelineSetAggressiveness_HighLoss(t *testing.T) {
	var aggressSet int32
	pipe := &mockPipeline{onSet: func(n int) { atomic.StoreInt32(&aggressSet, int32(n)) }}

	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn: func() RTPStats {
			// 10% loss — well above poorLossPct (3%)
			return RTPStats{PacketsReceived: 100, PacketsLost: 10, LatencyAvgMs: 1.0}
		},
		SampleInterval: 20 * time.Millisecond,
		Pipeline:       pipe,
	})
	m.Start()
	time.Sleep(80 * time.Millisecond)
	m.Stop()

	got := int(atomic.LoadInt32(&aggressSet))
	if got != 3 {
		t.Errorf("Pipeline.SetAggressiveness: want 3 for >3%% loss, got %d", got)
	}
}

// TestRTPMonitor_PipelineSetAggressiveness_HighJitter verifies SetAggressiveness(2)
// for high jitter when loss is within bounds.
func TestRTPMonitor_PipelineSetAggressiveness_HighJitter(t *testing.T) {
	var lastSet int32
	pipe := &mockPipeline{onSet: func(n int) { atomic.StoreInt32(&lastSet, int32(n)) }}

	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn: func() RTPStats {
			// 0% loss (good)
			return RTPStats{PacketsReceived: 100, PacketsLost: 0, LatencyAvgMs: 1.0}
		},
		JitterMsFn:     func() float64 { return 50.0 }, // above poorJitterMs (40ms)
		SampleInterval: 20 * time.Millisecond,
		Pipeline:       pipe,
	})
	m.Start()
	time.Sleep(80 * time.Millisecond)
	m.Stop()

	got := int(atomic.LoadInt32(&lastSet))
	if got != 2 {
		t.Errorf("Pipeline.SetAggressiveness: want 2 for high jitter, got %d", got)
	}
}

// TestRTPMonitor_PipelineRecovery verifies SetAggressiveness(1) when quality is good.
func TestRTPMonitor_PipelineRecovery(t *testing.T) {
	var lastSet int32 = -1
	pipe := &mockPipeline{onSet: func(n int) { atomic.StoreInt32(&lastSet, int32(n)) }}

	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn: func() RTPStats {
			return RTPStats{PacketsReceived: 100, PacketsLost: 0, LatencyAvgMs: 1.0}
		},
		JitterMsFn:     func() float64 { return 5.0 }, // good jitter
		SampleInterval: 20 * time.Millisecond,
		Pipeline:       pipe,
	})
	m.Start()
	time.Sleep(80 * time.Millisecond)
	m.Stop()

	got := int(atomic.LoadInt32(&lastSet))
	if got != 1 {
		t.Errorf("Pipeline.SetAggressiveness: want 1 for good quality (recovery), got %d", got)
	}
}

// TestRTPMonitor_CustomSNREstimate verifies SNREstimateFn path is used.
func TestRTPMonitor_CustomSNREstimate(t *testing.T) {
	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn: func() RTPStats {
			return RTPStats{PacketsReceived: 100, PacketsLost: 0, LatencyAvgMs: 1.0}
		},
		SNREstimateFn:  func() float64 { return 10.0 }, // low SNR → alert + recommendation
		SampleInterval: 20 * time.Millisecond,
	})
	m.Start()
	time.Sleep(80 * time.Millisecond)
	report, err := m.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// SNR 10 < poorSNRDB (15) → should fire alerts
	if report.AlertCount == 0 {
		t.Error("expected alert for low SNR estimate, got 0 alerts")
	}
}

// ─── recommend() branches ────────────────────────────────────────────────────

// TestRecommend_HighLossOver5Pct verifies the >5% loss recommendation text.
func TestRecommend_HighLossOver5Pct(t *testing.T) {
	m := &RTPMonitor{}
	r := RTPSessionReport{
		LossPct:      6.0, // > 5%
		AvgJitterMs:  5.0,
		AvgSNREstDB:  30.0,
		AvgLatencyMs: 1.0,
		AlertCount:   1,
	}
	recs := m.recommend(r)
	found := false
	for _, rec := range recs {
		if strings.Contains(rec, "PLC") || strings.Contains(rec, "JitterDepth") {
			found = true
		}
	}
	if !found {
		t.Errorf("recommend: expected PLC/JitterDepth mention for >5%% loss; got %v", recs)
	}
}

// TestRecommend_ModerateHighLoss verifies the 3-5% loss recommendation.
func TestRecommend_ModerateHighLoss(t *testing.T) {
	m := &RTPMonitor{}
	r := RTPSessionReport{
		LossPct:      4.0, // > poorLossPct(3%) but <= 5%
		AvgJitterMs:  5.0,
		AvgSNREstDB:  30.0,
		AvgLatencyMs: 1.0,
		AlertCount:   1,
	}
	recs := m.recommend(r)
	found := false
	for _, rec := range recs {
		if strings.Contains(rec, "JitterDepth") {
			found = true
		}
	}
	if !found {
		t.Errorf("recommend: expected JitterDepth mention for moderate loss; got %v", recs)
	}
}

// TestRecommend_HighJitter verifies the jitter recommendation branch.
func TestRecommend_HighJitter(t *testing.T) {
	m := &RTPMonitor{}
	r := RTPSessionReport{
		LossPct:      0.0,
		AvgJitterMs:  60.0, // > poorJitterMs (40ms)
		AvgSNREstDB:  30.0,
		AvgLatencyMs: 1.0,
		AlertCount:   1,
	}
	recs := m.recommend(r)
	found := false
	for _, rec := range recs {
		if strings.Contains(rec, "JitterDepth") {
			found = true
		}
	}
	if !found {
		t.Errorf("recommend: expected JitterDepth for high jitter; got %v", recs)
	}
}

// TestRecommend_LowSNR verifies the SNR recommendation.
func TestRecommend_LowSNR(t *testing.T) {
	m := &RTPMonitor{}
	r := RTPSessionReport{
		LossPct:      0.0,
		AvgJitterMs:  5.0,
		AvgSNREstDB:  10.0, // > 0 and < poorSNRDB (15)
		AvgLatencyMs: 1.0,
		AlertCount:   1,
	}
	recs := m.recommend(r)
	found := false
	for _, rec := range recs {
		if strings.Contains(rec, "SuppressorAggressiveness") || strings.Contains(rec, "aggressive") {
			found = true
		}
	}
	if !found {
		t.Errorf("recommend: expected suppressor recommendation for low SNR; got %v", recs)
	}
}

// TestRecommend_HighLatency verifies the latency recommendation.
func TestRecommend_HighLatency(t *testing.T) {
	m := &RTPMonitor{}
	r := RTPSessionReport{
		LossPct:      0.0,
		AvgJitterMs:  5.0,
		AvgSNREstDB:  30.0,
		AvgLatencyMs: 10.0, // > 8ms budget
		AlertCount:   1,
	}
	recs := m.recommend(r)
	found := false
	for _, rec := range recs {
		if strings.Contains(rec, "latency") || strings.Contains(rec, "real-time") {
			found = true
		}
	}
	if !found {
		t.Errorf("recommend: expected latency/real-time mention for high latency; got %v", recs)
	}
}

// TestRecommend_GoodQuality verifies the "no changes required" recommendation.
func TestRecommend_GoodQuality(t *testing.T) {
	m := &RTPMonitor{}
	r := RTPSessionReport{
		LossPct:      0.0,
		AvgJitterMs:  5.0,
		AvgSNREstDB:  30.0,
		AvgLatencyMs: 1.0,
		AlertCount:   0, // no alerts
	}
	recs := m.recommend(r)
	found := false
	for _, rec := range recs {
		if strings.Contains(rec, "good") || strings.Contains(rec, "no config changes") {
			found = true
		}
	}
	if !found {
		t.Errorf("recommend: expected 'good quality' recommendation; got %v", recs)
	}
}

// ─── ComputeSNR edge cases ────────────────────────────────────────────────────

// TestComputeSNR_Empty verifies that an empty slice returns 0.
func TestComputeSNR_Empty(t *testing.T) {
	snr := ComputeSNR([]int16{})
	if snr != 0 {
		t.Errorf("ComputeSNR(empty) = %.2f; want 0", snr)
	}
}

// TestComputeSNR_PerfectSignal verifies the noisePow < 1e-9 path returns 60.
// A DC signal (constant value) has zero within-window variance, so noisePow ≈ 0.
func TestComputeSNR_PerfectSignal(t *testing.T) {
	// Constant signal: all samples the same value → local RMS always equals global RMS
	// → diff = 0 → noisePow = 0 → returns 60.
	samples := make([]int16, 160)
	for i := range samples {
		samples[i] = 1000
	}
	snr := ComputeSNR(samples)
	if snr != 60 {
		t.Errorf("ComputeSNR(constant signal) = %.2f; want 60", snr)
	}
}

// TestComputeSNR_HighlyNoisy verifies noisy signals produce valid (non-NaN) SNR.
func TestComputeSNR_HighlyNoisy(t *testing.T) {
	// Random-like alternating pattern with very high noise variance.
	samples := make([]int16, 160)
	for i := range samples {
		if i%2 == 0 {
			samples[i] = 30000
		} else {
			samples[i] = -30000
		}
	}
	snr := ComputeSNR(samples)
	// Highly non-stationary → should be a valid float (not NaN)
	if math.IsNaN(snr) {
		t.Error("ComputeSNR returned NaN for noisy signal")
	}
}

// ─── RMSLevel edge cases ──────────────────────────────────────────────────────

// TestRMSLevel_EmptySlice verifies that an empty slice returns 0.
func TestRMSLevel_EmptySlice(t *testing.T) {
	rms := RMSLevel([]int16{})
	if rms != 0 {
		t.Errorf("RMSLevel(empty) = %.2f; want 0", rms)
	}
}

// TestRMSLevel_SingleSample verifies single-element slice.
func TestRMSLevel_SingleSample(t *testing.T) {
	rms := RMSLevel([]int16{1000})
	if math.Abs(rms-1000.0) > 0.01 {
		t.Errorf("RMSLevel([1000]) = %.2f; want 1000.0", rms)
	}
}

// TestRMSLevel_NegativeSample verifies that negative samples are squared correctly.
func TestRMSLevel_NegativeSample(t *testing.T) {
	rms := RMSLevel([]int16{-1000})
	if math.Abs(rms-1000.0) > 0.01 {
		t.Errorf("RMSLevel([-1000]) = %.2f; want 1000.0", rms)
	}
}

// ─── LatencyAccumulator.Stats edge cases ─────────────────────────────────────

// TestLatencyStats_SingleSample verifies Stats() on a single measurement.
func TestLatencyStats_SingleSample(t *testing.T) {
	var acc LatencyAccumulator
	acc.Add(5.0)
	s := acc.Stats()
	if s.Samples != 1 {
		t.Errorf("Samples: want 1, got %d", s.Samples)
	}
	if s.MinMs != 5.0 || s.MaxMs != 5.0 || s.MeanMs != 5.0 {
		t.Errorf("Stats: want all 5.0, got min=%.1f max=%.1f mean=%.1f", s.MinMs, s.MaxMs, s.MeanMs)
	}
	if math.Abs(s.RealTimeFactor-0.5) > 0.001 {
		t.Errorf("RealTimeFactor: want 0.5 for 5ms, got %.3f", s.RealTimeFactor)
	}
}

// TestLatencyStats_TwoSamples verifies P95 and min/max with two samples.
func TestLatencyStats_TwoSamples(t *testing.T) {
	var acc LatencyAccumulator
	acc.Add(1.0)
	acc.Add(9.0)
	s := acc.Stats()
	if s.MinMs != 1.0 {
		t.Errorf("MinMs: want 1.0, got %.1f", s.MinMs)
	}
	if s.MaxMs != 9.0 {
		t.Errorf("MaxMs: want 9.0, got %.1f", s.MaxMs)
	}
}

// ─── ComputeVADStats edge cases ───────────────────────────────────────────────

// TestComputeVADStats_Zero verifies zero framesProcessed returns empty struct.
func TestComputeVADStats_Zero(t *testing.T) {
	s := ComputeVADStats(0, 0)
	if s.TotalFrames != 0 || s.SpeechFrames != 0 || s.SilenceFrames != 0 {
		t.Errorf("expected zero VADStats for zero input, got %+v", s)
	}
	if s.SpeechRatio != 0 || s.CPUSavedPct != 0 {
		t.Errorf("expected zero ratios for zero input, got speech=%.2f cpu=%.2f", s.SpeechRatio, s.CPUSavedPct)
	}
}

// TestComputeVADStats_AllSilence verifies 100% silence calculation.
func TestComputeVADStats_AllSilence(t *testing.T) {
	s := ComputeVADStats(100, 100)
	if s.SpeechRatio != 0 {
		t.Errorf("SpeechRatio: want 0 for all silence, got %.2f", s.SpeechRatio)
	}
	if math.Abs(s.CPUSavedPct-30.0) > 0.01 {
		t.Errorf("CPUSavedPct: want 30.0 for all silence, got %.2f", s.CPUSavedPct)
	}
	if s.SpeechFrames != 0 {
		t.Errorf("SpeechFrames: want 0, got %d", s.SpeechFrames)
	}
	if s.SilenceFrames != 100 {
		t.Errorf("SilenceFrames: want 100, got %d", s.SilenceFrames)
	}
}

// TestComputeVADStats_QuarterSpeech tests 25% speech / 75% silence.
func TestComputeVADStats_QuarterSpeech(t *testing.T) {
	s := ComputeVADStats(200, 150) // 150 silent out of 200
	wantRatio := 0.25
	if math.Abs(s.SpeechRatio-wantRatio) > 0.001 {
		t.Errorf("SpeechRatio: want %.2f, got %.2f", wantRatio, s.SpeechRatio)
	}
	wantCPU := 0.75 * 30.0
	if math.Abs(s.CPUSavedPct-wantCPU) > 0.01 {
		t.Errorf("CPUSavedPct: want %.2f, got %.2f", wantCPU, s.CPUSavedPct)
	}
}

// ─── WriteAllReports edge cases ───────────────────────────────────────────────

// TestWriteAllReports_EmptyFiles verifies WriteAllReports works with empty file list.
func TestWriteAllReports_EmptyFiles(t *testing.T) {
	dir := t.TempDir()
	summary := BatchSummary{
		TotalFiles:     0,
		ProcessedFiles: 0,
		Files:          []FileResult{},
	}
	csvPath, summPath, filesPath, cfgPath, err := WriteAllReports(dir, summary)
	if err != nil {
		t.Fatalf("WriteAllReports with empty files: %v", err)
	}
	for _, p := range []string{csvPath, summPath, filesPath, cfgPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected output file %s to exist: %v", p, err)
		}
	}
}

// TestWriteAllReports_InvalidDir verifies an error is returned for an unwritable dir.
func TestWriteAllReports_InvalidDir(t *testing.T) {
	_, _, _, _, err := WriteAllReports("/dev/null/nonexistent", BatchSummary{})
	if err == nil {
		t.Error("expected error when writing to invalid dir, got nil")
	}
}

// TestWriteSummaryJSON_EmptySummary verifies JSON creation with zero-value summary.
func TestWriteSummaryJSON_EmptySummary(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteSummaryJSON(dir, BatchSummary{})
	if err != nil {
		t.Fatalf("WriteSummaryJSON with empty summary: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected output file %s, got: %v", path, err)
	}
}

// ─── TranscriptScorer.Score — rate-limit and empty-input branches ─────────────

// TestScore_ContextCancelDuringRateLimit verifies ctx.Done() is respected
// when waiting for the rate-limit delay.
func TestScore_ContextCancelDuringRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"85"}}]}`))
	}))
	defer srv.Close()

	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint:    srv.URL,
		LLMAPIKey:      "test-key",
		RateLimitDelay: 2 * time.Second, // long enough that cancel fires first
	})

	// Prime last call time so the rate limit will kick in on the next call.
	scorer.last = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := scorer.Score(ctx, "hello world", "hello world")
	if err == nil {
		t.Error("expected error (context cancellation during rate limit wait), got nil")
	}
}

// TestScore_EmptyComparison verifies that empty comparison skips LLM and returns -1.
func TestScore_EmptyComparison(t *testing.T) {
	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint: "http://localhost:99999", // unreachable — but LLM should be skipped
	})
	ctx := context.Background()

	scores, err := scorer.Score(ctx, "hello", "")
	if err != nil {
		t.Fatalf("Score: unexpected error: %v", err)
	}
	if scores.LLMScore != -1 {
		t.Errorf("LLMScore: want -1 for empty comparison, got %.1f", scores.LLMScore)
	}
}

// TestScore_EmptyReference verifies that empty reference skips LLM.
func TestScore_EmptyReference(t *testing.T) {
	scorer := NewTranscriptScorer(TranscriptScorerConfig{
		LLMEndpoint: "http://localhost:99999",
	})
	ctx := context.Background()

	scores, err := scorer.Score(ctx, "", "hello")
	if err != nil {
		t.Fatalf("Score: unexpected error: %v", err)
	}
	if scores.LLMScore != -1 {
		t.Errorf("LLMScore: want -1 for empty reference, got %.1f", scores.LLMScore)
	}
}

// ─── estimateSNRFromLoss ──────────────────────────────────────────────────────

// TestEstimateSNRFromLoss_ZeroLoss verifies 0% loss returns 30dB.
func TestEstimateSNRFromLoss_ZeroLoss(t *testing.T) {
	got := estimateSNRFromLoss(0)
	if got != 30.0 {
		t.Errorf("estimateSNRFromLoss(0) = %.1f; want 30.0", got)
	}
}

// TestEstimateSNRFromLoss_HighLoss verifies that high loss produces clamped 0 SNR.
func TestEstimateSNRFromLoss_HighLoss(t *testing.T) {
	// 10% loss: 30 - 10*4 = -10 → clamped to 0
	got := estimateSNRFromLoss(10.0)
	if got != 0 {
		t.Errorf("estimateSNRFromLoss(10.0) = %.1f; want 0 (clamped)", got)
	}
}

// TestEstimateSNRFromLoss_ModestLoss verifies midrange computation.
func TestEstimateSNRFromLoss_ModestLoss(t *testing.T) {
	// 5% loss: 30 - 5*4 = 10
	got := estimateSNRFromLoss(5.0)
	if math.Abs(got-10.0) > 0.001 {
		t.Errorf("estimateSNRFromLoss(5.0) = %.1f; want 10.0", got)
	}
}

// ─── RTPMonitor.Stop without Start ───────────────────────────────────────────

// TestRTPMonitor_StopWithoutStart verifies Stop is safe without Start.
func TestRTPMonitor_StopWithoutStart(t *testing.T) {
	m := NewRTPMonitor(RTPMonitorConfig{
		StatsFn: func() RTPStats { return RTPStats{PacketsReceived: 10, PacketsLost: 0} },
	})
	report, err := m.Stop()
	if err != nil {
		t.Fatalf("Stop without Start: %v", err)
	}
	// No snapshots since sampleLoop was never started.
	if len(report.Snapshots) != 0 {
		t.Errorf("expected 0 snapshots when stopped without start, got %d", len(report.Snapshots))
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// mockPipeline is a minimal Pipeline implementation for testing SetAggressiveness.
type mockPipeline struct {
	onSet func(n int)
}

func (p *mockPipeline) SetAggressiveness(n int) {
	if p.onSet != nil {
		p.onSet(n)
	}
}
