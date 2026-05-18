# WebbPlace backend build system
SHELL := /bin/bash
MODULE := github.com/OfficialA1manac/MagicWebb/backend

# ── Toolchain ─────────────────────────────────────────────────────────────
PROTOC        := protoc
PROTOC_GEN_GO := protoc-gen-go
BUF           := buf
ZIG           := zig
GOOSE         := goose

# ── Directories ───────────────────────────────────────────────────────────
PROTO_DIR := proto
GEN_DIR   := gen
HOT_DIR   := internal/hot
HOT_LIB   := $(HOT_DIR)/lib/libwebbhot.so

.PHONY: all proto zig build build-api build-indexer migrate migrate-down \
        test lint clean

all: zig proto build

# ── Zig shared library ────────────────────────────────────────────────────
zig: $(HOT_LIB)

$(HOT_LIB): $(HOT_DIR)/hot.zig $(HOT_DIR)/hot.h
	@mkdir -p $(HOT_DIR)/lib
	$(ZIG) build-lib \
		-O ReleaseFast \
		-dynamic \
		$(HOT_DIR)/hot.zig \
		--name webbhot \
		-femit-bin=$(HOT_LIB)

# ── Protobuf code generation ──────────────────────────────────────────────
proto:
	@mkdir -p $(GEN_DIR)
	$(BUF) generate

# Fallback without buf:
proto-raw:
	$(PROTOC) \
		-I $(PROTO_DIR) \
		-I $(shell go env GOPATH)/pkg/mod/github.com/googleapis/googleapis@latest \
		--go_out=$(GEN_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
		$(shell find $(PROTO_DIR) -name '*.proto')

# ── Go build ──────────────────────────────────────────────────────────────
build: build-api build-indexer

build-api:
	CGO_ENABLED=1 go build \
		-ldflags="-s -w" \
		-o bin/api \
		./cmd/api

build-indexer:
	CGO_ENABLED=1 go build \
		-ldflags="-s -w" \
		-o bin/indexer \
		./cmd/indexer

# ── Database migrations ───────────────────────────────────────────────────
DB_URL ?= $(POSTGRES_URL)

migrate:
	$(GOOSE) -dir internal/db/migrations postgres "$(DB_URL)" up

migrate-down:
	$(GOOSE) -dir internal/db/migrations postgres "$(DB_URL)" down

migrate-status:
	$(GOOSE) -dir internal/db/migrations postgres "$(DB_URL)" status

# ── Tests ─────────────────────────────────────────────────────────────────
test:
	go test ./... -race -count=1 -timeout 120s

test-integration:
	go test ./... -race -count=1 -timeout 300s -tags integration

# ── Lint ──────────────────────────────────────────────────────────────────
lint:
	golangci-lint run ./...

# ── Clean ─────────────────────────────────────────────────────────────────
clean:
	rm -rf bin/ $(GEN_DIR)/ $(HOT_DIR)/lib/
