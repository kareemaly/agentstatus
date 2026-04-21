package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kareemaly/agentstatus"
)

// Defaults applied when Config fields are left at their zero value.
const (
	DefaultPathTemplate = "~/tmp/agentstatus-events/{agent}/{date}/{hour}.jsonl"
	DefaultBufferSize   = 256
	DefaultIdleTimeout  = 5 * time.Minute
)

// ErrEmptyTemplate is returned from New if Config.PathTemplate is explicitly
// set to the empty string. The zero-value (unset) template falls back to
// DefaultPathTemplate and is not an error.
var ErrEmptyTemplate = errors.New("file sink: PathTemplate must be non-empty or left zero for default")

// Config configures a file Sink. Zero values are valid and produce the
// documented defaults.
type Config struct {
	// PathTemplate selects the destination file per event. Supports the
	// placeholders {agent}, {date} (UTC YYYY-MM-DD), {hour} (UTC HH), and
	// {session}. A leading "~/" is expanded to the current user's home.
	// Empty string falls back to DefaultPathTemplate.
	PathTemplate string
	// BufferSize bounds the internal event queue. A non-positive value uses
	// DefaultBufferSize. When the queue is full Send drops the oldest
	// queued event (counted via Drops).
	BufferSize int
	// IdleTimeout is how long an open file handle may sit unused before the
	// background worker closes it. A non-positive value uses
	// DefaultIdleTimeout.
	IdleTimeout time.Duration
	// Logger receives write and marshaling errors. Defaults to slog.Default().
	Logger *slog.Logger
	// Clock returns the current UTC time. Used for path-template placeholder
	// expansion when Event.At is zero and for stale-handle eviction. Exposed
	// primarily for deterministic tests; nil uses time.Now().UTC.
	//
	// Callers injecting a Clock are responsible for making it safe to call
	// from the sink's worker goroutine.
	Clock func() time.Time
}

// Sink appends agentstatus.Event values as JSON Lines to files on disk.
// Each event is serialized to one line and written with a trailing newline;
// the destination path is the configured template with placeholders expanded
// per-event. Files are opened in append mode, created recursively, and closed
// after an idle period.
//
// Sink implements agentstatus.Sink. It is safe for concurrent use.
type Sink struct {
	tpl         string
	idleTimeout time.Duration
	log         *slog.Logger

	events   chan agentstatus.Event
	drops    atomic.Int64
	isClosed atomic.Bool

	closeOnce sync.Once
	closed    chan struct{}
	done      chan struct{}

	// clock returns UTC wall time. Overridden in tests for deterministic
	// stale-handle eviction.
	clock func() time.Time

	mu    sync.Mutex
	files map[string]*openFile
}

type openFile struct {
	f        *os.File
	lastUsed time.Time
}

// New constructs a file Sink and starts its background writer goroutine.
// Close must be called to flush pending events and release file handles.
func New(cfg Config) (*Sink, error) {
	tpl := cfg.PathTemplate
	if tpl == "" {
		tpl = DefaultPathTemplate
	}
	if strings.TrimSpace(tpl) == "" {
		return nil, ErrEmptyTemplate
	}
	buf := cfg.BufferSize
	if buf <= 0 {
		buf = DefaultBufferSize
	}
	idle := cfg.IdleTimeout
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}

	s := &Sink{
		tpl:         tpl,
		idleTimeout: idle,
		log:         log,
		events:      make(chan agentstatus.Event, buf),
		closed:      make(chan struct{}),
		done:        make(chan struct{}),
		files:       make(map[string]*openFile),
		clock:       clock,
	}
	go s.run()
	return s, nil
}

// Name returns the stable sink identifier "file".
func (s *Sink) Name() string { return "file" }

// Send enqueues e for asynchronous writing. It never blocks: when the
// internal queue is full, the oldest queued event is discarded (counted via
// Drops) to make room. After Close, Send is a silent no-op.
func (s *Sink) Send(_ context.Context, e agentstatus.Event) error {
	if s.isClosed.Load() {
		return nil
	}
	select {
	case s.events <- e:
		return nil
	default:
	}
	select {
	case <-s.events:
		s.drops.Add(1)
	default:
	}
	select {
	case s.events <- e:
	default:
		s.drops.Add(1)
	}
	return nil
}

// Drops reports the cumulative number of events dropped due to buffer
// overflow since the sink was constructed.
func (s *Sink) Drops() int64 { return s.drops.Load() }

// Close marks the sink closed, drains the queue, closes all open files, and
// stops the background worker. It is idempotent; subsequent calls are no-ops
// that return nil.
func (s *Sink) Close() error {
	s.closeOnce.Do(func() {
		s.isClosed.Store(true)
		close(s.closed)
	})
	<-s.done
	return nil
}

func (s *Sink) run() {
	defer close(s.done)

	tickEvery := s.idleTimeout / 2
	if tickEvery <= 0 {
		tickEvery = time.Second
	}
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	for {
		select {
		case e := <-s.events:
			s.write(e)
		case <-ticker.C:
			s.evictIdle()
		case <-s.closed:
			s.drain()
			s.closeFiles()
			return
		}
	}
}

func (s *Sink) drain() {
	for {
		select {
		case e := <-s.events:
			s.write(e)
		default:
			return
		}
	}
}

func (s *Sink) write(e agentstatus.Event) {
	path, err := s.resolvePath(e)
	if err != nil {
		s.log.Error("agentstatus file sink: resolve path", "err", err)
		return
	}
	f, err := s.openFile(path)
	if err != nil {
		s.log.Error("agentstatus file sink: open file", "path", path, "err", err)
		return
	}
	line, err := marshal(e)
	if err != nil {
		s.log.Error("agentstatus file sink: marshal event", "err", err)
		return
	}
	if _, err := f.Write(line); err != nil {
		s.log.Error("agentstatus file sink: write", "path", path, "err", err)
	}
}

func (s *Sink) openFile(path string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if of, ok := s.files[path]; ok {
		of.lastUsed = s.clock()
		return of.f, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.files[path] = &openFile{f: f, lastUsed: s.clock()}
	return f, nil
}

func (s *Sink) evictIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.clock().Add(-s.idleTimeout)
	for path, of := range s.files {
		if of.lastUsed.Before(cutoff) {
			if err := of.f.Close(); err != nil {
				s.log.Error("agentstatus file sink: close stale file", "path", path, "err", err)
			}
			delete(s.files, path)
		}
	}
}

func (s *Sink) closeFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, of := range s.files {
		if err := of.f.Close(); err != nil {
			s.log.Error("agentstatus file sink: close file", "path", path, "err", err)
		}
		delete(s.files, path)
	}
}

func (s *Sink) resolvePath(e agentstatus.Event) (string, error) {
	t := e.At
	if t.IsZero() {
		t = s.clock()
	}
	t = t.UTC()

	path := s.tpl
	path = strings.ReplaceAll(path, "{agent}", string(e.Agent))
	path = strings.ReplaceAll(path, "{date}", t.Format("2006-01-02"))
	path = strings.ReplaceAll(path, "{hour}", t.Format("15"))
	path = strings.ReplaceAll(path, "{session}", e.SessionID)

	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return path, nil
}

type wireEvent struct {
	Agent           string            `json:"agent"`
	SessionID       string            `json:"session_id"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	Status          string            `json:"status"`
	PrevStatus      string            `json:"prev_status,omitempty"`
	Tool            string            `json:"tool,omitempty"`
	Work            string            `json:"work,omitempty"`
	At              time.Time         `json:"at"`
	Tags            map[string]string `json:"tags,omitempty"`
	Raw             map[string]any    `json:"raw,omitempty"`
}

func marshal(e agentstatus.Event) ([]byte, error) {
	w := wireEvent{
		Agent:           string(e.Agent),
		SessionID:       e.SessionID,
		ParentSessionID: e.ParentSessionID,
		Status:          string(e.Status),
		PrevStatus:      string(e.PrevStatus),
		Tool:            e.Tool,
		Work:            e.Work,
		At:              e.At,
		Tags:            e.Tags,
		Raw:             e.Raw,
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
