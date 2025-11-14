# Coral Agent Docker Image
FROM golang:1.25-bookworm AS builder

WORKDIR /build

# Install build dependencies including C compiler for CGO (required by go-duckdb).
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    make \
    gcc \
    g++ \
    && rm -rf /var/lib/apt/lists/*

# Copy go mod files.
COPY go.mod go.sum ./
RUN go mod download

# Copy source code.
COPY . .

# Build the binary.
RUN make build

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
    procps \
    vim-tiny \
    wget \
    unzip \
    && rm -rf /var/lib/apt/lists/*

# Install DuckDB CLI for shell debugging (RFD 026).
RUN wget -q https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip \
    && unzip duckdb_cli-linux-amd64.zip \
    && mv duckdb /usr/local/bin/duckdb \
    && chmod +x /usr/local/bin/duckdb \
    && rm duckdb_cli-linux-amd64.zip

# Create coral user.
RUN groupadd -g 1000 coral && \
    useradd -m -u 1000 -g coral coral

# Create necessary directories.
RUN mkdir -p /var/lib/coral /var/log/coral && \
    chown -R coral:coral /var/lib/coral /var/log/coral

# Copy binary from builder.
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
