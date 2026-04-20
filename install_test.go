package agentstatus

import (
	"strings"
	"testing"
)

func registerStub(t *testing.T, name Agent, install, uninstall func(InstallConfig) (InstallResult, error)) {
	t.Helper()
	a := Adapter{
		Name:           name,
		MapHookEvent:   okMap,
		InstallHooks:   install,
		UninstallHooks: uninstall,
	}
	if err := RegisterAdapter(a); err != nil {
		t.Fatalf("RegisterAdapter: %v", err)
	}
}

func TestInstallHooks_UnknownAgent(t *testing.T) {
	withCleanRegistry(t, func() {
		res, err := InstallHooks(InstallConfig{
			Endpoint: "http://localhost:9090/hook",
			Agents:   []Agent{"nonexistent"},
		})
		if err != nil {
			t.Fatalf("InstallHooks: %v", err)
		}
		if len(res) != 1 {
			t.Fatalf("len = %d", len(res))
		}
		if !res[0].Skipped || res[0].Reason != "adapter not registered" {
			t.Fatalf("res[0] = %+v", res[0])
		}
		if res[0].Agent != "nonexistent" {
			t.Fatalf("agent = %q", res[0].Agent)
		}
	})
}

func TestInstallHooks_UnknownAgentDoesNotAbortOthers(t *testing.T) {
	withCleanRegistry(t, func() {
		called := false
		registerStub(t, "fake",
			func(cfg InstallConfig) (InstallResult, error) {
				called = true
				return InstallResult{Installed: true, Path: "/tmp/fake"}, nil
			},
			nil,
		)
		res, err := InstallHooks(InstallConfig{
			Endpoint: "http://localhost:9090/hook",
			Agents:   []Agent{"nonexistent", "fake"},
		})
		if err != nil {
			t.Fatalf("InstallHooks: %v", err)
		}
		if len(res) != 2 {
			t.Fatalf("len = %d", len(res))
		}
		if !res[0].Skipped {
			t.Fatalf("res[0] = %+v", res[0])
		}
		if !res[1].Installed {
			t.Fatalf("res[1] = %+v", res[1])
		}
		if !called {
			t.Fatalf("fake adapter InstallHooks not invoked")
		}
	})
}

func TestInstallHooks_BadEndpoint(t *testing.T) {
	cases := []string{"", "not a url", "missing-scheme.example", "http://"}
	for _, ep := range cases {
		t.Run(ep, func(t *testing.T) {
			res, err := InstallHooks(InstallConfig{
				Endpoint: ep,
				Agents:   []Agent{Claude},
			})
			if err == nil {
				t.Fatalf("expected validation error for %q", ep)
			}
			if res != nil {
				t.Fatalf("expected nil results on validation failure, got %+v", res)
			}
		})
	}
}

func TestInstallHooks_NilAdapterFunc(t *testing.T) {
	withCleanRegistry(t, func() {
		registerStub(t, "fake", nil, nil)
		res, err := InstallHooks(InstallConfig{
			Endpoint: "http://localhost:9090/hook",
			Agents:   []Agent{"fake"},
		})
		if err != nil {
			t.Fatalf("InstallHooks: %v", err)
		}
		if !res[0].Skipped || !strings.Contains(res[0].Reason, "does not support install") {
			t.Fatalf("res[0] = %+v", res[0])
		}
	})
}
