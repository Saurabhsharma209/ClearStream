#!/usr/bin/env bash
# Download faster-whisper-tiny via curl (works when Python SSL is broken on macOS).
set -euo pipefail
MODEL_DIR="${1:-$HOME/.cache/clearstream-lab/faster-whisper-tiny}"
BASE="https://huggingface.co/Systran/faster-whisper-tiny/resolve/main"
mkdir -p "$MODEL_DIR"

need() {
  local f="$1"
  [[ -f "$MODEL_DIR/$f" ]] && [[ -s "$MODEL_DIR/$f" ]]
}

download() {
  local f="$1"
  if need "$f"; then
    echo "  ok $f"
    return
  fi
  echo "  get $f"
  curl -fsSL "$BASE/$f" -o "$MODEL_DIR/$f"
}

echo "Whisper model dir: $MODEL_DIR"
download config.json
download model.bin
download tokenizer.json
download vocabulary.txt
echo "done"
echo "$MODEL_DIR"
