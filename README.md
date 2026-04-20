# agentstatus

A Go library that tells you what your coding agents are doing in real
time — `starting`, `working`, `idle`, `awaiting_input`, `error`,
`ended` — via their native hook mechanisms.

## Supported agents (v0.1.0)

- **Claude Code** — hooks in `~/.claude/settings.json`
- **Codex** — `notify` bridge in `~/.codex/config.toml`
- **OpenCode** — plugin at `~/.config/opencode/plugins/agentstatus.ts`

macOS and Linux only. Requires `curl` and `sh`.

## Status

Pre-release. API unstable until `v1.0.0`. Not yet published.

## Design

Full design and invariants: [`specs/design.md`](specs/design.md).

## Development

```
make vet test build lint
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

[MIT](LICENSE).
