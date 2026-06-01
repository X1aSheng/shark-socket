# Examples

## Multi-Protocol Runtime

Run the configurable multi-protocol gateway:

```powershell
go run ./cmd/shark-socket -config .\examples\config\multi-protocol.json
```

Run the multi-protocol example:

```powershell
go run ./examples/multi-protocol
```

It starts:

- TCP echo on `127.0.0.1:18000`.
- WebSocket echo on `ws://127.0.0.1:18001/ws`.
- CoAP/LwM2M command responder on `127.0.0.1:18002`.
- Prometheus metrics on `http://127.0.0.1:18080/metrics`.
- OpenTelemetry tracing adapter through the Gateway tracer interface.

The example keeps transport sessions raw and lets handlers decide how to encode
messages. That matches the runtime design: Gateway owns lifecycle and shared
dependencies, transports own protocol IO, and codecs/adapters sit above raw
session payloads.

## LwM2M CoAP Commands

The CoAP responder accepts text payloads in this shape:

```text
register <endpoint> <lifetime-seconds> [object-path...]
update <endpoint> <lifetime-seconds>
deregister <endpoint>
write <endpoint> <resource-path> <value>
read <endpoint> <resource-path>
```

Example payloads:

```text
register device-1 60 /3/0
write device-1 /3/0/0 ACME
read device-1 /3/0/0
deregister device-1
```

## Metrics

Mount Prometheus metrics with the public facade:

```go
metrics := api.NewPrometheusMetrics()
gateway := api.NewGateway(api.WithMetrics(metrics))
http.ListenAndServe("127.0.0.1:18080", metrics)
```

## Tracing

Adapt an OpenTelemetry tracer without leaking vendor types into `internal/core`:

```go
gateway := api.NewGateway(
    api.WithTracer(api.NewOpenTelemetryTracer(otel.Tracer("shark-socket"))),
)
```
