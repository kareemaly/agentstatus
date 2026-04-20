package agentstatus

// Stream is the public handle returned by Hub.Events. It wraps an independent
// broadcast subscriber channel. v0.1.0 exposes only Channel(); transform and
// terminal combinators (Filter, Map, Debounce, Throttle, Fanout, …) land in a
// follow-up ticket.
type Stream struct {
	ch <-chan Event
}

// Channel returns the underlying receive-only Event channel. The channel is
// closed when the Hub that produced this Stream is closed.
func (s Stream) Channel() <-chan Event {
	return s.ch
}
