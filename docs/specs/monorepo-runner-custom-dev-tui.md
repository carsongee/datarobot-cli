# Monorepo-Runner: Custom Dev TUI — Technical Design

**Technical Design**
Carson Gee
May 13, 2026

> This document outlines the transition from a brittle, non-interactive Python-based process runner to a high-performance, interactive **Go-based Terminal User Interface (TUI)** integrated directly into our existing CLI. By leveraging the **Bubble Tea** ([Charm.sh](https://charm.sh)) ecosystem, we aim to provide a "K9s-style" cockpit for monorepo development, offering real-time visibility into process health, resource consumption, and lifecycle control. **Recommendation:** Build a custom TUI using Go. This leverages existing team expertise, minimizes external dependencies, and provides a superior Developer Experience (DX) tailored to our specific workflow.

PBMPs: [[PBMP-7668] Local Experimentation Experience](https://linear.app/datarobot/issue/PBMP-7668) — Adds another server to run that can fail that will put pressure on this. Also, we need to have a nice UX for the developer to find that interface (https://github.com/datarobot/dr-experimentation-cli).

## Background

The `datarobot-agent-application` monorepo currently uses a custom Python-based process runner invoked via `task dev`. It reads service definitions from `.taskfiledata.yaml` and starts 4 services with port readiness checks: `agent` (8842), `mcp_server` (9000), `fastapi_server` (8080), and `frontend_web` (5173).

`drdev` provides basic per-process log output, but lacks several features that development teams have come to expect from modern process management tools:

- **No color coding per process** — all log lines are the same color, making it hard to visually distinguish which service is logging what at a glance.
- **No resource monitoring** — no CPU or memory usage visible per process; developers can't tell if a service is leaking memory without opening a separate tool.
- **No per-service restart** — to restart a single crashed or hung service, you must kill the entire `task dev` session and restart everything.
- **No per-service status** — you can't tell if one of the services failed to start.
- **No log toggling** — no way to mute a noisy service or focus only on one service's output; it's a continuous wall of interleaved text.
- **No log search** — no way to search or filter historical log output without piping to a separate terminal.

In short, `drdev` is a slightly underfeatured version of `docker compose up` — it runs the services and tails their output, but offers no interactivity or observability beyond that.

## Overview

Current development requires orchestrating 2–5 server processes. Our existing Python script is a "fire-and-forget" tool that offers no feedback loop. If a process hangs but doesn't exit, the script incorrectly reports it as "Running." We need a tool that understands the **intent** and **readiness** of these processes.

## Rationale

- **For Developers:** Eliminates "Context Switching Fatigue." Instead of hunting through multiple terminal tabs to find a crashed service, the TUI provides a single pane of glass. Automated **readiness checks** remove the guesswork of knowing when a service is actually ready to receive traffic. Also, adds central "dashboard" to send folks off to the Local Experimentation service.
- **For Product/Management:** Improved DX translates to **Engineering Velocity.** Onboarding a new engineer becomes a 30-second command (`my-cli dev`) rather than a complex troubleshooting session.
- **For Customers/Stability:** Better local observability allows developers to catch resource leaks (high CPU/RAM) and race conditions locally before they ever reach a staging environment. Reduces boilerplate code and complex taskfile confusion.
- **For Builder Builders (Platform Engineers):** Provides a way to add visibility into new features, run more servers. Enables simpler dynamic component support (bespoke af-component discovery to add additional servers to start, check for dependencies, etc).

## Requirements

1. **Centralized Control:** Start/Stop/Restart all or individual processes via hotkeys (e.g., `r` for restart).
2. **Readiness Probes:** Visual status indicators (Starting, Healthy, Crashed) based on actual TCP/HTTP polling.
3. **Resource Monitoring:** Real-time CPU and Memory usage per PID displayed in the TUI.
4. **Interactive Log Management:** Ability to cycle through logs, toggle visibility, and use color-coded prefixes for different servers.
5. **Go Integration:** Must be compiled into our existing Go-based CLI for zero-dependency distribution.
6. **Dynamic Pre-flight Configuration:** Before starting processes, resolve runtime configuration from external sources (e.g. Pulumi stack outputs, authenticated user context) and inject the result as environment variables. Example: query Pulumi to get the user's OTel endpoint and entity ID, then start all services with `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_ENTITY_ID` correctly set — without requiring the developer to populate these manually or adding boilerplate resolution scripts to the repo.

## Out of scope

- Production process management (this is not a replacement for Systemd or Kubernetes).
- CI/CD pipeline orchestration.
- Container management (focused on bare binaries and scripts).

## Proposed solution

We will implement a custom TUI using the [Charm.sh](https://charm.sh) framework, known for its high-quality Go terminal components with the CLI as a subcommand. i.e., `datarobot templates dev` or similar (naming to be discussed if this is the approach).

### High-level Architecture

1. **The Supervisor:** A Go package wrapping `os/exec`. It manages process lifecycles, captures `stdout/stderr`, and ensures no "zombie" processes remain after exit.
2. **The Health Poller:** Background goroutines that perform periodic probes (TCP Dialing or HTTP GET) to confirm service readiness.
3. **The UI (Bubble Tea):** A state machine that renders the process list, resource metrics, and log viewport.

### Tech Stack

- **UI Framework:** [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- **Metrics:** [gopsutil](https://github.com/shirou/gopsutil) for fetching process-specific CPU/Mem.
- **Styling:** [Lip Gloss](https://github.com/charmbracelet/lipgloss)

## Analysis of Alternatives

In evaluating alternatives, we considered five approaches. A standard `Procfile` was created for the agent application and each Procfile-based tool was run against it. For pm2, an `ecosystem.config.cjs` was used instead:

```
1  agent:         cd agent && uv run python dev.py --autoreload
2  mcp_server:    cd mcp_server && uv run app/main.py
3  fastapi_server: cd fastapi_server &&
                  TEST_USER_EMAIL=developer@example.com uv run uvicorn app.main:app --
                  host 0.0.0.0 --port 8080 --reload
4  frontend_web:  cd frontend_web && npm run dev
```

**Hivemind** — color-coded per service, PTY-aware, no interactivity.

**Goreman** — color-coded per service with timestamps, otherwise identical UX to Hivemind.

**Overmind** — same visual output as Hivemind/Goreman, but with tmux backing and control commands.

**Foreman** — identical output to Goreman/Hivemind; requires Ruby runtime.

**pm2** — Node.js process manager with a real monitoring TUI (`pm2 monit`) showing per-process CPU and memory.

### Evaluation Criteria

- Interactivity (can you restart a single service without restarting all?)
- Readiness awareness (does it know the difference between "started" and "healthy"?)
- Resource visibility (does it show CPU/memory per process?)
- Pre-flight configuration (can it resolve external state before starting processes?)
- Zero external dependencies (can it be compiled into our CLI?)

| Option | Pros | Cons |
|--------|------|------|
| **Foreman / Hivemind (Procfile)** | Color-coded output per service; clean prefix+indent formatting; Hivemind uses PTY so dev servers behave as if in a real terminal (ANSI colors and spinners work correctly); zero learning curve. Automatically loads `.env` from the working directory. | No interactivity — no per-service restart, no pause, no kill; no liveness/readiness probes; no log search or filtering; no resource monitoring. **No pre-flight configuration** — cannot resolve runtime values (Pulumi outputs, OTel entity IDs) before starting processes; workarounds require wrapper scripts that add boilerplate to the repo. Foreman additionally requires a full Ruby runtime. |
| **Prox / Goreman** | Written in Go; lightweight; fits existing Procfile format; color-coded per service with timestamps. Automatically loads `.env`. | UX is functionally identical to Hivemind — same wall-of-text output with no interactivity, no liveness probes, no search, no resource monitoring. **No pre-flight configuration.** Zero DX improvement over the current Python script. |
| **Overmind** | Most capable of the Procfile runners: auto-restarts failed processes; supports starting subsets (`overmind start -x agent,mcp_server`); injects `PORT` into child processes; backed by tmux so you can attach to any individual process window. Automatically loads `.env`. | **Does not support Windows** — tmux is unavailable on Windows, disqualifying Overmind for any team with Windows developers. Interactivity is split across two interfaces — the log tail view has no controls, and process management requires separate `overmind` commands in another shell. No liveness probes or resource monitoring. No pre-flight configuration. The tmux-attach workflow fragments the DX rather than unifying it. |
| **pm2** | Most fully-featured evaluated alternative: per-service restart/stop/start, and a monitoring TUI (`pm2 monit`) showing real-time CPU and memory per process — the only evaluated tool to provide this natively. Node.js is already required as a project dependency. | **Does not load `.env` automatically** — unlike all Procfile runners, pm2 requires env vars to be explicitly declared in `ecosystem.config.cjs`; a custom workaround is required for any project using `.env` files. **No pre-flight configuration** — dynamic env resolution (Pulumi, auth) still requires external scripts. The `pm2 monit` TUI is underdeveloped: logs are only buffered from when the TUI is opened (no history), all services share a single log panel with no per-service isolation, and there is no log search. Color coding in the log view is inconsistent. No service URL or port display. Daemon model requires explicit `pm2 delete all` to clean up after exit. **Runtime dependency risk:** Node.js is currently required for the React/Vite frontend, but as the project moves toward fewer components and potentially drops TypeScript/Node entirely, pm2's runtime dependency could become a liability. |
| **Custom Go TUI (Recommended)** | **Total Control.** Built-in health checks, K9s-style UX, zero extra dependencies, ships inside our existing CLI binary. **Native pre-flight configuration** — as a first-class Go program with access to the CLI's existing Pulumi and auth integrations, it can resolve stack outputs and user context at startup and inject them into every process's environment, with no boilerplate added to the repo. | Requires internal development time (~1.5 weeks). |

## Recommended option

The decision reduces to a single question: **simple or full-featured?**

**Simple path — Goreman (Procfile runner):** If the goal is to match the current `drdev` experience with color-coded output and nothing more, switching to a Procfile and Goreman is the lowest-cost option. Goreman is a zero-dependency Go binary, supports Windows, is already used in this project's ecosystem, and requires no new development time. Foreman is ruled out by its Ruby runtime requirement; Overmind is ruled out by its Windows incompatibility and tmux dependency.

**Complex path — Custom Go TUI (Recommended):** If we want a full-featured developer cockpit — readiness probes, per-service restart, resource monitoring, log filtering — pm2 is the closest off-the-shelf option but falls short on multiple dimensions: it does not load `.env` automatically, its monitoring TUI is underdeveloped (no log history, no search, split UX), and its Node.js runtime dependency is a liability as the project potentially moves away from TypeScript. Critically, **no evaluated alternative supports pre-flight configuration** — the ability to query Pulumi stack outputs or authenticated user context before processes start and inject the results as environment variables. Every off-the-shelf tool requires a wrapper script in the repo to do this, which is exactly the kind of boilerplate we want to eliminate. The custom Go TUI runs inside our existing CLI, which already has Pulumi and auth integrations, making dynamic configuration a natural first-class feature rather than an afterthought. We pay a ~1.5 week development cost to get a tool that improves daily velocity indefinitely with zero external dependencies and zero repo boilerplate.

## Risk Management

| Risk | Level | Mitigation |
|------|-------|------------|
| **Zombie Processes** | Medium | Implement robust signal handling (`SIGINT`/`SIGTERM`) and context cancellation to ensure all child PIDs are reaped. |
| **Log Buffer Bloat** | Low | Use a circular buffer for log storage to prevent the CLI from consuming excessive RAM during long sessions. |
| **TUI Complexity** | Low | Use established TUI libraries (Bubble Tea) to handle the UI state machine boilerplate. |
| **Pre-flight Configuration Failure** | Low | If Pulumi or auth queries fail at startup, surface a clear error with actionable guidance (e.g. "run `dr auth login` first") rather than silently starting processes with missing env vars. Provide a `--skip-preflight` flag to bypass for offline use. |

## QA Plan

Manual smoke test: run `my-cli dev` against the agent application, verify all 4 services reach Healthy status, confirm CPU/RAM metrics update, verify per-service restart hotkey, confirm clean exit (no zombie processes via `pgrep`). Verify OTel env vars are correctly injected from Pulumi at startup.

## Delivery Milestones

| # | Milestone | Effort estimate (person weeks) | Time estimate (calendar weeks) |
|---|-----------|-------------------------------|-------------------------------|
| 1 | **Core Supervisor:** Start/Stop/Restart logic via Go. | 0.5 | 1 |
| 2 | **TUI MVP:** Process list, readiness icons, and basic log tailing. | 1.0 | 1 |
| 3 | **Observability:** TCP/HTTP health probes + CPU/RAM metrics integration. | 0.5 | 0.5 |
| 4 | **Polishing:** Keybindings for cycling, filtering, and "mute" log features. | 0.5 | 0.5 |

## Impact Analysis

| Area | Scope | Expected impacts |
|------|-------|-----------------|
| UI | Terminal | Replaces messy interleaved stdout with a structured, K9s-like dashboard. |
| API / SDK | None | No impact. |
| Architecture (services, integrations) | Local Dev | Decouples process running from brittle Python scripts. |
| Tech stack (languages, frameworks, 3rd party technologies) | Go | Adds `bubbletea`, `lipgloss`, and `gopsutil` as dependencies to the CLI binary. |
| Persistent Critical Services (databases, middleware) | None | No impact. |
| Product Security (Auth, Networking, Container security, Secrets/Credentials) | None | No impact. |
| Installation (Terraform, Helm, Container Images) | None | No impact; purely a development tool. |
| Configuration (Release toggles, feature entitlements, Helm chart values) | None | No impact. |
| Observability | Local Dev | **High Gain:** Real-time visibility into process health and resource leaks during development. Pre-flight configuration ensures OTel traces are correctly routed without manual env var setup. |
| CI/CD (pipelines, development infrastructure) | Minimal | No impact on production pipelines; purely a local development infrastructure tool. |
| Infrastructure (MTSaaS, STS, Onprem) | None | No impact. |
| Legal | None | All dependencies (Charm.sh, gopsutil) are MIT/BSD licensed. |
| Finance | None | No impact. |

## Feedback and Approvals

| Reviewer | Status | Feedback |
|----------|--------|----------|
| @AJ Alon | Review requested | |
| @Anatolii Stehnii | Reviewed | IMO before a new runner we need a contract of *how* do we want to run a multicomponent application: there should be a sort of `docker-compose.yaml`-kind of a manifest to wire up components together. New runner should be based on this contract. |
| @Matthew Nitzken | Reviewed | Overall I think this is a really good idea, and if its just cleanly part of the `drcli` that is the best option. I think the custom option makes sense because we're not trying to just generally run "processes" in the background, we're running datarobot components. Few comments, but overall this solution seems very solid and I don't have any big concerns. The `drcli` has evolved really well, so I feel confident this can be good since we now have a lot of experience on that front. |
| @nagajyothi.nookula | Review requested | |
| @Nate Daly | Review requested | |
| @Damon Stanley | Review requested | This is definitely a pain point. I've found myself occasionally running `task X:dev` in different terminal tabs to separate out logstreams. I definitely like consolidating into dr cli over drdev's extra thing to download. I do think there's some risk of this (a) being a bit more of a side quest than we want and (b) I think there's a chance it's worth getting into the docker compose world and leveraging tooling there (my comment on Dozzle). I think this is mitigated by the fact that we can be pretty incremental here. |
| @Andrii Kislitsyn | Review requested | |
