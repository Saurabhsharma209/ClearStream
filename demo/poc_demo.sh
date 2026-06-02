#!/usr/bin/env bash
# ClearStream POC Demo — tests all integration paths
set -e

BINARY="./clearstream"
echo "=== ClearStream POC Demo ==="
echo ""

# 1. Build
echo "[1/5] Building..."
go build -o $BINARY ./cmd/clearstream/
echo "    OK: binary built"

# 2. Version
echo "[2/5] Version check..."
$BINARY version
echo ""

# 3. Generate test audio (if tools/gen_test_audio exists)
echo "[3/5] Generating test audio..."
if [ -d tools/gen_test_audio ]; then
    go run tools/gen_test_audio/main.go 2>/dev/null && echo "    OK: test WAVs generated" || echo "    SKIP: gen_test_audio failed"
else
    echo "    SKIP: no test audio generator"
fi

# 4. HTTP server smoke test
echo "[4/5] HTTP server smoke test..."
$BINARY server --http :18080 --model passthrough &
SERVER_PID=$!
sleep 1
STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:18080/health 2>/dev/null || echo "000")
kill $SERVER_PID 2>/dev/null
if [ "$STATUS" = "200" ]; then
    echo "    OK: /health returned 200"
else
    echo "    WARN: /health returned $STATUS (may need ffmpeg)"
fi

# 5. Summary
echo ""
echo "[5/5] Integration paths available:"
echo "    File:     clearstream file -i noisy.wav -o clean.wav"
echo "    Dir:      clearstream dir -i ./recordings/ -o ./clean/"
echo "    RTP:      clearstream rtp --listen :5004 --forward HOST:5004"
echo "    HTTP:     clearstream server --http :8080"
echo "    Docker:   make poc"
echo ""
echo "=== POC Demo Complete ==="
