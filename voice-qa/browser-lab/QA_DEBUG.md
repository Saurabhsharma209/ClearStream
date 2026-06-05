# Voice AI Lab — QA guide & debugging

## What this setup is

This is **not** full WebRTC (no video call UI). It is a **local pipeline test bench**:

```
Browser mic + noise
    → orchestrator :8090/ingest  (Python)
    → ClearStream bridge :8081/stream  (Go, passthrough or rnnoise)
    → clean PCM back to orchestrator
    → Whisper STT → Ollama LLM → events to voice_ai.html
```

| Component | Port | Role |
|-----------|------|------|
| **UI server** | 8765 | Serves HTML only |
| **Orchestrator** | 8090 | Routes audio, runs STT/LLM |
| **ClearStream bridge** | 8081 | Noise suppression (L0 passthrough in QA) |

## How to know it is working

### Layer 1 — ClearStream only (no Whisper needed)

```bash
curl -s http://localhost:8081/health
# {"model":"passthrough","status":"ok",...}
```

Send test PCM:

```bash
cd examples/voice_ai_lab/orchestrator
.venv/bin/python -c "
import asyncio, websockets, struct, math
async def t():
    async with websockets.connect('ws://localhost:8090/ingest') as ws:
        for n in range(300):
            s=[int(8000*math.sin(2*math.pi*440*(n*160+i)/16000)) for i in range(160)]
            await ws.send(struct.pack('<'+'h'*160,*s))
        await asyncio.sleep(1)
asyncio.run(t())
"
tail -5 ../.run/orchestrator.log
# expect: ingest done frames=300
```

If you see `frames=300`, **ClearStream + orchestrator audio path works**.

### Layer 2 — STT + LLM

After **~3 seconds** of real speech, orchestrator log should show:

```
running STT on 3.0s of audio...
STT: 'hello world' (120ms)
LLM: '...' (800ms)
```

Voice AI tab shows JSON events when connected to `ws://localhost:8090/events`.

## Logs (always check these first)

```bash
tail -f examples/voice_ai_lab/.run/orchestrator.log   # STT/LLM/errors
tail -f examples/voice_ai_lab/.run/bridge.log         # bridge (if started via qa_up)
tail -f examples/voice_ai_lab/.run/ui.log             # HTTP 200 for HTML
```

## Known failure from your machine

**Whisper model download failed with SSL:**

```
CERTIFICATE_VERIFY_FAILED ... huggingface_hub
```

Fix (then restart):

```bash
make qa-lab-down
make qa-lab-up
# qa_up.sh now sets SSL_CERT_FILE via certifi
```

Or manually:

```bash
cd examples/voice_ai_lab/orchestrator
.venv/bin/pip install certifi
export SSL_CERT_FILE=$(.venv/bin/python -c "import certifi; print(certifi.where())")
.venv/bin/python orchestrator.py --bridge ws://localhost:8081/stream
```

## QA checklist

1. [ ] `curl localhost:8081/health` → ok
2. [ ] Open noisy_user → Connect → **Start mic** (must click both)
3. [ ] Frame count increases on noisy_user page
4. [ ] `orchestrator.log` shows `first audio frame received`
5. [ ] After 3s speech, STT line appears OR `STT empty` status on voice_ai tab
6. [ ] voice_ai tab connected → sees `hello` then `stt` / `llm` JSON

## Audio-only QA (skip broken STT)

```bash
python orchestrator.py --skip-stt --skip-llm --bridge ws://localhost:8081/stream
```

Use this to validate ClearStream latency/path only.
