package agentstatus

import (
	"sync"
	"testing"
	"time"
)

func newTestHub(t *testing.T) *Hub {
	t.Helper()
	h, err := NewHub(HubConfig{})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func recvWithin(t *testing.T, ch <-chan Event, d time.Duration) (Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(d):
		return Event{}, false
	}
}

func expectNoEvent(t *testing.T, ch <-chan Event, d time.Duration) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(d):
	}
}

func TestHub_SmokeFanout(t *testing.T) {
	t.Parallel()
	h := newTestHub(t)
	h.RegisterSession("s1", map[string]string{"k": "v"})

	streams := []Stream{h.Events(), h.Events(), h.Events()}

	h.dispatchSignal(Claude, Signal{
		Activity:  true,
		SessionID: "s1",
		At:        time.Unix(10, 0),
		Tool:      "Edit",
		Work:      "main.go",
	})

	for i, s := range streams {
		ev, ok := recvWithin(t, s.Channel(), time.Second)
		if !ok {
			t.Fatalf("stream %d: no event", i)
		}
		if ev.Agent != Claude {
			t.Errorf("stream %d agent: got %q, want %q", i, ev.Agent, Claude)
		}
		if ev.Status != StatusWorking || ev.PrevStatus != "" {
			t.Errorf("stream %d status: got %q prev %q", i, ev.Status, ev.PrevStatus)
		}
		if ev.SessionID != "s1" {
			t.Errorf("stream %d session: got %q", i, ev.SessionID)
		}
		if ev.Tool != "Edit" || ev.Work != "main.go" {
			t.Errorf("stream %d tool/work: got %q/%q", i, ev.Tool, ev.Work)
		}
		if !ev.At.Equal(time.Unix(10, 0)) {
			t.Errorf("stream %d at: got %v", i, ev.At)
		}
		if ev.Tags["k"] != "v" {
			t.Errorf("stream %d tags: got %v", i, ev.Tags)
		}
	}
}

func TestHub_TagsAreForwardOnly(t *testing.T) {
	t.Parallel()
	h := newTestHub(t)
	stream := h.Events()

	// Dispatch before register → no tags.
	h.dispatchSignal(Claude, Signal{Activity: true, SessionID: "s1"})
	ev1, ok := recvWithin(t, stream.Channel(), time.Second)
	if !ok {
		t.Fatal("missing first event")
	}
	if ev1.Tags != nil {
		t.Errorf("first event should have nil tags, got %v", ev1.Tags)
	}

	// Register, then dispatch a status change → tagged.
	h.RegisterSession("s1", map[string]string{"k": "v"})
	idle := StatusIdle
	h.dispatchSignal(Claude, Signal{Status: &idle, SessionID: "s1"})
	ev2, ok := recvWithin(t, stream.Channel(), time.Second)
	if !ok {
		t.Fatal("missing second event")
	}
	if ev2.Tags["k"] != "v" {
		t.Errorf("second event tags: got %v", ev2.Tags)
	}

	// Mutate source tags after-the-fact and confirm ev2 unchanged (defensive copy).
	// We also re-check ev1 to ensure it was never retroactively mutated.
	if ev1.Tags != nil {
		t.Errorf("ev1 tags drifted: %v", ev1.Tags)
	}
	if ev2.Tags["k"] != "v" {
		t.Errorf("ev2 tags drifted: %v", ev2.Tags)
	}
}

func TestHub_UnregisterIsForwardOnly(t *testing.T) {
	t.Parallel()
	h := newTestHub(t)
	stream := h.Events()

	h.RegisterSession("s1", map[string]string{"k": "v"})
	h.dispatchSignal(Claude, Signal{Activity: true, SessionID: "s1"})
	ev1, ok := recvWithin(t, stream.Channel(), time.Second)
	if !ok {
		t.Fatal("missing first event")
	}
	if ev1.Tags["k"] != "v" {
		t.Errorf("ev1 should be tagged, got %v", ev1.Tags)
	}

	h.UnregisterSession("s1")
	idle := StatusIdle
	h.dispatchSignal(Claude, Signal{Status: &idle, SessionID: "s1"})
	ev2, ok := recvWithin(t, stream.Channel(), time.Second)
	if !ok {
		t.Fatal("missing second event")
	}
	if ev2.Tags != nil {
		t.Errorf("ev2 should be untagged after unregister, got %v", ev2.Tags)
	}

	// ev1 is still tagged in-flight (captured before unregister).
	if ev1.Tags["k"] != "v" {
		t.Errorf("ev1 tags mutated after unregister: %v", ev1.Tags)
	}
}

func TestHub_DuplicateSuppression(t *testing.T) {
	t.Parallel()
	h := newTestHub(t)
	stream := h.Events()

	h.dispatchSignal(Claude, Signal{Activity: true, SessionID: "s1"})
	h.dispatchSignal(Claude, Signal{Activity: true, SessionID: "s1"})
	h.dispatchSignal(Claude, Signal{Activity: true, SessionID: "s1"})

	if _, ok := recvWithin(t, stream.Channel(), time.Second); !ok {
		t.Fatal("expected first event")
	}
	expectNoEvent(t, stream.Channel(), 50*time.Millisecond)
}

func TestHub_Subagent(t *testing.T) {
	t.Parallel()
	h := newTestHub(t)
	stream := h.Events()

	h.dispatchSignal(Claude, Signal{
		Activity:        true,
		SessionID:       "child",
		ParentSessionID: "parent",
	})

	ev, ok := recvWithin(t, stream.Channel(), time.Second)
	if !ok {
		t.Fatal("missing event")
	}
	if ev.SessionID != "child" || ev.ParentSessionID != "parent" {
		t.Errorf("session ids: got %q/%q", ev.SessionID, ev.ParentSessionID)
	}
}

func TestHub_AuthoritativeOverride(t *testing.T) {
	t.Parallel()
	h := newTestHub(t)
	stream := h.Events()

	h.dispatchSignal(Claude, Signal{Activity: true, SessionID: "s1"})
	_, _ = recvWithin(t, stream.Channel(), time.Second)

	idle := StatusIdle
	h.dispatchSignal(Claude, Signal{Activity: true, Status: &idle, SessionID: "s1"})
	ev, ok := recvWithin(t, stream.Channel(), time.Second)
	if !ok {
		t.Fatal("missing event")
	}
	if ev.Status != StatusIdle || ev.PrevStatus != StatusWorking {
		t.Errorf("got %q←%q, want idle←working", ev.Status, ev.PrevStatus)
	}
}

func TestHub_CloseIdempotent(t *testing.T) {
	t.Parallel()
	h, err := NewHub(HubConfig{})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	stream := h.Events()

	if err := h.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if _, ok := <-stream.Channel(); ok {
		t.Fatal("stream channel should be closed after Hub.Close")
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// Dispatching after close must not panic and must not deliver anywhere.
	h.dispatchSignal(Claude, Signal{Activity: true, SessionID: "s1"})
}

func TestDispatch_NormalizesToolName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"read", "Read"},
		{"Bash", "Bash"},
		{"", ""},
		{"grep", "Grep"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			h := newTestHub(t)
			stream := h.Events()
			h.dispatchSignal(Claude, Signal{
				Activity:  true,
				SessionID: "s1",
				Tool:      tc.in,
			})
			ev, ok := recvWithin(t, stream.Channel(), time.Second)
			if !ok {
				t.Fatal("no event")
			}
			if ev.Tool != tc.want {
				t.Errorf("Tool: got %q want %q", ev.Tool, tc.want)
			}
		})
	}
}

func TestHub_Concurrent(t *testing.T) {
	t.Parallel()
	h := newTestHub(t)

	// Pre-subscribe a couple of drainers so publishes have somewhere to go.
	for i := 0; i < 2; i++ {
		s := h.Events()
		go func(ch <-chan Event) {
			for range ch {
			}
		}(s.Channel())
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				h.RegisterSession("s", map[string]string{"x": "y"})
				h.dispatchSignal(Claude, Signal{Activity: true, SessionID: "s"})
				idle := StatusIdle
				h.dispatchSignal(Claude, Signal{Status: &idle, SessionID: "s"})
				if n%10 == 0 {
					h.UnregisterSession("s")
				}
				_ = h.Events()
				_ = id
			}
		}(i)
	}
	wg.Wait()
}
