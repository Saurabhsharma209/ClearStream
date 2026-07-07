// Package telemetry provides a vendor-neutral observability interface for
// ClearStream. It lets applications embedding the SDK receive metrics and
// structured events covering reliability, media quality of service (QoS),
// audio interruption rate (IRR), and CPU/resource usage — without coupling
// the SDK itself to any specific metrics backend (Prometheus, StatsD,
// CloudWatch, etc.). Applications implement the Sink interface and forward
// data to whatever backend they use; if no Sink is configured, ClearStream
// uses NoopSink and pays effectively zero overhead.
package telemetry

import "time"

// MetricKind describes how a Metric's Value should be interpreted/aggregated
// by the receiving backend.
type MetricKind int

const (
	// MetricCounter is a monotonically increasing value (e.g. total frames
	// processed). Backends typically track the delta since the last report.
	MetricCounter MetricKind = iota
	// MetricGauge is a point-in-time value that can go up or down (e.g.
	// current jitter buffer depth).
	MetricGauge
	// MetricHistogram is a value intended to be bucketed/aggregated across
	// many observations (e.g. per-frame processing latency).
	MetricHistogram
)

// String implements fmt.Stringer for MetricKind.
func (k MetricKind) String() string {
	switch k {
	case MetricCounter:
		return "counter"
	case MetricGauge:
		return "gauge"
	case MetricHistogram:
		return "histogram"
	default:
		return "unknown"
	}
}

// Severity classifies an Event's importance, mirroring common log levels so
// Sinks can route to the right alerting channel.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarn
	SeverityError
	SeverityCritical
)

// String implements fmt.Stringer for Severity.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarn:
		return "warn"
	case SeverityError:
		return "error"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// Metric is a single numeric observation emitted by the SDK.
type Metric struct {
	// Name is a dotted, namespaced identifier — see the Metric* constants
	// below for the full catalog of names the SDK emits.
	Name string
	// Value is the numeric observation.
	Value float64
	// Unit documents the measurement unit ("ms", "ns", "bytes", "ratio",
	// "percent", "count") for backends/dashboards that care.
	Unit string
	Kind MetricKind
	// Tags carry cardinality-bounded dimensions (call ID, codec, pool name).
	// Callers should avoid unbounded-cardinality tags (e.g. raw IP addresses).
	Tags      map[string]string
	Timestamp time.Time
}

// Event is a discrete, structured occurrence — a lifecycle transition, a
// reliability incident, or a quality-degradation trigger.
type Event struct {
	// Name is a dotted identifier — see the Event* constants below.
	Name     string
	Severity Severity
	Message  string
	// Fields carries structured context (call_id, cause, duration_ms, ...).
	Fields    map[string]interface{}
	Timestamp time.Time
}

// Sink is implemented by applications embedding ClearStream to receive
// telemetry. Implementations must be safe for concurrent use — the SDK may
// call RecordMetric/RecordEvent from multiple goroutines (one per active
// RTP session, file-processing worker, etc.) simultaneously. Implementations
// should not block or do expensive work inline; buffer/batch internally if
// forwarding over the network.
type Sink interface {
	RecordMetric(m Metric)
	RecordEvent(e Event)
}

// StartTimer starts a wall-clock timer and returns a func that, when called,
// records a histogram Metric named name with the elapsed time in
// milliseconds. This is a convenience for the common "measure how long this
// call took" pattern used throughout the pipeline instrumentation, e.g.:
//
//	stop := telemetry.StartTimer(sink, telemetry.MetricFrameLatencyMS, tags)
//	defer stop()
func StartTimer(sink Sink, name string, tags map[string]string) func() {
	start := time.Now()
	return func() {
		if sink == nil {
			return
		}
		sink.RecordMetric(Metric{
			Name:      name,
			Value:     float64(time.Since(start).Microseconds()) / 1000.0,
			Unit:      "ms",
			Kind:      MetricHistogram,
			Tags:      tags,
			Timestamp: time.Now(),
		})
	}
}

// ---------------------------------------------------------------------------
// Metric name catalog, grouped by observability dimension. Names follow the
// "clearstream.<component>.<measurement>" convention so they namespace
// cleanly in any downstream backend.
// ---------------------------------------------------------------------------

const (
	// -- Reliability ----------------------------------------------------

	// MetricFramesProcessedTotal counts frames successfully processed
	// end-to-end (counter).
	MetricFramesProcessedTotal = "clearstream.pipeline.frames_processed_total"
	// MetricErrorsTotal counts errors encountered anywhere in the pipeline,
	// tagged by component and error_type (counter).
	MetricErrorsTotal = "clearstream.errors_total"
	// MetricPanicsRecoveredTotal counts panics caught and recovered by
	// pipeline guard code, tagged by component (counter). Any non-zero rate
	// here indicates a reliability bug that needs investigation.
	MetricPanicsRecoveredTotal = "clearstream.pipeline.panics_recovered_total"
	// MetricSuppressorResetsTotal counts AI suppressor resets, tagged by
	// reason (counter).
	MetricSuppressorResetsTotal = "clearstream.suppressor.resets_total"

	// -- Media QoS --------------------------------------------------------

	// MetricRTPJitterMS is the current estimated RTP jitter in milliseconds
	// (gauge).
	MetricRTPJitterMS = "clearstream.rtp.jitter_ms"
	// MetricRTPPacketLossRatio is packets lost / packets expected over the
	// current measurement window, 0.0-1.0 (gauge).
	MetricRTPPacketLossRatio = "clearstream.rtp.packet_loss_ratio"
	// MetricRTPPLCTriggeredTotal counts packet-loss-concealment invocations
	// — each one is an audible quality compromise (counter).
	MetricRTPPLCTriggeredTotal = "clearstream.rtp.plc_triggered_total"
	// MetricRTPSSRCChangeTotal counts SSRC changes (new call leg detected on
	// an existing session) (counter).
	MetricRTPSSRCChangeTotal = "clearstream.rtp.ssrc_change_total"
	// MetricAudioSNRImprovementDB is the measured SNR improvement in dB
	// attributable to suppression, per evaluation window (histogram).
	MetricAudioSNRImprovementDB = "clearstream.audio.snr_improvement_db"
	// MetricAudioVADSpeechRatio is the fraction of frames classified as
	// speech (vs. silence) in the current window, 0.0-1.0 (gauge).
	MetricAudioVADSpeechRatio = "clearstream.audio.vad_speech_ratio"

	// -- IRR: Interruption Rate Ratio -------------------------------------
	// "IRR" is not a standardized industry acronym — it does not appear in
	// any telephony/audio-quality standard, nor in Exotel's internal
	// knowledge base. This SDK defines it explicitly as: the ratio of audio
	// frames that were interrupted (concealed, dropped, or replaced with
	// silence) to total frames delivered for a call. It is a user-perceived
	// continuity metric, distinct from raw network packet loss (which
	// measures loss *before* concealment papers over it). If your
	// organization uses "IRR" to mean something else, treat MetricIRR/
	// EventAudioInterruption as a starting point and rename/redefine as
	// needed — the underlying instrumentation (interruption counting) is
	// still the correct signal for "did the caller hear a glitch".

	// MetricIRR is interrupted_frames / total_frames for the current call or
	// measurement window, 0.0-1.0 (gauge). 0 = perfectly continuous audio.
	MetricIRR = "clearstream.audio.irr"
	// MetricInterruptionsTotal counts individual interruption occurrences,
	// tagged by cause: "plc" | "jitter_overflow" | "decode_error" |
	// "buffer_underrun" (counter).
	MetricInterruptionsTotal = "clearstream.audio.interruptions_total"

	// -- CPU / resource usage ----------------------------------------------

	// MetricFrameLatencyMS is wall-clock time to process one audio frame
	// through the suppression pipeline (histogram).
	MetricFrameLatencyMS = "clearstream.frame.process_latency_ms"
	// MetricModelInferenceLatencyMS is wall-clock time spent inside the AI
	// suppressor's Process call specifically, isolating model cost from
	// codec/resample overhead (histogram).
	MetricModelInferenceLatencyMS = "clearstream.model.inference_latency_ms"
	// MetricGoroutinesActive is runtime.NumGoroutine() sampled periodically
	// (gauge) — a proxy for concurrency-driven resource pressure.
	MetricGoroutinesActive = "clearstream.runtime.goroutines_active"
	// MetricWorkerPoolActive is the number of in-flight workers in a bounded
	// pool (e.g. file.ProcessDir's MaxConcurrency semaphore), tagged by pool
	// name (gauge).
	MetricWorkerPoolActive = "clearstream.worker_pool.active"
	// MetricWorkerPoolQueueDepth is the number of items waiting for a free
	// worker slot, tagged by pool name (gauge).
	MetricWorkerPoolQueueDepth = "clearstream.worker_pool.queue_depth"

	// -- Other ---------------------------------------------------------

	// MetricBufferPoolHitRatio is sync.Pool reuse hits / total gets, 0.0-1.0
	// (gauge) — measures allocation-pooling effectiveness.
	MetricBufferPoolHitRatio = "clearstream.memory.buffer_pool_hit_ratio"
	// MetricBatchProgressPercent is file.ProcessDir batch completion
	// percentage, 0-100 (gauge).
	MetricBatchProgressPercent = "clearstream.file.batch_progress_percent"
	// MetricWALFlushLatencyMS is billing WAL flush wall-clock latency
	// (histogram).
	MetricWALFlushLatencyMS = "clearstream.billing.wal_flush_latency_ms"
)

// ---------------------------------------------------------------------------
// Event name catalog.
// ---------------------------------------------------------------------------

const (
	// EventCallStarted fires when a new RTP session/call begins. Fields:
	// call_id, codec, remote_addr.
	EventCallStarted = "clearstream.call.started"
	// EventCallEnded fires when a call/session ends. Fields: call_id,
	// duration_ms, frames_processed, interruptions_total.
	EventCallEnded = "clearstream.call.ended"
	// EventSuppressorInitFailed fires when the AI suppressor fails to
	// initialize (e.g. model load failure). Severity: error.
	EventSuppressorInitFailed = "clearstream.suppressor.init_failed"
	// EventSuppressorReset fires whenever a suppressor is reset mid-call.
	// Fields: reason.
	EventSuppressorReset = "clearstream.suppressor.reset"
	// EventPanicRecovered fires when pipeline guard code recovers from a
	// panic. Severity: critical. Fields: component, recovered_value, stack.
	EventPanicRecovered = "clearstream.pipeline.panic_recovered"
	// EventRTPDecodeError fires on a codec decode failure. Fields: call_id,
	// codec, error.
	EventRTPDecodeError = "clearstream.rtp.decode_error"
	// EventRTPSSRCChanged fires when a session detects a new SSRC (new call
	// leg) and resets. Fields: call_id, old_ssrc, new_ssrc.
	EventRTPSSRCChanged = "clearstream.rtp.ssrc_changed"
	// EventAudioInterruption fires once per interruption occurrence (see
	// MetricIRR doc above for definition). Fields: call_id, duration_ms,
	// cause.
	EventAudioInterruption = "clearstream.audio.interruption"
	// EventWALFlushFailed fires when a billing WAL flush fails. Fields:
	// error, path.
	EventWALFlushFailed = "clearstream.billing.wal_flush_failed"
	// EventBatchFileFailed fires when a single file fails during
	// file.ProcessDir batch processing. Fields: path, error.
	EventBatchFileFailed = "clearstream.file.batch_file_failed"
)
