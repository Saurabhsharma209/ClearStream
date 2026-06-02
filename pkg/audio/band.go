package audio

// BandMode describes the audio bandwidth of a session.
type BandMode uint8

const (
	BandNarrow    BandMode = iota // NB  — 8 kHz  (Indian PSTN, G.711 µ-law/A-law, G.729, GSM)
	BandWide                      // WB  — 16 kHz (G.722, Speex-WB, Opus-NB, iLBC-WB)
	BandSuperWide                 // SWB — 32 kHz (Opus-SWB, SILK)
	BandFull                      // FB  — 48 kHz (Opus-FB, AAC, MP3, WebRTC default)
)

// SampleRate returns the canonical sample rate for a BandMode.
func (b BandMode) SampleRate() int {
	switch b {
	case BandNarrow:
		return 8000
	case BandWide:
		return 16000
	case BandSuperWide:
		return 32000
	case BandFull:
		return 48000
	default:
		return 8000
	}
}

// String returns a human-readable name.
func (b BandMode) String() string {
	switch b {
	case BandNarrow:
		return "narrowband(8kHz)"
	case BandWide:
		return "wideband(16kHz)"
	case BandSuperWide:
		return "super-wideband(32kHz)"
	case BandFull:
		return "fullband(48kHz)"
	default:
		return "unknown"
	}
}

// BandFromSampleRate infers BandMode from a sample rate.
func BandFromSampleRate(hz int) BandMode {
	switch {
	case hz <= 8000:
		return BandNarrow
	case hz <= 16000:
		return BandWide
	case hz <= 32000:
		return BandSuperWide
	default:
		return BandFull
	}
}

// RTPPayloadBand maps standard RTP payload types to their BandMode.
// G.722 is special: RTP clock rate is defined as 8000 (RFC 3551 historical quirk)
// but actual audio is 16kHz wideband. We correctly return BandWide for PT=9.
var RTPPayloadBand = map[uint8]BandMode{
	0:  BandNarrow, // PCMU  — G.711 µ-law 8kHz (Indian PSTN default)
	3:  BandNarrow, // GSM   — 8kHz
	4:  BandNarrow, // G723  — 8kHz
	7:  BandNarrow, // LPC   — 8kHz
	8:  BandNarrow, // PCMA  — G.711 A-law 8kHz (Indian PSTN)
	9:  BandWide,   // G722  — 16kHz wideband (NOTE: RTP clock=8000 per RFC 3551, but audio is 16kHz)
	15: BandNarrow, // G728  — 8kHz
	18: BandNarrow, // G729  — 8kHz
	// Dynamic PTs (96-127) — resolved from SDP; common defaults below:
	96:  BandFull, // Opus typically fullband 48kHz
	97:  BandWide, // Opus narrowband / SILK-WB
	111: BandFull, // Opus (common dynamic PT used by WebRTC, FreeSWITCH)
	110: BandFull, // Opus (another common dynamic PT)
}

// BandFromRTPPayloadType returns the BandMode for a given RTP payload type.
// Falls back to BandNarrow (safe default for Indian PSTN) if unknown.
func BandFromRTPPayloadType(pt uint8) BandMode {
	if b, ok := RTPPayloadBand[pt]; ok {
		return b
	}
	return BandNarrow // conservative: treat unknown as narrowband
}

// ProcessorSampleRate is the sample rate used internally by the noise suppressor.
// RNNoise and DeepFilterNet both expect 16kHz PCM.
const ProcessorSampleRate = 16000

// NeedsUpsample reports whether audio at the given input rate needs upsampling
// before the suppressor. Returns true for narrowband (8kHz→16kHz).
func NeedsUpsample(inputHz int) bool { return inputHz < ProcessorSampleRate }

// NeedsDownsample reports whether audio at the given input rate needs downsampling
// before the suppressor. Returns true for SWB/FB.
func NeedsDownsample(inputHz int) bool { return inputHz > ProcessorSampleRate }
