# ClearStream — Media Gateway Integration Guide

This guide covers five options for inserting ClearStream into your telephony platform media
path.  Choose the option that best fits your network topology and latency budget.

---

## Option A — SIP B2BUA Insertion (pkg/sip proxy)

ClearStream acts as a transparent SIP proxy between the upstream SIP trunk and
the your telephony platform.  It intercepts the RTP media stream, runs noise
suppression on every 10 ms frame, and forwards the clean stream to the next hop.

```
SIP Trunk ──SIP/RTP──► [ClearStream SIP Proxy :5060/:5004]
                                    │
                              (suppressed RTP)
                                    │
                                    ▼
                        your telephony platform ──► AgentStream / STT
```

### Configuration

1. Start ClearStream in SIP proxy mode:

   ```bash
   clearstream sip \
     --listen-sip  0.0.0.0:5060 \
     --listen-rtp  0.0.0.0:5004 \
     --upstream    sip-trunk.telephony.com:5060 \
     --downstream  media-gw.internal:5060
   ```

2. In your SIP trunk configuration, set ClearStream as the outbound proxy:

   ```ini
   ; asterisk/pjsip.conf example
   [trunk-clearstream]
   type=endpoint
   transport=transport-udp
   aors=trunk-clearstream-aor
   outbound_proxy=sip:clearstream.internal:5060

   [trunk-clearstream-aor]
   type=aor
   contact=sip:clearstream.internal:5060
   ```

3. For Kamailio-based trunks, add a route branch that sets the next hop:

   ```
   route[CLEARSTREAM] {
       $du = "sip:clearstream.internal:5060";
       route(RELAY);
   }
   ```

### Latency

Adds ~5–15 ms per hop depending on hardware.  Use the `rnnoise` model for the
lowest CPU footprint, or `deepfilter` for higher quality at ~3× CPU cost.

---

## Option B — RTP Mirror / Fork via rtpengine

Leave the caller's RTP path unchanged.  Use rtpengine to fork one copy of each
RTP packet to ClearStream, which suppresses noise and forwards the clean stream
to the STT engine.  The original caller stream is unaffected.

```
your telephony platform
       │
       ├── original RTP ──────────────────────► caller (unchanged)
       │
       └── forked RTP ──► [ClearStream :5004] ──► STT Engine / Recogenix
```

### rtpengine Direction Option

In your Kamailio `rtpengine_manage()` call, add a `direction` flag to fork one
leg to ClearStream:

```
# kamailio.cfg
rtpengine_manage("ICE=remove RTP-mirror=clearstream.internal:5004 codec-transcode=PCMU");
```

Or with the rtpengine `ng` control protocol (JSON):

```json
{
  "command": "offer",
  "call-id": "abc123",
  "from-tag": "tag1",
  "sdp": "...",
  "flags": ["asymmetric"],
  "direction": ["external", "internal"],
  "media address": "0.0.0.0",
  "RTP-mirror": "clearstream.internal:5004"
}
```

ClearStream listens on UDP :5004, suppresses, and re-streams clean PCMU/16 to
the STT endpoint configured with `--forward-addr`.

### Starting ClearStream for fork mode

```bash
clearstream rtp \
  --listen      0.0.0.0:5004 \
  --forward     stt-engine.internal:5005 \
  --model       rnnoise
```

---

## Option C — WSS Media Gate (WebSocket bridge)

Browser or mobile SDK connects over WSS, sending raw PCM or Opus.  ClearStream
acts as a WebSocket gateway: it receives the audio, suppresses noise, and
forwards clean frames to your telephony platform over a second WebSocket or RTP.

```
Browser / Mobile SDK
        │  WSS (Opus/PCM)
        ▼
[Nginx TLS termination]
        │  ws://
        ▼
[ClearStream :8081/stream]  ──► your telephony platform (WSS)
```

### Nginx Configuration

```nginx
# /etc/nginx/sites-available/clearstream-wss
server {
    listen 443 ssl;
    server_name audio.telephony.com;

    ssl_certificate     /etc/ssl/certs/telephony.crt;
    ssl_certificate_key /etc/ssl/private/telephony.key;

    # WebSocket upgrade for ClearStream audio gate
    location /audio-clean {
        proxy_pass          http://clearstream.internal:8081;
        proxy_http_version  1.1;
        proxy_set_header    Upgrade    $http_upgrade;
        proxy_set_header    Connection "upgrade";
        proxy_set_header    Host       $host;
        proxy_read_timeout  3600s;
        proxy_send_timeout  3600s;
    }

    # REST API (/enhance, /health, /metrics)
    location / {
        proxy_pass http://clearstream.internal:8080;
    }
}
```

### Starting ClearStream WebSocket gate

```bash
clearstream ws \
  --listen   0.0.0.0:8081 \
  --upstream wss://agentstream.telephony.com/audio \
  --model    rnnoise \
  --codec    pcm16
```

Client connects to `wss://audio.telephony.com/audio-clean` and sends 20 ms PCM
frames.  ClearStream returns clean frames on the same connection.

---

## Option D — HTTP Post-Processing (batch / recording pipeline)

The simplest integration: no topology change required.  After a call recording
is saved by the your telephony platform, POST it to ClearStream /enhance and
replace (or archive) the original file.

```
your telephony platform
         │  (stores .wav / .mp3)
         ▼
[POST /enhance]  ──► ClearStream  ──► clean file  ──► STT / QA / Archive
```

### Curl Example

```bash
curl -X POST http://clearstream.internal:8080/enhance \
     -F "audio=@/recordings/call-abc123.wav" \
     -o /recordings/call-abc123-clean.wav
```

### Webhook Integration (recording-complete callback)

Add a lightweight webhook consumer that calls ClearStream on every new recording:

```bash
#!/usr/bin/env bash
# /opt/telephony/hooks/enhance_recording.sh
RECORDING="$1"          # e.g. /recordings/call-abc123.wav
CLEAN="${RECORDING%.wav}-clean.wav"

curl -sf -X POST http://clearstream.internal:8080/enhance \
     -F "audio=@${RECORDING}" \
     -o "${CLEAN}" \
  && echo "Enhanced: ${CLEAN}" \
  || echo "Enhancement failed; using original"
```

Register this script in your recording pipeline's post-processing hook and pass
the recording path as the first argument.

### Health Check

```bash
curl http://clearstream.internal:8080/health
# {"status":"ok","model":"rnnoise","uptime_s":3600}
```

---

## Option E — Asterisk EAGI (Asterisk-based deployments)

For deployments running Asterisk (rather than a dedicated your telephony platform media gateway),
use the EAGI binary in `examples/asterisk/`.

See `examples/asterisk/README.md` and `examples/asterisk/extensions.conf` for
dialplan snippets covering real-time EAGI, record-and-replace, and full ARI
bridge patterns.

---

## Choosing an Option

| Option | Latency added | Topology change | Best for |
|--------|--------------|-----------------|----------|
| A — SIP B2BUA | 5–15 ms | SIP route change | Full duplex live calls |
| B — RTP fork | ~0 ms (fork) | rtpengine config | STT/QA without touching caller audio |
| C — WSS gate | 5–20 ms | DNS/Nginx change | Browser/SDK clients |
| D — HTTP batch | none (async) | None | Existing recording pipeline |
| E — Asterisk EAGI | 5–15 ms | Dialplan only | Asterisk deployments |

---

## Performance Tuning

- **Model**: `rnnoise` uses ~3% CPU per stream on a 2 GHz core; `deepfilter`
  uses ~15% but produces noticeably cleaner output for music-on-hold or
  low-SNR environments.
- **Concurrency**: ClearStream is safe for concurrent use.  Run multiple
  instances behind a load balancer for high call volumes.
- **Sample rate**: All options accept 8 kHz (PSTN), 16 kHz (wideband), or
  48 kHz (WebRTC) input.  ClearStream resamples internally.
