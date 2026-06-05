# Voice AI Lab — Evaluation Report

**Run ID:** `YYYYMMDD_HHMMSS`  
**Operator:**  
**Go version:**  
**ClearStream model (bridge):** passthrough | rnnoise  
**Whisper model:** tiny | base  

## Audio quality (Q)

| ID | Metric | L0 passthrough | L1 rnnoise | Result |
|----|--------|----------------|------------|--------|
| Q1 | ΔSNR vs noisy | ≥ 0 dB | ≥ 10 dB | |
| Q2 | Passthrough bit-exact | PASS | N/A | |
| Q3 | Artifact rate | &lt; 1% clipping | &lt; 0.1% bursts | |

**SNR source:** `go run tools/snr_benchmark/main.go`

## ASR / Voice AI (S)

| ID | Metric | Target | A (noisy) | B (passthrough) | C (rnnoise) |
|----|--------|--------|-----------|-----------------|-------------|
| S1 | Relative WER improvement vs A | ≥ 15% | baseline | | |
| S2 | Keyword accuracy (5 phrases) | 100% | | | |
| S3 | STT partial latency regression | ≤ +10 ms p95 | | | |
| S4 | LLM slot-fill (10-turn script) | ≥ 90% | | | |
| S5 | False barge-in | ≤ baseline | | | |

**WER source:** `examples/voice_ai_lab/eval/wer_eval.py`

## Latency (T) — SDK only

| ID | Metric | Target | Measured |
|----|--------|--------|----------|
| T1 | Pipeline p99 (L0 passthrough) | &lt; 0.5 ms | |
| T2 | WS bridge p99 | &lt; 1.0 ms | |
| T3 | RNNoise p99 (L1 report) | &lt; 20 ms | |
| T4 | E2E mouth→clean PCM p95 | &lt; 30 ms (ex-STT) | |

**Source:** `tools/latency_harness/main.go`, orchestrator `bridge_ms` in STT events

## Reliability (R)

| ID | Metric | Target | Measured |
|----|--------|--------|----------|
| R1 | Frame drop rate | 0 | |
| R2 | Fallback on error | passthrough | |
| R3 | Availability | 99.9999% | |
| R4 | Model header/meta | always set | |
| R5 | Passthrough SHA256 deterministic | PASS | |

**Source:** `tools/reliability_soak/main.go -frames 10000000`

## ML hygiene (M)

- [ ] M1 Artifacts saved under `results/<run>/`
- [ ] M2 Fixed noise seed documented
- [ ] M3 `make latency-harness` PASS in CI
- [ ] M4 This report completed

## Notes

_What improved at ASR vs LLM for this run?_
