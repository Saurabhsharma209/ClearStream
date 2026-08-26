# Repository Layout

A map of the top-level directories, for anyone new to the codebase. The
"one-line purpose" column is the thing to remember; everything else is
detail.

## Core SDK (what ships)

| Directory | Purpose |
|---|---|
| `clearstream.go` (root) + `clearstream_*_test.go` | The public Go SDK surface: `New()`, `Config`, `ProcessFile*`, `ProcessDir*`, `NewRTPSession`, `NewHTTPHandler`. Idiomatic Go puts a package's own tests next to it, which is why these test files sit at repo root alongside `clearstream.go` rather than in `pkg/`. |
| `pkg/audio` | The frame-processing pipeline: codec detection, resampling, VAD, AGC, AEC, noise-reduction gating, diarization, turn-end detection. |
| `pkg/model` | Suppressor backends (RNNoise CGo, DeepFilterNet ONNX, passthrough) and the suppressor pool. |
| `pkg/rtp` | Live RTP interception: jitter buffer, G.711/G.722/Opus codec handling, DTMF, RTCP, playback injection. |
| `pkg/file` | Batch/file-based post-processing (FFmpeg decode → suppress → encode pipeline). |
| `pkg/http` | The `POST /enhance` HTTP handler (`cs.NewHTTPHandler()`). |
| `pkg/sip`, `pkg/websocket`, `pkg/agentstream`, `pkg/billing`, `pkg/loadtest`, `pkg/telemetry`, `pkg/compat` | Supporting subsystems — SIP proxy/SDP handling, WebSocket bridging, agent-facing event streaming, usage billing/metering, concurrent load-test harness, metrics/telemetry sinks, and backward-compat shims, respectively. |
| `cmd/clearstream` | The CLI binary (`file` / `dir` / `rtp` / `probe` / `server` / `version` subcommands). |
| `cmd/clearstream-eval` | A separate CLI for running `pkg/eval` batch evaluations from the command line. |

## Evaluation, QA, and tooling (how we measure and test it)

These four areas all touch "does the SDK actually sound good / work end to
end," but they answer different questions and don't overlap:

| Directory | Answers | Notes |
|---|---|---|
| `pkg/eval` | "As a Go library, how do I score a batch of processed files?" | Importable package: SNR/RMS/clip-count metrics, CSV/JSON reporting, RTP quality monitoring, transcript-based scoring. What `cmd/clearstream-eval` and the `qa/e2e` scripts both build on. |
| `qa/e2e` | "Does the full call path (bridge + orchestrator + data pipe) work end to end, and how do different presets rank?" | Python scripts (`start_stack.sh`, `generate_qa_sheet.py`) plus a small Go WebSocket dialer (`bridge/ws_dial.go`) that stand up the real E2E stack and aggregate `pkg/eval`-style metrics with transcript quality into one QA sheet. |
| `qa/eval` | "Given recordings, what's the transcript quality gate score (Char/Word/LLM-judged accuracy) per preset?" | One script, `transcript_proxy_eval.py`, using local Whisper transcription — the transcript-scoring half that `qa/e2e/generate_qa_sheet.py` consumes. |
| `voice-qa` | "Manual/exploratory QA across a browser-based call rig, possibly spanning sibling repos." | `setup/env.local` wires up paths to sibling checkouts (`ClearStream-1`, `ingestream`) alongside this repo — this is a separate, heavier harness from `qa/`, not a replacement for it. `browser-lab/results/` is a checked-in example output only (`example_metrics.json`); real runs write large local result sets there and are not committed. **If you run the browser-lab orchestrator locally, it creates a Python `.venv/` under `browser-lab/orchestrator/` with thousands of files — this is gitignored (venvs self-ignore via their own generated `.gitignore`) but will make a local `find`/Finder browse of this directory look far larger than what's actually tracked in git.** |
| `scripts` | "How do I train, export, or quantize the noise-suppression models themselves?" | Python: DeepFilterNet/RNNoise ONNX export, quantization, fine-tuning, training-data prep, and one-off A/B analysis scripts (`sprint26_ab_test.py`, `sprint27_ab_compare.py`) — model tooling, not QA. |
| `tools` | Small standalone Go programs and configs used from the `make` targets in the root `README.md` (`gen_test_audio`, `snr_benchmark`, `load_test`, `noise_load`, `rnnoise_process`) plus `prometheus.yml` and `send_rtp_test.sh`. |
| `demo` | `poc_demo.sh` — a single smoke-test script that exercises every integration path against a built `./clearstream` binary. |
| `examples` | Minimal SDK usage examples for specific integrations (Asterisk AGI/ARI, a generic RTP bridge, WebRTC bridge, batch file cleanup). |
| `testdata` | Small checked-in WAV fixtures (`sample_clean.wav`, `sample_noisy.wav`, `sample_office.wav`) used by unit tests. |
| `docs` | Narrative writeups that don't fit in the README (denoiser eval methodology, NR tuning/training guide) plus this file. |

## Why isn't everything in one QA folder?

`pkg/eval`, `qa/e2e`, and `qa/eval` grew at different times for different
immediate needs (see `DEVLOG.md` for the day-by-day history) and now form a
layered stack — `pkg/eval` is the library, `qa/eval` scores transcripts,
`qa/e2e` orchestrates the full stack and combines both. `voice-qa` is a
separate, later addition for manual browser-based testing across sibling
repos and isn't meant to replace any of the three. This doc exists so that
distinction doesn't have to be rediscovered from scratch each time.
