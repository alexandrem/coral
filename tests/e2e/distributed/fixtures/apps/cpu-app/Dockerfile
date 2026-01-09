FROM golang:1.25-alpine

WORKDIR /app

# Copy from the correct path (context is project root)
COPY tests/e2e/distributed/fixtures/apps/cpu-app/main.go .

# Build with frame pointers for eBPF profiling on ARM64
# On ARM64, frame pointers must be explicitly enabled using the framepointer build tag
RUN go build -tags=framepointer -o cpu-app main.go

EXPOSE 8080

CMD ["./cpu-app"]
