"""
Sprint 26 A/B Test — Spectral Gate vs RNNoise on babble/background frames.

Compares ClearStream's Ephraim-Malah spectral gate (pkg/audio/noise_reducer.go)
against a Python RNNoise implementation on the same audio file.

Metric focus: background frames (RMS 50–280) — these contain the babble voice
that spectral approaches fail to remove. RNNoise should reduce them more.

5% Speech Degradation Limit: RNNoise must NOT reduce user-voice RMS by more
than 5% vs the spectral gate on speech frames (RMS ≥ 280).

Usage:
    pip install numpy soundfile  (rnnoise optional — falls back to mock)
    python scripts/sprint26_ab_test.py \\
        --raw      raw_audio.wav \\
        --gate     raw_audio_fixed_nr.wav \\
        --out-dir  eval_out/sprint26/

Outputs:
    sprint26_results.md   — leaderboard table matching Exotel Confluence format
    sprint26_frames.csv   — per-frame RMS and class data
    sprint26_rnnoise.wav  — RNNoise-processed audio (for listening test)
"""

import argparse, csv, os, struct, time
import numpy as np

# ──────────────────────────────────────────────────────────────────────────────
# WAV I/O (no dependency on soundfile for portability)
# ──────────────────────────────────────────────────────────────────────────────

def read_wav_i16(path):
    with open(path, 'rb') as f:
        data = f.read()
    return np.frombuffer(data[44:], dtype=np.int16).astype(np.float32), 16000

def write_wav(path, samples_f32, sr=16000):
    s = np.clip(samples_f32, -32768, 32767).astype(np.int16)
    os.makedirs(os.path.dirname(path) or '.', exist_ok=True)
    with open(path, 'wb') as f:
        n = len(s)
        f.write(b'RIFF')
        f.write(struct.pack('<I', 36 + n*2))
        f.write(b'WAVEfmt ')
        f.write(struct.pack('<IHHIIHH', 16, 1, 1, sr, sr*2, 2, 16))
        f.write(b'data')
        f.write(struct.pack('<I', n*2))
        f.write(s.tobytes())

# ──────────────────────────────────────────────────────────────────────────────
# Ephraim-Malah spectral gate  (same as pkg/audio/noise_reducer.go)
# ──────────────────────────────────────────────────────────────────────────────

def spectral_gate_nr(samples,
    BANDS=8, FRAME=160,
    ALPHA_G=0.96, ALPHA_P=0.94,
    OVERSUB=0.85, SPEECH_THRESH=280,
    MIN_GAIN_SPEECH=0.55, MIN_GAIN_NOISE=0.08,
    NOISE_EMA=0.997, GATE_ATT=0.08, HANGOVER=12,
):
    """Python mirror of AdaptiveNoiseReducer (Go) — Ephraim-Malah DD estimator."""
    band_size = FRAME // BANDS
    band_floor   = np.zeros(BANDS)
    band_gain    = np.ones(BANDS)
    band_snr     = np.ones(BANDS)
    global_ema   = 0.0
    hangover     = 0
    frame_count  = 0
    out = samples.copy()

    n_frames = len(samples) // FRAME
    for i in range(n_frames):
        f = samples[i*FRAME:(i+1)*FRAME]
        speech_this = False
        gains = np.ones(BANDS)

        for b in range(BANDS):
            s = f[b*band_size:(b+1)*band_size]
            rms = np.sqrt(np.mean(s**2))
            is_sp = rms >= SPEECH_THRESH
            if is_sp:
                speech_this = True
            if not is_sp:
                band_floor[b] = band_floor[b]*NOISE_EMA + rms*(1-NOISE_EMA)
            fl = max(band_floor[b], 1)
            post_snr = max(0, (rms/fl)**2 - 1)
            apriori  = ALPHA_P*(band_gain[b]**2)*band_snr[b] + (1-ALPHA_P)*post_snr
            band_snr[b] = apriori
            raw_g   = apriori / (apriori + OVERSUB)
            smooth  = ALPHA_G*band_gain[b] + (1-ALPHA_G)*raw_g
            eff_sp  = is_sp or hangover > 0
            min_g   = MIN_GAIN_SPEECH if eff_sp else MIN_GAIN_NOISE
            smooth  = np.clip(smooth, min_g, 1.0)
            band_gain[b] = smooth
            gains[b] = smooth

        if speech_this:
            hangover = HANGOVER
        elif hangover > 0:
            hangover -= 1

        fo = f.copy()
        for b in range(BANDS):
            fo[b*band_size:(b+1)*band_size] *= gains[b]

        frame_rms = np.sqrt(np.mean(fo**2))
        frame_count += 1
        if frame_count == 1:
            global_ema = frame_rms
        else:
            if frame_rms < global_ema:
                global_ema = global_ema*0.990 + frame_rms*0.010
            else:
                global_ema = global_ema*0.9995 + frame_rms*0.0005

        if frame_count > 50 and frame_rms < global_ema*1.5 and hangover == 0:
            fo *= GATE_ATT

        out[i*FRAME:(i+1)*FRAME] = fo

    return out

# ──────────────────────────────────────────────────────────────────────────────
# RNNoise — try rnnoise Python package; fall back to mock if not installed
# ──────────────────────────────────────────────────────────────────────────────

def build_rnnoise_processor():
    try:
        import rnnoise  # pip install rnnoise (wraps the C library)
        denoiser = rnnoise.RNNoise()
        SR_RNN = 48000
        FRAME_RNN = 480  # 10ms @ 48kHz

        def _rnnoise_process(samples_16k):
            """Upsample 16k→48k, run RNNoise, downsample 48k→16k."""
            # Upsample 3x via linear interpolation
            up = np.interp(
                np.arange(len(samples_16k)*3) / 3,
                np.arange(len(samples_16k)),
                samples_16k
            ).astype(np.int16)

            out48 = np.zeros_like(up, dtype=np.float32)
            n_frames = len(up) // FRAME_RNN
            for i in range(n_frames):
                chunk = up[i*FRAME_RNN:(i+1)*FRAME_RNN].astype(np.float32)
                # rnnoise expects int16-range float32
                cleaned = denoiser.process_frame(chunk)
                out48[i*FRAME_RNN:(i+1)*FRAME_RNN] = cleaned

            # Downsample 3x via averaging
            n = (len(out48) // 3) * 3
            out48 = out48[:n]
            return out48.reshape(-1, 3).mean(axis=1)

        print("[sprint26] Using real RNNoise (C library via Python binding)")
        return _rnnoise_process, "rnnoise-c"

    except ImportError:
        pass

    # ── Mock RNNoise: stationary-noise Wiener filter (demonstrates the A/B
    #    framework; replace with real model when available) ──────────────────
    print("[sprint26] rnnoise package not found — using mock RNNoise (Wiener)")
    print("           Install: pip install rnnoise")
    print("           Mock applies a simple frequency-domain Wiener filter.")

    FRAME = 160
    noise_est = None

    def _mock_rnnoise(samples):
        nonlocal noise_est
        out = samples.copy()
        n_frames = len(samples) // FRAME
        for i in range(n_frames):
            f = samples[i*FRAME:(i+1)*FRAME].astype(np.float64)
            spec = np.fft.rfft(f, n=FRAME)
            mag  = np.abs(spec)

            if noise_est is None:
                noise_est = mag * 0.5
            else:
                # Slow-update noise estimate (stationary component only)
                noise_est = 0.98*noise_est + 0.02*np.minimum(mag, noise_est*1.5)

            # Wiener gain
            snr  = np.maximum(0, (mag**2 - noise_est**2)) / (mag**2 + 1e-9)
            gain = np.sqrt(snr)
            filtered = spec * gain
            fo = np.fft.irfft(filtered, n=FRAME)
            out[i*FRAME:(i+1)*FRAME] = fo.astype(np.float32)
        return out

    return _mock_rnnoise, "rnnoise-mock"

# ──────────────────────────────────────────────────────────────────────────────
# Frame-level metrics
# ──────────────────────────────────────────────────────────────────────────────

SPEECH_THRESH  = 280.0
SILENCE_THRESH = 50.0
SPEECH_DEGRAD_LIMIT = 0.05  # 5% max RMS reduction on speech frames

def classify(rms):
    if rms < SILENCE_THRESH:
        return 'silence'
    if rms < SPEECH_THRESH:
        return 'background'
    return 'speech'

def frame_rms(f):
    return float(np.sqrt(np.mean(np.array(f, dtype=np.float64)**2)))

# ──────────────────────────────────────────────────────────────────────────────
# Main
# ──────────────────────────────────────────────────────────────────────────────

def run(raw_path, gate_path, out_dir):
    os.makedirs(out_dir, exist_ok=True)
    FRAME = 160

    print(f"\nReading audio files…")
    raw,  sr = read_wav_i16(raw_path)
    gate, _  = read_wav_i16(gate_path)
    n = min(len(raw), len(gate))
    raw, gate = raw[:n], gate[:n]
    n_frames = n // FRAME
    dur = n / sr
    print(f"  {dur:.1f}s  |  {n_frames} frames  |  {sr} Hz")

    # ── Run spectral gate (re-computed from raw for fair comparison) ─────────
    print("\nRunning Ephraim-Malah spectral gate…")
    t0 = time.time()
    gate_out = spectral_gate_nr(raw)
    gate_ms  = (time.time()-t0)*1000
    print(f"  Done in {gate_ms:.0f} ms  (RTF {gate_ms/(dur*1000):.4f})")

    # ── Run RNNoise ──────────────────────────────────────────────────────────
    print("\nRunning RNNoise…")
    rnn_fn, rnn_name = build_rnnoise_processor()
    t0 = time.time()
    rnn_out = np.array(rnn_fn(raw), dtype=np.float32)
    rnn_ms  = (time.time()-t0)*1000
    print(f"  Done in {rnn_ms:.0f} ms  (RTF {rnn_ms/(dur*1000):.4f})")

    # ── Per-frame analysis ───────────────────────────────────────────────────
    print("\nAnalysing frames…")
    rows = []
    stats = {c: {'raw':[], 'gate':[], 'rnn':[]} for c in ('speech','background','silence')}

    for i in range(n_frames):
        sl   = slice(i*FRAME, (i+1)*FRAME)
        rr   = frame_rms(raw[sl])
        gr   = frame_rms(gate_out[sl])
        rnr  = frame_rms(rnn_out[sl] if i*FRAME < len(rnn_out) else raw[sl])
        cls  = classify(rr)
        t    = round(i*FRAME/sr, 3)

        # 5% limit check: RNNoise must not degrade speech MORE than spectral gate by >5%.
        # Baseline is the gate output, not raw — we measure regression vs current best.
        violation = cls == 'speech' and gr > 0 and (gr - rnr)/gr > SPEECH_DEGRAD_LIMIT

        rows.append({
            'frame': i, 'time_s': t, 'class': cls,
            'raw_rms': round(rr,1), 'gate_rms': round(gr,1), 'rnn_rms': round(rnr,1),
            'gate_gain': round(gr/max(rr,1),3), 'rnn_gain': round(rnr/max(rr,1),3),
            'violation': int(violation),
        })
        stats[cls]['raw'].append(rr)
        stats[cls]['gate'].append(gr)
        stats[cls]['rnn'].append(rnr)

    # ── Write CSV ─────────────────────────────────────────────────────────────
    csv_path = os.path.join(out_dir, 'sprint26_frames.csv')
    with open(csv_path, 'w', newline='') as f:
        w = csv.DictWriter(f, fieldnames=rows[0].keys())
        w.writeheader(); w.writerows(rows)
    print(f"  Frame CSV → {csv_path}")

    # ── Write RNNoise WAV ─────────────────────────────────────────────────────
    rnn_wav_path = os.path.join(out_dir, 'sprint26_rnnoise.wav')
    write_wav(rnn_wav_path, rnn_out)
    print(f"  RNNoise WAV → {rnn_wav_path}")

    # ── Compute summary stats ─────────────────────────────────────────────────
    def mean_ratio(cls_key, proc):
        raw_l  = stats[cls_key]['raw']
        proc_l = stats[cls_key][proc]
        if not raw_l: return float('nan')
        ratios = [p/max(r,1) for r,p in zip(raw_l, proc_l)]
        return float(np.mean(ratios))

    def snr_improvement(cls_key, proc):
        raw_l  = np.array(stats[cls_key]['raw'])
        proc_l = np.array(stats[cls_key][proc])
        mask = raw_l > 1
        if not mask.any(): return 0.0
        delta = 20*np.log10(raw_l[mask] / np.maximum(proc_l[mask],1))
        return float(np.mean(delta))

    violations = sum(r['violation'] for r in rows)
    sp_frames  = len(stats['speech']['raw'])
    bg_frames  = len(stats['background']['raw'])

    # ── Write Markdown report ─────────────────────────────────────────────────
    md_path = os.path.join(out_dir, 'sprint26_results.md')
    with open(md_path, 'w') as f:
        f.write(f"""# Sprint 26 A/B Test Results
## Spectral Gate (Ephraim-Malah) vs {rnn_name}

**File:** `{os.path.basename(raw_path)}`
**Duration:** {dur:.1f}s | **Frames:** {n_frames} | **SR:** {sr} Hz
**5% Speech Degradation Limit applied to RNNoise**

---

## Frame Classification

| Class | Frames | % of call |
|---|---|---|
| User voice (speech) | {sp_frames} | {sp_frames/n_frames*100:.1f}% |
| Background / babble | {bg_frames} | {bg_frames/n_frames*100:.1f}% |
| Silence | {len(stats['silence']['raw'])} | {len(stats['silence']['raw'])/n_frames*100:.1f}% |

---

## Suppression Comparison

### Background frames (babble reduction — lower ratio = more removed)

| Suppressor | Mean RMS ratio | SNR improvement | Notes |
|---|---|---|---|
| Spectral Gate | {mean_ratio('background','gate'):.3f} | +{snr_improvement('background','gate'):.1f} dB | Rule-based, cannot separate voices |
| {rnn_name} | {mean_ratio('background','rnn'):.3f} | +{snr_improvement('background','rnn'):.1f} dB | ML-based, partial babble reduction |
| **Winner** | {'RNNoise' if mean_ratio('background','rnn') < mean_ratio('background','gate') else 'Spectral Gate'} | — | — |

### Speech frames (user voice preservation — higher ratio = better)

| Suppressor | Mean RMS ratio | SNR delta | Violations (>5%) |
|---|---|---|---|
| Spectral Gate | {mean_ratio('speech','gate'):.3f} | {snr_improvement('speech','gate'):+.1f} dB | 0 (baseline) |
| {rnn_name} | {mean_ratio('speech','rnn'):.3f} | {snr_improvement('speech','rnn'):+.1f} dB | {violations} / {sp_frames} ({violations/max(sp_frames,1)*100:.1f}%) |

**5% limit status:** {'✅ PASS' if violations/max(sp_frames,1) < 0.05 else '❌ FAIL — too many speech frames degraded beyond 5%'}

---

## Latency / RTF

| Suppressor | Wall time | RTF |
|---|---|---|
| Spectral Gate | {gate_ms:.0f} ms | {gate_ms/(dur*1000):.4f}× |
| {rnn_name} | {rnn_ms:.0f} ms | {rnn_ms/(dur*1000):.4f}× |

---

## Verdict

{'RNNoise reduces more background than the spectral gate while staying within the 5% speech degradation limit.' if mean_ratio('background','rnn') < mean_ratio('background','gate') and violations/max(sp_frames,1) < 0.05 else 'Spectral gate is currently better. RNNoise needs tuning or real trained weights.'}

**Next step (Sprint 27):** Collect 20h of real Exotel call-center babble noise and
fine-tune RNNoise weights to close the gap on Indian-English telephony audio.
""")

    print(f"\n{'='*60}")
    print(f"  Sprint 26 Results")
    print(f"{'='*60}")
    print(f"  Background RMS ratio  — Gate: {mean_ratio('background','gate'):.3f}  |  {rnn_name}: {mean_ratio('background','rnn'):.3f}")
    print(f"  Speech RMS ratio      — Gate: {mean_ratio('speech','gate'):.3f}  |  {rnn_name}: {mean_ratio('speech','rnn'):.3f}")
    print(f"  5% violations ({rnn_name}): {violations}/{sp_frames}  ({violations/max(sp_frames,1)*100:.1f}%)")
    bg_winner = rnn_name if mean_ratio('background','rnn') < mean_ratio('background','gate') else 'spectral gate'
    print(f"  Background winner:  {bg_winner}")
    print(f"  5% limit:  {'✅ PASS' if violations/max(sp_frames,1) < 0.05 else '❌ FAIL'}")
    print(f"\n  Report → {md_path}")
    print(f"  Audio  → {rnn_wav_path}")


if __name__ == '__main__':
    p = argparse.ArgumentParser(description='Sprint 26 A/B Test')
    p.add_argument('--raw',     default='raw_audio.wav',          help='Raw input WAV')
    p.add_argument('--gate',    default='raw_audio_fixed_nr.wav', help='Spectral gate output WAV')
    p.add_argument('--out-dir', default='eval_out/sprint26',       help='Output directory')
    args = p.parse_args()
    run(args.raw, args.gate, args.out_dir)
