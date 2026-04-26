FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /shark-socket ./examples/multi_protocol

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=builder /shark-socket /usr/local/bin/shark-socket

EXPOSE 8080 8081 8082 8083 5683/udp 9090

ENTRYPOINT ["shark-socket"]
