# Noise Reducer — Tuning & Training Guide

**ClearStream SDK | pkg/audio/noise_reducer.go**
Last updated: 2026-06-04

---

## 1. The Fundamental Limit: What Rule-Based NR Can and Cannot Do

Before tuning parameters, understand what class of problem you are solving:

| Noise Type | Rule-Based (Wiener/Spectral Gate) | ML Model (RNNoise / DeepFilterNet) |
|---|---|---|
| HVAC / fan (stationary) | ✅ Excellent | ✅ Excellent |
| Keyboard / clicks (impulse) | ✅ Good (limiter) | ✅ Good |
| Street / traffic (broadband) | ✅ Good | ✅ Good |
| Music in background | ⚠️ Partial | ✅ Good |
| **Background voice / babble** | ❌ Cannot separate | ✅ With training data |

**Why rule-based NR cannot remove background voices:** A Wiener filter estimates a noise floor from frames where the target speaker is silent. Background voices are non-stationary — they appear and disappear. When background speech is present, the noise floor estimator treats it as signal, not noise, so it is never subtracted. The AGC then amplifies the whole mix, making the background even louder. This is not a tuning problem; it is a fundamental algorithmic limitation.

To suppress background voices you need an ML model trained on paired data: clean target speech vs. the same signal with babble noise added. RNNoise and DeepFilterNet both support this. The training section below explains how.

---

## 2. The Algorithm: Decision-Directed Ephraim-Malah Wiener Filter

### What Was Wrong Before (Musical Noise / Jitter)

The original `noise_reducer.go` computed a Wiener gain independently for each 10ms frame:

```
gain = max(MinGain, 1 - OversubFactor × noiseFloor / rms)
```

With no memory between frames, the gain jumps frame-by-frame in response to rapid RMS fluctuations. This produces **musical noise** — a metallic, warbling artifact where partial tones appear and disappear. Measured gain CoV was **0.721** (anything > 0.5 is audible).

### The Fix: Ephraim-Malah Decision-Directed Estimator

The fix uses two ideas:

**A. Decision-directed a priori SNR** (Ephraim & Malah, 1984):

```
γ (post SNR)    = max(0, (rms/floor)² − 1)          -- measured this frame
ξ (prior SNR)   = α × G²_prev × ξ_prev  +  (1−α) × γ  -- smoothed estimate
G_raw           = ξ / (ξ + OversubFactor)             -- Wiener gain from prior SNR
```

By feeding the previous gain and SNR back into the current estimate, the gain reacts to the trend in SNR rather than to instantaneous noise bursts.

**B. Temporal gain smoothing:**

```
G = AlphaG × G_prev  +  (1−AlphaG) × G_raw
```

This is an exponential moving average on the gain itself. At AlphaG=0.96, each new frame contributes only 4% of the output — the gain evolves smoothly over ~250ms rather than jumping every 10ms.

**Result:** CoV drops from 0.721 → 0.317, gain flips (>0.3× change in one frame) drop from 3,145 → 183 across raw_audio.wav.

---

## 3. Parameter Reference

All parameters are exported on `AdaptiveNoiseReducer` and can be set after construction:

```go
nr := audio.NewAdaptiveNoiseReducer()
nr.AlphaG = 0.94        // less smoothing for faster response
nr.SpeechThresh = 350   // louder environment
```

### AlphaG — Gain Temporal Smoothing
| Value | Effect |
|---|---|
| 0.98 | Very smooth, slight "pumping" on fast transients |
| **0.96** | **Default. Good for telephony HVAC noise** |
| 0.92 | Faster response, slight residual roughness |
| 0.88 | Near-unsmoothed; may reintroduce musical noise |

**When to lower:** If the caller speaks in short bursts with long silence and the gain feels "slow to open", lower AlphaG to 0.92.

### AlphaP — A Priori SNR Smoothing
| Value | Effect |
|---|---|
| 0.96 | Maximum stationarity assumption — best for HVAC/fan |
| **0.94** | **Default** |
| 0.90 | Adapts faster — better for non-stationary noise |
| 0.85 | Fast adaptation; may amplify burst events |

**When to lower:** If the noise character changes rapidly mid-call (e.g., a door opens and outdoor noise enters), lower AlphaP to 0.90.

### OversubFactor — Suppression Aggressiveness
| Value | Effect |
|---|---|
| 1.5 | Aggressive — more noise removed, more coloration |
| **0.85** | **Default. Conservative — preserves soft phonemes** |
| 0.6 | Gentle — nearly pass-through except the softest noise |

**When to raise:** If background noise is loud (SNR < 20 dB) and word accuracy is more important than naturalness, raise to 1.2. For ASR pipelines (not human listening), 1.2–1.5 is acceptable.

### SpeechThresh — Speech vs. Noise Frame Classifier
This is the per-band RMS threshold. A band with RMS ≥ SpeechThresh is treated as speech (noise floor frozen, MinGainSpeech applied).

**How to set it:** Measure the inter-speech noise RMS from a sample recording and multiply by 2.5–3×.

```python
# Quick measurement from a WAV file
import numpy as np, soundfile as sf
samples, sr = sf.read('your_recording.wav')
samples = (samples * 32768).astype(np.int16)

frame_rms = []
for i in range(0, len(samples) - 160, 160):
    frame = samples[i:i+160].astype(float)
    frame_rms.append(np.sqrt(np.mean(frame**2)))

# Sort: bottom 30% = noise floor, top 30% = speech
frame_rms.sort()
n = len(frame_rms)
noise_rms = np.mean(frame_rms[:int(n*0.30)])
speech_rms = np.mean(frame_rms[int(n*0.70):])
suggested_thresh = noise_rms * 3.0
print(f"Noise RMS: {noise_rms:.1f}, Speech RMS: {speech_rms:.1f}")
print(f"Suggested SpeechThresh: {suggested_thresh:.0f}")
```

For raw_audio.wav: noise RMS ≈ 22, speech RMS ≈ 800 → SpeechThresh = 280.

### MinGainSpeech / MinGainNoise — Gain Floors

`MinGainSpeech = 0.55` means that even on a heavily noise-estimated band, at least 55% of the signal passes through during speech frames. This is comfort noise — it prevents the eerie silence that makes over-suppressed audio feel unnatural.

For pure ASR pipelines where a human is not listening: lower MinGainSpeech to 0.30 to allow more suppression. For call center agent monitoring where agents listen: keep at 0.50–0.60.

`MinGainNoise = 0.08` allows the system to suppress noise by up to −22 dB on non-speech frames. Raise it to 0.15 if you want audible comfort noise during silence.

### HangoverFrames — Word-End Protection
Each frame is 10ms. HangoverFrames=12 means 120ms of protection after the last speech frame. Without hangover, the gain drops immediately at word boundaries, clipping trailing consonants and final vowels.

| Frames | Duration | Use case |
|---|---|---|
| 6 | 60ms | Fast environments, command-and-control |
| **12** | **120ms** | **Default telephony** |
| 20 | 200ms | Slow speakers, Indian English with long vowels |

### NoiseEMACoeff — Noise Floor Adaptation Speed
Higher = slower adaptation. At 0.997 the noise floor has a time constant of roughly 3s. This is correct for stationary noise (HVAC). For call centers where agents move between quiet and noisy areas, lower to 0.990 (time constant ~1s) so the floor adapts faster between calls.

---

## 4. Configuration Presets

### 4.1 Indian Telephony — Agent Call Center (Default)
```yaml
# Optimised for 8kHz/16kHz PSTN/VoIP with HVAC + keyboard noise
AlphaG: 0.96
AlphaP: 0.94
OversubFactor: 0.85
SpeechThresh: 280
MinGainSpeech: 0.55
MinGainNoise: 0.08
HangoverFrames: 12
GateAttenuation: 0.08
NoiseEMACoeff: 0.997
```

### 4.2 Noisy Field Agent (Mobile, Outdoor)
```yaml
# Higher noise floor, more aggressive suppression
AlphaG: 0.94
AlphaP: 0.90
OversubFactor: 1.10
SpeechThresh: 500
MinGainSpeech: 0.45
MinGainNoise: 0.05
HangoverFrames: 15
GateAttenuation: 0.05
NoiseEMACoeff: 0.993
```

### 4.3 ASR Pipeline (No Human Listener)
```yaml
# Maximum noise reduction; naturalness not required
AlphaG: 0.93
AlphaP: 0.92
OversubFactor: 1.30
SpeechThresh: 300
MinGainSpeech: 0.30
MinGainNoise: 0.03
HangoverFrames: 10
GateAttenuation: 0.03
NoiseEMACoeff: 0.995
```

### 4.4 Low-Latency Real-Time (WebRTC)
```yaml
# Faster response at cost of slight roughness
AlphaG: 0.90
AlphaP: 0.88
OversubFactor: 0.80
SpeechThresh: 250
MinGainSpeech: 0.60
MinGainNoise: 0.10
HangoverFrames: 8
GateAttenuation: 0.10
NoiseEMACoeff: 0.990
```

---

## 5. What Requires ML: Background Voice / Babble Suppression

The Wiener filter cannot separate two voices. This is a **source separation** problem. The options in increasing capability:

### Option A: RNNoise (100 KB, ~5ms RTF)
Mozilla's RNNoise is a GRU-based model trained on 45h of speech + noise. It targets **stationary and semi-stationary** noise well, and handles some babble. It cannot reliably separate a clearly intelligible background speaker from the foreground speaker.

ClearStream's `pkg/audio/suppressor.go` wraps RNNoise — see `SuppressorConfig{Type: "rnnoise"}`.

### Option B: DeepFilterNet (30 MB, ~15ms RTF)
DeepFilterNet uses a two-stage complex spectrogram U-Net that explicitly models speech spectral structure. It handles babble better than RNNoise because it learns speech-vs-noise at the phoneme level. Still not a true speaker separator.

### Option C: Conv-TasNet / SepFormer (200 MB+, ~50ms RTF)
True speaker separation models. Trained on LibriMix / WSJ0-2mix. These can isolate one voice from two simultaneous speakers but require a speaker embedding (reference audio of the target speaker) to know which voice to keep. Not yet in ClearStream — planned for Sprint 28.

---

## 6. Training RNNoise on production telephony platform's Noise Types

### 6.1 What You Need

| Item | Requirement |
|---|---|
| Clean speech | 20–50h, single speaker per file, no background noise |
| Noise recordings | 5–10h of your specific noise types |
| Python environment | PyTorch ≥ 2.0, torchaudio, pesq, pystoi |
| Compute | 1× A100 or 2× V100 for ~12h training |

### 6.2 Collecting production telephony platform-Specific Noise Data

The key to good performance on Indian telephony is training on **the actual noise types your customers experience**:

```
recordings/noise/
  hvac_datacenter/       # server room / contact center HVAC
  keyboard_office/       # typing during calls
  street_mumbai/         # traffic, honking (outdoor agents)
  crowd_mall/            # babble in retail environments
  phone_handset_hiss/    # handset microphone self-noise
  call_center_babble/    # 5–10 simultaneous distant voices
```

Record each noise type at the codec path your calls actually use (G.711 µ-law or G.722 if you use HD voice) so the model learns the codec artifacts too. At minimum, 30 minutes of each type.

For babble noise: record actual call center ambient audio (with permission and PII removed). Synthetic babble (overlapping LibriSpeech utterances) does not match the Indian-English call center acoustic profile well.

### 6.3 Data Pipeline

```python
# scripts/prepare_training_data.py

import torch, torchaudio, random, numpy as np
from pathlib import Path

SAMPLE_RATE = 16000
SEGMENT_S   = 4          # 4-second training segments
SNR_RANGE   = (-5, 25)   # dB: cover the full range your calls see

def mix_at_snr(speech: torch.Tensor, noise: torch.Tensor, snr_db: float):
    """Mix speech + noise at a specified SNR."""
    speech_rms = speech.pow(2).mean().sqrt()
    noise_rms  = noise.pow(2).mean().sqrt()
    target_noise_rms = speech_rms / (10 ** (snr_db / 20))
    noise = noise * (target_noise_rms / (noise_rms + 1e-8))
    return speech + noise, speech   # noisy, clean

def build_dataset(speech_dir, noise_dir, output_dir, n_pairs=50000):
    speech_files = list(Path(speech_dir).glob("**/*.wav"))
    noise_files  = list(Path(noise_dir).glob("**/*.wav"))
    pairs = []
    for _ in range(n_pairs):
        sf = random.choice(speech_files)
        nf = random.choice(noise_files)
        speech, sr = torchaudio.load(sf)
        noise,  _  = torchaudio.load(nf)
        # Resample to 16kHz if needed
        if sr != SAMPLE_RATE:
            speech = torchaudio.functional.resample(speech, sr, SAMPLE_RATE)
        # Random crop to segment length
        seg_len = SAMPLE_RATE * SEGMENT_S
        if speech.shape[-1] < seg_len:
            continue
        start = random.randint(0, speech.shape[-1] - seg_len)
        speech = speech[..., start:start+seg_len]
        # Match noise length
        if noise.shape[-1] < seg_len:
            repeats = (seg_len // noise.shape[-1]) + 1
            noise = noise.repeat(1, repeats)
        noise = noise[..., :seg_len]
        snr = random.uniform(*SNR_RANGE)
        noisy, clean = mix_at_snr(speech, noise, snr)
        pairs.append((noisy, clean))
    return pairs
```

### 6.4 Training RNNoise Fine-Tune

RNNoise uses a hand-crafted feature set (Bark-scale bands + pitch). For fine-tuning on production telephony platform data, the most effective approach is to **retrain just the GRU weights** with your data while keeping the feature extraction fixed.

```python
# scripts/train_rnnoise_finetune.py

import torch
import torch.nn as nn

class RNNoiseGRU(nn.Module):
    """Minimal reimplementation of RNNoise's recurrent core for fine-tuning."""
    def __init__(self, input_size=42, hidden_size=96, output_size=22):
        super().__init__()
        self.gru1 = nn.GRU(input_size,  96, batch_first=True)
        self.gru2 = nn.GRU(96,          96, batch_first=True)
        self.gru3 = nn.GRU(96,          96, batch_first=True)
        self.fc   = nn.Linear(96, output_size)
        self.sigmoid = nn.Sigmoid()

    def forward(self, x):
        x, _ = self.gru1(x)
        x, _ = self.gru2(x)
        x, _ = self.gru3(x)
        return self.sigmoid(self.fc(x))   # per-band gain masks, 0–1


def sdr_loss(estimate: torch.Tensor, target: torch.Tensor) -> torch.Tensor:
    """Scale-invariant signal-to-distortion ratio loss (SI-SDR)."""
    eps = 1e-8
    target = target - target.mean(dim=-1, keepdim=True)
    estimate = estimate - estimate.mean(dim=-1, keepdim=True)
    s_target = (estimate * target).sum(dim=-1, keepdim=True) / \
               (target.pow(2).sum(dim=-1, keepdim=True) + eps) * target
    e_noise = estimate - s_target
    si_sdr = 10 * torch.log10(s_target.pow(2).sum(dim=-1) /
                               (e_noise.pow(2).sum(dim=-1) + eps) + eps)
    return -si_sdr.mean()


def train(model, train_loader, val_loader, epochs=50, lr=1e-3):
    opt = torch.optim.Adam(model.parameters(), lr=lr)
    sched = torch.optim.lr_scheduler.CosineAnnealingLR(opt, T_max=epochs)

    for epoch in range(epochs):
        model.train()
        train_loss = 0
        for noisy, clean in train_loader:
            opt.zero_grad()
            # Extract features (Bark-scale + pitch) — use rnnoise_feature_extractor
            feats = extract_features(noisy)          # [B, T, 42]
            masks = model(feats)                     # [B, T, 22]
            enhanced = apply_masks(noisy, masks)     # [B, samples]
            loss = sdr_loss(enhanced, clean)
            loss.backward()
            torch.nn.utils.clip_grad_norm_(model.parameters(), 1.0)
            opt.step()
            train_loss += loss.item()

        sched.step()
        print(f"Epoch {epoch+1}: train_loss={train_loss/len(train_loader):.4f}")

        if epoch % 5 == 0:
            validate_wer(model, val_loader)   # see section 6.5
```

### 6.5 WER-Validated Training Loop

The most important metric for ClearStream is not SDR or PESQ — it is WER improvement, which maps directly to the Char/Word/LLM scores from the production telephony platform eval framework. Use WER as the primary stop criterion:

```python
# After each 5 epochs, run ASR on a held-out validation set and measure WER delta.
# Stop training when:
#   WER_denoised < WER_noisy - 3%        (meaningful improvement)
#   AND WER_denoised plateau < 0.2%      (converged)
#
# Avoid: training until SDR is maximised — this often causes over-suppression
# that hurts WER even as SDR improves (Krisp 90 is an example of this).

def validate_wer(model, val_loader, asr_endpoint):
    model.eval()
    results = []
    with torch.no_grad():
        for noisy, clean, reference_transcript in val_loader:
            enhanced = model_enhance(model, noisy)
            hyp = asr_transcribe(enhanced, asr_endpoint)
            wer = compute_wer(reference_transcript, hyp)
            results.append(wer)
    mean_wer = sum(results) / len(results)
    print(f"  Validation WER: {mean_wer*100:.2f}%")
    return mean_wer
```

### 6.6 Exporting a Fine-Tuned Model

After training, export the model for ClearStream:

```python
# Export to ONNX (used by pkg/audio/suppressor.go)
dummy_input = torch.zeros(1, 100, 42)   # batch=1, T=100, features=42
torch.onnx.export(
    model, dummy_input,
    "models/rnnoise_telephony_v1.onnx",
    input_names=["features"],
    output_names=["gains"],
    dynamic_axes={"features": {1: "time"}, "gains": {1: "time"}},
    opset_version=14,
)

# In ClearStream config:
# suppressor_model: "models/rnnoise_telephony_v1.onnx"
```

---

## 7. Diagnosing Audio Quality Issues

### Decision Tree

```
Complaint: background noise audible
    ↓
Is noise stationary (HVAC, hiss)?
    YES → Lower OversubFactor to 1.0–1.2. Raise SpeechThresh.
    NO  → Is it a single background voice?
              YES → Rule-based NR cannot fix this. 
                    Use RNNoise/DeepFilterNet + babble training data.
              NO  → Is it intermittent (traffic, door slams)?
                        YES → Lower NoiseEMACoeff to 0.990, lower AlphaP to 0.90.
                              Enable PeakLimiter for impulses.
                        NO  → Broadband: raise OversubFactor to 1.1.

Complaint: voice sounds jittery / metallic
    ↓
    Raise AlphaG (try 0.97). Raise AlphaP (try 0.95).
    Check: CoV of per-frame gain should be < 0.40.
    If CoV > 0.50 → increase AlphaG further.

Complaint: word ends clipped / consonants cut off
    ↓
    Raise HangoverFrames (try 16–20).
    Lower SpeechThresh to catch soft trailing phonemes.
    Raise MinGainSpeech to 0.65.

Complaint: too much noise between words (comfort noise too loud)
    ↓
    Lower GateAttenuation to 0.04.
    Lower MinGainNoise to 0.04.

Complaint: gain changes audible when speaker pauses
    ↓
    This is gain "pumping". Raise AlphaG to 0.97–0.98.
    Raise MinGainSpeech to 0.60 (reduces the contrast between speech and silence).
```

### Measuring CoV from a WAV File

```python
# scripts/measure_gain_cov.py
import numpy as np, soundfile as sf, sys

raw_file    = sys.argv[1]   # raw_audio.wav
denoised_file = sys.argv[2] # processed output

raw,      sr = sf.read(raw_file)
denoised, _  = sf.read(denoised_file)

FRAME = 160
gains = []
for i in range(0, min(len(raw), len(denoised)) - FRAME, FRAME):
    r_rms = np.sqrt(np.mean(raw[i:i+FRAME]**2)) + 1e-9
    d_rms = np.sqrt(np.mean(denoised[i:i+FRAME]**2))
    gains.append(d_rms / r_rms)

gains = np.array(gains)
cov = gains.std() / (gains.mean() + 1e-9)
flips = int(np.sum(np.abs(np.diff(gains)) > 0.3))
print(f"CoV: {cov:.3f}  (target < 0.40)")
print(f"Gain flips >0.3x: {flips}  (target < 500 per 4-min call)")
```

---

## 8. Sprint Roadmap for NR Improvement

| Sprint | Work | Expected gain |
|---|---|---|
| 25 (current) | Port Ephraim-Malah to Go, add PeakLimiter | Jitter fixed, burst events handled |
| 26 | Integrate RNNoise via ONNX, A/B test vs spectral gate | +5–8% LLM score on babble calls |
| 27 | Collect 20h production telephony platform-specific noise data | Training data ready |
| 28 | Fine-tune RNNoise on production telephony platform data, WER validation loop | +3–5% WER on Indian English |
| 29 | Conv-TasNet speaker separation MVP | Background voice suppression |
| 30 | Online A/B: ClearStream DeepFilterNet vs Krisp 100 on same conversation set | Head-to-head Char/Word/LLM numbers |
