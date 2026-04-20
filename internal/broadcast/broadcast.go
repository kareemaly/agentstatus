package broadcast

import (
	"sync"
	"sync/atomic"
)

// Broadcaster fans out values of type T to independent bounded subscribers.
// Publish is non-blocking: when a subscriber's buffer is full, the oldest
// value in that subscriber's buffer is discarded and its drop counter is
// incremented.
//
// Broadcaster is safe for concurrent use.
type Broadcaster[T any] struct {
	bufferSize int

	mu     sync.Mutex
	subs   map[int]*subscriber[T]
	nextID int
	closed bool
}

type subscriber[T any] struct {
	ch    chan T
	drops atomic.Int64
}

// New creates a broadcaster that gives each subscriber a buffer of the given
// size. A non-positive bufferSize is clamped to 1 to keep semantics sane.
func New[T any](bufferSize int) *Broadcaster[T] {
	if bufferSize < 1 {
		bufferSize = 1
	}
	return &Broadcaster[T]{
		bufferSize: bufferSize,
		subs:       make(map[int]*subscriber[T]),
	}
}

// Subscribe registers a new subscriber and returns its id, receive-only
// channel, and a pointer to its drops counter. If the broadcaster is already
// closed, the returned channel is a pre-closed channel and Unsubscribe is a
// no-op for the returned id.
func (b *Broadcaster[T]) Subscribe() (id int, ch <-chan T, drops *atomic.Int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		closedCh := make(chan T)
		close(closedCh)
		var d atomic.Int64
		return -1, closedCh, &d
	}

	id = b.nextID
	b.nextID++
	s := &subscriber[T]{ch: make(chan T, b.bufferSize)}
	b.subs[id] = s
	return id, s.ch, &s.drops
}

// Unsubscribe removes the subscriber with the given id and closes its
// channel. Calling Unsubscribe on an already-removed id is a no-op.
func (b *Broadcaster[T]) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	s, ok := b.subs[id]
	if !ok {
		return
	}
	delete(b.subs, id)
	close(s.ch)
}

// Publish fans v out to every subscriber. For each subscriber, if the buffer
// has room the value is delivered; otherwise the oldest buffered value is
// dropped (and the subscriber's drop counter is incremented) and v takes its
// place.
//
// Publish on a closed broadcaster is a no-op, so producers racing with Close
// don't panic.
func (b *Broadcaster[T]) Publish(v T) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	for _, s := range b.subs {
		deliver(s, v)
	}
}

func deliver[T any](s *subscriber[T], v T) {
	select {
	case s.ch <- v:
		return
	default:
	}
	// Buffer full: drop oldest, then send. The drain select is non-blocking
	// so a reader racing with us can't cause a deadlock; in the rare case the
	// reader drained between our full-check and the drain we simply fall
	// through and the send below succeeds.
	select {
	case <-s.ch:
		s.drops.Add(1)
	default:
	}
	select {
	case s.ch <- v:
	default:
		// Still no room (another racing publisher filled the slot). Count a
		// drop on v itself rather than spin.
		s.drops.Add(1)
	}
}

// Close marks the broadcaster closed, closes every subscriber channel, and
// prevents further Publishes. Close is idempotent.
func (b *Broadcaster[T]) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	for id, s := range b.subs {
		close(s.ch)
		delete(b.subs, id)
	}
}
