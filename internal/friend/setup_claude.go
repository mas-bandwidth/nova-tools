package friend

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// PlanClaudeSetup keeps each Claude subscription in its own config root.
// Planning is read-only: native auth status runs only as an explicit apply step.
func PlanClaudeSetup(opts SetupOptions, fsys SettingsFS) (SetupPlan, error) {
	if fsys == nil {
		fsys = OSFS{}
	}
	if opts.Harness != "" && opts.Harness != "claude" {
		return SetupPlan{}, fmt.Errorf("Claude setup wants harness claude, got %q", opts.Harness)
	}
	profileOnly := opts.ProfileOnly && opts.Provider == "" && opts.Model == ""
	defaultRoute := ClaudeAccountSetupRoute(opts)
	if (!profileOnly && !defaultRoute) || opts.Package != "" || opts.BaseURL != "" || opts.KeyEnv != "" || opts.Seat != "" || len(opts.SecretNames) != 0 || opts.ContextLimit != 0 || opts.OutputLimit != 0 {
		return SetupPlan{}, errors.New("Claude setup wants --profile-only without route inputs or the explicit claude.ai/default subscription route; remove API key, seat, package, endpoint and limit inputs")
	}
	if opts.ConfigDir == "" || !filepath.IsAbs(opts.ConfigDir) || filepath.Clean(opts.ConfigDir) != opts.ConfigDir {
		return SetupPlan{}, errors.New("Claude setup wants --config-dir as a clean absolute path to this friend's own config directory")
	}
	if opts.ConfigDir == string(filepath.Separator) || opts.ConfigDir == opts.Home || opts.ConfigDir == opts.Dir {
		return SetupPlan{}, errors.New("Claude setup wants a dedicated config directory, separate from home and working directory")
	}
	if opts.Home == "" || !filepath.IsAbs(opts.Home) || filepath.Clean(opts.Home) != opts.Home {
		return SetupPlan{}, errors.New("Claude setup wants an absolute home for the native auth probe")
	}
	if opts.Program == "" || !filepath.IsAbs(opts.Program) {
		return SetupPlan{}, errors.New("Claude setup wants the installed claude program as an absolute path")
	}
	if err := claudeRootIsSafe(fsys, opts.ConfigDir); err != nil {
		return SetupPlan{}, err
	}
	if err := claudeRootIsSafe(fsys, opts.Home); err != nil {
		return SetupPlan{}, err
	}
	homeEntry, err := fsys.Lstat(opts.Home)
	if err != nil {
		return SetupPlan{}, fmt.Errorf("inspect Claude setup home: %w", err)
	}
	if homeEntry.Kind != KindDir && homeEntry.Kind != KindMissing {
		return SetupPlan{}, fmt.Errorf("Claude setup home %s is %s; name a private real directory", opts.Home, homeEntry.Kind)
	}

	root := opts.ConfigDir
	login := claudeLoginCommand(opts.Program, root, opts.Home)
	plan := SetupPlan{Harness: "claude", Facts: []SetupFact{
		{Name: "auth", State: "not_checked", Detail: "native status runs only during explicit setup apply"},
		{Name: "subscription", State: "not_checked"},
		{Name: "provider_turn", State: "not_checked"},
		{Name: "login_command", State: "if_required", Detail: login},
	}}
	if defaultRoute {
		plan.Facts = append(plan.Facts,
			SetupFact{Name: "route", State: "selected_unverified", Detail: "claude.ai/default"},
			SetupFact{Name: "model_resolution", State: "not_checked", Detail: "account default resolves only on a provider turn"})
	}
	if homeEntry.Kind == KindMissing {
		home := opts.Home
		plan.Steps = append(plan.Steps, SetupStep{
			Name: "claude_private_home", Kind: "local_write", Path: home, Want: "private directory",
			Apply: func() ([]SetupFact, error) {
				if err := claudeRootIsSafe(fsys, home); err != nil {
					return nil, err
				}
				if err := fsys.MkdirAll(home, 0o700); err != nil {
					return nil, fmt.Errorf("make Claude private home: %w", err)
				}
				return []SetupFact{{Name: "private_home", State: "created", Detail: home}}, nil
			},
		})
	}
	entry, err := fsys.Lstat(root)
	if err != nil {
		return SetupPlan{}, fmt.Errorf("inspect Claude config directory: %w", err)
	}
	if entry.Kind == KindMissing {
		plan.Steps = append(plan.Steps, SetupStep{
			Name: "claude_config_dir", Kind: "local_write", Path: root, Want: "private directory",
			Apply: func() ([]SetupFact, error) {
				if err := claudeRootIsSafe(fsys, root); err != nil {
					return nil, err
				}
				if err := fsys.MkdirAll(root, 0o700); err != nil {
					return nil, fmt.Errorf("make Claude config directory: %w", err)
				}
				return []SetupFact{{Name: "config_dir", State: "created", Detail: root}}, nil
			},
		})
	}
	for _, relative := range []string{".config", ".cache", ".local/share", ".local/state", "tmp"} {
		runtimeDir := filepath.Join(opts.Home, relative)
		if err := claudeRootIsSafe(fsys, runtimeDir); err != nil {
			return SetupPlan{}, err
		}
		entry, err := fsys.Lstat(runtimeDir)
		if err != nil {
			return SetupPlan{}, fmt.Errorf("inspect Claude runtime directory: %w", err)
		}
		if entry.Kind == KindMissing {
			plan.Steps = append(plan.Steps, SetupStep{
				Name: "claude_runtime_dir", Kind: "local_write", Path: runtimeDir, Want: "private directory",
				Apply: func() ([]SetupFact, error) {
					if err := claudeRootIsSafe(fsys, runtimeDir); err != nil {
						return nil, err
					}
					if err := fsys.MkdirAll(runtimeDir, 0o700); err != nil {
						return nil, fmt.Errorf("make Claude runtime directory: %w", err)
					}
					return []SetupFact{{Name: "runtime_dir", State: "created", Detail: runtimeDir}}, nil
				},
			})
		}
	}
	plan.Steps = append(plan.Steps, SetupStep{
		Name: "claude_auth_status", Kind: "native_check", Path: root,
		Want: "local claude.ai subscription authentication; native status may write inside the owned config directory",
		Command: &SetupCommand{
			Program:      opts.Program,
			Args:         []string{"auth", "status", "--json"},
			AllowedExits: []int{0, 1},
			Env: []string{
				"HOME=" + opts.Home,
				"CLAUDE_CONFIG_DIR=" + root,
				"XDG_CONFIG_HOME=" + filepath.Join(opts.Home, ".config"),
				"XDG_CACHE_HOME=" + filepath.Join(opts.Home, ".cache"),
				"XDG_DATA_HOME=" + filepath.Join(opts.Home, ".local/share"),
				"XDG_STATE_HOME=" + filepath.Join(opts.Home, ".local/state"),
				"TMPDIR=" + filepath.Join(opts.Home, "tmp"),
				"LANG=en_US.UTF-8",
				"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
			},
			Dir: opts.Home, Timeout: 30 * time.Second,
		},
		Validate: func(out SetupOutput) ([]SetupFact, error) {
			facts, err := validateClaudeAuthStatus(out, login)
			if err != nil || !defaultRoute {
				return facts, err
			}
			if len(facts) == 0 || facts[0].State != "local_claude_ai_auth" {
				return facts, errors.New("Claude default route needs local claude.ai sign-in; run the printed login command and reapply setup")
			}
			return facts, nil
		},
	})
	return plan, nil
}

// ClaudeAccountSetupRoute selects only the account's subscription default.
// It never names an API credential, endpoint, or billable Console route.
func ClaudeAccountSetupRoute(opts SetupOptions) bool {
	return opts.Harness == "claude" && !opts.ProfileOnly && opts.Provider == "claude.ai" && opts.Model == "claude.ai/default" &&
		opts.Package == "" && opts.BaseURL == "" && opts.KeyEnv == "" && opts.Seat == "" && len(opts.SecretNames) == 0 &&
		opts.ContextLimit == 0 && opts.OutputLimit == 0
}

// claudeRootIsSafe refuses symlinks along the named config path. An account
// root must not resolve through another location during setup.
func claudeRootIsSafe(fsys SettingsFS, root string) error {
	for p := root; p != filepath.Dir(p); p = filepath.Dir(p) {
		e, err := fsys.Lstat(p)
		if err != nil {
			return fmt.Errorf("inspect Claude config path: %w", err)
		}
		if e.Kind != KindDir && e.Kind != KindMissing {
			return fmt.Errorf("Claude config path %s is %s; name a real directory", p, e.Kind)
		}
	}
	return nil
}

// validateClaudeAuthStatus consumes only the native status shape. Native output
// is never echoed, because it may contain account identifiers.
func validateClaudeAuthStatus(out SetupOutput, login string) ([]SetupFact, error) {
	if out.Err != nil || (out.Exit != 0 && out.Exit != 1) {
		return nil, errors.New("Claude native auth status failed; inspect the local CLI and retry setup apply")
	}
	if len(out.Stdout) > 32<<10 {
		return nil, errors.New("Claude native auth status output is too large; inspect the local CLI and retry setup apply")
	}
	loggedIn, authMethod, err := claudeStatusFields(out.Stdout)
	if err != nil || loggedIn == nil || authMethod == nil {
		return nil, errors.New("Claude native auth status returned incomplete JSON; retry setup apply")
	}
	if !*loggedIn {
		if *authMethod != "none" {
			return nil, errors.New("Claude native auth status is inconsistent; retry setup apply")
		}
		return []SetupFact{
			{Name: "auth", State: "requires_user_login", Detail: "Claude subscription sign-in is interactive"},
			{Name: "login_command", State: "next", Detail: login},
		}, nil
	}
	if out.Exit != 0 {
		return nil, errors.New("Claude native auth status exited unsuccessfully despite reporting login; retry setup apply")
	}
	if *authMethod != "claude.ai" {
		return []SetupFact{
			{Name: "auth", State: "wrong_method", Detail: "Claude is signed in through a method other than claude.ai subscription"},
			{Name: "login_command", State: "next", Detail: login},
		}, nil
	}
	return []SetupFact{
		{Name: "auth", State: "local_claude_ai_auth", Detail: "native status reports claude.ai"},
		{Name: "subscription", State: "unverified", Detail: "local auth status does not verify subscription entitlement"},
	}, nil
}

// claudeStatusFields rejects duplicate and case-folded status members before
// classifying native authentication; unknown native fields remain private.
func claudeStatusFields(body string) (*bool, *string, error) {
	decoder := json.NewDecoder(strings.NewReader(body))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, nil, errors.New("status is not one JSON object")
	}
	seen := map[string]bool{}
	var loggedIn *bool
	var authMethod *string
	for decoder.More() {
		member, err := decoder.Token()
		if err != nil {
			return nil, nil, err
		}
		name, ok := member.(string)
		if !ok || seen[strings.ToLower(name)] {
			return nil, nil, errors.New("status has duplicate members")
		}
		seen[strings.ToLower(name)] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, nil, err
		}
		switch name {
		case "loggedIn":
			if err := json.Unmarshal(raw, &loggedIn); err != nil {
				return nil, nil, err
			}
		case "authMethod":
			if err := json.Unmarshal(raw, &authMethod); err != nil {
				return nil, nil, err
			}
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return nil, nil, errors.New("status object is incomplete")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, nil, errors.New("status has trailing JSON")
	}
	return loggedIn, authMethod, nil
}

// claudeLoginCommand is for display only; setup never executes it.
func claudeLoginCommand(program, root, home string) string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	assignments := []string{
		"HOME=" + home,
		"CLAUDE_CONFIG_DIR=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local/share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local/state"),
		"TMPDIR=" + filepath.Join(home, "tmp"),
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"LANG=en_US.UTF-8",
		"TERM=xterm-256color",
	}
	for i := range assignments {
		assignments[i] = quote(assignments[i])
	}
	return "env -i " + strings.Join(assignments, " ") + " " + quote(program) + " auth login --claudeai"
}
