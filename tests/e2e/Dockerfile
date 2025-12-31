# E2E Test Environment Dockerfile
# Provides a complete environment for running E2E tests with all dependencies.
FROM golang:1.25-bookworm

WORKDIR /workspace

# Install build dependencies (reused from main Dockerfile).
# Note: docker.io provides Docker CLI client for testcontainers.
# Testcontainers uses the host Docker daemon via mounted socket (not DinD).
RUN apt-get update && apt-get install -y --no-install-recommends \
    clang \
    g++ \
    gcc \
    git \
    llvm \
    make \
    docker.io \
    && rm -rf /var/lib/apt/lists/*

# Copy Makefile and install development tools.
COPY Makefile .
RUN make init

# Ensure Go bin directory is in PATH.
ENV PATH="${PATH}:/go/bin"

# Copy all source code (workspace will handle module resolution).
# Note: .dockerignore excludes go.work to prevent workspace issues,
# so we'll copy module files explicitly and let Go handle dependencies.
COPY go.mod go.sum ./
COPY . .

# Set working directory.
WORKDIR /workspace

# Default command runs E2E tests.
CMD ["bash", "-c", "make generate && cd tests/e2e/distributed && go test -v -timeout=30m ./..."]
