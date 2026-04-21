package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	agentstatus "github.com/kareemaly/agentstatus"
	"github.com/kareemaly/agentstatus/internal/configfile"
)

// installedEvents is the full set of Codex hook events we register.
var installedEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"Stop",
}

// markerField is the JSON field stamped on every inner hook this library
// installs. Its value is the caller-supplied InstallConfig.Marker, letting
// multiple consumers coexist without clobbering each other's entries.
const markerField = "agentstatusMarker"

func hookCommand(endpoint string) string {
	return fmt.Sprintf(
		"curl -s -X POST --max-time 5 -H 'Content-Type: application/json' --data-binary @- %s/codex",
		endpoint,
	)
}

// resolveBaseDir returns the base directory that contains the .codex folder.
// Precedence: Project > ConfigRoot > os.UserHomeDir.
func resolveBaseDir(cfg agentstatus.InstallConfig) (string, error) {
	switch {
	case cfg.Project != "":
		return cfg.Project, nil
	case cfg.ConfigRoot != "":
		return cfg.ConfigRoot, nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return home, nil
	}
}

func resolvePath(cfg agentstatus.InstallConfig) (string, error) {
	base, err := resolveBaseDir(cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, ".codex", "hooks.json"), nil
}

func installHooks(cfg agentstatus.InstallConfig) (agentstatus.InstallResult, error) {
	res := agentstatus.InstallResult{Agent: agentstatus.Codex, Marker: cfg.Marker}
	path, err := resolvePath(cfg)
	if err != nil {
		res.Reason = err.Error()
		return res, nil
	}
	res.Path = path

	unlock, err := configfile.Lock(path)
	if err != nil {
		res.Reason = err.Error()
		return res, nil
	}
	defer unlock()

	root, err := readHooksFile(path)
	if err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	if _, err := configfile.Backup(path); err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	applyInstall(root, cfg.Endpoint, cfg.Marker)

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		res.Reason = fmt.Sprintf("marshal: %v", err)
		return res, nil
	}
	if err := configfile.Write(path, data); err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	warnIfFlagMissing(cfg)

	res.Installed = true
	return res, nil
}

func uninstallHooks(cfg agentstatus.InstallConfig) (agentstatus.InstallResult, error) {
	res := agentstatus.InstallResult{Agent: agentstatus.Codex, Marker: cfg.Marker}
	path, err := resolvePath(cfg)
	if err != nil {
		res.Reason = err.Error()
		return res, nil
	}
	res.Path = path

	unlock, err := configfile.Lock(path)
	if err != nil {
		res.Reason = err.Error()
		return res, nil
	}
	defer unlock()

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		res.Skipped = true
		res.Reason = "hooks.json not found"
		return res, nil
	}

	root, err := readHooksFile(path)
	if err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	if _, err := configfile.Backup(path); err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	applyUninstall(root, cfg.Marker)

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		res.Reason = fmt.Sprintf("marshal: %v", err)
		return res, nil
	}
	if err := configfile.Write(path, data); err != nil {
		res.Reason = err.Error()
		return res, nil
	}
	res.Reason = "hooks removed"
	return res, nil
}

func readHooksFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("malformed hooks.json: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func applyInstall(root map[string]any, endpoint, marker string) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	wantCmd := hookCommand(endpoint)

	for _, event := range installedEvents {
		groups, _ := hooks[event].([]any)
		updated := false
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				continue
			}
			inner, _ := group["hooks"].([]any)
			for _, h := range inner {
				hm, ok := h.(map[string]any)
				if !ok {
					continue
				}
				if m, _ := hm[markerField].(string); m == marker {
					hm["command"] = wantCmd
					updated = true
				}
			}
		}
		if updated {
			hooks[event] = groups
			continue
		}
		groups = append(groups, map[string]any{
			"matcher": "",
			"hooks": []any{
				map[string]any{
					"type":      "command",
					"command":   wantCmd,
					"timeout":   10,
					markerField: marker,
				},
			},
		})
		hooks[event] = groups
	}
}

func applyUninstall(root map[string]any, marker string) {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		return
	}
	for event, raw := range hooks {
		groups, ok := raw.([]any)
		if !ok {
			continue
		}
		filteredGroups := groups[:0]
		for _, g := range groups {
			group, ok := g.(map[string]any)
			if !ok {
				filteredGroups = append(filteredGroups, g)
				continue
			}
			inner, _ := group["hooks"].([]any)
			keptInner := inner[:0]
			for _, h := range inner {
				hm, ok := h.(map[string]any)
				if !ok {
					keptInner = append(keptInner, h)
					continue
				}
				if m, _ := hm[markerField].(string); m == marker {
					continue
				}
				keptInner = append(keptInner, h)
			}
			if len(keptInner) == 0 {
				continue
			}
			group["hooks"] = keptInner
			filteredGroups = append(filteredGroups, group)
		}
		if len(filteredGroups) == 0 {
			delete(hooks, event)
			continue
		}
		hooks[event] = filteredGroups
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
}

// codexHooksPattern matches `codex_hooks = true` (with optional spaces) in a
// TOML line. Used only within a [features] section — see checkCodexHooksFlag.
var codexHooksPattern = regexp.MustCompile(`codex_hooks\s*=\s*true`)

// checkCodexHooksFlag does a lightweight line-based scan of config.toml to
// detect whether `[features] codex_hooks = true` is set. This is a
// warning-only path; a full TOML parser is not warranted.
func checkCodexHooksFlag(base string) bool {
	path := filepath.Join(base, ".codex", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	inFeatures := false
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "[") {
			inFeatures = line == "[features]"
			continue
		}
		if inFeatures && codexHooksPattern.MatchString(line) {
			return true
		}
	}
	return false
}

func warnIfFlagMissing(cfg agentstatus.InstallConfig) {
	base, err := resolveBaseDir(cfg)
	if err != nil {
		return
	}
	if !checkCodexHooksFlag(base) {
		slog.Default().Warn(
			"codex_hooks feature flag not detected in config.toml; hooks will not fire until you add: [features]\ncodex_hooks = true",
		)
	}
}
