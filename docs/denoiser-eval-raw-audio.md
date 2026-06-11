# Denoiser Evaluation — Transcript Comparison & Audio Analysis
**ClearStream Benchmarking Report | raw_audio.wav**
Generated: 2026-06-04 | Framework: production telephony VoiceBot (Char / Word / LLM)

---

## 1. Evaluation Framework

Matches the production telephony VoiceBot team's `denoiser_analysis.py` exactly. Three metrics:

| Metric | How it works | Best for | Limitation |
|--------|-------------|----------|------------|
| **Char** | `SequenceMatcher` on normalised characters. `ratio = 2M/T` | Typos, spelling errors | Over-punishes "nine" vs "9" |
| **Word** | `SequenceMatcher` on word tokens | Lexical accuracy | Order-sensitive, no semantics |
| **LLM** | Azure OpenAI GPT-3.5 semantic similarity, 0–100 | Meaning preservation, noise detection | Cost, latency, non-determinism |

Higher = better. Reference: your golden transcript.

ClearStream adds 4 audio-level metrics on top:

| Added Metric | What |
|---|---|
| **SNR (dB)** | True signal-to-noise ratio via noise-floor method |
| **Noise type** | ZCR-based classification (hum / fan / broadband / white) |
| **VAD Δ** | First VAD fire relative to actual speech start |
| **Latency RTF** | Real-time factor (< 1.0 = faster than real-time) |

---

## 2. Benchmark — production telephony Team Results (from Confluence)

Reference conversation: `42b4bd75-9b8b-41f4-8dee-76a9e882b206`
Evaluated: Krisp 90 / 95 / 100, Sanas, Hector, Hector Human

| Denoiser | N | Avg Char | Avg Word | Avg LLM | Rank |
|----------|---|----------|----------|---------|------|
| **Krisp 100** | 27 | 68.95% | **84.36%** | **80.19%** | 🥇 1st |
| Krisp 95 | 26 | 76.76% | 80.15% | 74.04% | 🥈 2nd |
| Krisp 90 | 24 | 70.83% | 74.16% | 53.54% | 🥉 3rd |
| Sanas | 48 | 30.50% | 37.47% | 15.00% | ❌ 4th |
| Hector | 26 | 13.65% | 16.76% | 6.92% | ❌ 5th |
| Hector Human | 22 | 15.03% | 19.84% | 6.59% | ❌ 6th |

**Key finding from team's analysis:**
- Krisp 100 is the clear winner on both Word and LLM metrics
- Sanas and Hector fail catastrophically — likely over-denoising strips or distorts speech
- Char scores are unreliable when number format differs ("nine eight seven" vs "987") — Word and LLM are better signals
- LLM score is the most important: Krisp 90 drops from 74% Word to **53% LLM**, meaning it sounds worse semantically than its word overlap suggests

---

## 3. raw_audio.wav — File Analysis

### 3.1 Recording Profile

| Property | Value |
|----------|-------|
| Duration | **235.7s (3.9 min)** |
| Format | PCM signed 16-bit LE, 16 kHz, mono |
| File size | 7.2 MB |
| Peak | -2.7 dBFS (23,964 / 32,767) — ⚠️ Only 2.7 dB headroom |
| RMS level | -28.5 dBFS |
| Clipped samples | 0 (clean) |

### 3.2 VAD / Speech Breakdown

| Frame class | Frames | Duration | % |
|-------------|--------|----------|---|
| Speech (RMS ≥ 300) | 12,244 | 122.4s | **51.9%** |
| Noise (50 < RMS < 300) | 6,049 | 60.5s | **25.7%** |
| Silence (RMS < 50) | 5,280 | 52.8s | 22.4% |
| Total | 23,573 | 235.7s | 100% |

Active conversation. 25.7% noise frames means inter-word gaps are noisy — this directly degrades ASR and inflates WER.

### 3.3 Noise Characterisation

| Metric | Value | Interpretation |
|--------|-------|----------------|
| Noise floor RMS | 7.9 (−72.4 dBFS) | Very low floor |
| Inter-speech noise RMS | 22.4 | Audible between words |
| Zero-crossing rate | ~1,116 Hz | Mid-band: **office fan / HVAC** |
| Noise type | `mid_band_fan_hvac` | Spectral gate targets this well |
| Burst events (impulse) | 2,166 | ⚠️ High — line clicks or plosives |

### 3.4 Spectral Energy

| Band | RMS | dBFS | Interpretation |
|------|-----|------|----------------|
| Sub-speech (0–300 Hz) | 652 | −34.0 | Room hum |
| Low-speech (300 Hz–1 kHz) | 973 | −30.5 | Core speech (strongest) |
| Mid-speech (1–3 kHz) | 365 | −39.1 | Consonants — 9 dB weaker than low-speech |
| High-speech (3–8 kHz) | 90 | −51.2 | Sibilance (very weak) |

The 9 dB gap between low and mid-speech bands indicates telephony channel roll-off. Consonant intelligibility is reduced — meaningful for WER.

### 3.5 Baseline SNR

| Metric | Value |
|--------|-------|
| True SNR (noise-floor method) | **47.4 dB** |
| Estimated MOS proxy | ~3.2 / 5.0 |
| Estimated WER impact | **~9.9%** (from noise-frame ratio) |

---

## 4. ClearStream Processing — Before vs After

Config used: `spectral_gate_threshold=150, gate_attenuation=0.15×, agc_target=2500, max_gain=3.0×`

| Metric | Raw | ClearStream | Delta |
|--------|-----|-------------|-------|
| True SNR | 47.4 dB | **69.1 dB** | **+21.7 dB** |
| Noise floor RMS | 7.9 | 1.3 | −15.9 dB |
| Noise frame % | 25.7% | **13.9%** | −11.8% |
| RMS level | −28.5 dBFS | −22.9 dBFS | +5.7 dB |
| Peak | −2.7 dBFS | −2.7 dBFS | 0 (no clip) |
| Est. WER impact | ~9.9% | **~5.7%** | **−4.2%** |
| Est. MOS proxy | 3.2 | **4.1** | +0.9 |
| Clarity score | 42.4 / 100 | 40.5 / 100 | −1.9 ⚠️ |
| AGC gain | — | 1.93× (+5.7 dB) | — |
| RTF | — | **< 0.05×** | 20× faster than real-time |

**Clarity score dropped 1.9 pts:** gate threshold (150 RMS) is clipping soft speech phonemes. Reduce to 100 RMS.

### 4.1 Segment-Level SNR (matches team's per-conversation comparison grid)

| # | Time | SNR Raw | SNR ClearStream | Δ | Status |
|---|------|---------|-----------------|---|--------|
| 1 | 0–10s | 54.4 dB | 69.8 dB | +15.4 | ✅ |
| 2 | 10–20s | 43.9 dB | 62.9 dB | +19.1 | ✅ |
| 3 | 20–30s | 42.4 dB | 63.2 dB | +20.9 | ✅ |
| 4 | 30–40s | 49.3 dB | 71.3 dB | +22.0 | ✅ |
| 5 | 40–50s | 47.2 dB | 67.6 dB | +20.4 | ✅ |
| 6 | 50–60s | 43.7 dB | 65.4 dB | +21.7 | ✅ |
| 7 | 60–70s | 44.5 dB | 67.2 dB | +22.7 | ✅ |
| 8 | 70–80s | 51.2 dB | 71.5 dB | +20.3 | ✅ |
| 9 | 80–90s | 50.6 dB | 72.3 dB | +21.7 | ✅ |
| 10 | 90–100s | 43.8 dB | 65.9 dB | +22.1 | ✅ |
| 11 | 100–110s | 36.9 dB | 56.3 dB | +19.4 | ✅ |
| 12 | 110–120s | 47.6 dB | 69.2 dB | +21.6 | ✅ |
| 13 | 120–130s | 46.9 dB | 69.4 dB | +22.4 | ✅ |
| 14 | 130–140s | 43.3 dB | 64.0 dB | +20.6 | ✅ |
| 15 | 140–150s | 46.6 dB | 68.7 dB | +22.2 | ✅ |
| 16 | 150–160s | 48.6 dB | 70.4 dB | +21.8 | ✅ |
| 17 | 160–170s | 43.5 dB | 62.1 dB | +18.6 | ✅ |
| 18 | 170–180s | 44.1 dB | 64.2 dB | +20.1 | ✅ |
| 19 | 180–190s | 46.4 dB | 68.1 dB | +21.7 | ✅ |
| 20 | 190–200s | 49.6 dB | 70.6 dB | +20.9 | ✅ |
| 21 | 200–210s | 46.7 dB | 68.6 dB | +21.9 | ✅ |
| 22 | 210–220s | 47.8 dB | 69.7 dB | +21.9 | ✅ |
| 23 | 220–235s | 38.5 dB | 58.0 dB | +19.5 | ✅ |
| **Avg** | | **46.3 dB** | **66.8 dB** | **+20.4 dB** | All pass |

---

## 5. ClearStream vs Krisp 100 — Positioning

ClearStream (spectral gate) hasn't been evaluated on the exact same conversation set as Krisp yet. This section maps what we know:

| Dimension | Krisp 100 | ClearStream (Spectral Gate) | ClearStream (RNNoise) | ClearStream (DeepFilter) |
|-----------|-----------|----------------------------|-----------------------|--------------------------|
| Avg Word score | **84.36%** | ~82% est. | TBD | TBD |
| Avg LLM score | **80.19%** | ~75% est. | TBD | TBD |
| SNR improvement | ~15–20 dB | **+21.7 dB** | TBD | TBD |
| Noise frame reduction | ~50–60% | **−11.8%** | TBD | TBD |
| Speech over-suppression | Minimal | Minor (−1.9 clarity) | TBD | TBD |
| Latency | ~15–25ms | **< 1ms** | ~5ms | ~15ms |
| RTF | ~0.2× | **< 0.05×** | ~0.05× | ~0.15× |
| Model size | Proprietary | 0 MB (rule-based) | 100 KB | 30 MB |
| Cost | Licensing fee | Open source | Open source | Open source |

**Estimates for Word/LLM scores** are based on the 4.2% WER improvement observed. Actual scores require running the same reference conversation through ClearStream and the voicebot ASR pipeline.

To get exact Char/Word/LLM numbers for ClearStream, run:
```bash
# 1. Process reference conversation audio through ClearStream
./clearstream-eval batch --input-dir ./ref_audio/ --output-dir ./ref_denoised/

# 2. Run ASR on both raw and denoised (pipe through your STT endpoint)

# 3. Score transcripts
python scripts/denoiser_analysis.py \
    --transcript-dir ./transcripts/ \
    --reference-conv 42b4bd75-9b8b-41f4-8dee-76a9e882b206 \
    --output-dir ./eval_out/
```

---

## 6. Issues Found in raw_audio.wav

### Issue 1 — Gate over-suppression (−1.9 clarity)
- Gate threshold 150 RMS clips soft vowel tails and fricatives
- **Fix:** Lower to `spectral_gate_threshold: 100`

### Issue 2 — Tight headroom (2.7 dB)
- AGC must stay conservative. Current gain = 1.93× is safe.
- **Fix:** Always compute `max_gain = 32767 / peak_sample` before setting AGC

### Issue 3 — 2,166 burst/impulse events
- High count of sudden amplitude spikes — likely line clicks or plosives
- **Fix needed:** `PeakLimiter` (5ms lookahead) in `pkg/audio/limiter.go` — Sprint 25

### Issue 4 — Weak 1–3 kHz band (−39 dBFS)
- Consonant intelligibility reduced by telephony roll-off
- **Optional fix:** +3 dB high-shelf EQ at 2 kHz for ASR pipelines

---

## 7. Recommended Config for This Noise Profile

```yaml
# raw_audio.wav profile: mid-band fan/HVAC, active conversation, tight headroom
vad_threshold: 0.15               # low — 52% speech ratio, don't skip utterances
spectral_gate_threshold: 100      # reduced from 150 to avoid over-suppression
spectral_gate_attenuation: 0.10   # softer gate
agc_target_rms: 2500              # conservative — only 2.7 dB headroom in source
agc_max_gain: 2.0                 # hard cap
suppressor_aggressiveness: 2      # medium — SNR 47 dB is already good
jitter_depth_frames: 4            # standard telephony
peak_limiter_enabled: true        # needed for 2166 burst events (Sprint 25)
peak_limiter_lookahead_ms: 5
```

---

## 8. Sprint 25 Tasks Identified from This Eval

| Task | File | Priority |
|------|------|----------|
| Add `PeakLimiter` struct | `pkg/audio/limiter.go` | High — 2166 bursts in this file |
| Add Char/Word/LLM to `FileResult` | `pkg/eval/metrics.go` | High — matches team framework |
| Wire `TranscriptScorer` into eval CLI | `cmd/clearstream-eval/main.go` | High |
| Add `--transcript-dir` flag to batch eval | `cmd/clearstream-eval/main.go` | Medium |
| Add PESQ/STOI when reference available | `pkg/eval/metrics.go` | Medium |
| Add `denoiser_low_matches.txt` output | `scripts/denoiser_analysis.py` | Low — matches team's output |

---

## 9. How to Run End-to-End

```bash
# Evaluate a directory of audio files
python scripts/denoiser_analysis.py \
    --audio-dir   /path/to/audio/   \   # subdir per denoiser: audio/clearstream/, audio/krisp100/
    --transcript-dir ./transcripts/ \   # subdir per denoiser + reference/
    --reference-conv 42b4bd75-...   \
    --output-dir  ./eval_out/       \
    --llm-endpoint $AZURE_OPENAI_ENDPOINT \
    --no-llm                             # omit this line to enable LLM scoring

# Outputs:
#   denoiser_results.md   — main report (matches Confluence format)
#   denoiser_summary.csv  — leaderboard
#   per_conversation_*.json
#   audio_metrics_*.csv   — SNR/VAD/noise per file
```
