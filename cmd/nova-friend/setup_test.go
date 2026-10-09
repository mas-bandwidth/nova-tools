package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupProfileTokenDatabaseFollowsNativeHomeAndExplicitOverride(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		bound bool
		db    string
		want  string
	}{
		{"legacy", false, "", "/host/.local/share/opencode/opencode.db"},
		{"private", true, "", "/private/.local/share/opencode/opencode.db"},
		{"explicit", true, "/chosen.db", "/chosen.db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := world{home: "/host", getenv: func(string) string { return "" }}
			if tc.bound {
				w.setupProfile = &friend.SetupProfile{Selection: friend.SetupOptions{Home: "/private"}}
			}
			cli := friendTool(w)
			for i := range cli.Verbs {
				if cli.Verbs[i].Name == "run" {
					cli.Verbs[i].Run = func(c *tool.Call) *tool.Out { return tool.Done().Fact("db", w.selectedOpenCodeDB(c)) }
				}
			}
			args := []string{"run", "--as", "bob", "--harness", "opencode", "--dir", "/work"}
			if tc.db != "" {
				args = append(args, "--db", tc.db)
			}
			testkit.Main(cli.Run).Do(t, args...).Exit(0).Out("db=" + tc.want)
		})
	}
}

func TestSetupInspectionAndDryRunPerformNoNativeCallsOrWrites(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	fs := friend.NewMemFS()
	w.settings = fs
	calls := 0
	w.setupPlanner = func(friend.SetupOptions, friend.SettingsFS) (friend.SetupPlan, error) {
		return friend.SetupPlan{Harness: "opencode", Steps: []friend.SetupStep{{Name: "configure", Kind: "file", Apply: func() ([]friend.SetupFact, error) { calls++; return nil, nil }}}}, nil
	}
	w.setupRunner = &friend.SetupRunner{FS: fs, Exec: func(context.Context, string, string, []string, string) (string, int, error) {
		calls++
		return "", 0, nil
	}, Identity: func(int) string { return "birth" }, GroupRunnable: func(int) bool { return false }, Lock: func(string) (func() error, error) { calls++; return func() error { return nil }, nil }, Nonce: func() string { return "run" }}
	base := []string{"setup", "--as", "bob", "--harness", "opencode", "--dir", "/work", "--config-dir", "/config", "--home", "/private", "--profile", "isolated", "--program", "/native", "--deny-self", "/coordinator", "--profile-only"}
	cli := cliOf(w)
	cli.Do(t, base...).Exit(0).Out("SETUP OK", "applied=false", "presence", "not_checked")
	cli.Do(t, append(base, "--apply", "--dry-run")...).Exit(0).Out("applied=false")
	refused := append(append([]string{}, base...), "--model", "ignored", "--apply", "--dry-run")
	cli.Do(t, refused...).Exit(2).Err("profile-only")
	assert.Zero(t, calls)
	entry, err := fs.Lstat("/config")
	assert.NoError(t, err)
	assert.Equal(t, friend.KindMissing, entry.Kind)
	assert.Empty(t, r.launchctl)
}

func TestSetupClaudeManifestFlagsBindOwnedCopyWithoutPublishingInputPath(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	w.settings = friend.NewMemFS()
	var got friend.SetupOptions
	w.setupPlanner = func(o friend.SetupOptions, _ friend.SettingsFS) (friend.SetupPlan, error) {
		got = o
		return friend.SetupPlan{Harness: o.Harness}, nil
	}
	cliOf(w).Do(t, "setup", "--as", "bob", "--harness", "claude", "--dir", "/work", "--config-dir", "/config", "--home", "/private", "--profile", "subscription-token", "--program", "/native/claude", "--deny-self", "/coordinator", "--provider", "claude.ai", "--model", "claude.ai/token-default", "--key-env", "CLAUDE_CODE_OAUTH_TOKEN", "--secrets", "CLAUDE_CODE_OAUTH_TOKEN", "--seat", "target-a", "--claude-account-id", "account-a", "--claude-machine", "mini", "--claude-accounts", "/reviewed/manifest.tsv", "--claude-accounts-sha256", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--dry-run").Exit(0).Out("applied=false")
	assert.Equal(t, "account-a", got.ClaudeAccountID)
	assert.Equal(t, "mini", got.ClaudeMachine)
	assert.Equal(t, "/reviewed/manifest.tsv", got.ClaudeAccountsSource)
	assert.Equal(t, friend.ClaudeAccountsOwnedPath("/config"), got.ClaudeAccountsPath)
	assert.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", got.ClaudeAccountsSHA256)
}

func TestSetupApplyFailureDoesNotClaimCompletionOrPublishProfile(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	w.setupPlanner = func(friend.SetupOptions, friend.SettingsFS) (friend.SetupPlan, error) {
		return friend.SetupPlan{Harness: "opencode", Steps: []friend.SetupStep{{Name: "refused", Apply: func() ([]friend.SetupFact, error) {
			return []friend.SetupFact{{Name: "partial", State: "written"}}, errors.New("fixture failure")
		}}}}, nil
	}
	w.setupRunner = &friend.SetupRunner{FS: r.fs, Exec: func(context.Context, string, string, []string, string) (string, int, error) {
		require.FailNow(t, "no native step exists")
		return "", 0, nil
	}, Identity: func(int) string { return "birth" }, GroupRunnable: func(int) bool { return false }, Lock: func(string) (func() error, error) { return func() error { return nil }, nil }, Nonce: func() string { return "run" }}
	cliOf(w).Do(t, "setup", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--config-dir", "/config", "--home", "/private", "--profile", "isolated", "--program", "/native", "--deny-self", "/coordinator", "--profile-only", "--apply").Exit(1).Err("apply_requested=true", "applied=false")
	entry, err := r.fs.Lstat(friend.SetupProfilePath("/config"))
	require.NoError(t, err)
	assert.Equal(t, friend.KindMissing, entry.Kind)
	assert.Empty(t, r.launchctl)
}

func TestSetupProfileInstallUsesPrivateNativeEnvironmentAndHostDeploymentPaths(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.onPath = map[string]string{"nova-secrets": "/native/nova-secrets", "sops": "/native/sops"}
	opts := friend.SetupOptions{Harness: "opencode", Friend: "bob", Dir: "/w/bob", ConfigDir: "/config", Home: "/private", Profile: "isolated", Program: "/native/opencode", DenySelf: []string{"/coordinator"}, Provider: "inception", Model: "inception/selected", Package: "@ai-sdk/openai-compatible", BaseURL: "https://example.test/v1", KeyEnv: "INCEPTION_API_KEY", SecretNames: []string{"INCEPTION_API_KEY"}, Seat: "seat"}
	plan, err := friend.PlanSetup(opts, r.fs, setupPlanner)
	require.NoError(t, err)
	// The generic native process binding is covered independently; this twin
	// applies the inspected filesystem steps without contacting a provider.
	for _, step := range plan.Steps {
		if step.Command != nil {
			// Public receipt twin; actual gated native proof is exercised by
			// TestNativeSetupReceiptTraceAndAllowedExitKeepStreamsSeparate.
			require.NoError(t, r.fs.WriteFile(filepath.Join(opts.ConfigDir, ".nova-friend-setup/RUN"), []byte(`{"run_id":"fixture","command_sha256":"hash","pid":42,"identity":"birth","ended":true}`), 0600))
		}
		if step.Apply != nil {
			_, err = step.Apply()
			require.NoError(t, err)
		}
	}
	profile, err := friend.ReadSetupProfile(r.fs, friend.SetupProfilePath(opts.ConfigDir))
	require.NoError(t, err)
	require.NoError(t, profile.ValidateUse("bob", "opencode", "/w/bob", r.fs, setupPlanner))
	a := friend.Agent{Home: r.home, Path: r.env["PATH"], Friend: "bob", Harness: "opencode", Dir: opts.Dir, SetupProfile: friend.SetupProfilePath(opts.ConfigDir), ConfigDir: opts.ConfigDir, DenySelf: opts.DenySelf, Secrets: opts.SecretNames, SecretsTool: "/native/nova-secrets", Sops: "/native/sops", NeedsEnv: opts.KeyEnv, Seat: opts.Seat, Environment: map[string]string{}}
	for _, item := range profile.Environment(r.env["PATH"]) {
		name, value, _ := strings.Cut(item, "=")
		a.Environment[name] = value
	}
	plist := a.Plist()
	assert.Contains(t, plist, "<key>HOME</key><string>/private</string>")
	assert.Contains(t, plist, "<key>OPENCODE_CONFIG_DIR</key><string>/config</string>")
	assert.Contains(t, plist, "<key>OPENCODE_CONFIG</key><string>/w/bob/opencode.json</string>")
	assert.Contains(t, plist, "<key>WorkingDirectory</key><string>/private</string>")
	assert.Equal(t, filepath.Join(r.home, "Library/LaunchAgents", a.Label()+".plist"), a.PlistPath())
	assert.Contains(t, strings.Join(a.Args(), " "), "--setup-profile /config/.nova-friend-setup/PROFILE")
	assert.Contains(t, strings.Join(a.Args(), " "), r.home)
	assert.NotContains(t, plist, "synthetic credential value")
	cliOf(r.world()).Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--setup-profile", friend.SetupProfilePath(opts.ConfigDir), "--dry-run").Exit(0).Out("--needs-env INCEPTION_API_KEY", "--deny-self /coordinator", "--setup-profile /config/.nova-friend-setup/PROFILE", "--state-dir "+friend.DefaultStateDir(r.home, "bob"))
	cliOf(r.world()).Do(t, "install", "--as", "ada", "--harness", "opencode", "--dir", "/w/bob", "--setup-profile", friend.SetupProfilePath(opts.ConfigDir), "--dry-run").Exit(2).Err("owner/harness/working directory")
	assert.Empty(t, r.launchctl)
}

func TestInstallPersistsWallRootsAndModelRequiredSecretNames(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.onPath = map[string]string{"nova-secrets": "/native/nova-secrets", "sops": "/native/sops"}
	cli := r.cli()
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--model", "inception/selected", "--deny-self", "/coordinator", "--secrets", "OTHER_KEY", "--seat", "seat", "--dry-run").Exit(2).Err("omits required INCEPTION_API_KEY")
	cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--model", "inception/selected", "--deny-self", "/coordinator", "--secrets", "INCEPTION_API_KEY", "--seat", "seat", "--dry-run").Exit(0).Out("--needs-env INCEPTION_API_KEY", "--deny-self /coordinator")
}
