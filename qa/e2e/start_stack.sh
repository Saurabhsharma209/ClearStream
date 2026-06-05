#!/usr/bin/env bash
# start_stack.sh — Launch the full ClearStream E2E QA stack.
#
# CS-010 fix: dp-endpoint was being resolved on every incoming call, exhausting
# the 1 000 req/min tenant rate limit during batch runs.  This script pre-resolves
# the WSS URL once at startup and exports VOICEBOT_DATA_PIPE_WSS so the bridge
# and orchestrator skip the per-call HTTP resolver entirely.
#
# Usage:
#   E2E_MODEL=rnnoise bash qa/e2e/start_stack.sh
#   bash qa/e2e/start_stack.sh stop
#
# Key environment variables:
#   DP_ENDPOINT              HTTP resolver URL (required unless VOICEBOT_DATA_PIPE_WSS is set)
#   VOICEBOT_DATA_PIPE_WSS   Pre-resolved WSS URL — set this to skip HTTP entirely
#   VOICEBOT_API_KEY         Basic-auth username for dp-endpoint (CS-005)
#   VOICEBOT_API_TOKEN       Basic-auth password for dp-endpoint (CS-005)
#   E2E_MODEL                NR backend: passthrough | rnnoise | deepfilter (default: passthrough)
#   E2E_BRIDGE_PORT          Bridge HTTP port (default: 8081)
#   E2E_ORCH_PORT            Orchestrator port (default: 8090)
#   E2E_DP_CACHE_TTL_SEC     How long to cache the resolved WSS URL (default: 300)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Load local env overrides (not committed — contains credentials).
LOCAL_ENV="$SCRIPT_DIR/voicebots.local.env"
if [[ -f "$LOCAL_ENV" ]]; then
  # shellcheck source=/dev/null
  source "$LOCAL_ENV"
fi

MODEL="${E2E_MODEL:-passthrough}"
BRIDGE_PORT="${E2E_BRIDGE_PORT:-8081}"
ORCH_PORT="${E2E_ORCH_PORT:-8090}"
RUN_DIR="$SCRIPT_DIR/.run"
mkdir -p "$RUN_DIR"

BRIDGE_PID="$RUN_DIR/bridge.pid"
ORCH_PID="$RUN_DIR/orchestrator.pid"

stop_pid() {
  local f="$1"
  if [[ -f "$f" ]]; then
    local pid
    pid=$(cat "$f")
    if kill -0 "$pid" 2>/dev/null; then
      echo "[stack] stopping PID $pid ($f)"
      kill "$pid" 2>/dev/null || true
      sleep 0.5
    fi
    rm -f "$f"
  fi
}

if [[ "${1:-}" == "stop" ]]; then
  stop_pid "$ORCH_PID"
  stop_pid "$BRIDGE_PID"
  echo "[stack] stopped"
  exit 0
fi

# ── CS-010: pre-resolve dp-endpoint once at stack start ──────────────────────
# If VOICEBOT_DATA_PIPE_WSS is already set (e.g. in voicebots.local.env), skip
# the HTTP resolve entirely — this is the zero-request path for local dev.
if [[ -z "${VOICEBOT_DATA_PIPE_WSS:-}" ]]; then
  DP_ENDPOINT="${DP_ENDPOINT:-}"
  if [[ -z "$DP_ENDPOINT" ]]; then
    echo "[stack] ERROR: set DP_ENDPOINT or VOICEBOT_DATA_PIPE_WSS before starting"
    exit 1
  fi

  echo "[stack] pre-resolving dp-endpoint: $DP_ENDPOINT"
  RESOLVE_ARGS=(-fsSL "$DP_ENDPOINT")

  # CS-005: attach Basic auth when credentials are present.
  if [[ -n "${VOICEBOT_API_KEY:-}" && -n "${VOICEBOT_API_TOKEN:-}" ]]; then
    RESOLVE_ARGS+=(-u "${VOICEBOT_API_KEY}:${VOICEBOT_API_TOKEN}")
  fi

  RESOLVE_RESP="$(curl "${RESOLVE_ARGS[@]}" --max-time 10 2>&1)" || {
    echo "[stack] ERROR: dp-endpoint resolve failed: $RESOLVE_RESP"
    exit 1
  }

  # Extract the url field from {"url": "wss://..."}.
  VOICEBOT_DATA_PIPE_WSS="$(echo "$RESOLVE_RESP" | python3 -c \
    "import json,sys; print(json.load(sys.stdin)['url'])" 2>/dev/null || echo "")"

  if [[ -z "$VOICEBOT_DATA_PIPE_WSS" ]]; then
    echo "[stack] ERROR: dp-endpoint returned unexpected JSON: $RESOLVE_RESP"
    exit 1
  fi
  echo "[stack] dp-endpoint resolved → $VOICEBOT_DATA_PIPE_WSS"
else
  echo "[stack] using pre-set VOICEBOT_DATA_PIPE_WSS (skipped HTTP resolve)"
fi

export VOICEBOT_DATA_PIPE_WSS

# ── Bridge ────────────────────────────────────────────────────────────────────
if curl -sf "http://localhost:${BRIDGE_PORT}/health" >/dev/null 2>&1; then
  echo "[bridge] already running on :${BRIDGE_PORT}"
else
  stop_pid "$BRIDGE_PID"
  echo "[bridge] starting on :${BRIDGE_PORT} model=$MODEL"
  cd "$REPO_ROOT"
  go run examples/bridge/main.go \
    --http ":${BRIDGE_PORT}" \
    --model "$MODEL" \
    >"$RUN_DIR/bridge.log" 2>&1 &
  echo $! >"$BRIDGE_PID"

  # Wait for health check (up to 5 s).
  for i in $(seq 1 20); do
    curl -sf "http://localhost:${BRIDGE_PORT}/health" >/dev/null 2>&1 && break
    sleep 0.25
  done
  echo "[bridge] ready"
fi

# ── Orchestrator (optional — skip if no venv) ─────────────────────────────────
ORCH_DIR="$SCRIPT_DIR/orchestrator"
if [[ -x "${ORCH_DIR}/.venv/bin/python" ]]; then
  stop_pid "$ORCH_PID"
  echo "[orchestrator] starting on :${ORCH_PORT}"
  "${ORCH_DIR}/.venv/bin/python" "${ORCH_DIR}/orchestrator.py" \
    --host localhost \
    --port "$ORCH_PORT" \
    --bridge "ws://localhost:${BRIDGE_PORT}/stream" \
    >"$RUN_DIR/orchestrator.log" 2>&1 &
  echo $! >"$ORCH_PID"
  sleep 1
  echo "[orchestrator] ready"
else
  echo "[orchestrator] venv not found — skipping (run voice-qa/browser-lab/qa_up.sh for full stack)"
fi

echo ""
echo "=== ClearStream E2E stack ready ==="
echo "  Bridge:  http://localhost:${BRIDGE_PORT}/health"
echo "  WSS URL: $VOICEBOT_DATA_PIPE_WSS"
echo "  Logs:    $RUN_DIR/"
echo "  Stop:    bash $0 stop"
