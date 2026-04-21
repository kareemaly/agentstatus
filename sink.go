package agentstatus

import "context"

// Sink is the consumer-facing delivery abstraction. Sinks receive Events from
// a Hub via AttachSink and are responsible for their own durability,
// buffering, and retry semantics. Implementations must be safe for concurrent
// use.
//
// Send should not block: the Hub's forwarder goroutine calls Send for each
// Event, and a slow Sink throttles only its own subscriber buffer (drop-oldest
// per the broadcaster). Sinks that perform I/O should enqueue the event onto
// an internal channel and return immediately.
type Sink interface {
	// Send delivers a single Event. A non-nil return is routed through
	// HubConfig.ErrorHandler; the forwarder continues running.
	Send(ctx context.Context, e Event) error
	// Name is a stable identifier used for diagnostics.
	Name() string
}

// AttachSink subscribes to the Hub's Event stream and forwards every Event to
// s in a dedicated background goroutine. The goroutine exits when the Hub is
// closed (which closes the underlying subscriber channel). Errors returned by
// s.Send are routed through HubConfig.ErrorHandler.
//
// Hub.Close blocks until all forwarder goroutines started by AttachSink have
// drained their subscriber channels into their respective Sinks, so consumers
// that call Sink.Close after Hub.Close are guaranteed every broadcast Event
// has been handed off before the Sink drains.
//
// AttachSink itself never blocks. Each attached Sink has an independent
// subscriber buffer; one slow Sink does not affect others or the Hub itself.
func (h *Hub) AttachSink(s Sink) {
	stream := h.Events()
	h.sinks.Add(1)
	go func() {
		defer h.sinks.Done()
		ctx := context.Background()
		for e := range stream.Channel() {
			if err := s.Send(ctx, e); err != nil {
				h.errH(err)
			}
		}
	}()
}
