# GoLB — Go Load Balancer

A lightweight HTTP load balancer written in Go with round-robin routing, automatic health checking, per-request retry logic, and structured JSON logging.

## Features

- Round-robin load balancing across configurable backends
- Automatic health checks with configurable interval and timeout
- Per-backend retry for idempotent methods (GET, HEAD, OPTIONS)
- Graceful failover — dead backends are skipped automatically
- Stable request ID propagated across backend switches for log correlation
- Structured JSON logging via `log/slog`
- Graceful shutdown on SIGINT/SIGTERM

## Architecture

```
cmd/golb/           Entry point — CLI flags + server wiring
internal/
  backend/          Backend struct with thread-safe alive state
  pool/             ServerPool — atomic round-robin counter
  logger/           Initialises JSON slog default
  health/           Periodic HTTP health checker
  proxy/            LoadBalancer — reverse proxy, retry, failover
```

## Getting Started

### Prerequisites

- Go 1.21+

### Build

```bash
make build          # produces ./golb binary
```

### Run

```bash
./golb --backends=http://localhost:8081,http://localhost:8082 --port=3030
```

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--backends` | (required) | Comma-separated list of backend URLs |
| `--port` | `3030` | Port the load balancer listens on |
| `--health-path` | `/health` | Path used for health probes |
| `--health-interval` | `10s` | Interval between health check rounds |
| `--health-timeout` | `2s` | Per-probe HTTP timeout |

## Local Testing

Start some backend servers and the load balancer in separate terminals:

```bash
# terminal 1 — backend on 8081
python3 -m http.server 8081

# terminal 2 — backend on 8082
python3 -m http.server 8082

# terminal 3 — load balancer
./golb --backends=http://localhost:8081,http://localhost:8082

# terminal 4 — send 6 requests
for i in $(seq 1 6); do curl -s -o /dev/null -w "%{http_code}\n" http://localhost:3030/; done
```

Each backend receives 3 requests (round-robin). The JSON log shows `backend`, `latency_ms`, `status`, and `request_id` for every request.

## Health Checks

GoLB probes each backend at `GET <url><health-path>` every `--health-interval`. A backend is healthy only if the response status is 2xx. State transitions emit structured log lines:

```json
{"level":"WARN","msg":"backend_down","backend":"http://localhost:8082","error":"..."}
{"level":"INFO","msg":"backend_up","backend":"http://localhost:8082"}
```

## Retry and Failover

For idempotent requests (GET, HEAD, OPTIONS), GoLB retries the same backend up to 3 times with a 10 ms delay before marking it dead and switching to the next available backend. Non-idempotent methods (POST, PUT, etc.) switch immediately to avoid consuming the request body twice. After `MaxBackendSwitches` (3) failed attempts across different backends, the client receives `503 Service Unavailable`.

## Development

```bash
make test           # go test ./...
make vet            # go vet ./...
make fmt            # gofmt -w .
make lint           # golangci-lint run ./...
make clean          # remove ./golb binary
```
