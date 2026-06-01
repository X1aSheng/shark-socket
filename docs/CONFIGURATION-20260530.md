# Configuration Guide

Updated: 2026-05-30T11:20:00

## Purpose

`shark-socket` can now start from a JSON configuration file and environment
variables. This lets operators change listeners, health checks, metrics, and
enabled protocols without editing Go source code.

## Startup

Default startup remains a TCP echo gateway on `127.0.0.1:18000`:

```powershell
go run ./cmd/shark-socket
```

Start with a JSON configuration file:

```powershell
go run ./cmd/shark-socket -config .\examples\config\multi-protocol.json
```

The same path can be supplied through the environment:

```powershell
$env:SHARK_CONFIG = '.\examples\config\multi-protocol.json'
go run ./cmd/shark-socket
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
| `name` | string | `tcp`, `udp`, `http`, `websocket`, `coap`, `quic`, or `grpc-web` |
| `enabled` | bool | Optional; defaults to `true` |
| `addr` | string | Listener address |
| `path` | string | WebSocket path or gRPC-Web WebSocket path |
| `mode` | string | CoAP mode; use `lwm2m` for LwM2M command binding |
| `max_message_bytes` | integer | gRPC-Web max request/message size |
| `tls_cert_file` | string | TCP TLS or QUIC server certificate PEM path |
| `tls_key_file` | string | TCP TLS or QUIC server private key PEM path |
| `tls_client_ca_file` | string | TCP or QUIC client CA bundle PEM path for mTLS |
| `tls_client_auth` | string | TCP or QUIC client certificate policy |
| `allowed_origins` | string array | WebSocket or gRPC-Web allowed Origin values; `*` allows all |

Example:

```json
{
  "shutdown_timeout": "10s",
  "health_addr": "127.0.0.1:18081",
  "metrics_addr": "127.0.0.1:18080",
  "protocols": [
    { "name": "tcp", "addr": "127.0.0.1:18000" },
    {
      "name": "websocket",
      "addr": "127.0.0.1:18004",
      "path": "/ws",
      "allowed_origins": ["https://console.example"]
    }
  ]
}
```

TCP can enable TLS by supplying certificate material. QUIC always requires it:

```json
{
  "protocols": [
    {
      "name": "tcp",
      "addr": "127.0.0.1:18000",
      "tls_cert_file": "certs/server.crt",
      "tls_key_file": "certs/server.key"
    },
    {
      "name": "quic",
      "addr": "127.0.0.1:18007",
      "tls_cert_file": "certs/server.crt",
      "tls_key_file": "certs/server.key"
    }
  ]
}
```

TCP and QUIC can require client certificates with `tls_client_auth` and
`tls_client_ca_file`:

```json
{
  "protocols": [
    {
      "name": "tcp",
      "addr": "127.0.0.1:18000",
      "tls_cert_file": "certs/server.crt",
      "tls_key_file": "certs/server.key",
      "tls_client_ca_file": "certs/client-ca.crt",
      "tls_client_auth": "require_and_verify"
    }
  ]
}
```

Supported `tls_client_auth` values:

| Value | Go TLS behavior |
| --- | --- |
| `none` | Do not request a client certificate |
| `request` | Request a client certificate without requiring one |
| `require_any` | Require any client certificate |
| `verify_if_given` | Verify a client certificate when supplied |
| `require_and_verify` | Require and verify a client certificate against `tls_client_ca_file` |

## Environment Overrides

Supported overrides:

| Variable | Effect |
| --- | --- |
| `SHARK_CONFIG` | Configuration file path |
| `SHARK_SHUTDOWN_TIMEOUT` | Overrides `shutdown_timeout` |
| `SHARK_HEALTH_ADDR` | Overrides health listener |
| `SHARK_METRICS_ADDR` | Overrides metrics listener |
| `SHARK_HTTP_ADDR` | Adds or overrides HTTP listener address |
| `SHARK_HTTP_ALLOWED_ORIGINS` | Comma-separated HTTP CORS allowed origins |
| `SHARK_TCP_ADDR` | Adds or overrides TCP listener address |
| `SHARK_TCP_CERT_FILE` | Enables/overrides TCP TLS server certificate path |
| `SHARK_TCP_KEY_FILE` | Enables/overrides TCP TLS server private key path |
| `SHARK_TCP_CLIENT_CA_FILE` | Enables/overrides TCP mTLS client CA bundle path |
| `SHARK_TCP_CLIENT_AUTH` | Overrides TCP client certificate policy |
| `SHARK_WS_ADDR` | Adds or overrides WebSocket listener address |
| `SHARK_WS_PATH` | Overrides WebSocket path when `SHARK_WS_ADDR` is set |
| `SHARK_WS_ALLOWED_ORIGINS` | Comma-separated WebSocket allowed origins |
| `SHARK_QUIC_ADDR` | Adds or overrides QUIC listener address |
| `SHARK_QUIC_CERT_FILE` | Overrides QUIC server certificate path |
| `SHARK_QUIC_KEY_FILE` | Overrides QUIC server private key path |
| `SHARK_QUIC_CLIENT_CA_FILE` | Overrides QUIC mTLS client CA bundle path |
| `SHARK_QUIC_CLIENT_AUTH` | Overrides QUIC client certificate policy |
| `SHARK_GRPCWEB_ADDR` | Adds or overrides gRPC-Web listener address |
| `SHARK_GRPCWEB_PATH` | Enables gRPC-Web WebSocket mode path when `SHARK_GRPCWEB_ADDR` is set |
| `SHARK_GRPCWEB_ALLOWED_ORIGINS` | Comma-separated gRPC-Web WebSocket allowed origins |
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

- TCP TLS, QUIC TLS, and client certificate verification are configurable.
- Certificate reload and finer per-protocol TLS defaults remain part of the
  security-baseline milestone.
- HTTP CORS and WebSocket/gRPC-Web Origin allowlists are configurable.
- File format is JSON only; YAML can be added later if operators need it.
