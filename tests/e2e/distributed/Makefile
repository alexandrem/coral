# E2E Test Makefile
# Uses base docker-compose.yml from project root with e2e overlay

.PHONY: help
help:
	@echo "E2E Test Commands:"
	@echo "  make up                        - Start all services with docker-compose"
	@echo "  make down                      - Stop all services"
	@echo "  make build                     - Build all container images"
	@echo "  make test                      - Run all E2E tests (services must be running)"
	@echo "  make test-filter FILTER=<name> - Run specific test group"
	@echo "  make test-all                  - Build, start services, run tests, then cleanup"
	@echo "  make logs                      - Show logs from all services"
	@echo "  make clean                     - Stop services and remove volumes"
	@echo ""
	@echo "Local Discovery (uses local discovery service instead of public):"
	@echo "  make local-build               - Build all images (incl. discovery)"
	@echo "  make local-up                  - Start services with local discovery"
	@echo "  make local-down                - Stop services with local discovery"
	@echo "  make local-logs                - Show logs from all services"
	@echo "  make local-clean               - Stop services and remove volumes"
	@echo ""
	@echo "Examples:"
	@echo "  make test-filter FILTER=Test4_OnDemandProbes"
	@echo "  make test-filter FILTER=Test4_OnDemandProbes/OnDemandProfiling"

# Compose file layering: base + e2e overlay
# --project-directory ensures build contexts resolve relative to the project root.
PROJECT_ROOT=../../..
COMPOSE_E2E=--project-directory $(PROJECT_ROOT) -f $(PROJECT_ROOT)/docker-compose.yml -f docker-compose.e2e.yml
COMPOSE_E2E_LOCAL_DISCOVERY=$(COMPOSE_E2E) -f docker-compose.e2e-local-discovery.yml

.PHONY: build
build:
	@echo "Building container images with BuildKit..."
	DOCKER_BUILDKIT=1 docker-compose $(COMPOSE_E2E) build

.PHONY: up
up:
	@echo "Starting docker-compose services..."
	docker-compose $(COMPOSE_E2E) up -d
	@echo "Waiting for services to be healthy..."
	@echo "✓ Services started"
	@echo ""
	@echo "Service endpoints:"
	@echo "  Colony:     localhost:9000 / https://localhost:8443"
	@echo "  Agent-0:    localhost:9001"
	@echo "  Agent-1:    localhost:9002"
	@echo "  CPU App:    http://localhost:8081"
	@echo "  OTEL App:   http://localhost:8082"
	@echo "  SDK App:    http://localhost:3001"

.PHONY: down
down:
	@echo "Stopping docker-compose services..."
	docker-compose $(COMPOSE_E2E) down

.PHONY: test
test:
	@echo "Running E2E tests..."
	@echo "(Services must be running - use 'make up' first)"
	go test -c -o e2e.test .
	./e2e.test -test.v -test.timeout 30m
	@rm e2e.test

.PHONY: test-filter
test-filter:
	@if [ -z "$(FILTER)" ]; then \
		echo "Usage: make test-filter FILTER=<test-name>"; \
		echo ""; \
		echo "Examples:"; \
		echo "  make test-filter FILTER=Test4_OnDemandProbes"; \
		echo "  make test-filter FILTER=Test4_OnDemandProbes/OnDemandProfiling"; \
		echo "  make test-filter FILTER=Test1_MeshConnectivity"; \
		echo ""; \
		echo "Available test groups:"; \
		echo "  Test1_MeshConnectivity"; \
		echo "  Test2_ServiceManagement"; \
		echo "  Test3_PassiveObservability"; \
		echo "  Test4_OnDemandProbes"; \
		echo "  Test5_CLICommands"; \
		exit 1; \
	fi
	@echo "Running E2E tests matching: $(FILTER)..."
	@echo "(Services must be running - use 'make up' first)"
	go test -v -timeout 30m -run "TestE2EOrchestrator/$(FILTER)"

.PHONY: test-all
test-all: build up
	@echo "Running full E2E test suite..."
	@$(MAKE) test || ($(MAKE) down && exit 1)
	@$(MAKE) down
	@echo "✓ All tests passed and services cleaned up"

.PHONY: logs
logs:
	docker-compose $(COMPOSE_E2E) logs -f

.PHONY: logs-colony
logs-colony:
	docker-compose $(COMPOSE_E2E) logs -f colony

.PHONY: logs-agent-0
logs-agent-0:
	docker-compose $(COMPOSE_E2E) logs -f agent-0

.PHONY: clean
clean:
	@echo "Stopping services and removing volumes..."
	docker-compose $(COMPOSE_E2E) down -v
	@echo "✓ Cleanup complete"

.PHONY: status
status:
	@echo "Service Status:"
	@docker-compose $(COMPOSE_E2E) ps

# --- Local Discovery targets ---

.PHONY: local-build
local-build:
	@echo "Building container images with BuildKit (local discovery)..."
	DOCKER_BUILDKIT=1 docker-compose $(COMPOSE_E2E_LOCAL_DISCOVERY) build

.PHONY: local-up
local-up:
	@echo "Starting docker-compose services with local discovery..."
	docker-compose $(COMPOSE_E2E_LOCAL_DISCOVERY) up -d
	@echo "✓ Services started (local discovery at localhost:18080)"

.PHONY: local-down
local-down:
	@echo "Stopping docker-compose services (local discovery)..."
	docker-compose $(COMPOSE_E2E_LOCAL_DISCOVERY) down

.PHONY: local-logs
local-logs:
	docker-compose $(COMPOSE_E2E_LOCAL_DISCOVERY) logs -f

.PHONY: local-clean
local-clean:
	@echo "Stopping services and removing volumes (local discovery)..."
	docker-compose $(COMPOSE_E2E_LOCAL_DISCOVERY) down -v
	@echo "✓ Cleanup complete"
