package friend

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claudeSetupOptions(root string) SetupOptions {
	return SetupOptions{
		Harness: "claude", Friend: "friend", Dir: "/friends/friend", ConfigDir: root,
		Home: "/friends/friend-home", Program: "/usr/local/bin/claude", ProfileOnly: true,
	}
}

// Plan must not invoke native status or create account files. A fresh root is
// created only by explicit apply, and status remains an ordered later step.
func TestPlanClaudeSetupIsReadOnly(t *testing.T) {
	t.Parallel()
	m := NewMemFS()
	require.NoError(t, m.MkdirAll("/friends/friend", 0o700))
	require.NoError(t, m.MkdirAll("/friends/friend-home", 0o700))
	plan, err := PlanClaudeSetup(claudeSetupOptions("/friends/claude-friend"), m)
	require.NoError(t, err)
	require.Len(t, plan.Steps, 7)
	assert.Equal(t, "not_checked", plan.Facts[0].State)
	assert.Equal(t, "local_write", plan.Steps[0].Kind)
	assert.Nil(t, plan.Steps[0].Command)
	assert.Equal(t, "native_check", plan.Steps[len(plan.Steps)-1].Kind)
	entry, err := m.Lstat("/friends/claude-friend")
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind)
	_, err = m.ReadFile("/friends/claude-friend/.claude.json")
	require.ErrorIs(t, err, os.ErrNotExist)

	facts, err := plan.Steps[0].Apply()
	require.NoError(t, err)
	assert.Equal(t, "created", facts[0].State)
	entry, err = m.Lstat("/friends/claude-friend")
	require.NoError(t, err)
	assert.Equal(t, KindDir, entry.Kind)
	_, err = m.ReadFile("/friends/claude-friend/.claude.json")
	require.ErrorIs(t, err, os.ErrNotExist, "directory setup must not create or copy account state")
}

// A status process inherits no API-key or OAuth override, and the login
// command is displayed as the native subscription flow, never executed.
func TestPlanClaudeSetupConfinesNativeStatus(t *testing.T) {
	t.Parallel()
	m := NewMemFS()
	require.NoError(t, m.MkdirAll("/friends/friend-home/.config", 0o700))
	require.NoError(t, m.MkdirAll("/friends/friend-home/.cache", 0o700))
	require.NoError(t, m.MkdirAll("/friends/friend-home/.local/share", 0o700))
	require.NoError(t, m.MkdirAll("/friends/friend-home/.local/state", 0o700))
	require.NoError(t, m.MkdirAll("/friends/friend-home/tmp", 0o700))
	require.NoError(t, m.MkdirAll("/friends/account's root", 0o700))
	plan, err := PlanClaudeSetup(claudeSetupOptions("/friends/account's root"), m)
	require.NoError(t, err)
	require.Len(t, plan.Steps, 1)
	step := plan.Steps[0]
	require.NotNil(t, step.Command)
	assert.Equal(t, "/usr/local/bin/claude", step.Command.Program)
	assert.Equal(t, []string{"auth", "status", "--json"}, step.Command.Args)
	assert.Equal(t, []int{0, 1}, step.Command.AllowedExits)
	assert.Equal(t, "/friends/friend-home", step.Command.Dir)
	assert.Contains(t, step.Command.Env, "CLAUDE_CONFIG_DIR=/friends/account's root")
	assert.Contains(t, step.Command.Env, "HOME=/friends/friend-home")
	assert.Contains(t, step.Command.Env, "XDG_CONFIG_HOME=/friends/friend-home/.config")
	assert.Contains(t, step.Command.Env, "XDG_CACHE_HOME=/friends/friend-home/.cache")
	assert.Contains(t, step.Command.Env, "TMPDIR=/friends/friend-home/tmp")
	for _, item := range step.Command.Env {
		assert.NotContains(t, item, "ANTHROPIC_API_KEY")
		assert.NotContains(t, item, "ANTHROPIC_AUTH_TOKEN")
		assert.NotContains(t, item, "CLAUDE_CODE_OAUTH_TOKEN")
	}
	assert.Contains(t, plan.Facts[3].Detail, "auth login --claudeai")
	assert.Contains(t, plan.Facts[3].Detail, "env -i")
	assert.Contains(t, plan.Facts[3].Detail, "'HOME=/friends/friend-home'")
	assert.Contains(t, plan.Facts[3].Detail, "'CLAUDE_CONFIG_DIR=/friends/account'\\''s root'")
	assert.Contains(t, plan.Facts[3].Detail, "'XDG_CONFIG_HOME=/friends/friend-home/.config'")
	assert.NotContains(t, strings.Join(step.Command.Args, " "), "login")
}

// Only an explicit claude.ai status is local auth evidence. Native
// output with account identifiers is never copied into a public fact/error.
func TestClaudeAuthStatusClassification(t *testing.T) {
	t.Parallel()
	login := "env 'CLAUDE_CONFIG_DIR=/friends/a' 'claude' auth login --claudeai"
	for _, tc := range []struct {
		name, stdout, wantState string
		exit                    int
		wantErr                 bool
	}{
		{name: "signed out", stdout: `{"loggedIn":false,"authMethod":"none","email":"private@example.com"}`, wantState: "requires_user_login"},
		{name: "Mini signed out exit one", stdout: `{"loggedIn":false,"authMethod":"none"}`, exit: 1, wantState: "requires_user_login"},
		{name: "claude ai auth", stdout: `{"loggedIn":true,"authMethod":"claude.ai","email":"private@example.com"}`, wantState: "local_claude_ai_auth"},
		{name: "API method", stdout: `{"loggedIn":true,"authMethod":"api_key"}`, wantState: "wrong_method"},
		{name: "inconsistent signed out", stdout: `{"loggedIn":false,"authMethod":"claude.ai"}`, exit: 1, wantErr: true},
		{name: "missing method", stdout: `{"loggedIn":true}`, wantErr: true},
		{name: "missing login flag", stdout: `{"authMethod":"claude.ai"}`, wantErr: true},
		{name: "duplicate login flag", stdout: `{"loggedIn":false,"loggedIn":true,"authMethod":"claude.ai"}`, wantErr: true},
		{name: "case folded login flag", stdout: `{"loggedIn":false,"LoggedIn":true,"authMethod":"claude.ai"}`, wantErr: true},
		{name: "duplicate method", stdout: `{"loggedIn":true,"authMethod":"none","authMethod":"claude.ai"}`, wantErr: true},
		{name: "trailing object", stdout: `{"loggedIn":true,"authMethod":"claude.ai"} {}`, wantErr: true},
		{name: "array status", stdout: `[{"loggedIn":true,"authMethod":"claude.ai"}]`, wantErr: true},
		{name: "invalid JSON", stdout: `private@example.com`, wantErr: true},
		{name: "oversized JSON", stdout: strings.Repeat("x", 33<<10), wantErr: true},
		{name: "native failure", stdout: `{"loggedIn":true,"authMethod":"claude.ai"}`, exit: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, err := validateClaudeAuthStatus(SetupOutput{Stdout: tc.stdout, Exit: tc.exit}, login)
			if tc.wantErr {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private@example.com")
				assert.Empty(t, facts)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantState, facts[0].State)
			for _, fact := range facts {
				assert.NotContains(t, fact.Detail, "private@example.com")
			}
			if tc.wantState != "local_claude_ai_auth" {
				assert.Equal(t, login, facts[1].Detail)
			} else {
				assert.Equal(t, "unverified", facts[1].State)
			}
		})
	}
}

func TestClaudeSetupRefusesSymlinkAndWrongRoot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, root string
		prepare    func(*testing.T, *MemFS)
	}{
		{name: "relative", root: "relative/root"},
		{name: "filesystem root", root: "/"},
		{name: "home as config", root: "/friends/friend-home"},
		{name: "working dir as config", root: "/friends/friend"},
		{name: "symlink root", root: "/friends/linked", prepare: func(_ *testing.T, m *MemFS) { m.Symlink("/other", "/friends/linked") }},
		{name: "symlink parent", root: "/friends/linked/account", prepare: func(_ *testing.T, m *MemFS) { m.Symlink("/other", "/friends/linked") }},
		{name: "file root", root: "/friends/file", prepare: func(t *testing.T, m *MemFS) {
			require.NoError(t, m.WriteFile("/friends/file", []byte("sentinel"), 0o600))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMemFS()
			require.NoError(t, m.MkdirAll("/friends", 0o700))
			require.NoError(t, m.MkdirAll("/friends/friend-home", 0o700))
			if tc.prepare != nil {
				tc.prepare(t, m)
			}
			_, err := PlanClaudeSetup(claudeSetupOptions(tc.root), m)
			require.Error(t, err)
		})
	}
}

func TestClaudeSetupKeepsFourConfigRootsSeparate(t *testing.T) {
	t.Parallel()
	m := NewMemFS()
	require.NoError(t, m.MkdirAll("/friends/friend-home", 0o700))
	for _, root := range []string{"/friends/a", "/friends/b", "/friends/c", "/friends/d"} {
		plan, err := PlanClaudeSetup(claudeSetupOptions(root), m)
		require.NoError(t, err)
		require.NotEmpty(t, plan.Steps)
		status := plan.Steps[len(plan.Steps)-1]
		require.NotNil(t, status.Command)
		assert.Contains(t, status.Command.Env, "CLAUDE_CONFIG_DIR="+root)
		assert.Contains(t, plan.Facts[3].Detail, "CLAUDE_CONFIG_DIR="+root)
		entry, err := m.Lstat(root)
		require.NoError(t, err)
		assert.Equal(t, KindMissing, entry.Kind, "planning must preserve each account root")
	}
}

func TestClaudeSetupCreatesFreshPrivateHomeBeforeNativeStatus(t *testing.T) {
	t.Parallel()
	m := NewMemFS()
	require.NoError(t, m.MkdirAll("/friends", 0o700))
	plan, err := PlanClaudeSetup(claudeSetupOptions("/friends/account"), m)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Steps)
	assert.Equal(t, "claude_private_home", plan.Steps[0].Name)
	assert.Equal(t, "claude_auth_status", plan.Steps[len(plan.Steps)-1].Name)
	entry, err := m.Lstat("/friends/friend-home")
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind)
	_, err = plan.Steps[0].Apply()
	require.NoError(t, err)
	entry, err = m.Lstat("/friends/friend-home")
	require.NoError(t, err)
	assert.Equal(t, KindDir, entry.Kind)
}

func TestClaudeSetupRefusesUnimplementedRouteInputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*SetupOptions)
	}{
		{name: "ordinary route", edit: func(o *SetupOptions) { o.ProfileOnly = false; o.Provider, o.Model = "anthropic", "anthropic/model" }},
		{name: "provider", edit: func(o *SetupOptions) { o.Provider = "anthropic" }},
		{name: "model", edit: func(o *SetupOptions) { o.Model = "anthropic/model" }},
		{name: "key name", edit: func(o *SetupOptions) { o.KeyEnv = "ANTHROPIC_API_KEY" }},
		{name: "package", edit: func(o *SetupOptions) { o.Package = "package" }},
		{name: "base URL", edit: func(o *SetupOptions) { o.BaseURL = "https://example.test" }},
		{name: "seat", edit: func(o *SetupOptions) { o.Seat = "seat" }},
		{name: "secret name", edit: func(o *SetupOptions) { o.SecretNames = []string{"NAME"} }},
		{name: "context limit", edit: func(o *SetupOptions) { o.ContextLimit = 1000 }},
		{name: "output limit", edit: func(o *SetupOptions) { o.OutputLimit = 1000 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := claudeSetupOptions("/friends/account")
			tc.edit(&opts)
			_, err := PlanClaudeSetup(opts, NewMemFS())
			require.ErrorContains(t, err, "--profile-only")
		})
	}
}

func TestClaudeDefaultSubscriptionRouteRequiresNativeClaudeAIAuth(t *testing.T) {
	t.Parallel()
	opts := claudeSetupOptions("/friends/account")
	opts.ProfileOnly = false
	opts.Provider = "claude.ai"
	opts.Model = "claude.ai/default"
	require.True(t, ClaudeAccountSetupRoute(opts))
	fs := NewMemFS()
	plan, err := PlanClaudeSetup(opts, fs)
	require.NoError(t, err)
	assert.Contains(t, plan.Facts, SetupFact{Name: "route", State: "selected_unverified", Detail: "claude.ai/default"})
	assert.Contains(t, plan.Facts, SetupFact{Name: "model_resolution", State: "not_checked", Detail: "account default resolves only on a provider turn"})
	status := plan.Steps[len(plan.Steps)-1]
	require.NotNil(t, status.Validate)
	for _, tc := range []struct {
		name, stdout, wantState string
		exit                    int
		wantError               bool
	}{
		{name: "signed out", stdout: `{"loggedIn":false,"authMethod":"none"}`, exit: 1, wantState: "requires_user_login", wantError: true},
		{name: "console", stdout: `{"loggedIn":true,"authMethod":"api_key"}`, wantState: "wrong_method", wantError: true},
		{name: "subscription", stdout: `{"loggedIn":true,"authMethod":"claude.ai"}`, wantState: "local_claude_ai_auth"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, err := status.Validate(SetupOutput{Stdout: tc.stdout, Exit: tc.exit})
			if tc.wantError {
				require.ErrorContains(t, err, "local claude.ai sign-in")
			} else {
				require.NoError(t, err)
				assert.Contains(t, facts, SetupFact{Name: "subscription", State: "unverified", Detail: "local auth status does not verify subscription entitlement"})
			}
			require.NotEmpty(t, facts)
			assert.Equal(t, tc.wantState, facts[0].State)
		})
	}
}

func TestClaudeAccountSetupRouteRefusesAPIBillingInputs(t *testing.T) {
	t.Parallel()
	base := claudeSetupOptions("/friends/account")
	base.ProfileOnly = false
	base.Provider = "claude.ai"
	base.Model = "claude.ai/default"
	for _, tc := range []struct {
		name string
		edit func(*SetupOptions)
	}{
		{name: "console provider", edit: func(o *SetupOptions) { o.Provider = "anthropic" }},
		{name: "other model", edit: func(o *SetupOptions) { o.Model = "claude.ai/sonnet" }},
		{name: "api key", edit: func(o *SetupOptions) { o.KeyEnv = "ANTHROPIC_API_KEY" }},
		{name: "seat", edit: func(o *SetupOptions) { o.Seat = "seat" }},
		{name: "secret", edit: func(o *SetupOptions) { o.SecretNames = []string{"ANTHROPIC_API_KEY"} }},
		{name: "package", edit: func(o *SetupOptions) { o.Package = "adapter" }},
		{name: "endpoint", edit: func(o *SetupOptions) { o.BaseURL = "https://example.test" }},
		{name: "limit", edit: func(o *SetupOptions) { o.OutputLimit = 10 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := base
			tc.edit(&opts)
			assert.False(t, ClaudeAccountSetupRoute(opts))
			_, err := PlanClaudeSetup(opts, NewMemFS())
			require.ErrorContains(t, err, "claude.ai/default subscription route")
		})
	}
}

func TestPlanSetupAllowsOnlyKeylessClaudeSubscriptionDefault(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	require.NoError(t, fs.MkdirAll("/friends/friend", 0o700))
	require.NoError(t, fs.MkdirAll("/coordinator", 0o700))
	opts := claudeSetupOptions("/friends/account")
	opts.ProfileOnly = false
	opts.Provider = "claude.ai"
	opts.Model = "claude.ai/default"
	opts.Profile = "isolated"
	opts.DenySelf = []string{"/coordinator"}
	plan, err := PlanSetup(opts, fs, PlanClaudeSetup)
	require.NoError(t, err)
	assert.Equal(t, "claude", plan.Harness)
	for _, fact := range plan.Facts {
		assert.NotEqual(t, "key_env", fact.Name)
		assert.NotEqual(t, "secret_names", fact.Name)
	}

	other := opts
	other.Model = "claude.ai/sonnet"
	_, err = PlanSetup(other, fs, PlanClaudeSetup)
	require.ErrorContains(t, err, "unknown provider wants explicit --key-env")

	withKey := opts
	withKey.KeyEnv = "ANTHROPIC_API_KEY"
	withKey.SecretNames = []string{"ANTHROPIC_API_KEY"}
	withKey.Seat = "seat"
	_, err = PlanSetup(withKey, fs, PlanClaudeSetup)
	require.ErrorContains(t, err, "claude.ai/default subscription route")
}

func TestClaudeDefaultRouteBindsOnlyAfterNativeSubscriptionAuth(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	require.NoError(t, fs.MkdirAll("/friends/friend", 0o700))
	require.NoError(t, fs.MkdirAll("/coordinator", 0o700))
	opts := claudeSetupOptions("/friends/account")
	opts.ProfileOnly = false
	opts.Provider = "claude.ai"
	opts.Model = "claude.ai/default"
	opts.Profile = "isolated"
	opts.DenySelf = []string{"/coordinator"}
	plan, err := PlanSetup(opts, fs, PlanClaudeSetup)
	require.NoError(t, err)
	statusOutput := `{"loggedIn":false,"loggedIn":true,"authMethod":"claude.ai"}`
	statusExit := 0
	runner := SetupRunner{
		FS: fs,
		Exec: func(ctx context.Context, _, _ string, _ []string, _ string) (string, int, error) {
			require.NoError(t, processStarted(ctx)(42))
			return statusOutput, statusExit, nil
		},
		Identity:      func(int) string { return "birth" },
		GroupRunnable: func(int) bool { return false },
		Lock:          func(string) (func() error, error) { return func() error { return nil }, nil },
		Nonce:         func() string { return "run" },
	}
	root := "/friends/account/.nova-friend-setup"
	result, err := runner.Apply(context.Background(), plan, root)
	require.ErrorContains(t, err, "incomplete JSON")
	entry, err := fs.Lstat(SetupProfilePath(opts.ConfigDir))
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind)

	statusOutput = `{"loggedIn":false,"authMethod":"none"}`
	statusExit = 1
	result, err = runner.Apply(context.Background(), plan, root)
	require.ErrorContains(t, err, "local claude.ai sign-in")
	assert.Contains(t, result.Facts, SetupFact{Name: "auth", State: "requires_user_login", Detail: "Claude subscription sign-in is interactive"})
	entry, err = fs.Lstat(SetupProfilePath(opts.ConfigDir))
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind)

	statusOutput = `{"loggedIn":true,"authMethod":"claude.ai"}`
	statusExit = 0
	result, err = runner.Apply(context.Background(), plan, root)
	require.NoError(t, err)
	assert.Contains(t, result.Facts, SetupFact{Name: "auth", State: "local_claude_ai_auth", Detail: "native status reports claude.ai"})
	entry, err = fs.Lstat(SetupProfilePath(opts.ConfigDir))
	require.NoError(t, err)
	assert.Equal(t, KindFile, entry.Kind)
}
