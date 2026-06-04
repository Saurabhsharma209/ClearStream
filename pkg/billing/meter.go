package billing

import (
	"os"
	"sync/atomic"
	"time"
)

// MeterConfig configures a SessionMeter.
type MeterConfig struct {
	SessionID string
	AccountID string
	Region    string
	NodeID    string
	PulseMs   int32 // default 6000
	WAL       *WALWriter
}

// SessionMeter tracks billing state for a single call.
// Create at session start; call Finalize() at hangup.
// Thread-safe: EnableFeature may be called from multiple goroutines.
type SessionMeter struct {
	cfg      MeterConfig
	startMs  int64  // unix ms at creation
	features uint32 // bitmask, updated atomically via EnableFeature()
}

// NewSessionMeter creates a SessionMeter and records the start time.
// If cfg.NodeID is empty, the machine hostname is used.
// If cfg.PulseMs is <= 0, it defaults to 6000.
func NewSessionMeter(cfg MeterConfig) *SessionMeter {
	if cfg.PulseMs <= 0 {
		cfg.PulseMs = 6000
	}
	if cfg.NodeID == "" {
		if h, err := os.Hostname(); err == nil {
			cfg.NodeID = h
		}
	}
	return &SessionMeter{
		cfg:     cfg,
		startMs: time.Now().UnixMilli(),
	}
}

// EnableFeature atomically sets the given feature bit.
// Safe to call from multiple goroutines (e.g., pipeline stages).
func (m *SessionMeter) EnableFeature(f Feature) {
	for {
		old := atomic.LoadUint32(&m.features)
		neu := old | uint32(f)
		if atomic.CompareAndSwapUint32(&m.features, old, neu) {
			return
		}
	}
}

// Finalize builds a CDR for the session, writes it to the WAL asynchronously
// (if a WALWriter is configured), and returns the CDR for the caller to inspect.
// Must be called exactly once at hangup.
func (m *SessionMeter) Finalize(avgLatencyMs float32, packetLossPct float32, snrEstDB float32, errCode int8) CDR {
	endTime := time.Now()
	startTime := time.UnixMilli(m.startMs)
	features := Feature(atomic.LoadUint32(&m.features))

	cdr := NewCDR(
		m.cfg.SessionID,
		m.cfg.AccountID,
		m.cfg.Region,
		m.cfg.NodeID,
		startTime,
		endTime,
		features,
		m.cfg.PulseMs,
		avgLatencyMs,
		packetLossPct,
		snrEstDB,
		errCode,
	)

	if m.cfg.WAL != nil {
		wal := m.cfg.WAL
		go func() {
			_ = wal.Write(cdr)
		}()
	}

	return cdr
}
