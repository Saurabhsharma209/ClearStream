// Package compat provides version detection and adaptive configuration helpers
// for integrating ClearStream with Asterisk, FreeSWITCH, Kamailio/RTPEngine,
// Janus WebRTC, and generic WSS media servers.
//
// ClearStream sits in the media path as a transparent RTP proxy or HTTP service.
// It does not call any proprietary APIs — it works purely at the RTP/PCM layer,
// so version compatibility is about codec support, transport quirks, and the
// signalling integration path (AGI, ESL, NG control protocol, REST).
package compat

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/exotel/clearstream/pkg/audio"
)

// Platform identifies the telephony platform ClearStream is integrated with.
type Platform string

const (
	PlatformAsterisk   Platform = "asterisk"
	PlatformFreeSWITCH Platform = "freeswitch"
	PlatformKamailio   Platform = "kamailio"  // usually paired with RTPEngine
	PlatformRTPEngine  Platform = "rtpengine" // Sipwise rtpengine standalone
	PlatformJanus      Platform = "janus"     // Meetecho Janus WebRTC gateway
	PlatformExotel     Platform = "exotel"    // Exotel vSIP / media gateway
	PlatformGenericWSS Platform = "wss"       // any WSS media server
	PlatformGenericRTP Platform = "rtp"       // raw RTP (no SIP signalling)
)

// Version represents a semver-style version number.
type Version struct {
	Major, Minor, Patch int
	Raw                 string
}

// ParseVersion parses "18.20.1", "1.10.12", "mr11.5.1.52" style version strings.
func ParseVersion(s string) Version {
	s = strings.TrimPrefix(s, "mr")
	parts := strings.SplitN(s, ".", 4)
	v := Version{Raw: s}
	if len(parts) > 0 {
		v.Major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		v.Minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 {
		v.Patch, _ = strconv.Atoi(parts[2])
	}
	return v
}

// GTE returns true if v >= other.
func (v Version) GTE(other Version) bool {
	if v.Major != other.Major {
		return v.Major > other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor > other.Minor
	}
	return v.Patch >= other.Patch
}

// String returns the raw version string.
func (v Version) String() string { return v.Raw }

// IntegrationProfile is returned by Recommend() — it tells callers exactly how
// to wire ClearStream for a specific platform and version.
type IntegrationProfile struct {
	Platform Platform
	Version  Version

	// SupportedCodecs lists audio codecs ClearStream can handle for this platform.
	SupportedCodecs []audio.Codec

	// PreferredCodec is the best codec to request in SDP/configuration.
	PreferredCodec audio.Codec

	// IntegrationPath is the recommended method to insert ClearStream.
	// e.g. "EAGI", "ESL socket", "NG control protocol", "HTTP REST", "RTP proxy"
	IntegrationPath string

	// SampleRate is the recommended internal processing sample rate.
	SampleRate int

	// AGCRecommended indicates whether AGC should be enabled for this platform.
	// True for mobile/WebRTC paths where level varies widely; false for PSTN trunks.
	AGCRecommended bool

	// Notes contains platform-specific integration advice.
	Notes []string

	// Warnings lists version-specific caveats to watch for.
	Warnings []string
}

// Recommend returns a filled IntegrationProfile for the given platform and version string.
// Pass an empty version string to get generic advice for the latest supported release.
func Recommend(platform Platform, versionStr string) (*IntegrationProfile, error) {
	v := ParseVersion(versionStr)
	p := &IntegrationProfile{Platform: platform, Version: v}

	switch platform {

	case PlatformAsterisk:
		return recommendAsterisk(p, v)

	case PlatformFreeSWITCH:
		return recommendFreeSWITCH(p, v)

	case PlatformKamailio, PlatformRTPEngine:
		return recommendKamailio(p, v)

	case PlatformJanus:
		return recommendJanus(p, v)

	case PlatformExotel:
		return recommendExotel(p, v)

	case PlatformGenericWSS:
		return recommendWSS(p, v)

	case PlatformGenericRTP:
		return recommendGenericRTP(p, v)

	default:
		return nil, fmt.Errorf("compat: unknown platform %q", platform)
	}
}

// ---- Asterisk ---------------------------------------------------------------

// AsteriskMinSupported is the oldest Asterisk version ClearStream supports.
// Versions before 16 lack EAGI and modern AMI event filtering.
var AsteriskMinSupported = ParseVersion("16.0.0")

// AsteriskRecommended is the recommended Asterisk version (current LTS as of 2026).
var AsteriskRecommended = ParseVersion("20.19.0")

func recommendAsterisk(p *IntegrationProfile, v Version) (*IntegrationProfile, error) {
	p.SupportedCodecs = []audio.Codec{audio.CodecG711U, audio.CodecG711A, audio.CodecG722}
	p.PreferredCodec = audio.CodecG711A // A-law preferred on Asterisk for PSTN compatibility
	p.SampleRate = 16000
	p.AGCRecommended = false

	switch {
	case v.Major == 0: // no version given — latest advice
		p.IntegrationPath = "EAGI (Enhanced AGI) script or ARI media WebSocket"
		p.Notes = []string{
			"Recommended: Asterisk 20 LTS (full support until Oct 2026) or Asterisk 22+.",
			"Use EAGI: set EAGI=yes in channel config, pipe audio stdin→ClearStream→stdout.",
			"Alternative: ARI (Asterisk REST Interface) bridge — stream media over WebSocket to ClearStream bridge.",
			"Set audiohooks or JACK for real-time path; EAGI adds ~10ms latency.",
			"Codec: PCMA (G.711 A-law) preferred for PSTN trunks; G.722 for HD voice paths.",
		}

	case v.GTE(ParseVersion("20.0.0")):
		p.IntegrationPath = "EAGI or ARI media WebSocket (both recommended)"
		p.Notes = []string{
			"Asterisk 20 LTS — fully supported. Use EAGI for simple dial plan injection.",
			"ARI bridge gives per-channel WebSocket media: bridge.Handler() on ws://host:8081/stream.",
			"Enable native RTP bridge: 'directmedia=no' in sip.conf/pjsip.conf to force media through Asterisk.",
			"AGI path: agi(clearstream-agi.sh) in dialplan, reads PCM via EAGI stdin/stdout.",
		}

	case v.GTE(ParseVersion("18.0.0")):
		p.IntegrationPath = "EAGI or ARI media WebSocket"
		p.Notes = []string{
			"Asterisk 18 LTS — supported. Same as 20 LTS integration path.",
			"Ensure 'res_ari_channels' module is loaded for ARI media streaming.",
		}

	case v.GTE(ParseVersion("16.0.0")):
		p.IntegrationPath = "EAGI only (ARI media streaming limited)"
		p.Notes = []string{
			"Asterisk 16 LTS — minimal support. EAGI works but ARI media WebSocket is limited.",
			"Upgrade to 18 or 20 LTS for best integration.",
		}
		p.Warnings = []string{
			"Asterisk 16 reached EOL. Upgrade recommended before using in production.",
		}

	default:
		return nil, fmt.Errorf("compat: Asterisk %s is below minimum supported version (%s)", v, AsteriskMinSupported)
	}

	return p, nil
}

// ---- FreeSWITCH -------------------------------------------------------------

// FreeSWITCHMinSupported is the oldest FreeSWITCH version ClearStream supports.
var FreeSWITCHMinSupported = ParseVersion("1.10.0")

// FreeSWITCHRecommended is the current recommended version (May 2026 release).
var FreeSWITCHRecommended = ParseVersion("20.26.2")

func recommendFreeSWITCH(p *IntegrationProfile, v Version) (*IntegrationProfile, error) {
	p.SupportedCodecs = []audio.Codec{audio.CodecG711U, audio.CodecG711A, audio.CodecG722, audio.CodecOpus}
	p.PreferredCodec = audio.CodecG711U
	p.SampleRate = 16000
	p.AGCRecommended = false

	switch {
	case v.Major == 0: // no version given
		p.IntegrationPath = "mod_spandsp ESL socket or mod_audio_stream (HTTP WebSocket)"
		p.Notes = []string{
			"Recommended: FreeSWITCH 20.26.x (SignalWire Stack, May 2026) or 1.10.x (community).",
			"Best path: mod_audio_stream — streams audio to ClearStream WebSocket bridge in real time.",
			"Alternative: ESL (Event Socket Library) — use esl.SetInputCallback to intercept audio frames.",
			"Use 'set media_processing_mode=bypass' off and force media through FreeSWITCH.",
			"Opus (48kHz) is supported via ClearStream's FFmpeg resample pipeline.",
		}

	case v.GTE(ParseVersion("20.0.0")): // SignalWire Stack
		p.IntegrationPath = "mod_audio_stream WebSocket or ESL"
		p.Notes = []string{
			"SignalWire Stack (20.x) — recommended. Full mod_audio_stream support.",
			"Configure: <action application=\"audio_stream\" data=\"ws://clearstream-host:8081/stream\"/>",
			"SRTP: supported. Pass SRTP key material through ESL if needed.",
			"Streaming TTS format support in mod_shout (Mistral MP3, NDJSON) pairs well with ClearStream post-processing.",
		}

	case v.GTE(ParseVersion("1.10.0")):
		p.IntegrationPath = "mod_audio_stream WebSocket or ESL"
		p.Notes = []string{
			"FreeSWITCH 1.10.x community — fully supported.",
			"mod_audio_stream WebSocket bridge is the cleanest path.",
			"Ensure 'bypass_media=false' so media flows through FreeSWITCH to ClearStream.",
		}

	default:
		return nil, fmt.Errorf("compat: FreeSWITCH %s is below minimum supported version (%s)", v, FreeSWITCHMinSupported)
	}

	return p, nil
}

// ---- Kamailio + RTPEngine ---------------------------------------------------

// RTPEngineMinSupported is the oldest rtpengine version ClearStream supports.
var RTPEngineMinSupported = ParseVersion("11.0.0")

// RTPEngineRecommended is the current recommended version (May 2026).
var RTPEngineRecommended = ParseVersion("11.5.1")

func recommendKamailio(p *IntegrationProfile, v Version) (*IntegrationProfile, error) {
	p.SupportedCodecs = []audio.Codec{audio.CodecG711U, audio.CodecG711A, audio.CodecG722, audio.CodecOpus}
	p.PreferredCodec = audio.CodecG711A
	p.SampleRate = 16000
	p.AGCRecommended = false
	p.IntegrationPath = "RTPEngine NG control protocol (UDP JSON)"

	switch {
	case v.Major == 0:
		p.Notes = []string{
			"Recommended: rtpengine 11.5.x LTS (May 2026) + Kamailio 6.0.x.",
			"Insert ClearStream as a second RTP proxy in the media path using rtpengine's 'to' directive.",
			"ClearStream listens on :5004, forwards clean audio to the real destination.",
			"rtpengine NG protocol: send 'offer'/'answer' with 'media-address' pointing to ClearStream.",
			"Kamailio config: rtpengine_manage(\"replace-origin replace-session-connection\");",
			"SRTP: ClearStream decrypts SRTP→PCM, processes, re-encrypts. Pass SRTP keys via NG protocol 'SDES-tag'.",
		}

	case v.GTE(ParseVersion("11.0.0")):
		p.Notes = []string{
			"rtpengine 11.x — fully supported. LTS (v11.5.x) recommended.",
			"Use NG control protocol over UDP to redirect media through ClearStream.",
			"Thread-safe: ClearStream's rtp.Session is safe for concurrent use.",
		}

	default:
		return nil, fmt.Errorf("compat: rtpengine %s is below minimum supported version (%s)", v, RTPEngineMinSupported)
	}

	return p, nil
}

// ---- Janus WebRTC -----------------------------------------------------------

func recommendJanus(p *IntegrationProfile, v Version) (*IntegrationProfile, error) {
	p.SupportedCodecs = []audio.Codec{audio.CodecOpus}
	p.PreferredCodec = audio.CodecOpus
	p.SampleRate = 48000    // Opus native rate; ClearStream resamples 48k→16k→48k
	p.AGCRecommended = true // WebRTC paths have widely variable mic levels
	p.IntegrationPath = "Janus AudioBridge plugin WebSocket or RTP forwarder"
	p.Notes = []string{
		"Recommended: Janus multistream (current, 2026) — legacy 0.x also supported.",
		"Two integration paths:",
		"  1. RTP forwarder: use janus.plugin.audiobridge 'rtp_forward' to send audio to ClearStream UDP port, receive clean audio back on a second port.",
		"  2. WebSocket: connect ClearStream WebSocket bridge (ws://host:8081/stream) directly from Janus lua/python plugin.",
		"Opus (48kHz): ClearStream resamples to 16kHz for AI processing, resamples back to 48kHz for Janus.",
		"AGC recommended: browser mic levels vary widely; set TargetRMS=3000, MaxGain=4.0.",
		"SRTP: Janus handles DTLS/SRTP internally; ClearStream receives plain RTP from the RTP forwarder.",
	}
	return p, nil
}

// ---- Exotel -----------------------------------------------------------------

func recommendExotel(p *IntegrationProfile, v Version) (*IntegrationProfile, error) {
	p.SupportedCodecs = []audio.Codec{audio.CodecG711A, audio.CodecG711U}
	p.PreferredCodec = audio.CodecG711A // Exotel prefers A-law for PSTN
	p.SampleRate = 16000
	p.AGCRecommended = false
	p.IntegrationPath = "RTP proxy (transparent, between Exotel media IP and AgentStream)"
	p.Notes = []string{
		"Exotel vSIP media IP range: RTP ports 10000–20000 (2 ports per call: RTP + RTCP).",
		"Preferred codec: PCMA (G.711 A-law) for PSTN trunk compatibility.",
		"ClearStream listens on :5004, forwards clean audio to AgentStream STT endpoint.",
		"AgentStream connector: use examples/exotel_integration/agentstream_connector.go.",
		"HTTP API path: POST /enhance with agc=false (PSTN levels are stable).",
		"ECC integration: see examples/ecc_integration/main.go — starts SIP proxy on :8081.",
		"RTCP: ClearStream parses Receiver Reports from Exotel media servers for loss/jitter monitoring.",
		"SIP Call-ID: pass via POST /sip/session/start JSON body for per-call session tracking.",
	}
	return p, nil
}

// ---- Generic WSS ------------------------------------------------------------

func recommendWSS(p *IntegrationProfile, v Version) (*IntegrationProfile, error) {
	p.SupportedCodecs = []audio.Codec{audio.CodecG711U, audio.CodecG711A, audio.CodecOpus}
	p.PreferredCodec = audio.CodecOpus
	p.SampleRate = 16000
	p.AGCRecommended = true
	p.IntegrationPath = "WebSocket bridge (pkg/websocket) — binary PCM messages"
	p.Notes = []string{
		"Protocol: binary WebSocket messages, 16kHz mono signed 16-bit PCM (little-endian).",
		"Mount: http.Handle(\"/stream\", bridge.Handler()) — compatible with any WSS server.",
		"Nginx proxy: proxy_pass with Upgrade/Connection headers for WSS termination.",
		"Browser/SDK: send mic audio as binary frames, receive enhanced audio in response.",
		"Max frame: 65536 bytes (~2s audio) configurable via BridgeConfig.MaxFrameBytes.",
		"AGC enabled by default for browser paths (mic level varies widely).",
	}
	return p, nil
}

// ---- Generic RTP ------------------------------------------------------------

func recommendGenericRTP(p *IntegrationProfile, v Version) (*IntegrationProfile, error) {
	p.SupportedCodecs = []audio.Codec{audio.CodecG711U, audio.CodecG711A, audio.CodecG722}
	p.PreferredCodec = audio.CodecG711U
	p.SampleRate = 16000
	p.AGCRecommended = false
	p.IntegrationPath = "Direct UDP RTP proxy (rtp.Session)"
	p.Notes = []string{
		"Any RTP source: point your SBC/media gateway RTP stream at ClearStream :5004.",
		"ClearStream auto-detects codec from RTP payload type (0=PCMU, 8=PCMA, 9=G722).",
		"RTCP Receiver Reports parsed on port+1 for quality monitoring.",
		"Jitter buffer: 4 frames default (~40ms); tune via rtp.Config.JitterDepth.",
		"PLC: fade-to-silence (0.9x decay per lost frame) for network resilience.",
	}
	return p, nil
}

// Summary returns a human-readable one-liner for the profile.
func (p *IntegrationProfile) Summary() string {
	codecs := make([]string, len(p.SupportedCodecs))
	for i, c := range p.SupportedCodecs {
		codecs[i] = string(c)
	}
	return fmt.Sprintf("[%s %s] path=%s preferred=%s agc=%v codecs=[%s]",
		p.Platform, p.Version, p.IntegrationPath, p.PreferredCodec, p.AGCRecommended,
		strings.Join(codecs, ","))
}
