package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentstatus "github.com/kareemaly/agentstatus"
)

func loadFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "claude", name)
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

	type want struct {
		drop      bool
		status    *agentstatus.Status
		activity  bool
		tool      string
		parent    string
		sessionID string
	}

	starting := agentstatus.StatusStarting
	idle := agentstatus.StatusIdle
	awaiting := agentstatus.StatusAwaitingInput
	errSt := agentstatus.StatusError
	ended := agentstatus.StatusEnded

	cases := []struct {
		fixture string
		event   string
		want    want
	}{
		{"session_start.json", "SessionStart", want{status: &starting, sessionID: "sess-1"}},
		{"user_prompt_submit.json", "UserPromptSubmit", want{activity: true, sessionID: "sess-1"}},
		{"pre_tool_use_read.json", "PreToolUse", want{activity: true, tool: "Read", sessionID: "sess-1"}},
		{"post_tool_use.json", "PostToolUse", want{activity: true, sessionID: "sess-1"}},
		{"post_tool_use_failure.json", "PostToolUseFailure", want{status: &errSt, tool: "Bash", sessionID: "sess-1"}},
		{"stop.json", "Stop", want{status: &idle, sessionID: "sess-1"}},
		{"notification.json", "Notification", want{status: &awaiting, sessionID: "sess-1"}},
		{"permission_request.json", "PermissionRequest", want{status: &awaiting, sessionID: "sess-1"}},
		{"subagent_start.json", "SubagentStart", want{status: &starting, sessionID: "agent-abc123", parent: "parent-1"}},
		{"subagent_stop.json", "SubagentStop", want{status: &idle, sessionID: "agent-abc123", parent: "parent-1"}},
		{"session_end.json", "SessionEnd", want{status: &ended, sessionID: "sess-1"}},
		{"pre_compact.json", "PreCompact", want{drop: true}},
		{"unknown_event.json", "NonExistent", want{drop: true}},
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
			if sig.ParentSessionID != tc.want.parent {
				t.Errorf("parent: got %q want %q", sig.ParentSessionID, tc.want.parent)
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

func TestMapHookEvent_TimestampRFC3339(t *testing.T) {
	payload := map[string]any{
		"hook_event_name": "Stop",
		"session_id":      "s1",
		"timestamp":       "2025-01-02T03:04:05Z",
	}
	sig, err := MapHookEvent("Stop", payload)
	if err != nil || sig == nil {
		t.Fatalf("map: %v %v", sig, err)
	}
	if sig.At.Year() != 2025 || sig.At.Month() != 1 || sig.At.Day() != 2 {
		t.Errorf("At: got %v", sig.At)
	}
}

func TestMapHookEvent_TimestampNumeric(t *testing.T) {
	payload := map[string]any{
		"hook_event_name": "Stop",
		"session_id":      "s1",
		"timestamp":       float64(1700000000),
	}
	sig, err := MapHookEvent("Stop", payload)
	if err != nil || sig == nil {
		t.Fatalf("map: %v %v", sig, err)
	}
	if sig.At.Unix() != 1700000000 {
		t.Errorf("At unix: got %d", sig.At.Unix())
	}
}

func TestMapHookEvent_MissingFieldsTolerated(t *testing.T) {
	// Empty payload — must not panic, must not error. Status pointer still
	// honored for the event.
	sig, err := MapHookEvent("Stop", map[string]any{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sig == nil || sig.Status == nil || *sig.Status != agentstatus.StatusIdle {
		t.Errorf("got %+v", sig)
	}
}

func TestMapHookEvent_AgentIDPropagation(t *testing.T) {
	t.Parallel()

	const parentID = "parent-sess"
	const agentID = "subagent-xyz"

	cases := []struct {
		name    string
		event   string
		payload map[string]any
		wantSID string
		wantPID string
	}{
		{
			name:  "PreToolUse with agent_id",
			event: "PreToolUse",
			payload: map[string]any{
				"hook_event_name": "PreToolUse",
				"session_id":      parentID,
				"agent_id":        agentID,
				"tool_name":       "Read",
			},
			wantSID: agentID,
			wantPID: parentID,
		},
		{
			name:  "PostToolUse with agent_id",
			event: "PostToolUse",
			payload: map[string]any{
				"hook_event_name": "PostToolUse",
				"session_id":      parentID,
				"agent_id":        agentID,
			},
			wantSID: agentID,
			wantPID: parentID,
		},
		{
			name:  "Stop with agent_id",
			event: "Stop",
			payload: map[string]any{
				"hook_event_name": "Stop",
				"session_id":      parentID,
				"agent_id":        agentID,
			},
			wantSID: agentID,
			wantPID: parentID,
		},
		{
			name:  "PreToolUse without agent_id",
			event: "PreToolUse",
			payload: map[string]any{
				"hook_event_name": "PreToolUse",
				"session_id":      "sess-only",
				"tool_name":       "Bash",
			},
			wantSID: "sess-only",
			wantPID: "",
		},
		{
			name:  "empty string agent_id treated as absent",
			event: "Stop",
			payload: map[string]any{
				"hook_event_name": "Stop",
				"session_id":      "sess-only",
				"agent_id":        "",
			},
			wantSID: "sess-only",
			wantPID: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig, err := MapHookEvent(tc.event, tc.payload)
			if err != nil {
				t.Fatalf("MapHookEvent: %v", err)
			}
			if sig == nil {
				t.Fatal("expected signal, got nil")
			}
			if sig.SessionID != tc.wantSID {
				t.Errorf("SessionID: got %q want %q", sig.SessionID, tc.wantSID)
			}
			if sig.ParentSessionID != tc.wantPID {
				t.Errorf("ParentSessionID: got %q want %q", sig.ParentSessionID, tc.wantPID)
			}
		})
	}
}
