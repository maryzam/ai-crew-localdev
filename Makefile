.PHONY: build build-agent build-broker build-credential-helper build-gh test verify docs-check lint clean install readiness readiness-devcontainer langfuse-up langfuse-down setup-hooks

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/maryzam/ai-crew-localdev/internal/cli.Version=$(VERSION)"
GOLANGCI_LINT ?= $(shell go env GOPATH)/bin/golangci-lint

build: build-agent build-broker build-credential-helper build-gh

build-agent:
	go build $(LDFLAGS) -o bin/ai-agent ./cmd/ai-agent

build-broker:
	go build $(LDFLAGS) -o bin/ai-agent-broker ./cmd/ai-agent-broker

build-credential-helper:
	go build -o bin/ai-agent-credential-helper ./cmd/ai-agent-credential-helper

build-gh:
	go build -o bin/ai-agent-gh ./cmd/ai-agent-gh

test:
	go test ./...

verify: build docs-check
	go test -race -count=1 ./...
	go test -tags integration -run '^$$' ./...
	go vet ./...
	go vet -tags integration ./...
	$(MAKE) lint

docs-check:
	@set -e; \
	missing=0; \
	for tool in lychee markdownlint-cli2 codespell; do \
		if ! command -v $$tool >/dev/null 2>&1; then \
			echo "docs-check: $$tool is not installed" >&2; \
			missing=1; \
		fi; \
	done; \
	if [ "$$missing" -ne 0 ]; then \
		if [ "$$CI" = "true" ]; then \
			exit 1; \
		fi; \
		echo "docs-check: skipping locally; install lychee, markdownlint-cli2, and codespell to run it" >&2; \
		exit 0; \
	fi; \
	lychee --offline --no-progress 'docs/**/*.md' README.md; \
	markdownlint-cli2 'docs/**/*.md' 'README.md'; \
	codespell docs/ README.md

lint:
	$(GOLANGCI_LINT) run

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
