FROM golang:1.25-alpine

WORKDIR /app

# Copy from the correct path (context is project root)
COPY tests/e2e/cpu-profile/cpu-intensive-app/main.go .

RUN go build -o cpu-app main.go

EXPOSE 8080

CMD ["./cpu-app"]
