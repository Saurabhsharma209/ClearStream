#!/usr/bin/env bash
# Run A/B/C evaluation matrix and write browser-lab/results/<timestamp>/metrics.json
set -euo pipefail

LAB="$(cd "$(dirname "$0")/.." && pwd)"
VOICE_QA_ROOT="$(cd "$LAB/.." && pwd)"
SETUP_DIR="$VOICE_QA_ROOT/setup"
# shellcheck source=/dev/null
[[ -f "$SETUP_DIR/env.local" ]] && source "$SETUP_DIR/env.local"
ROOT="${CLEARSTREAM_ROOT:-$(cd "$LAB/../../ClearStream-1" && pwd)}"
cd "$ROOT"

STAMP="$(date +%Y%m%d_%H%M%S)"
OUT="$LAB/results/${STAMP}"
mkdir -p "$OUT"

echo "=== ClearStream eval matrix -> $OUT ==="

if [[ ! -f testdata/noisy.wav ]]; then
  echo "[gen] testdata/noisy.wav"
  go run testdata/generate_noisy.go || true
fi

BINARY="${ROOT}/clearstream"
if [[ ! -x "$BINARY" ]]; then
  go build -o "$BINARY" ./cmd/clearstream/
fi

NOISY="testdata/noisy.wav"
[[ -f "$NOISY" ]] || NOISY="testdata/sample_noisy.wav"

run_condition() {
  local id="$1"
  local model="$2"
  local out_wav="$OUT/condition_${id}.wav"

  echo "[${id}] model=${model}"
  if [[ "$id" == "A" ]]; then
    cp "$NOISY" "$out_wav"
  else
    "$BINARY" file -i "$NOISY" -o "$out_wav" --model "$model"
  fi

  local snr_out
  snr_out="$(go run tools/snr_benchmark/main.go -noisy "$NOISY" -clean "$out_wav" 2>&1 | tail -1)"
  echo "  $snr_out"
}

run_condition A passthrough
run_condition B passthrough
run_condition C rnnoise

echo "[latency] L0 passthrough"
go run tools/latency_harness/main.go -frames 100000 -backend passthrough \
  -json "$OUT/latency_l0.json" || echo "  WARN: latency harness failed"

echo "[latency] L1 rnnoise (informational)"
go run tools/latency_harness/main.go -frames 10000 -backend rnnoise \
  -json "$OUT/latency_l1.json" || true

if command -v python3 >/dev/null && [[ -f "$LAB/eval/wer_eval.py" ]]; then
  echo "[wer] golden phrases"
  python3 "$LAB/eval/wer_eval.py" --results-dir "$OUT" || true
fi

cat > "$OUT/metrics.json" <<EOF
{
  "timestamp": "${STAMP}",
  "conditions": ["A_noisy_bypass", "B_passthrough", "C_rnnoise"],
  "artifacts": {
    "latency_l0": "latency_l0.json",
    "latency_l1": "latency_l1.json"
  },
  "notes": "Fill WER from wer_eval.py; see REPORT_TEMPLATE.md"
}
EOF

echo "=== Done: $OUT ==="
