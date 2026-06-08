package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ─── RTP quality thresholds ───────────────────────────────────────────────────

const (
	// poorLossPct: packet loss above this fraction is flagged as poor.
	poorLossPct = 0.03 // 3 %

	// poorJitterMs: jitter above this is flagged as poor.
	poorJitterMs = 40.0

	// poorSNRDB: estimated SNR below this triggers aggressiveness suggestion.
	poorSNRDB = 15.0
)

// ─── RTP session interface ────────────────────────────────────────────────────

// RTPStats is the subset of pkg/rtp.Stats that RTPMonitor needs.
// Keeps eval independent of the rtp package to avoid import cycles.
// Wire it up with an adapter:
//
//	monitor := eval.NewRTPMonitor(eval.RTPMonitorConfig{
//	    StatsFn: func() eval.RTPStats {
//	        s := rtpSession.Stats()
//	        return eval.RTPStats{
//	            PacketsReceived: s.PacketsReceived,
//	            PacketsLost:     s.PacketsLost,
//	            LatencyAvgMs:    s.LatencyAvgMs,
//	        }
//	    },
//	    JitterMsFn: rtpSession.Jitter.JitterMs,
//	})
type RTPStats struct {
	PacketsReceived uint64
	PacketsLost     uint64
	LatencyAvgMs    float64
}

// ─── Monitor ─────────────────────────────────────────────────────────────────

// RTPMonitorConfig configures a real-time RTP quality monitor.
type RTPMonitorConfig struct {
	// StatsFn is called each sample interval to get a fresh RTPStats snapshot.
	// Wire this to a closure that calls rtpSession.Stats() and converts the fields.
	// Required.
	StatsFn func() RTPStats

	// OutputDir is where the post-session JSON + YAML reports are written.
	OutputDir string

	// SampleInterval is how often quality snapshots are taken.
	// Default: 1 second.
	SampleInterval time.Duration

	// JitterMsFn returns the live jitter buffer depth in milliseconds.
	// Wire to rtpSession.jitter.JitterMs() or your own source.
	// Optional; if nil jitter is reported as 0.
	JitterMsFn func() float64

	// SNREstimateFn is an optional callback that returns the current estimated SNR (dB).
	// If nil, SNR is estimated from the PacketsLost/Received ratio.
	SNREstimateFn func() float64

	// OnAlert is called when a quality threshold is breached.
	// msg is a human-readable description of the problem.
	OnAlert func(msg string)

	// Pipeline is an optional live pipeline reference. When set, RTPMonitor
	// automatically calls SetAggressiveness on quality degradation events:
	//   loss > 3%  → SetAggressiveness(3)
	//   jitter > 40ms → SetAggressiveness(2)
	//   recovered  → SetAggressiveness(1)
	Pipeline interface {
		SetAggressiveness(n int)
	}
}

// QualitySnapshot is a point-in-time quality reading during a session.
type QualitySnapshot struct {
	At          time.Time `json:"at"`
	LossPct     float64   `json:"loss_pct"`
	JitterMs    float64   `json:"jitter_ms"`
	LatencyMs   float64   `json:"latency_ms"`
	SNREstDB    float64   `json:"snr_est_db"`
	AlertFired  bool      `json:"alert_fired"`
	AlertReason string    `json:"alert_reason,omitempty"`
}

// RTPSessionReport holds all metrics collected during one RTP session.
type RTPSessionReport struct {
	StartedAt       time.Time         `json:"started_at"`
	EndedAt         time.Time         `json:"ended_at"`
	DurationMs      float64           `json:"duration_ms"`
	PacketsReceived uint64            `json:"packets_received"`
	PacketsLost     uint64            `json:"packets_lost"`
	LossPct         float64           `json:"loss_pct"`
	AvgLatencyMs    float64           `json:"avg_latency_ms"`
	AvgJitterMs     float64           `json:"avg_jitter_ms"`
	AvgSNREstDB     float64           `json:"avg_snr_est_db"`
	AlertCount      int               `json:"alert_count"`
	Snapshots       []QualitySnapshot `json:"snapshots"`
	Recommendations []string          `json:"recommendations"`
	TunedConfig     *TunedConfig      `json:"tuned_config,omitempty"`
}

// RTPMonitor watches a live RTP session, collects per-second quality snapshots,
// fires alerts on threshold breaches, and writes a post-session report + tuned
// config YAML on Stop().
type RTPMonitor struct {
	cfg       RTPMonitorConfig
	startedAt time.Time
	stopCh    chan struct{}
	once      sync.Once

	mu        sync.Mutex
	snapshots []QualitySnapshot
	alerts    int64 // accessed via sync/atomic
}

// NewRTPMonitor creates an RTPMonitor.  Call Start() to begin sampling.
// Panics if StatsFn is nil.
func NewRTPMonitor(cfg RTPMonitorConfig) *RTPMonitor {
	if cfg.StatsFn == nil {
		panic("eval: RTPMonitorConfig.StatsFn must not be nil")
	}
	if cfg.SampleInterval <= 0 {
		cfg.SampleInterval = time.Second
	}
	return &RTPMonitor{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// Start begins background quality sampling. Non-blocking.
func (m *RTPMonitor) Start() {
	m.startedAt = time.Now()
	go m.sampleLoop()
}

// Stop halts sampling, generates the session report, writes output files, and
// returns the RTPSessionReport.
func (m *RTPMonitor) Stop() (RTPSessionReport, error) {
	m.once.Do(func() { close(m.stopCh) })

	m.mu.Lock()
	snaps := make([]QualitySnapshot, len(m.snapshots))
	copy(snaps, m.snapshots)
	m.mu.Unlock()

	endedAt := time.Now()
	final := m.cfg.StatsFn()

	report := m.buildReport(snaps, final, endedAt)

	if m.cfg.OutputDir != "" {
		if err := os.MkdirAll(m.cfg.OutputDir, 0o755); err == nil {
			m.writeReports(report)
		}
	}

	return report, nil
}

// ─── internals ───────────────────────────────────────────────────────────────

func (m *RTPMonitor) sampleLoop() {
	ticker := time.NewTicker(m.cfg.SampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.sample()
		}
	}
}

func (m *RTPMonitor) sample() {
	stats := m.cfg.StatsFn()
	snap := QualitySnapshot{
		At:        time.Now(),
		LatencyMs: stats.LatencyAvgMs,
	}

	// Packet loss %.
	if stats.PacketsReceived > 0 {
		snap.LossPct = float64(stats.PacketsLost) / float64(stats.PacketsReceived) * 100
	}

	// Jitter (if provided).
	if m.cfg.JitterMsFn != nil {
		snap.JitterMs = m.cfg.JitterMsFn()
	}

	// SNR estimate.
	if m.cfg.SNREstimateFn != nil {
		snap.SNREstDB = m.cfg.SNREstimateFn()
	} else {
		snap.SNREstDB = estimateSNRFromLoss(snap.LossPct)
	}

	// Alert logic.
	reason := m.checkThresholds(snap)
	if reason != "" {
		snap.AlertFired = true
		snap.AlertReason = reason
		atomic.AddInt64(&m.alerts, 1)
		if m.cfg.OnAlert != nil {
			m.cfg.OnAlert(reason)
		}
		if m.cfg.Pipeline != nil {
			if snap.LossPct > poorLossPct*100 {
				m.cfg.Pipeline.SetAggressiveness(3)
			} else if snap.JitterMs > poorJitterMs {
				m.cfg.Pipeline.SetAggressiveness(2)
			} else {
				m.cfg.Pipeline.SetAggressiveness(1)
			}
		}
	} else if m.cfg.Pipeline != nil {
		// Recovered — ease back to comfort noise
		m.cfg.Pipeline.SetAggressiveness(1)
	}

	m.mu.Lock()
	m.snapshots = append(m.snapshots, snap)
	m.mu.Unlock()
}

// checkThresholds returns a non-empty string if any quality threshold is breached.
func (m *RTPMonitor) checkThresholds(s QualitySnapshot) string {
	var reasons []string
	if s.LossPct > poorLossPct*100 {
		reasons = append(reasons, fmt.Sprintf("packet loss %.1f%% > %.0f%%", s.LossPct, poorLossPct*100))
	}
	if s.JitterMs > poorJitterMs {
		reasons = append(reasons, fmt.Sprintf("jitter %.1f ms > %.0f ms", s.JitterMs, poorJitterMs))
	}
	if s.SNREstDB > 0 && s.SNREstDB < poorSNRDB {
		reasons = append(reasons, fmt.Sprintf("SNR est. %.1f dB < %.0f dB", s.SNREstDB, poorSNRDB))
	}
	if len(reasons) == 0 {
		return ""
	}
	msg := "quality alert:"
	for _, r := range reasons {
		msg += " [" + r + "]"
	}
	return msg
}

// buildReport aggregates snapshots into an RTPSessionReport with recommendations.
func (m *RTPMonitor) buildReport(snaps []QualitySnapshot, final RTPStats, endedAt time.Time) RTPSessionReport {
	r := RTPSessionReport{
		StartedAt:       m.startedAt,
		EndedAt:         endedAt,
		DurationMs:      float64(endedAt.Sub(m.startedAt).Milliseconds()),
		PacketsReceived: final.PacketsReceived,
		PacketsLost:     final.PacketsLost,
		AlertCount:      int(atomic.LoadInt64(&m.alerts)),
		Snapshots:       snaps,
	}

	if final.PacketsReceived > 0 {
		r.LossPct = float64(final.PacketsLost) / float64(final.PacketsReceived) * 100
	}
	r.AvgLatencyMs = final.LatencyAvgMs

	// Averages across snapshots.
	if len(snaps) > 0 {
		var sumJitter, sumSNR float64
		for _, s := range snaps {
			sumJitter += s.JitterMs
			sumSNR += s.SNREstDB
		}
		r.AvgJitterMs = sumJitter / float64(len(snaps))
		r.AvgSNREstDB = sumSNR / float64(len(snaps))
	}

	// Recommendations.
	r.Recommendations = m.recommend(r)

	// Build synthetic BatchSummary to reuse TuneFromBatchSummary logic.
	pseudo := BatchSummary{
		ProcessedFiles:      1,
		AvgSNRImprovementDB: r.AvgSNREstDB - poorSNRDB, // rough estimate
		AvgLatencyP95Ms:     r.AvgLatencyMs * 1.5,      // approximate P95
		AvgSpeechRatio:      0.6,                       // telephony typical
	}
	tuned := TuneFromBatchSummary(pseudo)
	r.TunedConfig = &tuned

	return r
}

// recommend returns a slice of actionable config suggestions based on session metrics.
func (m *RTPMonitor) recommend(r RTPSessionReport) []string {
	var recs []string

	if r.LossPct > 5.0 {
		recs = append(recs, fmt.Sprintf(
			"Packet loss %.1f%% is high — increase JitterDepth to buffer loss bursts; "+
				"consider enabling PLC (GeneratePLC is already in jitter.go)", r.LossPct))
	} else if r.LossPct > poorLossPct*100 {
		recs = append(recs, fmt.Sprintf(
			"Packet loss %.1f%% exceeds %.0f%% threshold — increase JitterDepth by 2 frames", r.LossPct, poorLossPct*100))
	}

	if r.AvgJitterMs > poorJitterMs {
		depth := int(math.Ceil(r.AvgJitterMs/10.0)) + 2
		recs = append(recs, fmt.Sprintf(
			"Avg jitter %.1f ms — set JitterDepth=%d frames (ceil(jitter/10ms)+2)", r.AvgJitterMs, depth))
	}

	if r.AvgSNREstDB > 0 && r.AvgSNREstDB < poorSNRDB {
		recs = append(recs, fmt.Sprintf(
			"Estimated SNR %.1f dB is below %.0f dB — raise SuppressorAggressiveness to 3 (aggressive)", r.AvgSNREstDB, poorSNRDB))
	}

	if r.AvgLatencyMs > 8.0 {
		recs = append(recs, fmt.Sprintf(
			"Avg processing latency %.1f ms > 8ms real-time budget — "+
				"reduce SuppressorAggressiveness or disable AGC on this codec", r.AvgLatencyMs))
	}

	if r.AlertCount == 0 {
		recs = append(recs, "Session quality is good — no config changes required")
	}
	return recs
}

// estimateSNRFromLoss provides a rough SNR proxy when no direct measurement is available.
// High loss → worse effective SNR for the listener.
func estimateSNRFromLoss(lossPct float64) float64 {
	if lossPct <= 0 {
		return 30.0 // assume good quality
	}
	// Empirical: 5% loss ≈ 20 dB effective SNR degradation (E-Model approximation).
	return math.Max(0, 30.0-lossPct*4)
}

// ─── Output files ─────────────────────────────────────────────────────────────

func (m *RTPMonitor) writeReports(r RTPSessionReport) {
	ts := time.Now().Format("20060102_150405")

	// JSON report.
	jsonPath := filepath.Join(m.cfg.OutputDir, fmt.Sprintf("rtp_eval_%s.json", ts))
	if f, err := os.Create(jsonPath); err == nil {
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		f.Close()
	}

	// Tuned config YAML — written via WriteConfigYAML (no yaml dep required).
	if r.TunedConfig != nil {
		_, _ = WriteConfigYAML(m.cfg.OutputDir, *r.TunedConfig)
	}
}
