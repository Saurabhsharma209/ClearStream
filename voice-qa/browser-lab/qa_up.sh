#!/usr/bin/env bash
# Start full Voice AI Lab stack for QA (runs against CLEARSTREAM_ROOT Go module).
set -euo pipefail
LAB="$(cd "$(dirname "$0")" && pwd)"
VOICE_QA_ROOT="$(cd "$LAB/.." && pwd)"
SETUP_DIR="$VOICE_QA_ROOT/setup"
if [[ -f "$SETUP_DIR/env.local" ]]; then
  # shellcheck source=/dev/null
  source "$SETUP_DIR/env.local"
fi
ROOT="${CLEARSTREAM_ROOT:-$(cd "$LAB/../../ClearStream-1" 2>/dev/null && pwd)}"
if [[ ! -f "$ROOT/go.mod" ]]; then
  echo "Set CLEARSTREAM_ROOT to your ClearStream clone (contains go.mod)."
  exit 1
fi

MODEL="${1:-passthrough}"
if [[ "${1:-}" == "stop" ]]; then
  MODEL="stop"
fi
OLLAMA_MODEL="${OLLAMA_MODEL:-qwen2.5-coder}"
BRIDGE_PORT="${BRIDGE_PORT:-8081}"
ORCH_PORT="${ORCH_PORT:-8090}"
UI_PORT="${UI_PORT:-8765}"
PCAP_DIR="${PCAP_DIR:-$VOICE_QA_ROOT/pcap-captures}"
PCAP_ANALYZE="${PCAP_ANALYZE:-1}"

export PATH="$(brew --prefix go 2>/dev/null)/bin:$PATH"
mkdir -p "$PCAP_DIR"

mkdir -p "$LAB/.run"
BRIDGE_PID="$LAB/.run/bridge.pid"
ORCH_PID="$LAB/.run/orchestrator.pid"
UI_PID="$LAB/.run/ui.pid"

stop_if_running() {
  local f="$1"
  if [[ -f "$f" ]]; then
    local pid
    pid=$(cat "$f")
    if kill -0 "$pid" 2>/dev/null; then
      echo "Stopping PID $pid ($f)"
      kill "$pid" 2>/dev/null || true
      sleep 0.5
    fi
    rm -f "$f"
  fi
}

if [[ "$MODEL" == "stop" ]]; then
  stop_if_running "$UI_PID"
  stop_if_running "$ORCH_PID"
  stop_if_running "$BRIDGE_PID"
  echo "Voice AI Lab stopped."
  exit 0
fi

cd "$ROOT"

# Bridge (ClearStream library — examples/bridge)
if curl -sf "http://localhost:${BRIDGE_PORT}/health" >/dev/null 2>&1; then
  echo "[bridge] already running on :${BRIDGE_PORT}"
else
  stop_if_running "$BRIDGE_PID"
  echo "[bridge] starting on :${BRIDGE_PORT} model=$MODEL pcap=$PCAP_DIR (clearstream=$ROOT)"
  PCAP_ARGS=(--pcap "$PCAP_DIR")
  [[ "$PCAP_ANALYZE" == "1" ]] && PCAP_ARGS+=(--pcap-analyze)
  go run examples/bridge/main.go --http ":${BRIDGE_PORT}" --model "$MODEL" "${PCAP_ARGS[@]}" \
    >"$LAB/.run/bridge.log" 2>&1 &
  echo $! >"$BRIDGE_PID"
  for i in $(seq 1 20); do
    curl -sf "http://localhost:${BRIDGE_PORT}/health" >/dev/null && break
    sleep 0.25
  done
fi

ORCH_DIR="$LAB/orchestrator"
if [[ ! -x "$ORCH_DIR/.venv/bin/python" ]]; then
  echo "[orchestrator] creating venv + installing deps (first run may take ~1 min)"
  python3 -m venv "$ORCH_DIR/.venv"
  "$ORCH_DIR/.venv/bin/pip" install -q -U pip
  "$ORCH_DIR/.venv/bin/pip" install -q -r "$ORCH_DIR/requirements.txt"
fi

"$ORCH_DIR/.venv/bin/pip" install -q certifi 2>/dev/null || true
export SSL_CERT_FILE="$("$ORCH_DIR/.venv/bin/python" -c "import certifi; print(certifi.where())" 2>/dev/null || echo "")"
export REQUESTS_CA_BUNDLE="${SSL_CERT_FILE:-}"
export HF_HUB_DISABLE_TELEMETRY=1

WHISPER_DIR="${WHISPER_MODEL_PATH:-$HOME/.cache/clearstream-lab/faster-whisper-tiny}"
if [[ ! -f "$WHISPER_DIR/model.bin" ]]; then
  echo "[orchestrator] downloading Whisper tiny..."
  bash "$ORCH_DIR/download_whisper.sh" "$WHISPER_DIR"
fi
export WHISPER_MODEL_PATH="$WHISPER_DIR"

stop_if_running "$ORCH_PID"
echo "[orchestrator] starting on :${ORCH_PORT} ollama=$OLLAMA_MODEL"
"$ORCH_DIR/.venv/bin/python" "$ORCH_DIR/orchestrator.py" \
  --host localhost \
  --port "$ORCH_PORT" \
  --bridge "ws://localhost:${BRIDGE_PORT}/stream" \
  --condition B \
  --ollama-model "$OLLAMA_MODEL" \
  --whisper-path "$WHISPER_MODEL_PATH" \
  >"$LAB/.run/orchestrator.log" 2>&1 &
echo $! >"$ORCH_PID"
sleep 1

stop_if_running "$UI_PID"
echo "[ui] serving on http://localhost:${UI_PORT}"
cd "$LAB/ui"
python3 -m http.server "$UI_PORT" >"$LAB/.run/ui.log" 2>&1 &
echo $! >"$UI_PID"

echo ""
echo "=== Voice AI Lab QA stack ready ==="
echo "  CLEARSTREAM_ROOT=$ROOT"
echo "  Noisy user:   http://localhost:${UI_PORT}/noisy_user.html"
echo "  Voice AI:     http://localhost:${UI_PORT}/voice_ai.html"
echo "  PCAP dir:     $PCAP_DIR  (make -C \"$ROOT\" pcap-analyze PCAP_DIR=$PCAP_DIR)"
echo "  Stop: bash $LAB/qa_up.sh stop"
