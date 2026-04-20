package agentstatus

import (
	"io"
	"log/slog"
	"maps"
	"sync"

	"github.com/kareemaly/agentstatus/internal/broadcast"
)

// HubConfig configures a Hub. Zero values are valid and produce the documented
// defaults.
type HubConfig struct {
	// Logger receives library diagnostics. Defaults to a discarding logger.
	Logger *slog.Logger
	// BufferSize is the per-subscriber event buffer. Defaults to 256.
	BufferSize int
	// DropPolicy controls buffer-overflow behavior. v0.1.0 supports only
	// DropOldest; other values are accepted but treated as DropOldest.
	DropPolicy DropPolicy
	// ErrorHandler receives library-level errors (currently unused; the seam
	// is present so adapter / sink tickets don't change this signature).
	ErrorHandler func(error)
}

const defaultBufferSize = 256

// Hub is the in-process multi-session coordinator. It maintains per-session
// state, broadcasts transition Events to any number of independent Stream
// subscribers, and exposes a forward-only session tag registry.
//
// Hub is safe for concurrent use. Consumers must call Close to release
// subscriber goroutines (via channel closes) when done.
type Hub struct {
	log   *slog.Logger
	errH  func(error)
	bcast *broadcast.Broadcaster[Event]

	mu       sync.Mutex
	sessions map[string]sessionState
	tags     map[string]map[string]string
	closed   bool
}

// NewHub constructs a Hub from the given config. It returns an error for
// interface stability — future configuration may introduce failing
// validations — but any zero-value HubConfig is accepted today.
func NewHub(cfg HubConfig) (*Hub, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	buf := cfg.BufferSize
	if buf <= 0 {
		buf = defaultBufferSize
	}

	errH := cfg.ErrorHandler
	if errH == nil {
		errH = func(err error) { log.Error("agentstatus error", "err", err) }
	}

	return &Hub{
		log:      log,
		errH:     errH,
		bcast:    broadcast.New[Event](buf),
		sessions: make(map[string]sessionState),
		tags:     make(map[string]map[string]string),
	}, nil
}

// Close shuts the Hub down and closes every outstanding subscriber channel.
// It is idempotent.
func (h *Hub) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()

	h.bcast.Close()
	return nil
}

// Events returns a new independent subscriber Stream. Every call returns a
// fresh Stream with its own buffer; slow consumers on one Stream never block
// others.
func (h *Hub) Events() Stream {
	_, ch, _ := h.bcast.Subscribe()
	return Stream{ch: ch}
}

// RegisterSession attaches forward-only metadata tags to a session. Events
// emitted after this call carry a copy of the tags; events emitted earlier
// are never retroactively modified.
//
// Passing a nil or empty tags map registers the session with no tags.
// Re-registering overwrites the previous tag set.
func (h *Hub) RegisterSession(sessionID string, tags map[string]string) {
	copied := make(map[string]string, len(tags))
	maps.Copy(copied, tags)
	h.mu.Lock()
	h.tags[sessionID] = copied
	h.mu.Unlock()
}

// UnregisterSession removes a session's tags. Events already dispatched are
// unaffected; events dispatched after this call carry nil Tags unless the
// session is re-registered.
func (h *Hub) UnregisterSession(sessionID string) {
	h.mu.Lock()
	delete(h.tags, sessionID)
	h.mu.Unlock()
}

// dispatchSignal is the internal seam that drives a Signal through the
// decision machine and publishes the resulting Event (if any) to
// subscribers. Adapter ingest paths will call this once they are built.
//
// Agent is passed separately because Signal (per design) has no Agent field:
// the emitting adapter always knows which Agent it represents.
func (h *Hub) dispatchSignal(agent Agent, sig Signal) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}

	prev := h.sessions[sig.SessionID]
	next, trans := Decide(prev, sig)
	if trans == nil {
		h.sessions[sig.SessionID] = next
		h.mu.Unlock()
		return
	}
	h.sessions[sig.SessionID] = next

	var tags map[string]string
	if t, ok := h.tags[sig.SessionID]; ok {
		tags = make(map[string]string, len(t))
		maps.Copy(tags, t)
	}
	h.mu.Unlock()

	ev := Event{
		Agent:           agent,
		SessionID:       sig.SessionID,
		ParentSessionID: sig.ParentSessionID,
		Status:          trans.Status,
		PrevStatus:      trans.PrevStatus,
		Tool:            sig.Tool,
		Work:            sig.Work,
		At:              sig.At,
		Tags:            tags,
	}
	h.bcast.Publish(ev)
}
