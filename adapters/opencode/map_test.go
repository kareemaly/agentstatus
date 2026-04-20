package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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

	type want struct {
		drop      bool
		status    *agentstatus.Status
		activity  bool
		tool      string
		sessionID string
		parentID  string
	}

	starting := agentstatus.StatusStarting
	idle := agentstatus.StatusIdle
	awaiting := agentstatus.StatusAwaitingInput
	errSt := agentstatus.StatusError

	cases := []struct {
		fixture string
		event   string
		want    want
	}{
		{"session_created.json", "session.created", want{status: &starting, sessionID: "ses_25493201cffetSRyGKrGBfTh1A"}},
		{"session_created_with_parent.json", "session.created", want{status: &starting, sessionID: "ses_child01cffetSRyGKrGBfTh1A", parentID: "ses_25493201cffetSRyGKrGBfTh1A"}},
		{"session_created_no_info.json", "session.created", want{status: &starting, sessionID: "ses_noinfo01cffetSRyGKrGBfTh"}},
		{"session_status.json", "session.status", want{activity: true, sessionID: "s1"}},
		{"session_idle.json", "session.idle", want{status: &idle, sessionID: "s1"}},
		{"permission_asked.json", "permission.asked", want{status: &awaiting, sessionID: "s1"}},
		{"session_error.json", "session.error", want{status: &errSt, sessionID: "s1"}},
		{"tool_execute_before.json", "tool.execute.before", want{activity: true, sessionID: "s1", tool: "bash"}},
		{"tool_execute_after.json", "tool.execute.after", want{activity: true, sessionID: "s1", tool: "bash"}},
		{"dropped_message_delta.json", "message.part.delta", want{drop: true}},
		{"installation_update_available.json", "installation.update-available", want{drop: true}},
		{"server_instance_disposed.json", "server.instance.disposed", want{drop: true}},
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
				t.Fatalf("status: got %q want %q", *sig.Status, *tc.want.status)
			}
			if sig.Activity != tc.want.activity {
				t.Fatalf("activity: got %v want %v", sig.Activity, tc.want.activity)
			}
			if sig.Tool != tc.want.tool {
				t.Fatalf("tool: got %q want %q", sig.Tool, tc.want.tool)
			}
			if sig.SessionID != tc.want.sessionID {
				t.Fatalf("sessionID: got %q want %q", sig.SessionID, tc.want.sessionID)
			}
			if sig.ParentSessionID != tc.want.parentID {
				t.Fatalf("parentSessionID: got %q want %q", sig.ParentSessionID, tc.want.parentID)
			}
			if sig.Raw == nil {
				t.Fatal("Raw is nil")
			}
			if sig.At.IsZero() {
				t.Fatal("At is zero")
			}
		})
	}
}

func TestMapHookEvent_UnknownEvent(t *testing.T) {
	payload := map[string]any{"sessionID": "s1"}
	sig, err := MapHookEvent("unknown.event", payload)
	if err != nil {
		t.Fatalf("MapHookEvent: %v", err)
	}
	if sig != nil {
		t.Fatalf("expected nil for unknown event, got %+v", sig)
	}
}

func TestMapHookEvent_MissingSessionID(t *testing.T) {
	payload := map[string]any{}
	sig, err := MapHookEvent("session.status", payload)
	if err != nil {
		t.Fatalf("MapHookEvent: %v", err)
	}
	if sig == nil {
		t.Fatal("expected signal")
	}
	if sig.SessionID != "" {
		t.Fatalf("sessionID: got %q want empty", sig.SessionID)
	}
}

func TestMapHookEvent_RawPreserved(t *testing.T) {
	payload := loadFixture(t, "session_status.json")
	sig, err := MapHookEvent("session.status", payload)
	if err != nil {
		t.Fatalf("MapHookEvent: %v", err)
	}
	if sig == nil {
		t.Fatal("expected signal")
	}
	// Verify raw contains the parsed payload
	payloadFromRaw, ok := sig.Raw["payload"].(map[string]any)
	if !ok {
		t.Fatalf("Raw does not contain payload as map: %+v", sig.Raw)
	}
	if sessionID, ok := payloadFromRaw["sessionID"].(string); !ok || sessionID != "s1" {
		t.Fatalf("payload.sessionID in Raw: got %q", sessionID)
	}
}
