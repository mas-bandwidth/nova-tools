package friend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func codexSetupOptions() SetupOptions {
	return SetupOptions{Harness: "codex", Friend: "worker", Profile: "account", Dir: "/workers/work", Home: "/workers/home", ConfigDir: "/workers/config", Program: "/usr/bin/codex", ProfileOnly: true, DenySelf: []string{"/coordinator"}}
}

func TestCodexSetupPlansWithoutCredentialAccess(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	o := codexSetupOptions()
	p, err := PlanSetup(o, fs, PlanCodexSetup)
	require.NoError(t, err)
	e, err := fs.Lstat(o.ConfigDir)
	require.NoError(t, err)
	assert.Equal(t, KindMissing, e.Kind)
	step := p.Steps[len(p.Steps)-2] // final public profile metadata follows native status
	require.NotNil(t, step.Command)
	assert.Equal(t, []string{"login", "status"}, step.Command.Args)
	assert.True(t, step.Command.StatusOnStderr)
	assert.Contains(t, step.Command.Env, "CODEX_HOME="+o.ConfigDir)
	for _, item := range step.Command.Env {
		assert.NotContains(t, item, "TOKEN")
		assert.NotContains(t, item, "API_KEY")
	}
	var login string
	for _, fact := range p.Facts {
		if fact.Name == "login_command" {
			login = fact.Detail
		}
	}
	assert.Contains(t, login, "login --device-auth")
	assert.Contains(t, (SetupProfile{Selection: o}).Environment("/bin"), "CODEX_HOME="+o.ConfigDir)
}

func TestCodexSetupRefusesRoutesAndSharedRoots(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*SetupOptions)
	}{
		{"API key", func(o *SetupOptions) { o.KeyEnv = "OPENAI_API_KEY" }},
		{"model", func(o *SetupOptions) { o.Model = "openai/model" }},
		{"endpoint", func(o *SetupOptions) { o.BaseURL = "https://example.invalid" }},
		{"not profile only", func(o *SetupOptions) { o.ProfileOnly = false }},
		{"shared root", func(o *SetupOptions) { o.ConfigDir = o.Home }},
		{"relative root", func(o *SetupOptions) { o.ConfigDir = "config" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := codexSetupOptions()
			tc.edit(&o)
			_, err := PlanCodexSetup(o, NewMemFS())
			require.Error(t, err)
		})
	}
	fs := NewMemFS()
	fs.Symlink("/elsewhere", "/workers")
	_, err := PlanCodexSetup(codexSetupOptions(), fs)
	require.Error(t, err)
}

func TestCodexAuthStatusAcceptsOnlySubscriptionMethod(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text, state string
		exit              int
		fail              bool
	}{
		{"native ChatGPT", "Logged in using ChatGPT\n", "local_chatgpt_auth", 0, false},
		{"not logged in", "Not logged in\n", "requires_user_login", 1, false},
		{"inconsistent success", "Logged in using ChatGPT", "", 1, true},
		{"inconsistent missing", "Not logged in", "", 0, true},
		{"API key", "Logged in using an API key - private-secret", "", 0, true},
		{"enterprise", "Logged in using an access token - private-secret", "", 0, true},
		{"extra output", "Logged in using ChatGPT\nprivate-secret", "", 0, true},
		{"oversized", strings.Repeat("private-secret", 500), "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			facts, err := validateCodexAuthStatus(SetupOutput{Stdout: tc.text, Exit: tc.exit}, "consent-command")
			if tc.fail {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private-secret")
				assert.Empty(t, facts)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.state, facts[0].State)
		})
	}
	_, err := validateCodexAuthStatus(SetupOutput{Err: errors.New("private-secret")}, "consent-command")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private-secret")
}

func TestCodexStatusCapturesNativeStderrOnly(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), execSeparateOutputKey{}, true)
	ctx = context.WithValue(ctx, setupStderrOutputKey{}, true)
	out, exit, err := RealExec(ctx, t.TempDir(), "/bin/sh", []string{"-c", "printf 'private-secret'; printf 'Logged in using ChatGPT\\n' >&2"}, "")
	require.NoError(t, err)
	assert.Zero(t, exit)
	assert.Equal(t, "Logged in using ChatGPT\n", out)
	facts, err := validateCodexAuthStatus(SetupOutput{Stdout: out, Exit: exit}, "consent-command")
	require.NoError(t, err)
	assert.Equal(t, "local_chatgpt_auth", facts[0].State)
}
