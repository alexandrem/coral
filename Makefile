.PHONY: build clean init install install-tools test run help

# Build variables
BINARY_NAME=coral
BUILD_DIR=bin
VERSION?=dev
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GO_VERSION=$(shell go version | awk '{print $$3}')

# Linker flags to set version info
LDFLAGS=-ldflags "\
	-X github.com/coral-io/coral/pkg/version.Version=$(VERSION) \
	-X github.com/coral-io/coral/pkg/version.GitCommit=$(GIT_COMMIT) \
	-X github.com/coral-io/coral/pkg/version.BuildDate=$(BUILD_DATE)"

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the coral binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/coral
	@echo "✓ Built $(BUILD_DIR)/$(BINARY_NAME)"

clean: ## Remove build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@echo "✓ Cleaned"

install: build ## Install the binary to $GOPATH/bin
	@echo "Installing..."
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(shell go env GOPATH)/bin/
	@echo "✓ Installed to $(shell go env GOPATH)/bin/$(BINARY_NAME)"

test: ## Run tests
	@echo "Running tests..."
	go test ./...

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
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
	go install github.com/bufbuild/buf/cmd/buf@latest

lint: ## Run linter
	@echo "Running linter..."
	golangci-lint run

all: clean build test ## Clean, build, and test
