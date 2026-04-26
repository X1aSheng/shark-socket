FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /shark-socket ./examples/multi_protocol

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=builder /shark-socket /usr/local/bin/shark-socket

EXPOSE 18000 18200/udp 18400 18600 18800/udp 9090

ENTRYPOINT ["shark-socket"]
