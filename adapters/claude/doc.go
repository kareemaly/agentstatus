// Package claude provides the built-in adapter for Claude Code.
//
// It maps Claude's native hook events (SessionStart, PreToolUse, Stop,
// Notification, etc.) into agentstatus Signals, and installs/uninstalls
// hook entries in ~/.claude/settings.json (or a project-level
// .claude/settings.json when requested).
//
// Import for side effects to register the adapter:
//
//	import _ "github.com/kareemaly/agentstatus/adapters/claude"
package claude
