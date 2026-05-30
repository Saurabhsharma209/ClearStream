.PHONY: build test test-race test-nocgo lint fmt clean install

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

.DEFAULT_GOAL := build
