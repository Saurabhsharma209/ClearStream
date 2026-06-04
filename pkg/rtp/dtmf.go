package rtp

import "fmt"

// DTMFPayloadType is the conventional RTP dynamic payload type for telephone events (RFC4733).
const DTMFPayloadType uint8 = 101

// DTMFDigit represents a decoded DTMF digit.
type DTMFDigit struct {
	Digit      string // "0"-"9", "*", "#", "A"-"D"
	DurationMs int    // event duration in ms (computed from duration field)
	Volume     int    // power level 0-63 dBm0 (0 = loudest)
	End        bool   // true on the final packet of a digit
}

// dtmfEventTable maps RFC4733 event codes to digit strings.
var dtmfEventTable = [...]string{
	"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
	"*", "#", "A", "B", "C", "D",
}

// DTMFDetector detects RFC4733 DTMF digits in RTP packets.
type DTMFDetector struct {
	sampleRate int   // needed to convert duration ticks to ms
	lastEvent  uint8 // last event code (to suppress duplicate packets for same event)
	lastEnd    bool  // whether last packet was an end packet
}

// NewDTMFDetector creates a DTMF detector for the given sample rate (typically 8000).
func NewDTMFDetector(sampleRate int) *DTMFDetector {
	if sampleRate <= 0 {
		sampleRate = 8000
	}
	return &DTMFDetector{sampleRate: sampleRate, lastEvent: 255}
}

// ParseDTMFPayload parses a 4-byte RFC4733 telephone-event payload.
// Returns nil if the payload is not valid or is a duplicate.
func (d *DTMFDetector) ParseDTMFPayload(payload []byte) (*DTMFDigit, error) {
	if len(payload) < 4 {
		return nil, fmt.Errorf("dtmf: payload too short: %d bytes (need 4)", len(payload))
	}
	eventCode := payload[0]
	end := payload[1]&0x80 != 0
	volume := int(payload[1] & 0x3F)
	duration := int(payload[2])<<8 | int(payload[3])

	// Suppress duplicate packets for the same event (RFC4733 sends 3 end packets)
	if eventCode == d.lastEvent && end == d.lastEnd {
		return nil, nil // duplicate
	}
	d.lastEvent = eventCode
	d.lastEnd = end

	if int(eventCode) >= len(dtmfEventTable) {
		return nil, fmt.Errorf("dtmf: unknown event code %d", eventCode)
	}

	durationMs := 0
	if d.sampleRate > 0 {
		durationMs = duration * 1000 / d.sampleRate
	}

	return &DTMFDigit{
		Digit:      dtmfEventTable[eventCode],
		DurationMs: durationMs,
		Volume:     volume,
		End:        end,
	}, nil
}

// Reset clears detector state (call on new call leg).
func (d *DTMFDetector) Reset() {
	d.lastEvent = 255
	d.lastEnd = false
}
