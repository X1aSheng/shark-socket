# Configuration Guide

Updated: 2026-05-30T08:51:09

## Purpose

`shark-socket-new` can now start from a JSON configuration file and environment
variables. This lets operators change listeners, health checks, metrics, and
enabled protocols without editing Go source code.

## Startup

Default startup remains a TCP echo gateway on `127.0.0.1:18000`:

```powershell
go run ./cmd/shark-socket-new
```

Start with a JSON configuration file:

```powershell
go run ./cmd/shark-socket-new -config .\examples\config\multi-protocol.json
```

The same path can be supplied through the environment:

```powershell
$env:SHARK_CONFIG = '.\examples\config\multi-protocol.json'
go run ./cmd/shark-socket-new
```

## JSON Schema

Top-level fields:

| Field | Type | Purpose |
| --- | --- | --- |
| `shutdown_timeout` | duration string | Graceful shutdown timeout, for example `10s` |
| `health_addr` | string | HTTP health listener address |
| `metrics_addr` | string | Prometheus metrics listener address |
| `protocols` | array | Enabled protocol listeners |

Protocol fields:

| Field | Type | Purpose |
| --- | --- | --- |
| `name` | string | `tcp`, `udp`, `http`, `websocket`, `coap`, or `grpc-web` |
| `enabled` | bool | Optional; defaults to `true` |
| `addr` | string | Listener address |
| `path` | string | WebSocket path or gRPC-Web WebSocket path |
| `mode` | string | CoAP mode; use `lwm2m` for LwM2M command binding |
| `max_message_bytes` | integer | gRPC-Web max request/message size |

Example:

```json
{
  "shutdown_timeout": "10s",
  "health_addr": "127.0.0.1:18081",
  "metrics_addr": "127.0.0.1:18080",
  "protocols": [
    { "name": "tcp", "addr": "127.0.0.1:18000" },
    { "name": "websocket", "addr": "127.0.0.1:18004", "path": "/ws" }
  ]
}
```

## Environment Overrides

Supported overrides:

| Variable | Effect |
| --- | --- |
| `SHARK_CONFIG` | Configuration file path |
| `SHARK_SHUTDOWN_TIMEOUT` | Overrides `shutdown_timeout` |
| `SHARK_HEALTH_ADDR` | Overrides health listener |
| `SHARK_METRICS_ADDR` | Overrides metrics listener |
| `SHARK_TCP_ADDR` | Adds or overrides TCP listener address |
| `SHARK_WS_ADDR` | Adds or overrides WebSocket listener address |
| `SHARK_WS_PATH` | Overrides WebSocket path when `SHARK_WS_ADDR` is set |
| `SHARK_GRPCWEB_ADDR` | Adds or overrides gRPC-Web listener address |
| `SHARK_GRPCWEB_PATH` | Enables gRPC-Web WebSocket mode path when `SHARK_GRPCWEB_ADDR` is set |
| `SHARK_GRPCWEB_MAX_MESSAGE_BYTES` | Overrides gRPC-Web max message size |

`SHARK_GRPCWEB_MAX_MESSAGE_BYTES` must be a valid non-negative integer. Invalid
values fail startup instead of silently falling back.

Container deployments set listener addresses to `0.0.0.0` so services can
receive traffic from outside the container.

Docker builds also support `GOPROXY` through the Dockerfile and compose build
args. The compose default is `https://goproxy.cn,direct`, which keeps cloud
builds usable in network environments where `proxy.golang.org` is slow or
blocked.

## Health And Metrics

Health endpoints:

- `GET /healthz`: process liveness.
- `GET /readyz`: Gateway readiness; returns `503` until protocols are started.

Metrics:

- `GET /metrics`: Prometheus text format on `metrics_addr`.

## Current Limits

- QUIC is not yet exposed through config because it requires TLS material.
- TLS/mTLS configuration is planned as a security-baseline milestone.
- File format is JSON only; YAML can be added later if operators need it.
