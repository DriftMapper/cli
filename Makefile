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
DIST := dist

# sha256sum is GNU coreutils and isn't on macOS by default; shasum -a 256 is
# the BSD/macOS equivalent, and accepts the same "-c checkfile" form.
# release-build/publish-platforms run on both a CI ubuntu runner and a
# developer's Mac (bootstrap publish), so neither tool alone is safe to
# hardcode. Resolved once at parse time, not per-invocation.
ifeq ($(shell command -v sha256sum 2>/dev/null),)
SHA256 := shasum -a 256
else
SHA256 := sha256sum
endif

# --provenance attests via the CI provider's OIDC token (GitHub Actions'
# id-token: write here) — there is no such provider on a developer's laptop,
# so npm publish rejects it with EUSAGE ("provider: null") outside CI. Every
# publish target defaults to it on; a local bootstrap publish overrides with
# PROVENANCE=false. release.yml never sets this, so CI always gets true —
# nothing to remember to flip back.
PROVENANCE ?= true
PROVENANCE_FLAG = $(if $(filter true,$(PROVENANCE)),--provenance,--no-provenance)

.PHONY: help build vet format test cross npm-test e2e npm-check check clean \
	release-build publish-platforms wait-platforms publish-wrapper publish-npm require-version

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

require-version:
	@test -n "$(VERSION)" || { echo "VERSION is required, e.g. make $(MAKECMDGOALS) VERSION=1.2.3"; exit 1; }

release-build: require-version ## Cross-compile all six release targets, version-stamped, into $(DIST)/ (requires VERSION=x.y.z; mirrors release.yml's build job)
	@mkdir -p $(DIST)
	@for target in $(TARGETS); do \
		os="$${target%-*}"; arch="$${target##*-}"; \
		goos="$$os"; [ "$$os" = "win32" ] && goos="windows"; \
		goarch="$$arch"; [ "$$arch" = "x64" ] && goarch="amd64"; \
		asset="$(DIST)/driftmapper-$${target}"; \
		[ "$$goos" = "windows" ] && asset="$${asset}.exe"; \
		echo "==> $$target (GOOS=$$goos GOARCH=$$goarch)"; \
		CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" \
			go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o "$$asset" ./cmd/driftmapper; \
		chmod +x "$$asset"; \
		(cd $(DIST) && $(SHA256) "$$(basename "$$asset")" > "$$(basename "$$asset").sha256"); \
	done

# Idempotent (skips a target already published at VERSION) so a failed
# partial run can just be re-invoked — see CLAUDE.md's "publish platform
# packages first, idempotently" gotcha. Expects $(DIST)/driftmapper-<target>
# (+ .sha256, verified if present) to already exist — from release-build
# locally, or from `gh release download` in CI.
publish-platforms: require-version ## Stamp+publish all six @driftmapper/cli-<target> packages from binaries in $(DIST)/ (requires VERSION=x.y.z)
	@for target in $(TARGETS); do \
		os="$${target%-*}"; \
		goos="$$os"; [ "$$os" = "win32" ] && goos="windows"; \
		asset="$(DIST)/driftmapper-$${target}"; \
		binname="driftmapper"; \
		if [ "$$goos" = "windows" ]; then asset="$${asset}.exe"; binname="driftmapper.exe"; fi; \
		pkg_dir="npm/platforms/cli-$${target}"; \
		pkg_name="@driftmapper/cli-$${target}"; \
		if npm view "$${pkg_name}@$(VERSION)" version >/dev/null 2>&1; then \
			echo "$${pkg_name}@$(VERSION) already published — skipping"; continue; \
		fi; \
		echo "==> $$target"; \
		test -f "$$asset" || { echo "missing $$asset — run 'make release-build VERSION=$(VERSION)' first, or download release assets into $(DIST)/"; exit 1; }; \
		if [ -f "$${asset}.sha256" ]; then (cd "$$(dirname "$$asset")" && $(SHA256) -c "$$(basename "$$asset").sha256"); fi; \
		mkdir -p "$$pkg_dir/bin"; \
		cp "$$asset" "$$pkg_dir/bin/$$binname"; \
		chmod +x "$$pkg_dir/bin/$$binname"; \
		node -e "\
			const fs = require('fs'); \
			const p = '$$pkg_dir/package.json'; \
			const pkg = JSON.parse(fs.readFileSync(p, 'utf-8')); \
			pkg.version = '$(VERSION)'; \
			fs.writeFileSync(p, JSON.stringify(pkg, null, 2) + '\n');"; \
		tgz=$$(cd "$$pkg_dir" && npm pack --silent --pack-destination /tmp); \
		tar -tvf "/tmp/$$tgz" | grep "bin/$$binname" | grep -q -- '-rwx' || { echo "exec bit missing on packed $$binname for $$target"; exit 1; }; \
		rm -f "/tmp/$$tgz"; \
		(cd "$$pkg_dir" && npm publish $(PROVENANCE_FLAG) --access public); \
	done

# The wrapper's optionalDependencies pin these exactly, not a caret (see
# CLAUDE.md) — never call publish-wrapper before every target here is
# actually visible on the registry.
wait-platforms: require-version ## Poll the registry until all six platform packages are visible at VERSION (requires VERSION=x.y.z)
	@for target in $(TARGETS); do \
		pkg_name="@driftmapper/cli-$${target}"; \
		printf 'waiting for %s@%s ' "$$pkg_name" "$(VERSION)"; \
		ok=false; \
		for attempt in $$(seq 1 30); do \
			if npm view "$${pkg_name}@$(VERSION)" version >/dev/null 2>&1; then ok=true; break; fi; \
			printf '.'; \
			sleep 10; \
		done; \
		if [ "$$ok" = true ]; then \
			echo "found (attempt $$attempt/30)"; \
		else \
			echo "FAILED"; \
			echo "$${pkg_name}@$(VERSION) never became visible on the registry after 30 attempts (5 min)"; \
			exit 1; \
		fi; \
	done

publish-wrapper: require-version ## Stamp+publish the @driftmapper/cli wrapper package (requires VERSION=x.y.z)
	@node -e "\
		const fs = require('fs'); \
		const p = 'npm/wrapper/package.json'; \
		const pkg = JSON.parse(fs.readFileSync(p, 'utf-8')); \
		pkg.version = '$(VERSION)'; \
		for (const dep of Object.keys(pkg.optionalDependencies)) { \
			pkg.optionalDependencies[dep] = '$(VERSION)'; \
		} \
		fs.writeFileSync(p, JSON.stringify(pkg, null, 2) + '\n');"
	cd npm/wrapper && npm publish $(PROVENANCE_FLAG) --access public

publish-npm: publish-platforms wait-platforms publish-wrapper ## Full publish sequence: platform packages, wait, then wrapper (requires VERSION=x.y.z; same order release.yml uses)

npm-test: ## Run the npm wrapper's unit tests (resolve.js logic)
	cd npm/wrapper && node --test

e2e: ## Pack-and-install e2e: builds a real driftmapper binary, exercises the launcher
	cd npm/wrapper && npm run test:e2e

npm-check: npm-test e2e ## Run both npm wrapper checks (unit tests + e2e)

check: test npm-check ## Run everything CI runs: go vet+test, npm unit tests, e2e

clean: ## Remove build artifacts
	rm -rf bin
