BINARY := multica-notify
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
COMPOSE_TEST := deploy/compose/test.yml

.PHONY: help
help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: clean
clean: ## Remove build outputs and test caches
	rm -rf bin dist
	go clean -testcache

.PHONY: build
build: ## Build the bridge binary for the host platform
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

.PHONY: mocksender
mocksender: ## Build the signed test-event sender
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/mocksender ./cmd/mocksender

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
package: ## Cross-compile linux/arm64 + amd64 release artifacts into dist/
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-$(VERSION)-linux-arm64 ./cmd/$(BINARY)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-$(VERSION)-linux-amd64 ./cmd/$(BINARY)
	cd dist && sha256sum $(BINARY)-$(VERSION)-linux-* > sha256sums.txt
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
