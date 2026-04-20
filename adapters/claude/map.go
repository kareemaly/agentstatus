package claude

import (
	"time"

	agentstatus "github.com/kareemaly/agentstatus"
)

// MapHookEvent translates a single Claude Code hook payload into a Signal.
//
// Returning (nil, nil) means "drop silently" — used for unknown event names
// and for metadata-only events (PreCompact). MapHookEvent never returns an
// error today; the signature reserves the seam for adapters that need to
// surface parse failures separately from drops.
func MapHookEvent(event string, payload map[string]any) (*agentstatus.Signal, error) {
	sessionID := getString(payload, "session_id")
	at := getTime(payload)

	base := func(s *agentstatus.Status, activity bool) *agentstatus.Signal {
		return &agentstatus.Signal{
			At:        at,
			Activity:  activity,
			Status:    s,
			SessionID: sessionID,
			Raw:       payload,
		}
	}

	withTool := func(s *agentstatus.Signal) *agentstatus.Signal {
		s.Tool = getString(payload, "tool_name")
		return s
	}

	// subagentSession rebinds the session ids for SubagentStart/SubagentStop:
	// per the Claude hooks schema the subagent's own identity is `agent_id`,
	// while the parent session keeps emitting under its own `session_id`.
	subagentSession := func(s *agentstatus.Signal) *agentstatus.Signal {
		s.SessionID = getString(payload, "agent_id")
		s.ParentSessionID = sessionID
		return s
	}

	switch event {
	case "SessionStart":
		s := agentstatus.StatusStarting
		return base(&s, false), nil

	case "UserPromptSubmit":
		return base(nil, true), nil

	case "PreToolUse":
		return withTool(base(nil, true)), nil

	case "PostToolUse":
		return base(nil, true), nil

	case "PostToolUseFailure":
		s := agentstatus.StatusError
		return withTool(base(&s, false)), nil

	case "Stop":
		s := agentstatus.StatusIdle
		return base(&s, false), nil

	case "Notification":
		s := agentstatus.StatusAwaitingInput
		return base(&s, false), nil

	case "PermissionRequest":
		s := agentstatus.StatusAwaitingInput
		return base(&s, false), nil

	case "SubagentStart":
		s := agentstatus.StatusStarting
		return subagentSession(base(&s, false)), nil

	case "SubagentStop":
		s := agentstatus.StatusIdle
		return subagentSession(base(&s, false)), nil

	case "SessionEnd":
		s := agentstatus.StatusEnded
		return base(&s, false), nil

	case "PreCompact":
		// Metadata only; no status change.
		return nil, nil

	default:
		// Unknown event — log-and-drop.
		return nil, nil
	}
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// getTime resolves the wall-clock time for a Claude payload. Claude does not
// emit a timestamp on every hook today; we accept "timestamp" as either
// RFC3339 string or Unix-seconds number, and otherwise fall back to
// time.Now() so downstream consumers always see a non-zero At.
func getTime(m map[string]any) time.Time {
	switch v := m["timestamp"].(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	case float64:
		secs := int64(v)
		nsecs := int64((v - float64(secs)) * 1e9)
		return time.Unix(secs, nsecs)
	}
	return time.Now()
}
