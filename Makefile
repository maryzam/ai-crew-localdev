.PHONY: build build-agent build-broker build-credential-helper build-gh test verify docs-check lint clean install readiness readiness-devcontainer langfuse-up langfuse-down setup-hooks

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/maryzam/ai-crew-localdev/internal/cli.Version=$(VERSION)"
GOLANGCI_LINT ?= $(shell go env GOPATH)/bin/golangci-lint
GO_BUILD_FLAGS ?= -buildvcs=false

build: build-agent build-broker build-credential-helper build-gh

build-agent:
	go build $(GO_BUILD_FLAGS) $(LDFLAGS) -o bin/ai-agent ./cmd/ai-agent

build-broker:
	go build $(GO_BUILD_FLAGS) $(LDFLAGS) -o bin/ai-agent-broker ./cmd/ai-agent-broker

build-credential-helper:
	go build $(GO_BUILD_FLAGS) -o bin/ai-agent-credential-helper ./cmd/ai-agent-credential-helper

build-gh:
	go build $(GO_BUILD_FLAGS) -o bin/ai-agent-gh ./cmd/ai-agent-gh

test:
	go test ./...

verify: build docs-check
	go test -race -count=1 ./...
	go test -tags integration -run '^$$' ./...
	go vet ./...
	go vet -tags integration ./...
	$(MAKE) lint

docs-check:
	lychee --no-progress 'docs/**/*.md' README.md
	markdownlint-cli2 'docs/**/*.md' 'README.md'
	codespell docs/ README.md

lint:
	GOFLAGS=-buildvcs=false $(GOLANGCI_LINT) run

readiness:
	bash ./scripts/devcontainer-readiness.sh

readiness-devcontainer:
	go test -tags integration -run TestDevcontainerCLIWorkflow -timeout 30m ./internal/e2e/

langfuse-up:
	docker compose -f contrib/langfuse/docker-compose.yml up -d

langfuse-down:
	docker compose -f contrib/langfuse/docker-compose.yml down

clean:
	rm -rf bin/

setup-hooks:
	git config core.hooksPath .githooks

install: build setup-hooks
	mkdir -p ~/.local/bin
	cp bin/ai-agent bin/ai-agent-broker bin/ai-agent-credential-helper bin/ai-agent-gh ~/.local/bin/
