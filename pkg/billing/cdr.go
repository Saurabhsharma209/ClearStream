package billing

import (
	"math"
	"time"
)

// CDR (Call Detail Record) — one per session, emitted on call end.
// Wire size: ~180 bytes (protobuf). At 1B CDRs/day = ~167 GB/day compressed.
type CDR struct {
	SessionID     string // UUID v4 — Kafka dedup key
	AccountID     string
	Region        string // us-east-1, ap-south-1, etc.
	NodeID        string // hostname of processing pod
	StartTime     time.Time
	EndTime       time.Time
	DurationMs    int64
	Features      Feature
	PulseMs       int32 // billing granularity (default 6000ms = 6s minimum)
	BilledUnits   int32 // ceil(DurationMs / PulseMs)
	AvgLatencyMs  float32
	PacketLossPct float32
	SNREstDB      float32
	ErrorCode     int8 // 0=clean 1=cancelled 2=pipeline_error
}

// NewCDR constructs a CDR and computes BilledUnits.
func NewCDR(
	sessionID, accountID, region, nodeID string,
	startTime, endTime time.Time,
	features Feature,
	pulseMs int32,
	avgLatencyMs, packetLossPct, snrEstDB float32,
	errCode int8,
) CDR {
	durationMs := endTime.Sub(startTime).Milliseconds()
	if durationMs < 0 {
		durationMs = 0
	}
	pulse := pulseMs
	if pulse <= 0 {
		pulse = 6000
	}
	billedUnits := int32(math.Ceil(float64(durationMs) / float64(pulse)))
	if billedUnits < 1 {
		billedUnits = 1
	}
	return CDR{
		SessionID:     sessionID,
		AccountID:     accountID,
		Region:        region,
		NodeID:        nodeID,
		StartTime:     startTime,
		EndTime:       endTime,
		DurationMs:    durationMs,
		Features:      features,
		PulseMs:       pulse,
		BilledUnits:   billedUnits,
		AvgLatencyMs:  avgLatencyMs,
		PacketLossPct: packetLossPct,
		SNREstDB:      snrEstDB,
		ErrorCode:     errCode,
	}
}

// BilledSeconds returns the billable duration in seconds.
func (c *CDR) BilledSeconds() float64 {
	return float64(c.BilledUnits) * float64(c.PulseMs) / 1000.0
}

// Cost returns billable units * unitPriceUSD.
func (c *CDR) Cost(unitPriceUSD float64) float64 {
	return float64(c.BilledUnits) * unitPriceUSD
}
