#!/usr/bin/env python3
"""Offline WER evaluation for golden phrases (requires jiwer + faster-whisper)."""

import argparse
import json
import subprocess
import sys
from pathlib import Path

try:
    import jiwer
except ImportError:
    print("pip install jiwer", file=sys.stderr)
    sys.exit(1)


GOLDEN = Path(__file__).parent / "golden_phrases.json"


def transcribe_wav(wav: Path, model: str = "tiny") -> str:
    from faster_whisper import WhisperModel

    import numpy as np
    import wave

    with wave.open(str(wav), "rb") as wf:
        sr = wf.getframerate()
        frames = wf.readframes(wf.getnframes())
    samples = np.frombuffer(frames, dtype=np.int16).astype("float32") / 32768.0
    if sr != 16000:
        # simple decimation for POC
        step = max(1, sr // 16000)
        samples = samples[::step]

    m = WhisperModel(model, device="cpu", compute_type="int8")
    segs, _ = m.transcribe(samples, language="en", beam_size=1)
    return " ".join(s.text.strip() for s in segs).strip().lower()


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--results-dir", required=True)
    p.add_argument("--whisper-model", default="tiny")
    args = p.parse_args()

    out_dir = Path(args.results_dir)
    golden = json.loads(GOLDEN.read_text())
    references = [g["text"].lower() for g in golden["phrases"]]

    # For POC we score one combined WAV per condition against first reference phrase
    # Full golden-set recording workflow is documented in README.
    rows = []
    for wav in sorted(out_dir.glob("condition_*.wav")):
        hyp = transcribe_wav(wav, args.whisper_model)
        ref = references[0]
        wer = jiwer.wer(ref, hyp) if hyp else 1.0
        rows.append({"file": wav.name, "reference": ref, "hypothesis": hyp, "wer": wer})

    baseline = next((r["wer"] for r in rows if "A" in r["file"]), None)
    report = {"wer_rows": rows, "baseline_wer": baseline}
    if baseline and baseline > 0:
        for r in rows:
            r["relative_improvement"] = (baseline - r["wer"]) / baseline

    out_path = out_dir / "wer.json"
    out_path.write_text(json.dumps(report, indent=2))
    print(f"wrote {out_path}")


if __name__ == "__main__":
    main()
