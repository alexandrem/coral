# Build stage
FROM golang:1.25-bookworm AS builder

WORKDIR /workspace

# Copy the entire repository to support local replace directives
COPY . .

WORKDIR /workspace/examples/sdk-go

# Build with debug symbols (recommended for Coral SDK)
RUN go build -gcflags="all=-N -l" -o sdk-go main.go

# Runtime stage
FROM debian:bookworm-slim

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

# Copy binary from builder
COPY --from=builder /workspace/examples/sdk-go/sdk-go .

# Expose application port and SDK debug port
EXPOSE 3001 9092

# Run the application
CMD ["./sdk-go"]
