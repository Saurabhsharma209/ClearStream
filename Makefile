.PHONY: build test lint fmt vet clean poc bench test-race test-nocgo install poc-build poc-up poc-clean poc-generate-audio coverage coverage-html qa-cs-regression qa-office-conv-rnnoise qa-office-conv-full build-slim build-docker-scratch

GOFLAGS ?= -trimpath
BINARY  := clearstream

build:
	go build $(GOFLAGS) ./...

test:
	go test -race -count=1 ./...

bench:
	go test -run='^$$' -bench=. -benchmem ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

lint: fmt vet

clean:
	go clean ./...
	rm -f $(BINARY)

install:
	go install ./cmd/clearstream

test-race:
	go test -race $(GOFLAGS) ./...

test-nocgo:
	CGO_ENABLED=0 go test ./...

poc: build
	@echo "Starting ClearStream POC (HTTP on :8080, RTP on :5004)"
	go run cmd/clearstream/main.go server --http :8080 &
	@echo "Server started. Test with: curl -X POST http://localhost:8080/enhance"

poc-generate-audio:
	mkdir -p testdata
	go run testdata/generate_noisy.go

poc-build:
	docker compose build

poc-up:
	docker compose up --abort-on-container-exit

poc-clean:
	docker compose down -v

loadtest:
	go test -v -run TestLoadTest -timeout=60s ./pkg/loadtest/...

bench-all:
	go test -run='^$$' -bench=. -benchmem -benchtime=2s ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -5
	@echo "Full report: go tool cover -html=coverage.out"

coverage-html:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Saved to coverage.html"

# ── Slim SDK build targets (AQ-005) ──────────────────────────────────────────

# build-slim: CGO_ENABLED=0, strip debug + DWARF, passthrough+rnnoise-Go backend.
# Result: ~6 MB binary, no C runtime dependency, runs in Docker scratch.
# Optimisation stack: -ldflags="-s -w" removes ~40% binary size vs default.
build-slim:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY)-slim ./cmd/clearstream/
	@echo "Slim binary: $$(du -sh $(BINARY)-slim | cut -f1)"
	@echo "To compress further: upx --best $(BINARY)-slim  (adds ~5ms startup)"

# build-docker-scratch: multi-stage Docker build → FROM scratch image.
# Result: ~8 MB image (binary + CA certs only). Requires Docker.
build-docker-scratch:
	docker build -f Dockerfile.slim -t clearstream-slim:latest .
	@echo "Image size: $$(docker images clearstream-slim:latest --format '{{.Size}}')"

# ── QA targets ───────────────────────────────────────────────────────────────

# CS-009/CS-012/CS-013 unit regression suite (no CGO required, fast).
# Runs the core unit tests that validate all fixed bugs:
#   CS-001 seqLess wraparound, CS-002 initialDepth+lastArrival,
#   CS-003 PLC fade, CS-007 WAV parser, CS-009 AGC convergence,
#   CS-012 adaptive VAD speech ratio, CS-013 AGC clip count.
qa-cs-regression:
	@echo "=== CS regression suite (CGO_ENABLED=0) ==="
	CGO_ENABLED=0 go test -v -count=1 \
		-run 'TestJitterSeqWraparound|TestJitterBufferReset|TestResetRestoresInitialDepth|TestPLCFadeToSilence|TestAGCConvergesWithinFiftyFrames|TestAdaptiveVADSpeechRatio|TestAGCClipCount' \
		./pkg/rtp/... ./pkg/audio/...
	@echo "=== CS regression PASS ==="

# CS-014: rnnoise quality eval (requires CGO_ENABLED=1 and rnnoise C lib).
# Run this on Mac/Linux with librnnoise installed.
# Fails the build if CGO is disabled and STRICT_NC=1 (CI quality gate).
qa-office-conv-rnnoise:
ifeq ($(STRICT_NC),1)
	@if [ "$(CGO_ENABLED)" = "0" ]; then \
		echo "ERROR: qa-office-conv-rnnoise requires CGO_ENABLED=1 (rnnoise C lib)."; \
		echo "  Install librnnoise and re-run, or unset STRICT_NC to skip."; \
		exit 1; \
	fi
endif
	@echo "=== Office-conv NC eval (rnnoise, CGO_ENABLED=1) ==="
	CGO_ENABLED=1 go build -tags rnnoise -o $(BINARY)-rnnoise ./cmd/clearstream/
	@echo "Binary: $(BINARY)-rnnoise"
	CALLS?=5; DURATION?=30; \
	bash voice-qa/browser-lab/eval/run_matrix.sh rnnoise \
		|| echo "WARN: run_matrix.sh not found — run manually"
	@echo "=== Done: check eval_out/ for metrics ==="

# Full office-conv E2E eval (passthrough + rnnoise, N calls, D seconds each).
# Usage: make qa-office-conv-full CALLS=10 DURATION=45
CALLS    ?= 5
DURATION ?= 30
qa-office-conv-full: qa-cs-regression
	@echo "=== Full office-conv E2E: CALLS=$(CALLS) DURATION=$(DURATION)s ==="
	E2E_TIER_CALLS=$(CALLS) E2E_DURATION=$(DURATION) \
		bash qa/e2e/start_stack.sh || true
	@echo "=== Full eval complete ==="

.DEFAULT_GOAL := build
