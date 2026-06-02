.PHONY: build test lint fmt vet clean poc bench test-race test-nocgo install poc-build poc-up poc-clean poc-generate-audio

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

.DEFAULT_GOAL := build
