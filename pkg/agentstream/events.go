package agentstream

import "encoding/json"

// EventType identifies the type of WSS event.
type EventType string

const (
	EventStart         EventType = "start"
	EventMedia         EventType = "media"
	EventCleanMedia    EventType = "clean_media"
	EventDTMF          EventType = "dtmf"
	EventSpeechStarted EventType = "speech_started"
	EventSpeechEnded   EventType = "speech_ended"
	EventTurnPredicted EventType = "turn_predicted"
	EventInterruption  EventType = "interruption_detected"
	EventBackchannel   EventType = "backchannel_detected"
	EventClear         EventType = "clear"
	EventMediaQuality  EventType = "media_quality"
	EventMark          EventType = "mark"
	EventStop          EventType = "stop"
	EventError         EventType = "error"
)

// RecommendedAction is the bot action hint in conversation events.
type RecommendedAction string

const (
	ActionWait        RecommendedAction = "wait"
	ActionRespond     RecommendedAction = "bot_respond"
	ActionStopTTS     RecommendedAction = "stop_tts"
	ActionContinueTTS RecommendedAction = "continue_tts"
)

// CustomParameters holds SIP header metadata passed through to the bot.
// Keys are normalized to snake_case (e.g. X-Campaign-ID → campaign_id).
type CustomParameters map[string]string

// StartEvent is sent when a new stream is established.
type StartEvent struct {
	Event            EventType        `json:"event"`
	CallSID          string           `json:"call_sid"`
	StreamSID        string           `json:"stream_sid"`
	AccountSID       string           `json:"account_sid"`
	Direction        string           `json:"direction"`
	From             string           `json:"from"`
	To               string           `json:"to"`
	Codec            string           `json:"codec"`
	SampleRate       int              `json:"sample_rate"`
	Tracks           []string         `json:"tracks"`
	CustomParameters CustomParameters `json:"custom_parameters,omitempty"`
}

// MediaEvent carries a raw decoded RTP audio frame.
type MediaEvent struct {
	Event          EventType `json:"event"`
	StreamSID      string    `json:"stream_sid"`
	SequenceNumber uint64    `json:"sequence_number"`
	Track          string    `json:"track"`
	Codec          string    `json:"codec"`
	SampleRate     int       `json:"sample_rate"`
	TimestampMs    int64     `json:"timestamp_ms"`
	Payload        string    `json:"payload"`
}

// EnhancementInfo describes which audio processing was applied.
type EnhancementInfo struct {
	NoiseSuppression            bool `json:"noise_suppression"`
	VoiceIsolation              bool `json:"voice_isolation"`
	BackgroundVoiceCancellation bool `json:"background_voice_cancellation"`
	GainNormalization           bool `json:"gain_normalization"`
	EchoCancellation            bool `json:"echo_cancellation"`
}

// CleanMediaEvent carries an enhanced audio frame with processing metadata.
type CleanMediaEvent struct {
	Event               EventType       `json:"event"`
	StreamSID           string          `json:"stream_sid"`
	SequenceNumber      uint64          `json:"sequence_number"`
	Track               string          `json:"track"`
	Codec               string          `json:"codec"`
	SampleRate          int             `json:"sample_rate"`
	TimestampMs         int64           `json:"timestamp_ms"`
	Enhancement         EnhancementInfo `json:"enhancement"`
	ProcessingLatencyMs float64         `json:"processing_latency_ms"`
	Payload             string          `json:"payload"`
}

// DTMFEvent is emitted when an RFC2833 or SIP INFO DTMF digit is detected.
type DTMFEvent struct {
	Event      EventType `json:"event"`
	StreamSID  string    `json:"stream_sid"`
	Digit      string    `json:"digit"`
	DurationMs int       `json:"duration_ms"`
	Source     string    `json:"source"`
}

// SpeechStartedEvent is emitted when VAD detects speech beginning.
type SpeechStartedEvent struct {
	Event       EventType `json:"event"`
	StreamSID   string    `json:"stream_sid"`
	Speaker     string    `json:"speaker"`
	TimestampMs int64     `json:"timestamp_ms"`
}

// SpeechEndedEvent is emitted when VAD detects end of speech.
type SpeechEndedEvent struct {
	Event            EventType `json:"event"`
	StreamSID        string    `json:"stream_sid"`
	Speaker          string    `json:"speaker"`
	TimestampMs      int64     `json:"timestamp_ms"`
	SpeechDurationMs int64     `json:"speech_duration_ms"`
}

// TurnPredictedEvent is emitted when the system predicts the caller has finished their turn.
type TurnPredictedEvent struct {
	Event               EventType         `json:"event"`
	StreamSID           string            `json:"stream_sid"`
	Speaker             string            `json:"speaker"`
	Confidence          float64           `json:"confidence"`
	RecommendedAction   RecommendedAction `json:"recommended_action"`
	ProcessingLatencyMs float64           `json:"processing_latency_ms"`
}

// InterruptionEvent is emitted when the caller interrupts bot playback.
type InterruptionEvent struct {
	Event               EventType         `json:"event"`
	StreamSID           string            `json:"stream_sid"`
	Confidence          float64           `json:"confidence"`
	RecommendedAction   RecommendedAction `json:"recommended_action"`
	ProcessingLatencyMs float64           `json:"processing_latency_ms"`
}

// BackchannelEvent is emitted for short acknowledgments ("hmm", "okay") that should not stop TTS.
type BackchannelEvent struct {
	Event             EventType         `json:"event"`
	StreamSID         string            `json:"stream_sid"`
	Confidence        float64           `json:"confidence"`
	RecommendedAction RecommendedAction `json:"recommended_action"`
}

// ClearEvent instructs the bot to stop queued TTS audio immediately.
type ClearEvent struct {
	Event     EventType `json:"event"`
	StreamSID string    `json:"stream_sid"`
	Reason    string    `json:"reason"`
}

// MediaQualityEvent is emitted periodically with RTP quality metrics.
type MediaQualityEvent struct {
	Event             EventType `json:"event"`
	StreamSID         string    `json:"stream_sid"`
	Codec             string    `json:"codec"`
	JitterMs          float64   `json:"jitter_ms"`
	PacketLossPct     float64   `json:"packet_loss_pct"`
	RTPGapCount       int       `json:"rtp_gap_count"`
	SilenceDurationMs int64     `json:"silence_duration_ms"`
	QualityScore      int       `json:"quality_score"`
}

// MarkEvent confirms bot audio playback progress.
type MarkEvent struct {
	Event     EventType `json:"event"`
	StreamSID string    `json:"stream_sid"`
	Name      string    `json:"name"`
}

// StopEvent is emitted when the stream ends (call completed or error).
type StopEvent struct {
	Event       EventType `json:"event"`
	StreamSID   string    `json:"stream_sid"`
	CallSID     string    `json:"call_sid"`
	Reason      string    `json:"reason"`
	DurationSec int       `json:"duration_sec"`
}

// ErrorEvent is emitted on stream errors.
// FailureCode identifies the category of a stream error.
type FailureCode string

const (
	FailureCodeInternal    FailureCode = "internal_error"
	FailureCodeTimeout     FailureCode = "timeout"
	FailureCodeUnsupported FailureCode = "unsupported_codec"
	FailureCodeAuth        FailureCode = "auth_failed"
)

type ErrorEvent struct {
	Event     EventType   `json:"event"`
	StreamSID string      `json:"stream_sid"`
	Code      FailureCode `json:"code"`
	Message   string      `json:"message"`
}

// MarshalEvent serializes any event struct to JSON bytes.
func MarshalEvent(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
