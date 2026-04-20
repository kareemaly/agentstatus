package opencode

import (
	"time"

	"github.com/kareemaly/agentstatus"
)

var droppedByDesign = map[string]string{
	"session.updated":               "session metadata changed, not agent state",
	"session.deleted":               "lifecycle end, no status signal needed",
	"session.diff":                  "internal diff, not agent state",
	"session.compacted":             "internal housekeeping",
	"message.updated":               "too noisy — full message churn",
	"message.removed":               "too noisy",
	"message.part.updated":          "too noisy — per-token content churn",
	"message.part.removed":          "too noisy",
	"message.part.delta":            "too noisy — per-character streaming",
	"permission.replied":            "downstream of awaiting_input, no status change",
	"file.edited":                   "environment change, not agent state",
	"todo.updated":                  "environment change",
	"command.executed":              "environment change",
	"installation.update-available": "internal version check, not agent state",
	"server.instance.disposed":      "shutdown signal, not agent state",
}

func MapHookEvent(event string, payload map[string]any) (*agentstatus.Signal, error) {
	if _, ok := droppedByDesign[event]; ok {
		return nil, nil
	}

	// Extract the actual event properties from the payload wrapper.
	// The payload may be the full wrapper object with "payload" key, or just the properties.
	var props map[string]any
	if p, ok := payload["payload"].(map[string]any); ok {
		props = p
	} else {
		props = payload
	}

	var sig *agentstatus.Signal

	switch event {
	case "session.created":
		status := agentstatus.StatusStarting
		sig = &agentstatus.Signal{
			At:              getTime(props),
			Status:          &status,
			SessionID:       resolveSessionID(payload, props),
			ParentSessionID: resolveParentSessionID(payload, props),
			Raw:             payload,
		}

	case "session.status":
		sig = &agentstatus.Signal{
			At:        getTime(props),
			Activity:  true,
			SessionID: resolveSessionID(payload, props),
			Raw:       payload,
		}

	case "session.idle":
		status := agentstatus.StatusIdle
		sig = &agentstatus.Signal{
			At:        getTime(props),
			Status:    &status,
			SessionID: resolveSessionID(payload, props),
			Raw:       payload,
		}

	case "permission.asked":
		status := agentstatus.StatusAwaitingInput
		sig = &agentstatus.Signal{
			At:        getTime(props),
			Status:    &status,
			SessionID: resolveSessionID(payload, props),
			Raw:       payload,
		}

	case "session.error":
		status := agentstatus.StatusError
		sig = &agentstatus.Signal{
			At:        getTime(props),
			Status:    &status,
			SessionID: resolveSessionID(payload, props),
			Raw:       payload,
		}

	case "tool.execute.before", "tool.execute.after":
		sig = &agentstatus.Signal{
			At:        getTime(props),
			Activity:  true,
			SessionID: resolveSessionID(payload, props),
			Tool:      getString(props, "tool"),
			Raw:       payload,
		}

	default:
		return nil, nil
	}

	return sig, nil
}

// resolveSessionID prefers the wrapper's top-level "session_id"; falls back to
// inner props "sessionID". Tool hook payloads only populate the wrapper field.
func resolveSessionID(wrapper, inner map[string]any) string {
	if s := getString(wrapper, "session_id"); s != "" {
		return s
	}
	return getString(inner, "sessionID")
}

// resolveParentSessionID prefers the wrapper's top-level "parent_session_id";
// falls back to inner props info.parentID (session.created shape).
func resolveParentSessionID(wrapper, inner map[string]any) string {
	if s := getString(wrapper, "parent_session_id"); s != "" {
		return s
	}
	if info, ok := inner["info"].(map[string]any); ok {
		return getString(info, "parentID")
	}
	return ""
}

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	return ""
}

func getTime(payload map[string]any) time.Time {
	if payload == nil {
		return time.Now()
	}
	if ts, ok := payload["timestamp"]; ok {
		if s, ok := ts.(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t
			}
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
		}
		if f, ok := ts.(float64); ok {
			return time.Unix(int64(f), 0)
		}
	}
	return time.Now()
}
