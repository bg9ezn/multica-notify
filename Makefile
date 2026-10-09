BINARY := multica-notify
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
VERSION_FLAG := -X github.com/bg9ezn/multica-notify/internal/version.Value=$(VERSION)
# Release artifacts are stripped; dev builds keep symbols so panics stay readable.
RELEASE_LDFLAGS := -s -w $(VERSION_FLAG)
COMPOSE_TEST := deploy/compose/test.yml
# Release targets: os/arch pairs, extensible one-line-per-platform.
# Raspberry Pi (64-bit OS) is linux/arm64 — the same artifact as any
# 64-bit Linux. linux/arm is built with GOARM=6 (named armv6) so one
# artifact covers every 32-bit ARM: Pi Zero/1 (v6) through Pi 2/3 on a
# 32-bit OS (v7+). linux/loong64 covers Loongson (LoongArch64).
# darwin binaries are unsigned — macOS Gatekeeper needs
# `xattr -d com.apple.quarantine <binary>` on first run.
PLATFORMS := linux/amd64 linux/arm64 linux/loong64 linux/riscv64 linux/arm windows/amd64 windows/arm64 darwin/amd64 darwin/arm64

.PHONY: help
help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: clean
clean: ## Remove build outputs and test caches
	rm -rf bin dist
	go clean -testcache

.PHONY: build
build: ## Build the bridge binary for the host platform (unstripped, version stamped)
	mkdir -p bin
	go build -trimpath -ldflags "$(VERSION_FLAG)" -o bin/$(BINARY) ./cmd/$(BINARY)

.PHONY: mocksender
mocksender: ## Build the signed test-event sender
	mkdir -p bin
	go build -trimpath -o bin/mocksender ./cmd/mocksender

.PHONY: test
test: ## Run unit tests (race detector on)
	go test -race -count=1 ./...

.PHONY: test-integration
test-integration: ## Run integration tests against a real ntfy (docker compose)
	docker compose -f $(COMPOSE_TEST) up -d --wait
	@rc=0; go test -race -tags=integration -count=1 ./test/integration/... || rc=$$?; \
	docker compose -f $(COMPOSE_TEST) down --remove-orphans >/dev/null 2>&1 || true; \
	exit $$rc

.PHONY: lint
lint: ## gofmt (no diff allowed) + go vet
	@unformatted=$$(gofmt -l .); if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; fi
	go vet ./...

.PHONY: package
package: ## Cross-compile release artifacts for all PLATFORMS into dist/
	mkdir -p dist
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		goarm=""; namearch=$$arch; \
		if [ "$$os/$$arch" = "linux/arm" ]; then goarm="GOARM=6"; namearch="armv6"; fi; \
		ext=$$([ "$$os" = "windows" ] && echo .exe || echo ""); \
		echo "building $$os/$$namearch"; \
		\
		env CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $$goarm go build -trimpath -ldflags "$(RELEASE_LDFLAGS)" \
			-o "dist/$(BINARY)-$(VERSION)-$$os-$$namearch$$ext" ./cmd/$(BINARY); \
	done
	cd dist && sha256sum $(BINARY)-$(VERSION)-* > sha256sums.txt
	@echo "artifacts in dist/:"
	@ls -l dist/

.PHONY: run
run: ## Run the bridge with the example config (plain HTTP, no TLS)
	go run ./cmd/$(BINARY) -config deploy/examples/config.example.yaml

.PHONY: manifest-pack
manifest-pack: ## Zip the plugin manifest for upload into Multica
	mkdir -p dist
	cd manifest && zip -q ../dist/multica.plugin.json.zip multica.plugin.json && cd ..
	@echo "wrote dist/multica.plugin.json.zip (replace BRIDGE_HOST_PLACEHOLDER before installing)"

.PHONY: verify
verify: lint build test ## Everything CI runs on a push
