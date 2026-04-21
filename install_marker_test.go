package agentstatus_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentstatus "github.com/kareemaly/agentstatus"
	_ "github.com/kareemaly/agentstatus/adapters/claude"
	_ "github.com/kareemaly/agentstatus/adapters/codex"
	_ "github.com/kareemaly/agentstatus/adapters/opencode"
)

// markerCount walks a Claude/Codex-shaped hooks map and counts managed
// entries for the given marker, across every event and group.
func markerCount(t *testing.T, path, marker string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	n := 0
	for _, raw := range hooks {
		groups, _ := raw.([]any)
		for _, g := range groups {
			group, _ := g.(map[string]any)
			inner, _ := group["hooks"].([]any)
			for _, h := range inner {
				hm, _ := h.(map[string]any)
				if m, _ := hm["agentstatusMarker"].(string); m == marker {
					n++
				}
			}
		}
	}
	return n
}

// TestMultiMarker_ClaudeCoexist verifies two consumers can install Claude
// hooks under different markers and both sets survive, then each uninstall
// only removes its own.
func TestMultiMarker_ClaudeCoexist(t *testing.T) {
	configRoot := t.TempDir()
	base := agentstatus.InstallConfig{
		Endpoint:   "http://localhost:9090/hook",
		Agents:     []agentstatus.Agent{agentstatus.Claude},
		ConfigRoot: configRoot,
	}

	cortexCfg := base
	cortexCfg.Marker = "cortex"
	if _, err := agentstatus.InstallHooks(cortexCfg); err != nil {
		t.Fatalf("install cortex: %v", err)
	}

	captureCfg := base
	captureCfg.Marker = "capture"
	captureCfg.Endpoint = "http://localhost:9191/capture"
	if _, err := agentstatus.InstallHooks(captureCfg); err != nil {
		t.Fatalf("install capture: %v", err)
	}

	path := filepath.Join(configRoot, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "http://localhost:9090/hook/claude") {
		t.Fatalf("cortex endpoint missing:\n%s", data)
	}
	if !strings.Contains(string(data), "http://localhost:9191/capture/claude") {
		t.Fatalf("capture endpoint missing:\n%s", data)
	}
	if got := markerCount(t, path, "cortex"); got == 0 {
		t.Fatalf("expected cortex entries, got 0")
	}
	if got := markerCount(t, path, "capture"); got == 0 {
		t.Fatalf("expected capture entries, got 0")
	}

	// Uninstall capture only — cortex entries must survive.
	if _, err := agentstatus.UninstallHooks(captureCfg); err != nil {
		t.Fatalf("uninstall capture: %v", err)
	}
	if got := markerCount(t, path, "capture"); got != 0 {
		t.Fatalf("capture entries remained after uninstall: %d", got)
	}
	if got := markerCount(t, path, "cortex"); got == 0 {
		t.Fatalf("cortex entries wiped by capture uninstall")
	}

	// And now cortex.
	if _, err := agentstatus.UninstallHooks(cortexCfg); err != nil {
		t.Fatalf("uninstall cortex: %v", err)
	}
	if got := markerCount(t, path, "cortex"); got != 0 {
		t.Fatalf("cortex entries remained after uninstall: %d", got)
	}
}

// TestMultiMarker_CodexCoexist mirrors the Claude test for Codex's hooks.json.
func TestMultiMarker_CodexCoexist(t *testing.T) {
	configRoot := t.TempDir()
	base := agentstatus.InstallConfig{
		Endpoint:   "http://localhost:9090/hook",
		Agents:     []agentstatus.Agent{agentstatus.Codex},
		ConfigRoot: configRoot,
	}

	cortexCfg := base
	cortexCfg.Marker = "cortex"
	if _, err := agentstatus.InstallHooks(cortexCfg); err != nil {
		t.Fatalf("install cortex: %v", err)
	}

	captureCfg := base
	captureCfg.Marker = "capture"
	captureCfg.Endpoint = "http://localhost:9191/capture"
	if _, err := agentstatus.InstallHooks(captureCfg); err != nil {
		t.Fatalf("install capture: %v", err)
	}

	path := filepath.Join(configRoot, ".codex", "hooks.json")
	if markerCount(t, path, "cortex") == 0 {
		t.Fatalf("cortex entries missing")
	}
	if markerCount(t, path, "capture") == 0 {
		t.Fatalf("capture entries missing")
	}

	if _, err := agentstatus.UninstallHooks(captureCfg); err != nil {
		t.Fatalf("uninstall capture: %v", err)
	}
	if markerCount(t, path, "capture") != 0 {
		t.Fatalf("capture entries remained after uninstall")
	}
	if markerCount(t, path, "cortex") == 0 {
		t.Fatalf("cortex entries wiped by capture uninstall")
	}
}

// TestMultiMarker_OpenCodeSideBySide verifies the OpenCode plugin filename is
// marker-scoped so two consumers produce two files, and each uninstall only
// removes its own file.
func TestMultiMarker_OpenCodeSideBySide(t *testing.T) {
	configRoot := t.TempDir()
	base := agentstatus.InstallConfig{
		Endpoint:   "http://localhost:9090/hook",
		Agents:     []agentstatus.Agent{agentstatus.OpenCode},
		ConfigRoot: configRoot,
	}

	cortexCfg := base
	cortexCfg.Marker = "cortex"
	if _, err := agentstatus.InstallHooks(cortexCfg); err != nil {
		t.Fatalf("install cortex: %v", err)
	}

	captureCfg := base
	captureCfg.Marker = "capture"
	captureCfg.Endpoint = "http://localhost:9191/capture"
	if _, err := agentstatus.InstallHooks(captureCfg); err != nil {
		t.Fatalf("install capture: %v", err)
	}

	pluginsDir := filepath.Join(configRoot, ".config", "opencode", "plugins")
	cortexPath := filepath.Join(pluginsDir, "agentstatus-cortex.ts")
	capturePath := filepath.Join(pluginsDir, "agentstatus-capture.ts")

	cortexData, err := os.ReadFile(cortexPath)
	if err != nil {
		t.Fatalf("read cortex plugin: %v", err)
	}
	captureData, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture plugin: %v", err)
	}
	if !bytes.Contains(cortexData, []byte("agentstatus.opencode.cortex")) {
		t.Fatalf("cortex id missing:\n%s", cortexData)
	}
	if !bytes.Contains(captureData, []byte("agentstatus.opencode.capture")) {
		t.Fatalf("capture id missing:\n%s", captureData)
	}

	if _, err := agentstatus.UninstallHooks(captureCfg); err != nil {
		t.Fatalf("uninstall capture: %v", err)
	}
	if _, err := os.Stat(capturePath); !os.IsNotExist(err) {
		t.Fatalf("capture plugin should be removed: %v", err)
	}
	if _, err := os.Stat(cortexPath); err != nil {
		t.Fatalf("cortex plugin should remain: %v", err)
	}
}
