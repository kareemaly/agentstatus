// Package broadcast is an internal fan-out primitive used by the Hub to
// dispatch Events to independent, per-subscriber buffered channels with
// a configurable drop policy.
package broadcast
