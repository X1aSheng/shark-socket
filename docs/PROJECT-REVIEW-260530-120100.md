# Project Review 2026-05-30 12:01

## Scope

- Ran the new resource-limited benchmark matrix on cloud host `120.76.44.233`.
- Verified cloud smoke, light, and selected medium stages with Linux resource
  gates.
- Recorded cloud benchmark results in
  `docs/BENCHMARK-RESULT-260530-120000.md`.

## Validation

Cloud commands:

```bash
go run scripts/run_benchmarks.go -profile cloud -stage smoke -logdir logs/cloud-bench
go run scripts/run_benchmarks.go -profile cloud -stage light -logdir logs/cloud-bench
go run scripts/run_benchmarks.go -profile cloud -stage medium -logdir logs/cloud-bench
```

Results:

- Smoke passed.
- Light passed.
- Selected medium passed.
- Resource gates remained healthy: MemAvailable stayed around 1177-1212 MB and
  load1 stayed around 0.23-0.50.

## Notes

- The test used a temporary archive upload at
  `/tmp/shark-socket-new-bench-d64a9db`; existing remote project directories
  were not modified.
- `47.96.129.59` was not used because SSH host key verification failed locally.
