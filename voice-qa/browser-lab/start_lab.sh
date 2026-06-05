#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

MODEL="${1:-passthrough}"

echo "Starting Voice AI Lab bridge (model=$MODEL) on :8081..."
go run examples/voice_ai_lab/bridge/main.go --http :8081 --model "$MODEL" &
BRIDGE_PID=$!
sleep 1

echo ""
echo "Bridge PID=$BRIDGE_PID"
echo "Next terminal:"
echo "  cd examples/voice_ai_lab/orchestrator && pip install -r requirements.txt"
echo "  python orchestrator.py --bridge ws://localhost:8081/stream --condition B"
echo ""
echo "Open in browser:"
echo "  examples/voice_ai_lab/ui/noisy_user.html"
echo "  examples/voice_ai_lab/ui/voice_ai.html"
echo ""
echo "Stop bridge: kill $BRIDGE_PID"

wait $BRIDGE_PID
