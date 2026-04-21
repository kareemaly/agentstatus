package agentstatus_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentstatus "github.com/kareemaly/agentstatus"
	_ "github.com/kareemaly/agentstatus/adapters/claude"
	_ "github.com/kareemaly/agentstatus/adapters/codex"
	_ "github.com/kareemaly/agentstatus/adapters/opencode"
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
		Marker:     "test",
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
	if strings.Contains(string(after), "agentstatusMarker") {
		t.Fatalf("managed marker still present after uninstall:\n%s", after)
	}
}

// TestInstallHooks_CodexRoundTrip validates that the orchestrator routes
// correctly for the Codex adapter and that install/uninstall is a clean
// round-trip.
func TestInstallHooks_CodexRoundTrip(t *testing.T) {
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
		Marker:     "test",
		Agents:     []agentstatus.Agent{agentstatus.Codex},
		ConfigRoot: configRoot,
	}

	installRes, err := agentstatus.InstallHooks(cfg)
	if err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if len(installRes) != 1 || !installRes[0].Installed {
		t.Fatalf("install result = %+v", installRes)
	}

	path := filepath.Join(configRoot, ".codex", "hooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), srv.URL+"/hook/codex") {
		t.Fatalf("hooks.json missing endpoint URL:\n%s", data)
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
	if strings.Contains(string(after), "agentstatusMarker") {
		t.Fatalf("managed marker still present after uninstall:\n%s", after)
	}
}

// TestInstallHooks_OpenCodeRoundTrip validates that the OpenCode adapter
// installs a TypeScript plugin and uninstalls it cleanly.
func TestInstallHooks_OpenCodeRoundTrip(t *testing.T) {
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
		Marker:     "test",
		Agents:     []agentstatus.Agent{agentstatus.OpenCode},
		ConfigRoot: configRoot,
	}

	installRes, err := agentstatus.InstallHooks(cfg)
	if err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if len(installRes) != 1 || !installRes[0].Installed {
		t.Fatalf("install result = %+v", installRes)
	}

	path := filepath.Join(configRoot, ".config", "opencode", "plugins", "agentstatus-test.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(data, []byte(srv.URL+"/hook/opencode")) {
		t.Fatalf("plugin file missing endpoint URL:\n%s", data)
	}
	if !bytes.Contains(data, []byte("@managed-by-agentstatus: opencode")) {
		t.Fatalf("plugin file missing marker:\n%s", data)
	}
	if !bytes.Contains(data, []byte("id: \"agentstatus.opencode.test\"")) {
		t.Fatalf("plugin file missing id:\n%s", data)
	}
	if !bytes.Contains(data, []byte("hook_event_name:")) {
		t.Fatalf("plugin file uses event_type instead of hook_event_name:\n%s", data)
	}

	uninstallRes, err := agentstatus.UninstallHooks(cfg)
	if err != nil {
		t.Fatalf("UninstallHooks: %v", err)
	}
	if len(uninstallRes) != 1 || uninstallRes[0].Installed {
		t.Fatalf("uninstall result = %+v", uninstallRes)
	}

	// Verify file removed
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plugin file should be removed: %v", err)
	}
}

// TestInstallHooks_AllThreeAgents verifies that InstallHooks with AllAgents
// correctly installs all three adapters and UninstallHooks removes all.
func TestInstallHooks_AllThreeAgents(t *testing.T) {
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
		Marker:     "test",
		Agents:     agentstatus.AllAgents,
		ConfigRoot: configRoot,
	}

	installRes, err := agentstatus.InstallHooks(cfg)
	if err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}
	if len(installRes) != 3 {
		t.Fatalf("expected 3 install results, got %d: %+v", len(installRes), installRes)
	}

	for _, res := range installRes {
		if !res.Installed {
			t.Fatalf("adapter %q not installed: %+v", res.Agent, res)
		}
	}

	// Verify files exist
	claudePath := filepath.Join(configRoot, ".claude", "settings.json")
	codexPath := filepath.Join(configRoot, ".codex", "hooks.json")
	opencodePath := filepath.Join(configRoot, ".config", "opencode", "plugins", "agentstatus-test.ts")

	if _, err := os.Stat(claudePath); err != nil {
		t.Fatalf("Claude settings missing: %v", err)
	}
	if _, err := os.Stat(codexPath); err != nil {
		t.Fatalf("Codex hooks missing: %v", err)
	}
	if _, err := os.Stat(opencodePath); err != nil {
		t.Fatalf("OpenCode plugin missing: %v", err)
	}

	// Uninstall all
	uninstallRes, err := agentstatus.UninstallHooks(cfg)
	if err != nil {
		t.Fatalf("UninstallHooks: %v", err)
	}
	if len(uninstallRes) != 3 {
		t.Fatalf("expected 3 uninstall results, got %d: %+v", len(uninstallRes), uninstallRes)
	}

	// Verify files cleaned
	claudeData, _ := os.ReadFile(claudePath)
	if strings.Contains(string(claudeData), "agentstatusMarker") {
		t.Fatal("Claude managed marker still present")
	}

	codexData, _ := os.ReadFile(codexPath)
	if strings.Contains(string(codexData), "agentstatusMarker") {
		t.Fatal("Codex managed marker still present")
	}

	if _, err := os.Stat(opencodePath); !os.IsNotExist(err) {
		t.Fatal("OpenCode plugin file should be removed")
	}
}
