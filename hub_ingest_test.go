package agentstatus_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentstatus "github.com/kareemaly/agentstatus"
	_ "github.com/kareemaly/agentstatus/adapters/claude"
)

func newServedHub(t *testing.T) (*agentstatus.Hub, *httptest.Server) {
	t.Helper()
	h, err := agentstatus.NewHub(agentstatus.HubConfig{})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(func() {
		srv.Close()
		_ = h.Close()
	})
	return h, srv
}

func loadPayload(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "claude", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

func postFixture(t *testing.T, srv *httptest.Server, agent, fixture string) *http.Response {
	t.Helper()
	body := loadPayload(t, fixture)
	resp, err := http.Post(srv.URL+"/hook/"+agent, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", fixture, err)
	}
	return resp
}

func recvOrFail(t *testing.T, ch <-chan agentstatus.Event, d time.Duration) agentstatus.Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		return ev
	case <-time.After(d):
		t.Fatal("no event")
		return agentstatus.Event{}
	}
}

func TestHTTP_HappyPath(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	for _, fix := range []string{"session_start.json", "pre_tool_use_read.json", "stop.json"} {
		resp := postFixture(t, srv, "claude", fix)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status %d", fix, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	ch := stream.Channel()
	e1 := recvOrFail(t, ch, time.Second)
	e2 := recvOrFail(t, ch, time.Second)
	e3 := recvOrFail(t, ch, time.Second)

	if e1.Status != agentstatus.StatusStarting || e1.PrevStatus != "" {
		t.Errorf("e1: %q←%q", e1.Status, e1.PrevStatus)
	}
	if e2.Status != agentstatus.StatusWorking || e2.PrevStatus != agentstatus.StatusStarting {
		t.Errorf("e2: %q←%q", e2.Status, e2.PrevStatus)
	}
	if e2.Tool != "Read" {
		t.Errorf("e2 tool: %q", e2.Tool)
	}
	if e3.Status != agentstatus.StatusIdle || e3.PrevStatus != agentstatus.StatusWorking {
		t.Errorf("e3: %q←%q", e3.Status, e3.PrevStatus)
	}
	for _, e := range []agentstatus.Event{e1, e2, e3} {
		if e.Agent != agentstatus.Claude {
			t.Errorf("agent: %q", e.Agent)
		}
		if e.SessionID != "sess-1" {
			t.Errorf("session: %q", e.SessionID)
		}
		if e.Raw == nil || e.Raw["session_id"] != "sess-1" {
			t.Errorf("raw: %v", e.Raw)
		}
	}
}

func TestHTTP_UnknownAgent(t *testing.T) {
	t.Parallel()
	_, srv := newServedHub(t)
	resp, err := http.Post(srv.URL+"/hook/nope", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: %d", resp.StatusCode)
	}
}

func TestHTTP_MalformedJSON(t *testing.T) {
	t.Parallel()
	_, srv := newServedHub(t)
	resp, err := http.Post(srv.URL+"/hook/claude", "application/json", bytes.NewReader([]byte(`{`)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: %d", resp.StatusCode)
	}
}

func TestHTTP_OversizedBody(t *testing.T) {
	t.Parallel()
	_, srv := newServedHub(t)
	big := bytes.Repeat([]byte("a"), 2<<20) // 2 MiB
	resp, err := http.Post(srv.URL+"/hook/claude", "application/json", bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status: %d", resp.StatusCode)
	}
}

func TestHTTP_GetNotAllowed(t *testing.T) {
	t.Parallel()
	_, srv := newServedHub(t)
	resp, err := http.Get(srv.URL + "/hook/claude")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: %d", resp.StatusCode)
	}
}

func TestHTTP_UnknownEventNoEvent(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	resp := postFixture(t, srv, "claude", "unknown_event.json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	select {
	case ev := <-stream.Channel():
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHTTP_PreCompactNoEvent(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	resp := postFixture(t, srv, "claude", "pre_compact.json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	select {
	case ev := <-stream.Channel():
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHTTP_SubagentFlow(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	for _, fix := range []string{"subagent_start.json", "subagent_stop.json"} {
		resp := postFixture(t, srv, "claude", fix)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status %d", fix, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	ch := stream.Channel()
	e1 := recvOrFail(t, ch, time.Second)
	e2 := recvOrFail(t, ch, time.Second)

	for i, e := range []agentstatus.Event{e1, e2} {
		if e.SessionID != "agent-abc123" {
			t.Errorf("[%d] session: %q", i, e.SessionID)
		}
		if e.ParentSessionID != "parent-1" {
			t.Errorf("[%d] parent: %q", i, e.ParentSessionID)
		}
	}
	if e1.Status != agentstatus.StatusStarting {
		t.Errorf("e1 status: %q", e1.Status)
	}
	if e2.Status != agentstatus.StatusIdle {
		t.Errorf("e2 status: %q", e2.Status)
	}
}

func TestHTTP_SubagentIntraEvents(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	for _, fix := range []string{"subagent_start.json", "pre_tool_use_subagent.json", "subagent_stop.json"} {
		resp := postFixture(t, srv, "claude", fix)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status %d", fix, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	ch := stream.Channel()
	e1 := recvOrFail(t, ch, time.Second)
	e2 := recvOrFail(t, ch, time.Second)
	e3 := recvOrFail(t, ch, time.Second)

	for i, e := range []agentstatus.Event{e1, e2, e3} {
		if e.SessionID != "agent-abc123" {
			t.Errorf("[%d] SessionID: got %q want %q", i, e.SessionID, "agent-abc123")
		}
		if e.ParentSessionID != "parent-1" {
			t.Errorf("[%d] ParentSessionID: got %q want %q", i, e.ParentSessionID, "parent-1")
		}
	}
	if e1.Status != agentstatus.StatusStarting {
		t.Errorf("e1 status: %q", e1.Status)
	}
	if e2.Tool != "Read" {
		t.Errorf("e2 tool: %q", e2.Tool)
	}
	if e3.Status != agentstatus.StatusIdle {
		t.Errorf("e3 status: %q", e3.Status)
	}
}

func TestHTTP_ElicitationFlow(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	for _, fix := range []string{
		"session_start.json",
		"user_prompt_submit.json",
		"elicitation_form.json",
		"elicitation_result_accept.json",
		"stop.json",
	} {
		resp := postFixture(t, srv, "claude", fix)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status %d", fix, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	ch := stream.Channel()
	e1 := recvOrFail(t, ch, time.Second) // starting
	e2 := recvOrFail(t, ch, time.Second) // working (UserPromptSubmit)
	e3 := recvOrFail(t, ch, time.Second) // awaiting_input (Elicitation)
	e4 := recvOrFail(t, ch, time.Second) // working (ElicitationResult resumes)
	e5 := recvOrFail(t, ch, time.Second) // idle (Stop)

	if e1.Status != agentstatus.StatusStarting {
		t.Errorf("e1 status: %q", e1.Status)
	}
	if e2.Status != agentstatus.StatusWorking || e2.PrevStatus != agentstatus.StatusStarting {
		t.Errorf("e2: %q←%q", e2.Status, e2.PrevStatus)
	}
	if e3.Status != agentstatus.StatusAwaitingInput || e3.PrevStatus != agentstatus.StatusWorking {
		t.Errorf("e3: %q←%q", e3.Status, e3.PrevStatus)
	}
	if e4.Status != agentstatus.StatusWorking || e4.PrevStatus != agentstatus.StatusAwaitingInput {
		t.Errorf("e4: %q←%q", e4.Status, e4.PrevStatus)
	}
	if e5.Status != agentstatus.StatusIdle || e5.PrevStatus != agentstatus.StatusWorking {
		t.Errorf("e5: %q←%q", e5.Status, e5.PrevStatus)
	}
}

func TestHTTP_StopFailureProducesError(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	resp := postFixture(t, srv, "claude", "stop_failure.json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	e := recvOrFail(t, stream.Channel(), time.Second)
	if e.Status != agentstatus.StatusError {
		t.Errorf("status: got %q want error", e.Status)
	}
}

func TestHTTP_NotificationAuthSuccessNoEvent(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	resp := postFixture(t, srv, "claude", "notification_auth_success.json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	select {
	case ev := <-stream.Channel():
		t.Fatalf("unexpected event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHTTP_MultiToolSequence(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	// 7 POSTs → 5 events (PostToolUse rows suppressed; each PostToolUse now
	// carries the same (working, tool) pair as its preceding PreToolUse).
	for _, fix := range []string{
		"session_start.json",
		"user_prompt_submit.json",
		"pre_tool_use_read.json",
		"post_tool_use.json",
		"pre_tool_use_bash.json",
		"post_tool_use_bash.json",
		"stop.json",
	} {
		resp := postFixture(t, srv, "claude", fix)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status %d", fix, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	ch := stream.Channel()
	e1 := recvOrFail(t, ch, time.Second) // starting
	e2 := recvOrFail(t, ch, time.Second) // working, tool=""
	e3 := recvOrFail(t, ch, time.Second) // working, tool="Read"
	e4 := recvOrFail(t, ch, time.Second) // working, tool="Bash"
	e5 := recvOrFail(t, ch, time.Second) // idle, tool=""

	if e1.Status != agentstatus.StatusStarting || e1.PrevStatus != "" {
		t.Errorf("e1: %q←%q", e1.Status, e1.PrevStatus)
	}
	if e2.Status != agentstatus.StatusWorking || e2.PrevStatus != agentstatus.StatusStarting || e2.Tool != "" {
		t.Errorf("e2: %q←%q tool=%q", e2.Status, e2.PrevStatus, e2.Tool)
	}
	if e3.Status != agentstatus.StatusWorking || e3.PrevStatus != agentstatus.StatusWorking || e3.Tool != "Read" {
		t.Errorf("e3: %q←%q tool=%q", e3.Status, e3.PrevStatus, e3.Tool)
	}
	if e4.Status != agentstatus.StatusWorking || e4.PrevStatus != agentstatus.StatusWorking || e4.Tool != "Bash" {
		t.Errorf("e4: %q←%q tool=%q", e4.Status, e4.PrevStatus, e4.Tool)
	}
	if e5.Status != agentstatus.StatusIdle || e5.PrevStatus != agentstatus.StatusWorking || e5.Tool != "" {
		t.Errorf("e5: %q←%q tool=%q", e5.Status, e5.PrevStatus, e5.Tool)
	}

	// Confirm no 6th event arrives.
	select {
	case ev := <-ch:
		t.Fatalf("unexpected 6th event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHTTP_PostToolUseFailureIsActivity(t *testing.T) {
	t.Parallel()
	h, srv := newServedHub(t)
	stream := h.Events()

	// PreToolUse(Bash) sets state to working/Bash.
	// PostToolUseFailure(Bash) is the same (working, "Bash") pair → suppressed.
	// Stop → idle. No error event should ever appear.
	for _, fix := range []string{
		"session_start.json",
		"user_prompt_submit.json",
		"pre_tool_use_bash.json",
		"post_tool_use_failure.json",
		"stop.json",
	} {
		resp := postFixture(t, srv, "claude", fix)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status %d", fix, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	ch := stream.Channel()
	e1 := recvOrFail(t, ch, time.Second) // starting
	e2 := recvOrFail(t, ch, time.Second) // working, tool=""
	e3 := recvOrFail(t, ch, time.Second) // working, tool="Bash"
	e4 := recvOrFail(t, ch, time.Second) // idle   (PostToolUseFailure suppressed)

	if e1.Status != agentstatus.StatusStarting {
		t.Errorf("e1 status: %q", e1.Status)
	}
	if e2.Status != agentstatus.StatusWorking || e2.Tool != "" {
		t.Errorf("e2: status=%q tool=%q", e2.Status, e2.Tool)
	}
	if e3.Status != agentstatus.StatusWorking || e3.Tool != "Bash" {
		t.Errorf("e3: status=%q tool=%q", e3.Status, e3.Tool)
	}
	if e4.Status != agentstatus.StatusIdle {
		t.Errorf("e4 status: %q", e4.Status)
	}

	// Confirm no error event arrived anywhere in the sequence.
	for i, e := range []agentstatus.Event{e1, e2, e3, e4} {
		if e.Status == agentstatus.StatusError {
			t.Errorf("e%d: unexpected error status", i+1)
		}
	}

	// Confirm no 5th event (PostToolUseFailure was suppressed).
	select {
	case ev := <-ch:
		t.Fatalf("unexpected 5th event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHTTP_ConcurrentIngest(t *testing.T) {
	t.Parallel()
	h, err := agentstatus.NewHub(agentstatus.HubConfig{BufferSize: 2048})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(func() {
		srv.Close()
		_ = h.Close()
	})

	stream := h.Events()
	var got atomic.Int32
	done := make(chan struct{})
	go func() {
		for range stream.Channel() {
			if got.Add(1) == 1000 {
				close(done)
				return
			}
		}
	}()

	const N = 1000
	var wg sync.WaitGroup
	wg.Add(N)
	for i := range N {
		go func(i int) {
			defer wg.Done()
			body := fmt.Appendf(nil, `{"hook_event_name":"SessionStart","session_id":"s-%d"}`, i)
			resp, err := http.Post(srv.URL+"/hook/claude", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Errorf("POST: %v", err)
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				t.Errorf("status: %d", resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out: got %d/1000 events", got.Load())
	}
}
