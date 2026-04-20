// Package funcsink provides a sink that wraps an arbitrary
// func(context.Context, Event) error, useful for ad-hoc delivery logic
// or adapting Events into a consumer's own event bus.
package funcsink
