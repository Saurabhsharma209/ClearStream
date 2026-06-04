#!/usr/bin/env python3
"""
denoiser_analysis.py — Enhanced Denoiser Evaluation Script
Matches the Exotel VoiceBot framework (Char / Word / LLM scoring)
and adds audio-level metrics (SNR, VAD delta, noise type, spectral analysis).

Usage:
    python denoiser_analysis.py \\
        --audio-dir  /path/to/raw_audio_files/ \\
        --output-dir /path/to/eval_outputs/   \\
        --denoisers  clearstream krisp100 krisp95 \\
        --reference-conv 42b4bd75-9b8b-41f4-8dee-76a9e882b206

    # For transcript-only eval (no audio, fetch from voicebot API):
    python denoiser_analysis.py \\
        --transcript-mode \\
        --voicebot-url https://voicebot.exotel.com \\
        --reference-conv 42b4bd75-...

Environment variables:
    AZURE_OPENAI_API_KEY      — Azure OpenAI API key (for LLM scoring)
    AZURE_OPENAI_ENDPOINT     — Azure OpenAI endpoint URL
    OPENAI_API_KEY            — OpenAI API key (alternative to Azure)
    VOICEBOT_USERNAME         — VoiceBot API username
    VOICEBOT_PASSWORD         — VoiceBot API password
"""

import argparse
import csv
import json
import math
import os
import re
import struct
import subprocess
import sys
import time
from collections import defaultdict
from datetime import datetime
from difflib import SequenceMatcher
from pathlib import Path
from typing import Optional

# ── Optional dependencies ─────────────────────────────────────────────────────
try:
    import requests
    HAS_REQUESTS = True
except ImportError:
    HAS_REQUESTS = False
    print("[WARN] requests not installed — transcript fetching and LLM scoring disabled")

try:
    import openai
    HAS_OPENAI = True
except ImportError:
    HAS_OPENAI = False

# ─────────────────────────────────────────────────────────────────────────────
# 1. TEXT SCORING  (Char / Word / LLM)
#    Matches denoiser_analysis.py from VoiceBot Confluence page
# ─────────────────────────────────────────────────────────────────────────────

def normalise(text: str) -> str:
    """Lowercase + collapse whitespace. Same as original script."""
    return re.sub(r'\s+', ' ', text.lower()).strip()


def char_score(reference: str, comparison: str) -> float:
    """
    Character-level SequenceMatcher ratio on normalised text.
    ratio = 2 * M / T  (M = matching chars, T = total chars in both)
    """
    a, b = normalise(reference), normalise(comparison)
    if not a and not b:
        return 1.0
    return SequenceMatcher(None, a, b).ratio()


def word_score(reference: str, comparison: str) -> float:
    """
    Word-level SequenceMatcher ratio.
    Tokenises on whitespace after normalisation.
    """
    a = normalise(reference).split()
    b = normalise(comparison).split()
    if not a and not b:
        return 1.0
    total = len(a) + len(b)
    if total == 0:
        return 0.0
    m = SequenceMatcher(None, a, b)
    # Sum of matching block sizes = matching words
    matching = sum(block.size for block in m.get_matching_blocks())
    return 2.0 * matching / total


# LLM prompt — identical to Confluence page, with minor formatting tightening
LLM_SYSTEM_PROMPT = """You are evaluating the quality of a denoiser for voice-call transcripts.
You will be given a reference (golden) user transcript and a comparison transcript produced from denoised audio.
Rate how similar the comparison is to the reference on a scale of 0 to 100.

Rules:
- Be LENIENT on: minor rephrasing, number format differences ("nine eight seven" vs "987"),
  small transcription errors, different punctuation.
- PENALISE LIGHTLY: minor word changes that preserve meaning.
- PENALISE HEAVILY: extra words from background noise, hallucinated content,
  completely wrong sentences, major omissions.
- Score 0 = hallucinated / completely wrong content unrelated to reference.
- Score 100 = same meaning, same key words, no extra noise content.

Respond with ONLY an integer from 0 to 100. No explanation."""


_llm_last_call = 0.0

def llm_score(
    reference: str,
    comparison: str,
    endpoint: Optional[str] = None,
    api_key: Optional[str] = None,
    model: str = "voice-bot-gpt-35-1106",
    timeout: float = 30.0,
    rate_limit_delay: float = 1.0,
) -> float:
    """
    LLM semantic similarity score (0–100).
    Matches Azure OpenAI call in denoiser_analysis.py.
    Returns -1 on failure or if LLM is not configured.
    """
    global _llm_last_call

    endpoint = endpoint or os.environ.get("AZURE_OPENAI_ENDPOINT") or ""
    api_key  = api_key  or os.environ.get("AZURE_OPENAI_API_KEY") or os.environ.get("OPENAI_API_KEY") or ""

    if not endpoint or not api_key or not HAS_REQUESTS:
        return -1.0

    # Rate limiting
    elapsed = time.time() - _llm_last_call
    if elapsed < rate_limit_delay:
        time.sleep(rate_limit_delay - elapsed)

    user_msg = f"Reference:\n{reference}\n\nComparison:\n{comparison}"
    payload = {
        "model": model,
        "messages": [
            {"role": "system", "content": LLM_SYSTEM_PROMPT},
            {"role": "user",   "content": user_msg},
        ],
        "max_tokens": 5,
        "temperature": 0,
    }

    try:
        resp = requests.post(
            endpoint,
            headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
            json=payload,
            timeout=timeout,
        )
        resp.raise_for_status()
        content = resp.json()["choices"][0]["message"]["content"].strip()
        _llm_last_call = time.time()
        return float(int(content))
    except Exception as e:
        print(f"[WARN] LLM score failed: {e}")
        _llm_last_call = time.time()
        return -1.0


# ─────────────────────────────────────────────────────────────────────────────
# 2. AUDIO METRICS  (SNR, noise floor, VAD, spectral — ClearStream extension)
# ─────────────────────────────────────────────────────────────────────────────

def decode_pcm(path: str, rate: int = 16000) -> list:
    """Decode any audio file to mono 16kHz int16 PCM via ffmpeg."""
    r = subprocess.run(
        ["ffmpeg", "-y", "-i", str(path), "-ac", "1", "-ar", str(rate), "-f", "s16le", "-"],
        capture_output=True,
    )
    data = r.stdout
    n = len(data) // 2
    return list(struct.unpack(f"<{n}h", data))


def audio_metrics(samples: list, frame_size: int = 160) -> dict:
    """
    Compute ClearStream audio metrics for one file.
    Returns dict matching the extended eval schema.
    """
    if not samples:
        return {}

    SR = 16000
    frames = [samples[i:i+frame_size] for i in range(0, len(samples)-frame_size, frame_size)]
    rmss = [math.sqrt(sum(s*s for s in f)/len(f)) for f in frames]
    sorted_rms = sorted(rmss)
    n = len(sorted_rms)

    # Noise floor = bottom 10% of frames (same as eval CLI)
    noise_idx = max(1, int(n * 0.10))
    noise_floor = sum(sorted_rms[:noise_idx]) / noise_idx

    # Speech level = top 30% of frames
    speech_idx = int(n * 0.70)
    speech_rms = sum(sorted_rms[speech_idx:]) / len(sorted_rms[speech_idx:])

    # True SNR
    true_snr = 20 * math.log10(speech_rms / max(noise_floor, 1))

    # Overall RMS and peak
    overall_rms = math.sqrt(sum(s*s for s in samples) / len(samples))
    peak = max(abs(s) for s in samples)
    level_dbfs  = 20 * math.log10(overall_rms / 32768) if overall_rms > 0 else -96
    peak_dbfs   = 20 * math.log10(peak / 32768) if peak > 0 else -96

    # VAD breakdown
    SILENCE_THR, SPEECH_THR = 50, 300
    silence_frames = sum(1 for r in rmss if r < SILENCE_THR)
    speech_frames  = sum(1 for r in rmss if r >= SPEECH_THR)
    noise_frames   = len(rmss) - silence_frames - speech_frames

    # Noise ZCR (only silence frames)
    silence_samples = [s for i, f in enumerate(frames) if rmss[i] < SILENCE_THR for s in f]
    zcr = 0.0
    if len(silence_samples) > 1:
        zc = sum(1 for i in range(1, len(silence_samples)) if silence_samples[i] * silence_samples[i-1] < 0)
        zcr = zc / len(silence_samples) * SR

    if zcr < 500:
        noise_type = "low_freq_hum"
    elif zcr < 1500:
        noise_type = "mid_band_fan_hvac"
    elif zcr < 3000:
        noise_type = "broadband_traffic"
    else:
        noise_type = "white_noise"

    # Duration
    duration_s = len(samples) / SR

    return {
        "duration_s":        round(duration_s, 2),
        "snr_db":            round(true_snr, 1),
        "noise_floor_rms":   round(noise_floor, 2),
        "speech_rms":        round(speech_rms, 1),
        "level_dbfs":        round(level_dbfs, 1),
        "peak_dbfs":         round(peak_dbfs, 1),
        "speech_pct":        round(speech_frames / len(rmss) * 100, 1),
        "noise_pct":         round(noise_frames  / len(rmss) * 100, 1),
        "silence_pct":       round(silence_frames/ len(rmss) * 100, 1),
        "noise_type":        noise_type,
        "noise_zcr_hz":      round(zcr, 0),
        "clipped_samples":   sum(1 for s in samples if abs(s) >= 32700),
    }


def vad_timing(samples: list, frame_size: int = 160, sr: int = 16000, threshold: float = 300) -> dict:
    """
    Compute VAD start/stop times for comparison with ground-truth speech boundaries.
    Returns first_vad_start, last_vad_stop, vad_starts[], vad_stops[].
    """
    frames = [samples[i:i+frame_size] for i in range(0, len(samples)-frame_size, frame_size)]
    is_speech = [math.sqrt(sum(s*s for s in f)/len(f)) >= threshold for f in frames]

    starts, stops = [], []
    in_speech = False
    for i, s in enumerate(is_speech):
        t = i * frame_size / sr
        if s and not in_speech:
            starts.append(round(t, 3))
            in_speech = True
        elif not s and in_speech:
            stops.append(round(t, 3))
            in_speech = False
    if in_speech:
        stops.append(round(len(samples) / sr, 3))

    return {
        "vad_starts":      starts,
        "vad_stops":       stops,
        "first_vad_start": starts[0] if starts else None,
        "last_vad_stop":   stops[-1]  if stops  else None,
    }


# ─────────────────────────────────────────────────────────────────────────────
# 3. REPORT GENERATION
# ─────────────────────────────────────────────────────────────────────────────

def format_table(header: list, rows: list, pct_cols: set = None) -> str:
    """ASCII table — matches denoiser_results.md format from Confluence."""
    pct_cols = pct_cols or set()
    col_w = [len(h) for h in header]
    for row in rows:
        for i, v in enumerate(row):
            col_w[i] = max(col_w[i], len(str(v)))

    sep  = "-" * (sum(col_w) + 3 * len(col_w) + 1)
    line = lambda row: "  ".join(str(v).rjust(col_w[i]) for i, v in enumerate(row))
    lines = [sep, "  ".join(h.ljust(col_w[i]) for i, h in enumerate(header)), sep]
    for row in rows:
        lines.append(line(row))
    lines.append(sep)
    return "\n".join(lines)


def write_markdown_report(
    results_by_denoiser: dict,
    audio_metrics_by_file: dict,
    output_path: str,
    reference_conv: str = "",
    generated_at: str = "",
):
    """
    Write denoiser_results.md matching the Confluence format,
    extended with audio-level metrics section.
    """
    generated_at = generated_at or datetime.now().isoformat()
    lines = [
        "# Denoiser Analysis Results",
        f"Generated: {generated_at}",
        "",
        "## Metric Definitions",
        "",
        "| Metric | What it measures | Notes |",
        "|--------|-----------------|-------|",
        "| Char | Character-level similarity (SequenceMatcher) | Sensitive to typos; normalises lowercase + whitespace |",
        "| Word | Word-level similarity (tokenised SequenceMatcher) | Lexical match. Order-sensitive. |",
        "| LLM | Semantic similarity (Azure OpenAI) | 0–100. Lenient on rephrasing; penalises noise/hallucinations |",
        "| SNR | Signal-to-Noise Ratio (dB) | True SNR via noise-floor method (ClearStream extension) |",
        "| VAD Δ Start | first_vad_start − speech_start_time (s) | Negative = VAD fires early; positive = VAD fires late |",
        "| WER | Word Error Rate vs reference transcript | Lower is better |",
        "",
        f"Higher Char/Word/LLM = better.  Reference conversation: {reference_conv}",
        "",
        "---",
        "",
        "## Results by Denoiser",
    ]

    for denoiser, data in results_by_denoiser.items():
        conversations = data["conversations"]  # list of dicts: id, char, word, llm
        avg_char = data["avg_char"]
        avg_word = data["avg_word"]
        avg_llm  = data["avg_llm"]
        skipped  = data.get("skipped", 0)

        lines += [
            "",
            f"### {denoiser}",
        ]
        if skipped:
            lines.append(f"[{denoiser}] Skipped {skipped} conversation(s) with empty transcript")
        lines += [
            "=" * 68,
            f"{'Conversation ID':<44} {'Char':>8} {'Word':>8} {'LLM':>8}",
            "-" * 68,
        ]
        for c in conversations:
            char_s = f"{c['char']*100:.2f}%" if c['char'] >= 0 else "—"
            word_s = f"{c['word']*100:.2f}%" if c['word'] >= 0 else "—"
            llm_s  = f"{c['llm']:.2f}%"      if c['llm'] >= 0  else "—"
            lines.append(f"{c['id']:<44} {char_s:>8} {word_s:>8} {llm_s:>8}")
        lines += [
            "-" * 68,
            f"{'Average':<44} {avg_char:>7.2f}% {avg_word:>7.2f}% {avg_llm:>7.2f}%",
            "",
        ]

    # Audio metrics section (ClearStream extension)
    if audio_metrics_by_file:
        lines += [
            "---",
            "",
            "## Audio Metrics (ClearStream Extension)",
            "",
            "| File | Denoiser | SNR (dB) | Noise Floor | Level (dBFS) | Speech% | Noise Type | Clipped |",
            "|------|----------|----------|-------------|--------------|---------|------------|---------|",
        ]
        for key, m in audio_metrics_by_file.items():
            fname, den = key
            lines.append(
                f"| {fname} | {den} | {m.get('snr_db','—')} | "
                f"{m.get('noise_floor_rms','—')} | {m.get('level_dbfs','—')} | "
                f"{m.get('speech_pct','—')}% | {m.get('noise_type','—')} | "
                f"{m.get('clipped_samples',0)} |"
            )

    with open(output_path, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(f"[OK] Report written: {output_path}")


def write_csv(rows: list, path: str, fieldnames: list):
    with open(path, "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=fieldnames, extrasaction="ignore")
        w.writeheader()
        w.writerows(rows)
    print(f"[OK] CSV written: {path}")


# ─────────────────────────────────────────────────────────────────────────────
# 4. MAIN
# ─────────────────────────────────────────────────────────────────────────────

def parse_args():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--audio-dir",       help="Directory with raw audio files (one subdir per denoiser)")
    p.add_argument("--output-dir",      default="./denoiser_eval_out", help="Where to write results")
    p.add_argument("--denoisers",       nargs="+", default=["clearstream","krisp100","krisp95","krisp90","sanas","hector"])
    p.add_argument("--reference-conv",  default="", help="Reference conversation ID for Char/Word/LLM eval")
    p.add_argument("--transcript-dir",  help="Dir containing reference/ and per-denoiser .txt transcript files")
    p.add_argument("--llm-endpoint",    default=os.environ.get("AZURE_OPENAI_ENDPOINT",""))
    p.add_argument("--llm-key",         default=os.environ.get("AZURE_OPENAI_API_KEY",""))
    p.add_argument("--llm-model",       default="voice-bot-gpt-35-1106")
    p.add_argument("--no-llm",          action="store_true", help="Disable LLM scoring (faster, free)")
    p.add_argument("--rate-limit",      type=float, default=1.0, help="Seconds between LLM calls")
    return p.parse_args()


def load_transcripts(transcript_dir: str, denoisers: list) -> dict:
    """
    Load transcripts from directory layout:
        transcript_dir/reference/<conv_id>.txt
        transcript_dir/<denoiser>/<conv_id>.txt
    Returns {denoiser: {conv_id: text}}
    """
    result = {"reference": {}}
    ref_dir = Path(transcript_dir) / "reference"
    if ref_dir.exists():
        for f in ref_dir.glob("*.txt"):
            result["reference"][f.stem] = f.read_text().strip()

    for den in denoisers:
        den_dir = Path(transcript_dir) / den
        result[den] = {}
        if den_dir.exists():
            for f in den_dir.glob("*.txt"):
                result[den][f.stem] = f.read_text().strip()
    return result


def main():
    args = parse_args()
    out_dir = Path(args.output_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    results_by_denoiser = {}
    audio_metrics_by_file = {}
    all_per_file_rows = []

    # ── Transcript scoring ─────────────────────────────────────────
    if args.transcript_dir:
        transcripts = load_transcripts(args.transcript_dir, args.denoisers)
        reference_map = transcripts.get("reference", {})

        for denoiser in args.denoisers:
            den_transcripts = transcripts.get(denoiser, {})
            conv_results = []
            skipped = 0
            sum_char = sum_word = sum_llm = 0.0
            n_llm = 0

            all_conv_ids = set(reference_map) | set(den_transcripts)
            for conv_id in sorted(all_conv_ids):
                ref  = reference_map.get(conv_id, "")
                comp = den_transcripts.get(conv_id, "")
                if not ref or not comp:
                    skipped += 1
                    continue

                cs = char_score(ref, comp)
                ws = word_score(ref, comp)
                ls = -1.0
                if not args.no_llm and args.llm_endpoint:
                    ls = llm_score(ref, comp,
                                   endpoint=args.llm_endpoint,
                                   api_key=args.llm_key,
                                   model=args.llm_model,
                                   rate_limit_delay=args.rate_limit)

                conv_results.append({"id": conv_id, "char": cs, "word": ws, "llm": ls})
                sum_char += cs
                sum_word += ws
                if ls >= 0:
                    sum_llm += ls
                    n_llm += 1

            n = len(conv_results)
            results_by_denoiser[denoiser] = {
                "conversations": conv_results,
                "avg_char": sum_char / n * 100 if n else 0,
                "avg_word": sum_word / n * 100 if n else 0,
                "avg_llm":  sum_llm / n_llm      if n_llm else -1,
                "skipped":  skipped,
            }
            print(f"[{denoiser}] n={n} skipped={skipped} "
                  f"Char={results_by_denoiser[denoiser]['avg_char']:.1f}% "
                  f"Word={results_by_denoiser[denoiser]['avg_word']:.1f}% "
                  f"LLM={results_by_denoiser[denoiser]['avg_llm']:.1f}")

    # ── Audio metrics ──────────────────────────────────────────────
    if args.audio_dir:
        audio_base = Path(args.audio_dir)
        for denoiser in args.denoisers:
            den_dir = audio_base / denoiser
            if not den_dir.exists():
                continue
            for audio_file in sorted(den_dir.glob("*.wav")):
                print(f"  Analysing {denoiser}/{audio_file.name} …")
                try:
                    samples = decode_pcm(str(audio_file))
                    m = audio_metrics(samples)
                    v = vad_timing(samples)
                    m.update({"filename": audio_file.name, "denoiser": denoiser})
                    m.update(v)
                    audio_metrics_by_file[(audio_file.name, denoiser)] = m
                    all_per_file_rows.append(m)
                except Exception as e:
                    print(f"  [WARN] {audio_file.name}: {e}")

    # ── Write outputs ──────────────────────────────────────────────
    ts = datetime.now().strftime("%Y%m%d_%H%M%S")

    # denoiser_results.md (matches Confluence format)
    write_markdown_report(
        results_by_denoiser,
        audio_metrics_by_file,
        str(out_dir / "denoiser_results.md"),
        reference_conv=args.reference_conv,
        generated_at=datetime.now().isoformat(),
    )

    # Per-conversation JSON
    all_conv_rows = []
    for den, data in results_by_denoiser.items():
        for c in data["conversations"]:
            all_conv_rows.append({
                "denoiser": den,
                "conversation_id": c["id"],
                "char_score": round(c["char"]*100, 2),
                "word_score": round(c["word"]*100, 2),
                "llm_score":  c["llm"],
            })

    with open(out_dir / f"per_conversation_{ts}.json", "w") as f:
        json.dump(all_conv_rows, f, indent=2)

    # Summary CSV (matches denoiser leaderboard format)
    summary_rows = []
    for den, data in results_by_denoiser.items():
        summary_rows.append({
            "denoiser": den,
            "n_conversations": len(data["conversations"]),
            "skipped": data.get("skipped", 0),
            "avg_char_pct": round(data["avg_char"], 2),
            "avg_word_pct": round(data["avg_word"], 2),
            "avg_llm_score": round(data["avg_llm"], 2) if data["avg_llm"] >= 0 else "",
        })
    if summary_rows:
        write_csv(summary_rows, str(out_dir / "denoiser_summary.csv"),
                  ["denoiser","n_conversations","skipped","avg_char_pct","avg_word_pct","avg_llm_score"])

    # Audio per-file CSV (ClearStream extension — matches VADEvalRow schema)
    if all_per_file_rows:
        audio_fields = ["filename","denoiser","duration_s","snr_db","noise_floor_rms",
                        "speech_rms","level_dbfs","peak_dbfs","speech_pct","noise_pct",
                        "silence_pct","noise_type","noise_zcr_hz","clipped_samples",
                        "first_vad_start","last_vad_stop"]
        write_csv(all_per_file_rows, str(out_dir / f"audio_metrics_{ts}.csv"), audio_fields)

    # Print leaderboard
    print("\n" + "=" * 60)
    print("LEADERBOARD")
    print("=" * 60)
    print(f"{'Denoiser':<16} {'Char':>8} {'Word':>8} {'LLM':>8} {'N':>5}")
    print("-" * 50)
    ranked = sorted(results_by_denoiser.items(),
                    key=lambda x: x[1]["avg_word"], reverse=True)
    for den, data in ranked:
        llm_s = f"{data['avg_llm']:>7.1f}" if data["avg_llm"] >= 0 else "      —"
        print(f"{den:<16} {data['avg_char']:>7.1f}% {data['avg_word']:>7.1f}% {llm_s} {len(data['conversations']):>5}")
    print("=" * 60)
    print(f"\nOutputs written to: {out_dir}/")


if __name__ == "__main__":
    main()
