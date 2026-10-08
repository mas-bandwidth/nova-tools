package friend

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dshHeadlessManifest = `{"private":true,"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-headless"]}}}`

// dshSettingsFS observes all mutation attempts, including no-op directory
// creation; settings never initializes a profile or serializes its config.
type dshSettingsFS struct {
	*MemFS
	writes, mkdirs int
	reads          []string
}

func (f *dshSettingsFS) WriteFile(p string, raw []byte, perm fs.FileMode) error {
	f.writes++
	return f.MemFS.WriteFile(p, raw, perm)
}

func (f *dshSettingsFS) MkdirAll(p string, perm fs.FileMode) error {
	f.mkdirs++
	return f.MemFS.MkdirAll(p, perm)
}

func (f *dshSettingsFS) ReadFile(p string) ([]byte, error) {
	f.reads = append(f.reads, p)
	return f.MemFS.ReadFile(p)
}

// SPEC-FRIEND.md, Harness settings: the initialized headless composition is
// required; desktop presence and an empty directory cannot stand in for it.
func TestDSHSettingsRequireInitializedHeadlessWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, manifest string
		profile        string
		invalid        bool
	}{
		{"missing profile", "", "", false},
		{"desktop only", dshHeadlessManifest, "desktop", false},
		{"empty headless directory", "", "headless", false},
		{"base only", `{"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-base"]}}}`, "headless", true},
		{"headless only", `{"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-headless"]}}}`, "headless", true},
		{"case alias dsh", `{"DSH":{"profile":{"bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-headless"]}}}`, "headless", true},
		{"case alias profile", `{"dsh":{"Profile":{"bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-headless"]}}}`, "headless", true},
		{"case alias bundles", `{"dsh":{"profile":{"Bundles":["@deepseek-ai/dsh-base","@deepseek-ai/dsh-headless"]}}}`, "headless", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMemFS()
			require.NoError(t, m.MkdirAll("/work/zoe", 0o700))
			require.NoError(t, m.MkdirAll("/owned/dsh/profiles", 0o700))
			if tc.profile != "" {
				p := "/owned/dsh/profiles/" + tc.profile
				require.NoError(t, m.MkdirAll(p, 0o700))
				if tc.manifest != "" {
					require.NoError(t, m.WriteFile(p+"/package.json", []byte(tc.manifest), 0o600))
				}
			}
			f := &dshSettingsFS{MemFS: m}
			h := HarnessSettings{Harness: "dsh", Dir: "/work/zoe", DSHHome: "/owned/dsh", FS: f}
			all, err := h.Check()
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, Drift(all))
			}
			_, err = h.Plan()
			require.Error(t, err)
			_, err = h.Write()
			require.Error(t, err)
			assert.Zero(t, f.writes)
			assert.Zero(t, f.mkdirs)
		})
	}
}

func TestDSHSettingsPreserveEveryConfigByte(t *testing.T) {
	t.Parallel()
	m, h := fakeHome(t)
	h.Harness = "dsh"
	require.NoError(t, m.MkdirAll("/home/zoe/.dsh/profiles/desktop", 0o700))
	files := map[string]string{
		"/home/zoe/.dsh/profiles/headless/package.json":     "\n" + dshHeadlessManifest + "\n",
		"/home/zoe/.dsh/profiles/headless/cordis.patch.yml": "# owner model stays\n- id: agent-default-model\n  config: {provider: deepseek-account, model: deepseek-v4-pro}\n",
		"/home/zoe/.dsh/cordis.patch.yml":                   "# expression is data\n- id: tools\n  config: {mode: !!js process.env.DSH_TOOLS_MODE}\n- &existing {id: unrelated, config: {enabled: true}}\n- *existing\n",
	}
	for p, text := range files {
		require.NoError(t, m.WriteFile(p, []byte(text), 0o600))
	}
	f := &dshSettingsFS{MemFS: m}
	h.FS = f
	all, err := h.Check()
	require.NoError(t, err)
	assert.Empty(t, Drift(all))
	plan, err := h.Plan()
	require.NoError(t, err)
	assert.Empty(t, plan)
	wrote, err := h.Write()
	require.NoError(t, err)
	assert.Empty(t, wrote)
	assert.Zero(t, f.writes)
	assert.Zero(t, f.mkdirs)
	for p, text := range files {
		assert.Equal(t, text, readFake(t, m, p))
	}
}

func TestDSHSettingsRefuseSymlinksBeforeReadingTheirConfig(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/home/zoe", "/home/zoe/.dsh", "/home/zoe/.dsh/profiles", "/home/zoe/.dsh/profiles/headless", "/home/zoe/.dsh/profiles/headless/package.json", "/home/zoe/.dsh/profiles/headless/cordis.patch.yml", "/home/zoe/.dsh/cordis.patch.yml"} {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			m, h := fakeHome(t)
			h.Harness = "dsh"
			m.Symlink("/private/other-owner", p)
			f := &dshSettingsFS{MemFS: m}
			h.FS = f
			_, err := h.Write()
			require.ErrorIs(t, err, ErrNotRealDir)
			assert.Contains(t, err.Error(), p)
			assert.NotContains(t, f.reads, p)
			for _, read := range f.reads {
				assert.NotContains(t, read, p+"/")
			}
			assert.Zero(t, f.writes)
			assert.Zero(t, f.mkdirs)
		})
	}
}

func TestDSHSettingsRefuseMalformedConfigWithoutQuotingIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, text string }{
		{"package.json", `{"SECRET_FIXTURE_DO_NOT_ECHO":`},
		{"package.json", `null`},
		{"package.json", ""},
		{"package.json", `[]`},
		{"package.json", `{"dsh":{"profile":{"bundles":"SECRET_FIXTURE_DO_NOT_ECHO"}}}`},
		{"cordis.patch.yml", "- SECRET_FIXTURE_DO_NOT_ECHO: [\n"},
		{"cordis.patch.yml", "SECRET_FIXTURE_DO_NOT_ECHO: value\n"},
		{"cordis.patch.yml", "- SECRET_FIXTURE_DO_NOT_ECHO\n"},
		{"cordis.patch.yml", "[]\n---\n- id: SECRET_FIXTURE_DO_NOT_ECHO\n"},
	} {
		t.Run(tc.file+tc.text, func(t *testing.T) {
			t.Parallel()
			m, h := fakeHome(t)
			h.Harness = "dsh"
			p := "/home/zoe/.dsh/profiles/headless/" + tc.file
			require.NoError(t, m.WriteFile(p, []byte(tc.text), 0o600))
			f := &dshSettingsFS{MemFS: m}
			h.FS = f
			_, err := h.Check()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "SECRET_FIXTURE_DO_NOT_ECHO")
			_, err = h.Write()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "SECRET_FIXTURE_DO_NOT_ECHO")
			assert.Equal(t, tc.text, readFake(t, m, p))
			assert.Zero(t, f.writes)
			assert.Zero(t, f.mkdirs)
		})
	}
}
