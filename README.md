# agentstatus

A Go library that tells you what your coding agents are doing, in real time.

Subscribes to native hook mechanisms in Claude Code, Codex, and OpenCode, normalizes the events into a unified stream, and gives you typed `Event`s you can filter, log, or pipe wherever you want.

```
starting → working → idle
          ↓
  awaiting_input / error / ended
```

## Install

```bash
go get github.com/kareemaly/agentstatus@latest
```

Requires Go 1.24+. macOS and Linux. `curl` and `sh` must be available (used by the installed hooks to POST events to the hub).

## Quickstart

```go
package main

import (
    "context"
    "fmt"
    "net/http"

    agentstatus "github.com/kareemaly/agentstatus"
    _ "github.com/kareemaly/agentstatus/adapters/claude"
    _ "github.com/kareemaly/agentstatus/adapters/codex"
    _ "github.com/kareemaly/agentstatus/adapters/opencode"
)

func main() {
    // 1. Create a hub
    hub, _ := agentstatus.NewHub(agentstatus.HubConfig{})
    defer hub.Close()

    // 2. Serve the hook endpoint
    go http.ListenAndServe(":9090", hub.Handler())

    // 3. Install hooks into every supported agent.
    // Marker namespaces this consumer's entries so other tools (e.g. a
    // capture script) can install alongside without clobbering.
    agentstatus.InstallHooks(agentstatus.InstallConfig{
        Endpoint: "http://localhost:9090/hook",
        Marker:   "my-tool",
        Agents:   agentstatus.AllAgents,
    })

    // 4. Subscribe to the event stream
    for e := range hub.Events().Channel() {
        fmt.Printf("[%s] %s: %s  tool=%q\n",
            e.At.Format("15:04:05"), e.Agent, e.Status, e.Tool)
    }
}
```

Run this, then run `claude`, `codex`, or `opencode` in any project. Events stream into your loop.

To remove hooks cleanly (scoped to your marker):

```go
agentstatus.UninstallHooks(agentstatus.InstallConfig{
    Endpoint: "http://localhost:9090/hook",
    Marker:   "my-tool",
    Agents:   agentstatus.AllAgents,
})
```

`Marker` is required and must match `^[a-zA-Z0-9_-]{1,32}$`. It namespaces your entries so multiple tools can install side-by-side without overwriting each other; `UninstallHooks` only touches entries with the same marker.

## What you get per event

```go
type Event struct {
    Agent           Agent             // Claude, Codex, OpenCode
    SessionID       string            // agent-provided session id
    ParentSessionID string            // set on subagent lifecycle events
    Status          Status            // working, idle, awaiting_input, error, ended, starting
    PrevStatus      Status            // what status we were in before
    Tool            string            // tool name (title-cased), if applicable
    Work            string            // optional human-readable context
    At              time.Time         // hook-provided timestamp
    Tags            map[string]string // consumer-registered metadata
    Raw             map[string]any    // original hook payload
}
```

Tool names are normalized across agents (Claude's `Read` and OpenCode's `read` both surface as `"Read"`). Original casing is preserved in `Event.Raw`.

## Supported agents

| Agent      | Mechanism                        | Install target                                                         |
|------------|----------------------------------|------------------------------------------------------------------------|
| Claude Code| Native hooks (JSON on stdin)     | `~/.claude/settings.json` (or project-level)                           |
| Codex      | `hooks.json` (experimental)      | `~/.codex/hooks.json` (or project-level)                               |
| OpenCode   | TypeScript plugin                | `$XDG_CONFIG_HOME/opencode/plugins/agentstatus-<marker>.ts` (or project-level) |

### Coverage by agent

| Signal              | Claude | Codex       | OpenCode |
|---------------------|:------:|:-----------:|:--------:|
| `starting`          |   ✓    |      ✓      |    ✓     |
| `working`           |   ✓    |      ✓      |    ✓     |
| `awaiting_input`    |   ✓    |      ✗      |    ✓     |
| `idle`              |   ✓    |      ✓      |    ✓     |
| `error`             |   ✓    |      ✗      |    ✓     |
| `ended`             |   ✓    |      ✗      |    ✗     |
| Tool visibility     |  all   |  Bash only  |   all    |
| Subagent attribution|   ✓    |      ✗      |    ✓     |

See [`specs/design.md`](specs/design.md) for per-agent coverage gap rationales and the full event → status mapping tables.

## Sinks

Sinks durably deliver events somewhere beyond the in-memory Hub. Attach one
via `Hub.AttachSink`; it runs on its own subscriber goroutine and never
blocks the Hub or other sinks.

```go
import "github.com/kareemaly/agentstatus/sinks/file"

sink, _ := file.New(file.Config{
    PathTemplate: "~/tmp/agentstatus-events/{agent}/{date}/{hour}.jsonl",
})
defer sink.Close()
hub.AttachSink(sink)
```

Built-in reference sinks live under `sinks/`:

| Sink              | Status        | Purpose                                  |
|-------------------|---------------|------------------------------------------|
| `sinks/file`      | v0.1.3        | Append events as JSONL to a templated path on disk |
| `sinks/webhook`   | stub          | Generic HTTP POST (planned)              |
| `sinks/slog`      | stub          | Structured log bridge (planned)          |
| `sinks/funcsink`  | stub          | Wrap a `func(Event) error` (planned)     |

`Hub.Close` waits for attached sinks' forwarder goroutines to finish
draining before returning, so `sink.Close()` immediately after
`hub.Close()` sees every broadcast event. See
[`sinks/file/README.md`](sinks/file/README.md) for the capture-script
worked example.

## Adding a custom agent

External adapters register the same way the built-in ones do:

```go
agentstatus.RegisterAdapter(agentstatus.Adapter{
    Name:           "my-agent",
    MapHookEvent:   myMapFunc,
    InstallHooks:   myInstallFunc,
    UninstallHooks: myUninstallFunc,
})
```

See the built-in adapters under `adapters/{claude,codex,opencode}` for reference implementations.

## Configuration notes per agent

- **Claude** — nothing extra. Just install hooks and run `claude`.
- **Codex** — requires `[features] codex_hooks = true` in `~/.codex/config.toml`. The installer warns if it's not set. The library **does not** modify `config.toml`.
- **OpenCode** — defaults to `$XDG_CONFIG_HOME/opencode/plugins/agentstatus-<marker>.ts` (falling back to `~/.config/opencode/plugins/` when `XDG_CONFIG_HOME` is unset). Pass `cfg.Project` to install project-locally under `<project>/.opencode/plugins/` instead. Disabled if `OPENCODE_PURE=1` is set; installer warns.

## Platform support

- **macOS**: ✓
- **Linux**: ✓
- **Windows**: untested in v0.1. `flock(2)` and POSIX path conventions are assumed throughout. PRs welcome.

## Status

`v0.x.y` — pre-release. API may change during initial real-world usage. Semver will be committed from `v1.0.0` once the library has been used in production for a few weeks.

## Design

The full design doc (invariants, architectural decisions, per-event mapping tables, coverage gaps) lives at [`specs/design.md`](specs/design.md). It's the source of truth; the README is the friendly front.

## Development

```bash
make vet test build lint
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for contribution guidelines, adding adapters, and running the test suite.

## License

[MIT](LICENSE).
