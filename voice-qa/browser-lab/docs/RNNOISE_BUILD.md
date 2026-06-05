# RNNoise build (L1 quality tier)

L0 latency contract uses **passthrough** only. RNNoise is measured under **L1** (separate p99 budget, typically &lt;20 ms per frame).

## macOS (Homebrew)

```bash
brew install rnnoise
export CGO_ENABLED=1
export CGO_CFLAGS="-I$(brew --prefix rnnoise)/include"
export CGO_LDFLAGS="-L$(brew --prefix rnnoise)/lib -lrnnoise"
go build -tags rnnoise ./...
```

## Linux (Debian/Ubuntu)

```bash
sudo apt-get install -y librnnoise-dev pkg-config
export CGO_ENABLED=1
go build -tags rnnoise ./...
```

## Verify

```bash
go run examples/voice_ai_lab/bridge/main.go --model rnnoise
go run tools/latency_harness/main.go -backend rnnoise -frames 10000 -json /tmp/l1.json
go run tools/snr_benchmark/main.go -noisy testdata/noisy.wav -clean testdata/enhanced.wav
```

Without CGO/tags, `rnnoise` backend falls back to passthrough with a one-time stderr warning.

## Voice AI Lab condition C

```bash
# Terminal 1
go run examples/voice_ai_lab/bridge/main.go --model rnnoise

# Terminal 2
python orchestrator.py --bridge ws://localhost:8081/stream --condition C
```

Fill [REPORT_TEMPLATE.md](../REPORT_TEMPLATE.md) L1 rows after `eval/run_matrix.sh`.
