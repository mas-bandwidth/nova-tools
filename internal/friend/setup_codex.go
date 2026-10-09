package friend

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// PlanCodexSetup implements profile inspection in SPEC-CODEX-SUBSCRIPTION-SETUP.
// Native credentials stay in one host's owned CODEX_HOME; setup never enrolls it.
func PlanCodexSetup(o SetupOptions, fs SettingsFS) (SetupPlan, error) {
	if fs == nil {
		fs = OSFS{}
	}
	if o.Harness != "codex" || !o.ProfileOnly || o.Provider != "" || o.Model != "" || o.Package != "" || o.BaseURL != "" || o.KeyEnv != "" || o.Seat != "" || len(o.SecretNames) != 0 || o.ContextLimit != 0 || o.OutputLimit != 0 {
		return SetupPlan{}, errors.New("Codex subscription setup requires --profile-only without provider, model, token or API inputs; run: nova-friend setup -h")
	}
	for _, root := range []string{o.Home, o.ConfigDir, o.Dir} {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
			return SetupPlan{}, errors.New("Codex setup needs explicit absolute private roots and working directory; run: nova-friend setup -h")
		}
		if err := SetupPath(fs, root, true); err != nil {
			return SetupPlan{}, err
		}
	}
	if !filepath.IsAbs(o.Program) || o.Home == o.ConfigDir || o.Home == o.Dir || o.ConfigDir == o.Dir {
		return SetupPlan{}, errors.New("Codex setup needs separate private home/config roots and an absolute native executable; run: nova-friend setup -h")
	}
	env := []string{"HOME=" + o.Home, "CODEX_HOME=" + o.ConfigDir, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=en_US.UTF-8"}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	parts := []string{"env", "-i"}
	for _, item := range env {
		parts = append(parts, quote(item))
	}
	parts = append(parts, quote(o.Program), "login", "--device-auth")
	login := strings.Join(parts, " ")
	p := SetupPlan{Harness: "codex", Facts: []SetupFact{
		{Name: "auth", State: "not_checked", Detail: "native status runs only during explicit apply"},
		{Name: "subscription", State: "not_checked"}, {Name: "provider_turn", State: "not_checked"},
		{Name: "enrollment", State: "native_per_host", Detail: "each account/profile on each execution host owns its own refresh session"},
		{Name: "login_command", State: "if_required", Detail: login},
	}}
	for _, root := range []string{o.Home, o.ConfigDir} {
		entry, err := fs.Lstat(root)
		if err != nil {
			return SetupPlan{}, err
		}
		if entry.Kind != KindMissing {
			continue
		}
		p.Steps = append(p.Steps, SetupStep{Name: "codex_private_directory", Kind: "local_write", Path: root, Want: "private directory", Apply: func() ([]SetupFact, error) {
			if err := SetupPath(fs, root, true); err != nil {
				return nil, err
			}
			if err := fs.MkdirAll(root, 0700); err != nil {
				return nil, err
			}
			return []SetupFact{{Name: "private_directory", State: "created", Detail: root}}, nil
		}})
	}
	p.Steps = append(p.Steps, SetupStep{Name: "codex_auth_status", Kind: "native_check", Path: o.ConfigDir, Want: "native ChatGPT login status; no entitlement or identity proof", Command: &SetupCommand{Program: o.Program, Args: []string{"login", "status"}, Env: env, Dir: o.Home, Timeout: 30 * time.Second, AllowedExits: []int{0, 1}, StatusOnStderr: true}, Validate: func(out SetupOutput) ([]SetupFact, error) { return validateCodexAuthStatus(out, login) }})
	return p, nil
}

// validateCodexAuthStatus accepts only exact native method receipts; unknown
// status and API/enterprise methods never become subscription evidence (spec).
func validateCodexAuthStatus(out SetupOutput, login string) ([]SetupFact, error) {
	if out.Err != nil || len(out.Stdout) > 4096 {
		return nil, errors.New("Codex login status is unavailable or oversized; inspect the installed CLI and retry setup")
	}
	switch strings.TrimSpace(out.Stdout) {
	case "Logged in using ChatGPT":
		if out.Exit != 0 {
			break
		}
		return []SetupFact{{Name: "auth", State: "local_chatgpt_auth"}, {Name: "subscription", State: "unverified", Detail: "status does not verify selected account, allowance or model access"}}, nil
	case "Not logged in":
		if out.Exit != 1 {
			break
		}
		return []SetupFact{{Name: "auth", State: "requires_user_login"}, {Name: "login_command", State: "next", Detail: login}}, nil
	}
	return nil, errors.New("Codex subscription status is unrecognized or uses another auth method; inspect the native CLI and enroll this private profile through its printed device sign-in command")
}
