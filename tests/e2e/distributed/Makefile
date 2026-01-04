# E2E Test Makefile
# Simplified docker-compose based E2E tests

.PHONY: help
help:
	@echo "E2E Test Commands:"
	@echo "  make up          - Start all services with docker-compose"
	@echo "  make down        - Stop all services"
	@echo "  make build       - Build all container images"
	@echo "  make test        - Run E2E tests (services must be running)"
	@echo "  make test-all    - Build, start services, run tests, then cleanup"
	@echo "  make logs        - Show logs from all services"
	@echo "  make clean       - Stop services and remove volumes"

.PHONY: build
build:
	@echo "Building container images with BuildKit..."
	DOCKER_BUILDKIT=1 docker-compose build

.PHONY: up
up:
	@echo "Starting docker-compose services..."
	docker-compose up -d
	@echo "Waiting for services to be healthy..."
	@sleep 10
	@echo "✓ Services started"
	@echo ""
	@echo "Service endpoints:"
	@echo "  Discovery:  http://localhost:8080"
	@echo "  Colony:     localhost:9000"
	@echo "  Agent-0:    localhost:9001"
	@echo "  Agent-1:    localhost:9002"
	@echo "  CPU App:    http://localhost:8081"
	@echo "  OTEL App:   http://localhost:8082"
	@echo "  SDK App:    http://localhost:3001"

.PHONY: down
down:
	@echo "Stopping docker-compose services..."
	docker-compose down

.PHONY: test
test:
	@echo "Running E2E tests..."
	@echo "(Services must be running - use 'make up' first)"
	go test -v -timeout 30m ./...

.PHONY: test-all
test-all: build up
	@echo "Running full E2E test suite..."
	@$(MAKE) test || ($(MAKE) down && exit 1)
	@$(MAKE) down
	@echo "✓ All tests passed and services cleaned up"

.PHONY: logs
logs:
	docker-compose logs -f

.PHONY: logs-colony
logs-colony:
	docker-compose logs -f colony

.PHONY: logs-agent-0
logs-agent-0:
	docker-compose logs -f agent-0

.PHONY: clean
clean:
	@echo "Stopping services and removing volumes..."
	docker-compose down -v
	@echo "✓ Cleanup complete"

.PHONY: status
status:
	@echo "Service Status:"
	@docker-compose ps
