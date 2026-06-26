package billing

import (
	"os"
	"sync"
	"testing"
	"time"
)

// TestCDRBilledSeconds verifies BilledSeconds() = BilledUnits * PulseMs / 1000.
func TestCDRBilledSeconds(t *testing.T) {
	start := time.Now()
	// 12000ms duration, 6000ms pulse → BilledUnits=2, BilledSeconds=12.0
	end := start.Add(12000 * time.Millisecond)
	cdr := NewCDR("sid1", "acct", "us-east-1", "node1", start, end, FeatureVAD, 6000, 0, 0, 0, 0)

	expected := float64(cdr.BilledUnits) * float64(cdr.PulseMs) / 1000.0
	got := cdr.BilledSeconds()
	if got != expected {
		t.Errorf("BilledSeconds() = %f, want %f (BilledUnits=%d, PulseMs=%d)", got, expected, cdr.BilledUnits, cdr.PulseMs)
	}
	if cdr.BilledUnits != 2 {
		t.Errorf("expected BilledUnits=2, got %d", cdr.BilledUnits)
	}
	if got != 12.0 {
		t.Errorf("expected BilledSeconds()=12.0, got %f", got)
	}
}

// TestCDRCost verifies Cost(unitPrice) = BilledUnits * unitPrice.
func TestCDRCost(t *testing.T) {
	start := time.Now()
	end := start.Add(12000 * time.Millisecond)
	cdr := NewCDR("sid2", "acct", "us-east-1", "node1", start, end, FeatureVAD, 6000, 0, 0, 0, 0)

	unitPrice := 0.01
	want := float64(cdr.BilledUnits) * unitPrice
	got := cdr.Cost(unitPrice)
	if got != want {
		t.Errorf("Cost(%f) = %f, want %f", unitPrice, got, want)
	}
	if got != 0.02 {
		t.Errorf("expected Cost()=0.02, got %f", got)
	}
}

// TestCDRNegativeDuration verifies that when endTime is before startTime,
// DurationMs is clamped to 0 and BilledUnits is clamped to minimum 1.
func TestCDRNegativeDuration(t *testing.T) {
	now := time.Now()
	start := now
	end := now.Add(-5 * time.Second) // end before start

	cdr := NewCDR("sid3", "acct", "us-east-1", "node1", start, end, FeatureVAD, 6000, 0, 0, 0, 0)

	if cdr.DurationMs != 0 {
		t.Errorf("expected DurationMs=0 for negative duration, got %d", cdr.DurationMs)
	}
	if cdr.BilledUnits != 1 {
		t.Errorf("expected BilledUnits=1 (minimum) for zero duration, got %d", cdr.BilledUnits)
	}
}

// TestNewSessionMeterHostname verifies that when cfg.NodeID is empty,
// NewSessionMeter fills NodeID with the OS hostname (non-empty).
func TestNewSessionMeterHostname(t *testing.T) {
	cfg := MeterConfig{
		SessionID: "sess-hostname",
		AccountID: "acct",
		Region:    "us-west-2",
		NodeID:    "", // empty — should trigger os.Hostname() path
		PulseMs:   6000,
	}
	meter := NewSessionMeter(cfg)

	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		if meter.cfg.NodeID != hostname {
			t.Errorf("expected NodeID=%q, got %q", hostname, meter.cfg.NodeID)
		}
	} else {
		t.Skip("os.Hostname() unavailable in this environment")
	}
	if meter.cfg.NodeID == "" {
		t.Error("NodeID should be non-empty after NewSessionMeter with empty cfg.NodeID")
	}
}

// TestSessionMeterFinalizeWithWAL creates a SessionMeter with a WALWriter,
// calls Finalize, sleeps to let the async goroutine complete, then verifies
// the CDR was written to the WAL file by using RecoverAndFlush.
func TestSessionMeterFinalizeWithWAL(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var recovered []CDR

	wal, err := NewWALWriter(dir, func(cdrs []CDR) error {
		mu.Lock()
		recovered = append(recovered, cdrs...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("NewWALWriter: %v", err)
	}

	cfg := MeterConfig{
		SessionID: "sess-wal-finalize",
		AccountID: "acct-finalize",
		Region:    "ap-south-1",
		NodeID:    "testnode",
		PulseMs:   6000,
		WAL:       wal,
	}
	meter := NewSessionMeter(cfg)
	meter.EnableFeature(FeatureVAD)

	time.Sleep(10 * time.Millisecond)
	cdr := meter.Finalize(5.0, 0.1, 15.0, 0)

	// Wait for async WAL goroutine to finish writing.
	time.Sleep(50 * time.Millisecond)

	// Close the WAL (leaves the file on disk without rotating).
	if err := wal.Close(); err != nil {
		t.Fatalf("wal.Close: %v", err)
	}

	// Open a fresh WALWriter and recover the CDR written above.
	wal2, err := NewWALWriter(dir, func(cdrs []CDR) error {
		mu.Lock()
		recovered = append(recovered, cdrs...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("NewWALWriter (recovery): %v", err)
	}
	defer wal2.Close()

	if err := wal2.RecoverAndFlush(); err != nil {
		t.Fatalf("RecoverAndFlush: %v", err)
	}

	mu.Lock()
	count := len(recovered)
	var found bool
	for _, r := range recovered {
		if r.SessionID == cdr.SessionID {
			found = true
			if r.AccountID != cdr.AccountID {
				t.Errorf("AccountID mismatch: got %s, want %s", r.AccountID, cdr.AccountID)
			}
		}
	}
	mu.Unlock()

	if count < 1 {
		t.Errorf("expected at least 1 recovered CDR, got %d", count)
	}
	if !found {
		t.Errorf("CDR with SessionID=%q not found in recovered records", cdr.SessionID)
	}
}

// TestWALRotate verifies that setting RotateInterval to a very small duration
// causes Write to trigger rotation, resulting in OnFlush being called and a new
// WAL file being opened.
func TestWALRotate(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var flushed []CDR

	w, err := NewWALWriter(dir, func(cdrs []CDR) error {
		mu.Lock()
		flushed = append(flushed, cdrs...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("NewWALWriter: %v", err)
	}
	defer w.Close()

	start := time.Now()
	end := start.Add(7 * time.Second)
	cdr1 := NewCDR("rotate-session-1", "acct", "us-east-1", "node1", start, end, FeatureVAD, 6000, 0, 0, 0, 0)

	// Write the first CDR normally.
	if err := w.Write(cdr1); err != nil {
		t.Fatalf("Write cdr1: %v", err)
	}

	// Force rotation on next Write by setting interval to 1 nanosecond.
	w.mu.Lock()
	w.RotateInterval = 1
	w.mu.Unlock()

	// Sleep 1ms to ensure time.Since(w.created) >= 1ns.
	time.Sleep(1 * time.Millisecond)

	cdr2 := NewCDR("rotate-session-2", "acct", "us-east-1", "node1", start, end, FeatureVAD, 6000, 0, 0, 0, 0)

	// This Write should trigger rotation.
	if err := w.Write(cdr2); err != nil {
		t.Fatalf("Write cdr2 (should trigger rotation): %v", err)
	}

	// OnFlush should have been called with the first CDR's batch.
	mu.Lock()
	count := len(flushed)
	mu.Unlock()

	if count < 1 {
		t.Errorf("expected at least 1 CDR flushed via OnFlush after rotation, got %d", count)
	}

	// Verify there is still a current WAL file open for new writes.
	w.mu.Lock()
	hasFile := w.f != nil
	w.mu.Unlock()

	if !hasFile {
		t.Error("expected a new WAL file to be open after rotation")
	}
}
