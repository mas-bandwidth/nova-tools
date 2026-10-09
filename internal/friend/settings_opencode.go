package friend

import (
	"encoding/json"
	"fmt"
	"io"
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
		func(cfg map[string]any) (string, error) {
			perm, err := openCodePermission(cfg, file)
			if err != nil {
				return "", err
			}
			ext, exists := perm["external_directory"]
			if exists && ext == nil {
				return "", fmt.Errorf("%s: permission.external_directory is null; fix it before setup", file)
			}
			if s, ok := ext.(string); ok {
				if s == "allow" {
					return "allow", nil // every directory is allowed
				}
				return "", fmt.Errorf("%s: permission.external_directory is a non-allow scalar; setup will not replace a scalar permission, change it explicitly", file)
			}
			allowed, ok := ext.(map[string]any)
			if ext != nil && !ok {
				return "", fmt.Errorf("%s: permission.external_directory must be a JSON object or allow; fix it before setup", file)
			}
			if v, exists := allowed[pattern]; exists {
				if value, ok := v.(string); ok {
					if value != "allow" {
						return "", fmt.Errorf("%s: permission.external_directory for %s is non-allow; setup will not replace an explicit permission", file, pattern)
					}
					return value, nil
				}
				return "", fmt.Errorf("%s: permission.external_directory for %s must be a string; fix it before setup", file, pattern)
			}
			return KindMissing, nil
		},
		func(cfg map[string]any) error {
			perm, err := openCodePermission(cfg, file)
			if err != nil {
				return err
			}
			ext, exists := perm["external_directory"]
			if exists && ext == nil {
				return fmt.Errorf("%s: permission.external_directory is null; fix it before setup", file)
			}
			allowed, ok := ext.(map[string]any)
			if ext != nil && !ok {
				return fmt.Errorf("%s: permission.external_directory is a scalar; setup will not replace it", file)
			}
			if allowed == nil {
				allowed = map[string]any{}
			}
			if current, exists := allowed[pattern]; exists {
				if value, ok := current.(string); !ok || value != "allow" {
					return fmt.Errorf("%s: permission.external_directory for %s differs; setup will not replace an explicit permission", file, pattern)
				}
			}
			allowed[pattern], perm["external_directory"], cfg["permission"] = "allow", allowed, perm
			return nil
		})}
	if h.Model != "" {
		out = append(out, h.openCodeKey(file, "model", h.Model,
			func(cfg map[string]any) (string, error) { v, _ := cfg["model"].(string); return v, nil },
			func(cfg map[string]any) error { cfg["model"] = h.Model; return nil }))
	}
	return out, nil
}

func openCodePermission(cfg map[string]any, file string) (map[string]any, error) {
	v, exists := cfg["permission"]
	if !exists {
		return map[string]any{}, nil
	}
	perm, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: permission must be a JSON object; fix it before setup", file)
	}
	return perm, nil
}

// openCodeKey is one setting in the project config: get reads it from the
// parsed file, set puts it there.
func (h HarnessSettings) openCodeKey(file, name, want string, get func(map[string]any) (string, error), set func(map[string]any) error) setting {
	return setting{
		Setting: Setting{Harness: h.Harness, File: file, Name: name, Want: want},
		read: func() (string, error) {
			cfg, err := h.openCodeConfig(file)
			if err != nil {
				return "", err
			}
			v, err := get(cfg)
			if err != nil {
				return "", err
			}
			if v != "" {
				return v, nil
			}
			return KindMissing, nil
		},
		write: func() error {
			cfg, err := h.openCodeConfig(file)
			if err != nil {
				return err
			}
			if err := set(cfg); err != nil {
				return err
			}
			raw, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return err
			}
			return h.writeConfig(file, string(raw)+"\n", 0o600)
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
	return parseOpenCodeConfig(text, file)
}

// parseOpenCodeConfig preserves JSON numbers and refuses duplicate members at
// every depth before either settings writer merges the file. Errors never echo
// existing keys or values, which may include credentials.
func parseOpenCodeConfig(text, file string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	value, err := openCodeJSONValue(decoder, 0)
	if err != nil {
		return nil, fmt.Errorf("%s must contain one JSON object without duplicate keys; fix it before setup", file)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("%s must contain one JSON object without trailing content; fix it before setup", file)
	}
	cfg, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must contain one JSON object; fix it before setup", file)
	}
	return cfg, nil
}

func openCodeJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("JSON nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON key")
			}
			value, err := openCodeJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		_, err := decoder.Token()
		return object, err
	case '[':
		array := []any{}
		for decoder.More() {
			value, err := openCodeJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := decoder.Token()
		return array, err
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}
