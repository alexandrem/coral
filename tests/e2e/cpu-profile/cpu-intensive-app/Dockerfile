FROM golang:1.25-alpine

WORKDIR /app

COPY main.go .

RUN go build -o cpu-app main.go

CMD ["./cpu-app"]
