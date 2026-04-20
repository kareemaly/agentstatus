// Package opencode provides the built-in adapter for OpenCode.
//
// It maps OpenCode plugin events (session.created, session.status,
// session.idle, permission.asked, session.error) into agentstatus
// Signals, and installs/uninstalls the generated TypeScript plugin at
// ~/.config/opencode/plugins/agentstatus.ts.
//
// Import for side effects to register the adapter:
//
//	import _ "github.com/kareemaly/agentstatus/adapters/opencode"
package opencode
