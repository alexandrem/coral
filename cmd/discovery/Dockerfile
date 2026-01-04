# Discovery Service Docker Image
FROM golang:1.25-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

# Copy go mod files.
COPY go.mod go.sum ./

# Download dependencies with cache mount for faster builds.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# Copy source code.
COPY . .

# Build the discovery binary for the target platform.
# No CGO needed (no DuckDB dependency).
# Use cache mounts to speed up builds.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    CGO_ENABLED=0 \
    go build -ldflags="-w -s" \
    -o /build/coral-discovery \
    ./cmd/discovery

# Final stage - minimal runtime image.
FROM alpine:latest

# Install runtime dependencies.
RUN apk --no-cache add ca-certificates

# Create discovery user.
RUN addgroup -g 1000 discovery && \
    adduser -D -u 1000 -G discovery discovery

WORKDIR /app

# Copy binary from builder.
COPY --from=builder /build/coral-discovery /usr/local/bin/coral-discovery

# Run as non-root user.
USER discovery

# Expose HTTP port.
EXPOSE 8080

# Health check.
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run discovery service.
ENTRYPOINT ["/usr/local/bin/coral-discovery"]
