.PHONY: build build-dev clean init install install-tools test run help generate proto docker-build docker-buildx test-e2e test-e2e-up test-e2e-down dev-up dev-up-local dev-down dev-logs dev-status dev-env

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

# Docker compose file layering for dev stack
COMPOSE_DEV=-f docker-compose.yml -f docker-compose.dev.yml
COMPOSE_DEV_LOCAL=$(COMPOSE_DEV) -f docker-compose.dev-local-discovery.yml

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

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
	@# Create symlinks in bin/ for e2e tests (only when using default platform-specific BUILD_DIR)
	@# Skip if BUILD_DIR is overridden (e.g., in Docker builds)
	@if [ "$(BUILD_DIR)" = "bin/$(shell go env GOOS)_$(shell go env GOARCH)" ]; then \
		rm -f bin/$(BINARY_NAME) bin/coral-discovery; \
		ln -s $(shell go env GOOS)_$(shell go env GOARCH)/$(BINARY_NAME) bin/$(BINARY_NAME); \
		ln -s $(shell go env GOOS)_$(shell go env GOARCH)/coral-discovery bin/coral-discovery; \
		echo "✓ Created symlinks: bin/$(BINARY_NAME) → $(BUILD_DIR)/$(BINARY_NAME)"; \
	else \
		echo "✓ Skipping symlink creation (BUILD_DIR override detected)"; \
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
	@rm -f bin/$(BINARY_NAME) bin/coral-discovery
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

test-e2e: generate ## Run E2E distributed tests (requires Docker + BuildKit)
	@echo "Running E2E distributed tests with docker-compose..."
	@# Check if docker is available
	@if ! command -v docker >/dev/null 2>&1; then \
		echo "❌ Docker not found. Please install Docker or Colima"; \
		exit 1; \
	fi
	@# Check if docker is running
	@if ! docker info >/dev/null 2>&1; then \
		echo "❌ Docker is not running"; \
		echo "  macOS: Start Colima with 'colima start'"; \
		echo "  Linux: Start Docker daemon"; \
		exit 1; \
	fi
	@# Verify we're running in a Linux environment (via docker)
	@if [ "$$(docker version -f '{{.Server.Os}}')" != "linux" ]; then \
		echo "❌ Docker must be running on Linux"; \
		echo "  Current: $$(docker version -f '{{.Server.Os}}')"; \
		exit 1; \
	fi
	@echo "✓ Docker running on Linux (host: $$(uname -s))"
	@echo "Starting E2E test suite (containers will run on Linux)..."
	@cd tests/e2e/distributed && $(MAKE) test-all

test-e2e-filter: generate ## Run specific E2E test group (use FILTER=<test-name>)
	@if [ -z "$(FILTER)" ]; then \
		echo "Usage: make test-e2e-filter FILTER=<test-name>"; \
		echo ""; \
		echo "Examples:"; \
		echo "  make test-e2e-filter FILTER=Test4_OnDemandProbes"; \
		echo "  make test-e2e-filter FILTER=Test4_OnDemandProbes/OnDemandProfiling"; \
		echo ""; \
		echo "This requires services to be running:"; \
		echo "  1. Start services: make test-e2e-up"; \
		echo "  2. Run test: make test-e2e-filter FILTER=Test4_OnDemandProbes"; \
		echo "  3. Stop services: make test-e2e-down"; \
		exit 1; \
	fi
	@cd tests/e2e/distributed && $(MAKE) test-filter FILTER=$(FILTER)

test-e2e-up: ## Start E2E test services (docker-compose)
	@echo "Starting E2E test services..."
	@cd tests/e2e/distributed && $(MAKE) up

test-e2e-down: ## Stop E2E test services (docker-compose)
	@echo "Stopping E2E test services..."
	@cd tests/e2e/distributed && $(MAKE) down

test-e2e-logs: ## View E2E test service logs
	@cd tests/e2e/distributed && $(MAKE) logs

test-e2e-docker: ## [DEPRECATED] Use test-e2e instead
	@echo "⚠️  test-e2e-docker is deprecated"
	@echo "  Use 'make test-e2e' for docker-compose based tests"
	@echo "  Or use 'make test-e2e-up && make test-e2e-down' for manual control"
	@exit 1

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
	@if which goimports >/dev/null 2>&1; then \
		echo "Formatting code..."; \
		goimports -w .; \
		echo "✓ Code formatted"; \
	else \
		echo "⚠️  goimports not found, skipping formatting"; \
	fi

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
	@echo "✓ Development tools installed to $(shell go env GOPATH)/bin"

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

# =============================================================================
# Local Development Stack
# =============================================================================

# Helper to check Docker status
define check_docker
    @if ! command -v docker >/dev/null 2>&1; then \
       echo "❌ Docker not found. Please install Docker or Colima"; exit 1; \
    fi
    @if ! docker info >/dev/null 2>&1; then \
       echo "❌ Docker is not running"; exit 1; \
    fi
endef

# Helper to wait for Colony and print connection info
define wait_and_setup
    @echo "Waiting for colony to initialize..."
    @for i in 1 2 3 4 5 6 7 8 9 10; do \
       if docker exec coral-colony-1 cat /shared/colony_id >/dev/null 2>&1; then break; fi; \
       sleep 2; \
    done
    @$(MAKE) -s dev-env
endef

# Consolidated Docker Up logic
# Usage: $(call docker_up, $(COMPOSE_VAR), "Description")
define docker_up
    $(check_docker)
    @echo "Starting dev stack with $2..."
    docker-compose $1 up -d
    @echo ""
    @echo "✓ Dev stack running"
    $(wait_and_setup)
endef

dev-up: ## Start dev stack (colony + agents + test apps, public discovery)
	$(call docker_up,$(COMPOSE_DEV),"public discovery")
	@echo "  Colony:  localhost:9000 / https://localhost:8443"
	@echo "  Agent-0: localhost:9001 | CPU App: localhost:8081 | OTEL App: localhost:8082"
	@echo "  Agent-1: localhost:9002 | SDK App: localhost:3001"
	@echo ""
	@echo "Tip: Run 'make dev-logs' to tail logs"

dev-up-local: ## Start dev stack with local discovery service
	$(call docker_up,$(COMPOSE_DEV_LOCAL),"local discovery")
	@echo "  Discovery: http://localhost:18080"
	@echo "  Colony:    localhost:9000 / https://localhost:8443"
	@echo "  Agent-0:   localhost:9001"
	@echo "  Agent-1:   localhost:9002"
	@echo ""
	@echo "Tip: Run 'make dev-logs' to tail logs"

dev-down: ## Stop dev stack
	@echo "Stopping dev stack..."
	docker-compose $(COMPOSE_DEV) down -v 2>/dev/null || true
	docker-compose $(COMPOSE_DEV_LOCAL) down -v 2>/dev/null || true
	@echo "✓ Dev stack stopped"

dev-logs: ## Tail dev stack logs
	docker-compose $(COMPOSE_DEV) logs -f

dev-status: ## Show dev stack status
	@echo "Dev Stack Status:"
	@docker-compose $(COMPOSE_DEV) ps 2>/dev/null || docker-compose $(COMPOSE_DEV_LOCAL) ps 2>/dev/null || echo "No dev stack running"

dev-env: ## Print env vars and commands to connect CLI to dev stack
	@if ! docker exec coral-colony-1 cat /shared/colony_id >/dev/null 2>&1; then \
       echo "⚠️  Colony not ready yet or stack not running."; \
       exit 0; \
    fi
	@COLONY_ID=$$(docker exec coral-colony-1 cat /shared/colony_id); \
	echo "Creating admin API token..."; \
	TOKEN=$$(docker exec coral-colony-1 coral colony token create dev-admin --permissions admin --recreate 2>/dev/null | grep "^Token: " | sed 's/Token: //'); \
	if [ -n "$$TOKEN" ]; then \
	   echo ""; \
	   echo "To connect CLI to dev stack:"; \
	   echo "  export CORAL_COLONY_ENDPOINT=https://localhost:8443"; \
	   echo "  export CORAL_API_TOKEN=$$TOKEN"; \
	   echo "  export CORAL_INSECURE=true"; \
	   echo ""; \
	   echo "To add remote and fetch CA:"; \
	   echo "  coral colony add-remote dev https://localhost:8443 --insecure"; \
	   echo ""; \
	   echo "Colony ID: $$COLONY_ID"; \
	else \
	   echo "⚠️  Could not create API token (colony may still be starting)"; \
	fi
