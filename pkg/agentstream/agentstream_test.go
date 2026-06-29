package agentstream

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// StreamState.String()
// ---------------------------------------------------------------------------

func TestStreamStateString(t *testing.T) {
	cases := []struct {
		state    StreamState
		expected string
	}{
		{StateCreated, "CREATED"},
		{StateSIPNegotiating, "SIP_NEGOTIATING"},
		{StateRTPEstablished, "RTP_ESTABLISHED"},
		{StateWSSConnecting, "WSS_CONNECTING"},
		{StateStreaming, "STREAMING"},
		{StateBotAudioActive, "BOT_AUDIO_ACTIVE"},
		{StateInterruptionDetected, "INTERRUPTION_DETECTED"},
		{StateClearingPlayback, "CLEARING_PLAYBACK"},
		{StateStopping, "STOPPING"},
		{StateCompleted, "COMPLETED"},
		{StateSIPFailed, "SIP_FAILED"},
		{StateRTPTimeout, "RTP_TIMEOUT"},
		{StateWSSFailed, "WSS_FAILED"},
		{StateBotTimeout, "BOT_TIMEOUT"},
		{StateModelFailed, "MODEL_FAILED"},
		{StateFallbackActive, "FALLBACK_ACTIVE"},
		{StateForceTerminated, "FORCE_TERMINATED"},
		// out-of-range → default branch
		{StreamState(255), "UNKNOWN"},
	}
	for _, tc := range cases {
		t.Run(tc.expected, func(t *testing.T) {
			if got := tc.state.String(); got != tc.expected {
				t.Errorf("StreamState(%d).String() = %q; want %q", tc.state, got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// StreamState.IsError()
// ---------------------------------------------------------------------------

func TestStreamStateIsError(t *testing.T) {
	errorStates := []StreamState{
		StateSIPFailed, StateRTPTimeout, StateWSSFailed,
		StateBotTimeout, StateModelFailed, StateFallbackActive, StateForceTerminated,
	}
	nonErrorStates := []StreamState{
		StateCreated, StateSIPNegotiating, StateRTPEstablished,
		StateWSSConnecting, StateStreaming, StateBotAudioActive,
		StateInterruptionDetected, StateClearingPlayback, StateStopping, StateCompleted,
	}
	for _, s := range errorStates {
		if !s.IsError() {
			t.Errorf("expected %s to be an error state", s)
		}
	}
	for _, s := range nonErrorStates {
		if s.IsError() {
			t.Errorf("expected %s NOT to be an error state", s)
		}
	}
}

// ---------------------------------------------------------------------------
// StreamState.IsTerminal()
// ---------------------------------------------------------------------------

func TestStreamStateIsTerminal(t *testing.T) {
	terminalStates := []StreamState{StateCompleted, StateForceTerminated}
	nonTerminalStates := []StreamState{
		StateCreated, StateSIPNegotiating, StateRTPEstablished,
		StateWSSConnecting, StateStreaming, StateBotAudioActive,
		StateInterruptionDetected, StateClearingPlayback, StateStopping,
		StateSIPFailed, StateRTPTimeout, StateWSSFailed,
		StateBotTimeout, StateModelFailed, StateFallbackActive,
	}
	for _, s := range terminalStates {
		if !s.IsTerminal() {
			t.Errorf("expected %s to be terminal", s)
		}
	}
	for _, s := range nonTerminalStates {
		if s.IsTerminal() {
			t.Errorf("expected %s NOT to be terminal", s)
		}
	}
}

// ---------------------------------------------------------------------------
// CanTransition
// ---------------------------------------------------------------------------

func TestCanTransition_ValidPaths(t *testing.T) {
	valid := [][2]StreamState{
		{StateCreated, StateSIPNegotiating},
		{StateSIPNegotiating, StateRTPEstablished},
		{StateRTPEstablished, StateWSSConnecting},
		{StateWSSConnecting, StateStreaming},
		{StateStreaming, StateBotAudioActive},
		{StateStreaming, StateStopping},
		{StateBotAudioActive, StateInterruptionDetected},
		{StateInterruptionDetected, StateClearingPlayback},
		{StateClearingPlayback, StateStreaming},
		{StateStopping, StateCompleted},
		{StateStopping, StateForceTerminated},
		{StateWSSFailed, StateFallbackActive},
		{StateFallbackActive, StateStreaming},
		{StateRTPTimeout, StateStopping},
		{StateSIPFailed, StateForceTerminated},
		{StateBotTimeout, StateFallbackActive},
		{StateModelFailed, StateStreaming},
	}
	for _, pair := range valid {
		if !CanTransition(pair[0], pair[1]) {
			t.Errorf("CanTransition(%s → %s) should be true", pair[0], pair[1])
		}
	}
}

func TestCanTransition_InvalidPaths(t *testing.T) {
	invalid := [][2]StreamState{
		{StateCreated, StateStreaming},
		{StateCompleted, StateCreated},
		{StateForceTerminated, StateCreated},
		{StateStreaming, StateCreated},
		// state with no entry in validTransitions
		{StateCompleted, StateStreaming},
	}
	for _, pair := range invalid {
		if CanTransition(pair[0], pair[1]) {
			t.Errorf("CanTransition(%s → %s) should be false", pair[0], pair[1])
		}
	}
}

// ---------------------------------------------------------------------------
// EventType constants
// ---------------------------------------------------------------------------

func TestEventTypeConstants(t *testing.T) {
	cases := []struct {
		et       EventType
		expected string
	}{
		{EventStart, "start"},
		{EventMedia, "media"},
		{EventCleanMedia, "clean_media"},
		{EventDTMF, "dtmf"},
		{EventSpeechStarted, "speech_started"},
		{EventSpeechEnded, "speech_ended"},
		{EventTurnPredicted, "turn_predicted"},
		{EventInterruption, "interruption_detected"},
		{EventBackchannel, "backchannel_detected"},
		{EventClear, "clear"},
		{EventMediaQuality, "media_quality"},
		{EventMark, "mark"},
		{EventStop, "stop"},
		{EventError, "error"},
	}
	for _, tc := range cases {
		if string(tc.et) != tc.expected {
			t.Errorf("EventType %q != %q", tc.et, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// RecommendedAction constants
// ---------------------------------------------------------------------------

func TestRecommendedActionConstants(t *testing.T) {
	cases := []struct {
		action   RecommendedAction
		expected string
	}{
		{ActionWait, "wait"},
		{ActionRespond, "bot_respond"},
		{ActionStopTTS, "stop_tts"},
		{ActionContinueTTS, "continue_tts"},
	}
	for _, tc := range cases {
		if string(tc.action) != tc.expected {
			t.Errorf("RecommendedAction %q != %q", tc.action, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// FailureCode constants
// ---------------------------------------------------------------------------

func TestFailureCodeConstants(t *testing.T) {
	cases := []struct {
		code     FailureCode
		expected string
	}{
		{FailureCodeInternal, "internal_error"},
		{FailureCodeTimeout, "timeout"},
		{FailureCodeUnsupported, "unsupported_codec"},
		{FailureCodeAuth, "auth_failed"},
	}
	for _, tc := range cases {
		if string(tc.code) != tc.expected {
			t.Errorf("FailureCode %q != %q", tc.code, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// StartEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestStartEventJSONRoundTrip(t *testing.T) {
	original := StartEvent{
		Event:      EventStart,
		CallSID:    "CA123",
		StreamSID:  "MZ456",
		AccountSID: "AC789",
		Direction:  "inbound",
		From:       "+15551234567",
		To:         "+15559876543",
		Codec:      "PCMU",
		SampleRate: 8000,
		Tracks:     []string{"inbound", "outbound"},
		CustomParameters: CustomParameters{
			"campaign_id": "camp-42",
			"language":    "en-US",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal StartEvent: %v", err)
	}

	var got StartEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal StartEvent: %v", err)
	}

	if got.Event != original.Event {
		t.Errorf("Event: got %q want %q", got.Event, original.Event)
	}
	if got.CallSID != original.CallSID {
		t.Errorf("CallSID: got %q want %q", got.CallSID, original.CallSID)
	}
	if got.StreamSID != original.StreamSID {
		t.Errorf("StreamSID: got %q want %q", got.StreamSID, original.StreamSID)
	}
	if got.AccountSID != original.AccountSID {
		t.Errorf("AccountSID: got %q want %q", got.AccountSID, original.AccountSID)
	}
	if got.Direction != original.Direction {
		t.Errorf("Direction: got %q want %q", got.Direction, original.Direction)
	}
	if got.From != original.From {
		t.Errorf("From: got %q want %q", got.From, original.From)
	}
	if got.To != original.To {
		t.Errorf("To: got %q want %q", got.To, original.To)
	}
	if got.Codec != original.Codec {
		t.Errorf("Codec: got %q want %q", got.Codec, original.Codec)
	}
	if got.SampleRate != original.SampleRate {
		t.Errorf("SampleRate: got %d want %d", got.SampleRate, original.SampleRate)
	}
	if len(got.Tracks) != len(original.Tracks) {
		t.Errorf("Tracks len: got %d want %d", len(got.Tracks), len(original.Tracks))
	}
	if got.CustomParameters["campaign_id"] != "camp-42" {
		t.Errorf("CustomParameters[campaign_id]: got %q want %q", got.CustomParameters["campaign_id"], "camp-42")
	}
}

// StartEvent omitempty: CustomParameters absent when nil
func TestStartEventOmitEmptyCustomParameters(t *testing.T) {
	ev := StartEvent{
		Event:     EventStart,
		CallSID:   "CA000",
		StreamSID: "MZ000",
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, ok := m["custom_parameters"]; ok {
		t.Error("custom_parameters should be absent when nil (omitempty)")
	}
}

// ---------------------------------------------------------------------------
// MediaEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestMediaEventJSONRoundTrip(t *testing.T) {
	original := MediaEvent{
		Event:          EventMedia,
		StreamSID:      "MZ789",
		SequenceNumber: 42,
		Track:          "inbound",
		Codec:          "PCMU",
		SampleRate:     8000,
		TimestampMs:    1234567890,
		Payload:        "base64encodedaudio==",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal MediaEvent: %v", err)
	}

	var got MediaEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal MediaEvent: %v", err)
	}

	if got.SequenceNumber != original.SequenceNumber {
		t.Errorf("SequenceNumber: got %d want %d", got.SequenceNumber, original.SequenceNumber)
	}
	if got.TimestampMs != original.TimestampMs {
		t.Errorf("TimestampMs: got %d want %d", got.TimestampMs, original.TimestampMs)
	}
	if got.Payload != original.Payload {
		t.Errorf("Payload: got %q want %q", got.Payload, original.Payload)
	}
	if got.Track != original.Track {
		t.Errorf("Track: got %q want %q", got.Track, original.Track)
	}
}

// ---------------------------------------------------------------------------
// CleanMediaEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestCleanMediaEventJSONRoundTrip(t *testing.T) {
	original := CleanMediaEvent{
		Event:          EventCleanMedia,
		StreamSID:      "MZ-clean",
		SequenceNumber: 7,
		Track:          "inbound",
		Codec:          "PCMU",
		SampleRate:     16000,
		TimestampMs:    9999,
		Enhancement: EnhancementInfo{
			NoiseSuppression:            true,
			VoiceIsolation:              false,
			BackgroundVoiceCancellation: true,
			GainNormalization:           true,
			EchoCancellation:            false,
		},
		ProcessingLatencyMs: 3.14,
		Payload:             "cleanpayload==",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal CleanMediaEvent: %v", err)
	}

	var got CleanMediaEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal CleanMediaEvent: %v", err)
	}

	if got.Enhancement.NoiseSuppression != true {
		t.Error("NoiseSuppression should be true")
	}
	if got.Enhancement.BackgroundVoiceCancellation != true {
		t.Error("BackgroundVoiceCancellation should be true")
	}
	if got.Enhancement.VoiceIsolation != false {
		t.Error("VoiceIsolation should be false")
	}
	if got.ProcessingLatencyMs != original.ProcessingLatencyMs {
		t.Errorf("ProcessingLatencyMs: got %f want %f", got.ProcessingLatencyMs, original.ProcessingLatencyMs)
	}
	if got.Payload != original.Payload {
		t.Errorf("Payload: got %q want %q", got.Payload, original.Payload)
	}
}

// ---------------------------------------------------------------------------
// DTMFEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestDTMFEventJSONRoundTrip(t *testing.T) {
	original := DTMFEvent{
		Event:      EventDTMF,
		StreamSID:  "MZ-dtmf",
		Digit:      "5",
		DurationMs: 120,
		Source:     "rfc2833",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal DTMFEvent: %v", err)
	}
	var got DTMFEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal DTMFEvent: %v", err)
	}
	if got.Digit != original.Digit {
		t.Errorf("Digit: got %q want %q", got.Digit, original.Digit)
	}
	if got.DurationMs != original.DurationMs {
		t.Errorf("DurationMs: got %d want %d", got.DurationMs, original.DurationMs)
	}
}

// ---------------------------------------------------------------------------
// SpeechStartedEvent / SpeechEndedEvent JSON round-trips
// ---------------------------------------------------------------------------

func TestSpeechStartedEventJSONRoundTrip(t *testing.T) {
	original := SpeechStartedEvent{
		Event:       EventSpeechStarted,
		StreamSID:   "MZ-vad",
		Speaker:     "caller",
		TimestampMs: 5000,
	}
	data, _ := json.Marshal(original)
	var got SpeechStartedEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Speaker != original.Speaker || got.TimestampMs != original.TimestampMs {
		t.Errorf("SpeechStartedEvent mismatch: got %+v want %+v", got, original)
	}
}

func TestSpeechEndedEventJSONRoundTrip(t *testing.T) {
	original := SpeechEndedEvent{
		Event:            EventSpeechEnded,
		StreamSID:        "MZ-vad",
		Speaker:          "caller",
		TimestampMs:      8000,
		SpeechDurationMs: 3000,
	}
	data, _ := json.Marshal(original)
	var got SpeechEndedEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SpeechDurationMs != original.SpeechDurationMs {
		t.Errorf("SpeechDurationMs: got %d want %d", got.SpeechDurationMs, original.SpeechDurationMs)
	}
}

// ---------------------------------------------------------------------------
// TurnPredictedEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestTurnPredictedEventJSONRoundTrip(t *testing.T) {
	original := TurnPredictedEvent{
		Event:               EventTurnPredicted,
		StreamSID:           "MZ-turn",
		Speaker:             "caller",
		Confidence:          0.95,
		RecommendedAction:   ActionRespond,
		ProcessingLatencyMs: 12.5,
	}
	data, _ := json.Marshal(original)
	var got TurnPredictedEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Confidence != original.Confidence {
		t.Errorf("Confidence: got %f want %f", got.Confidence, original.Confidence)
	}
	if got.RecommendedAction != original.RecommendedAction {
		t.Errorf("RecommendedAction: got %q want %q", got.RecommendedAction, original.RecommendedAction)
	}
}

// ---------------------------------------------------------------------------
// InterruptionEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestInterruptionEventJSONRoundTrip(t *testing.T) {
	original := InterruptionEvent{
		Event:               EventInterruption,
		StreamSID:           "MZ-intr",
		Confidence:          0.88,
		RecommendedAction:   ActionStopTTS,
		ProcessingLatencyMs: 5.1,
	}
	data, _ := json.Marshal(original)
	var got InterruptionEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RecommendedAction != ActionStopTTS {
		t.Errorf("RecommendedAction: got %q want %q", got.RecommendedAction, ActionStopTTS)
	}
}

// ---------------------------------------------------------------------------
// BackchannelEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestBackchannelEventJSONRoundTrip(t *testing.T) {
	original := BackchannelEvent{
		Event:             EventBackchannel,
		StreamSID:         "MZ-bc",
		Confidence:        0.72,
		RecommendedAction: ActionContinueTTS,
	}
	data, _ := json.Marshal(original)
	var got BackchannelEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RecommendedAction != ActionContinueTTS {
		t.Errorf("RecommendedAction: got %q want %q", got.RecommendedAction, ActionContinueTTS)
	}
}

// ---------------------------------------------------------------------------
// ClearEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestClearEventJSONRoundTrip(t *testing.T) {
	original := ClearEvent{
		Event:     EventClear,
		StreamSID: "MZ-clr",
		Reason:    "interruption",
	}
	data, _ := json.Marshal(original)
	var got ClearEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Reason != original.Reason {
		t.Errorf("Reason: got %q want %q", got.Reason, original.Reason)
	}
}

// ---------------------------------------------------------------------------
// MediaQualityEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestMediaQualityEventJSONRoundTrip(t *testing.T) {
	original := MediaQualityEvent{
		Event:             EventMediaQuality,
		StreamSID:         "MZ-mq",
		Codec:             "PCMU",
		JitterMs:          4.2,
		PacketLossPct:     0.5,
		RTPGapCount:       3,
		SilenceDurationMs: 500,
		QualityScore:      85,
	}
	data, _ := json.Marshal(original)
	var got MediaQualityEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.QualityScore != original.QualityScore {
		t.Errorf("QualityScore: got %d want %d", got.QualityScore, original.QualityScore)
	}
	if got.JitterMs != original.JitterMs {
		t.Errorf("JitterMs: got %f want %f", got.JitterMs, original.JitterMs)
	}
}

// ---------------------------------------------------------------------------
// MarkEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestMarkEventJSONRoundTrip(t *testing.T) {
	original := MarkEvent{
		Event:     EventMark,
		StreamSID: "MZ-mark",
		Name:      "tts-chunk-3",
	}
	data, _ := json.Marshal(original)
	var got MarkEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != original.Name {
		t.Errorf("Name: got %q want %q", got.Name, original.Name)
	}
}

// ---------------------------------------------------------------------------
// StopEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestStopEventJSONRoundTrip(t *testing.T) {
	original := StopEvent{
		Event:       EventStop,
		StreamSID:   "MZ-stop",
		CallSID:     "CA-stop",
		Reason:      "completed",
		DurationSec: 180,
	}
	data, _ := json.Marshal(original)
	var got StopEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.DurationSec != original.DurationSec {
		t.Errorf("DurationSec: got %d want %d", got.DurationSec, original.DurationSec)
	}
	if got.Reason != original.Reason {
		t.Errorf("Reason: got %q want %q", got.Reason, original.Reason)
	}
}

// ---------------------------------------------------------------------------
// ErrorEvent JSON round-trip
// ---------------------------------------------------------------------------

func TestErrorEventJSONRoundTrip(t *testing.T) {
	original := ErrorEvent{
		Event:     EventError,
		StreamSID: "MZ-err",
		Code:      FailureCodeTimeout,
		Message:   "connection timed out",
	}
	data, _ := json.Marshal(original)
	var got ErrorEvent
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Code != original.Code {
		t.Errorf("Code: got %q want %q", got.Code, original.Code)
	}
	if got.Message != original.Message {
		t.Errorf("Message: got %q want %q", got.Message, original.Message)
	}
}

// ---------------------------------------------------------------------------
// CustomParameters JSON round-trip
// ---------------------------------------------------------------------------

func TestCustomParametersJSONRoundTrip(t *testing.T) {
	original := CustomParameters{
		"campaign_id": "camp-001",
		"language":    "hi-IN",
		"region":      "ap-south-1",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal CustomParameters: %v", err)
	}
	var got CustomParameters
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal CustomParameters: %v", err)
	}
	for k, v := range original {
		if got[k] != v {
			t.Errorf("CustomParameters[%q]: got %q want %q", k, got[k], v)
		}
	}
}

// ---------------------------------------------------------------------------
// MarshalEvent helper
// ---------------------------------------------------------------------------

func TestMarshalEvent(t *testing.T) {
	ev := StopEvent{
		Event:       EventStop,
		StreamSID:   "MZ-marshal",
		CallSID:     "CA-marshal",
		Reason:      "test",
		DurationSec: 60,
	}
	data, err := MarshalEvent(ev)
	if err != nil {
		t.Fatalf("MarshalEvent: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if m["event"] != "stop" {
		t.Errorf("event field: got %q want \"stop\"", m["event"])
	}
}
