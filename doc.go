// Package agentstatus is a Go library for real-time status detection of
// coding agents (Claude Code, Codex, OpenCode, and custom adapters).
//
// It consolidates native hook events from multiple agents into a single
// in-process Hub, normalizes them into Events with a small set of Statuses
// (working, idle, awaiting_input, error, ended), and exposes a fluent
// Stream pipeline with pluggable sinks for delivery.
//
// Use [InstallHooks] and [UninstallHooks] to wire or unwire the relevant
// entries in each agent's native config file — e.g. ~/.claude/settings.json
// for Claude Code — so the agent forwards hook events to a running Hub.
//
// See specs/design.md for the full design.
package agentstatus
