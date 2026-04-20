package codex

import (
	"time"

	agentstatus "github.com/kareemaly/agentstatus"
)

// MapHookEvent translates a single Codex hooks.json payload into a Signal.
// Returns (nil, nil) for unknown events (silent drop), consistent with Claude.
//
// Session identity: Codex payloads carry session_id (the thread id). No agent_id
// equivalent is currently documented; subagents are opaque to hooks.
// ParentSessionID is always empty until Codex exposes subagent identity.
//
// Timestamp: Codex payloads carry no At field. time.Now() is used as fallback,
// same as Claude.
func MapHookEvent(event string, payload map[string]any) (*agentstatus.Signal, error) {
	sessionID, _ := payload["session_id"].(string)
	at := time.Now()

	base := func(s *agentstatus.Status, activity bool) *agentstatus.Signal {
		return &agentstatus.Signal{
			At:              at,
			Activity:        activity,
			Status:          s,
			SessionID:       sessionID,
			ParentSessionID: "",
			Raw:             payload,
		}
	}

	switch event {
	case "SessionStart":
		s := agentstatus.StatusStarting
		return base(&s, false), nil

	case "UserPromptSubmit":
		return base(nil, true), nil

	case "PreToolUse":
		sig := base(nil, true)
		sig.Tool, _ = payload["tool_name"].(string)
		return sig, nil

	case "PostToolUse":
		sig := base(nil, true)
		sig.Tool, _ = payload["tool_name"].(string)
		return sig, nil

	case "Stop":
		s := agentstatus.StatusIdle
		return base(&s, false), nil

	default:
		return nil, nil
	}
}
