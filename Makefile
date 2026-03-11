.PHONY: build build-broker test lint clean install

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/maryzam/ai-crew-localdev/internal/cli.Version=$(VERSION)"

build: build-agent build-broker

build-agent:
	go build $(LDFLAGS) -o bin/ai-agent ./cmd/ai-agent

build-broker:
	go build $(LDFLAGS) -o bin/ai-agent-broker ./cmd/ai-agent-broker

test:
	go test ./...

lint:
	golangci-lint run

clean:
	rm -rf bin/

install: build
	mkdir -p ~/.local/bin
	cp bin/ai-agent bin/ai-agent-broker ~/.local/bin/
