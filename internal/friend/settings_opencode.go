package friend

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// openCodeSettings: the project config <dir>/opencode.json is not a
// symlink, allows the friend's directory to a tool call
// (permission.external_directory, what AllowDirs writes before each turn),
// and, when --model names one, holds that model. Each is merged into what
// the file holds.
func openCodeSettings(h HarnessSettings) ([]setting, error) {
	file := filepath.Join(h.Dir, OpenCodeConfig)
	pattern := strings.TrimRight(h.Dir, "/") + "/**"
	out := []setting{h.realFile(file), h.openCodeKey(file, "permission.external_directory."+pattern, "allow",
		func(cfg map[string]any) string {
			perm, _ := cfg["permission"].(map[string]any)
			if s, ok := perm["external_directory"].(string); ok && s == "allow" {
				return "allow" // every directory is allowed
			}
			allowed, _ := perm["external_directory"].(map[string]any)
			v, _ := allowed[pattern].(string)
			return v
		},
		func(cfg map[string]any) {
			perm, _ := cfg["permission"].(map[string]any)
			if perm == nil {
				perm = map[string]any{}
			}
			allowed, _ := perm["external_directory"].(map[string]any)
			if allowed == nil {
				allowed = map[string]any{}
			}
			allowed[pattern], perm["external_directory"], cfg["permission"] = "allow", allowed, perm
		})}
	if h.Model != "" {
		out = append(out, h.openCodeKey(file, "model", h.Model,
			func(cfg map[string]any) string { v, _ := cfg["model"].(string); return v },
			func(cfg map[string]any) { cfg["model"] = h.Model }))
	}
	return out, nil
}

// openCodeKey is one setting in the project config: get reads it from the
// parsed file, set puts it there.
func (h HarnessSettings) openCodeKey(file, name, want string, get func(map[string]any) string, set func(map[string]any)) setting {
	return setting{
		Setting: Setting{Harness: h.Harness, File: file, Name: name, Want: want},
		read: func() (string, error) {
			cfg, err := h.openCodeConfig(file)
			if err != nil {
				return "", err
			}
			if v := get(cfg); v != "" {
				return v, nil
			}
			return KindMissing, nil
		},
		write: func() error {
			cfg, err := h.openCodeConfig(file)
			if err != nil {
				return err
			}
			set(cfg)
			raw, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return err
			}
			return h.writeConfig(file, string(raw)+"\n", 0o644)
		},
	}
}

// openCodeConfig is the project config as a JSON object; a missing file is
// one holding only the schema.
func (h HarnessSettings) openCodeConfig(file string) (map[string]any, error) {
	text, err := h.readOrEmpty(file)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return map[string]any{"$schema": "https://opencode.ai/config.json"}, nil
	}
	cfg := map[string]any{}
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %v", file, err)
	}
	return cfg, nil
}
