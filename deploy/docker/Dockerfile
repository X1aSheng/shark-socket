FROM golang:1.26-alpine AS build
WORKDIR /src
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY} \
    GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/shark-socket ./cmd/shark-socket

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget
RUN adduser -u 1000 -D -H shark
USER shark
COPY --from=build /out/shark-socket /usr/local/bin/shark-socket
# Bind to 0.0.0.0 so the EXPOSE'd ports are reachable from outside the
# container (the app's compiled-in defaults listen on 127.0.0.1 only).
ENV SHARK_TCP_ADDR=0.0.0.0:18000 \
    SHARK_METRICS_ADDR=0.0.0.0:18080 \
    SHARK_HEALTH_ADDR=0.0.0.0:18081
EXPOSE 18000 18080 18081
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:18081/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/shark-socket"]
