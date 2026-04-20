# agentstatus

A Go library for real-time status detection of coding agents (Claude Code, Codex, OpenCode, and more via custom adapters).

One unified event stream across all your agents. Hook-first, no tmux dependency, no daemon, no DSL. Just a library.

## Status

Pre-release — API is unstable. Not published yet.

Design doc: see [`specs/design.md`](specs/design.md) (coming soon).

## What it does

- Installs native hooks into each supported agent (one-liner per agent, reversible)
- Consolidates hook events into a single `Hub` in your Go process
- Emits `Event`s with normalized `Status` (`working` / `idle` / `awaiting_input` / `error` / `ended`)
- Exposes a fluent pipeline (`Filter`, `Map`, `Debounce`, `Throttle`, `Fanout`) over the event stream
- Ships reference sinks for webhook, Slack, file, and slog

## Supported agents (v0.1.0)

- Claude Code — via native hooks (`~/.claude/settings.json`)
- Codex — via `notify` command (`~/.codex/config.toml`)
- OpenCode — via plugin (`~/.config/opencode/plugins/`)

Custom agents: implement `agentstatus.Adapter` and call `RegisterAdapter`.

## Platforms

macOS and Linux. Requires `curl` and `sh` on the host. Windows is untested.

## License

MIT (pending).
