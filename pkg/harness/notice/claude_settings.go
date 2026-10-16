package notice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// mergeClaudeHook adds one command for event to settings.json. An existing
// file keeps its other keys. The command is added once per event.
func mergeClaudeHook(path, event, command string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	root := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("claude settings is not a JSON object: %w", err)
		}
		if root == nil {
			return errors.New("claude settings is not a JSON object")
		}
	}
	hooks, err := objectField(root, "hooks")
	if err != nil {
		return err
	}
	groups, err := arrayField(hooks, event)
	if err != nil {
		return err
	}
	if commandPresent(groups, command) {
		return nil
	}
	groups = append(groups, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": command}},
	})
	hooks[event] = groups
	root["hooks"] = hooks
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return atomicfile.Write(path, out, 0o600, atomicfile.ExactMode())
}

// mergeClaudeEnv sets one variable in settings.json's env, keeping the file's
// other keys and variables.
func mergeClaudeEnv(path, name, value string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	root := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("claude settings is not a JSON object: %w", err)
		}
		if root == nil {
			return errors.New("claude settings is not a JSON object")
		}
	}
	env, err := objectField(root, "env")
	if err != nil {
		return err
	}
	if env[name] == value {
		return nil
	}
	env[name] = value
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return atomicfile.Write(path, out, 0o600, atomicfile.ExactMode())
}

func objectField(root map[string]any, key string) (map[string]any, error) {
	v, ok := root[key]
	if !ok || v == nil {
		m := map[string]any{}
		root[key] = m
		return m, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("claude settings %s wants an object", key)
	}
	return m, nil
}

func arrayField(root map[string]any, key string) ([]any, error) {
	v, ok := root[key]
	if !ok || v == nil {
		return nil, nil
	}
	groups, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("claude settings %s wants an array", key)
	}
	return groups, nil
}

func commandPresent(groups []any, command string) bool {
	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hooks, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hooks {
			row, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if row["command"] == command {
				return true
			}
		}
	}
	return false
}
