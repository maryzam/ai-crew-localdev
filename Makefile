.PHONY: build test lint clean install

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/maryzam/ai-crew-localdev/internal/cli.Version=$(VERSION)"

build:
	go build $(LDFLAGS) -o bin/ai-agent ./cmd/ai-agent

test:
	go test ./...

lint:
	golangci-lint run

clean:
	rm -rf bin/

install: build
	mkdir -p ~/.local/bin
	cp bin/ai-agent ~/.local/bin/
