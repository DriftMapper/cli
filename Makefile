# Entry points for the Go binary and its npm wrapper. Run `make` with no
# target to list everything below; each target's comment is both the docs
# and the source of truth CI runs against (see .github/workflows/ci.yml).
#
# Two toolchains live here: Go (cmd/driftmapper, internal/) and npm
# (npm/wrapper). `check` runs everything both CI jobs run, in the same order.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

BIN := bin/driftmapper
TARGETS := darwin-arm64 darwin-x64 linux-arm64 linux-x64 win32-arm64 win32-x64

.PHONY: help build vet format test cross npm-test e2e npm-check check clean

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

build: ## Build the driftmapper binary for the host platform into bin/driftmapper
	go build -o $(BIN) ./cmd/driftmapper

vet: ## Run go vet across all packages
	go vet ./...

format: ## Run go fmt across all files
	go fmt ./...

test: vet ## Run go vet, then the Go test suite
	go test ./...

cross: ## Cross-compile all six release targets into /tmp, and check the build is deterministic (mirrors release.yml)
	@for target in $(TARGETS); do \
		os="$${target%-*}"; arch="$${target##*-}"; \
		goos="$$os"; [ "$$os" = "win32" ] && goos="windows"; \
		goarch="$$arch"; [ "$$arch" = "x64" ] && goarch="amd64"; \
		out="/tmp/driftmapper-$${target}"; \
		[ "$$goos" = "windows" ] && out="$${out}.exe"; \
		echo "==> $$target (GOOS=$$goos GOARCH=$$goarch)"; \
		CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" \
			go build -trimpath -ldflags="-s -w" -o "$$out" ./cmd/driftmapper; \
		CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" \
			go build -trimpath -ldflags="-s -w" -o "$${out}.rebuild" ./cmd/driftmapper; \
		cmp -s "$$out" "$${out}.rebuild" || { echo "FAIL: $$target build is not reproducible"; exit 1; }; \
		rm -f "$${out}.rebuild"; \
	done

npm-test: ## Run the npm wrapper's unit tests (resolve.js logic)
	cd npm/wrapper && node --test

e2e: ## Pack-and-install e2e: builds a real driftmapper binary, exercises the launcher
	cd npm/wrapper && npm run test:e2e

npm-check: npm-test e2e ## Run both npm wrapper checks (unit tests + e2e)

check: test npm-check ## Run everything CI runs: go vet+test, npm unit tests, e2e

clean: ## Remove build artifacts
	rm -rf bin
