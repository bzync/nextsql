# =============================================================================
# NextSQL Makefile
# Native Multimodel Database Engine
# =============================================================================

SHELL := /bin/bash
.DEFAULT_GOAL := help

# Versioning and Build Metadata
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

# Output directory
BIN_DIR := ./bin

.PHONY: help build build-server build-cli build-admin build-bench \
	test test-unit test-race test-pr test-production test-fault test-upgrade \
	test-nightly test-chaos test-soak test-soak-constrained test-admin-ui \
	test-drivers test-driver-go test-driver-node test-driver-bun \
	test-driver-python test-driver-php test-driver-ruby \
	fmt fmt-check vet lint tidy clean check-clean doctor

# -----------------------------------------------------------------------------
# Help
# -----------------------------------------------------------------------------
help: ## Show this help message with available targets
	@echo ""
	@echo "  NextSQL Build & Development Automation"
	@echo "  ======================================"
	@echo ""
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""

# -----------------------------------------------------------------------------
# Build Targets
# -----------------------------------------------------------------------------
build: build-server build-cli build-admin build-bench ## Build all NextSQL binaries into ./bin/

build-server: ## Compile nextsqld (core database daemon)
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nextsqld ./cmd/nextsqld

build-cli: ## Compile nextsql (operational and SQL query CLI)
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nextsql ./cmd/nextsql

build-admin: ## Compile nextsql-admin (unified Setup, Operations, & Studio binary)
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nextsql-admin ./cmd/nextsql-admin

build-bench: ## Compile nextsql-bench (workload and benchmark harness)
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nextsql-bench ./cmd/nextsql-bench

# -----------------------------------------------------------------------------
# Code Quality & Formatting
# -----------------------------------------------------------------------------
fmt: ## Format all Go code using gofmt -s
	gofmt -s -w .

fmt-check: ## Verify all Go source files adhere to gofmt -s
	@test -z "$$(gofmt -s -l .)" || (echo "Unformatted files found. Run 'make fmt':" && gofmt -s -l . && exit 1)

vet: ## Run standard go vet analyzer across all packages
	go vet ./...

lint: fmt-check vet ## Run formatting, vet, and frontend typechecks
	@if [ -d "internal/admin/frontend" ] && command -v npm >/dev/null 2>&1; then \
		echo "Checking frontend TypeScript types..."; \
		npm --prefix internal/admin/frontend run typecheck; \
	fi

tidy: ## Clean up unused Go module dependencies
	go mod tidy

# -----------------------------------------------------------------------------
# Testing: Standard & Profiles
# -----------------------------------------------------------------------------
test: ## Run standard package tests
	go test ./...

test-unit: ## Run fast unit tests
	go test -short ./...

test-race: ## Run race detector on concurrent packages
	go test -race ./internal/protocol ./internal/security ./internal/executor ./internal/storage ./internal/wal ./internal/txn

test-pr: ## Run PR evidence test profile (scripts/test-production.sh pr)
	./scripts/test-production.sh pr

test-production: ## Run full production durability evidence test profile
	./scripts/test-production.sh production

test-fault: ## Run storage I/O fault-injection tests
	./scripts/test-production.sh fault

test-upgrade: ## Run retained release fixture upgrade and compatibility tests
	./scripts/test-production.sh upgrade

test-nightly: ## Run nightly soak and evidence profile
	./scripts/test-production.sh nightly

test-chaos: ## Run crash recovery and corruption injection suite
	./scripts/test-production.sh chaos

test-soak: ## Run sustained B+Tree invariant soak test
	./scripts/run-btree-soak.sh

test-soak-constrained: ## Run memory-constrained B+Tree soak test
	./scripts/run-btree-soak-constrained.sh

test-admin-ui: ## Run NextSQL Admin frontend typecheck, studio, setup, revision, and axe a11y tests
	cd internal/admin/frontend && npm run typecheck && npm run test:studio \
	  && npm run test:setup && npm run test:revision && npm run test:a11y

# -----------------------------------------------------------------------------
# Testing: Client Drivers
# -----------------------------------------------------------------------------
test-drivers: test-driver-go test-driver-node test-driver-bun test-driver-python test-driver-php test-driver-ruby ## Run test suites for all 6 official client drivers

test-driver-go: ## Run Go driver tests
	go test -v ./drivers/go

test-driver-node: ## Run Node.js driver tests
	node --test drivers/node/nextsql.test.js

test-driver-bun: ## Run Bun driver tests
	@if command -v bun >/dev/null 2>&1; then \
		bun test drivers/bun/nextsql.test.js; \
	else \
		echo "Skipping Bun driver tests: bun not installed"; \
	fi

test-driver-python: ## Run Python driver tests
	python3 -m unittest discover drivers/python/tests

test-driver-php: ## Run PHP driver tests
	php drivers/php/tests/unit.php && php drivers/php/tests/test_pool.php

test-driver-ruby: ## Run Ruby driver tests
	ruby -I drivers/ruby/lib drivers/ruby/test/test_protocol.rb && \
	ruby -I drivers/ruby/lib drivers/ruby/test/test_pool.rb && \
	ruby -I drivers/ruby/lib drivers/ruby/test/test_field_encryption.rb

# -----------------------------------------------------------------------------
# Maintenance & Diagnostics
# -----------------------------------------------------------------------------
clean: ## Clean build binaries, test caches, coverage profiles, and temporary databases
	rm -rf $(BIN_DIR)
	rm -rf nextsql nextsqld nextsql-admin nextsql-bench
	rm -rf *.test *.out coverage.* profile.*
	rm -rf .test-* .bench-* .bench-tmp .slo-* .fixtures .gotmp .go-cache-*
	rm -rf key nextsql.pw *.db *.db-* *.wal *.sock
	@echo "Clean complete."

check-clean: ## Verify git working directory has no uncommitted changes
	@test -z "$$(git status --porcelain)" || (echo "Git repository is dirty:" && git status --porcelain && exit 1)

doctor: ## Audit local development toolchain and driver runtimes
	./scripts/dev-doctor.sh
