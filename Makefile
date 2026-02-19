# ─────────────────────────────────────────────────────────────────────────────
# GOLB — Makefile
# ─────────────────────────────────────────────────────────────────────────────

BINARY     := bin/gateway
IMAGE      := golb
TAG        ?= latest

# Version info — injected into the binary via -ldflags and into Docker via --build-arg.
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.buildDate=$(BUILD_DATE)

.PHONY: build build-healthcheck vet tidy \
        unit-test e2e-test test \
        docker-build docker-run docker-stop \
        clean help

# ── Go targets ───────────────────────────────────────────────────────────────

## build: compile the gateway binary into bin/
build:
	@echo "→ building gateway ($(VERSION))"
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/gateway
	@echo "✓ $(BINARY)"

## vet: run go vet on all packages
vet:
	go vet ./...

## tidy: run go mod tidy
tidy:
	go mod tidy

## unit-test: run unit and functional tests with race detector
unit-test:
	go test -v -race ./internal/...

## e2e-test: build the binary and run full end-to-end tests
e2e-test:
	go test -v -timeout 90s -race ./tests/e2e/...

## test: vet + unit-test + e2e-test
test: vet unit-test e2e-test

# ── Docker targets ───────────────────────────────────────────────────────────

## docker-build: build the Docker image with version labels
docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-f deployments/Dockerfile \
		-t $(IMAGE):$(TAG) \
		.

## docker-run: start gateway + echo backends via docker compose
docker-run:
	VERSION=$(VERSION) COMMIT=$(COMMIT) BUILD_DATE=$(BUILD_DATE) \
	docker compose -f deployments/docker-compose.yml up --build

## docker-stop: stop and remove compose containers
docker-stop:
	docker compose -f deployments/docker-compose.yml down

# ── Misc ─────────────────────────────────────────────────────────────────────

## clean: remove build artifacts
clean:
	rm -rf bin/

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'

.DEFAULT_GOAL := build
