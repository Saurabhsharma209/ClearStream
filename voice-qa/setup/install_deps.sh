#!/usr/bin/env bash
# One-time local deps: SIPp, Redis (brew or Docker), Asterisk 18 (Docker), env file.
set -euo pipefail

SETUP_DIR="$(cd "$(dirname "$0")" && pwd)"
VOICE_QA_ROOT="$(cd "$SETUP_DIR/.." && pwd)"
export VOICE_QA_ROOT

echo "=== Voice QA: install dependencies ==="

if ! command -v brew >/dev/null; then
  echo "Homebrew required. Install from https://brew.sh"
  exit 1
fi

# SIPp (SIP load / bidirectional test tool — "Sippy" in docs)
if command -v sipp >/dev/null; then
  echo "[ok] SIPp: $(sipp -v 2>&1 | head -1)"
else
  echo "[install] SIPp via Homebrew (may take several minutes if openssl builds)..."
  brew install sipp
fi

# Docker for Asterisk 18 + Redis (no native Asterisk 18 formula on macOS Homebrew)
if ! command -v docker >/dev/null; then
  echo "[warn] Docker not found. Install Docker Desktop for Asterisk 18 + Redis containers."
else
  echo "[ok] Docker: $(docker --version)"
fi

# Optional: Redis CLI via brew (containers still used for Ingestix stack)
if ! command -v redis-cli >/dev/null; then
  echo "[install] redis (cli only, optional)..."
  brew install redis || true
fi

# env.local
if [[ ! -f "$SETUP_DIR/env.local" ]]; then
  cp "$SETUP_DIR/env.example" "$SETUP_DIR/env.local"
  echo "[created] $SETUP_DIR/env.local"
else
  echo "[ok] env.local exists"
fi

echo ""
echo "Next:"
echo "  source $SETUP_DIR/env.local"
echo "  bash $SETUP_DIR/start_ingestix_deps.sh   # Redis + Asterisk 18 containers"
echo "  bash $VOICE_QA_ROOT/browser-lab/qa_up.sh # browser QA lab"
