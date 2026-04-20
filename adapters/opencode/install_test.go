package opencode

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	agentstatus "github.com/kareemaly/agentstatus"
)

func baseCfg(t *testing.T) agentstatus.InstallConfig {
	t.Helper()
	return agentstatus.InstallConfig{
		Endpoint:   "http://localhost:9090/hook",
		ConfigRoot: t.TempDir(),
	}
}

type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	h.records = append(h.records, r.Clone())
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *captureHandler) hasWarn(substr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, substr) {
			return true
		}
	}
	return false
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
	if res.Agent != agentstatus.OpenCode {
		t.Fatalf("agent = %q", res.Agent)
	}
	if !strings.HasPrefix(res.Path, cfg.ConfigRoot) {
		t.Fatalf("path %s not under ConfigRoot %s", res.Path, cfg.ConfigRoot)
	}

	// Verify file exists and contains marker
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read plugin file: %v", err)
	}
	if !bytes.Contains(data, []byte(managedMarker)) {
		t.Fatal("file missing managed marker")
	}
	if !bytes.Contains(data, []byte("agentstatus.opencode")) {
		t.Fatal("file missing plugin id")
	}
	if !bytes.Contains(data, []byte("http://localhost:9090/hook/opencode")) {
		t.Fatal("file missing endpoint URL")
	}
}

func TestInstall_Idempotent(t *testing.T) {
	cfg := baseCfg(t)
	res1, err := installHooks(cfg)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if !res1.Installed {
		t.Fatalf("first install failed: %+v", res1)
	}

	data1, _ := os.ReadFile(res1.Path)

	res2, err := installHooks(cfg)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !res2.Installed {
		t.Fatalf("second install failed: %+v", res2)
	}

	data2, _ := os.ReadFile(res2.Path)

	if !bytes.Equal(data1, data2) {
		t.Fatal("files differ on idempotent install")
	}
}

func TestInstall_SelfHealsEndpoint(t *testing.T) {
	cfg := baseCfg(t)
	res1, _ := installHooks(cfg)
	data1, _ := os.ReadFile(res1.Path)

	// Install with different endpoint
	cfg.Endpoint = "http://localhost:8080/hook"
	res2, err := installHooks(cfg)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if !res2.Installed {
		t.Fatalf("second install failed: %+v", res2)
	}

	data2, _ := os.ReadFile(res2.Path)

	if bytes.Equal(data1, data2) {
		t.Fatal("files should differ after endpoint change")
	}
	if !bytes.Contains(data2, []byte("http://localhost:8080/hook/opencode")) {
		t.Fatal("new endpoint not in file")
	}
}

func TestInstall_RejectsNonManaged(t *testing.T) {
	cfg := baseCfg(t)
	path := filepath.Join(cfg.ConfigRoot, ".opencode", "plugin", "agentstatus.ts")
	os.MkdirAll(filepath.Dir(path), 0755)

	// Write non-managed file
	os.WriteFile(path, []byte("// user-owned plugin\nexport default {}"), 0644)

	res, err := installHooks(cfg)
	if err == nil {
		t.Fatal("expected error for non-managed file")
	}
	if res.Agent != agentstatus.OpenCode {
		t.Fatalf("agent = %q", res.Agent)
	}

	// Verify file untouched
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("user-owned")) {
		t.Fatal("file was modified")
	}
}

func TestUninstall_RoundTrip(t *testing.T) {
	cfg := baseCfg(t)
	res1, _ := installHooks(cfg)
	if !res1.Installed {
		t.Fatalf("install failed: %+v", res1)
	}

	// Verify file exists
	if _, err := os.Stat(res1.Path); err != nil {
		t.Fatalf("file missing after install: %v", err)
	}

	res2, err := uninstallHooks(cfg)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if res2.Installed {
		t.Fatalf("uninstall result: %+v", res2)
	}

	// Verify file removed
	if _, err := os.Stat(res2.Path); !os.IsNotExist(err) {
		t.Fatalf("file should be removed: %v", err)
	}

	// Verify directory NOT removed
	dir := filepath.Dir(res2.Path)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("directory should remain: %v", err)
	}
}

func TestUninstall_MissingFile(t *testing.T) {
	cfg := baseCfg(t)
	res, err := uninstallHooks(cfg)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("expected skipped result: %+v", res)
	}
	if res.Reason != "plugin file not found" {
		t.Fatalf("reason: %q", res.Reason)
	}
}

func TestUninstall_RejectsNonManaged(t *testing.T) {
	cfg := baseCfg(t)
	path := filepath.Join(cfg.ConfigRoot, ".opencode", "plugin", "agentstatus.ts")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("// user-owned"), 0644)

	res, err := uninstallHooks(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if res.Agent != agentstatus.OpenCode {
		t.Fatalf("agent = %q", res.Agent)
	}

	// Verify file untouched
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file was removed: %v", err)
	}
}

func TestResolvePath_ProjectBeatsConfigRoot(t *testing.T) {
	cfg := agentstatus.InstallConfig{
		Project:    "/home/user/myproject",
		ConfigRoot: "/tmp/config",
	}
	path, _ := resolvePath(cfg)
	if !strings.HasPrefix(path, "/home/user/myproject") {
		t.Fatalf("path = %s, should use Project", path)
	}
}

func TestResolvePath_ConfigRoot(t *testing.T) {
	cfg := agentstatus.InstallConfig{
		ConfigRoot: "/tmp/config",
	}
	path, _ := resolvePath(cfg)
	if !strings.HasPrefix(path, "/tmp/config") {
		t.Fatalf("path = %s, should use ConfigRoot", path)
	}
}

func TestInstall_OPENCODE_PURE_LogsWarning(t *testing.T) {
	// Save old env
	oldPure := os.Getenv("OPENCODE_PURE")
	defer os.Setenv("OPENCODE_PURE", oldPure)

	// Set OPENCODE_PURE
	os.Setenv("OPENCODE_PURE", "1")

	cfg := baseCfg(t)

	// Capture slog output
	handler := &captureHandler{}
	slog.SetDefault(slog.New(handler))

	res, err := installHooks(cfg)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if !res.Installed {
		t.Fatalf("install failed: %+v", res)
	}

	if !handler.hasWarn("OPENCODE_PURE") {
		t.Fatal("expected warning about OPENCODE_PURE")
	}
}

func TestInstall_PathContainsEndpoint(t *testing.T) {
	cfg := baseCfg(t)
	endpoint := "http://192.168.1.100:8888/custom/path"
	cfg.Endpoint = endpoint
	res, _ := installHooks(cfg)

	data, _ := os.ReadFile(res.Path)
	if !bytes.Contains(data, []byte(endpoint+"/opencode")) {
		t.Fatalf("endpoint not found in plugin: %s", string(data))
	}
}

func TestPlugin_ContainsID(t *testing.T) {
	cfg := baseCfg(t)
	res, _ := installHooks(cfg)
	data, _ := os.ReadFile(res.Path)
	if !bytes.Contains(data, []byte("id: \"agentstatus.opencode\"")) {
		t.Fatal("plugin missing id field")
	}
}

func TestPlugin_ContainsToolHooks(t *testing.T) {
	cfg := baseCfg(t)
	res, _ := installHooks(cfg)
	data, _ := os.ReadFile(res.Path)
	if !bytes.Contains(data, []byte("tool.execute.before")) {
		t.Fatal("plugin missing tool.execute.before hook")
	}
	if !bytes.Contains(data, []byte("tool.execute.after")) {
		t.Fatal("plugin missing tool.execute.after hook")
	}
}
