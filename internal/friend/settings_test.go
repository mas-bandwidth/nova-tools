package friend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHome is a friend's home in the in-memory twin: her working
// directory, and an already initialized DeepSeek Harness headless profile.
func fakeHome(t *testing.T) (*MemFS, HarnessSettings) {
	t.Helper()
	m := NewMemFS()
	require.NoError(t, m.MkdirAll("/home/zoe/zoe-working", 0o755))
	require.NoError(t, m.MkdirAll("/home/zoe/.dsh/profiles/headless", 0o700))
	require.NoError(t, m.WriteFile("/home/zoe/.dsh/profiles/headless/package.json", []byte(dshHeadlessManifest), 0o600))
	return m, HarnessSettings{Friend: "zoe", Dir: "/home/zoe/zoe-working", Home: "/home/zoe", FS: m}
}

func readFake(t *testing.T, m *MemFS, p string) string {
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
			with    func(h *HarnessSettings)
			file    string
			holds   []string
		}{
			{"codex", nil, "/home/zoe/.codex/config.toml", []string{"[sandbox_workspace_write]", `writable_roots = ["/home/zoe/zoe-working"]`}},
			{"grok", nil, "/home/zoe/.nova-friend/zoe/zoe.wake", nil},
			{"claude", func(h *HarnessSettings) { h.ConfigDir = "/home/zoe/.claude-zoe" }, "", nil},
			{"opencode", func(h *HarnessSettings) { h.Model = "deepseek/deepseek-v4" }, "/home/zoe/zoe-working/opencode.json", []string{`"model": "deepseek/deepseek-v4"`, `"/home/zoe/zoe-working/**": "allow"`}},
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
				assert.NotEmpty(t, Drift(before), "a fresh home is missing the harness's settings")

				wrote, err := h.Write()
				require.NoError(t, err)
				assert.Equal(t, named(Drift(before)), named(wrote), "install writes exactly what check said had drifted")
				if tc.file != "" {
					text := readFake(t, m, tc.file)
					for _, s := range tc.holds {
						assert.Contains(t, text, s)
					}
				}
				after, err := h.Check()
				require.NoError(t, err)
				assert.Empty(t, Drift(after))

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
		assert.Equal(t, KindDir, e.Kind)

		h.ConfigDir = ""
		_, err = h.Write()
		assert.ErrorIs(t, err, ErrNoConfigDir)
	})

	t.Run("a symlinked directory is refused and nothing is written", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			harness, link string
			with          func(h *HarnessSettings)
			untouched     string
		}{
			{"codex", "/home/zoe/zoe-working", nil, "/home/zoe/.codex/config.toml"},
			{"opencode", "/home/zoe/zoe-working", nil, "/home/zoe/zoe-working/opencode.json"},
			{"claude", "/home/zoe/.claude-zoe", func(h *HarnessSettings) { h.ConfigDir = "/home/zoe/.claude-zoe" }, ""},
			{"grok", "/home/zoe/wakes", func(h *HarnessSettings) { h.Wake = "/home/zoe/wakes/zoe.wake" }, "/home/zoe/wakes/zoe.wake"},
			{"dsh", "/home/zoe/.dsh/profiles/headless", nil, "/home/zoe/.dsh/profiles/headless/untouched.yml"},
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
				require.ErrorIs(t, err, ErrNotRealDir)
				assert.Contains(t, err.Error(), tc.link)
				assert.Contains(t, err.Error(), "symlink to /Volumes/nova/ai/zoe")
				if tc.untouched != "" {
					e, err := m.Lstat(tc.untouched)
					require.NoError(t, err)
					assert.Equal(t, KindMissing, e.Kind, "nothing is written past a refusal")
				}
				all, err := h.Check()
				require.NoError(t, err)
				var names []string
				for _, s := range Drift(all) {
					names = append(names, s.File)
				}
				assert.Contains(t, names, tc.link, "check names the symlink as drift")
			})
		}
	})

	t.Run("a dsh home with no headless profile is refused, never invented", func(t *testing.T) {
		t.Parallel()
		m := NewMemFS()
		require.NoError(t, m.MkdirAll("/home/zoe/zoe-working", 0o755))
		h := HarnessSettings{Harness: "dsh", Friend: "zoe", Dir: "/home/zoe/zoe-working", Home: "/home/zoe", FS: m}
		_, err := h.Write()
		require.ErrorIs(t, err, ErrNotRealDir)
		assert.Contains(t, err.Error(), "dsh-home")
	})

	t.Run("check refuses a dsh composition missing headless", func(t *testing.T) {
		t.Parallel()
		m, h := fakeHome(t)
		h.Harness = "dsh"
		file := "/home/zoe/.dsh/profiles/headless/package.json"
		require.NoError(t, m.WriteFile(file, []byte(`{"dsh":{"profile":{"bundles":["@deepseek-ai/dsh-base"]}}}`), 0o600))
		_, err := h.Check()
		require.ErrorContains(t, err, "wants base and headless bundles in an initialized headless profile")
	})

	t.Run("a config file that is a symlink is refused, never replaced", func(t *testing.T) {
		t.Parallel()
		m, h := fakeHome(t)
		h.Harness = "codex"
		m.Symlink("/home/zoe/dotfiles/codex.toml", "/home/zoe/.codex/config.toml")
		_, err := h.Write()
		require.ErrorIs(t, err, ErrNotRealDir)
		e, _ := m.Lstat("/home/zoe/.codex/config.toml")
		assert.Equal(t, KindSymlink, e.Kind)
	})

	t.Run("a harness with no settings checks only the friend's directory", func(t *testing.T) {
		t.Parallel()
		_, h := fakeHome(t)
		h.Harness = "gemini"
		all, err := h.Check()
		require.NoError(t, err)
		assert.Equal(t, []Setting{{Harness: "gemini", File: "/home/zoe/zoe-working", Name: "dir", Want: KindDir, Have: KindDir}}, all)
	})

	t.Run("the real filesystem: a symlinked root is refused", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		real := filepath.Join(home, "real")
		require.NoError(t, os.Mkdir(real, 0o755))
		link := filepath.Join(home, "zoe-working")
		require.NoError(t, os.Symlink(real, link))
		h := HarnessSettings{Harness: "codex", Friend: "zoe", Dir: link, Home: home}
		_, err := h.Write()
		require.True(t, errors.Is(err, ErrNotRealDir), "%v", err)
		_, err = os.Lstat(filepath.Join(home, ".codex", "config.toml"))
		assert.True(t, os.IsNotExist(err))

		h.Dir = real
		_, err = h.Write()
		require.NoError(t, err)
		roots, found, err := CodexRoots(string(must(os.ReadFile(filepath.Join(home, ".codex", "config.toml")))))
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, []string{real}, roots)
	})
}

// named is each setting's file and name.
func named(ss []Setting) []string {
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
			out, err := CodexAddRoot(tc.in, "/w/zoe")
			require.NoError(t, err)
			assert.Equal(t, tc.want, out)
			roots, found, err := CodexRoots(out)
			require.NoError(t, err)
			assert.True(t, found)
			assert.Contains(t, roots, "/w/zoe")
		})
	}
	_, _, err := CodexRoots("[sandbox_workspace_write]\nwritable_roots = [\n\"/w/a\",\n")
	assert.ErrorContains(t, err, "no closing ]")
}
