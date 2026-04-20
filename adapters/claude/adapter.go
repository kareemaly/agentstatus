package claude

import (
	agentstatus "github.com/kareemaly/agentstatus"
)

// Adapter is the registered Claude Code adapter. Imported for side effects
// from package init().
//
// Caveats (per specs/design.md §"Known gaps"):
//
//   - Auto-approved tools: when a user has pre-approved a tool, Claude does
//     not fire PermissionRequest, so the "awaiting_input" status will be rarer
//     than for users running with default permissions.
//   - "Thinking" gap: between UserPromptSubmit and the next hook event no
//     status signal fires. Status remains "working" (inferred from
//     UserPromptSubmit) until PreToolUse or Stop. This is acceptable.
//   - Subagent identity: per the Claude hooks schema, SubagentStart and
//     SubagentStop fire under the parent session's `session_id` and carry the
//     subagent's stable id in `agent_id`. We model the subagent as an
//     independent session: emitted Event.SessionID = agent_id, and
//     Event.ParentSessionID = parent's session_id.
var Adapter = agentstatus.Adapter{
	Name:           agentstatus.Claude,
	MapHookEvent:   MapHookEvent,
	InstallHooks:   installHooks,
	UninstallHooks: uninstallHooks,
}

func init() {
	if err := agentstatus.RegisterAdapter(Adapter); err != nil {
		// Registry collisions during init mean a programming error in the
		// importing binary (double-import of this package is impossible; a
		// duplicate Name in another adapter is the only way). Panic so the
		// binary fails fast at startup.
		panic(err)
	}
}
