# Voice QA workspace

Local QA and telephony setup for **ClearStream** + **Ingestream (Ingestix)**. This folder is **not** part of the ClearStream git repo.

## Prerequisites

Run once:

```bash
bash setup/install_deps.sh
source setup/env.local   # created from env.example on first install
```

| Dependency | Role |
|------------|------|
| **Asterisk 18** | Media source; AudioSocket + AMI for Ingestix |
| **Redis 6+** | Ingestix ↔ Streamix streams |
| **SIPp** | SIP/RTP load and noisy bidirectional call simulation |
| **Go, Python, Ollama** | Browser lab (see `browser-lab/README.md`) |

## Subfolders

| Path | Contents |
|------|----------|
| `browser-lab/` | Noisy-user Web UI, orchestrator, WER eval, `qa_up.sh` |
| `asterisk/` | Docker Compose for Asterisk 18 + sample config |
| `sipp/` | XML scenarios and helper scripts |
| `integration/` | ClearStream + Ingestream integration (edit here) |
| `setup/` | `install_deps.sh`, `env.example`, Redis/Asterisk helpers |

## Start browser lab

```bash
source setup/env.local
bash browser-lab/qa_up.sh
# Open http://localhost:8765/noisy_user.html and voice_ai.html
```

## Start Ingestix stack

```bash
bash setup/start_ingestix_deps.sh   # Redis + Asterisk 18 containers
cd "$INGESTREAM_ROOT" && make build && cd target && ./ingestix
```

See [ingestream README](../ingestream/README.md) for upstream build details.
