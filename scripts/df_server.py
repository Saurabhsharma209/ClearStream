#!/usr/bin/env python3
"""
df_server.py — DeepFilterNet HTTP inference server for ClearStream.

Accepts raw 16kHz int16 PCM, returns enhanced 16kHz int16 PCM.
Called by pkg/model/deepfilter_server.go in ClearStream.

Usage:
  python3 scripts/df_server.py                  # default: port 7878
  python3 scripts/df_server.py --port 7878
  python3 scripts/df_server.py --model /path/to/checkpoint  # fine-tuned model

Protocol:
  POST /enhance
    Content-Type: application/octet-stream
    Body: raw int16 LE PCM at 16kHz (any length)
    Response 200: raw int16 LE PCM at 16kHz (same length as input)

  GET /health
    Response 200: {"status":"ok","model":"DeepFilterNet3","sr":16000,"latency_ms":N}

  POST /shutdown  (optional, for graceful stop from Go)

Copy to: ~/ClearStream/scripts/df_server.py
"""

import argparse
import json
import logging
import os
import struct
import sys
import time
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import numpy as np
import torch
import torchaudio

# Suppress noisy deepfilternet logging
logging.getLogger("df").setLevel(logging.WARNING)
os.environ["DF_LOGGING_LEVEL"] = "WARNING"

# ── Model loading ─────────────────────────────────────────────────────────────

MODEL      = None
DF_STATE   = None
MODEL_SR   = None
MODEL_LOCK = threading.Lock()

def load_model(checkpoint_path: str = None):
    """Load DeepFilterNet. Uses pretrained weights if no checkpoint given."""
    global MODEL, DF_STATE, MODEL_SR

    from df.enhance import init_df
    print("Loading DeepFilterNet...", flush=True)
    t0 = time.time()

    if checkpoint_path:
        model, df_state, _ = init_df()
        ckpt = torch.load(checkpoint_path, map_location="cpu")
        state = ckpt.get("model", ckpt)
        model.load_state_dict(state, strict=False)
        print(f"  Loaded fine-tuned checkpoint: {checkpoint_path}")
    else:
        model, df_state, _ = init_df()
        print("  Using pretrained DeepFilterNet3 weights")

    MODEL    = model.eval()
    DF_STATE = df_state
    MODEL_SR = df_state.sr()

    # Warmup (first inference is slow due to JIT)
    dummy = torch.zeros(1, MODEL_SR)
    from df.enhance import enhance
    _ = enhance(MODEL, DF_STATE, dummy)

    ms = (time.time() - t0) * 1000
    print(f"  Model ready in {ms:.0f}ms | Device: CPU | SR: {MODEL_SR}Hz", flush=True)


# ── Inference ─────────────────────────────────────────────────────────────────

INPUT_SR = 16000  # ClearStream native rate

def enhance_pcm(pcm_bytes: bytes) -> bytes:
    """
    Input:  raw int16 LE PCM at 16kHz
    Output: raw int16 LE PCM at 16kHz (noise suppressed)
    """
    from df.enhance import enhance

    # Decode int16 LE → float32 [-1, 1]
    n_samples = len(pcm_bytes) // 2
    samples = np.frombuffer(pcm_bytes, dtype="<i2").astype(np.float32) / 32768.0
    audio = torch.from_numpy(samples).unsqueeze(0)  # [1, N]

    # Resample 16kHz → 48kHz (model native)
    if INPUT_SR != MODEL_SR:
        audio = torchaudio.functional.resample(audio, INPUT_SR, MODEL_SR)

    # Run enhancement
    with MODEL_LOCK:
        with torch.no_grad():
            enhanced = enhance(MODEL, DF_STATE, audio)  # [1, N]

    # Resample 48kHz → 16kHz
    if INPUT_SR != MODEL_SR:
        enhanced = torchaudio.functional.resample(enhanced, MODEL_SR, INPUT_SR)

    # Clip + encode back to int16 LE
    enhanced = enhanced.squeeze(0).clamp(-1.0, 1.0)
    out_int16 = (enhanced.numpy() * 32767).astype("<i2")
    return out_int16.tobytes()


# ── HTTP handler ──────────────────────────────────────────────────────────────

LATENCY_MS = 0.0  # rolling average

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass  # suppress per-request logs (Go side logs anyway)

    def do_GET(self):
        if self.path == "/health":
            body = json.dumps({
                "status":     "ok",
                "model":      "DeepFilterNet3",
                "sr":         INPUT_SR,
                "latency_ms": round(LATENCY_MS, 1),
            }).encode()
            self._respond(200, "application/json", body)
        else:
            self._respond(404, "text/plain", b"not found")

    def do_POST(self):
        if self.path == "/enhance":
            length = int(self.headers.get("Content-Length", 0))
            body   = self.rfile.read(length)
            if not body:
                self._respond(400, "text/plain", b"empty body")
                return
            try:
                t0  = time.time()
                out = enhance_pcm(body)
                ms  = (time.time() - t0) * 1000

                # Update rolling latency average
                global LATENCY_MS
                LATENCY_MS = 0.9 * LATENCY_MS + 0.1 * ms

                self._respond(200, "application/octet-stream", out)
            except Exception as e:
                self._respond(500, "text/plain", str(e).encode())

        elif self.path == "/shutdown":
            self._respond(200, "text/plain", b"shutting down")
            threading.Thread(target=self.server.shutdown, daemon=True).start()

        else:
            self._respond(404, "text/plain", b"not found")

    def _respond(self, code: int, ct: str, body: bytes):
        self.send_response(code)
        self.send_header("Content-Type", ct)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


# ── Entry point ───────────────────────────────────────────────────────────────

def main():
    ap = argparse.ArgumentParser(description="DeepFilterNet inference server for ClearStream")
    ap.add_argument("--port",  type=int, default=7878, help="HTTP port (default 7878)")
    ap.add_argument("--host",  default="127.0.0.1",   help="Bind address (default 127.0.0.1)")
    ap.add_argument("--model", default=None,
                    help="Path to fine-tuned .pt checkpoint (omit for pretrained weights)")
    args = ap.parse_args()

    load_model(args.model)

    server = HTTPServer((args.host, args.port), Handler)
    print(f"DeepFilterNet server listening on {args.host}:{args.port}", flush=True)
    print(f"  POST http://{args.host}:{args.port}/enhance  (raw int16 PCM in, enhanced out)", flush=True)
    print(f"  GET  http://{args.host}:{args.port}/health", flush=True)

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down.", flush=True)


if __name__ == "__main__":
    main()
