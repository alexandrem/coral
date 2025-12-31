.PHONY: build build-dev clean init install install-tools test run help generate proto docker-build docker-buildx

# Build variables
BINARY_NAME=coral
BUILD_DIR=bin/$(shell go env GOOS)_$(shell go env GOARCH)
VERSION?=dev
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GO_VERSION=$(shell go version | awk '{print $$3}')

# CGO is necessary for duckdb.
CGO_ENABLED=1
export CGO_ENABLED

# Linker flags to set version info
LDFLAGS=-ldflags "\
	-X github.com/coral-mesh/coral/pkg/version.Version=$(VERSION) \
	-X github.com/coral-mesh/coral/pkg/version.GitCommit=$(GIT_COMMIT) \
	-X github.com/coral-mesh/coral/pkg/version.BuildDate=$(BUILD_DATE)"

# Docker variables
DOCKER_IMAGE?=coral
DOCKER_TAG?=$(VERSION)

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

generate: proto ## Generate eBPF, download Beyla and Deno binaries (run before first build)
	@echo "Running go generate..."
	@# Check for llvm-strip (required for bpf2go)
	@if ! which llvm-strip >/dev/null 2>&1; then \
		if [ -d "/usr/local/homebrew/opt/llvm/bin" ]; then \
			export PATH="/usr/local/homebrew/opt/llvm/bin:$$PATH"; \
		elif [ -d "/opt/homebrew/opt/llvm/bin" ]; then \
			export PATH="/opt/homebrew/opt/llvm/bin:$$PATH"; \
		fi; \
	fi; \
	if ! which llvm-strip >/dev/null 2>&1; then \
		echo "❌ Error: llvm-strip not found in PATH"; \
		echo "  macOS: brew install llvm"; \
		echo "  Linux: sudo apt-get install clang llvm"; \
		exit 1; \
	fi; \
	env -u GOOS -u GOARCH go generate ./...
	@echo "✓ Generated files ready"

proto: ## Generate protobuf files using buf
	@if which buf >/dev/null 2>&1; then \
		echo "Running buf generate..."; \
		buf generate; \
		echo "✓ Protobuf files generated"; \
	else \
		echo "⚠️  buf not found, skipping protobuf generation"; \
	fi
	@$(MAKE) -s fmt

build: generate ## Build the coral binary
	@echo "Building for $(GOOS)/$(GOARCH) → $(BUILD_DIR)"
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/coral
	@echo "✓ Built $(BUILD_DIR)/$(BINARY_NAME)"
	@echo "Building coral-discovery..."
	go build $(LDFLAGS) -o $(BUILD_DIR)/coral-discovery ./cmd/discovery
	@echo "✓ Built $(BUILD_DIR)/coral-discovery"
	@# Copy Deno binary for current platform
	@DENO_SRC="internal/cli/run/binaries/deno-$(shell go env GOOS)-$(shell go env GOARCH)"; \
	DENO_DST="$(BUILD_DIR)/deno-$(shell go env GOOS)-$(shell go env GOARCH)"; \
	if [ -f "$$DENO_SRC" ]; then \
		cp "$$DENO_SRC" "$$DENO_DST"; \
		chmod +x "$$DENO_DST"; \
		echo "✓ Copied Deno binary to $(BUILD_DIR)"; \
	else \
		echo "⚠️  Deno binary not found at $$DENO_SRC"; \
		echo "   Run 'make generate' to download Deno binaries"; \
	fi

build-dev: build ## Build and grant TUN creation privileges (Linux: capabilities, macOS: setuid)
	@echo "Granting TUN device creation privileges..."
	@if [ "$$(uname)" = "Linux" ]; then \
		echo "  Linux detected: applying CAP_NET_ADMIN capability"; \
		sudo setcap cap_net_admin+ep $(BUILD_DIR)/$(BINARY_NAME) 2>/dev/null || \
			(echo "  ⚠️  setcap failed. Install libcap2-bin or use sudo to run coral."; exit 0); \
		echo "  ✓ Capabilities applied to $(BUILD_DIR)/$(BINARY_NAME)"; \
	elif [ "$$(uname)" = "Darwin" ]; then \
		echo "  macOS detected: applying setuid (requires password)"; \
		sudo chown root:wheel $(BUILD_DIR)/$(BINARY_NAME) && \
		sudo chmod u+s $(BUILD_DIR)/$(BINARY_NAME); \
		echo "  ✓ Setuid applied to $(BUILD_DIR)/$(BINARY_NAME)"; \
		echo "  ⚠️  Note: setuid grants elevated privileges to all users"; \
	else \
		echo "  ⚠️  Unknown platform. You may need to run with sudo."; \
	fi
	@echo "✓ Development build complete. Run without sudo: ./$(BUILD_DIR)/$(BINARY_NAME) colony start"

clean: ## Remove build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@echo "✓ Cleaned"

install: build ## Install the binary to $GOPATH/bin
	@echo "Installing..."
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(shell go env GOPATH)/bin/
	@echo "✓ Installed to $(shell go env GOPATH)/bin/$(BINARY_NAME)"

test: generate ## Run tests
	@echo "Running tests..."
	go test ./...

test-ci: generate ## Run tests in CI
	@echo "Running tests..."
	go test -short -count=1 -parallel=8 -coverprofile=coverage.out -covermode=atomic ./... -timeout=10m

test-linux: ## Run tests in Linux Docker (tests platform-specific code)
	@echo "Running tests in Linux Docker..."
	@docker run --rm \
		-v "$(PWD)":/workspace \
		-w /workspace \
		golang:1.25 \
		bash -c "apt-get update -qq && apt-get install -y -qq clang llvm > /dev/null 2>&1 && go test -short ./..."

ci-check: ## Run full CI checks locally (lint + test on Linux)
	@echo "=== Running full CI checks locally ==="
	@echo "1. Linting on Linux..."
	@$(MAKE) -s lint-linux
	@echo "✓ Lint passed"
	@echo ""
	@echo "2. Testing on Linux..."
	@$(MAKE) -s test-linux
	@echo "✓ Tests passed"
	@echo ""
	@echo "✓ All CI checks passed!"

run: build ## Build and run the CLI
	@$(BUILD_DIR)/$(BINARY_NAME)

fmt: ## Format Go code
	@echo "Formatting code..."
	goimports -w .

vet: ## Run go vet
	@echo "Running go vet..."
	go vet ./...

mod-tidy: ## Tidy Go modules
	@echo "Tidying modules..."
	go mod tidy

init: ## Initialize development
	$(MAKE) -s install-tools

install-tools: ## Install development tools
	@echo "Installing development dependencies..."
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
	go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.19.1
	go install github.com/bufbuild/buf/cmd/buf@v1.61.0
	go install golang.org/x/tools/cmd/goimports@latest

lint: ## Run linter
	@echo "Running linter..."
	golangci-lint run --config .golangci.yml ./...

lint-linux: ## Run linter in Linux Docker (tests platform-specific code)
	@echo "Running linter in Linux Docker..."
	@docker run --rm \
		-v "$(PWD)":/workspace \
		-w /workspace \
		golangci/golangci-lint:latest \
		golangci-lint run --config .golangci.yml ./...

docker-build: ## Build Docker image for current platform
	@echo "Building Docker image $(DOCKER_IMAGE):$(DOCKER_TAG)..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	@echo "✓ Built $(DOCKER_IMAGE):$(DOCKER_TAG)"

docker-buildx: ## Build multi-platform Docker image (requires buildx)
	@echo "Building multi-platform Docker image $(DOCKER_IMAGE):$(DOCKER_TAG)..."
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) \
		--load \
		.
	@echo "✓ Built $(DOCKER_IMAGE):$(DOCKER_TAG) for linux/amd64,linux/arm64"

all: clean build test ## Clean, build, and test

