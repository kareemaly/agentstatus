package agentstatus

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Adapter is the per-agent extension point. Built-in adapters live under
// adapters/<name> and self-register from init(); third parties register the
// same way.
//
// MapHookEvent is required: it translates a single native hook payload into a
// Signal (or returns (nil, nil) to silently drop, e.g. for unknown event
// names or metadata-only events).
//
// InstallHooks and UninstallHooks may be nil if an adapter does not yet
// implement them; the orchestrator (deferred to a later ticket) treats nil as
// "skipped — not implemented".
type Adapter struct {
	Name           Agent
	MapHookEvent   func(event string, payload map[string]any) (*Signal, error)
	InstallHooks   func(cfg InstallConfig) (InstallResult, error)
	UninstallHooks func(cfg InstallConfig) (InstallResult, error)
}

// InstallConfig parameterizes hook installation. See specs/design.md.
type InstallConfig struct {
	// Endpoint is the base URL the bridge POSTs hook payloads to (e.g.
	// "http://localhost:9090/hook"). Adapters append /<agent> as needed.
	Endpoint string
	// Agents narrows install to a subset; empty means all registered agents.
	Agents []Agent
	// Project, when non-empty, targets a project-level config file instead of
	// the user-level default.
	Project string
}

// InstallResult is one adapter's outcome from an install or uninstall pass.
type InstallResult struct {
	Agent     Agent
	Installed bool
	Skipped   bool
	Reason    string
	Path      string
}

// AllAgents enumerates the built-in agent identifiers, in a stable order
// suitable for InstallConfig.Agents.
var AllAgents = []Agent{Claude, Codex, OpenCode}

var (
	registryMu sync.RWMutex
	registry   = map[Agent]Adapter{}
)

// ErrUnknownAgent is returned by Hub.Ingest when no adapter is registered
// under the given Agent name. The HTTP handler maps this to 404.
var ErrUnknownAgent = errors.New("agentstatus: unknown agent")

// RegisterAdapter adds an adapter to the package-level registry. It is
// goroutine-safe and intended to be called from init() in adapter
// subpackages. Returns an error if the name is empty or already registered.
func RegisterAdapter(a Adapter) error {
	if a.Name == "" {
		return errors.New("agentstatus: adapter name is empty")
	}
	if a.MapHookEvent == nil {
		return fmt.Errorf("agentstatus: adapter %q has nil MapHookEvent", a.Name)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[a.Name]; ok {
		return fmt.Errorf("agentstatus: adapter %q already registered", a.Name)
	}
	registry[a.Name] = a
	return nil
}

// Adapters returns a snapshot of registered adapters, sorted by Name. The
// returned slice is owned by the caller; mutating it does not affect the
// registry.
func Adapters() []Adapter {
	registryMu.RLock()
	out := make([]Adapter, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	registryMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lookupAdapter is the internal accessor used by Hub.Ingest.
func lookupAdapter(name Agent) (Adapter, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	a, ok := registry[name]
	return a, ok
}
