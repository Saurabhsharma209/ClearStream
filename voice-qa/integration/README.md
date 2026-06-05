# ClearStream ↔ Ingestream integration

**All integration changes go here** (not in the ClearStream library repo unless you are adding a reusable API).

## Targets

| System | Repo | Role |
|--------|------|------|
| ClearStream | `CLEARSTREAM_ROOT` | Noise suppression on RTP or WebSocket PCM |
| Ingestix | `INGESTREAM_ROOT` | Asterisk AMI + AudioSocket → Redis streams |

## Suggested flow (local)

1. `bash ../setup/start_ingestix_deps.sh` — Redis + Asterisk 18
2. Configure `ingestream/cmd/config.json` for AMI (`127.0.0.1:5038`) and Redis
3. Insert ClearStream on the media path (RTP proxy or WS bridge) **before** audio reaches STT/Streamix
4. Validate with `../sipp/` scenarios against Asterisk

Add scripts in this directory as you wire endpoints (e.g. `run_local_stack.sh`, AMI dialplan hooks).
