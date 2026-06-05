#!/usr/bin/env bash
set -euo pipefail
SETUP_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
[[ -f "$SETUP_DIR/env.local" ]] && source "$SETUP_DIR/env.local"

if ! command -v docker >/dev/null; then
  echo "Docker required for Asterisk 18 + Redis on macOS."
  exit 1
fi

cd "$SETUP_DIR"
docker compose up -d
echo ""
echo "Redis:    ${REDIS_URL:-redis://127.0.0.1:6379}"
echo "Asterisk: SIP :${ASTERISK_SIP_PORT:-5060}/udp  AMI :${ASTERISK_AMI_PORT:-5038}"
echo "Stop:     docker compose -f $SETUP_DIR/docker-compose.yml down"
