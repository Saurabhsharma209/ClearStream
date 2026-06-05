# Asterisk 18 (local)

Production-like Asterisk **18.x** runs via Docker (see `../setup/docker-compose.yml`).

## Custom config

Drop overrides into `config/` (mounted read-only at `/etc/asterisk/custom` in the container). Enable for Ingestix:

- **AMI** on port 5038 (manager.conf)
- **AudioSocket** / streaming modules per ingestream docs
- **PJSIP** trunk or endpoints for SIPp UAC/UAS tests

## ClearStream dialplan

Use snippets from `ClearStream-1/examples/asterisk/extensions.conf` as a reference; adapt paths for your Docker layout.

## Verify

```bash
docker compose -f ../setup/docker-compose.yml ps
# AMI: telnet 127.0.0.1 5038  (after manager user configured)
```
