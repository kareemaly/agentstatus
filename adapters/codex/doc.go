// Package codex provides the built-in adapter for Codex.
//
// It maps Codex notify events (session_meta, task_started, task_complete,
// permission, error) into agentstatus Signals, and installs/uninstalls
// the notify bridge script at ~/.agentstatus/codex-bridge.sh plus the
// corresponding entry in ~/.codex/config.toml.
//
// Import for side effects to register the adapter:
//
//	import _ "github.com/kareemaly/agentstatus/adapters/codex"
package codex
