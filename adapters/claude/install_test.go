package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentstatus "github.com/kareemaly/agentstatus"
)

func baseCfg(t *testing.T) agentstatus.InstallConfig {
	t.Helper()
	return agentstatus.InstallConfig{
		Endpoint:   "http://localhost:9090/hook",
		Marker:     "test",
		ConfigRoot: t.TempDir(),
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

func managedHooksFor(t *testing.T, root map[string]any, event, marker string) []map[string]any {
	t.Helper()
	hooks, _ := root["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	var out []map[string]any
	for _, g := range groups {
		group, _ := g.(map[string]any)
		inner, _ := group["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if m, _ := hm[markerField].(string); m == marker {
				out = append(out, hm)
			}
		}
	}
	return out
}

func TestInstall_EmptyDir(t *testing.T) {
	cfg := baseCfg(t)
	res, err := installHooks(cfg)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if !res.Installed || res.Skipped {
		t.Fatalf("result = %+v", res)
	}
	if res.Agent != agentstatus.Claude {
		t.Fatalf("agent = %q", res.Agent)
	}
	if filepath.Dir(filepath.Dir(res.Path)) != cfg.ConfigRoot {
		t.Fatalf("path = %s, wanted under %s", res.Path, cfg.ConfigRoot)
	}

	root := readJSON(t, res.Path)
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks missing; root=%v", root)
	}
	for _, event := range installedEvents {
		entries := managedHooksFor(t, root, event, cfg.Marker)
		if len(entries) != 1 {
			t.Fatalf("event %s: got %d managed hooks, want 1", event, len(entries))
		}
		cmd, _ := entries[0]["command"].(string)
		if !strings.Contains(cmd, "/hook/claude") {
			t.Fatalf("event %s: command %q missing /hook/claude", event, cmd)
		}
		if !strings.Contains(cmd, cfg.Endpoint) {
			t.Fatalf("event %s: command %q missing endpoint", event, cmd)
		}
	}
	if len(hooks) != len(installedEvents) {
		t.Fatalf("hooks has %d keys, want %d", len(hooks), len(installedEvents))
	}
}

func TestInstall_Idempotent(t *testing.T) {
	cfg := baseCfg(t)
	if _, err := installHooks(cfg); err != nil {
		t.Fatalf("install 1: %v", err)
	}
	path := filepath.Join(cfg.ConfigRoot, ".claude", "settings.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := installHooks(cfg); err != nil {
		t.Fatalf("install 2: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("byte diff between runs\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestInstall_PreservesUserEntries(t *testing.T) {
	cfg := baseCfg(t)
	path := filepath.Join(cfg.ConfigRoot, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	userAuth := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "echo user-owned",
						},
					},
				},
			},
		},
	}
	seed, _ := json.MarshalIndent(userAuth, "", "  ")
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := installHooks(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}
	root := readJSON(t, path)
	hooks, _ := root["hooks"].(map[string]any)
	stop, _ := hooks["Stop"].([]any)

	var sawUser, sawOurs bool
	for _, g := range stop {
		group, _ := g.(map[string]any)
		inner, _ := group["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			cmd, _ := hm["command"].(string)
			if m, _ := hm[markerField].(string); m == cfg.Marker {
				sawOurs = true
				continue
			}
			if cmd == "echo user-owned" {
				sawUser = true
			}
		}
	}
	if !sawUser {
		t.Fatalf("user entry lost")
	}
	if !sawOurs {
		t.Fatalf("managed entry not installed")
	}
}

func TestInstall_SelfHealsEndpoint(t *testing.T) {
	cfg := baseCfg(t)
	oldEndpoint := "http://old:1234/hook"
	path := filepath.Join(cfg.ConfigRoot, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seedRoot := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type":      "command",
							"command":   hookCommand(oldEndpoint),
							"timeout":   10,
							markerField: cfg.Marker,
						},
						map[string]any{
							"type":    "command",
							"command": "echo user-sibling",
						},
					},
				},
			},
		},
	}
	seed, _ := json.MarshalIndent(seedRoot, "", "  ")
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := installHooks(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}
	root := readJSON(t, path)
	entries := managedHooksFor(t, root, "Stop", cfg.Marker)
	if len(entries) != 1 {
		t.Fatalf("managed Stop hooks = %d, want 1", len(entries))
	}
	cmd, _ := entries[0]["command"].(string)
	if !strings.Contains(cmd, "http://localhost:9090/hook/claude") {
		t.Fatalf("endpoint not updated: %q", cmd)
	}
	if strings.Contains(cmd, oldEndpoint) {
		t.Fatalf("old endpoint still present: %q", cmd)
	}
	// User sibling still present under Stop.
	hooks, _ := root["hooks"].(map[string]any)
	stop, _ := hooks["Stop"].([]any)
	found := false
	for _, g := range stop {
		group, _ := g.(map[string]any)
		inner, _ := group["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			c, _ := hm["command"].(string)
			if c == "echo user-sibling" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("user sibling entry removed")
	}
}

func TestInstall_RejectsMalformed(t *testing.T) {
	cfg := baseCfg(t)
	path := filepath.Join(cfg.ConfigRoot, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	junk := []byte(`{not-json`)
	if err := os.WriteFile(path, junk, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := installHooks(cfg)
	if err != nil {
		t.Fatalf("installHooks returned error: %v", err)
	}
	if res.Installed {
		t.Fatalf("expected Installed=false")
	}
	if !strings.Contains(res.Reason, "malformed settings.json") {
		t.Fatalf("reason = %q", res.Reason)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, junk) {
		t.Fatalf("file modified despite parse error")
	}
}

func TestUninstall_RoundTrip(t *testing.T) {
	cfg := baseCfg(t)
	path := filepath.Join(cfg.ConfigRoot, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// "original" written with the same marshaler the installer uses, so the
	// byte-equivalence check after install→uninstall is meaningful.
	original := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "echo user-owned",
						},
					},
				},
			},
		},
	}
	originalBytes, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, originalBytes, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := installHooks(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := uninstallHooks(cfg); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, originalBytes) {
		t.Fatalf("round-trip not byte-equivalent\noriginal:\n%s\ngot:\n%s", originalBytes, got)
	}
}

func TestUninstall_PreservesUserEntries(t *testing.T) {
	cfg := baseCfg(t)
	if _, err := installHooks(cfg); err != nil {
		t.Fatalf("install: %v", err)
	}
	path := filepath.Join(cfg.ConfigRoot, ".claude", "settings.json")
	// Now mutate the file to add a user entry in Stop alongside ours.
	root := readJSON(t, path)
	hooks, _ := root["hooks"].(map[string]any)
	stop, _ := hooks["Stop"].([]any)
	stop = append(stop, map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "echo user-owned"},
		},
	})
	hooks["Stop"] = stop
	out, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("reseed: %v", err)
	}

	if _, err := uninstallHooks(cfg); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	afterRoot := readJSON(t, path)
	if len(managedHooksFor(t, afterRoot, "Stop", cfg.Marker)) != 0 {
		t.Fatalf("managed Stop entry still present")
	}
	afterHooks, _ := afterRoot["hooks"].(map[string]any)
	afterStop, _ := afterHooks["Stop"].([]any)
	found := false
	for _, g := range afterStop {
		group, _ := g.(map[string]any)
		inner, _ := group["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			c, _ := hm["command"].(string)
			if c == "echo user-owned" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("user entry removed")
	}
}

func TestUninstall_MissingFile(t *testing.T) {
	cfg := baseCfg(t)
	res, err := uninstallHooks(cfg)
	if err != nil {
		t.Fatalf("uninstallHooks: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("expected Skipped=true, got %+v", res)
	}
	if res.Reason != "settings.json not found" {
		t.Fatalf("reason = %q", res.Reason)
	}
}

func TestUninstall_RejectsMalformed(t *testing.T) {
	cfg := baseCfg(t)
	path := filepath.Join(cfg.ConfigRoot, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	junk := []byte(`{not-json`)
	if err := os.WriteFile(path, junk, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := uninstallHooks(cfg)
	if err != nil {
		t.Fatalf("uninstallHooks: %v", err)
	}
	if res.Installed {
		t.Fatalf("expected Installed=false")
	}
	if !strings.Contains(res.Reason, "malformed settings.json") {
		t.Fatalf("reason = %q", res.Reason)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, junk) {
		t.Fatalf("file modified despite parse error")
	}
}

func TestResolvePath_ProjectBeatsConfigRoot(t *testing.T) {
	project := t.TempDir()
	configRoot := t.TempDir()
	cfg := agentstatus.InstallConfig{
		Endpoint:   "http://localhost:9090/hook",
		Project:    project,
		ConfigRoot: configRoot,
	}
	got, err := resolvePath(cfg)
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	want := filepath.Join(project, ".claude", "settings.json")
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestResolvePath_ConfigRootBeatsHome(t *testing.T) {
	configRoot := t.TempDir()
	cfg := agentstatus.InstallConfig{
		Endpoint:   "http://localhost:9090/hook",
		ConfigRoot: configRoot,
	}
	got, err := resolvePath(cfg)
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	want := filepath.Join(configRoot, ".claude", "settings.json")
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
