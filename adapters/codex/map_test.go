package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentstatus "github.com/kareemaly/agentstatus"
)

func loadFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join("testdata", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

func TestMapHookEvent_AllRows(t *testing.T) {
	t.Parallel()

	starting := agentstatus.StatusStarting
	idle := agentstatus.StatusIdle

	type want struct {
		drop      bool
		status    *agentstatus.Status
		activity  bool
		tool      string
		sessionID string
	}

	cases := []struct {
		fixture string
		event   string
		want    want
	}{
		{"session_start.json", "SessionStart", want{status: &starting, sessionID: "codex-sess-1"}},
		{"user_prompt_submit.json", "UserPromptSubmit", want{activity: true, sessionID: "codex-sess-1"}},
		{"pre_tool_use.json", "PreToolUse", want{activity: true, tool: "Bash", sessionID: "codex-sess-1"}},
		{"post_tool_use.json", "PostToolUse", want{activity: true, tool: "Bash", sessionID: "codex-sess-1"}},
		{"stop.json", "Stop", want{status: &idle, sessionID: "codex-sess-1"}},
		// Unknown event → drop
		{"session_start.json", "UnknownEvent", want{drop: true}},
	}

	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			payload := loadFixture(t, tc.fixture)
			sig, err := MapHookEvent(tc.event, payload)
			if err != nil {
				t.Fatalf("MapHookEvent: %v", err)
			}
			if tc.want.drop {
				if sig != nil {
					t.Fatalf("expected drop, got %+v", sig)
				}
				return
			}
			if sig == nil {
				t.Fatal("expected signal, got nil")
			}
			if (sig.Status == nil) != (tc.want.status == nil) {
				t.Fatalf("status presence: got %v want %v", sig.Status, tc.want.status)
			}
			if sig.Status != nil && *sig.Status != *tc.want.status {
				t.Errorf("status: got %q want %q", *sig.Status, *tc.want.status)
			}
			if sig.Activity != tc.want.activity {
				t.Errorf("activity: got %v want %v", sig.Activity, tc.want.activity)
			}
			if sig.Tool != tc.want.tool {
				t.Errorf("tool: got %q want %q", sig.Tool, tc.want.tool)
			}
			if sig.SessionID != tc.want.sessionID {
				t.Errorf("session: got %q want %q", sig.SessionID, tc.want.sessionID)
			}
			if sig.ParentSessionID != "" {
				t.Errorf("ParentSessionID: got %q want empty", sig.ParentSessionID)
			}
			if sig.Raw == nil {
				t.Error("Raw is nil")
			} else if sig.Raw["hook_event_name"] != payload["hook_event_name"] {
				t.Errorf("Raw mismatch: %v", sig.Raw)
			}
			if sig.At.IsZero() {
				t.Error("At is zero")
			}
		})
	}
}

func TestMapHookEvent_PreToolUseMissingTool(t *testing.T) {
	payload := map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      "sess-1",
	}
	sig, err := MapHookEvent("PreToolUse", payload)
	if err != nil {
		t.Fatalf("MapHookEvent: %v", err)
	}
	if sig == nil {
		t.Fatal("expected signal, got nil")
	}
	if sig.Tool != "" {
		t.Errorf("Tool: got %q want empty", sig.Tool)
	}
	if !sig.Activity {
		t.Error("Activity: want true")
	}
}

func TestMapHookEvent_RawRoundTrip(t *testing.T) {
	payload := loadFixture(t, "pre_tool_use.json")
	sig, err := MapHookEvent("PreToolUse", payload)
	if err != nil || sig == nil {
		t.Fatalf("MapHookEvent: %v %v", sig, err)
	}
	if sig.Raw["hook_event_name"] != payload["hook_event_name"] {
		t.Errorf("Raw hook_event_name mismatch")
	}
	if sig.Raw["tool_name"] != payload["tool_name"] {
		t.Errorf("Raw tool_name mismatch")
	}
	if sig.Raw["session_id"] != payload["session_id"] {
		t.Errorf("Raw session_id mismatch")
	}
}

func TestNormalizeTool_ViaHub(t *testing.T) {
	h, err := agentstatus.NewHub(agentstatus.HubConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()
	stream := h.Events()

	payload := []byte(`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"bash"}`)
	if err := h.Ingest(agentstatus.Codex, payload); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	select {
	case ev := <-stream.Channel():
		if ev.Tool != "Bash" {
			t.Errorf("Tool: got %q want %q", ev.Tool, "Bash")
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestMapHookEvent_MissingFieldsTolerated(t *testing.T) {
	sig, err := MapHookEvent("Stop", map[string]any{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sig == nil || sig.Status == nil || *sig.Status != agentstatus.StatusIdle {
		t.Errorf("got %+v", sig)
	}
}
