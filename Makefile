.PHONY: build test test-race test-nocgo lint fmt clean install 
        poc poc-build poc-up poc-clean poc-generate-audio

GOFLAGS ?=
BINARY  := clearstream

build:
	go build $(GOFLAGS) ./...

test:
	go test $(GOFLAGS) ./...

test-race:
	go test -race $(GOFLAGS) ./...

test-nocgo:
	CGO_ENABLED=0 go test ./...

bench:
	go test -bench=. -benchmem ./...

lint:
	go vet ./...

fmt:
	go fmt ./...

clean:
	go clean ./...
	rm -f $(BINARY)

install:
	go install ./cmd/clearstream

# --- POC targets ---

poc-generate-audio:
	mkdir -p testdata
	go run testdata/generate_noisy.go

poc-build:
	docker compose build

poc-up:
	docker compose up --abort-on-container-exit

poc-clean:
	docker compose down -v

poc: poc-generate-audio poc-build poc-up

.DEFAULT_GOAL := build
