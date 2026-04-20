# Agent Status Library — Design

> **Status:** Locked-in design for v0.1.0. Naming TBD (working name: `agent-status-lib`).
> **Audience:** Library maintainers and contributors.
> **Positioning:** Open-source Go library, maintained independently of cortex. Cortex is a consumer, not a design driver.

---

## Purpose

A Go library that tells you what your coding agents (Claude Code, Codex, OpenCode, and anything else someone writes an adapter for) are doing in real time — `working`, `idle`, `awaiting_input`, `error`, `ended` — via their native hook mechanisms.

Consumers get a single unified event stream across all agents, composable with filter/map/debounce/fanout primitives, with pluggable sinks for delivery (webhook, Slack, file, custom).

## Non-goals

- Not a daemon. No config file, no DSL, no runtime service. Pure library.
- Not a process supervisor. Consumer owns agent lifecycle.
- Not a cross-machine system. Hub is in-process.
- Not a durable event store. In-memory only; consumers add durability via sinks.
- Not Vector. No transform DSL, no DAG config, no hot reload.
- No pane scraping / tmux dependency in v0.1.0. Hook-first only.
  (May be added as an optional source later if hook gaps prove painful.)
- No Windows support in v0.1.0 (untested; macOS + Linux only).

---

## Architecture

### Three layers

```
┌─────────────────────────────────────────────────┐
│ Adapters (Claude / Codex / OpenCode / custom)   │  per-agent data + install + parse
└─────────────────────────────────────────────────┘
                       ▲
┌─────────────────────────────────────────────────┐
│ Decision (per-session state machine)            │  pure: Signal → Status transitions
└─────────────────────────────────────────────────┘
                       ▲
┌─────────────────────────────────────────────────┐
│ Hub (multi-session coordinator + pipeline)      │  unified event stream, combinators, sinks
└─────────────────────────────────────────────────┘
```

### What the library does NOT own

- Network transport. `Hub.Ingest(agent, payload)` is the primitive. `Hub.ServeHTTP(addr)` is a thin convenience wrapper. Consumers can mount the hub on their own router.
- Process lifecycle. If consumers need "ended" on process death, they supply a `LivenessCheck` callback.
- Ticket / session metadata. Consumers register arbitrary tags via `Hub.RegisterSession(sessionID, tags)` and/or enrich via `stream.Map(...)`.

### Hook bridges: no binary shipped

The library ships no compiled binary. `InstallHooks` generates and writes:

- **Claude** → inline `curl` invocation as the `command` field in `~/.claude/settings.json` hook entries.
- **Codex** → a ~5-line shell script at `~/.agentstatus/codex-bridge.sh` (stable library-owned path), referenced from `~/.codex/config.toml` `notify`.
- **OpenCode** → a generated TypeScript plugin file at `~/.config/opencode/plugins/agentstatus.ts`.

Requires `curl` and `sh` on the host. Universally present on macOS + Linux.

`UninstallHooks` removes the generated files and the config entries (matched by stable markers so we never touch user-owned config).

---

## Public API

### Event type

```go
type Agent string
const (
    Claude   Agent = "claude"
    Codex    Agent = "codex"
    OpenCode Agent = "opencode"
)

type Status string
const (
    StatusStarting       Status = "starting"
    StatusWorking        Status = "working"
    StatusIdle           Status = "idle"
    StatusAwaitingInput  Status = "awaiting_input"
    StatusError          Status = "error"
    StatusEnded          Status = "ended"
)

type Event struct {
    Agent           Agent
    SessionID       string
    ParentSessionID string              // empty unless subagent
    Status          Status
    PrevStatus      Status              // enables idle→working filter predicates
    Tool            string              // optional
    Work            string              // optional
    At              time.Time           // from hook payload; fallback to ingest time
    Tags            map[string]string   // consumer-registered metadata
    Raw             map[string]any      // original hook payload (escape hatch)
}
```

### Hub

```go
type Hub struct { /* unexported */ }

func NewHub(cfg HubConfig) (*Hub, error)

type HubConfig struct {
    Logger        *slog.Logger     // optional, default: discard
    BufferSize    int              // per-subscriber buffer, default 256
    DropPolicy    DropPolicy       // default: DropOldest
    ErrorHandler  func(error)      // sink errors, ingest errors; default: log via Logger
}

// Primitive — no transport
func (h *Hub) Ingest(agent Agent, payload []byte) error

// Convenience — mount at :addr
func (h *Hub) ServeHTTP(addr string) error
// Or mount on your own router
func (h *Hub) Handler() http.Handler

// Broadcast — every call returns an independent stream
func (h *Hub) Events() Stream

// Session metadata
func (h *Hub) RegisterSession(sessionID string, tags map[string]string)
func (h *Hub) UnregisterSession(sessionID string)

// Lifecycle
func (h *Hub) Close() error
```

### Install / Uninstall

```go
type InstallConfig struct {
    Endpoint string   // e.g. http://localhost:9090/hook
    Agents   []Agent  // or AllAgents
    Project  string   // optional: write to project-level settings instead of user-level
}

type InstallResult struct {
    Agent     Agent
    Installed bool
    Skipped   bool
    Reason    string  // why skipped or failed
    Path      string  // config file or artifact path that was touched
}

func InstallHooks(cfg InstallConfig) ([]InstallResult, error)
func UninstallHooks(cfg InstallConfig) ([]InstallResult, error)
```

Per-adapter install logic is encapsulated in the adapter itself (see Adapter below).
`InstallHooks` is idempotent and self-healing — running twice does not duplicate entries.

### Adapter (extension point)

External adapters register themselves the same way built-in ones do.

```go
type Adapter struct {
    Name           Agent
    MapHookEvent   func(event string, payload map[string]any) (*Signal, error)
    InstallHooks   func(cfg InstallConfig) (InstallResult, error)
    UninstallHooks func(cfg InstallConfig) (InstallResult, error)
}

func RegisterAdapter(a Adapter) error   // returns error on name collision
func Adapters() []Adapter               // introspection

// Internal signal type — adapters produce these, decision consumes them
type Signal struct {
    At       time.Time
    Activity bool
    Status   *Status   // authoritative override if non-nil
    Tool     string
    Work     string
    SessionID string
    ParentSessionID string
    Raw      map[string]any // original hook payload; flows into Event.Raw
}
```

Built-in adapters (`claude`, `codex`, `opencode`) are registered via `init()` in their subpackages. Users import `_ "github.com/<org>/<name>/adapters/claude"` to enable.

### Stream + pipeline combinators

`Stream` is a fluent wrapper over an internal broadcast channel.

```go
type Stream struct { /* unexported */ }

// Transforms (non-terminal)
func (s Stream) Filter(pred func(Event) bool) Stream
func (s Stream) Map(fn func(Event) Event) Stream
func (s Stream) Tap(fn func(Event)) Stream
func (s Stream) Debounce(d time.Duration) Stream      // per-session
func (s Stream) Throttle(d time.Duration) Stream      // global
func (s Stream) Window(d time.Duration) WindowStream  // batch by time

// Terminals
func (s Stream) Fanout(sinks ...Sink) error
func (s Stream) Channel() <-chan Event

// Composition helpers
func Not(p func(Event) bool) func(Event) bool
func And(preds ...func(Event) bool) func(Event) bool
func Or(preds ...func(Event) bool) func(Event) bool

// Predefined predicates
var (
    IdleToWorking   func(Event) bool
    AnyAwaitingInput func(Event) bool
)
func ByAgent(a Agent) func(Event) bool
func BySession(id string) func(Event) bool
func ByStatus(s ...Status) func(Event) bool
func ByTag(key, value string) func(Event) bool
```

### Sinks

```go
type Sink interface {
    Send(ctx context.Context, e Event) error
    Name() string
}
```

Reference implementations in subpackages:
- `sinks/webhook` — generic HTTP POST
- `sinks/file` — JSONL append
- `sinks/slog` — structured log
- `sinks/funcsink` — wrap a `func(Event) error`

Sink wrappers:
- `sinks.WithRetry(s Sink, policy RetryPolicy) Sink`
- `sinks.WithBuffer(s Sink, size int, drop DropPolicy) Sink`

---

## Semantic contracts

### Event ordering

- Within a session: preserved. The decision machine reorders events arriving within a 200ms window by `At` to tolerate hook subprocess races.
- Across sessions: no ordering guarantee.
- `At` is taken from the hook payload when present; falls back to ingest time.

### Fanout semantics

- **Parallel** — one slow sink doesn't block others.
- **Error isolation** — a failing sink's errors route to `HubConfig.ErrorHandler`; other sinks continue.
- **Per-sink buffering** — each sink has an independent buffer with configurable drop policy (via `sinks.WithBuffer`). Default: 256-event buffer, drop-oldest, counted in metrics.
- **Per-sink ordering preserved**. Cross-sink ordering not guaranteed.

### Backpressure at Ingest

- `Hub.Ingest` uses a bounded internal queue. Default: drop-oldest with a metrics counter.
- Never blocks indefinitely; never returns a blocking error to the HTTP layer.
- Observable via `Hub.Metrics()` (TBD — v0.2.0).

### Subscriber semantics

- Every call to `Hub.Events()` returns an **independent** broadcast-style Stream.
- Each subscriber has its own buffer; slow subscribers drop (with metrics counter), never block other subscribers or the hub.

### Session tagging

- `RegisterSession` is **forward-only**. Events emitted before registration are not retroactively tagged.
- Unregistered sessions still produce events (with empty Tags).
- `UnregisterSession` does not affect events already in flight.

### Subagents

- Subagents are modeled as **independent sessions** with `ParentSessionID` populated.
- Library emits events for both parent and subagent sessions.
- Consumers collapse via `stream.Filter(func(e Event) bool { return e.ParentSessionID == "" })` if desired.

### Decision machine

- Pure function: `Decide(state, signal) -> (newState, *Transition)`.
- Emits a Transition only when `Status` changes. Duplicates suppressed at source.
- Authoritative hook events (e.g., Claude `Stop`, Codex `agent-turn-complete`) override inferred state.
- No idle-window heuristic in v0.1.0 (hooks are authoritative). May be added if gap-driven.

### Install safety

- All config files are backed up before write (`.bak` suffix with timestamp).
- Writes are atomic (temp file + rename).
- Cross-process safety via `flock(2)` on the config file during edit.
- Every entry written by the library carries a stable marker so `UninstallHooks` only removes our entries.

---

## Event → Status mapping (per agent)

### Claude Code

| Hook event          | Signal                              |
|---------------------|-------------------------------------|
| `SessionStart`      | Status: starting                    |
| `UserPromptSubmit`  | Activity: true (inferred: working)  |
| `PreToolUse`        | Activity: true, Tool: `<name>`      |
| `PostToolUse`       | Activity: true                      |
| `PostToolUseFailure`| Status: error                       |
| `Stop`              | Status: idle                        |
| `Notification`      | Status: awaiting_input              |
| `PermissionRequest` | Status: awaiting_input              |
| `SubagentStart`     | (new session, starting)             |
| `SubagentStop`      | (subagent session → idle)           |
| `SessionEnd`        | Status: ended                       |
| `PreCompact`        | (metadata only, no status change)   |

### Codex

| Notify event           | Signal                        |
|------------------------|-------------------------------|
| `session_meta`         | Status: starting              |
| `task_started`         | Activity: true                |
| `task_complete` / `agent-turn-complete` | Status: idle |
| permission events      | Status: awaiting_input        |
| `error`                | Status: error                 |

### OpenCode

| Plugin event            | Signal                       |
|-------------------------|------------------------------|
| `session.created` (no parent) | Status: starting       |
| `session.status(busy)`  | Activity: true               |
| `session.idle`          | Status: idle                 |
| `permission.asked`      | Status: awaiting_input       |
| `session.error`         | Status: error                |

---

## Known gaps and documented caveats

1. **Claude "thinking" gap.** Between `UserPromptSubmit` and the next hook event, no status signal fires. Status stays `working` (inferred from `UserPromptSubmit`) until `PreToolUse` or `Stop`. Acceptable.

2. **Auto-approved tools.** If users have pre-approved tools in Claude, `PermissionRequest` does not fire for those. "awaiting_input" will be rarer than expected. Documented.

3. **Codex `notify` single slot.** Only one `notify` command per Codex install. Collides with peon-ping and other tools. The library warns on install if a non-library `notify` is already set; uninstall of either will break the other. Documented clearly.

4. **Crash / kill -9.** No hook fires on hard process death. The library's status stays at last-emitted. Consumers owning process lifecycle can detect ended and call `hub.Ingest` with a synthesized end event, or pass a `LivenessCheck`. Documented.

5. **Remote agents.** Hooks fire on the remote host and cannot reach a local hub endpoint unless the user forwards the port. Out of scope for v0.1.0.

6. **Settings scope (Claude).** Project-level `.claude/settings.json` overrides user-level. Installer warns if a project-level settings.json exists without our hooks. `InstallConfig.Project` lets caller target project-level explicitly.

7. **Hook schema drift.** Agents may rename or add events. Adapters tolerate unknown events (log-and-drop). Missing critical events (e.g., `Stop`) degrade the status machine but don't crash.

---

## Repo layout

Single Go module, stdlib-only core.

```
github.com/<org>/<name>
├── go.mod                       # stdlib only for core
├── LICENSE                      # MIT
├── README.md                    # quickstart + API overview
├── doc.go                       # package-level godoc
│
├── agentstatus.go               # Event, Status, Agent, core types
├── hub.go                       # Hub, ingest, broadcast, session registry
├── decision.go                  # Signal, decision machine
├── stream.go                    # Stream + combinators
├── install.go                   # InstallHooks / UninstallHooks orchestration
├── adapter.go                   # Adapter type + registry
│
├── adapters/
│   ├── claude/
│   │   ├── adapter.go
│   │   ├── install.go           # writes ~/.claude/settings.json
│   │   └── map.go               # MapHookEvent
│   ├── codex/
│   │   ├── adapter.go
│   │   ├── install.go           # writes ~/.codex/config.toml + bridge.sh
│   │   ├── bridge.sh.tmpl
│   │   └── map.go
│   └── opencode/
│       ├── adapter.go
│       ├── install.go           # writes ~/.config/opencode/plugins/agentstatus.ts
│       ├── plugin.ts.tmpl
│       └── map.go
│
├── pipeline/                    # optional subpackage (same module)
│   ├── predicates.go
│   └── combinators.go
│
├── sinks/
│   ├── webhook/
│   ├── file/
│   ├── slog/
│   └── funcsink/
│
└── internal/
    ├── broadcast/               # fan-out broadcaster primitive
    └── configfile/              # atomic write + flock helpers
```

### Dependency policy

- Core (`agentstatus.go`, `hub.go`, `decision.go`, `stream.go`, `install.go`, `adapter.go`, all `adapters/*`, `internal/*`, `pipeline/`): **stdlib only**.
- Each sink subpackage may have its own deps (scoped to that sink). Users who don't import the Slack sink don't pull in Slack deps.

### Versioning

- Start at `v0.1.0`. Break freely during initial cortex integration.
- Commit to `v1.0.0` only once the API has been used in anger for several weeks.
- Wire protocol (bridge → hub HTTP payload) is a public contract from v0.1.0 so external bridges can exist.

---

## Installation UX

### Standalone user

```go
package main

import (
    "context"
    "net/http"
    agentstatus "github.com/<org>/<name>"
    _ "github.com/<org>/<name>/adapters/claude"
    _ "github.com/<org>/<name>/adapters/codex"
    _ "github.com/<org>/<name>/adapters/opencode"
    "github.com/<org>/<name>/sinks/webhook"
)

func main() {
    hub, _ := agentstatus.NewHub(agentstatus.HubConfig{})
    defer hub.Close()

    go hub.ServeHTTP(":9090")

    agentstatus.InstallHooks(agentstatus.InstallConfig{
        Endpoint: "http://localhost:9090/hook",
        Agents:   agentstatus.AllAgents,
    })

    events := hub.Events()
    events.
        Filter(agentstatus.Not(agentstatus.IdleToWorking)).
        Debounce(5 * time.Second).
        Fanout(webhook.New("https://my.server/events"))

    select {} // or ctx-based lifecycle
}
```

### Cortex integration (illustrative)

```go
// cortexd wires its existing HTTP router
mux.Handle("/agent/hook", hub.Handler())

// cortex registers session metadata as it learns session_id
hub.RegisterSession(sessionID, map[string]string{
    "ticket_id": ticketID,
    "repo":      repoPath,
})

// cortex consumes events for its own event bus
events := hub.Events()
events.Tap(func(e Event) {
    cortex.Bus.Emit(ev.SessionStatus{
        SessionID: e.SessionID,
        TicketID:  e.Tags["ticket_id"],
        Status:    string(e.Status),
    })
})
```

---

## Open questions (track during implementation)

- Name of the library (TBD, being decided post-spec).
- Whether `pipeline` combinators live in the root package or a subpackage — root is friendlier, subpackage keeps the top-level symbol count small. Default: root.
- Metrics surface (counters for drops, sink errors, sessions) — probably a `hub.Metrics()` method returning a struct. Defer to v0.2.
- Whether to add a `ReplayBuffer(n int)` option that retains last-N events per session for late-subscriber replay. Potentially useful for cortex TUI attach. Defer unless cortex needs it.
- Windows support via `*.cmd` bridge + equivalent config locations. Community-driven if demand exists.

---

## Design invariants (do not violate)

1. **Core is stdlib-only.** No third-party imports in anything outside `sinks/*`.
2. **No binary shipped.** Install is filesystem writes only.
3. **Adapters are externally extensible.** `RegisterAdapter` is public and documented.
4. **Hub owns no transport.** `Ingest` is the primitive; `ServeHTTP` / `Handler` are helpers.
5. **Decision machine is pure.** No I/O, no hidden clock, no globals.
6. **Pre-decision signal filtering is not exposed.** All consumer filtering is on the `Event` stream.
7. **Forward-only tagging.** No retroactive event mutation.
8. **Cortex is one consumer among many.** No cortex-specific concepts (tickets, repos, cortexd) leak into the API surface.

---
