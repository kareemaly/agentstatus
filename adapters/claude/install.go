package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	agentstatus "github.com/kareemaly/agentstatus"
	"github.com/kareemaly/agentstatus/internal/configfile"
)

// installedEvents is the set of Claude hook events we register. These are the
// events MapHookEvent either maps to a Signal or explicitly cares about. The
// events in droppedByDesign (environment + agent-team workflow) are NOT
// installed — we don't want Claude firing hooks we'd only drop.
var installedEvents = []string{
	"SessionStart", "SessionEnd", "UserPromptSubmit",
	"PreToolUse", "PostToolUse", "PostToolUseFailure",
	"Stop", "StopFailure",
	"Notification", "PermissionRequest", "PermissionDenied",
	"SubagentStart", "SubagentStop",
	"Elicitation", "ElicitationResult",
}

// managedMarker is the JSON field this library stamps on every inner hook it
// installs. Uninstall removes only entries carrying this marker; user-authored
// entries are left untouched.
const managedMarker = "agentstatusManaged"

func hookCommand(endpoint string) string {
	return fmt.Sprintf(
		"curl -s -X POST --max-time 5 -H 'Content-Type: application/json' --data-binary @- %s/claude",
		endpoint,
	)
}

// resolvePath returns the Claude settings.json target for the given config.
// Precedence: Project > ConfigRoot > os.UserHomeDir.
func resolvePath(cfg agentstatus.InstallConfig) (string, error) {
	switch {
	case cfg.Project != "":
		return filepath.Join(cfg.Project, ".claude", "settings.json"), nil
	case cfg.ConfigRoot != "":
		return filepath.Join(cfg.ConfigRoot, ".claude", "settings.json"), nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	}
}

func installHooks(cfg agentstatus.InstallConfig) (agentstatus.InstallResult, error) {
	res := agentstatus.InstallResult{Agent: agentstatus.Claude}
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

	root, err := readSettings(path)
	if err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	if _, err := configfile.Backup(path); err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	applyInstall(root, cfg.Endpoint)

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		res.Reason = fmt.Sprintf("marshal: %v", err)
		return res, nil
	}
	if err := configfile.Write(path, data); err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	if cfg.Project == "" {
		warnIfProjectOverrides(path)
	}

	res.Installed = true
	return res, nil
}

func uninstallHooks(cfg agentstatus.InstallConfig) (agentstatus.InstallResult, error) {
	res := agentstatus.InstallResult{Agent: agentstatus.Claude}
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
		res.Reason = "settings.json not found"
		return res, nil
	}

	root, err := readSettings(path)
	if err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	if _, err := configfile.Backup(path); err != nil {
		res.Reason = err.Error()
		return res, nil
	}

	applyUninstall(root)

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

func readSettings(path string) (map[string]any, error) {
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
		return nil, fmt.Errorf("malformed settings.json: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func applyInstall(root map[string]any, endpoint string) {
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
				if managed, _ := hm[managedMarker].(bool); managed {
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
					"type":        "command",
					"command":     wantCmd,
					"timeout":     10,
					managedMarker: true,
				},
			},
		})
		hooks[event] = groups
	}
}

func applyUninstall(root map[string]any) {
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
				if managed, _ := hm[managedMarker].(bool); managed {
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

func warnIfProjectOverrides(target string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	projectPath := filepath.Join(cwd, ".claude", "settings.json")
	if projectPath == target {
		return
	}
	if _, err := os.Stat(projectPath); err != nil {
		return
	}
	slog.Default().Warn(
		"agentstatus: user-level hooks installed, but a project-level .claude/settings.json exists and may override them",
		"user_settings", target,
		"project_settings", projectPath,
	)
}
