// Package pipeline holds optional predicate and combinator helpers for
// composing Event streams (ByAgent, BySession, ByStatus, ByTag, And, Or,
// Not, IdleToWorking, AnyAwaitingInput).
//
// The root agentstatus package re-exports the most commonly used
// helpers; this subpackage exists for users who prefer a narrower
// top-level symbol set.
package pipeline
