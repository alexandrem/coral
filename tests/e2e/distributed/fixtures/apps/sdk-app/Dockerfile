FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy app-specific go mod file (no go.sum needed for local-only deps).
COPY tests/e2e/distributed/fixtures/apps/sdk-app/go.mod ./

# Copy main module dependencies (needed for replace directive).
COPY go.mod go.sum ./coral-root/
COPY pkg/ ./coral-root/pkg/

# Adjust replace directive to point to copied location.
RUN go mod edit -replace=github.com/coral-mesh/coral=./coral-root

RUN go mod download

# Copy app source code.
COPY tests/e2e/distributed/fixtures/apps/sdk-app/main.go ./

# Build the application.
RUN CGO_ENABLED=0 GOOS=linux go build -o sdk-app main.go

# Final stage.
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder.
COPY --from=builder /app/sdk-app .

# Expose ports (HTTP server + SDK debug port).
EXPOSE 3001 9002

# Run the application.
CMD ["./sdk-app"]
