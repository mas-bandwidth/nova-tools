package main

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

func (w world) withSetupProfile(c *tool.Call, installing bool) (world, error) {
	path := c.Str("setup-profile")
	if path == "" {
		return w, nil
	}
	// Profile validation reads only; an early refusal still inspects the
	// caller's dry-run request before tool's no-write contract is rendered.
	c.DryRun()
	fs := w.settings
	if fs == nil {
		fs = friend.OSFS{}
	}
	profile, err := friend.ReadSetupProfile(fs, path)
	if err != nil {
		return w, err
	}
	dir, err := filepath.Abs(c.Str("dir"))
	if err != nil {
		return w, err
	}
	if err = profile.ValidateUse(c.Str("as"), c.Str("harness"), dir, fs, setupPlanner); err != nil {
		return w, err
	}
	o := profile.Selection
	if (c.Given("model") && c.Str("model") != o.Model) || (c.Given("config-dir") && c.Str("config-dir") != o.ConfigDir) || (c.Given("needs-env") && c.Str("needs-env") != o.KeyEnv) {
		return w, errors.New("flags conflict with completed setup selection; use the matching PROFILE without overriding its model/config/required key")
	}
	if roots := commaList(c.Str("deny-self")); c.Given("deny-self") && !slices.Equal(roots, o.DenySelf) {
		return w, errors.New("--deny-self conflicts with setup wall roots; preserve the completed profile's explicit exclusions")
	}
	if installing {
		if names := secretNames(c.Str("secrets")); len(names) > 0 && !slices.Equal(names, o.SecretNames) {
			return w, errors.New("--secrets conflicts with setup names; use the matching PROFILE without widening secret access")
		}
		if seat := c.Str("seat"); seat != "" && seat != o.Seat {
			return w, errors.New("--seat conflicts with setup secret seat; use its selected seat")
		}
	}
	w.setupProfile = &profile
	getenv := w.getenv
	selected := map[string]string{}
	for _, item := range profile.Environment(getenv("PATH")) {
		name, value, _ := strings.Cut(item, "=")
		selected[name] = value
	}
	w.getenv = func(name string) string {
		if value, ok := selected[name]; ok {
			return value
		}
		return getenv(name)
	}
	w.exec = profile.Exec(w.exec, getenv)
	return w, nil
}

func (w world) selectedModel(c *tool.Call) string {
	if w.setupProfile != nil {
		return w.setupProfile.Selection.Model
	}
	return c.Str("model")
}

func (w world) selectedOpenCodeDB(c *tool.Call) string {
	if w.setupProfile != nil && !c.Given("db") {
		return filepath.Join(w.setupProfile.Selection.Home, ".local", "share", "opencode", "opencode.db")
	}
	return c.Str("db")
}
func (w world) selectedNeedsEnv(c *tool.Call) []string {
	if w.setupProfile != nil {
		return friend.NeedsEnvOf(w.setupProfile.Selection.KeyEnv, "")
	}
	return friend.NeedsEnvOf(c.Str("needs-env"), c.Str("model"))
}

func (w world) setupProfileCommand(program string, args []string) (string, []string) {
	o := w.setupProfile.Selection
	if program == o.Harness || (o.Harness == "dsh" && program == friend.DSHProgram) {
		program = o.Program
	}
	if program == "env" && len(args) > 1 && args[1] == "claude" {
		args = slices.Clone(args)
		args[1] = o.Program
	}
	return program, args
}
