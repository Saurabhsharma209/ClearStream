package agentstream

// StreamState represents the lifecycle state of an AgentStream session.
type StreamState uint8

const (
	StateCreated StreamState = iota
	StateSIPNegotiating
	StateRTPEstablished
	StateWSSConnecting
	StateStreaming
	StateBotAudioActive
	StateInterruptionDetected
	StateClearingPlayback
	StateStopping
	StateCompleted
	// Error states
	StateSIPFailed
	StateRTPTimeout
	StateWSSFailed
	StateBotTimeout
	StateModelFailed
	StateFallbackActive
	StateForceTerminated
)

// String returns a human-readable state name.
func (s StreamState) String() string {
	switch s {
	case StateCreated:
		return "CREATED"
	case StateSIPNegotiating:
		return "SIP_NEGOTIATING"
	case StateRTPEstablished:
		return "RTP_ESTABLISHED"
	case StateWSSConnecting:
		return "WSS_CONNECTING"
	case StateStreaming:
		return "STREAMING"
	case StateBotAudioActive:
		return "BOT_AUDIO_ACTIVE"
	case StateInterruptionDetected:
		return "INTERRUPTION_DETECTED"
	case StateClearingPlayback:
		return "CLEARING_PLAYBACK"
	case StateStopping:
		return "STOPPING"
	case StateCompleted:
		return "COMPLETED"
	case StateSIPFailed:
		return "SIP_FAILED"
	case StateRTPTimeout:
		return "RTP_TIMEOUT"
	case StateWSSFailed:
		return "WSS_FAILED"
	case StateBotTimeout:
		return "BOT_TIMEOUT"
	case StateModelFailed:
		return "MODEL_FAILED"
	case StateFallbackActive:
		return "FALLBACK_ACTIVE"
	case StateForceTerminated:
		return "FORCE_TERMINATED"
	default:
		return "UNKNOWN"
	}
}

// IsError returns true if the state is an error/failure state.
func (s StreamState) IsError() bool {
	return s >= StateSIPFailed
}

// IsTerminal returns true if the state is final (no further transitions).
func (s StreamState) IsTerminal() bool {
	return s == StateCompleted || s == StateForceTerminated
}

// validTransitions defines allowed state transitions.
var validTransitions = map[StreamState][]StreamState{
	StateCreated:              {StateSIPNegotiating, StateSIPFailed, StateForceTerminated},
	StateSIPNegotiating:       {StateRTPEstablished, StateSIPFailed, StateForceTerminated},
	StateRTPEstablished:       {StateWSSConnecting, StateRTPTimeout, StateForceTerminated},
	StateWSSConnecting:        {StateStreaming, StateWSSFailed, StateForceTerminated},
	StateStreaming:            {StateBotAudioActive, StateInterruptionDetected, StateStopping, StateRTPTimeout, StateWSSFailed, StateFallbackActive},
	StateBotAudioActive:       {StateInterruptionDetected, StateStreaming, StateStopping, StateWSSFailed},
	StateInterruptionDetected: {StateClearingPlayback, StateStreaming},
	StateClearingPlayback:     {StateStreaming, StateStopping},
	StateStopping:             {StateCompleted, StateForceTerminated},
	StateWSSFailed:            {StateFallbackActive, StateStopping},
	StateFallbackActive:       {StateStreaming, StateStopping},
	StateRTPTimeout:           {StateStopping},
	StateSIPFailed:            {StateForceTerminated},
	StateBotTimeout:           {StateFallbackActive, StateStopping},
	StateModelFailed:          {StateFallbackActive, StateStreaming},
}

// CanTransition returns true if transitioning from src to dst is valid.
func CanTransition(src, dst StreamState) bool {
	allowed, ok := validTransitions[src]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == dst {
			return true
		}
	}
	return false
}
