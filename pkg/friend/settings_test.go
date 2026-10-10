package friend_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/friend/friendtest"
)

// fakeHome is a friend's home in the in-memory twin: her working
// directory, and the DeepSeek Harness desktop profile the app makes.
func fakeHome(t *testing.T) (*friendtest.MemFS, friend.HarnessSettings) {
	t.Helper()
	m := friendtest.NewMemFS()
	require.NoError(t, m.MkdirAll("/home/zoe/zoe-working", 0o755))
	require.NoError(t, m.MkdirAll("/home/zoe/.dsh/profiles/desktop", 0o700))
	return m, friend.HarnessSettings{Friend: "zoe", Dir: "/home/zoe/zoe-working", Home: "/home/zoe", FS: m}
}

func readFake(t *testing.T, m *friendtest.MemFS, p string) string {
	t.Helper()
	raw, err := m.ReadFile(p)
	require.NoError(t, err)
	return string(raw)
}

// docs/SPEC-FRIEND.md, Harness settings: install writes each harness's own
// settings, a symlinked directory is refused with nothing written, and
// check names a drifted setting.
func TestInstallWritesTheHarnessSettingsAFriendNeeds(t *testing.T) {
	t.Parallel()

	t.Run("each harness's settings are written, then check finds no drift", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			harness string
			with    func(h *friend.HarnessSettings)
			file    string
			holds   []string
		}{
			{"codex", nil, "/home/zoe/.codex/config.toml", []string{"[sandbox_workspace_write]", `writable_roots = ["/home/zoe/zoe-working"]`}},
			{"dsh", nil, "/home/zoe/.dsh/profiles/desktop/cordis.patch.yml", []string{"id: agent-preset-registry", "selectedDefault: standard", "default: standard"}},
			{"grok", nil, "/home/zoe/.nova-friend/zoe/zoe.wake", nil},
			{"claude", func(h *friend.HarnessSettings) { h.ConfigDir = "/home/zoe/.claude-zoe" }, "", nil},
			{"opencode", func(h *friend.HarnessSettings) { h.Model = "deepseek/deepseek-v4" }, "/home/zoe/zoe-working/opencode.json", []string{`"model": "deepseek/deepseek-v4"`, `"/home/zoe/zoe-working/**": "allow"`}},
		} {
			t.Run(tc.harness, func(t *testing.T) {
				t.Parallel()
				m, h := fakeHome(t)
				h.Harness = tc.harness
				if tc.with != nil {
					tc.with(&h)
				}
				before, err := h.Check()
				require.NoError(t, err)
				assert.NotEmpty(t, friend.Drift(before), "a fresh home is missing the harness's settings")

				wrote, err := h.Write()
				require.NoError(t, err)
				assert.Equal(t, named(friend.Drift(before)), named(wrote), "install writes exactly what check said had drifted")
				if tc.file != "" {
					text := readFake(t, m, tc.file)
					for _, s := range tc.holds {
						assert.Contains(t, text, s)
					}
				}
				after, err := h.Check()
				require.NoError(t, err)
				assert.Empty(t, friend.Drift(after))

				again, err := h.Write()
				require.NoError(t, err)
				assert.Empty(t, again, "a second install writes nothing")
			})
		}
	})

	t.Run("the claude config directory is made, and is wanted", func(t *testing.T) {
		t.Parallel()
		m, h := fakeHome(t)
		h.Harness, h.ConfigDir = "claude", "/home/zoe/.claude-zoe"
		_, err := h.Write()
		require.NoError(t, err)
		e, err := m.Lstat("/home/zoe/.claude-zoe")
		require.NoError(t, err)
		assert.Equal(t, friend.KindDir, e.Kind)

		h.ConfigDir = ""
		_, err = h.Write()
		assert.ErrorIs(t, err, friend.ErrNoConfigDir)
	})

	t.Run("a symlinked directory is refused and nothing is written", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			harness, link string
			with          func(h *friend.HarnessSettings)
			untouched     string
		}{
			{"codex", "/home/zoe/zoe-working", nil, "/home/zoe/.codex/config.toml"},
			{"opencode", "/home/zoe/zoe-working", nil, "/home/zoe/zoe-working/opencode.json"},
			{"claude", "/home/zoe/.claude-zoe", func(h *friend.HarnessSettings) { h.ConfigDir = "/home/zoe/.claude-zoe" }, ""},
			{"grok", "/home/zoe/wakes", func(h *friend.HarnessSettings) { h.Wake = "/home/zoe/wakes/zoe.wake" }, "/home/zoe/wakes/zoe.wake"},
			{"dsh", "/home/zoe/.dsh/profiles/desktop", nil, "/home/zoe/.dsh/profiles/desktop/cordis.patch.yml"},
		} {
			t.Run(tc.harness, func(t *testing.T) {
				t.Parallel()
				m, h := fakeHome(t)
				h.Harness = tc.harness
				if tc.with != nil {
					tc.with(&h)
				}
				m.Symlink("/Volumes/nova/ai/zoe", tc.link)
				_, err := h.Write()
				require.ErrorIs(t, err, friend.ErrNotRealDir)
				assert.Contains(t, err.Error(), tc.link)
				assert.Contains(t, err.Error(), "symlink to /Volumes/nova/ai/zoe")
				if tc.untouched != "" {
					e, err := m.Lstat(tc.untouched)
					require.NoError(t, err)
					assert.Equal(t, friend.KindMissing, e.Kind, "nothing is written past a refusal")
				}
				all, err := h.Check()
				require.NoError(t, err)
				var names []string
				for _, s := range friend.Drift(all) {
					names = append(names, s.File)
				}
				assert.Contains(t, names, tc.link, "check names the symlink as drift")
			})
		}
	})

	t.Run("a dsh home with no desktop profile is refused, never invented", func(t *testing.T) {
		t.Parallel()
		m := friendtest.NewMemFS()
		require.NoError(t, m.MkdirAll("/home/zoe/zoe-working", 0o755))
		h := friend.HarnessSettings{Harness: "dsh", Friend: "zoe", Dir: "/home/zoe/zoe-working", Home: "/home/zoe", FS: m}
		_, err := h.Write()
		require.ErrorIs(t, err, friend.ErrNotRealDir)
		assert.Contains(t, err.Error(), "desktop-profile")
	})

	t.Run("check names a drifted setting", func(t *testing.T) {
		t.Parallel()
		m, h := fakeHome(t)
		h.Harness = "dsh"
		_, err := h.Write()
		require.NoError(t, err)
		file := "/home/zoe/.dsh/profiles/desktop/cordis.patch.yml"
		require.NoError(t, m.WriteFile(file, []byte(strings.Replace(readFake(t, m, file), "selectedDefault: standard", "selectedDefault: minimal", 1)), 0o600))
		all, err := h.Check()
		require.NoError(t, err)
		drift := friend.Drift(all)
		require.Len(t, drift, 1)
		assert.Equal(t, friend.Setting{Harness: "dsh", File: file, Name: "agent-preset-registry.config.selectedDefault", Want: "standard", Have: "minimal"}, drift[0])
	})

	t.Run("a config file that is a symlink is refused, never replaced", func(t *testing.T) {
		t.Parallel()
		m, h := fakeHome(t)
		h.Harness = "codex"
		m.Symlink("/home/zoe/dotfiles/codex.toml", "/home/zoe/.codex/config.toml")
		_, err := h.Write()
		require.ErrorIs(t, err, friend.ErrNotRealDir)
		e, _ := m.Lstat("/home/zoe/.codex/config.toml")
		assert.Equal(t, friend.KindSymlink, e.Kind)
	})

	t.Run("a harness with no settings checks only the friend's directory", func(t *testing.T) {
		t.Parallel()
		_, h := fakeHome(t)
		h.Harness = "gemini"
		all, err := h.Check()
		require.NoError(t, err)
		assert.Equal(t, []friend.Setting{{Harness: "gemini", File: "/home/zoe/zoe-working", Name: "dir", Want: friend.KindDir, Have: friend.KindDir}}, all)
	})

	t.Run("the real filesystem: a symlinked root is refused", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		real := filepath.Join(home, "real")
		require.NoError(t, os.Mkdir(real, 0o755))
		link := filepath.Join(home, "zoe-working")
		require.NoError(t, os.Symlink(real, link))
		h := friend.HarnessSettings{Harness: "codex", Friend: "zoe", Dir: link, Home: home}
		_, err := h.Write()
		require.True(t, errors.Is(err, friend.ErrNotRealDir), "%v", err)
		_, err = os.Lstat(filepath.Join(home, ".codex", "config.toml"))
		assert.True(t, os.IsNotExist(err))

		h.Dir = real
		_, err = h.Write()
		require.NoError(t, err)
		roots, found, err := friend.CodexRoots(string(must(os.ReadFile(filepath.Join(home, ".codex", "config.toml")))))
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, []string{real}, roots)
	})
}

// named is each setting's file and name.
func named(ss []friend.Setting) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.File+" "+s.Name)
	}
	return out
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestCodexWritableRootsAreMergedByLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{"empty", "", "[sandbox_workspace_write]\nwritable_roots = [\"/w/zoe\"]\n"},
		{"no table", "model = \"o5\"\n", "model = \"o5\"\n\n[sandbox_workspace_write]\nwritable_roots = [\"/w/zoe\"]\n"},
		{"table, no key", "# mine\n[sandbox_workspace_write]\nnetwork_access = true\n", "# mine\n[sandbox_workspace_write]\nwritable_roots = [\"/w/zoe\"]\nnetwork_access = true\n"},
		{"key with another root", "[sandbox_workspace_write]\nwritable_roots = [\"/w/a\"] # a\nnetwork_access = true\n[agents]\n", "[sandbox_workspace_write]\nwritable_roots = [\"/w/a\", \"/w/zoe\"]\nnetwork_access = true\n[agents]\n"},
		{"multi-line array", "[sandbox_workspace_write]\nwritable_roots = [\n  '/w/a',\n  \"/w/b\", # b\n]\n", "[sandbox_workspace_write]\nwritable_roots = [\"/w/a\", \"/w/b\", \"/w/zoe\"]\n"},
		{"dotted key", "sandbox_workspace_write.writable_roots = [\"/w/a\"]\n", "sandbox_workspace_write.writable_roots = [\"/w/a\", \"/w/zoe\"]\n"},
		{"already there", "[sandbox_workspace_write]\nwritable_roots = [\"/w/zoe\"]\n", "[sandbox_workspace_write]\nwritable_roots = [\"/w/zoe\"]\n"},
		{"another table's key is not it", "[profiles.x.sandbox_workspace_write]\nwritable_roots = [\"/w/x\"]\n", "[profiles.x.sandbox_workspace_write]\nwritable_roots = [\"/w/x\"]\n\n[sandbox_workspace_write]\nwritable_roots = [\"/w/zoe\"]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := friend.CodexAddRoot(tc.in, "/w/zoe")
			require.NoError(t, err)
			assert.Equal(t, tc.want, out)
			roots, found, err := friend.CodexRoots(out)
			require.NoError(t, err)
			assert.True(t, found)
			assert.Contains(t, roots, "/w/zoe")
		})
	}
	_, _, err := friend.CodexRoots("[sandbox_workspace_write]\nwritable_roots = [\n\"/w/a\",\n")
	assert.ErrorContains(t, err, "no closing ]")
}

func TestDSHPresetKeepsTheRestOfThePatchFile(t *testing.T) {
	t.Parallel()
	m, h := fakeHome(t)
	h.Harness = "dsh"
	file := "/home/zoe/.dsh/profiles/desktop/cordis.patch.yml"
	require.NoError(t, m.WriteFile(file, []byte(`# Your patch layer for this dsh profile
- id: agent-default-model
  name: "@deepseek-ai/dsh-agent-default-model"
  config:
    model: deepseek-v4-pro
- id: agent-preset-registry
  name: "@deepseek-ai/dsh-agent-preset-registry"
  config:
    default: standard
    selectedDefault: minimal
`), 0o600))
	wrote, err := h.Write()
	require.NoError(t, err)
	require.Len(t, wrote, 1)
	assert.Equal(t, "agent-preset-registry.config.selectedDefault", wrote[0].Name)
	text := readFake(t, m, file)
	assert.Contains(t, text, "# Your patch layer for this dsh profile")
	assert.Contains(t, text, `name: "@deepseek-ai/dsh-agent-default-model"`)
	assert.Contains(t, text, "model: deepseek-v4-pro")
	assert.Contains(t, text, "selectedDefault: standard")
	assert.NotContains(t, text, "minimal")
}
