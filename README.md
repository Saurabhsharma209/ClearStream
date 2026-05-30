# ClearStream

AI-powered, codec-agnostic audio enhancement SDK in Go.  
Suppresses background noise from live RTP streams and post-processed audio/video files.

---

## Features

| Capability | Details |
|---|---|
| **Codec-agnostic** | G.711 (µ-law/A-law), G.722, G.729, Opus, AAC, MP3, FLAC, PCM |
| **File processing** | Audio files (wav, mp3, flac, ogg) and video files (mp4, mkv, mov, webm) |
| **Live RTP** | UDP interception with jitter buffer, packet loss concealment, sub-20ms latency |
| **AI backends** | RNNoise (CPU, zero-dep), DeepFilterNet (ONNX, higher quality) |
| **Passthrough mode** | Pipeline testing without a model |
| **CLI tool** | `clearstream file` and `clearstream rtp` commands |

---

## Quick Start

### Prerequisites

```bash
# FFmpeg (required for file processing and complex codec transcoding)
brew install ffmpeg          # macOS
apt-get install ffmpeg       # Ubuntu

# RNNoise (required for rnnoise backend)
brew install rnnoise         # macOS
apt-get install librnnoise-dev  # Ubuntu
```

### File Post-Processing

```go
import "github.com/exotel/clearstream"

cs, _ := clearstream.New(clearstream.DefaultConfig())
defer cs.Close()

// Works with audio files
cs.ProcessFile("noisy_call.wav", "clean_call.wav")

// Works with video files — video track passes through unchanged
cs.ProcessFile("noisy_meeting.mp4", "clean_meeting.mp4")
```

### Live RTP Interception

```go
cs, _ := clearstream.New(clearstream.DefaultConfig())

session, _ := cs.NewRTPSession(clearstream.RTPConfig{
    ListenAddr:  ":5004",          // receive RTP here
    ForwardAddr: "10.0.0.2:5004",  // send clean RTP here
})
session.Start()
// ... wait for shutdown signal ...
session.Stop()
```

### Raw PCM Pipeline

```go
// For direct integration into existing media stacks
pipe := cs.Pipeline()

// Feed 16kHz mono int16 frames
cleanFrame, err := pipe.ProcessFrames(rawPCMBytes, output)
```

---

## AI Backends

### RNNoise (default)
- Xiph.org's RNNoise — proven in production (used in Firefox, Jitsi)
- CPU only, ~1ms per 10ms frame
- Requires `librnnoise` installed

### DeepFilterNet (higher quality)
- Microsoft/Rikorose DeepFilterNet via ONNX Runtime
- Better quality on music, complex noise, and overlapping speech
- Requires ONNX Runtime + exported model:

```bash
pip install deepfilternet
python -c "
from df.enhance import init_df
model, _, _ = init_df()
model.export_onnx('deepfilter.onnx')
"
```

```go
cs, _ := clearstream.New(clearstream.Config{
    Model:     "deepfilter",
    ModelPath: "./deepfilter.onnx",
})
```

---

## Supported Codecs

| Codec | RTP PT | Live | File | Notes |
|---|---|---|---|---|
| G.711 µ-law (PCMU) | 0 | ✅ native | ✅ | Standard telephony |
| G.711 A-law (PCMA) | 8 | ✅ native | ✅ | Standard telephony |
| G.722 | 9 | ✅ via FFmpeg | ✅ | Wideband telephony |
| G.729 | 18 | ✅ via FFmpeg | ✅ | Compressed VoIP |
| Opus | 111 | ✅ via FFmpeg | ✅ | WebRTC default |
| AAC | — | — | ✅ | Video call audio |
| MP3 | — | — | ✅ | Recorded media |
| FLAC | — | — | ✅ | Lossless |
| WAV/PCM | — | ✅ native | ✅ | Raw audio |

---

## CLI

```bash
go install github.com/exotel/clearstream/cmd/clearstream@latest

# Post-process a file
clearstream file -i noisy.mp4 -o clean.mp4
clearstream file -i call.wav -o clean.wav --model deepfilter --model-path ./deepfilter.onnx

# Live RTP interception
clearstream rtp --listen :5004 --forward 10.0.0.2:5004
clearstream rtp --listen :5004 --forward 10.0.0.2:5004 --codec pcmu --model deepfilter

# Probe codec info
clearstream probe recording.mp4
```

---

## Architecture

```
┌─────────────────────────────────────────────┐
│               ClearStream SDK               │
│                                             │
│  ProcessFile()          NewRTPSession()     │
│       │                       │             │
│  pkg/file/processor    pkg/rtp/session      │
│       │                       │             │
│       └───────────┬───────────┘             │
│                   │                         │
│           pkg/audio/pipeline                │
│       (16kHz mono PCM frames)               │
│                   │                         │
│           pkg/model/Suppressor              │
│        ┌──────────┴──────────┐              │
│     RNNoise           DeepFilterNet         │
│    (CGo/C)            (ONNX Runtime)        │
└─────────────────────────────────────────────┘

pkg/audio/codec     — codec detection via ffprobe/ffmpeg
pkg/audio/resample  — sample rate conversion, mono/stereo
pkg/rtp/jitter      — jitter buffer + packet loss concealment
```

---

## Roadmap

- [ ] Echo cancellation (AEC) as a pipeline stage
- [ ] Speaker diarization (who is speaking)
- [ ] Transcription integration (Whisper)
- [ ] WebRTC data channel support
- [ ] Fine-tuned model for Indian English + call center noise profiles
- [ ] GPU inference path (CUDA via ONNX Runtime)
- [ ] gRPC API for language-agnostic SDK embedding

---

## License

Apache 2.0 — Exotel / ClearStream R&D
