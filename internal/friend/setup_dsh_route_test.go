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

type dshAccountFS struct {
	*MemFS
	credentialReads int
}

func (f *dshAccountFS) ReadFile(path string) ([]byte, error) {
	if filepath.Base(path) == ".credentials.yaml" {
		f.credentialReads++
		return nil, errors.New("private account contents must not be read")
	}
	return f.MemFS.ReadFile(path)
}

type dshAccountRig struct {
	t       *testing.T
	fs      *dshAccountFS
	opts    SetupOptions
	calls   [][]string
	schemas int
	output  SetupOutput
}

func newDSHAccountRig(t *testing.T) *dshAccountRig {
	t.Helper()
	_, o := dshSetupFixture(t)
	o.ProfileOnly, o.Provider, o.Model = false, "deepseek-account", "deepseek-account/deepseek-v4-pro"
	o.DenySelf = []string{filepath.Join(t.TempDir(), "coordinator")}
	return &dshAccountRig{t: t, fs: &dshAccountFS{MemFS: NewMemFS()}, opts: o, output: SetupOutput{Stdout: dshSetupSchema}}
}

func (r *dshAccountRig) apply() (SetupPlan, error) {
	r.t.Helper()
	plan, err := PlanSetup(r.opts, r.fs, PlanDSHSetup)
	if err != nil {
		return plan, err
	}
	count := 0
	runner := SetupRunner{FS: r.fs, Identity: func(int) string { return "birth" }, GroupRunnable: func(int) bool { return false }, Lock: func(string) (func() error, error) { return func() error { return nil }, nil }, Nonce: func() string { count++; return strings.Repeat("a", count) }}
	runner.Exec = func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		r.calls = append(r.calls, append([]string(nil), args...))
		assert.Equal(r.t, r.opts.Program, name)
		assert.Equal(r.t, r.opts.Dir, dir)
		assert.Empty(r.t, stdin)
		assert.Equal(r.t, []string{"HOME=" + r.opts.Home, "DSH_HOME=" + r.opts.ConfigDir, "TMPDIR=" + filepath.Join(r.opts.ConfigDir, ".nova-friend-setup/tmp"), "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, ctx.Value(execEnvironmentKey{}))
		if err := processStarted(ctx)(42); err != nil {
			return "", 1, err
		}
		switch strings.Join(args, " ") {
		case "--version":
			return "0.2.0-rc.2", 0, nil
		case "--profile headless --dump-default-config":
			dshSetupGenerated(r.t, r.fs.MemFS, r.opts, dshSetupManifest)
			return "plugins: []", 0, nil
		case "--profile headless --dump-config-schema":
			r.schemas++
			if selected, err := dshAccountRoute(r.fs, r.opts, false); err == nil && selected {
				return r.output.Stdout, r.output.Exit, r.output.Err
			}
			return dshSetupSchema, 0, nil
		default:
			require.FailNow(r.t, "unsupported native call could boot a provider", strings.Join(args, " "))
			return "", 1, nil
		}
	}
	return runner.Apply(context.Background(), plan, filepath.Join(r.opts.ConfigDir, ".nova-friend-setup"))
}

// DSHAccountRoute: explicit selection never calls a provider, reads account
// contents or substitutes the official API-key route; drift refuses activation.
func TestDSHAccountRouteApplyAndFreshActivationAudit(t *testing.T) {
	t.Parallel()
	r := newDSHAccountRig(t)
	plan, err := PlanSetup(r.opts, r.fs, PlanDSHSetup)
	require.NoError(t, err)
	entry, err := r.fs.Lstat(r.opts.ConfigDir)
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind, "planning is read-only")
	assert.Contains(t, plan.Facts, SetupFact{Name: "account-auth", State: "unknown", Detail: "no supported native CLI account-status projection; existing account sign-in through the native UI may be required"})
	out, err := r.apply()
	require.NoError(t, err)
	assert.Contains(t, out.Facts, SetupFact{Name: "model-route", State: "selected", Detail: r.opts.Model})
	assert.Equal(t, 2, r.schemas, "static and explicit route schemas are independent")
	profile, err := ReadSetupProfile(r.fs, SetupProfilePath(r.opts.ConfigDir))
	require.NoError(t, err)
	require.NoError(t, profile.ValidateUse(r.opts.Friend, "dsh", r.opts.Dir, r.fs, PlanDSHSetup))
	certified, err := PlanDSHSetup(r.opts, r.fs)
	require.NoError(t, err)
	for _, step := range certified.Steps {
		assert.Nil(t, step.Command, "certified matching route does not spawn another native command")
		assert.Nil(t, step.Apply, "certified matching route preserves existing bytes")
	}
	patch := filepath.Join(r.opts.ConfigDir, "profiles/headless/cordis.patch.yml")
	data, err := r.fs.ReadFile(patch)
	require.NoError(t, err)
	assert.Contains(t, string(data), "provider: deepseek-account")
	assert.Contains(t, string(data), "model: deepseek-v4-pro")
	mode, err := r.fs.Lstat(patch)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0600), mode.Mode)
	// Native sign-in may persist a grant later; route audit neither reads it nor
	// declares it valid, even when malformed synthetic contents exist.
	require.NoError(t, r.fs.MemFS.WriteFile(filepath.Join(r.opts.ConfigDir, ".credentials.yaml"), []byte("private synthetic contents"), 0600))
	require.NoError(t, profile.ValidateUse(r.opts.Friend, "dsh", r.opts.Dir, r.fs, PlanDSHSetup))
	assert.Zero(t, r.fs.credentialReads)
	require.NoError(t, r.fs.WriteFile(patch, []byte("- id: agent-default-model\n  config: {provider: deepseek-official, model: deepseek-flash}\n"), 0600))
	err = profile.ValidateUse(r.opts.Friend, "dsh", r.opts.Dir, r.fs, PlanDSHSetup)
	require.ErrorContains(t, err, "conflicts")
	assert.NotContains(t, err.Error(), "private synthetic contents")
}

func TestDSHAccountRouteRefusesImplicitAuthorityAndCompetingLayers(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(*SetupOptions){
		"provider":        func(o *SetupOptions) { o.Provider = "deepseek-official" },
		"implicit model":  func(o *SetupOptions) { o.Model = "" },
		"different model": func(o *SetupOptions) { o.Model = "deepseek-account/deepseek-flash" },
		"short model":     func(o *SetupOptions) { o.Model = "deepseek-v4-pro" },
		"profile":         func(o *SetupOptions) { o.Profile = "web" },
		"api key": func(o *SetupOptions) {
			o.KeyEnv, o.Seat, o.SecretNames = "DEEPSEEK_API_KEY", "seat", []string{"DEEPSEEK_API_KEY"}
		},
		"endpoint": func(o *SetupOptions) { o.BaseURL = "https://example.invalid" },
		"package":  func(o *SetupOptions) { o.Package = "custom" },
		"limits":   func(o *SetupOptions) { o.ContextLimit = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newDSHAccountRig(t)
			edit(&r.opts)
			_, err := PlanSetup(r.opts, r.fs, PlanDSHSetup)
			require.Error(t, err)
			assert.Empty(t, r.calls)
		})
	}
	for name, raw := range map[string]string{
		"different selection": "- id: agent-default-model\n  config: {provider: other, model: other}\n",
		"duplicate key":       "- id: agent-default-model\n  config: {provider: deepseek-account, provider: other, model: deepseek-v4-pro}\n",
		"expression":          "- id: agent-default-model\n  config: {provider: !!js process.env.ROUTE, model: deepseek-v4-pro}\n",
		"extra row":           "- id: other\n  config: {provider: deepseek-account, model: deepseek-v4-pro}\n",
		"extra field":         "- id: agent-default-model\n  disabled: true\n  config: {provider: deepseek-account, model: deepseek-v4-pro}\n",
		"multi document":      "[]\n---\n[]\n",
		"alias":               "- &route {id: agent-default-model, config: {provider: deepseek-account, model: deepseek-v4-pro}}\n- *route\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newDSHAccountRig(t)
			dshSetupGenerated(t, r.fs.MemFS, r.opts, dshSetupManifest)
			patch := filepath.Join(r.opts.ConfigDir, "profiles/headless/cordis.patch.yml")
			require.NoError(t, r.fs.WriteFile(patch, []byte(raw), 0600))
			_, err := PlanDSHSetup(r.opts, r.fs)
			require.Error(t, err)
			got, err := r.fs.ReadFile(patch)
			require.NoError(t, err)
			assert.Equal(t, raw, string(got), "conflicting public config remains untouched")
		})
	}
}

func TestDSHAccountRouteSchemaFailureCannotPublishOrReuseStaticEpoch(t *testing.T) {
	t.Parallel()
	for name, output := range map[string]SetupOutput{
		"incomplete":      {Stdout: strings.Replace(dshSetupSchema, "true", "false", 1)},
		"nonzero":         {Stdout: dshSetupSchema, Exit: 1},
		"private failure": {Err: errors.New("private synthetic error")},
		"trailing":        {Stdout: dshSetupSchema + " {}"},
		"diagnostic":      {Stdout: strings.Replace(dshSetupSchema, "[]", `[{"level":"error","message":"private synthetic error"}]`, 1)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newDSHAccountRig(t)
			r.output = output
			_, err := r.apply()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private synthetic error")
			_, err = ReadSetupProfile(r.fs, SetupProfilePath(r.opts.ConfigDir))
			require.Error(t, err, "schema failure cannot publish route PROFILE")
			plan, err := PlanDSHSetup(r.opts, r.fs)
			require.NoError(t, err)
			assert.Contains(t, dshAccountSteps(plan), "account-route-schema", "matching patch with failed/no route PROFILE must revalidate")
		})
	}
	// A static PROFILE with the same owner/root and an ended native receipt is
	// still a different selection epoch, so exact patch bytes alone are insufficient.
	r := newDSHAccountRig(t)
	_, err := r.apply()
	require.NoError(t, err)
	path := SetupProfilePath(r.opts.ConfigDir)
	profile, err := ReadSetupProfile(r.fs, path)
	require.NoError(t, err)
	profile.Selection.ProfileOnly, profile.Selection.Provider, profile.Selection.Model = true, "", ""
	raw, err := json.Marshal(profile)
	require.NoError(t, err)
	require.NoError(t, r.fs.WriteFile(path, raw, 0600))
	plan, err := PlanDSHSetup(r.opts, r.fs)
	require.NoError(t, err)
	assert.Contains(t, dshAccountSteps(plan), "account-route-schema")
}

func dshAccountSteps(plan SetupPlan) []string {
	var names []string
	for _, step := range plan.Steps {
		names = append(names, step.Name)
	}
	return names
}

func TestDSHAccountRouteRefusesApplyTimeDriftAndUnsafeState(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*testing.T, *dshAccountRig){
		"home override": func(t *testing.T, r *dshAccountRig) {
			require.NoError(t, r.fs.WriteFile(filepath.Join(r.opts.ConfigDir, "cordis.patch.yml"), []byte("- id: agent-default-model\n  config: {provider: deepseek-official, model: deepseek-flash}\n"), 0600))
		},
		"profile override": func(t *testing.T, r *dshAccountRig) {
			require.NoError(t, r.fs.WriteFile(filepath.Join(r.opts.ConfigDir, "profiles/headless/cordis.yml"), []byte("- id: other\n"), 0600))
		},
		"environment": func(t *testing.T, r *dshAccountRig) {
			require.NoError(t, r.fs.WriteFile(filepath.Join(r.opts.Dir, ".env"), []byte("private synthetic contents"), 0600))
		},
		"credential alias": func(_ *testing.T, r *dshAccountRig) {
			r.fs.Symlink("/another-account", filepath.Join(r.opts.ConfigDir, ".credentials.yaml"))
		},
		"patch alias": func(_ *testing.T, r *dshAccountRig) {
			r.fs.Symlink("/another-patch", filepath.Join(r.opts.ConfigDir, "profiles/headless/cordis.patch.yml"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newDSHAccountRig(t)
			_, err := r.apply()
			require.NoError(t, err)
			plan, err := PlanDSHSetup(r.opts, r.fs)
			require.NoError(t, err)
			change(t, r)
			for _, step := range plan.Steps {
				if step.Validate != nil {
					_, err = step.Validate(SetupOutput{})
					require.Error(t, err, "previously certified plan must re-audit before publication")
				}
			}
			_, err = PlanDSHSetup(r.opts, r.fs)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private synthetic contents")
			assert.Zero(t, r.fs.credentialReads)
		})
	}
}
