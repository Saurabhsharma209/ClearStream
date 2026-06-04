package billing

import (
	"math"
	"os"
	"sync"
	"testing"
	"time"
)

// approxEqual reports whether a and b differ by less than eps.
func approxEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

// TestFeatureBitmask verifies bitmask composition and Has() correctness.
func TestFeatureBitmask(t *testing.T) {
	f := FeatureVAD | FeatureSpectralNR | FeatureAGC

	if !f.Has(FeatureVAD) {
		t.Error("expected VAD to be set")
	}
	if !f.Has(FeatureSpectralNR) {
		t.Error("expected SpectralNR to be set")
	}
	if !f.Has(FeatureAGC) {
		t.Error("expected AGC to be set")
	}
	if f.Has(FeatureRNNoise) {
		t.Error("expected RNNoise to be unset")
	}
	if f.Has(FeatureDeepFilter) {
		t.Error("expected DeepFilter to be unset")
	}
	if f.Has(FeatureRTPMonitor) {
		t.Error("expected RTPMonitor to be unset")
	}
	if f.Has(FeatureEval) {
		t.Error("expected Eval to be unset")
	}

	s := f.String()
	if s == "" || s == "none" {
		t.Errorf("unexpected String() = %q", s)
	}
	t.Logf("Feature string: %s", s)
}

// TestCDRBilledUnits: 7000ms / 6000ms pulse → 2 units (ceil(1.166…) = 2).
func TestCDRBilledUnits(t *testing.T) {
	start := time.Now()
	end := start.Add(7000 * time.Millisecond)
	cdr := NewCDR("sid", "acct", "us-east-1", "node1", start, end, FeatureVAD, 6000, 0, 0, 0, 0)

	if cdr.BilledUnits != 2 {
		t.Errorf("expected BilledUnits=2, got %d (DurationMs=%d)", cdr.BilledUnits, cdr.DurationMs)
	}
}

// TestCDRBilledUnits_MinimumPulse: 2000ms / 6000ms pulse → 1 unit (ceil(0.333…) rounds to 1, clamped to min 1).
func TestCDRBilledUnits_MinimumPulse(t *testing.T) {
	start := time.Now()
	end := start.Add(2000 * time.Millisecond)
	cdr := NewCDR("sid", "acct", "us-east-1", "node1", start, end, FeatureVAD, 6000, 0, 0, 0, 0)

	if cdr.BilledUnits != 1 {
		t.Errorf("expected BilledUnits=1, got %d (DurationMs=%d)", cdr.BilledUnits, cdr.DurationMs)
	}
}

// TestSessionMeter_Finalize creates a meter, enables features, sleeps ~10ms,
// finalizes, and verifies CDR fields.
func TestSessionMeter_Finalize(t *testing.T) {
	cfg := MeterConfig{
		SessionID: "test-session-uuid",
		AccountID: "acct-123",
		Region:    "ap-south-1",
		NodeID:    "testnode",
		PulseMs:   6000,
	}
	meter := NewSessionMeter(cfg)
	meter.EnableFeature(FeatureVAD)
	meter.EnableFeature(FeatureSpectralNR)

	// Sleep a tiny bit so DurationMs > 0.
	time.Sleep(10 * time.Millisecond)

	cdr := meter.Finalize(12.5, 0.3, 18.0, 0)

	if cdr.SessionID != "test-session-uuid" {
		t.Errorf("unexpected SessionID: %s", cdr.SessionID)
	}
	if cdr.AccountID != "acct-123" {
		t.Errorf("unexpected AccountID: %s", cdr.AccountID)
	}
	if cdr.Region != "ap-south-1" {
		t.Errorf("unexpected Region: %s", cdr.Region)
	}
	if !cdr.Features.Has(FeatureVAD) {
		t.Error("expected VAD feature set")
	}
	if !cdr.Features.Has(FeatureSpectralNR) {
		t.Error("expected SpectralNR feature set")
	}
	if cdr.Features.Has(FeatureRNNoise) {
		t.Error("expected RNNoise unset")
	}
	if cdr.DurationMs <= 0 {
		t.Errorf("expected positive DurationMs, got %d", cdr.DurationMs)
	}
	if cdr.BilledUnits < 1 {
		t.Errorf("expected BilledUnits >= 1, got %d", cdr.BilledUnits)
	}
	if cdr.AvgLatencyMs != 12.5 {
		t.Errorf("expected AvgLatencyMs=12.5, got %f", cdr.AvgLatencyMs)
	}
	if cdr.ErrorCode != 0 {
		t.Errorf("expected ErrorCode=0, got %d", cdr.ErrorCode)
	}
	t.Logf("CDR: duration=%dms billedUnits=%d features=%s", cdr.DurationMs, cdr.BilledUnits, cdr.Features)
}

// TestWALWriter_WriteAndRecover writes 3 CDRs, closes the WAL,
// then opens a new WALWriter and calls RecoverAndFlush to verify all 3 are recovered.
func TestWALWriter_WriteAndRecover(t *testing.T) {
	dir, err := os.MkdirTemp("", "billing-wal-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Writer 1: write 3 CDRs and close (no rotation, file stays on disk).
	var mu sync.Mutex
	var flushed []CDR

	w1, err := NewWALWriter(dir, func(cdrs []CDR) error {
		mu.Lock()
		flushed = append(flushed, cdrs...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	for i := 0; i < 3; i++ {
		end := start.Add(time.Duration(i+1) * 7 * time.Second)
		cdr := NewCDR(
			"session-"+string(rune('A'+i)),
			"acct", "us-east-1", "node1",
			start, end,
			FeatureVAD, 6000, 0, 0, 0, 0,
		)
		if err := w1.Write(cdr); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	// Close without rotating — file stays on disk for recovery.
	if err := w1.Close(); err != nil {
		t.Fatal(err)
	}

	// Writer 2: recovery pass — should find the .wal file and call OnFlush.
	w2, err := NewWALWriter(dir, func(cdrs []CDR) error {
		mu.Lock()
		flushed = append(flushed, cdrs...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	if err := w2.RecoverAndFlush(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	count := len(flushed)
	mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 recovered CDRs, got %d", count)
	}
}

// TestStaticRateCard_Pricing verifies that base + feature prices sum correctly.
func TestStaticRateCard_Pricing(t *testing.T) {
	const eps = 1e-15

	rc := &StaticRateCard{
		BasePrice: 0.000001,
		FeaturePrices: map[Feature]float64{
			FeatureSpectralNR: 0.0000005,
			FeatureRNNoise:    0.000001,
		},
	}

	// No premium features — only base price.
	priceBase := rc.UnitPrice(FeatureVAD)
	if !approxEqual(priceBase, 0.000001, eps) {
		t.Errorf("base price mismatch: got %v", priceBase)
	}

	// SpectralNR only.
	priceNR := rc.UnitPrice(FeatureVAD | FeatureSpectralNR)
	expectedNR := 0.000001 + 0.0000005
	if !approxEqual(priceNR, expectedNR, eps) {
		t.Errorf("SpectralNR price mismatch: got %v want %v", priceNR, expectedNR)
	}

	// Both SpectralNR and RNNoise.
	priceBoth := rc.UnitPrice(FeatureVAD | FeatureSpectralNR | FeatureRNNoise)
	expectedBoth := 0.000001 + 0.0000005 + 0.000001
	if !approxEqual(priceBoth, expectedBoth, eps) {
		t.Errorf("combined price mismatch: got %.20f want %.20f", priceBoth, expectedBoth)
	}

	// Default rate card smoke test.
	drc := DefaultTelephonyRateCard()
	fullFeatures := FeatureVAD | FeatureSpectralNR | FeatureRNNoise | FeatureDeepFilter | FeatureAGC | FeatureRTPMonitor | FeatureEval
	fullPrice := drc.UnitPrice(fullFeatures)
	if fullPrice <= 0 {
		t.Errorf("default rate card returned non-positive price: %v", fullPrice)
	}
	t.Logf("Default rate card full-feature price: $%.10f/unit", fullPrice)
}
