.PHONY: build devcontainer-assets langfuse-assets dist dist-checksums install-script-test env-contract neutral-fixtures journey e2e-live test verify verify-code verify-go verify-telemetry telemetry-schema docs-check semantic-check dependency-check source-comments adr-gate-test ci-classifier-test lint clean install readiness readiness-login readiness-devcontainer readiness-project-devcontainer langfuse-up langfuse-down setup-hooks

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/maryzam/ai-crew-localdev/internal/cli.Version=$(VERSION)"
BUILDVCS ?= true
GOLANGCI_LINT ?= $(shell go env GOPATH)/bin/golangci-lint
INSTALL_DIR := $(or $(shell go env GOBIN),$(HOME)/.local/bin)

DIST_GOARCH ?= $(shell go env GOARCH)

build:
	CGO_ENABLED=0 go build -buildvcs=$(BUILDVCS) $(LDFLAGS) -o bin/ai-agent ./cmd/ai-agent
	ln -sf ai-agent bin/ai-agent-broker
	ln -sf ai-agent bin/ai-agent-gh
	ln -sf ai-agent bin/ai-agent-credential-helper

langfuse-assets:
	cp contrib/langfuse/docker-compose.yml contrib/langfuse/.env.example internal/runtime/uphost/assets/langfuse/

devcontainer-assets:
	cp .devcontainer/Dockerfile .devcontainer/devcontainer.json .devcontainer/entrypoint.sh internal/runtime/devcontainer/assets/generic/

dist:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=$(DIST_GOARCH) go build -buildvcs=$(BUILDVCS) -trimpath $(LDFLAGS) -o dist/ai-agent-linux-$(DIST_GOARCH) ./cmd/ai-agent

dist-checksums:
	cd dist && sha256sum ai-agent-linux-* > SHA256SUMS

install-script-test:
	bash scripts/check-install-script_test.sh

env-contract:
	bash scripts/check-env-contract.sh

test:
	go test ./...

verify: docs-check verify-code

verify-code: BUILDVCS=false
verify-code: build semantic-check dependency-check env-contract neutral-fixtures source-comments adr-gate-test ci-classifier-test install-script-test verify-telemetry verify-go

verify-go:
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

semantic-check:
	scripts/check-doc-identifiers.sh

dependency-check:
	scripts/check-dependencies.sh

neutral-fixtures:
	scripts/check-neutral-fixtures.sh

source-comments:
	scripts/check-source-comments.sh

adr-gate-test:
	scripts/check-adr-gate_test.sh

ci-classifier-test:
	scripts/classify-ci-changes_test.sh

telemetry-schema:
	go run ./cmd/telemetry-schema

verify-telemetry:
	go test ./internal/platform/telemetry
	go test ./internal/app/adaptive
	go test ./internal/runtime/launcher -run TelemetryInvariant
	go test ./internal/cli -run Runs
	go run ./cmd/telemetry-schema -check

lint:
	$(GOLANGCI_LINT) run

readiness:
	bash ./scripts/devcontainer-readiness.sh

readiness-login:
	go test -tags integration -run TestLoginPersistsAcrossContainerReplacement -timeout 30m ./test/e2e/ -count=1

readiness-devcontainer:
	go test -tags integration -run TestDevcontainerCLIWorkflow -timeout 30m ./test/e2e/

journey:
	go test -tags integration -run TestCleanHostJourney -timeout 30m ./test/e2e/ -count=1

e2e-live: build readiness readiness-login readiness-devcontainer readiness-project-devcontainer journey
	go test -tags integration -run 'TestLive' -timeout 60m ./test/e2e/ -count=1 -v

readiness-project-devcontainer:
	go test -tags integration -run TestProjectDevcontainerE2E -timeout 45m ./test/e2e/ -count=1

langfuse-up:
	docker compose -f contrib/langfuse/docker-compose.yml up -d

langfuse-down:
	docker compose -f contrib/langfuse/docker-compose.yml down

clean:
	rm -rf bin/

setup-hooks:
	git config core.hooksPath .githooks

install: build setup-hooks
	mkdir -p $(INSTALL_DIR)
	cp -f bin/ai-agent $(INSTALL_DIR)/
	ln -sf ai-agent $(INSTALL_DIR)/ai-agent-broker
	ln -sf ai-agent $(INSTALL_DIR)/ai-agent-gh
	ln -sf ai-agent $(INSTALL_DIR)/ai-agent-credential-helper
	@echo "installed ai-agent multi-call binary and symlinks to $(INSTALL_DIR)"
