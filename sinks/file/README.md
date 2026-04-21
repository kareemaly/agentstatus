# sinks/file

A stdlib-only reference Sink that appends `agentstatus.Event` values as
JSON Lines to files on disk. Picks a destination file per event by expanding
a path template with the placeholders `{agent}`, `{date}`, `{hour}`, and
`{session}` (all UTC).

Intended use: durability for "capture in the wild" sessions — attach the sink
to a `Hub`, leave it running for a day, and inspect the resulting JSONL files
with `grep` / `jq`.

## Usage

```go
package main

import (
    "context"
    "log"
    "net/http"
    "os/signal"
    "syscall"
    "time"

    "github.com/kareemaly/agentstatus"
    _ "github.com/kareemaly/agentstatus/adapters/claude"
    _ "github.com/kareemaly/agentstatus/adapters/codex"
    _ "github.com/kareemaly/agentstatus/adapters/opencode"
    "github.com/kareemaly/agentstatus/sinks/file"
)

func main() {
    hub, err := agentstatus.NewHub(agentstatus.HubConfig{})
    if err != nil {
        log.Fatal(err)
    }

    sink, err := file.New(file.Config{
        PathTemplate: "~/tmp/agentstatus-events/{agent}/{date}/{hour}.jsonl",
        BufferSize:   256,
        IdleTimeout:  5 * time.Minute,
    })
    if err != nil {
        log.Fatal(err)
    }
    hub.AttachSink(sink)

    go http.ListenAndServe(":9090", hub.Handler())

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    <-ctx.Done()

    // Hub.Close waits for attached-sink forwarders to finish draining before
    // returning, so sink.Close sees the complete event stream.
    _ = hub.Close()
    _ = sink.Close()
}
```

After leaving the capture running, inspect the output:

```sh
jq '.status' ~/tmp/agentstatus-events/claude/2026-04-21/15.jsonl | sort | uniq -c
jq -c 'select(.status == "awaiting_input")' ~/tmp/agentstatus-events/**/*.jsonl
```

## Path template

| Placeholder | Value |
|-------------|-------|
| `{agent}`   | `claude`, `codex`, `opencode`, or the custom adapter's Name |
| `{date}`    | UTC `YYYY-MM-DD` of `Event.At` (or `time.Now().UTC()` if `At` is zero) |
| `{hour}`    | UTC `HH` (00-23) |
| `{session}` | `Event.SessionID` |

A leading `~/` is expanded to the current user's home (`os.UserHomeDir`).
Files are opened in append mode, and parent directories are created
recursively on first use.

## Event wire format

Each line is one `Event` marshaled to JSON with a trailing newline. Field
names are snake_case; empty optional fields are omitted:

```json
{"agent":"claude","session_id":"abc","status":"working","prev_status":"idle","tool":"Read","at":"2026-04-21T15:30:02Z","raw":{"hook_event_name":"PreToolUse","tool_name":"Read","session_id":"abc"}}
```

`raw` preserves the original hook payload verbatim, which is the primary
reason to use this sink for exploratory analysis: every field that the
agent emitted is still there, even ones agentstatus didn't model.

## Concurrency & durability semantics

- **Non-blocking delivery.** `Send` always returns immediately. Internally
  the sink enqueues the event onto a buffered channel (default 256) and a
  single background goroutine drains it. Slow I/O never blocks the caller.
- **Drop-oldest on overflow.** If the buffer is full, the oldest queued event
  is discarded (counted via `Drops()`) to make room for the newest.
- **Stale handle eviction.** File handles that haven't been written to for
  `IdleTimeout` (default 5 min) are closed by the worker to bound open-file
  usage during long runs. Re-opened lazily on the next matching event.
- **Atomic per-line writes.** Each serialized event is one `Write` call with
  a trailing newline; concurrent writes to the same file are serialized
  inside the sink.
- **`Close` drains pending events.** Flushes the queue, closes every open
  file, then returns. Idempotent.
- **`Hub.Close` blocks on attached sinks.** The hub waits for each forwarder
  to drain its subscriber channel into its sink before returning, so calling
  `sink.Close()` after `hub.Close()` will see every broadcast event.

## Not in scope

- Rotation by size, compression, or retention — path rotation is purely
  time-based via the template.
- At-most-once / at-least-once delivery guarantees. Write errors are logged
  via the configured `slog.Logger` and the event is dropped; use a more
  durable sink (or a persistent queue) if you need retries.
- Cross-process file locking. If two processes write to the same path with
  overlapping hours, their lines interleave at the OS level (single `Write`
  calls are atomic on POSIX for small payloads, but no ordering guarantee).
