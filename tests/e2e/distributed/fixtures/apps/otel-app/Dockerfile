FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy app-specific go mod files.
COPY tests/e2e/distributed/fixtures/apps/otel-app/go.mod ./
COPY tests/e2e/distributed/fixtures/apps/otel-app/go.sum ./
RUN go mod download

# Copy source code.
COPY tests/e2e/distributed/fixtures/apps/otel-app/main.go ./

# Build with frame pointers for eBPF instrumentation.
# Disabling inlining and optimizations (-N -l) ensures stable function
# boundaries for uprobes and reliable CLIENT span capture.
RUN go build -tags=framepointer -gcflags="all=-N -l" -o otel-app main.go

# Final stage.
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder.
COPY --from=builder /app/otel-app .

# Expose port.
EXPOSE 8080

# Run the application.
CMD ["./otel-app"]
