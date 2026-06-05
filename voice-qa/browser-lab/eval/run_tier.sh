#!/usr/bin/env bash
# run_tier.sh — run one NR tier eval and APPEND results to the shared metrics JSONL.
#
# CS-006 fix: the previous version used ">" (truncate) when rotating the JSONL
# file while other processes still held open file descriptors to it.  On Linux
# this produces a zero-byte "file" that other writers continue appending to an
# unlinked inode, silently losing all earlier data.  This script uses ">>" (append)
# for the live JSONL and a safe offline rotation strategy: copy-then-truncate
# (with tail -n +N) so readers always see a consistent file.
#
# Usage:
#   run_tier.sh <tier> <model> <metrics_jsonl> [<noisy_wav>]
#
# Arguments:
#   tier          Tier label written into every JSONL record (e.g. "L0", "L1", "L2")
#   model         NR backend: passthrough | rnnoise | deepfilter
#   metrics_jsonl Absolute path to the shared .jsonl metrics file
#   noisy_wav     Source WAV (default: testdata/noisy.wav)
#
# Example:
#   run_tier.sh L1 rnnoise /tmp/eval/metrics.jsonl testdata/noisy.wav
set -euo pipefail

TIER="${1:?Usage: run_tier.sh <tier> <model> <metrics_jsonl> [<noisy_wav>]}"
MODEL="${2:?Usage: run_tier.sh <tier> <model> <metrics_jsonl> [<noisy_wav>]}"
METRICS_JSONL="${3:?Usage: run_tier.sh <tier> <model> <metrics_jsonl> [<noisy_wav>]}"
NOISY_WAV="${4:-testdata/noisy.wav}"

STAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
TMPDIR_TIER="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_TIER"' EXIT

OUT_WAV="$TMPDIR_TIER/clean_${TIER}.wav"
LATENCY_JSON="$TMPDIR_TIER/latency_${TIER}.json"

# ── 1. Noise reduction ────────────────────────────────────────────────────────
echo "[run_tier] ${TIER} model=${MODEL} noisy=${NOISY_WAV}"
if [[ "$MODEL" == "passthrough" ]]; then
    cp "$NOISY_WAV" "$OUT_WAV"
else
    ./clearstream file -i "$NOISY_WAV" -o "$OUT_WAV" --model "$MODEL"
fi

# ── 2. SNR measurement ────────────────────────────────────────────────────────
SNR_LINE="$(go run tools/snr_benchmark/main.go -noisy "$NOISY_WAV" -clean "$OUT_WAV" 2>&1 | tail -1 || echo 'snr=N/A')"
echo "  [snr] ${SNR_LINE}"

# ── 3. Latency measurement ────────────────────────────────────────────────────
go run tools/latency_harness/main.go \
    -frames 10000 \
    -backend "$MODEL" \
    -json "$LATENCY_JSON" 2>/dev/null || echo '{}' > "$LATENCY_JSON"

P50="$(python3 -c "import json,sys; d=json.load(open('$LATENCY_JSON')); print(d.get('p50_us','N/A'))" 2>/dev/null || echo N/A)"
P99="$(python3 -c "import json,sys; d=json.load(open('$LATENCY_JSON')); print(d.get('p99_us','N/A'))" 2>/dev/null || echo N/A)"

# ── 4. WER (optional — needs orchestrator venv with faster-whisper) ───────────
WER="null"
if [[ -x "$(command -v python3)" ]] && [[ -f "voice-qa/browser-lab/eval/wer_eval.py" ]]; then
    WER="$(python3 voice-qa/browser-lab/eval/wer_eval.py \
             --wav "$OUT_WAV" \
             --phrases voice-qa/browser-lab/eval/golden_phrases.json \
             --json 2>/dev/null | python3 -c "import json,sys; print(json.load(sys.stdin).get('wer','null'))" 2>/dev/null || echo null)"
fi

# ── 5. Build JSONL record ─────────────────────────────────────────────────────
RECORD="$(python3 - <<PYEOF
import json, sys
rec = {
    "timestamp":  "$STAMP",
    "tier":       "$TIER",
    "model":      "$MODEL",
    "noisy_wav":  "$NOISY_WAV",
    "snr":        "$SNR_LINE",
    "latency_p50_us": "$P50",
    "latency_p99_us": "$P99",
    "wer":        $WER,
}
print(json.dumps(rec))
PYEOF
)"

# ── 6. Append — NEVER truncate — to the shared JSONL ────────────────────────
# CS-006 root cause: using ">" truncates the inode while other processes still
# hold open FDs pointing to it.  Those writers see file-offset 0 and overwrite
# what was just written, silently losing data.  ">>" is safe: it always seeks
# to EOF before writing, so concurrent appenders produce interleaved-but-intact
# JSONL lines (each line is a single write(2) call, which is atomic for small
# payloads on Linux ext4/xfs).
mkdir -p "$(dirname "$METRICS_JSONL")"
echo "$RECORD" >> "$METRICS_JSONL"
echo "[run_tier] appended record to $METRICS_JSONL"

# ── 7. Safe rotation (optional) ───────────────────────────────────────────────
# If the caller sets ROTATE_AFTER_LINES (e.g. ROTATE_AFTER_LINES=1000), rotate
# the JSONL by copying all lines except the first N to a new file, then
# replacing the original.  This avoids the FD-leak truncation problem by using
# a rename (atomic on POSIX) instead of truncate.
ROTATE_AFTER_LINES="${ROTATE_AFTER_LINES:-0}"
if [[ "$ROTATE_AFTER_LINES" -gt 0 ]]; then
    LINE_COUNT="$(wc -l < "$METRICS_JSONL" || echo 0)"
    if [[ "$LINE_COUNT" -gt "$ROTATE_AFTER_LINES" ]]; then
        KEEP=$((LINE_COUNT - ROTATE_AFTER_LINES))
        ROTATED="${METRICS_JSONL%.jsonl}_$(date -u +%Y%m%d_%H%M%S).jsonl"
        # Archive the old lines.
        head -n "$ROTATE_AFTER_LINES" "$METRICS_JSONL" > "$ROTATED"
        # Keep only the tail lines — tail -n +N reads from line N to EOF
        # (does NOT truncate; writes to a temp file first, then renames).
        TMPFILE="$(mktemp "${METRICS_JSONL}.XXXXXX")"
        tail -n +"$((ROTATE_AFTER_LINES + 1))" "$METRICS_JSONL" > "$TMPFILE"
        mv "$TMPFILE" "$METRICS_JSONL"
        echo "[run_tier] rotated ${ROTATE_AFTER_LINES} lines → $ROTATED (kept ${KEEP})"
    fi
fi
