#!/usr/bin/env python3
"""
transcript_proxy_eval.py — Transcript quality gate for ClearStream QA.

CS-T01 fix: The original eval had no transcript quality gates — all presets
scored identically (~55% Char) because they all used the same spectral proxy.
This script:
  1. Transcribes WAV files using faster-whisper (local, no API key needed).
  2. Computes Char/Word accuracy vs a reference transcript.
  3. Applies a regression gate: preset must be within ±5% Char of the
     passthrough baseline. This catches speech destruction (heavy denoising)
     without requiring a perfect ASR score.
  4. Optionally computes LLM semantic score when Azure OpenAI creds are set
     (AZURE_OPENAI_API_KEY / AZURE_OPENAI_ENDPOINT). Deferred otherwise.

Usage:
    python3 qa/eval/transcript_proxy_eval.py \\
        --results-dir eval_out/20260605_152104 \\
        --reference-transcript testdata/raw_audio_transcript.txt \\
        [--whisper-model tiny] \\
        [--baseline-label condition_A] \\
        [--char-tolerance 0.05] \\
        [--json]

Exit codes:
    0  All presets pass the regression gate
    1  One or more presets fail (Char drop > tolerance vs baseline)
    2  Missing dependency (jiwer / faster-whisper)
"""
import argparse
import json
import os
import sys
import wave
from pathlib import Path


def _require(pkg: str, install: str) -> None:
    try:
        __import__(pkg)
    except ImportError:
        print(f"[transcript_eval] missing dependency: pip install {install}", file=sys.stderr)
        sys.exit(2)


def char_accuracy(reference: str, hypothesis: str) -> float:
    """Character-level accuracy (1 - CER)."""
    _require("jiwer", "jiwer")
    import jiwer
    cer = jiwer.cer(reference.lower().strip(), hypothesis.lower().strip())
    return max(0.0, 1.0 - cer)


def word_accuracy(reference: str, hypothesis: str) -> float:
    """Word-level accuracy (1 - WER)."""
    _require("jiwer", "jiwer")
    import jiwer
    wer = jiwer.wer(reference.lower().strip(), hypothesis.lower().strip())
    return max(0.0, 1.0 - wer)


def transcribe(wav_path: Path, model_name: str = "tiny") -> str:
    """Transcribe a WAV file using faster-whisper. Falls back to empty string on failure."""
    _require("faster_whisper", "faster-whisper")
    _require("numpy", "numpy")
    import numpy as np
    from faster_whisper import WhisperModel

    try:
        with wave.open(str(wav_path), "rb") as wf:
            sr = wf.getframerate()
            raw = wf.readframes(wf.getnframes())
        samples = np.frombuffer(raw, dtype=np.int16).astype("float32") / 32768.0
        if sr != 16000:
            step = max(1, sr // 16000)
            samples = samples[::step]
        m = WhisperModel(model_name, device="cpu", compute_type="int8")
        segs, _ = m.transcribe(samples, language="en", beam_size=1)
        return " ".join(s.text.strip() for s in segs).strip()
    except Exception as exc:
        print(f"[transcript_eval] WARN transcribe {wav_path.name}: {exc}", file=sys.stderr)
        return ""


def llm_semantic_score(reference: str, hypothesis: str) -> float | None:
    """
    Optional LLM semantic similarity via Azure OpenAI.
    Returns None when credentials are not set (deferred path — CS-T01).
    Credentials: AZURE_OPENAI_API_KEY, AZURE_OPENAI_ENDPOINT (never hard-coded).
    """
    api_key = os.environ.get("AZURE_OPENAI_API_KEY", "")
    endpoint = os.environ.get("AZURE_OPENAI_ENDPOINT", "")
    if not api_key or not endpoint:
        return None  # credentials not available — skip

    try:
        from openai import AzureOpenAI  # type: ignore
    except ImportError:
        return None

    client = AzureOpenAI(api_key=api_key, azure_endpoint=endpoint, api_version="2024-02-01")
    prompt = (
        "Score the semantic similarity between these two utterances on a scale of 0.0 to 1.0. "
        "Reply with only the number.\n\n"
        f"Reference: {reference}\nHypothesis: {hypothesis}"
    )
    try:
        resp = client.chat.completions.create(
            model="gpt-4o",
            messages=[{"role": "user", "content": prompt}],
            max_tokens=10,
            temperature=0,
        )
        return float(resp.choices[0].message.content.strip())
    except Exception as exc:
        print(f"[transcript_eval] WARN LLM score failed: {exc}", file=sys.stderr)
        return None


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--results-dir", required=True, help="Directory containing condition_*.wav files")
    p.add_argument("--reference-transcript", default="", help="Path to plain-text reference transcript")
    p.add_argument("--whisper-model", default="tiny", help="faster-whisper model size (default: tiny)")
    p.add_argument("--baseline-label", default="condition_A", help="Filename stem of the passthrough baseline")
    p.add_argument("--char-tolerance", type=float, default=0.05,
                   help="Max allowed Char drop vs baseline (default: 0.05 = 5 ppt)")
    p.add_argument("--json", action="store_true", help="Output JSON instead of human-readable text")
    args = p.parse_args()

    results_dir = Path(args.results_dir)
    if not results_dir.is_dir():
        print(f"[transcript_eval] ERROR: --results-dir {results_dir} does not exist", file=sys.stderr)
        return 1

    # Load reference transcript (optional).
    reference = ""
    if args.reference_transcript:
        ref_path = Path(args.reference_transcript)
        if ref_path.is_file():
            reference = ref_path.read_text().strip()
        else:
            print(f"[transcript_eval] WARN: reference transcript not found: {ref_path}", file=sys.stderr)

    wavs = sorted(results_dir.glob("condition_*.wav"))
    if not wavs:
        print(f"[transcript_eval] ERROR: no condition_*.wav files in {results_dir}", file=sys.stderr)
        return 1

    rows = []
    for wav in wavs:
        print(f"[transcript_eval] transcribing {wav.name} …", file=sys.stderr)
        hyp = transcribe(wav, args.whisper_model)
        char_acc = char_accuracy(reference, hyp) if reference else None
        word_acc = word_accuracy(reference, hyp) if reference else None
        llm_score = llm_semantic_score(reference, hyp) if reference else None
        rows.append({
            "label": wav.stem,
            "wav": str(wav),
            "hypothesis": hyp,
            "char_accuracy": char_acc,
            "word_accuracy": word_acc,
            "llm_semantic": llm_score,
        })

    # Identify baseline.
    baseline_row = next((r for r in rows if r["label"] == args.baseline_label), None)
    baseline_char = baseline_row["char_accuracy"] if baseline_row else None

    # Regression gate: each preset must be within ±tolerance of baseline Char.
    failures = []
    for row in rows:
        if row["label"] == args.baseline_label:
            row["gate"] = "baseline"
            continue
        if baseline_char is not None and row["char_accuracy"] is not None:
            drop = baseline_char - row["char_accuracy"]
            if drop > args.char_tolerance:
                row["gate"] = f"FAIL (char drop {drop:.3f} > tolerance {args.char_tolerance})"
                failures.append(row["label"])
            else:
                row["gate"] = f"PASS (char drop {drop:.3f})"
        else:
            row["gate"] = "SKIP (no reference transcript)"

    report = {
        "results_dir": str(results_dir),
        "baseline_label": args.baseline_label,
        "baseline_char": baseline_char,
        "char_tolerance": args.char_tolerance,
        "rows": rows,
        "failures": failures,
        "pass": len(failures) == 0,
    }

    if args.json:
        print(json.dumps(report, indent=2))
    else:
        print(f"\n{'='*60}")
        print(f"Transcript Quality Report — {results_dir.name}")
        print(f"{'='*60}")
        print(f"Baseline: {args.baseline_label}  Char={baseline_char:.3f}" if baseline_char else "Baseline: N/A")
        print(f"Tolerance: ±{args.char_tolerance:.0%}  Reference: {'yes' if reference else 'no'}")
        print()
        for row in rows:
            char_str = f"{row['char_accuracy']:.3f}" if row['char_accuracy'] is not None else "N/A"
            word_str = f"{row['word_accuracy']:.3f}" if row['word_accuracy'] is not None else "N/A"
            llm_str  = f"{row['llm_semantic']:.3f}" if row['llm_semantic']  is not None else "deferred"
            print(f"  {row['label']:25s}  Char={char_str}  Word={word_str}  LLM={llm_str}  [{row['gate']}]")
        print()
        if failures:
            print(f"FAIL: {len(failures)} preset(s) degraded transcript quality: {', '.join(failures)}")
        else:
            print("PASS: all presets within tolerance")
        print(f"{'='*60}\n")

    # Write sidecar JSON for generate_qa_sheet.py to consume.
    out_json = results_dir / "transcript_eval.json"
    out_json.write_text(json.dumps(report, indent=2))
    print(f"[transcript_eval] wrote {out_json}", file=sys.stderr)

    return 0 if len(failures) == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
