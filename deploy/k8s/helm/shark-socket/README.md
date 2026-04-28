# shark-socket Helm Chart

Multi-protocol network gateway supporting TCP, UDP, HTTP, WebSocket, CoAP, QUIC, and gRPC-Web.

## Installation

```bash
helm install shark-socket . --namespace shark-socket --create-namespace
```

## Configuration

See `values.yaml` for all configurable parameters.

Key parameters:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `2` |
| `image.repository` | Image repository | `x1asheng/shark-socket` |
| `image.tag` | Image tag | `latest` |
| `protocols.tcp.enabled` | Enable TCP server | `true` |
| `protocols.udp.enabled` | Enable UDP server | `true` |
| `protocols.http.enabled` | Enable HTTP server | `true` |
| `protocols.websocket.enabled` | Enable WebSocket server | `true` |
| `protocols.coap.enabled` | Enable CoAP server | `true` |
| `protocols.quic.enabled` | Enable QUIC server | `false` |
| `protocols.grpcweb.enabled` | Enable gRPC-Web server | `false` |
| `metrics.enabled` | Enable Prometheus metrics | `true` |
| `autoscaling.enabled` | Enable HPA | `true` |
| `ingress.enabled` | Enable Ingress | `false` |

## Production deployment

```bash
helm install shark-socket . \
  --namespace shark-socket --create-namespace \
  --values values.yaml \
  --values values-prod.yaml
```
