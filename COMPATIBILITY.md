# ClearStream Compatibility Matrix

ClearStream is a **transparent RTP/PCM media processing layer**. It does not
call any proprietary PBX APIs — it sits in the media path and works with any
platform that can send/receive RTP UDP packets or WebSocket binary PCM frames.

Compatibility is therefore about:
1. **Codec support** (which codecs the platform sends and ClearStream handles)
2. **Integration path** (how you insert ClearStream into the signalling/media flow)
3. **Version-specific caveats** (API changes, module availability, known bugs)

---

## Asterisk

| Version | Support | Integration Path | Notes |
|---------|---------|-----------------|-------|
| **20.x LTS** ✅ recommended | Full | EAGI + ARI media WebSocket | Active LTS, full support until Oct 2026 |
| **22.x** ✅ current | Full | EAGI + ARI media WebSocket | Current standard release (22.9.0, Apr 2026) |
| **23.x** ✅ current | Full | EAGI + ARI media WebSocket | Latest standard release (23.3.0, Apr 2026) |
| **18.x LTS** ✅ supported | Full | EAGI + ARI media WebSocket | EOL — upgrade recommended |
| **16.x LTS** ⚠️ minimal | EAGI only | EAGI | EOL, no ARI media stream support |
| **< 16** ❌ | Not supported | — | No EAGI support |

**Preferred codec:** PCMA (G.711 A-law) for PSTN; G.722 for HD voice  
**Sample rate:** 8kHz wire → ClearStream resamples to 16kHz → back to 8kHz  
**AGC:** Off by default for PSTN paths (stable levels); enable for conference rooms

### EAGI Integration (simplest)
```ini
; extensions.conf
exten => _X.,1,Answer()
exten => _X.,n,AGI(clearstream-agi,${CALLERID(num)}.wav)
exten => _X.,n,Hangup()
```
```bash
# Build the EAGI binary:
go build -o /var/lib/asterisk/agi-bin/clearstream-agi \
  examples/asterisk/agi/main.go
```

### ARI Bridge (real-time, per-channel)
```bash
go run examples/asterisk/ari_bridge/main.go
# Connects to Asterisk ARI, creates bridge, streams audio via WebSocket
```

### directmedia config (required)
```ini
; pjsip.conf or sip.conf — force media through Asterisk (not direct to endpoint)
directmedia=no
```

---

## FreeSWITCH

| Version | Support | Integration Path | Notes |
|---------|---------|-----------------|-------|
| **20.26.x** ✅ recommended | Full | mod_audio_stream WebSocket | SignalWire Stack, May 2026 |
| **1.10.x** ✅ current | Full | mod_audio_stream WebSocket | Community release, 1.10.12+ |
| **1.8.x** ⚠️ limited | ESL only | Event Socket Library | mod_audio_stream not available |
| **< 1.8** ❌ | Not supported | — | — |

**Preferred codec:** PCMU (G.711 µ-law); Opus supported via FFmpeg resample  
**Sample rate:** 8kHz or 48kHz (Opus) → ClearStream normalises to 16kHz  
**AGC:** Off for PSTN; On for WebRTC/browser paths

### mod_audio_stream Integration (recommended)
```xml
<!-- dialplan: stream audio to ClearStream WebSocket bridge -->
<action application="audio_stream"
        data="ws://clearstream-host:8081/stream"/>
```

### ESL Integration
```go
// Go ESL client — intercept audio frames via Event Socket
conn.SetInputCallback(func(frame []byte) []byte {
    enhanced, _ := client.EnhanceAudio(ctx, frame)
    return enhanced
})
```

### Key config
```xml
<!-- ensure media flows through FreeSWITCH, not peer-to-peer -->
<param name="bypass-media" value="false"/>
```

---

## Kamailio + RTPEngine

| Version | Support | Integration Path | Notes |
|---------|---------|-----------------|-------|
| **rtpengine 11.5.x LTS** ✅ recommended | Full | NG control protocol | May 2026, current LTS |
| **rtpengine 11.x** ✅ | Full | NG control protocol | — |
| **Kamailio 6.0.x** ✅ recommended | Full | rtpengine module | Current stable |
| **Kamailio 5.7.x / 5.8.x** ✅ | Full | rtpengine module | — |
| **rtpengine < 11** ⚠️ | Limited | NG protocol (older schema) | JSON fields may differ |

**Integration:** ClearStream acts as a second RTP hop in the rtpengine chain.

```
Caller → Kamailio SDP rewrite → ClearStream :5004 → clean audio → Callee
```

### Kamailio config snippet
```
# route traffic through ClearStream before the real destination
rtpengine_manage("replace-origin replace-session-connection
                  media-address=<clearstream-ip>
                  RTP/AVP");
```

### rtpengine NG protocol
ClearStream's `pkg/sip/proxy.go` exposes HTTP control; pair it with rtpengine's
`--listen-ng` for automated session management:
```bash
# Start ClearStream SIP proxy:
go run examples/ecc_integration/main.go --sip :8081 --http :8080
```

---

## Janus WebRTC Gateway

| Version | Support | Integration Path | Notes |
|---------|---------|-----------------|-------|
| **Janus multistream** ✅ recommended | Full | AudioBridge RTP forwarder or WebSocket plugin | Current (2026) |
| **Janus 0.x legacy** ✅ | Full | RTP forwarder | Still supported |

**Preferred codec:** Opus (48kHz native)  
**Resampling:** ClearStream 48kHz → 16kHz AI processing → 48kHz back  
**AGC:** **On** — browser mic levels vary widely (use `DefaultAGCConfig()`)

### AudioBridge RTP forwarder path
```json
// Janus AudioBridge: forward room audio to ClearStream
{
  "request": "rtp_forward",
  "room": 1234,
  "host": "clearstream-host",
  "port": 5004,
  "codec": "opus",
  "ptype": 111
}
```
ClearStream receives the RTP, enhances it, forwards clean audio back to Janus
on a return port you configure in `rtp.Config.ForwardAddr`.

### WebSocket plugin path
```javascript
// Janus JS SDK: pipe audio through ClearStream WS bridge
const cs = new WebSocket('wss://clearstream-host/stream');
// send mic audio as binary PCM, receive enhanced PCM back
```

---

## Cloud Telephony / vSIP Infrastructure

| Component | Supported | Notes |
|-----------|-----------|-------|
| **vSIP / Virtual SIP Trunking** ✅ | Full | Transparent RTP proxy between carrier and agent endpoint |
| **ECC (Contact Centre)** ✅ | Full | SIP proxy + Prometheus metrics for ops visibility |
| **AgentStream (STT pipeline)** ✅ | Full | HTTP `/enhance` or Go client `EnhanceAudio()` |
| **cloud telephony WebRTC SDK** ✅ | Full | WebSocket bridge (WS binary PCM) |
| **Kamailio/Obelix** ✅ | Full | RTP proxy path via SIP SDP auto-detection |

**Preferred codec:** PCMA (G.711 A-law) — cloud telephony PSTN trunks prefer A-law  
**RTP ports:** 10000–20000 (cloud telephony media range); ClearStream listens on 5004 by default  
**AGC:** Off — cloud telephony PSTN levels are normalised at the carrier

### Quick start with cloud telephony vSIP
```bash
# Start ClearStream as a transparent RTP proxy:
go run cmd/clearstream/main.go rtp \
  --listen :5004 \
  --forward <agentstream-rtp-ip>:<port> \
  --codec PCMA

# Or use the ECC integration example:
go run examples/ecc_integration/main.go
```

---

## Generic WSS / WebSocket Media Servers

Any WSS media server (Twilio Media Streams, Amazon Chime SDK, custom Golang/Node
WSS servers, etc.) can plug into ClearStream's WebSocket bridge.

**Protocol:** Binary WebSocket messages — 16kHz mono signed 16-bit PCM little-endian  
**Max message size:** 65536 bytes (~2s audio), configurable  
**AGC:** On by default (browser/mobile mic level varies)

```
Client → binary PCM → ws://clearstream-host/stream → enhanced PCM → Client
```

Nginx reverse proxy for WSS termination:
```nginx
location /stream {
    proxy_pass http://clearstream:8081;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
}
```

---

## Codec Support Summary

| Codec | Wire Rate | ClearStream Processing | Re-encode | Notes |
|-------|-----------|----------------------|-----------|-------|
| **PCMU** (G.711 µ-law) | 8kHz | 16kHz | PCMU | RTP PT 0 — auto-detected |
| **PCMA** (G.711 A-law) | 8kHz | 16kHz | PCMA | RTP PT 8 — cloud telephony preferred |
| **G.722** | 16kHz (wideband) | 16kHz native | G.722 | RTP PT 9 |
| **G.729** | 8kHz | 16kHz via FFmpeg | G.729 | Requires FFmpeg g729 decoder |
| **Opus** | 8/16/48kHz | 16kHz via FFmpeg resample | Opus | WebRTC standard |
| **PCM s16le** | any | direct | PCM | Used for file processing and streaming |

---

## AGC Configuration by Platform

| Platform | AGC Default | Recommended TargetRMS | MaxGain | Reason |
|----------|-------------|----------------------|---------|--------|
| cloud telephony PSTN trunk | Off | — | — | Carrier normalises levels |
| Asterisk PSTN | Off | — | — | Stable PSTN levels |
| FreeSWITCH PSTN | Off | — | — | Stable |
| FreeSWITCH WebRTC | On | 3000 | 4.0 | Browser mic varies |
| Janus WebRTC | **On** | 3000 | 4.0 | Browser mic varies widely |
| cloud telephony WebRTC SDK | **On** | 2500 | 3.0 | Mobile mic has high variance |
| Generic WSS | **On** | 3000 | 4.0 | Unknown input source |
| Asterisk conference | On | 2000 | 3.0 | Multiple speakers, mixed levels |

---

## Using the `compat` Package

```go
import "github.com/exotel/clearstream/pkg/compat"

// Get integration advice for your platform + version:
profile, err := compat.Recommend(compat.PlatformAsterisk, "20.19.0")
if err != nil {
    log.Fatal(err)
}

fmt.Println(profile.IntegrationPath)  // "EAGI or ARI media WebSocket"
fmt.Println(profile.PreferredCodec)   // "pcma"
fmt.Println(profile.AGCRecommended)   // false

for _, note := range profile.Notes {
    fmt.Println(" •", note)
}

// Use the profile to configure your RTP session:
agcCfg := audio.DefaultAGCConfig()
session, _ := cs.NewRTPSession(rtp.Config{
    ListenAddr:  ":5004",
    ForwardAddr: "agentstream:5005",
    Codec:       profile.PreferredCodec,
    AGC:         func() *audio.AGCConfig {
        if profile.AGCRecommended { return &agcCfg }
        return nil
    }(),
})
```

---

## Minimum System Requirements

| Dependency | Required | Notes |
|------------|----------|-------|
| Go 1.21+ | Yes | 1.22 recommended (CI matrix) |
| FFmpeg 4.x+ | Yes | For file processing and G.729/Opus codec paths |
| FFmpeg 6.x+ | Recommended | Better Opus quality, improved G.722 |
| librnnoise | Optional | Only needed with `-tags rnnoise`; apt may not have `librnnoise-dev` on Ubuntu 24.04 — build from source if needed |
| ONNX Runtime | Optional | Only needed with `-tags onnx` (DeepFilterNet) |
