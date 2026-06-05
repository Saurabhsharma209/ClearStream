#!/usr/bin/env python3
"""
Voice AI Lab orchestrator.

Flow: browser (noisy PCM) -> /ingest -> ClearStream bridge -> clean PCM -> STT -> Ollama -> TTS -> /events

Usage:
  python orchestrator.py --bridge ws://localhost:8081/stream
  # open ui/noisy_user.html and ui/voice_ai.html
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import os
import ssl
import time
from collections import deque
from typing import Set

# macOS Homebrew Python often lacks system CA bundle for HuggingFace downloads.
try:
    import certifi

    os.environ.setdefault("SSL_CERT_FILE", certifi.where())
    os.environ.setdefault("REQUESTS_CA_BUNDLE", certifi.where())
    ssl._create_default_https_context = lambda: ssl.create_default_context(cafile=certifi.where())
except ImportError:
    pass

import numpy as np

try:
    import websockets
except ImportError:
    raise SystemExit("pip install -r requirements.txt")

log = logging.getLogger("voice_ai_lab")

SAMPLE_RATE = 16000
FRAME_BYTES = 320  # 160 samples int16 LE


class ClearStreamClient:
    """Forwards PCM to ClearStream WS bridge; returns enhanced PCM."""

    def __init__(self, bridge_url: str, model_hint: str = ""):
        self.bridge_url = bridge_url
        self.model_hint = model_hint
        self._ws = None
        self._model = "unknown"

    async def connect(self):
        self._ws = await websockets.connect(self.bridge_url, max_size=2**20)
        meta = await self._ws.recv()
        if isinstance(meta, str):
            try:
                j = json.loads(meta)
                self._model = j.get("model", self._model)
                log.info("bridge meta: %s", j)
            except json.JSONDecodeError:
                pass

    async def enhance(self, pcm: bytes) -> bytes:
        if not self._ws:
            await self.connect()
        t0 = time.perf_counter()
        await self._ws.send(pcm)
        out = await self._ws.recv()
        if isinstance(out, str):
            raise RuntimeError(f"unexpected text from bridge: {out[:200]}")
        elapsed_ms = (time.perf_counter() - t0) * 1000
        return out, elapsed_ms

    async def close(self):
        if self._ws:
            await self._ws.close()
            self._ws = None

    @property
    def model(self) -> str:
        return self._model


class STTEngine:
    def __init__(self, model_size: str = "tiny", device: str = "cpu", model_path: str = ""):
        self._model = None
        self._size = model_size if not model_path else model_path
        self._device = device
        self._model_path = model_path

    def _ensure(self):
        if self._model is None:
            log.info("loading Whisper model %r (first run downloads from HuggingFace)...", self._size)
            from faster_whisper import WhisperModel

            self._model = WhisperModel(self._size, device=self._device, compute_type="int8")
            log.info("Whisper model ready")

    def preload(self):
        self._ensure()

    def transcribe_pcm(self, pcm: bytes) -> tuple[str, float]:
        self._ensure()
        samples = np.frombuffer(pcm, dtype=np.int16).astype(np.float32) / 32768.0
        if len(samples) < SAMPLE_RATE // 2:
            return "", 0.0
        t0 = time.perf_counter()
        segments, _ = self._model.transcribe(
            samples,
            language="en",
            vad_filter=True,
            beam_size=1,
        )
        text = " ".join(s.text.strip() for s in segments).strip()
        return text, (time.perf_counter() - t0) * 1000


class OllamaClient:
    def __init__(self, base: str = "http://localhost:11434", model: str = "llama3.2"):
        self.base = base.rstrip("/")
        self.model = model

    def chat(self, user_text: str) -> tuple[str, float]:
        import requests

        t0 = time.perf_counter()
        payload = {
            "model": self.model,
            "messages": [
                {
                    "role": "system",
                    "content": "You are a concise voice assistant. Reply in one short sentence.",
                },
                {"role": "user", "content": user_text},
            ],
            "stream": False,
        }
        try:
            r = requests.post(f"{self.base}/api/chat", json=payload, timeout=60)
            r.raise_for_status()
            reply = r.json().get("message", {}).get("content", "")
        except Exception as e:
            reply = f"[ollama unavailable: {e}]"
        return reply.strip(), (time.perf_counter() - t0) * 1000


async def tts_edge(text: str) -> bytes:
    """Return 16 kHz mono PCM via edge-tts (optional; pip install edge-tts)."""
    try:
        import edge_tts
        import tempfile
        import subprocess
        import os

        voice = "en-US-AriaNeural"
        with tempfile.NamedTemporaryFile(suffix=".mp3", delete=False) as f:
            mp3_path = f.name
        comm = edge_tts.Communicate(text, voice)
        await comm.save(mp3_path)
        wav_path = mp3_path + ".wav"
        subprocess.run(
            ["ffmpeg", "-y", "-i", mp3_path, "-ar", str(SAMPLE_RATE), "-ac", "1", "-f", "wav", wav_path],
            check=True,
            capture_output=True,
        )
        with open(wav_path, "rb") as wf:
            data = wf.read()
        os.unlink(mp3_path)
        os.unlink(wav_path)
        # strip 44-byte WAV header
        if len(data) > 44:
            return data[44:]
        return b""
    except Exception as e:
        log.warning("TTS skipped: %s", e)
        return b""


class LabOrchestrator:
    def __init__(self, args):
        self.args = args
        self.bridge_url = args.bridge
        self.stt = STTEngine(args.whisper_model, model_path=args.whisper_path) if not args.skip_stt else None
        self.llm = OllamaClient(args.ollama, args.ollama_model) if not args.skip_llm else None
        self.events: Set[websockets.WebSocketServerProtocol] = set()
        self._utterance_frames = int(args.utterance_sec * SAMPLE_RATE / 160)

    async def broadcast(self, msg: dict | bytes):
        dead = []
        for ws in self.events:
            try:
                if isinstance(msg, dict):
                    await ws.send(json.dumps(msg))
                else:
                    await ws.send(msg)
            except Exception:
                dead.append(ws)
        for ws in dead:
            self.events.discard(ws)

    async def handle_ingest(self, ws):
        log.info("ingest client connected")
        bridge = None if self.args.bypass_bridge else ClearStreamClient(self.bridge_url, self.args.condition)
        if bridge:
            await bridge.connect()

        frame_latencies = deque(maxlen=5000)
        buf = bytearray()
        frames = 0
        try:
            async for message in ws:
                if isinstance(message, str):
                    continue
                frames += 1
                if frames == 1:
                    log.info("first audio frame received (%d bytes)", len(message))
                    await self.broadcast({"type": "status", "msg": "receiving audio"})
                elif frames % 100 == 0:
                    log.info("ingest frames=%d bridge_p50=%.3fms", frames, sorted(frame_latencies)[len(frame_latencies) // 2] if frame_latencies else 0)

                if self.args.bypass_bridge:
                    clean = message
                    lat_ms = 0.0
                else:
                    clean, lat_ms = await bridge.enhance(message)
                frame_latencies.append(lat_ms)

                if not self.args.skip_stt:
                    buf.extend(clean)
                    if len(buf) >= self._utterance_frames * FRAME_BYTES:
                        pcm = bytes(buf[: self._utterance_frames * FRAME_BYTES])
                        buf.clear()
                        log.info("running STT on %.1fs of audio...", self.args.utterance_sec)
                        try:
                            text, stt_ms = await asyncio.to_thread(self.stt.transcribe_pcm, pcm)
                        except Exception as e:
                            log.exception("STT failed")
                            await self.broadcast({"type": "error", "stage": "stt", "msg": str(e)})
                            continue
                        if not text:
                            log.info("STT returned empty (silence or unrecognised)")
                            await self.broadcast({"type": "status", "msg": "STT empty — speak louder or longer"})
                            continue
                        log.info("STT: %r (%.0fms)", text, stt_ms)
                        await self.broadcast(
                            {
                                "type": "stt",
                                "text": text,
                                "stt_ms": round(stt_ms, 2),
                                "bridge_ms_p50": round(
                                    sorted(frame_latencies)[len(frame_latencies) // 2], 3
                                )
                                if frame_latencies
                                else 0,
                                "model": bridge.model if bridge else "bypass",
                                "condition": self.args.condition,
                            }
                        )
                        if self.llm and not self.args.skip_llm:
                            reply, llm_ms = await asyncio.to_thread(self.llm.chat, text)
                            log.info("LLM: %r (%.0fms)", reply[:80], llm_ms)
                            await self.broadcast(
                                {
                                    "type": "llm",
                                    "reply": reply,
                                    "llm_ms": round(llm_ms, 2),
                                }
                            )
                            if self.args.tts:
                                pcm_out = await tts_edge(reply)
                                if pcm_out:
                                    await self.broadcast(pcm_out)
        except websockets.ConnectionClosed:
            pass
        except Exception as e:
            log.exception("ingest handler error")
            await self.broadcast({"type": "error", "stage": "ingest", "msg": str(e)})
        finally:
            if bridge:
                await bridge.close()
            log.info("ingest done frames=%d", frames)

    async def handle_events(self, ws):
        self.events.add(ws)
        await ws.send(
            json.dumps(
                {
                    "type": "hello",
                    "bridge": self.args.bridge,
                    "condition": self.args.condition,
                }
            )
        )
        try:
            async for _ in ws:
                pass
        finally:
            self.events.discard(ws)

    async def run(self):
        if self.stt and not self.args.skip_stt:
            log.info("preloading Whisper (may download model on first run)...")
            try:
                await asyncio.to_thread(self.stt.preload)
                await self.broadcast({"type": "status", "msg": "Whisper STT ready"})
            except Exception as e:
                log.exception("Whisper preload failed")
                await self.broadcast({"type": "error", "stage": "stt_init", "msg": str(e)})

        async def router(ws):
            path = ws.request.path if hasattr(ws, "request") else getattr(ws, "path", "/")
            if path in ("/ingest", "/ingest/"):
                await self.handle_ingest(ws)
            elif path in ("/events", "/events/"):
                await self.handle_events(ws)
            else:
                await ws.close(1008, "unknown path")

        async with websockets.serve(router, self.args.host, self.args.port, max_size=2**20):
            log.info(
                "orchestrator ws://%s:%d ingest=/ingest events=/events condition=%s",
                self.args.host,
                self.args.port,
                self.args.condition,
            )
            await asyncio.Future()


def main():
    p = argparse.ArgumentParser(description="ClearStream Voice AI Lab orchestrator")
    p.add_argument("--host", default="localhost")
    p.add_argument("--port", type=int, default=8090)
    p.add_argument("--bridge", default="ws://localhost:8081/stream")
    p.add_argument(
        "--condition",
        default="B",
        choices=["A", "B", "C"],
        help="A=bypass bridge, B=passthrough bridge, C=rnnoise bridge (set bridge --model)",
    )
    p.add_argument("--bypass-bridge", action="store_true", help="Force condition A")
    p.add_argument("--utterance-sec", type=float, default=3.0, help="PCM buffer before STT")
    p.add_argument("--whisper-model", default="tiny")
    p.add_argument("--whisper-path", default=os.environ.get("WHISPER_MODEL_PATH", ""), help="Local faster-whisper model directory")
    p.add_argument("--ollama", default="http://localhost:11434")
    p.add_argument("--ollama-model", default="llama3.2")
    p.add_argument("--skip-stt", action="store_true")
    p.add_argument("--skip-llm", action="store_true")
    p.add_argument("--tts", action="store_true", help="Enable edge-tts playback")
    args = p.parse_args()

    if args.condition == "A":
        args.bypass_bridge = True

    logging.basicConfig(level=logging.INFO)
    lab = LabOrchestrator(args)
    asyncio.run(lab.run())


if __name__ == "__main__":
    main()
