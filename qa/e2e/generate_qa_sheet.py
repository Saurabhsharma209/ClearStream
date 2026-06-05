#!/usr/bin/env python3
"""
generate_qa_sheet.py — Combine metrics JSONL + transcript_eval.json into a
single QA summary sheet (JSON + optional CSV).

CS-T01: Aggregates per-call proxy metrics with per-preset transcript quality
scores so the QA sheet shows SNR, RMS, clip_count, Char, Word, LLM in one row
per preset per run — enabling config ranking and regression detection.

Usage:
    python3 qa/e2e/generate_qa_sheet.py \\
        --results-dir eval_out/20260605_152104 \\
        [--out qa_sheet.json] \\
        [--csv qa_sheet.csv]
"""
import argparse
import csv
import json
import sys
from pathlib import Path


def load_jsonl(path: Path) -> list[dict]:
    if not path.exists():
        return []
    rows = []
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            rows.append(json.loads(line))
        except json.JSONDecodeError as exc:
            print(f"[qa_sheet] WARN bad JSONL line in {path}: {exc}", file=sys.stderr)
    return rows


def load_json(path: Path) -> dict:
    if not path.exists():
        return {}
    try:
        return json.loads(path.read_text())
    except Exception as exc:
        print(f"[qa_sheet] WARN {path}: {exc}", file=sys.stderr)
        return {}


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--results-dir", required=True)
    p.add_argument("--out", default="", help="Output JSON path (default: <results-dir>/qa_sheet.json)")
    p.add_argument("--csv", default="", help="Optional CSV output path")
    args = p.parse_args()

    results_dir = Path(args.results_dir)
    if not results_dir.is_dir():
        print(f"[qa_sheet] ERROR: --results-dir {results_dir} does not exist", file=sys.stderr)
        return 1

    # Load proxy JSONL (per-call metrics from run_tier.sh).
    metrics_rows = load_jsonl(results_dir / "metrics.jsonl")

    # Load transcript eval sidecar (from transcript_proxy_eval.py).
    transcript_report = load_json(results_dir / "transcript_eval.json")
    transcript_by_label = {
        row["label"]: row
        for row in transcript_report.get("rows", [])
    }

    # Aggregate metrics per tier.
    from collections import defaultdict
    by_tier: dict = defaultdict(lambda: {
        "snr_lines": [],
        "latency_p50_us": [],
        "latency_p99_us": [],
        "wer": [],
    })
    for row in metrics_rows:
        tier = row.get("tier", "unknown")
        by_tier[tier]["snr_lines"].append(row.get("snr", ""))
        p50 = row.get("latency_p50_us")
        p99 = row.get("latency_p99_us")
        if p50 and p50 != "N/A":
            try:
                by_tier[tier]["latency_p50_us"].append(float(p50))
            except ValueError:
                pass
        if p99 and p99 != "N/A":
            try:
                by_tier[tier]["latency_p99_us"].append(float(p99))
            except ValueError:
                pass
        w = row.get("wer")
        if w is not None:
            by_tier[tier]["wer"].append(w)

    def avg(lst: list) -> float | None:
        return sum(lst) / len(lst) if lst else None

    sheet_rows = []
    all_tiers = sorted(set(list(by_tier.keys()) + [r["label"] for r in transcript_report.get("rows", [])]))
    for tier in all_tiers:
        m = by_tier.get(tier, {})
        t = transcript_by_label.get(tier, {})
        row = {
            "tier": tier,
            "snr": m.get("snr_lines", [""])[0] if m.get("snr_lines") else None,
            "latency_p50_us_avg": avg(m.get("latency_p50_us", [])),
            "latency_p99_us_avg": avg(m.get("latency_p99_us", [])),
            "wer_avg": avg([w for w in m.get("wer", []) if isinstance(w, (int, float))]),
            "char_accuracy": t.get("char_accuracy"),
            "word_accuracy": t.get("word_accuracy"),
            "llm_semantic": t.get("llm_semantic"),
            "transcript_gate": t.get("gate", "N/A"),
            "hypothesis_excerpt": (t.get("hypothesis", "") or "")[:120],
        }
        sheet_rows.append(row)

    # QA gate summary.
    transcript_pass = transcript_report.get("pass", None)
    failures = transcript_report.get("failures", [])
    summary = {
        "results_dir": str(results_dir),
        "total_tiers": len(sheet_rows),
        "transcript_gate_pass": transcript_pass,
        "transcript_failures": failures,
        "rows": sheet_rows,
    }

    # Write JSON.
    out_path = Path(args.out) if args.out else results_dir / "qa_sheet.json"
    out_path.write_text(json.dumps(summary, indent=2))
    print(f"[qa_sheet] wrote {out_path}")

    # Write CSV.
    csv_path = Path(args.csv) if args.csv else None
    if csv_path:
        fields = [
            "tier", "snr", "latency_p50_us_avg", "latency_p99_us_avg", "wer_avg",
            "char_accuracy", "word_accuracy", "llm_semantic",
            "transcript_gate", "hypothesis_excerpt",
        ]
        with csv_path.open("w", newline="") as f:
            w = csv.DictWriter(f, fieldnames=fields, extrasaction="ignore")
            w.writeheader()
            w.writerows(sheet_rows)
        print(f"[qa_sheet] wrote {csv_path}")

    # Print summary.
    print(f"\n{'='*60}")
    print(f"QA Sheet — {results_dir.name}")
    print(f"{'='*60}")
    for row in sheet_rows:
        char = f"{row['char_accuracy']:.3f}" if row['char_accuracy'] is not None else "N/A"
        word = f"{row['word_accuracy']:.3f}" if row['word_accuracy'] is not None else "N/A"
        gate = row["transcript_gate"]
        print(f"  {row['tier']:20s}  Char={char}  Word={word}  [{gate}]")

    if transcript_pass is False:
        print(f"\nFAIL: transcript regressions in: {', '.join(failures)}")
        return 1
    elif transcript_pass is True:
        print("\nPASS: all presets within transcript tolerance")
    else:
        print("\nINFO: transcript gate not evaluated (no reference transcript)")

    print(f"{'='*60}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
