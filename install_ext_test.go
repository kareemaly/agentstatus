package agentstatus_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentstatus "github.com/kareemaly/agentstatus"
	_ "github.com/kareemaly/agentstatus/adapters/claude"
)

// Full install/uninstall round-trip through the real Claude adapter, against
// a live httptest.Server on Hub.Handler(). Verifies that the emitted curl
// command in settings.json actually points at the running hub endpoint, and
// that Uninstall cleans up all managed markers.
func TestInstallHooks_FullRoundTrip(t *testing.T) {
	h, err := agentstatus.NewHub(agentstatus.HubConfig{})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)

	configRoot := t.TempDir()
	cfg := agentstatus.InstallConfig{
		Endpoint:   srv.URL + "/hook",
		Agents:     []agentstatus.Agent{agentstatus.Claude},
		ConfigRoot: configRoot,
	}

	installRes, err := agentstatus.InstallHooks(cfg)
	if err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if len(installRes) != 1 || !installRes[0].Installed {
		t.Fatalf("install result = %+v", installRes)
	}

	path := filepath.Join(configRoot, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), srv.URL+"/hook/claude") {
		t.Fatalf("settings.json missing endpoint URL:\n%s", data)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	uninstallRes, err := agentstatus.UninstallHooks(cfg)
	if err != nil {
		t.Fatalf("UninstallHooks: %v", err)
	}
	if len(uninstallRes) != 1 || uninstallRes[0].Installed {
		t.Fatalf("uninstall result = %+v", uninstallRes)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after: %v", err)
	}
	if strings.Contains(string(after), "agentstatusManaged") {
		t.Fatalf("managed marker still present after uninstall:\n%s", after)
	}
}
