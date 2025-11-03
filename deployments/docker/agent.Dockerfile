# Coral Agent Docker Image
FROM golang:1.22-alpine AS builder

WORKDIR /build

# Install build dependencies.
RUN apk add --no-cache git make

# Copy go mod files.
COPY go.mod go.sum ./
RUN go mod download

# Copy source code.
COPY . .

# Build the binary.
RUN make build

# Final stage - minimal runtime image.
FROM alpine:latest

# Install runtime dependencies.
RUN apk add --no-cache ca-certificates

# Create coral user.
RUN addgroup -g 1000 coral && \
    adduser -D -u 1000 -G coral coral

# Create necessary directories.
RUN mkdir -p /var/lib/coral /var/log/coral && \
    chown -R coral:coral /var/lib/coral /var/log/coral

# Copy binary from builder.
COPY --from=builder /build/bin/coral /usr/local/bin/coral

# Switch to coral user.
USER coral

# Set working directory.
WORKDIR /home/coral

# Health check.
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD coral agent status || exit 1

# Default entrypoint.
ENTRYPOINT ["/usr/local/bin/coral"]

# Default command.
CMD ["agent", "start"]
