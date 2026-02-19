#!/usr/bin/env bash
# scripts/build.sh — build, vet, and optionally test the gateway binary.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${ROOT}/bin/gateway"

echo "→ go vet"
go vet ./...

echo "→ go build → ${BIN}"
mkdir -p "${ROOT}/bin"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "${BIN}" ./cmd/gateway

echo "✓ built ${BIN}"
