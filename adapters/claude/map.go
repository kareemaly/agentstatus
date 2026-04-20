package claude

import (
	"time"

	agentstatus "github.com/kareemaly/agentstatus"
)

// droppedByDesign is the set of events explicitly not mapped to a status
// signal. A hook event maps to a signal only when it represents a change in
// what Claude is doing. Events that carry no such implication are dropped:
//
//   - Environment events (InstructionsLoaded, ConfigChange, CwdChanged,
//     FileChanged, WorktreeCreate, WorktreeRemove, PreCompact, PostCompact):
//     the environment around Claude changed, not Claude itself.
//   - Agent-team workflow events (TaskCreated, TaskCompleted, TeammateIdle):
//     teammates share session_id with the orchestrating session; per-teammate
//     identity modeling is out of scope for v0.1.
var droppedByDesign = map[string]struct{}{
	// Environment events: no Claude-state change
	"InstructionsLoaded": {},
	"ConfigChange":       {},
	"CwdChanged":         {},
	"FileChanged":        {},
	"WorktreeCreate":     {},
	"WorktreeRemove":     {},
	"PreCompact":         {},
	"PostCompact":        {},
	// Agent-team workflow: teammates share session_id; no per-teammate model in v0.1
	"TaskCreated":   {},
	"TaskCompleted": {},
	"TeammateIdle":  {},
}

// MapHookEvent translates a single Claude Code hook payload into a Signal.
//
// Returning (nil, nil) means "drop silently" — used for events in
// droppedByDesign, for Notification types that carry no status implication
// (auth_success, unknown types), and for unrecognised event names.
// MapHookEvent never returns an error today; the signature reserves the seam
// for adapters that need to surface parse failures separately from drops.
func MapHookEvent(event string, payload map[string]any) (*agentstatus.Signal, error) {
	if _, drop := droppedByDesign[event]; drop {
		return nil, nil
	}

	// Claude emits agent_id on every hook fired within a subagent context.
	// Whenever agent_id is non-empty it is the true session identifier and the
	// outer session_id becomes the parent. This applies to all event types, not
	// only SubagentStart/SubagentStop.
	sessionID := getString(payload, "session_id")
	parentSessionID := ""
	if agentID := getString(payload, "agent_id"); agentID != "" {
		parentSessionID = sessionID
		sessionID = agentID
	}
	at := getTime(payload)

	base := func(s *agentstatus.Status, activity bool) *agentstatus.Signal {
		return &agentstatus.Signal{
			At:              at,
			Activity:        activity,
			Status:          s,
			SessionID:       sessionID,
			ParentSessionID: parentSessionID,
			Raw:             payload,
		}
	}

	withTool := func(s *agentstatus.Signal) *agentstatus.Signal {
		s.Tool = getString(payload, "tool_name")
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

	case "StopFailure":
		// Fires instead of Stop when the turn ends due to an API error.
		// The error field (rate_limit, authentication_failed, etc.) is preserved
		// in Raw but not promoted to a typed field.
		s := agentstatus.StatusError
		return base(&s, false), nil

	case "Notification":
		// Dispatch on notification_type: only the three prompt-like types
		// imply a status change. auth_success is an env event (drop). Unknown
		// future types are also dropped for safety.
		switch getString(payload, "notification_type") {
		case "permission_prompt", "idle_prompt", "elicitation_dialog":
			s := agentstatus.StatusAwaitingInput
			return base(&s, false), nil
		default:
			return nil, nil
		}

	case "PermissionRequest":
		s := agentstatus.StatusAwaitingInput
		return base(&s, false), nil

	case "PermissionDenied":
		// Auto-mode classifier denied the tool call. The model typically
		// retries; treat as activity rather than idle or awaiting.
		return withTool(base(nil, true)), nil

	case "Elicitation":
		// MCP server requesting user input mid-task — same semantics as
		// PermissionRequest.
		s := agentstatus.StatusAwaitingInput
		return base(&s, false), nil

	case "ElicitationResult":
		// User responded to an elicitation; Claude resumes work.
		return base(nil, true), nil

	case "SubagentStart":
		s := agentstatus.StatusStarting
		return base(&s, false), nil

	case "SubagentStop":
		s := agentstatus.StatusIdle
		return base(&s, false), nil

	case "SessionEnd":
		s := agentstatus.StatusEnded
		return base(&s, false), nil

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
