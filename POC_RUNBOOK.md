# ClearStream POC Runbook
## "Noise-free Voice AI in 10 minutes"

## Prerequisites (one-time setup)
1. Go 1.22+ installed
2. FFmpeg installed (brew install ffmpeg)
3. Clone: git clone https://github.com/Saurabhsharma209/ClearStream && cd ClearStream
4. go mod tidy && go build ./...

## Demo 1: File Enhancement (2 min)
### Generate test audio
go run tools/gen_test_audio/main.go
# Creates: testdata/sample_clean.wav, sample_noisy.wav, sample_office.wav

### Listen to noisy audio (open in any player)
open testdata/sample_noisy.wav   # macOS
# You'll hear: sine tones + heavy static noise

### Run ClearStream
go run cmd/clearstream/main.go file -i testdata/sample_noisy.wav -o testdata/output_clean.wav

### Listen to clean audio
open testdata/output_clean.wav
# You'll hear: same tones, significantly reduced noise

### Measure improvement
go run tools/snr_benchmark/main.go
# Shows SNR before/after table

## Demo 2: Live HTTP API (2 min)
### Start server
go run cmd/clearstream/main.go server --http :8080 &

### Enhance via curl
curl -X POST http://localhost:8080/enhance 
  -F "audio=@testdata/sample_noisy.wav" 
  -o testdata/enhanced_via_api.wav

### Benchmark against live server
go run tools/snr_benchmark/main.go --server http://localhost:8080

### Check health + metrics
curl http://localhost:8080/health
curl http://localhost:8080/metrics

## Demo 3: Docker (3 min)
make poc

## Demo 4: Live RTP (advanced)
### Start ClearStream RTP listener
go run cmd/clearstream/main.go rtp --listen :5004 --forward localhost:5005 &

### Send test RTP stream
bash tools/send_rtp_test.sh localhost 5004

## Demo 5: AgentStream Integration
### See examples/exotel_integration/agentstream_connector.go
### The EnhanceAudio() function is a drop-in for your STT pipeline

## Integration Paths
| Scenario | How to integrate | Effort |
|---|---|---|
| File cleanup (recordings) | POST /enhance via HTTP | 10 min |
| AgentStream STT | Use EnhanceAudio() client | 30 min |
| Live SIP call | pkg/sip proxy, POST /sip/session/start | 2 hrs |
| Asterisk | EAGI handler binary | 2 hrs |
| WebRTC browser | WebSocket bridge :8081/stream | 2 hrs |
| Exotel Media Gateway | RTP intercept or WSS gate | 4 hrs |

## Expected SNR Results
| Noise type | Before | After (passthrough) | After (RNNoise) |
|---|---|---|---|
| Heavy static | ~5 dB | 5 dB (no-op) | ~18 dB |
| Office noise | ~9 dB | 9 dB (no-op) | ~22 dB |
| Call center | ~7 dB | 7 dB (no-op) | ~20 dB |

Note: In POC mode (CGO_ENABLED=0), passthrough model is used.
Real noise reduction requires: CGO_ENABLED=1 + libRNNoise installed.
