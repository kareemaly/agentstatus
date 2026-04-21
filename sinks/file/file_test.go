package file

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kareemaly/agentstatus"
	_ "github.com/kareemaly/agentstatus/adapters/claude"
)

func newSink(t *testing.T, cfg Config) *Sink {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNew_EmptyTemplateRejected(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{PathTemplate: "   "}); err == nil {
		t.Fatal("expected error for whitespace-only template")
	}
}

func TestResolvePath_Placeholders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newSink(t, Config{PathTemplate: dir + "/{agent}/{date}/{hour}-{session}.jsonl"})

	at := time.Date(2026, 4, 21, 15, 30, 0, 0, time.UTC)
	got, err := s.resolvePath(agentstatus.Event{
		Agent:     agentstatus.Claude,
		SessionID: "abc",
		At:        at,
	})
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	want := filepath.Join(dir, "claude", "2026-04-21", "15-abc.jsonl")
	if got != want {
		t.Errorf("path: got %q want %q", got, want)
	}
}

func TestResolvePath_UTCNormalization(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newSink(t, Config{PathTemplate: dir + "/{date}/{hour}.jsonl"})

	// 01:30 in a +02:00 zone is 23:30 the previous day in UTC.
	loc := time.FixedZone("fixed", 2*60*60)
	at := time.Date(2026, 4, 22, 1, 30, 0, 0, loc)
	got, err := s.resolvePath(agentstatus.Event{At: at})
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	want := filepath.Join(dir, "2026-04-21", "23.jsonl")
	if got != want {
		t.Errorf("path: got %q want %q", got, want)
	}
}

func TestResolvePath_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := newSink(t, Config{PathTemplate: "~/capture/{agent}.jsonl"})
	got, err := s.resolvePath(agentstatus.Event{Agent: agentstatus.Codex, At: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	want := filepath.Join(home, "capture", "codex.jsonl")
	if got != want {
		t.Errorf("path: got %q want %q", got, want)
	}
}

func TestResolvePath_EmptyAtFallsBackToClock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newSink(t, Config{
		PathTemplate: dir + "/{date}.jsonl",
		Clock:        func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})

	got, err := s.resolvePath(agentstatus.Event{})
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	want := filepath.Join(dir, "2026-01-02.jsonl")
	if got != want {
		t.Errorf("path: got %q want %q", got, want)
	}
}

func TestSend_AppendsOneLinePerEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newSink(t, Config{PathTemplate: dir + "/events.jsonl"})

	events := []agentstatus.Event{
		{Agent: agentstatus.Claude, SessionID: "s1", Status: agentstatus.StatusWorking, At: time.Unix(1, 0).UTC(), Tool: "Read"},
		{Agent: agentstatus.Claude, SessionID: "s1", Status: agentstatus.StatusIdle, PrevStatus: agentstatus.StatusWorking, At: time.Unix(2, 0).UTC(), Raw: map[string]any{"hook_event_name": "Stop"}},
	}
	for _, e := range events {
		if err := s.Send(context.Background(), e); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := readLines(t, filepath.Join(dir, "events.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("line count: got %d want 2; content=%v", len(lines), lines)
	}

	var first wireEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("unmarshal line 0: %v", err)
	}
	if first.Agent != "claude" || first.SessionID != "s1" || first.Status != "working" || first.Tool != "Read" {
		t.Errorf("line 0 fields: %+v", first)
	}

	var second wireEvent
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("unmarshal line 1: %v", err)
	}
	if second.PrevStatus != "working" {
		t.Errorf("line 1 prev_status: got %q want working", second.PrevStatus)
	}
	if got, _ := second.Raw["hook_event_name"].(string); got != "Stop" {
		t.Errorf("line 1 raw: got %v", second.Raw)
	}
}

func TestSend_AppendsToExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "pre.jsonl")
	if err := os.WriteFile(path, []byte("pre-existing\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	s := newSink(t, Config{PathTemplate: path})
	if err := s.Send(context.Background(), agentstatus.Event{Agent: agentstatus.Claude, SessionID: "x", At: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 2 || lines[0] != "pre-existing" {
		t.Fatalf("lines=%v; expected pre-existing preserved and one appended", lines)
	}
}

func TestSend_CreatesDirectoriesRecursively(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newSink(t, Config{PathTemplate: dir + "/{agent}/{date}/{hour}.jsonl"})

	at := time.Date(2026, 4, 21, 15, 0, 0, 0, time.UTC)
	if err := s.Send(context.Background(), agentstatus.Event{Agent: agentstatus.Claude, SessionID: "s1", At: at}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	expected := filepath.Join(dir, "claude", "2026-04-21", "15.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected file not created: %v", err)
	}
}

func TestClose_FlushesPending(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newSink(t, Config{PathTemplate: dir + "/events.jsonl", BufferSize: 128})

	const n = 50
	for i := range n {
		if err := s.Send(context.Background(), agentstatus.Event{Agent: agentstatus.Claude, SessionID: "s1", At: time.Unix(int64(i), 0).UTC()}); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := readLines(t, filepath.Join(dir, "events.jsonl"))
	if len(lines) != n {
		t.Fatalf("line count after close: got %d want %d (close did not flush)", len(lines), n)
	}
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newSink(t, Config{PathTemplate: dir + "/x.jsonl"})
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Send after Close is a no-op.
	if err := s.Send(context.Background(), agentstatus.Event{}); err != nil {
		t.Errorf("Send after Close: %v", err)
	}
}

func TestSend_DropOldestOnOverflow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Keep the worker starved by closing immediately-created sinks... instead
	// inject a stall: construct a sink whose worker is blocked on a slow
	// writer. Simpler: use BufferSize=2 and push faster than worker can
	// drain by pausing via a mid-test hook. Since the worker drains
	// quickly on fast filesystems, we instead gate the worker by grabbing
	// the sink's internal mutex to hold openFile.
	s := newSink(t, Config{PathTemplate: dir + "/events.jsonl", BufferSize: 2})

	// Hold the files mutex so write() blocks once the first event starts.
	s.mu.Lock()

	// Prime the worker with a first event it will start processing and
	// then block on the mutex inside openFile.
	_ = s.Send(context.Background(), agentstatus.Event{Agent: "a", SessionID: "0", At: time.Unix(0, 0).UTC()})

	// Wait until that event has been pulled off the channel.
	waitFor(t, func() bool { return len(s.events) == 0 }, time.Second)

	// Now the channel is empty and the worker is blocked on s.mu. Fill the
	// buffer (2), then push extras to trigger drop-oldest.
	for i := 1; i <= 10; i++ {
		_ = s.Send(context.Background(), agentstatus.Event{Agent: "a", SessionID: "e", At: time.Unix(int64(i), 0).UTC()})
	}
	// Release the worker.
	s.mu.Unlock()

	if drops := s.Drops(); drops == 0 {
		t.Errorf("expected Drops > 0, got %d", drops)
	}
}

func TestEvictIdle_ClosesStaleHandles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var current atomic.Int64
	current.Store(base.Unix())
	clock := func() time.Time { return time.Unix(current.Load(), 0).UTC() }

	s := newSink(t, Config{
		PathTemplate: dir + "/{session}.jsonl",
		IdleTimeout:  time.Hour,
		Clock:        clock,
	})

	pathA := filepath.Join(dir, "a.jsonl")
	pathB := filepath.Join(dir, "b.jsonl")

	if err := s.Send(context.Background(), agentstatus.Event{SessionID: "a", At: base}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := s.Send(context.Background(), agentstatus.Event{SessionID: "b", At: base}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Wait until both files have been opened and their lastUsed recorded.
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.files) == 2
	}, time.Second)

	// Advance clock past idle timeout, touch only "a" so that "b" becomes stale.
	current.Store(base.Add(90 * time.Minute).Unix())
	if err := s.Send(context.Background(), agentstatus.Event{SessionID: "a", At: time.Unix(current.Load(), 0).UTC()}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Wait until a's lastUsed reflects the advanced clock.
	advanced := base.Add(90 * time.Minute)
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		of, ok := s.files[pathA]
		return ok && !of.lastUsed.Before(advanced)
	}, time.Second)

	// Trigger eviction synchronously; the worker's own ticker would also fire,
	// but timing in a short test is not deterministic.
	s.evictIdle()

	s.mu.Lock()
	_, hasA := s.files[pathA]
	_, hasB := s.files[pathB]
	s.mu.Unlock()
	if !hasA {
		t.Errorf("session 'a' was evicted but should be fresh")
	}
	if hasB {
		t.Errorf("session 'b' should have been evicted as stale")
	}
}

func TestSink_IntegratesWithHub(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hub, err := agentstatus.NewHub(agentstatus.HubConfig{})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}

	s, err := New(Config{
		PathTemplate: dir + "/{agent}/{date}.jsonl",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New sink: %v", err)
	}
	hub.AttachSink(s)

	stream := hub.Events()

	sendAndAwait := func(payload string) {
		t.Helper()
		if err := hub.Ingest(agentstatus.Claude, []byte(payload)); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
		select {
		case <-stream.Channel():
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for event broadcast")
		}
	}

	sendAndAwait(`{"hook_event_name":"SessionStart","session_id":"s1","timestamp":"2026-04-21T10:00:00Z"}`)
	sendAndAwait(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Read","timestamp":"2026-04-21T10:00:01Z"}`)
	sendAndAwait(`{"hook_event_name":"Stop","session_id":"s1","timestamp":"2026-04-21T10:00:02Z"}`)

	if err := hub.Close(); err != nil {
		t.Fatalf("hub Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("sink Close: %v", err)
	}

	lines := readLines(t, filepath.Join(dir, "claude", "2026-04-21.jsonl"))
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d: %v", len(lines), lines)
	}

	var starting, working, idle bool
	for _, ln := range lines {
		var w wireEvent
		if err := json.Unmarshal([]byte(ln), &w); err != nil {
			t.Fatalf("unmarshal %q: %v", ln, err)
		}
		switch w.Status {
		case "starting":
			starting = true
		case "working":
			working = true
		case "idle":
			idle = true
		}
		if w.Raw == nil {
			t.Errorf("event missing Raw: %+v", w)
		}
	}
	if !starting || !working || !idle {
		t.Errorf("expected starting+working+idle statuses in file, got starting=%v working=%v idle=%v", starting, working, idle)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

func waitFor(t *testing.T, pred func() bool, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for predicate")
}

func TestMarshal_OmitsEmptyOptionals(t *testing.T) {
	t.Parallel()
	b, err := marshal(agentstatus.Event{
		Agent:     agentstatus.Claude,
		SessionID: "s1",
		Status:    agentstatus.StatusIdle,
		At:        time.Unix(0, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("marshal missing trailing newline: %q", s)
	}
	forbidden := []string{`"parent_session_id"`, `"tool"`, `"work"`, `"tags"`, `"raw"`, `"prev_status"`}
	for _, k := range forbidden {
		if strings.Contains(s, k) {
			t.Errorf("marshal should omit empty %s: %s", k, s)
		}
	}
}
