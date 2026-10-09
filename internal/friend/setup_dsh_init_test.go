package friend

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dshSetupManifest = `{"dependencies":{},"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-headless"]}}}`
const dshSetupSchema = `{"type":"object","x-cordis":{"profile":"headless","complete":true,"diagnostics":[]}}`

type dshSetupSpy struct {
	*MemFS
	writes int
}

func (s *dshSetupSpy) WriteFile(p string, b []byte, mode fs.FileMode) error {
	s.writes++
	return s.MemFS.WriteFile(p, b, mode)
}
func (s *dshSetupSpy) MkdirAll(p string, mode fs.FileMode) error {
	s.writes++
	return s.MemFS.MkdirAll(p, mode)
}

func dshSetupFixture(t *testing.T) (*dshSetupSpy, SetupOptions) {
	t.Helper()
	root := t.TempDir()
	return &dshSetupSpy{MemFS: NewMemFS()}, SetupOptions{Harness: "dsh", Friend: "helper", Dir: filepath.Join(root, "work"), Home: filepath.Join(root, "home"), ConfigDir: filepath.Join(root, "profile"), Program: "/signed/runtime/dsh", Profile: "headless", ProfileOnly: true}
}

func dshSetupGenerated(t *testing.T, m *MemFS, o SetupOptions, manifest string) {
	t.Helper()
	dir := filepath.Join(o.ConfigDir, "profiles", "headless")
	require.NoError(t, m.MkdirAll(dir, 0700))
	for name, raw := range map[string]string{"package.json": manifest, "cordis.patch.yml": "# user bytes\n[]\n", "cordis.yml": "[]\n", "pnpm-workspace.yaml": "packages: []\n"} {
		require.NoError(t, m.WriteFile(filepath.Join(dir, name), []byte(raw), 0600))
	}
}

// SPEC-FRIEND harness setup: inspection is read-only; explicit apply runs only
// static native commands in the complete owned environment, never a task.
func TestDSHSetupStaticCommandsAndReadOnlyPlan(t *testing.T) {
	t.Parallel()
	m, o := dshSetupFixture(t)
	p, err := PlanDSHSetup(o, m)
	require.NoError(t, err)
	require.Len(t, p.Steps, 4)
	assert.Zero(t, m.writes)
	entry, err := m.Lstat(o.ConfigDir)
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind)
	for i, args := range [][]string{{"--version"}, {"--profile", "headless", "--dump-default-config"}, {"--profile", "headless", "--dump-config-schema"}} {
		cmd := p.Steps[i+1].Command
		require.NotNil(t, cmd)
		assert.Equal(t, o.Program, cmd.Program)
		assert.Equal(t, args, cmd.Args)
		assert.Equal(t, o.Dir, cmd.Dir)
		assert.Equal(t, 90*time.Second, cmd.Timeout)
		assert.Equal(t, []string{"HOME=" + o.Home, "DSH_HOME=" + o.ConfigDir, "TMPDIR=" + filepath.Join(o.ConfigDir, ".nova-friend-setup", "tmp"), "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}, cmd.Env)
	}
	_, err = p.Steps[0].Apply()
	require.NoError(t, err)
	_, err = p.Steps[1].Validate(SetupOutput{Stdout: "0.2.0-rc.2\n"})
	require.NoError(t, err)
	dshSetupGenerated(t, m.MemFS, o, dshSetupManifest)
	facts, err := p.Steps[2].Validate(SetupOutput{Stdout: "plugins: []\n"})
	require.NoError(t, err)
	assert.Contains(t, facts[0].Detail, "sha256=")
	assert.NotContains(t, facts[0].Detail, "plugins")
	facts, err = p.Steps[3].Validate(SetupOutput{Stdout: dshSetupSchema})
	require.NoError(t, err)
	assert.Equal(t, "schema", facts[len(facts)-1].Name)
	assert.Contains(t, p.Facts, SetupFact{Name: "provider", State: "not_checked", Detail: "static profile only; no model, credentials, task or availability proof"})
}

func TestDSHSetupRejectsAuthorityAndUnsafePaths(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*SetupOptions){
		"harness": func(o *SetupOptions) { o.Harness = "claude" }, "scope": func(o *SetupOptions) { o.ProfileOnly = false },
		"desktop": func(o *SetupOptions) { o.Profile = "desktop" }, "implicit profile": func(o *SetupOptions) { o.Profile = "" },
		"provider": func(o *SetupOptions) { o.Provider = "deepseek-account" }, "model": func(o *SetupOptions) { o.Model = "v4-pro" },
		"package": func(o *SetupOptions) { o.Package = "plugin" }, "baseURL": func(o *SetupOptions) { o.BaseURL = "https://example.invalid" },
		"key": func(o *SetupOptions) { o.KeyEnv = "DEEPSEEK_API_KEY" }, "seat": func(o *SetupOptions) { o.Seat = "seat" },
		"secrets": func(o *SetupOptions) { o.SecretNames = []string{"key"} }, "context": func(o *SetupOptions) { o.ContextLimit = 1 },
		"output": func(o *SetupOptions) { o.OutputLimit = 1 }, "relative program": func(o *SetupOptions) { o.Program = "dsh" },
		"relative work": func(o *SetupOptions) { o.Dir = "work" }, "root home": func(o *SetupOptions) { o.Home = "/" },
		"unclean config": func(o *SetupOptions) { o.ConfigDir += "/../other" }, "implicit config": func(o *SetupOptions) { o.ConfigDir = "" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m, o := dshSetupFixture(t)
			change(&o)
			_, err := PlanDSHSetup(o, m)
			require.Error(t, err)
			assert.Zero(t, m.writes)
		})
	}
	for _, where := range []string{"home", "work", "config", "parent"} {
		t.Run("symlink "+where, func(t *testing.T) {
			t.Parallel()
			m, o := dshSetupFixture(t)
			p := map[string]string{"home": o.Home, "work": o.Dir, "config": o.ConfigDir, "parent": filepath.Dir(o.ConfigDir)}[where]
			m.Symlink("/external", p)
			_, err := PlanDSHSetup(o, m)
			require.Error(t, err)
			assert.Zero(t, m.writes)
		})
	}
}

func TestDSHSetupRefusesStateAndRechecksBeforeApply(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"sessions", "storages", ".credentials.yaml", ".env", "work-env", "cordis.patch.yml", "profiles/headless/node_modules"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m, o := dshSetupFixture(t)
			p, err := PlanDSHSetup(o, m)
			require.NoError(t, err)
			file := filepath.Join(o.ConfigDir, name)
			if name == "work-env" {
				file = filepath.Join(o.Dir, ".env")
			}
			require.NoError(t, m.MemFS.MkdirAll(filepath.Dir(file), 0700))
			require.NoError(t, m.MemFS.WriteFile(file, []byte("synthetic-private-value"), 0600))
			_, err = PlanDSHSetup(o, m)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "synthetic-private-value")
			_, err = p.Steps[0].Apply()
			require.Error(t, err)
			assert.Zero(t, m.writes)
		})
	}
}

func TestDSHSetupSchemaAndNativeFailures(t *testing.T) {
	t.Parallel()
	failures := map[string]SetupOutput{
		"native nonzero": {Stdout: dshSetupSchema, Exit: 1}, "native private error": {Stdout: dshSetupSchema, Err: errors.New("synthetic-private-value")},
		"oversize": {Stdout: strings.Repeat("x", dshSetupOutputMax+1)}, "invalid": {Stdout: "{"}, "trailing": {Stdout: dshSetupSchema + " {}"},
		"array": {Stdout: "[]"}, "no metadata": {Stdout: `{}`}, "wrong profile": {Stdout: `{"x-cordis":{"profile":"desktop","complete":true,"diagnostics":[]}}`}, "incomplete": {Stdout: `{"x-cordis":{"profile":"headless","complete":false,"diagnostics":[]}}`},
		"string complete": {Stdout: `{"x-cordis":{"profile":"headless","complete":"true","diagnostics":[]}}`}, "no diagnostics": {Stdout: `{"x-cordis":{"profile":"headless","complete":true}}`},
		"null diagnostics": {Stdout: `{"x-cordis":{"profile":"headless","complete":true,"diagnostics":null}}`}, "bad diagnostics": {Stdout: `{"x-cordis":{"profile":"headless","complete":true,"diagnostics":{}}}`},
		"error diagnostic": {Stdout: `{"x-cordis":{"profile":"headless","complete":true,"diagnostics":[{"level":"error","message":"synthetic-private-value"}]}}`},
		"unknown level":    {Stdout: `{"x-cordis":{"profile":"headless","complete":true,"diagnostics":[{"level":"info","message":"x"}]}}`}, "missing message": {Stdout: `{"x-cordis":{"profile":"headless","complete":true,"diagnostics":[{"level":"warning"}]}}`}, "null message": {Stdout: `{"x-cordis":{"profile":"headless","complete":true,"diagnostics":[{"level":"warning","message":null}]}}`}, "missing level": {Stdout: `{"x-cordis":{"profile":"headless","complete":true,"diagnostics":[{}]}}`}, "duplicate nested": {Stdout: `{"x-cordis":{"profile":"headless","complete":false,"complete":true,"diagnostics":[]}}`},
		"duplicate top": {Stdout: `{"x-cordis":{"profile":"headless","complete":false},"x-cordis":{"profile":"headless","complete":true,"diagnostics":[]}}`},
		"wrong case":    {Stdout: `{"X-cordis":{"Complete":true,"diagnostics":[]}}`},
	}
	for name, out := range failures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m, o := dshSetupFixture(t)
			p, err := PlanDSHSetup(o, m)
			require.NoError(t, err)
			dshSetupGenerated(t, m.MemFS, o, dshSetupManifest)
			_, err = p.Steps[3].Validate(out)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "synthetic-private-value")
		})
	}
	t.Run("version", func(t *testing.T) {
		t.Parallel()
		m, o := dshSetupFixture(t)
		p, err := PlanDSHSetup(o, m)
		require.NoError(t, err)
		for _, out := range []SetupOutput{{Stdout: "0.2.1"}, {Stdout: "0.2.0-rc.2", Exit: 1}, {Stdout: "0.2.0-rc.2", Err: errors.New("synthetic-private-value")}} {
			_, err = p.Steps[1].Validate(out)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "synthetic-private-value")
		}
	})
	t.Run("warning is valid", func(t *testing.T) {
		t.Parallel()
		m, o := dshSetupFixture(t)
		p, err := PlanDSHSetup(o, m)
		require.NoError(t, err)
		dshSetupGenerated(t, m.MemFS, o, dshSetupManifest)
		facts, err := p.Steps[3].Validate(SetupOutput{Stdout: `{"x-cordis":{"profile":"headless","complete":true,"diagnostics":[{"level":"warning","message":"synthetic-private-value"}]}}`})
		require.NoError(t, err)
		for _, fact := range facts {
			assert.NotContains(t, fact.Detail, "synthetic-private-value")
		}
	})
}

func TestDSHSetupManifestAndGeneratedFileRefusals(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"malformed": "{", "duplicate": `{"dsh":{"profile":{"bundles":[],"bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-headless"]}}}`,
		"DSH": strings.Replace(dshSetupManifest, `"dsh"`, `"DSH"`, 1), "Profile": strings.Replace(dshSetupManifest, `"profile"`, `"Profile"`, 1), "Bundles": strings.Replace(dshSetupManifest, `"bundles"`, `"Bundles"`, 1),
		"desktop": strings.Replace(dshSetupManifest, "dsh-headless", "dsh-desktop", 1), "reordered": `{"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-headless","@deepseek-ai/dsh-base"]}}}`,
		"extra": strings.Replace(dshSetupManifest, `dsh-headless"]`, `dsh-headless","extra"]`, 1), "duplicate bundle": strings.Replace(dshSetupManifest, "dsh-headless", "dsh-base", 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m, o := dshSetupFixture(t)
			dshSetupGenerated(t, m.MemFS, o, raw)
			_, err := PlanDSHSetup(o, m)
			require.Error(t, err)
			assert.Zero(t, m.writes)
		})
	}
	for _, name := range []string{"package.json", "cordis.patch.yml", "cordis.yml", "pnpm-workspace.yaml"} {
		t.Run("symlink "+name, func(t *testing.T) {
			t.Parallel()
			m, o := dshSetupFixture(t)
			dshSetupGenerated(t, m.MemFS, o, dshSetupManifest)
			m.Symlink("/external", filepath.Join(o.ConfigDir, "profiles", "headless", name))
			_, err := PlanDSHSetup(o, m)
			require.Error(t, err)
			assert.Zero(t, m.writes)
		})
	}
	t.Run("unexpected native state", func(t *testing.T) {
		t.Parallel()
		m, o := dshSetupFixture(t)
		p, err := PlanDSHSetup(o, m)
		require.NoError(t, err)
		dshSetupGenerated(t, m.MemFS, o, dshSetupManifest)
		require.NoError(t, m.MemFS.MkdirAll(filepath.Join(o.ConfigDir, "sessions"), 0700))
		_, err = p.Steps[2].Validate(SetupOutput{Stdout: "plugins: []"})
		require.Error(t, err)
		_, err = p.Steps[3].Validate(SetupOutput{Stdout: dshSetupSchema})
		require.Error(t, err)
	})
}

// A process can fail after native initialization but before schema collection.
// Replanning never rewrites those bytes or converts presence into schema proof.
func TestDSHSetupInterruptedInitializationAndExistingAudit(t *testing.T) {
	t.Parallel()
	m, o := dshSetupFixture(t)
	p, err := PlanDSHSetup(o, m)
	require.NoError(t, err)
	dshSetupGenerated(t, m.MemFS, o, dshSetupManifest)
	_, err = p.Steps[2].Validate(SetupOutput{Stdout: "plugins: []\n"})
	require.NoError(t, err)
	_, err = p.Steps[3].Validate(SetupOutput{Stdout: `{"x-cordis":{"profile":"headless","complete":false,"diagnostics":[]}}`})
	require.Error(t, err)
	again, err := PlanDSHSetup(o, m)
	require.NoError(t, err)
	assert.Empty(t, again.Steps)
	assert.Zero(t, m.writes)
	assert.Contains(t, again.Facts, SetupFact{Name: "schema", State: "not_checked", Detail: "existing profile is preserved; no native command runs"})
	raw, err := m.ReadFile(filepath.Join(o.ConfigDir, "profiles", "headless", "cordis.patch.yml"))
	require.NoError(t, err)
	assert.Equal(t, "# user bytes\n[]\n", string(raw))
	t.Run("partial manifest only refuses retry", func(t *testing.T) {
		t.Parallel()
		m, o := dshSetupFixture(t)
		dir := filepath.Join(o.ConfigDir, "profiles", "headless")
		require.NoError(t, m.MemFS.MkdirAll(dir, 0700))
		require.NoError(t, m.MemFS.WriteFile(filepath.Join(dir, "package.json"), []byte(dshSetupManifest), 0600))
		_, err := PlanDSHSetup(o, m)
		require.Error(t, err)
		assert.Zero(t, m.writes)
		raw, err := m.ReadFile(filepath.Join(dir, "package.json"))
		require.NoError(t, err)
		assert.Equal(t, dshSetupManifest, string(raw))
	})
}

// Schema collection imports trusted plugin modules; a new static profile must
// not introduce local dependencies or user patch rows into that collection.
func TestDSHSetupRejectsNewUserLayersBeforeSchema(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{"cordis.patch.yml": "- insert: [{name: custom}]\n", "cordis.yml": "null\n", "package.json": strings.Replace(dshSetupManifest, `"dependencies":{}`, `"dependencies":{"custom":"1"}`, 1)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m, o := dshSetupFixture(t)
			p, err := PlanDSHSetup(o, m)
			require.NoError(t, err)
			dshSetupGenerated(t, m.MemFS, o, dshSetupManifest)
			require.NoError(t, m.MemFS.WriteFile(filepath.Join(o.ConfigDir, "profiles", "headless", name), []byte(raw), 0600))
			_, err = p.Steps[2].Validate(SetupOutput{Stdout: "dump"})
			require.Error(t, err)
			_, err = p.Steps[3].Validate(SetupOutput{Stdout: dshSetupSchema})
			require.Error(t, err)
		})
	}
}
