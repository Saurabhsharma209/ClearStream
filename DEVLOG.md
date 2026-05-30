## 2026-05-30

**Agents run:** API Layer, AI Model, QA/Testing
**Build:** passing (CGO_ENABLED=0)

### Changes
- clearstream.go: Added Version constant, Config.Validate(), full Go doc comments
- cmd/clearstream/main.go: Fixed CLI compile error (clearstream.FileOptions ? file.Options), removed broken init()
- pkg/model/rnnoise.go: Added //go:build cgo tag, fixed upsample3x/downsample3x to use linear interpolation
- pkg/model/rnnoise_nocgo.go: New file Ñ graceful fallback to passthrough when CGo unavailable
- pkg/model/bench_test.go: BenchmarkPassthrough, TestPassthroughRoundtrip, TestNewSuppressor*
- Makefile: build/test/test-race/test-nocgo/bench/lint/fmt/clean/install targets
- pkg/audio/pipeline_test.go: 5 tests Ñ frame boundaries, flush, reset, passthrough fidelity
- pkg/rtp/jitter_test.go: 6 tests Ñ in-order, out-of-order, packet loss, seq wraparound, reset
- .github/workflows/ci.yml: CI on push/PR to main (Go 1.22, FFmpeg, RNNoise, race detector)

### Blocked
- DeepFilterNet ONNX: needs ONNX Runtime Go bindings + exported model file (manual setup)
- go.sum: needs `go mod tidy` run locally to populate after adding pion/rtp deps

### Tomorrow
- pkg/audio: Add VAD (voice activity detection) energy-threshold implementation
- pkg/rtp: Fix G.711 µ-law/A-law round-trip correctness + add SSRC change detection
- pkg/file: Add OnProgress callback + ProcessDir() batch processing
