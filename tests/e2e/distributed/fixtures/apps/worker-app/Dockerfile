FROM golang:1.25-alpine

WORKDIR /app

# Copy from the correct path (context is project root)
COPY tests/e2e/distributed/fixtures/apps/worker-app/main.go .

RUN go build -o worker-app main.go

# No EXPOSE: worker-app is a client-only process with no listening socket
# (RFD 102 test fixture for the client-only discovery path).
CMD ["./worker-app"]
