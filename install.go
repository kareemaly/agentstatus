package agentstatus

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
)

var markerPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

// InstallHooks wires each requested agent's hook config to forward events to
// cfg.Endpoint. It never aborts on a single adapter's failure — per-agent
// issues are surfaced in the returned slice; the error is reserved for
// systemic problems (e.g. invalid config) that affect the whole run.
//
// Running InstallHooks twice against the same config yields the same on-disk
// result: adapters self-heal their entries and never duplicate.
func InstallHooks(cfg InstallConfig) ([]InstallResult, error) {
	return orchestrate(cfg, func(a Adapter) func(InstallConfig) (InstallResult, error) {
		return a.InstallHooks
	})
}

// UninstallHooks removes hook entries previously written by InstallHooks,
// leaving user-authored config untouched. Running UninstallHooks twice
// returns clean "not installed" results the second time.
func UninstallHooks(cfg InstallConfig) ([]InstallResult, error) {
	return orchestrate(cfg, func(a Adapter) func(InstallConfig) (InstallResult, error) {
		return a.UninstallHooks
	})
}

func orchestrate(
	cfg InstallConfig,
	pick func(Adapter) func(InstallConfig) (InstallResult, error),
) ([]InstallResult, error) {
	if err := validateEndpoint(cfg.Endpoint); err != nil {
		return nil, err
	}
	if err := validateMarker(cfg.Marker); err != nil {
		return nil, err
	}
	agents := cfg.Agents
	if len(agents) == 0 {
		agents = AllAgents
	}
	out := make([]InstallResult, 0, len(agents))
	for _, name := range agents {
		a, ok := lookupAdapter(name)
		if !ok {
			out = append(out, InstallResult{
				Agent:   name,
				Skipped: true,
				Reason:  "adapter not registered",
			})
			continue
		}
		fn := pick(a)
		if fn == nil {
			out = append(out, InstallResult{
				Agent:   name,
				Skipped: true,
				Reason:  "adapter does not support install",
			})
			continue
		}
		res, err := fn(cfg)
		if res.Agent == "" {
			res.Agent = name
		}
		if res.Marker == "" {
			res.Marker = cfg.Marker
		}
		if err != nil {
			res.Installed = false
			if res.Reason == "" {
				res.Reason = err.Error()
			}
		}
		out = append(out, res)
	}
	return out, nil
}

func validateMarker(marker string) error {
	if marker == "" {
		return errors.New("agentstatus: InstallConfig.Marker is required")
	}
	if !markerPattern.MatchString(marker) {
		return fmt.Errorf("agentstatus: InstallConfig.Marker %q must match ^[a-zA-Z0-9_-]{1,32}$", marker)
	}
	return nil
}

func validateEndpoint(endpoint string) error {
	if endpoint == "" {
		return errors.New("agentstatus: InstallConfig.Endpoint is required")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("agentstatus: invalid Endpoint: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("agentstatus: Endpoint %q must have scheme and host", endpoint)
	}
	return nil
}
