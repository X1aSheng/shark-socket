# Project Review 2026-05-30 12:36

## Scope

- Re-ran dual-cloud validation using both cloud servers.
- Used `47.96.129.59` as the server/build/benchmark node.
- Used `120.76.44.233` as the client/light benchmark node.
- Captured benchmark statistics, test logs, cross-host protocol results, and
  remote log locations.

## Validation

Server2 `47.96.129.59`:

```bash
go test ./... -count=1
go vet ./...
go run scripts/run_tests.go -mode all -timeout 5m
go run scripts/run_benchmarks.go -profile cloud -stage medium -logdir logs/dual-server-bench
go build -o /tmp/shark-socket-new-dual-server-a3d283f/shark-socket-new ./cmd/shark-socket-new
```

Server1 `120.76.44.233`:

```bash
go test ./... -count=1
go run scripts/run_benchmarks.go -profile cloud -stage medium -logdir logs/dual-client-bench
```

Cross-host checks from server1 to server2 passed for:

- Health/readiness.
- TCP length-prefix echo.
- UDP datagram echo.
- HTTP POST echo.
- WebSocket binary echo.
- CoAP/LwM2M register/write/read/deregister lifecycle.
- gRPC-Web direct echo.

## Results

- Server2 full tests passed.
- Server2 vet passed.
- Server2 scripted validation passed: 113 unit tests, 6 integration tests, and
  benchmark report passed.
- Server2 resource-gated medium benchmark passed.
- Server1 full tests passed.
- Server1 resource-gated medium benchmark passed.
- The server2 temporary binary process was stopped after validation.

## Logs

Remote logs remain available at:

- `/tmp/shark-socket-new-dual-server-a3d283f/logs`
- `/tmp/shark-socket-new-dual-client-a3d283f/logs`

Detailed benchmark data and log sizes are recorded in
`docs/BENCHMARK-RESULT-260530-123500-DUAL-CLOUD.md`.
