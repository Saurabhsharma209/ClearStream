# Voice AI Lab — Local WebRTC-Style Evaluation

Two-party local lab: **noisy user** (browser mic + injected noise) → **ClearStream** → **Voice AI** (Whisper + Ollama + optional TTS).

## Architecture

```
noisy_user.html --WS--> orchestrator:8090/ingest --WS--> bridge:8081/stream
                              |                              |
                              +-------- clean PCM ----------+
                              v
                         faster-whisper -> Ollama -> (TTS) -> voice_ai.html /events
```

## Prerequisites

| Tool | Purpose |
|------|---------|
| Go 1.22+ | Bridge, harnesses (`go.mod` minimum; see `.go-version`) |
| FFmpeg | File / TTS conversion |
| Python 3.10+ | Orchestrator |
| Ollama | Local LLM (`ollama pull llama3.2`) |
| Optional: `edge-tts` | TTS playback (`pip install edge-tts`) |

## Quick start

```bash
source ../setup/env.local
bash qa_up.sh
# Open http://localhost:8765/noisy_user.html and voice_ai.html
```

Bridge only (from ClearStream repo):

```bash
cd "$CLEARSTREAM_ROOT" && go run examples/bridge/main.go --http :8081 --model passthrough
```

## Evaluation matrix (A / B / C)

| Condition | Path | Purpose |
|-----------|------|---------|
| **A** | `--condition A` (bypass bridge) | Noisy audio straight to STT — baseline WER |
| **B** | `--condition B` + bridge `--model passthrough` | L0: prove pipeline + **&lt;0.5 ms** p99 latency |
| **C** | `--condition C` + bridge `--model rnnoise` | L1: SNR / WER quality (see [RNNoise build](docs/RNNOISE_BUILD.md)) |

```bash
bash eval/run_matrix.sh
```

Results land in `browser-lab/results/<timestamp>/`.

## Success criteria (DOD)

See [REPORT_TEMPLATE.md](REPORT_TEMPLATE.md) for the full Q/S/T/R/M checklist.

**Tier summary:**

- **L0 (passthrough):** p99 frame latency **&lt;0.5 ms** — `go run tools/latency_harness/main.go`
- **L1 (rnnoise):** ΔSNR ≥10 dB, WER ≥15% relative improvement — `eval/run_matrix.sh` + `wer_eval.py`
- **Reliability:** `go run tools/reliability_soak/main.go -frames 10000000`

## PCAP (all in/out paths)

Browser lab enables capture by default (`PCAP_DIR` under `voice-qa/pcap-captures`):

- `{session}-in.pcap` — audio from client (noisy mic)
- `{session}-out.pcap` — audio back to client (after ClearStream)

Analyze after a run:

```bash
make -C "$CLEARSTREAM_ROOT" pcap-analyze PCAP_DIR=~/ClearStream/voice-qa/pcap-captures
```

RTP mode: `clearstream rtp --forward ... --pcap ./pcap-captures --pcap-analyze`

## Frame size

Use **160 samples (320 bytes)** per WebSocket message = 10 ms @ 16 kHz. Matches `pkg/audio.FrameSizeSamples`.

## RNNoise (L1 quality)

Build with CGO: [docs/RNNOISE_BUILD.md](docs/RNNOISE_BUILD.md)

## Makefile targets

```bash
make latency-harness    # L0 gate
make reliability-soak   # 1M frames (use -frames 10000000 for full R3)
make eval-matrix        # A/B/C batch eval
```
