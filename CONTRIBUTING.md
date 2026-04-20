# Contributing to agentstatus

Thanks for your interest in contributing. This is an open-source Go library
maintained independently; external contributions are welcome.

## Design source of truth

The locked-in design lives in [`specs/design.md`](specs/design.md). Read it
before opening a non-trivial PR. If you want to change the design, open an
issue first rather than a code PR.

Key invariants (do not violate without a spec change):

- Core (`agentstatus.go`, `hub.go`, `decision.go`, `stream.go`,
  `install.go`, `adapter.go`, all `adapters/*`, `internal/*`, `pipeline/`)
  is **stdlib-only**.
- No binary shipped. Install is filesystem writes only.
- Decision machine is pure — no I/O, no hidden clock, no globals.
- `RegisterAdapter` is the external extension point.

## Running tests and lint

Requirements: Go 1.24+ and [`golangci-lint`](https://golangci-lint.run/).

```
make vet     # go vet ./...
make test    # go test ./...
make build   # go build ./...
make lint    # golangci-lint run ./...
make tidy    # go mod tidy
```

CI runs the same checks on macOS and Linux for every PR.

## Commit style

- Short imperative subject line (e.g. `add codex adapter install`), under
  72 characters.
- Reference the affected package in parentheses if the change is
  localized: `adapters/claude: handle SubagentStart`.
- Body is optional; use it to explain the *why* when the *what* isn't
  obvious from the diff.

## Adding a new adapter

Adapters live under `adapters/<name>/` and must:

1. Define an `agentstatus.Adapter` value and register it from `init()` so
   callers enable the adapter with a blank import:
   `import _ "github.com/kareemaly/agentstatus/adapters/<name>"`.
2. Implement `MapHookEvent(event string, payload map[string]any)
   (*Signal, error)` — pure, no I/O. Unknown events must be tolerated
   (log-and-drop); missing critical events must degrade, not crash.
3. Implement `InstallHooks` / `UninstallHooks`. Writes must be atomic,
   back up the original config (`.bak` with timestamp), take an advisory
   lock on the config file, and tag every written entry with a stable
   marker so uninstall never touches user-owned config.
4. Be stdlib-only — adapters are part of the core per the dependency
   policy.
5. Document the event → Signal mapping in `specs/design.md` under
   "Event → Status mapping".

Tests should cover the mapping table exhaustively and exercise install
and uninstall against a temp HOME.
