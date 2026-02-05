FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy app-specific go mod file (no go.sum needed for local-only deps).
COPY tests/e2e/distributed/fixtures/apps/memory-app/go.mod ./

# Copy main module dependencies (needed for replace directive).
COPY go.mod go.sum ./coral-root/
COPY pkg/ ./coral-root/pkg/

# Adjust replace directive to point to copied location.
RUN go mod edit -replace=github.com/coral-mesh/coral=./coral-root

RUN go mod download

# Copy app source code.
COPY tests/e2e/distributed/fixtures/apps/memory-app/main.go ./

# Build the application with frame pointers for eBPF profiling (ARM64 support).
RUN CGO_ENABLED=0 GOOS=linux go build -tags=framepointer -o memory-app main.go

# Final stage.
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder.
COPY --from=builder /app/memory-app .

# Expose ports (HTTP server + SDK debug port).
EXPOSE 8080 9004

# Run the application.
CMD ["./memory-app"]
