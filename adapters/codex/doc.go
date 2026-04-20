// Package codex provides the built-in adapter for Codex.
//
// It maps Codex hook events (SessionStart, UserPromptSubmit, PreToolUse,
// PostToolUse, Stop) into agentstatus Signals via Codex's experimental
// hooks.json mechanism, and installs/uninstalls inline curl entries in
// ~/.codex/hooks.json.
//
// Hooks require [features] codex_hooks = true in ~/.codex/config.toml.
// The installer warns if that flag is not detected but does not modify
// config.toml.
//
// Import for side effects to register the adapter:
//
//	import _ "github.com/kareemaly/agentstatus/adapters/codex"
package codex
