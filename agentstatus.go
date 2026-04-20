package agentstatus

import "time"

// Agent identifies a coding-agent family (Claude Code, Codex, OpenCode, or a
// custom adapter). Built-in agent names are declared as constants below; any
// string is a valid Agent, so third-party adapters can define their own.
type Agent string

const (
	Claude   Agent = "claude"
	Codex    Agent = "codex"
	OpenCode Agent = "opencode"
)

// Status is the normalized lifecycle state of a single agent session.
type Status string

const (
	StatusStarting      Status = "starting"
	StatusWorking       Status = "working"
	StatusIdle          Status = "idle"
	StatusAwaitingInput Status = "awaiting_input"
	StatusError         Status = "error"
	StatusEnded         Status = "ended"
)

// Event is the public, cross-agent unit of the library's output stream. Each
// Event represents a status transition for a single session. See specs/design.md
// for field semantics.
type Event struct {
	Agent           Agent
	SessionID       string
	ParentSessionID string
	Status          Status
	PrevStatus      Status
	Tool            string
	Work            string
	At              time.Time
	Tags            map[string]string
	Raw             map[string]any
}

// DropPolicy controls how bounded buffers behave when full. Only DropOldest is
// implemented in v0.1.0; the type is kept open so additional policies can be
// added without an API break.
type DropPolicy int

const (
	// DropOldest discards the oldest buffered value to make room for a new one.
	DropOldest DropPolicy = iota
)
