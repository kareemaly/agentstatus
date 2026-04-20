package agentstatus

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// withCleanRegistry runs fn with a freshly-empty registry, restoring whatever
// adapters were registered (e.g. by init() in blank-imported subpackages)
// when fn returns. Tests share the package-level registry, so this isolation
// is required.
func withCleanRegistry(t *testing.T, fn func()) {
	t.Helper()
	registryMu.Lock()
	saved := registry
	registry = map[Agent]Adapter{}
	registryMu.Unlock()

	defer func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	}()
	fn()
}

func okMap(string, map[string]any) (*Signal, error) { return nil, nil }

func TestRegisterAdapter_Duplicate(t *testing.T) {
	withCleanRegistry(t, func() {
		a := Adapter{Name: "fake", MapHookEvent: okMap}
		if err := RegisterAdapter(a); err != nil {
			t.Fatalf("first register: %v", err)
		}
		err := RegisterAdapter(a)
		if err == nil {
			t.Fatal("expected duplicate error")
		}
		if !strings.Contains(err.Error(), "already registered") {
			t.Errorf("error text: %v", err)
		}
	})
}

func TestRegisterAdapter_EmptyName(t *testing.T) {
	withCleanRegistry(t, func() {
		err := RegisterAdapter(Adapter{MapHookEvent: okMap})
		if err == nil {
			t.Fatal("expected empty-name error")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error text: %v", err)
		}
	})
}

func TestRegisterAdapter_NilMap(t *testing.T) {
	withCleanRegistry(t, func() {
		err := RegisterAdapter(Adapter{Name: "fake"})
		if err == nil {
			t.Fatal("expected nil-map error")
		}
	})
}

func TestAdapters_SortedSnapshot(t *testing.T) {
	withCleanRegistry(t, func() {
		_ = RegisterAdapter(Adapter{Name: "zebra", MapHookEvent: okMap})
		_ = RegisterAdapter(Adapter{Name: "alpha", MapHookEvent: okMap})
		_ = RegisterAdapter(Adapter{Name: "mango", MapHookEvent: okMap})

		got := Adapters()
		if len(got) != 3 {
			t.Fatalf("len: %d", len(got))
		}
		want := []Agent{"alpha", "mango", "zebra"}
		for i, a := range got {
			if a.Name != want[i] {
				t.Errorf("[%d]: got %q, want %q", i, a.Name, want[i])
			}
		}

		// Mutating the returned slice must not affect the registry.
		got[0] = Adapter{Name: "tampered"}
		again := Adapters()
		if again[0].Name != "alpha" {
			t.Errorf("registry mutated via snapshot: %q", again[0].Name)
		}
	})
}

func TestRegisterAdapter_Concurrent(t *testing.T) {
	withCleanRegistry(t, func() {
		var wg sync.WaitGroup
		for i := range 16 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = RegisterAdapter(Adapter{
					Name:         Agent("a-" + string(rune('a'+i))),
					MapHookEvent: okMap,
				})
				_ = Adapters()
			}(i)
		}
		wg.Wait()
		if got := len(Adapters()); got != 16 {
			t.Errorf("registered: got %d, want 16", got)
		}
	})
}

func TestErrUnknownAgent_IsSentinel(t *testing.T) {
	withCleanRegistry(t, func() {
		h, err := NewHub(HubConfig{})
		if err != nil {
			t.Fatalf("NewHub: %v", err)
		}
		t.Cleanup(func() { _ = h.Close() })

		err = h.Ingest("nope", []byte(`{}`))
		if !errors.Is(err, ErrUnknownAgent) {
			t.Fatalf("Ingest err: %v", err)
		}
	})
}
