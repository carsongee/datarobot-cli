# `dr up` - Local development service runner

Start and supervise local development services with a real-time TUI dashboard.

> [!NOTE]
> `dr up` is a preview feature. Enable it with the environment variable:
> ```bash
> export DATAROBOT_CLI_FEATURE_UP=true
> ```

## Quick start

```bash
# Start all services defined in dr-up.yaml
dr up

# Use a custom config file
dr up --config path/to/myconfig.yaml

# Skip pre-flight environment resolution (offline mode)
dr up --skip-preflight
```

## Synopsis

```bash
dr up [flags]
```

## Description

`dr up` reads a YAML config file (default: `dr-up.yaml`) that lists the processes to run, then starts them all and presents an interactive TUI. Each service gets its own color-coded log stream. The dashboard shows real-time CPU and memory usage, readiness status, and a scrollable log panel for the selected service.

Before starting processes, a **pre-flight** step may query external sources (such as Pulumi stack outputs or your authenticated user context) and inject the resolved values as environment variables into every service. This lets services start with the correct configuration without requiring you to populate `.env` files manually. Use `--skip-preflight` to bypass this step for offline or CI use.

### TUI layout

```
┌─ Service list ──────────────────────────────┐
│ ▶ api         Healthy   PID 12345  1.2% 128MiB │
│   frontend    Starting  PID 12346  0.0%   0MiB │
│   worker      Crashed                          │
└─────────────────────────────────────────────┘
┌─ Logs: api ─────────────────────────────────┐
│ INFO  Uvicorn running on http://0.0.0.0:8080  │
│ INFO  Application startup complete.           │
└─────────────────────────────────────────────┘
 j/k navigate · r restart · s stop · m mute · / filter · G bottom · o open · q quit
```

### Service states

| State        | Meaning                                          |
|--------------|--------------------------------------------------|
| Starting     | Process is running; readiness probe pending.     |
| Healthy      | Readiness probe confirmed the service is ready.  |
| Crashed      | Process exited unexpectedly.                     |
| Stopped      | Service was intentionally stopped (`s`).         |
| Restarting   | Service is being restarted (`r`).                |

Services with `probe: none` (or no probe configured) move directly to Healthy after the process starts.

### Keybindings

| Key            | Action                                                      |
|----------------|-------------------------------------------------------------|
| `j` / `k` / `↑` `↓` | Navigate the service list                          |
| `r`            | Restart the selected service                                |
| `s`            | Stop the selected service                                   |
| `m`            | Mute / unmute logs for the selected service                 |
| `/`            | Open log filter input (Enter to apply, Esc to clear)        |
| `G`            | Scroll logs to bottom (re-enable auto-scroll)               |
| `o`            | Open service URL in the browser (HTTP probe URL or `localhost:<port>`) |
| `q` / `Esc`   | Quit and stop all services                                  |

## Options

```
  -c, --config string    Path to service config file (default: dr-up.yaml)
      --skip-preflight   Skip pre-flight configuration steps (for offline use)
  -h, --help             Show help information
```

### Global options

All [global flags](README.md#global-flags) are also available.

## Configuration file

Services are defined in a YAML file (`dr-up.yaml` by default).

### Full schema

```yaml
services:
  - name: <string>          # Required. Unique name for the service.
    command: <string>       # Required. Shell command to run.
    dir: <string>           # Working directory (relative to the config file, or absolute).
    env:                    # Extra environment variables for this service.
      KEY: value
    port: <int>             # Port number (1–65535). Required for probe: tcp.
    probe: tcp|http|none    # Readiness probe type. Defaults to none.
    url: <string>           # HTTP probe URL. Required for probe: http when port is not set.
```

### Probe types

| Probe  | Behavior                                                                                                     |
|--------|--------------------------------------------------------------------------------------------------------------|
| `tcp`  | Dials `127.0.0.1:<port>` every 500 ms. Marks the service Healthy on the first successful connection.        |
| `http` | Makes a GET to `url` (or `http://localhost:<port>` when only `port` is set) every 500 ms. Marks Healthy when the response status is < 500. |
| `none` | No probe — the service is marked Healthy immediately after the process starts.                               |

### Validation rules

- At least one service must be defined.
- Service names must be unique.
- `command` is required for every service.
- `probe: tcp` requires a valid `port` (1–65535).
- `probe: http` requires either a valid `port` or an explicit `url` (must be an absolute `http://` or `https://` URL).

### Examples

**Minimal — two services with no probes:**

```yaml
services:
  - name: api
    command: uv run uvicorn app.main:app --reload
  - name: frontend
    command: npm run dev
```

**With TCP readiness probes:**

```yaml
services:
  - name: api
    command: uv run uvicorn app.main:app --reload
    dir: backend
    port: 8080
    probe: tcp
  - name: frontend
    command: npm run dev
    dir: frontend
    port: 5173
    probe: tcp
```

**With an HTTP probe and extra environment variables:**

```yaml
services:
  - name: api
    command: uv run uvicorn app.main:app --reload
    dir: backend
    port: 8080
    probe: http
    env:
      LOG_LEVEL: debug
      DATABASE_URL: postgresql://localhost/dev
  - name: worker
    command: uv run celery -A app worker -l info
    dir: backend
    probe: none
```

**HTTP probe with an explicit URL:**

```yaml
services:
  - name: api
    command: uv run uvicorn app.main:app --reload
    dir: backend
    probe: http
    url: http://localhost:8080/health
```

## Pre-flight configuration

Before starting services, `dr up` runs a pre-flight step that resolves runtime configuration from external sources and injects the result as environment variables into every service process. This allows services to start with the correct configuration automatically — for example, resolving your OTel endpoint from a Pulumi stack output rather than requiring manual `.env` population.

To skip this step (for offline development or CI), use `--skip-preflight`:

```bash
dr up --skip-preflight
```

## Error handling

### Config file not found

```text
Config file "dr-up.yaml" not found. Create it or use --config to specify a path.
```

Create a `dr-up.yaml` in your project root, or point to an existing file with `--config`.

### No services defined

```text
loading config: invalid config "dr-up.yaml": config must define at least one service
```

Add at least one entry under `services:` in your config file.

### Pre-flight failure

```text
pre-flight: <error details>
```

Check that you are authenticated (`dr auth login`) or run with `--skip-preflight` to bypass.

## See also

- [`dr start`](start.md) — run the template quickstart process.
- [`dr run`](run.md) — execute individual application tasks.
- [`dr auth`](auth.md) — authenticate with DataRobot (required for pre-flight).
