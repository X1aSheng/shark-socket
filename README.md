# shark-socket-new

`shark-socket-new` is a redesigned architecture spike for Shark-Socket.

The goal is not to copy the old project file-by-file. It keeps the useful
ideas, then makes the runtime contracts explicit:

- Gateway owns global runtime composition.
- Transports receive runtime dependencies instead of closing shared resources.
- Global plugins are applied through one plugin runner.
- Graceful shutdown is staged through optional transport capabilities.
- Typed messages are layered through codecs, while the transport core stays raw.

## Current Vertical Slice

- `api`: public facade.
- `internal/core`: stable contracts.
- `internal/runtime`: Gateway, plugin chain, session manager.
- `internal/transport/tcp`: TCP transport with length-prefixed framing.
- `cmd/shark-socket-new`: echo server example.

## Run

```bash
go run ./cmd/shark-socket-new
```

The example listens on `127.0.0.1:18000` and echoes length-prefixed TCP frames.

## Design Status

This is a compileable architecture baseline. More protocols should be added by
implementing `core.Server`, and optionally `core.RuntimeConfigurable` and
`core.StagedServer`.
