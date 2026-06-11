# ClearStream — AI Audio Enhancement SDK

> Codec-agnostic, real-time noise suppression for telephony. Drop-in RTP proxy or HTTP service. No proprietary APIs.

## What it does

ClearStream suppresses background noise from live voice calls and recorded audio using AI-based noise suppression (RNNoise / DeepFilter). It works as a transparent RTP proxy sitting in the media path between a carrier (Asterisk, FreeSWITCH, Kamailio) and an agent endpoint, or as an HTTP batch processor for cleaning up recorded files. Supported codecs: G.711 µ-law (PCMU), G.711 A-law (PCMA), G.722, and Opus. Compatible with Asterisk, FreeSWITCH, Kamailio, Janus, and any SIP/RTP infrastructure.

---

## Quick Start

### 1. Build

```bash
git clone https://github.com/Saurabhsharma209/ClearStream
cd ClearStream
go build ./cmd/clearstream/
```

### 2. File processing

```bash
# Single file (audio or video — mp3, wav, flac, ogg, mp4, mkv, mov, …)
./clearstream file -i noisy.wav -o clean.wav

# Probe a file (show codec info without processing)
./clearstream probe recording.mp4

# Batch-process a directory
./clearstream dir -i ./recordings/ -o ./clean/ --workers 8
```

### 3. Live RTP

```bash
./clearstream rtp --listen :5004 --forward AGENT_IP:5004 --codec pcmu
```

```
Caller ──► SBC/Carrier ──► ClearStream :5004 ──► (suppress) ──► Agent :5004
                           │
                     UDP RTP packets
                     G.711 / Opus / G.722
```

### 4. HTTP API server

```bash
./clearstream server --http :8080

# Enhance a file via curl
curl -F audio=@noisy.wav http://localhost:8080/enhance -o clean.wav

# Health check
curl http://localhost:8080/health
```

### 5. Docker / POC demo

```bash
make poc
```

Or run the full demo script:

```bash
bash demo/poc_demo.sh
```

---

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/enhance` | POST | Upload audio/video file (`audio` form field), get noise-suppressed audio back. Max 100 MB. |
| `/health` | GET | JSON: `status`, `version`, `model`, `uptime_sec` |
| `/info` | GET | SDK capabilities: codecs, frame size, pool size, endpoint map |
| `/metrics` | GET | JSON counters: requests total/ok/failed, avg processing ms |
| `/metrics/prometheus` | GET | Prometheus metrics (histograms + counters) |

**POST /enhance query parameters (optional):**

| Parameter | Value | Effect |
|-----------|-------|--------|
| `audio_only` | `true` | Strip video track from output |
| `normalize_peak` | `true` | Peak-normalize output level |
| `agc` | `true` | Enable Automatic Gain Control |
| `agc_target_rms` | float | AGC target RMS (default from `DefaultAGCConfig()`) |
| `agc_max_gain` | float | AGC max gain multiplier |
| `agc_attack_ms` | float | AGC attack time in ms |
| `agc_release_ms` | float | AGC release time in ms |

Response headers include `X-Processing-Ms` and `X-ClearStream-Model`.

---

## Go SDK

```go
import "github.com/exotel/clearstream"

cs, err := clearstream.New(clearstream.Config{
    Model:                 "passthrough", // "rnnoise" (default) or "deepfilter"
    EnableVAD:             true,          // skip suppression on silence (~30% CPU saving)
    MaxConcurrentSessions: 32,
    SampleRate:            16000,
    Channels:              1,
    FFmpegPath:            "ffmpeg",
})
if err != nil {
    log.Fatal(err)
}
defer cs.Close()

// --- File processing ---
err = cs.ProcessFile("noisy.wav", "clean.wav")

// With options (AGC, peak normalize, audio-only strip)
agcCfg := audio.DefaultAGCConfig()
err = cs.ProcessFileWithOptions("noisy.mp4", "clean.mp4", file.Options{
    AudioOnly:     false,
    NormalizePeak: true,
    AGC:           &agcCfg,
})

// --- Live RTP session ---
sess, err := cs.NewRTPSession(rtp.Config{
    ListenAddr:  ":5004",
    ForwardAddr: "agent:5004",
    Codec:       audio.CodecG711A, // PCMA
    PayloadType: 8,
    JitterDepth: 4,               // ~40ms jitter buffer
    OnStats: func(s rtp.Stats) {
        fmt.Printf("rx=%d tx=%d lost=%d latency=%.1fms\n",
            s.PacketsReceived, s.PacketsSent, s.PacketsLost, s.LatencyAvgMs)
    },
})
sess.Start()
defer sess.Stop()

// --- HTTP handler (embed in your own server) ---
http.Handle("/", cs.NewHTTPHandler())

// --- Raw pipeline (advanced: feed PCM frames directly) ---
pipeline := cs.Pipeline()
stats := cs.PipelineStats()
_ = pipeline
_ = stats
```

---

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Model` | string | `"rnnoise"` | Suppressor backend: `"rnnoise"`, `"deepfilter"`, `"passthrough"` |
| `ModelPath` | string | `""` | Path to ONNX model file — required when `Model = "deepfilter"` |
| `SampleRate` | int | `16000` | Internal processing sample rate (8000–48000 Hz) |
| `Channels` | int | `1` | Number of audio channels (1 or 2) |
| `FFmpegPath` | string | `"ffmpeg"` | Path to ffmpeg binary |
| `MaxConcurrentSessions` | int | `32` | Suppressor pool size — limits parallel RTP sessions |
| `EnableVAD` | bool | `false` | Voice Activity Detection — skip suppression on silence |
| `AdaptiveVAD` | bool | `false` | Adaptive noise floor calibration (requires `EnableVAD=true`) |
| `VADThreshold` | float64 | `300` | RMS energy threshold for static VAD (16-bit telephony PCM) |
| `EnableAGC` | bool | `false` | Automatic Gain Control for all sessions |
| `AGC` | `*audio.AGCConfig` | `nil` | Fine-grained AGC settings (falls back to `DefaultAGCConfig()`) |
| `Logger` | `*zap.Logger` | production | Optional zap logger |

Use `clearstream.DefaultConfig()` for sensible defaults, then override as needed. Call `cfg.Validate()` before `New()` to get clear error messages.

---

## Signal Processing Pipeline

```
RTP/File ──► Decode (G.711/Opus/G.722)
          ──► Resample (8kHz → 16kHz, Kaiser FIR)
          ──► VAD (skip suppression on silence frames)
          ──► Noise Suppressor (RNNoise / DeepFilter / passthrough)
          ──► AGC (normalize output level)
          ──► Resample (16kHz → 8kHz)
          ──► Encode ──► Output (RTP forward / file write)
```

Frame size: 160 samples (20ms at 8kHz / 10ms at 16kHz).

---

## Suppressor Backends

| Backend | Quality | Dependencies | When to use |
|---------|---------|--------------|-------------|
| `passthrough` | None (audio unmodified) | None | Testing, benchmarking, development |
| `rnnoise` | Good (~15dB SNR improvement) | None (pure Go or CGO) | Default for production telephony |
| `deepfilter` | Excellent (~25dB SNR improvement) | ONNX Runtime + model file | High-quality requirements |

Set `CLEARSTREAM_MODEL` environment variable to override model at runtime (useful for Docker).

---

## Cloud Telephony / vSIP Integration

### Transparent RTP proxy (recommended)

```bash
./clearstream rtp --listen :5004 --forward AGENT_IP:5004 --codec pcma
# Also start HTTP server:
./clearstream server --http :8080
```

Your carrier sends RTP (G.711 A-law or µ-law) to ClearStream on `:5004`. ClearStream suppresses noise and forwards clean packets to the agent endpoint. The HTTP server on `:8080` exposes `/health`, `/enhance`, and Prometheus metrics.

**Preferred codec:** PCMA (G.711 A-law) — Most PSTN trunks prefer A-law (PT 8). Switch to PCMU (PT 0) if your trunk uses µ-law.

**RTP port range:** Configure to match your carrier (e.g. 10000–20000); ClearStream listens on `:5004` by default.

**AGC:** Disabled by default on PSTN paths — levels are typically normalized at the carrier.

**VAD:** Enabled by default in the POC — saves ~30% CPU on typical call silence.

### AgentStream / STT pipeline

Point AgentStream's pre-STT step at `POST /enhance`. The handler accepts any audio format, denoises it, and returns the same format. Headers `X-Processing-Ms` and `X-ClearStream-Model` are included in every response.

---

## Platform Compatibility

| Platform | Status | Integration Path |
|----------|--------|-----------------|
| **Cloud Telephony vSIP / Contact Center** | Full | Transparent RTP proxy; HTTP `/enhance` for STT pipeline |
| **WebRTC SDK / Kamailio / RTPEngine** | Full | WebSocket bridge (binary PCM); RTP proxy via SIP SDP |
| **Asterisk 20.x LTS / 22.x / 23.x** | Full | EAGI + ARI media WebSocket |
| **Asterisk 18.x LTS** | Full (EOL — upgrade recommended) | EAGI + ARI media WebSocket |
| **FreeSWITCH 20.26.x / 1.10.x** | Full | `mod_audio_stream` WebSocket |
| **Kamailio 6.0.x + RTPEngine 11.5.x LTS** | Full | NG control protocol |
| **Janus WebRTC Gateway** | Full | AudioBridge RTP forwarder or WebSocket plugin |

See [COMPATIBILITY.md](COMPATIBILITY.md) for version-specific caveats and configuration snippets.

---

## Development

```bash
make test        # all tests with race detector
make bench       # benchmarks
make loadtest    # 50-session concurrent load test
make fmt         # gofmt + goimports

# Generate test audio and measure SNR improvement
go run tools/gen_test_audio/main.go
go run tools/snr_benchmark/main.go

# SNR benchmark against a live server
go run tools/snr_benchmark/main.go --server http://localhost:8080
```

**Build tags:**

```bash
# Pure Go (default) — passthrough suppressor
go build ./cmd/clearstream/

# With RNNoise (CGO)
go build -tags rnnoise ./cmd/clearstream/

# With DeepFilter (ONNX Runtime)
go build -tags onnx ./cmd/clearstream/
```

---

## Version

```bash
./clearstream version
# clearstream v0.1.0 (ClearStream Audio Enhancement SDK)
```

---

## License

MIT
