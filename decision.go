package agentstatus

import "time"

// Signal is the internal per-session input to the decision machine. Adapters
// translate native hook payloads into Signals; Hub feeds them through Decide.
//
// Signal carries no Agent field: the Agent is known at the adapter/ingest seam
// and attached to the emitted Event there.
//
// Raw is the original hook payload. Decide does not read or write it; the
// field flows around the decision machine into the emitted Event.Raw as a
// design-mandated escape hatch for consumers that need provider-specific
// fields not surfaced on Event.
type Signal struct {
	At              time.Time
	Activity        bool
	Status          *Status
	Tool            string
	Work            string
	SessionID       string
	ParentSessionID string
	Raw             map[string]any
}

// Transition is the internal return value of Decide. It is emitted when the
// pair (Status, Tool) changes; duplicates are suppressed.
type Transition struct {
	Status     Status
	PrevStatus Status
	Tool       string
}

// sessionState is the per-session state the decision machine carries between
// Signals. Unexported by design: only Hub constructs and stores these.
type sessionState struct {
	Status Status
	Tool   string
}

// Decide is a pure function from (state, signal) to (newState, *Transition).
//
// Rules:
//  1. If sig.Status != nil, it is authoritative (overrides Activity inference).
//  2. Else if sig.Activity, the inferred status is StatusWorking.
//  3. Else no candidate — state is returned unchanged, Transition is nil.
//  4. If the candidate (Status, Tool) pair equals the current state pair, the
//     duplicate is suppressed: state is returned unchanged, Transition is nil.
//  5. Otherwise the new state is returned along with a Transition. PrevStatus
//     is the previous Status regardless of whether the change was driven by a
//     status change, a tool change, or both.
//
// Decide has no I/O, no globals, no clock. The wall-clock time lives in
// sig.At and is the caller's responsibility.
func Decide(state sessionState, sig Signal) (sessionState, *Transition) {
	var candidate Status
	switch {
	case sig.Status != nil:
		candidate = *sig.Status
	case sig.Activity:
		candidate = StatusWorking
	default:
		return state, nil
	}

	if candidate == state.Status && sig.Tool == state.Tool {
		return state, nil
	}

	return sessionState{Status: candidate, Tool: sig.Tool}, &Transition{
		Status:     candidate,
		PrevStatus: state.Status,
		Tool:       sig.Tool,
	}
}
