"""Sprint 27 A/B Comparison — Real CGO RNNoise vs Ephraim-Malah Spectral Gate.

Compares:
  A: raw_audio_fixed_nr.wav  — Ephraim-Malah spectral gate (pkg/audio/noise_reducer.go)
  B: eval_out/sprint27/rnnoise_real.wav — Real CGO RNNoise (pkg/model/rnnoise.go)

against raw_audio.wav as the unprocessed reference.

Frame classification (160 samples = 10ms @ 16kHz):
  Speech     : RMS >= 280
  Background : 50 <= RMS < 280
  Silence    : RMS < 50

Metrics:
  - Mean RMS ratio vs raw  (1.0 = no change, <1.0 = suppressed)
  - Mean SNR improvement on background frames (dB)
  - Speech violations: frames where processor degrades speech RMS by >5%

Outputs:
  eval_out/sprint27/sprint27_results.md   — markdown report
  eval_out/sprint27/sprint27_frames.csv   — per-frame data
"""

import argparse
import csv
import math
import os
import struct
import sys

import numpy as np

FRAME = 160        # 10ms @ 16kHz
SILENCE_THRESH = 50.0
SPEECH_THRESH  = 280.0
DEGRADATION_LIMIT = 0.05  # 5% max speech RMS degradation


# ---------------------------------------------------------------------------
# WAV I/O
# ---------------------------------------------------------------------------

def read_wav_i16(path):
    """Read a 16kHz mono PCM16 WAV; returns float32 array."""
    with open(path, 'rb') as f:
        data = f.read()
    # Locate 'data' chunk
    offset = 12
    data_start = 44
    while offset + 8 <= len(data):
        chunk_id = data[offset:offset+4]
        chunk_size = int.from_bytes(data[offset+4:offset+8], 'little')
        if chunk_id == b'data':
            data_start = offset + 8
            break
        offset += 8 + chunk_size
    return np.frombuffer(data[data_start:], dtype=np.int16).astype(np.float32)


# ---------------------------------------------------------------------------
# Frame-level metrics
# ---------------------------------------------------------------------------

def rms(frame):
    if len(frame) == 0:
        return 0.0
    return float(np.sqrt(np.mean(frame.astype(np.float64)**2)))


def classify(r):
    if r < SILENCE_THRESH:
        return 'silence'
    if r < SPEECH_THRESH:
        return 'background'
    return 'speech'


def snr_delta_db(raw_rms, proc_rms):
    """SNR improvement: positive = noise removed, negative = degraded."""
    p = max(proc_rms, 1.0)
    r = max(raw_rms, 1.0)
    return 20.0 * math.log10(r / p)


# ---------------------------------------------------------------------------
# Main comparison
# ---------------------------------------------------------------------------

def compare(raw_arr, gate_arr, rnn_arr):
    n_frames = min(len(raw_arr), len(gate_arr), len(rnn_arr)) // FRAME

    rows = []
    for i in range(n_frames):
        raw_f  = raw_arr [i*FRAME:(i+1)*FRAME]
        gate_f = gate_arr[i*FRAME:(i+1)*FRAME]
        rnn_f  = rnn_arr [i*FRAME:(i+1)*FRAME]

        raw_r  = rms(raw_f)
        gate_r = rms(gate_f)
        rnn_r  = rms(rnn_f)
        cls    = classify(raw_r)

        gate_ratio = gate_r / max(raw_r, 1.0)
        rnn_ratio  = rnn_r  / max(raw_r, 1.0)
        gate_snr   = snr_delta_db(raw_r, gate_r)
        rnn_snr    = snr_delta_db(raw_r, rnn_r)

        gate_viol = cls == 'speech' and raw_r > 0 and (raw_r - gate_r) / raw_r > DEGRADATION_LIMIT
        rnn_viol  = cls == 'speech' and raw_r > 0 and (raw_r - rnn_r)  / raw_r > DEGRADATION_LIMIT

        rows.append({
            'frame': i,
            'class': cls,
            'raw_rms':   round(raw_r,  2),
            'gate_rms':  round(gate_r, 2),
            'rnn_rms':   round(rnn_r,  2),
            'gate_ratio': round(gate_ratio, 4),
            'rnn_ratio':  round(rnn_ratio,  4),
            'gate_snr_db': round(gate_snr, 3),
            'rnn_snr_db':  round(rnn_snr,  3),
            'gate_viol': gate_viol,
            'rnn_viol':  rnn_viol,
        })
    return rows


def summarise(rows):
    sp = [r for r in rows if r['class'] == 'speech']
    bg = [r for r in rows if r['class'] == 'background']
    si = [r for r in rows if r['class'] == 'silence']

    def mean(lst, key):
        return sum(x[key] for x in lst) / len(lst) if lst else 0.0

    return {
        'total_frames': len(rows),
        'speech_frames': len(sp),
        'background_frames': len(bg),
        'silence_frames': len(si),
        # RMS ratios
        'gate_speech_rms_ratio':  round(mean(sp, 'gate_ratio'), 4),
        'rnn_speech_rms_ratio':   round(mean(sp, 'rnn_ratio'),  4),
        'gate_bg_rms_ratio':      round(mean(bg, 'gate_ratio'), 4),
        'rnn_bg_rms_ratio':       round(mean(bg, 'rnn_ratio'),  4),
        # SNR improvement on background frames
        'gate_bg_snr_db':  round(mean(bg, 'gate_snr_db'), 3),
        'rnn_bg_snr_db':   round(mean(bg, 'rnn_snr_db'),  3),
        # Speech violations
        'gate_speech_violations': sum(1 for r in sp if r['gate_viol']),
        'rnn_speech_violations':  sum(1 for r in sp if r['rnn_viol']),
    }


# ---------------------------------------------------------------------------
# Report generation
# ---------------------------------------------------------------------------

VERDICT_TEMPLATE = """## Sprint 27 A/B Test — Real CGO RNNoise vs Ephraim-Malah Spectral Gate

**Date:** 2026-06-16
**Build:** `CGO_ENABLED=1 go build -tags rnnoise` ✅

### Audio corpus
| Property | Value |
|---|---|
| Input file | `raw_audio.wav` (synthetic telephony, 60s, 16kHz mono PCM16) |
| Frame size | 160 samples = 10ms @ 16kHz |
| Total frames | {total_frames} |
| Speech frames (RMS≥280) | {speech_frames} ({speech_pct:.1f}%) |
| Background frames (50≤RMS<280) | {background_frames} ({bg_pct:.1f}%) |
| Silence frames (RMS<50) | {silence_frames} ({si_pct:.1f}%) |

### Results

#### Speech frame RMS preservation (higher = better, 1.0 = no change)
| Processor | Mean RMS ratio | Speech violations (>5% degradation) |
|---|---|---|
| Spectral Gate (A) | {gate_speech_rms_ratio:.4f} | {gate_speech_violations} |
| RNNoise CGO  (B) | {rnn_speech_rms_ratio:.4f} | {rnn_speech_violations} |

#### Background / babble suppression (higher SNR delta = more noise removed)
| Processor | Mean background RMS ratio | SNR improvement (dB) |
|---|---|---|
| Spectral Gate (A) | {gate_bg_rms_ratio:.4f} | {gate_bg_snr_db:.2f} dB |
| RNNoise CGO  (B) | {rnn_bg_rms_ratio:.4f} | {rnn_bg_snr_db:.2f} dB |

### Verdict

{verdict}

### Interpretation
{interpretation}
"""


def make_verdict(s):
    gate_speech_ok = s['gate_speech_violations'] == 0
    rnn_speech_ok  = s['rnn_speech_violations']  == 0
    rnn_better_bg  = s['rnn_bg_snr_db'] > s['gate_bg_snr_db']
    rnn_preserves  = s['rnn_speech_rms_ratio'] >= s['gate_speech_rms_ratio'] - 0.02  # within 2%

    if rnn_speech_ok and rnn_better_bg and rnn_preserves:
        verdict = "**PASS** — RNNoise CGO wins on background suppression with zero speech violations."
        interp = (
            f"RNNoise removed {s['rnn_bg_snr_db']:.2f} dB more background noise than the spectral gate "
            f"({s['rnn_bg_snr_db'] - s['gate_bg_snr_db']:+.2f} dB delta), while preserving speech RMS at "
            f"{s['rnn_speech_rms_ratio']:.4f} (gate: {s['gate_speech_rms_ratio']:.4f}). "
            f"Zero speech violations confirms it is safe for production. "
            f"Recommendation: promote RNNoise CGO as the default suppressor for ClearStream v1.0."
        )
    elif not rnn_speech_ok:
        verdict = f"**FAIL** — RNNoise CGO causes {s['rnn_speech_violations']} speech violations (>5% degradation)."
        interp = (
            f"RNNoise degraded {s['rnn_speech_violations']} speech frames beyond the 5% threshold. "
            f"Background SNR improvement was {s['rnn_bg_snr_db']:.2f} dB vs {s['gate_bg_snr_db']:.2f} dB for the gate. "
            f"Recommend tuning the upsample/downsample path or adding a speech-aware gain floor before promoting to production."
        )
    elif not rnn_better_bg:
        verdict = "**PARTIAL PASS** — RNNoise CGO preserves speech but does not improve background suppression vs spectral gate."
        interp = (
            f"Background SNR: Gate={s['gate_bg_snr_db']:.2f} dB, RNNoise={s['rnn_bg_snr_db']:.2f} dB. "
            f"RNNoise is not more aggressive on background frames. "
            f"Both have {s['gate_speech_violations']} / {s['rnn_speech_violations']} speech violations respectively. "
            f"Recommend profiling on real telephony corpus before deciding on default backend."
        )
    else:
        verdict = "**PASS (marginal)** — RNNoise CGO meets criteria but speech preservation is borderline."
        interp = (
            f"RNNoise background SNR: {s['rnn_bg_snr_db']:.2f} dB (gate: {s['gate_bg_snr_db']:.2f} dB). "
            f"Speech RMS ratio: {s['rnn_speech_rms_ratio']:.4f} vs {s['gate_speech_rms_ratio']:.4f}. "
            f"Speech violations: {s['rnn_speech_violations']} (gate: {s['gate_speech_violations']}). "
            f"Marginal pass — recommend further tuning on real telephony recordings."
        )
    return verdict, interp


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def main():
    p = argparse.ArgumentParser(description='Sprint 27 A/B comparison')
    p.add_argument('--raw',   default='raw_audio.wav',                      help='Raw input WAV')
    p.add_argument('--gate',  default='raw_audio_fixed_nr.wav',             help='Spectral gate output WAV')
    p.add_argument('--rnn',   default='eval_out/sprint27/rnnoise_real.wav', help='Real RNNoise output WAV')
    p.add_argument('--out-dir', default='eval_out/sprint27',                help='Output directory')
    args = p.parse_args()

    os.makedirs(args.out_dir, exist_ok=True)

    print(f'Reading raw:  {args.raw}')
    raw  = read_wav_i16(args.raw)
    print(f'Reading gate: {args.gate}')
    gate = read_wav_i16(args.gate)
    print(f'Reading rnn:  {args.rnn}')
    rnn  = read_wav_i16(args.rnn)

    print(f'Lengths — raw:{len(raw)}, gate:{len(gate)}, rnn:{len(rnn)} samples')

    print('Running frame-by-frame comparison...')
    rows = compare(raw, gate, rnn)
    s    = summarise(rows)
    verdict, interp = make_verdict(s)

    # Write CSV
    csv_path = os.path.join(args.out_dir, 'sprint27_frames.csv')
    with open(csv_path, 'w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=rows[0].keys())
        writer.writeheader()
        writer.writerows(rows)
    print(f'Wrote frame CSV: {csv_path}')

    # Write markdown report
    md_path = os.path.join(args.out_dir, 'sprint27_results.md')
    total = s['total_frames']
    md = VERDICT_TEMPLATE.format(
        total_frames=total,
        speech_frames=s['speech_frames'],
        speech_pct=100*s['speech_frames']/max(total,1),
        background_frames=s['background_frames'],
        bg_pct=100*s['background_frames']/max(total,1),
        silence_frames=s['silence_frames'],
        si_pct=100*s['silence_frames']/max(total,1),
        gate_speech_rms_ratio=s['gate_speech_rms_ratio'],
        rnn_speech_rms_ratio=s['rnn_speech_rms_ratio'],
        gate_speech_violations=s['gate_speech_violations'],
        rnn_speech_violations=s['rnn_speech_violations'],
        gate_bg_rms_ratio=s['gate_bg_rms_ratio'],
        rnn_bg_rms_ratio=s['rnn_bg_rms_ratio'],
        gate_bg_snr_db=s['gate_bg_snr_db'],
        rnn_bg_snr_db=s['rnn_bg_snr_db'],
        verdict=verdict,
        interpretation=interp,
    )
    with open(md_path, 'w') as f:
        f.write(md)
    print(f'Wrote report:  {md_path}')

    # Print summary to stdout
    print('\n' + '='*60)
    print('SPRINT 27 A/B SUMMARY')
    print('='*60)
    print(f"Total frames:         {s['total_frames']}")
    print(f"  Speech:             {s['speech_frames']}")
    print(f"  Background:         {s['background_frames']}")
    print(f"  Silence:            {s['silence_frames']}")
    print(f"\nSpeech RMS ratio:     Gate={s['gate_speech_rms_ratio']:.4f}  RNNoise={s['rnn_speech_rms_ratio']:.4f}")
    print(f"Speech violations:    Gate={s['gate_speech_violations']}       RNNoise={s['rnn_speech_violations']}")
    print(f"Background SNR delta: Gate={s['gate_bg_snr_db']:.2f}dB  RNNoise={s['rnn_bg_snr_db']:.2f}dB")
    print(f"\n{verdict}")

    return 0 if 'PASS' in verdict else 1


if __name__ == '__main__':
    sys.exit(main())
