package friend

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupRefusesProfileRoutePrivacyAndSharedHomeAmbiguity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*testing.T, *SetupOptions, *MemFS)
	}{
		{"known missing key", func(_ *testing.T, o *SetupOptions, _ *MemFS) {
			o.ProfileOnly = false
			o.Provider = "inception"
			o.Model = "inception/chosen"
		}},
		{"known wrong key", func(_ *testing.T, o *SetupOptions, _ *MemFS) {
			o.ProfileOnly = false
			o.Provider = "inception"
			o.Model = "inception/chosen"
			o.KeyEnv = "OTHER_KEY"
			o.SecretNames = []string{"OTHER_KEY"}
			o.Seat = "seat"
		}},
		{"unknown missing name", func(_ *testing.T, o *SetupOptions, _ *MemFS) {
			o.ProfileOnly = false
			o.Provider = "custom"
			o.Model = "custom/chosen"
		}},
		{"profile package", func(_ *testing.T, o *SetupOptions, _ *MemFS) { o.Package = "ignored" }},
		{"profile URL", func(_ *testing.T, o *SetupOptions, _ *MemFS) { o.BaseURL = "https://example.test" }},
		{"profile seat", func(_ *testing.T, o *SetupOptions, _ *MemFS) { o.Seat = "seat" }},
		{"profile secrets", func(_ *testing.T, o *SetupOptions, _ *MemFS) { o.SecretNames = []string{"KEY"} }},
		{"profile limits", func(_ *testing.T, o *SetupOptions, _ *MemFS) { o.ContextLimit = 1 }},
		{"public root", func(t *testing.T, o *SetupOptions, f *MemFS) { require.NoError(t, f.MkdirAll(o.ConfigDir, 0755)) }},
		{"unowned config", func(t *testing.T, o *SetupOptions, f *MemFS) {
			require.NoError(t, f.MkdirAll(o.ConfigDir, 0700))
			require.NoError(t, f.WriteFile(filepath.Join(o.ConfigDir, "account"), []byte("unread credential fixture"), 0600))
		}},
		{"unowned home", func(t *testing.T, o *SetupOptions, f *MemFS) {
			require.NoError(t, f.MkdirAll(o.Home, 0700))
			require.NoError(t, f.WriteFile(filepath.Join(o.Home, "account"), []byte("unread credential fixture"), 0600))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewMemFS()
			o := SetupOptions{Harness: "test", Friend: "worker", Dir: "/work", Home: "/private", ConfigDir: "/config", Profile: "isolated", Program: "/native", ProfileOnly: true, DenySelf: []string{"/coordinator"}}
			tc.edit(t, &o, f)
			calls := 0
			_, err := PlanSetup(o, f, func(SetupOptions, SettingsFS) (SetupPlan, error) { calls++; return SetupPlan{}, nil })
			require.Error(t, err)
			assert.Zero(t, calls)
			assert.NotContains(t, err.Error(), "unread credential fixture")
		})
	}
}

type setupFailFS struct {
	*MemFS
	failPath string
}

func (s setupFailFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	if path == s.failPath {
		return errors.New("fixture atomic write refused")
	}
	return s.MemFS.WriteFile(path, data, perm)
}

func TestSetupBindingAndPrivateModeAreRecheckedBeforeApplyAndActivation(t *testing.T) {
	t.Parallel()
	for _, condition := range []string{"mode_changed", "owner_changed", "publish_failed"} {
		t.Run(condition, func(t *testing.T) {
			t.Parallel()
			memory := NewMemFS()
			opts := SetupOptions{Harness: "test", Friend: "worker", Dir: "/work", Home: "/private", ConfigDir: "/config", Profile: "isolated", Program: "/native", ProfileOnly: true, DenySelf: []string{"/coordinator"}}
			var filesystem SettingsFS = memory
			if condition == "publish_failed" {
				filesystem = setupFailFS{MemFS: memory, failPath: SetupProfilePath(opts.ConfigDir)}
			}
			plan, err := PlanSetup(opts, filesystem, func(SetupOptions, SettingsFS) (SetupPlan, error) { return SetupPlan{Harness: "test"}, nil })
			require.NoError(t, err)
			if condition == "mode_changed" {
				require.NoError(t, memory.MkdirAll(opts.Home, 0755))
			}
			if condition == "owner_changed" {
				require.NoError(t, memory.MkdirAll(filepath.Join(opts.ConfigDir, ".nova-friend-setup"), 0700))
				require.NoError(t, memory.WriteFile(filepath.Join(opts.ConfigDir, ".nova-friend-setup/OWNER"), []byte(`{"Friend":"other"}`), 0600))
			}
			r := SetupRunner{FS: filesystem, Exec: func(context.Context, string, string, []string, string) (string, int, error) {
				require.FailNow(t, "no native step exists")
				return "", 0, nil
			}, Identity: func(int) string { return "birth" }, GroupRunnable: func(int) bool { return false }, Lock: func(string) (func() error, error) { return func() error { return nil }, nil }, Nonce: func() string { return "run" }}
			_, err = r.Apply(context.Background(), plan, filepath.Join(opts.ConfigDir, ".nova-friend-setup"))
			require.Error(t, err)
			_, err = ReadSetupProfile(filesystem, SetupProfilePath(opts.ConfigDir))
			require.Error(t, err)
		})
	}
}

func TestSetupPublishesOnlyAfterValidationAndBindsProfileAndNativeHome(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	opts := SetupOptions{Harness: "test", Friend: "worker", Dir: "/work", Home: "/private", ConfigDir: "/config", Profile: "isolated", Program: "/native", ProfileOnly: true, DenySelf: []string{"/coordinator"}}
	planner := func(SetupOptions, SettingsFS) (SetupPlan, error) { return SetupPlan{Harness: "test"}, nil }
	plan, err := PlanSetup(opts, fs, planner)
	require.NoError(t, err)
	locked := []string{}
	r := SetupRunner{FS: fs, Exec: func(context.Context, string, string, []string, string) (string, int, error) {
		require.FailNow(t, "no native step exists")
		return "", 0, nil
	}, Identity: func(int) string { return "birth" }, GroupRunnable: func(int) bool { return false }, Nonce: func() string { return "run" }, Lock: func(path string) (func() error, error) {
		locked = append(locked, path)
		return func() error { return nil }, nil
	}}
	_, err = r.Apply(context.Background(), plan, filepath.Join(opts.ConfigDir, ".nova-friend-setup"))
	require.NoError(t, err)
	assert.Equal(t, []string{"/config/.nova-friend-setup/LOCK", "/private/.nova-friend-setup/LOCK"}, locked)
	profile, err := ReadSetupProfile(fs, SetupProfilePath(opts.ConfigDir))
	require.NoError(t, err)
	assert.Equal(t, opts, profile.Selection)
	assert.ErrorContains(t, profile.ValidateUse(opts.Friend, opts.Harness, opts.Dir, fs, planner), "no selected model route")
	changed := opts
	changed.Profile = "other"
	_, err = PlanSetup(changed, fs, planner)
	assert.ErrorContains(t, err, "another selection")
	changed = opts
	changed.ConfigDir = "/another"
	_, err = PlanSetup(changed, fs, planner)
	assert.ErrorContains(t, err, "another config root")
	public, err := fs.ReadFile(SetupProfilePath(opts.ConfigDir))
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(public)), "credential value")
	// A new native attempt cannot reuse the old completion certificate, even
	// after its leader exits but final profile publication has not succeeded.
	receipt, err := json.Marshal(setupReceipt{RunID: "retry", Command: "hash", PID: 42, Identity: "birth", Ended: true})
	require.NoError(t, err)
	require.NoError(t, fs.WriteFile(filepath.Join(opts.ConfigDir, ".nova-friend-setup/RUN"), receipt, 0600))
	_, err = ReadSetupProfile(fs, SetupProfilePath(opts.ConfigDir))
	assert.ErrorContains(t, err, "latest native attempt")
}

func TestSetupProfileNativeEnvironmentContainsOnlySelectedReferences(t *testing.T) {
	t.Parallel()
	profile := SetupProfile{Version: 1, Selection: SetupOptions{Harness: "opencode", Friend: "worker", Dir: "/work", Home: "/private", ConfigDir: "/config", Program: "/native/opencode", SecretNames: []string{"API_KEY"}}}
	var program string
	var environment []string
	exec := profile.Exec(func(ctx context.Context, _, name string, _ []string, _ string) (string, int, error) {
		program = name
		environment = ctx.Value(execEnvironmentKey{}).([]string)
		return "", 0, nil
	}, func(name string) string {
		if name == "API_KEY" {
			return "synthetic-only"
		}
		if name == "PATH" {
			return "/native:/usr/bin:/bin"
		}
		return "ambient-not-selected"
	})
	_, _, err := exec(context.Background(), "/work", "opencode", nil, "")
	require.NoError(t, err)
	assert.Equal(t, "/native/opencode", program)
	assert.Contains(t, environment, "HOME=/private")
	assert.Contains(t, environment, "OPENCODE_CONFIG_DIR=/config")
	assert.Contains(t, environment, "OPENCODE_CONFIG=/work/opencode.json")
	assert.Contains(t, environment, "API_KEY=synthetic-only")
	assert.NotContains(t, strings.Join(environment, "\n"), "ambient-not-selected")
}
