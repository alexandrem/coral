FROM golang:1.25-alpine

WORKDIR /app

# Copy from the correct path (context is project root)
COPY tests/e2e/distributed/fixtures/apps/netns-app/main.go .

# Build with frame pointers for eBPF instrumentation on ARM64 (see cpu-app).
RUN go build -tags=framepointer -o netns-app main.go

EXPOSE 8080

CMD ["./netns-app"]
