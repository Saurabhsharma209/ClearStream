package eval

import (
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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

// mockPipeline is a minimal Pipeline implementation for testing SetAggressiveness.
type mockPipeline struct {
	onSet func(n int)
}

func (p *mockPipeline) SetAggressiveness(n int) {
	if p.onSet != nil {
		p.onSet(n)
	}
}

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

type mockPipelineCB2 struct {
	onSet func(n int)
}

func (p *mockPipelineCB2) SetAggressiveness(n int) {
	if p.onSet != nil {
		p.onSet(n)
	}
}

func TestEstimateSNRFromLoss_Zero(t *testing.T) {
	if got := estimateSNRFromLoss(0); got != 30.0 {
		t.Errorf("lossPct=0: want 30.0, got %.2f", got)
	}
}

func TestEstimateSNRFromLoss_Negative(t *testing.T) {
	if got := estimateSNRFromLoss(-1); got != 30.0 {
		t.Errorf("lossPct=-1: want 30.0, got %.2f", got)
	}
}

func TestEstimateSNRFromLoss_High(t *testing.T) {
	if got := estimateSNRFromLoss(10); got != 0 {
		t.Errorf("lossPct=10: want 0 (clamped), got %.2f", got)
	}
}

func TestEstimateSNRFromLoss_Mid(t *testing.T) {
	if got := estimateSNRFromLoss(5); got != 10.0 {
		t.Errorf("lossPct=5: want 10.0, got %.2f", got)
	}
}
