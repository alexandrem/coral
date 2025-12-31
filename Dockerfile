# Coral Agent Docker Image
FROM golang:1.25-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

# Install build dependencies including C compiler for CGO (required by go-duckdb).
RUN apt-get update && apt-get install -y --no-install-recommends \
    clang \
    g++ \
    gcc \
    git \
    llvm \
    make \
    && rm -rf /var/lib/apt/lists/*

# Init project tooling.
COPY Makefile .
RUN make init

# Ensure Go bin directory is in PATH.
ENV PATH="${PATH}:/go/bin"

# Copy go mod files (skip go.work - not needed for container builds).
COPY go.mod go.sum ./
RUN go mod download

# Copy source code.
COPY . .

# Build the binary for the target platform.
# Override BUILD_DIR to use a consistent path instead of platform-specific subdirectories.
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} make build BUILD_DIR=/build/bin

# Final stage - minimal runtime image.
FROM debian:bookworm-slim

# Install runtime dependencies including WireGuard and networking tools.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    wireguard-tools \
    iptables \
    iproute2 \
    bash \
    curl \
    tcpdump \
    dnsutils \
    netcat-openbsd \
    net-tools \
    procps \
    vim-tiny \
    wget \
    unzip \
    && rm -rf /var/lib/apt/lists/*

# Create coral user.
RUN groupadd -g 1000 coral && \
    useradd -m -u 1000 -g coral coral

# Create necessary directories.
RUN mkdir -p /var/lib/coral /var/log/coral && \
    chown -R coral:coral /var/lib/coral /var/log/coral

# Copy binary from builder (built for target platform via TARGETOS/TARGETARCH).
COPY --from=builder /build/bin/coral /usr/local/bin/coral

# Run as root for TUN device creation (required for WireGuard mesh).
USER root

# Set working directory.
# Note: Running as root is required for TUN device creation.
# In production, consider using capabilities and user namespaces for better security.
WORKDIR /root

# Health check.
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD coral agent status || exit 1

# Default entrypoint.
ENTRYPOINT ["/usr/local/bin/coral"]

# Default command.
CMD ["agent", "start"]
