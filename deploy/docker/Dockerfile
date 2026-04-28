FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /shark-socket ./cmd/shark-socket

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
RUN addgroup -S shark && adduser -S shark -G shark

COPY --from=builder /shark-socket /usr/local/bin/shark-socket

USER shark

EXPOSE 18000/tcp 18200/udp 18400/tcp 18600/tcp 18800/udp 18900/udp 18650/tcp 9091/tcp

HEALTHCHECK --interval=15s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:9091/healthz || exit 1

LABEL org.opencontainers.image.title="shark-socket"
LABEL org.opencontainers.image.description="Multi-protocol network gateway (TCP, UDP, HTTP, WebSocket, CoAP, QUIC, gRPC-Web)"
LABEL org.opencontainers.image.source="https://github.com/X1aSheng/shark-socket"
LABEL org.opencontainers.image.licenses="MIT"

ENTRYPOINT ["shark-socket"]
