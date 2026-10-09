package friend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claudeTokenOptions() SetupOptions {
	opts := claudeSetupOptions("/friends/token-config")
	opts.ProfileOnly = false
	opts.Provider = "claude.ai"
	opts.Model = "claude.ai/token-default"
	opts.KeyEnv = "CLAUDE_CODE_OAUTH_TOKEN"
	opts.SecretNames = []string{"CLAUDE_CODE_OAUTH_TOKEN"}
	opts.Seat = "mini-seat"
	opts.Profile = "subscription-token"
	opts.DenySelf = []string{"/coordinator"}
	return opts
}

func TestClaudeTokenRouteIsExactAndNeverAnAPIKey(t *testing.T) {
	t.Parallel()
	base := claudeTokenOptions()
	require.True(t, ClaudeTokenSetupRoute(base))
	assert.False(t, ClaudeAccountSetupRoute(base))
	assert.NotContains(t, ClaudeTrim, "--bare", "Claude's subscription token is ignored in bare mode")
	for _, tc := range []struct {
		name string
		edit func(*SetupOptions)
	}{
		{"profile only", func(o *SetupOptions) { o.ProfileOnly = true }},
		{"API provider", func(o *SetupOptions) { o.Provider = "anthropic" }},
		{"API model", func(o *SetupOptions) { o.Model = "anthropic/default" }},
		{"local OAuth model", func(o *SetupOptions) { o.Model = "claude.ai/default" }},
		{"API key name", func(o *SetupOptions) { o.KeyEnv = "ANTHROPIC_API_KEY" }},
		{"bearer token name", func(o *SetupOptions) { o.KeyEnv = "ANTHROPIC_AUTH_TOKEN" }},
		{"missing seat", func(o *SetupOptions) { o.Seat = "" }},
		{"missing sealed name", func(o *SetupOptions) { o.SecretNames = nil }},
		{"extra secret", func(o *SetupOptions) { o.SecretNames = append(o.SecretNames, "ANTHROPIC_API_KEY") }},
		{"endpoint", func(o *SetupOptions) { o.BaseURL = "https://example.test" }},
		{"package", func(o *SetupOptions) { o.Package = "other" }},
		{"limit", func(o *SetupOptions) { o.OutputLimit = 100 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := base
			opts.SecretNames = slices.Clone(base.SecretNames)
			tc.edit(&opts)
			assert.False(t, ClaudeTokenSetupRoute(opts))
			_, err := PlanClaudeSetup(opts, NewMemFS())
			require.ErrorContains(t, err, "subscription route")
		})
	}
}

func TestClaudeTokenRouteKeepsProviderRefusalAndLimitClassification(t *testing.T) {
	t.Parallel()
	exit, err := refused("claude -p", `{"type":"error","error":{"type":"authentication_error","message":"invalid credential"}}`, 1, nil)
	assert.Equal(t, 1, exit)
	var denied ProviderRefused
	require.True(t, errors.As(err, &denied))
	assert.Contains(t, denied.Reason, "authentication_error")

	now := time.Date(2026, 10, 8, 20, 0, 0, 0, time.UTC)
	hit, ok := ParseLimit("claude", `{"type":"billing_error","message":"credit balance is too low"}`, now, time.Hour)
	require.True(t, ok)
	assert.Equal(t, KindCredits, hit.Kind)
	_, retry := ProviderRefusal(`{"type":"rate_limit_error","message":"slow down"}`)
	assert.False(t, retry, "a transient 429 is not an auth failure or API fallback")
}

func TestClaudeSealedSubscriptionTokenSetupHasNoNativeOrTokenRead(t *testing.T) {
	t.Parallel()
	fs := OSFS{}
	root := t.TempDir()
	opts := claudeTokenOptions()
	opts.Dir = filepath.Join(root, "friend")
	opts.ConfigDir = filepath.Join(root, "token-config")
	opts.Home = filepath.Join(root, "token-home")
	opts.DenySelf = []string{filepath.Join(root, "coordinator")}
	require.NoError(t, os.MkdirAll(opts.Dir, 0700))
	require.NoError(t, os.MkdirAll(opts.DenySelf[0], 0700))
	raw := []byte("account_id\tlabel\tsource_machine\tsource_seat\tmachine\tprofile_id\ttarget_seat\tissued_at\naccount-a\tAccount A\tstudio\tsource-a\tmini\tsubscription-token\tmini-seat\t\n")
	opts.ClaudeAccountID, opts.ClaudeMachine = "account-a", "mini"
	opts.ClaudeAccountsSource = filepath.Join(root, "reviewed.tsv")
	opts.ClaudeAccountsPath = ClaudeAccountsOwnedPath(opts.ConfigDir)
	opts.ClaudeAccountsSHA256 = secrets.ClaudeTokenFleetSHA256(raw)
	require.NoError(t, os.WriteFile(opts.ClaudeAccountsSource, raw, 0600))
	plan, err := PlanSetup(opts, fs, PlanClaudeSetup)
	require.NoError(t, err)
	assert.Contains(t, plan.Facts, SetupFact{Name: "auth", State: "sealed_token_not_checked", Detail: "only the named subscription token is selected; setup never reads its value"})
	assert.Contains(t, plan.Facts, SetupFact{Name: "token_expiry", State: "unknown", Detail: "verify rotation metadata separately; setup cannot infer the token's mint time"})
	for _, step := range plan.Steps {
		assert.Nil(t, step.Command, "the token route must not invoke local OAuth status or a provider")
	}
	entry, err := fs.Lstat(opts.ConfigDir)
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind)

	runner := SetupRunner{
		FS: fs,
		Exec: func(context.Context, string, string, []string, string) (string, int, error) {
			require.FailNow(t, "token setup cannot start a native command")
			return "", 0, nil
		},
		Identity:      func(int) string { return "birth" },
		GroupRunnable: func(int) bool { return false },
		Lock:          func(string) (func() error, error) { return func() error { return nil }, nil },
		Nonce:         func() string { return "run" },
	}
	applied, err := runner.Apply(context.Background(), plan, filepath.Join(opts.ConfigDir, ".nova-friend-setup"))
	require.NoError(t, err)
	assert.Contains(t, applied.Facts, SetupFact{Name: "setup_profile", State: "written", Detail: SetupProfilePath(opts.ConfigDir)})
	storedProfile, err := os.ReadFile(SetupProfilePath(opts.ConfigDir))
	require.NoError(t, err)
	assert.NotContains(t, string(storedProfile), opts.ClaudeAccountsSource, "PROFILE must not persist a staging source path")
	profile, err := ReadSetupProfile(fs, SetupProfilePath(opts.ConfigDir))
	require.NoError(t, err)
	assert.Empty(t, profile.Selection.ClaudeAccountsSource, "the source staging path is not persisted")
	opts.ClaudeAccountsSource = ""
	assert.Equal(t, opts, profile.Selection)
	require.NoError(t, profile.ValidateUse(opts.Friend, opts.Harness, opts.Dir, fs, PlanClaudeSetup))

	getenv := func(name string) string {
		switch name {
		case "PATH":
			return "/usr/bin:/bin"
		case "CLAUDE_CODE_OAUTH_TOKEN":
			return "fixture-token"
		case "ANTHROPIC_API_KEY":
			return "unselected-api-fixture"
		default:
			return ""
		}
	}
	bound := profile.Exec(func(ctx context.Context, _, _ string, _ []string, _ string) (string, int, error) {
		env := ctx.Value(execEnvironmentKey{}).([]string)
		assert.Contains(t, env, "CLAUDE_CODE_OAUTH_TOKEN=fixture-token")
		for _, item := range env {
			assert.NotContains(t, item, "ANTHROPIC_API_KEY")
			assert.NotContains(t, item, "ANTHROPIC_AUTH_TOKEN")
		}
		return "", 0, nil
	}, getenv)
	_, _, err = bound(context.Background(), opts.Dir, opts.Program, []string{"-p", "probe", "--model", "default"}, "")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(opts.ClaudeAccountsPath, append(raw, '\n'), 0600))
	_, err = ReadSetupProfile(fs, SetupProfilePath(opts.ConfigDir))
	require.ErrorContains(t, err, "pinned gate digest")
	_, exit, err := bound(context.Background(), opts.Dir, opts.Program, []string{"-p", "probe"}, "")
	assert.Equal(t, 125, exit)
	require.ErrorContains(t, err, "pinned gate digest")
}

func TestClaudeTokenManifestRefusesWrongAccountMachineProfileOrSeat(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	raw := []byte("account_id\tlabel\tsource_machine\tsource_seat\tmachine\tprofile_id\ttarget_seat\tissued_at\naccount-a\tAccount A\tstudio\tsource-a\tmini\tsubscription-token\tmini-seat\t\n")
	source := filepath.Join(root, "reviewed.tsv")
	require.NoError(t, os.WriteFile(source, raw, 0600))
	for _, tc := range []struct {
		name string
		edit func(*SetupOptions)
	}{
		{"account", func(o *SetupOptions) { o.ClaudeAccountID = "account-b" }},
		{"machine", func(o *SetupOptions) { o.ClaudeMachine = "other-mini" }},
		{"profile", func(o *SetupOptions) { o.Profile = "other-profile" }},
		{"seat", func(o *SetupOptions) { o.Seat = "other-seat" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := NewMemFS()
			o := claudeTokenOptions()
			o.ClaudeAccountID, o.ClaudeMachine = "account-a", "mini"
			o.ClaudeAccountsSource = source
			o.ClaudeAccountsPath = ClaudeAccountsOwnedPath(o.ConfigDir)
			o.ClaudeAccountsSHA256 = secrets.ClaudeTokenFleetSHA256(raw)
			tc.edit(&o)
			_, err := PlanSetup(o, fs, PlanClaudeSetup)
			require.ErrorContains(t, err, "no exact account, machine, profile and seat binding")
			entry, err := fs.Lstat(SetupProfilePath(o.ConfigDir))
			require.NoError(t, err)
			assert.Equal(t, KindMissing, entry.Kind)
		})
	}
}

func TestClaudeTokenManifestChangeBetweenPlanAndApplyCannotPublishProfile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	raw := []byte("account_id\tlabel\tsource_machine\tsource_seat\tmachine\tprofile_id\ttarget_seat\tissued_at\naccount-a\tAccount A\tstudio\tsource-a\tmini\tsubscription-token\tmini-seat\t\n")
	source := filepath.Join(root, "reviewed.tsv")
	require.NoError(t, os.WriteFile(source, raw, 0600))
	fs := NewMemFS()
	o := claudeTokenOptions()
	o.ClaudeAccountID, o.ClaudeMachine = "account-a", "mini"
	o.ClaudeAccountsSource = source
	o.ClaudeAccountsPath = ClaudeAccountsOwnedPath(o.ConfigDir)
	o.ClaudeAccountsSHA256 = secrets.ClaudeTokenFleetSHA256(raw)
	plan, err := PlanSetup(o, fs, PlanClaudeSetup)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(source, append(raw, '\n'), 0600))
	runner := SetupRunner{
		FS: fs,
		Exec: func(context.Context, string, string, []string, string) (string, int, error) {
			require.FailNow(t, "token setup cannot launch native")
			return "", 0, nil
		},
		Identity: func(int) string { return "birth" }, GroupRunnable: func(int) bool { return false },
		Lock: func(string) (func() error, error) { return func() error { return nil }, nil }, Nonce: func() string { return "run" },
	}
	_, err = runner.Apply(context.Background(), plan, filepath.Join(o.ConfigDir, ".nova-friend-setup"))
	require.ErrorContains(t, err, "pinned gate digest")
	entry, err := fs.Lstat(SetupProfilePath(o.ConfigDir))
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind)
}

func TestClaudeSealedTokenRouteRefusesPriorLocalOAuthReceipt(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	opts := claudeTokenOptions()
	require.NoError(t, fs.MkdirAll("/friends/token-config/.nova-friend-setup", 0700))
	require.NoError(t, fs.WriteFile("/friends/token-config/.nova-friend-setup/RUN", []byte("local OAuth receipt"), 0600))
	_, err := PlanClaudeSetup(opts, fs)
	require.ErrorContains(t, err, "fresh config root")
}
