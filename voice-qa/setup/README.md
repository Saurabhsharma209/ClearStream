# Setup

## install_deps.sh

Installs **SIPp** and creates `env.local`. Asterisk 18 is **not** available as a standard Homebrew formula on Apple Silicon/macOS; use Docker instead.

```bash
bash install_deps.sh
source env.local
```

## Asterisk 18 + Redis (Ingestix)

```bash
bash start_ingestix_deps.sh
```

Uses `docker-compose.yml` (image `andrius/asterisk:18-current`, Redis 6.2).

Mount custom dialplan under `../asterisk/config/` when you wire AudioSocket/AMI for ingestream.

## Build ingestream

```bash
cd "$INGESTREAM_ROOT"
make build
cd target && ./ingestix
```

Configure `cmd/config.json` per your team (AMI host `127.0.0.1`, Redis URL from `env.local`).

## SIPp

Scenarios live in `../sipp/`. Example:

```bash
sipp -sn uac -d 10000 -s 1000 127.0.0.1:5060
```

Adjust extensions to match your Asterisk dialplan.
