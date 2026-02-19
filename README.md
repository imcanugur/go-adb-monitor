# go-adb-monitor

A production-grade Go application that monitors Android devices via ADB (Android Debug Bridge) using **streaming** — not polling.

## Architecture

```
cmd/adb-monitor/         CLI entry point
internal/
  adb/                   ADB wire protocol client (TCP to adb server)
  tracker/               Device tracker via track-devices streaming protocol
  event/                 Pub/sub event bus (decouples producers from consumers)
  monitor/               Orchestrator + per-device property collectors
  logging/               Structured logging (slog)
```

### Key Design Decisions

| Decision | Rationale |
|---|---|
| **Streaming via `track-devices-l`** | Push-based, zero-latency detection vs polling. ADB server pushes state on change. |
| **Event bus** | Decouples tracker from monitor, enables multiple consumers without tight coupling. |
| **Per-device goroutines** | Each online device gets its own monitor goroutine with independent lifecycle. |
| **Context-based cancellation** | Clean shutdown propagation from signal → orchestrator → per-device goroutines. |
| **Exponential backoff reconnect** | Resilient to ADB server restarts. |
| **No global state** | All dependencies injected via constructors. |

### Data Flow

```
ADB Server ──TCP stream──▶ Tracker ──events──▶ Event Bus ──▶ Monitor Orchestrator
                                                    │                │
                                                    │          starts/stops
                                                    │                │
                                                    ▼                ▼
                                              stdout printer   DeviceMonitor (per device)
                                                                     │
                                                               ADB shell commands
                                                               (getprop, dumpsys)
                                                                     │
                                                                     ▼
                                                               Event Bus (properties)
```

## Usage

```bash
# Build
go build -o adb-monitor ./cmd/adb-monitor

# Run (ADB server must be running)
./adb-monitor

# With options
./adb-monitor \
  -adb-addr 127.0.0.1:5037 \
  -log-level debug \
  -log-format json \
  -prop-interval 15s \
  -json-events
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `-adb-addr` | `127.0.0.1:5037` | ADB server address |
| `-log-level` | `info` | Log level: debug, info, warn, error |
| `-log-format` | `text` | Log format: text, json |
| `-prop-interval` | `30s` | How often to collect device properties |
| `-json-events` | `false` | Emit events as JSON to stdout |

## Prerequisites

- Go 1.22+
- ADB installed and server running (`adb start-server`)
- At least one Android device connected or emulator running

## Events

| Event | Trigger |
|---|---|
| `device_connected` | New device appears in ADB |
| `device_disconnected` | Device disappears from ADB |
| `device_state_changed` | Device transitions states (e.g. unauthorized → device) |
| `device_properties` | Periodic property snapshot (battery, model, OS version, etc.) |

## Collected Properties

- `ro.product.model`, `ro.product.manufacturer`
- `ro.build.version.release`, `ro.build.version.sdk`
- Battery level, status, temperature, power source
- Hardware, timezone, build ID
