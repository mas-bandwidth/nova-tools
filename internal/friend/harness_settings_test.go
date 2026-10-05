package friend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jobTemp is a directory inside this card's job, the parent of the module.
// Install tests write a fake home there and nowhere under a real login.
func jobTemp(t *testing.T) (root, home, work string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	module := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	root, err := os.MkdirTemp(filepath.Dir(module), "hs-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	home = filepath.Join(root, "home")
	work = filepath.Join(root, "work")
	require.NoError(t, os.Mkdir(home, 0o755))
	require.NoError(t, os.Mkdir(work, 0o755))
	return root, home, work
}

// installSettings writes s and checks the files on disk against the twin.
// Drift after that write is empty: check compares, it does not repair.
func installSettings(t *testing.T, s Settings) Prepared {
	t.Helper()
	p, err := s.Prepare()
	require.NoError(t, err)
	require.NoError(t, p.Write())
	for path, body := range p.Files {
		raw, err := os.ReadFile(path)
		require.NoError(t, err, path)
		assert.Equal(t, body, string(raw), path)
	}
	drifts, err := SettingsDrift(s.Home, s.Friend)
	require.NoError(t, err)
	assert.Empty(t, drifts)
	return p
}

// TestInstallWritesTheHarnessSettingsAFriendNeeds is the card's test: each
// adapter's settings land in a fake home, a symlinked directory is refused,
// and check names a drift without rewriting the file (docs/SPEC-FRIEND.md,
// harness settings).
func TestInstallWritesTheHarnessSettingsAFriendNeeds(t *testing.T) {
	t.Parallel()

	t.Run("codex", func(t *testing.T) {
		_, home, work := jobTemp(t)
		s := Settings{Friend: "ada", Harness: "codex", Home: home, Dir: work}
		p := installSettings(t, s)
		config := filepath.Join(home, ".codex", "config.toml")
		assert.Contains(t, p.Files[config], "sandbox_mode = \"workspace-write\"")
		assert.Contains(t, p.Files[config], "writable_roots = ["+strconv.Quote(work)+"]")
		_, err := os.Stat(intentPath(home, "ada"))
		require.NoError(t, err)

		// A root that is itself a symlink is not written, and the file that
		// already names one keeps every other byte except that root.
		other := filepath.Join(filepath.Dir(home), "other")
		require.NoError(t, os.Mkdir(other, 0o755))
		linked := filepath.Join(filepath.Dir(home), "linked-root")
		require.NoError(t, os.Symlink(other, linked))
		seed := "writable_roots = [" + strconv.Quote(linked) + "]\n"
		require.NoError(t, os.WriteFile(config, []byte(seed), 0o644))
		p, err = s.Prepare()
		require.NoError(t, err)
		assert.Contains(t, strings.Join(p.drifts, "\n"), "symlink")
		require.NoError(t, p.Write())
		raw, err := os.ReadFile(config)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), linked)
		assert.Contains(t, string(raw), work)
		drifts, err := SettingsDrift(home, "ada")
		require.NoError(t, err)
		assert.Empty(t, drifts)
	})

	t.Run("codex symlink refused", func(t *testing.T) {
		root, home, work := jobTemp(t)
		link := filepath.Join(root, "link")
		require.NoError(t, os.Symlink(work, link))
		_, err := (Settings{Friend: "ada", Harness: "codex", Home: home, Dir: link}).Prepare()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
		assert.NoFileExists(t, filepath.Join(home, ".codex", "config.toml"))
		_, statErr := os.Stat(intentPath(home, "ada"))
		assert.True(t, os.IsNotExist(statErr))
	})

	t.Run("codex unreadable roots stay", func(t *testing.T) {
		_, home, work := jobTemp(t)
		configDir := filepath.Join(home, ".codex")
		require.NoError(t, os.Mkdir(configDir, 0o755))
		config := filepath.Join(configDir, "config.toml")
		seed := "writable_roots = [\n  \"/tmp/x\"\n]\n"
		require.NoError(t, os.WriteFile(config, []byte(seed), 0o644))
		s := Settings{Friend: "ada", Harness: "codex", Home: home, Dir: work}
		p, err := s.Prepare()
		require.NoError(t, err)
		assert.Contains(t, strings.Join(p.drifts, "\n"), "cannot read as one line")
		require.NoError(t, p.Write())
		raw, err := os.ReadFile(config)
		require.NoError(t, err)
		assert.Equal(t, seed, string(raw))
	})

	t.Run("dsh", func(t *testing.T) {
		_, home, work := jobTemp(t)
		s := Settings{Friend: "ada", Harness: "dsh", Home: home, Dir: work}
		p := installSettings(t, s)
		settings := filepath.Join(home, ".dsh", "settings.yaml")
		assert.Contains(t, p.Files[settings], "default: \"\"")
		kept := "theme: keep\nagent-presets:\n  default: \"minimal\"\n"
		require.NoError(t, os.WriteFile(settings, []byte(kept), 0o644))
		p, err := s.Prepare()
		require.NoError(t, err)
		assert.Contains(t, p.Files[settings], "theme: keep")
		require.NoError(t, p.Write())
		raw, err := os.ReadFile(settings)
		require.NoError(t, err)
		assert.Contains(t, string(raw), "theme: keep")
		assert.Contains(t, string(raw), `default: ""`)
		drifts, err := SettingsDrift(home, "ada")
		require.NoError(t, err)
		assert.Empty(t, drifts)

		mutated := "agent-presets:\n  default: minimal\n"
		require.NoError(t, os.WriteFile(settings, []byte(mutated), 0o644))
		drifts, err = SettingsDrift(home, "ada")
		require.NoError(t, err)
		assert.Contains(t, strings.Join(drifts, "\n"), `dsh agent_preset: installed "minimal", want ""`)
		raw, err = os.ReadFile(settings)
		require.NoError(t, err)
		assert.Equal(t, mutated, string(raw))
	})

	t.Run("grok", func(t *testing.T) {
		_, home, work := jobTemp(t)
		s := Settings{Friend: "ada", Harness: "grok", Home: home, Dir: work}
		p := installSettings(t, s)
		wake := filepath.Join(work, "nova-friend.wake")
		assert.Equal(t, wake, p.Wake)
		_, err := os.Stat(wake)
		require.NoError(t, err)
		record := filepath.Join(home, ".grok", grokWakeRecord)
		raw, err := os.ReadFile(record)
		require.NoError(t, err)
		assert.Equal(t, wake+"\n", string(raw))

		require.NoError(t, os.WriteFile(record, []byte("nope\n"), 0o644))
		drifts, err := SettingsDrift(home, "ada")
		require.NoError(t, err)
		assert.Contains(t, strings.Join(drifts, "\n"), `grok wake_file: installed "nope", want `+strconv.Quote(wake))
		raw, err = os.ReadFile(record)
		require.NoError(t, err)
		assert.Equal(t, "nope\n", string(raw))
	})

	t.Run("claude", func(t *testing.T) {
		_, home, work := jobTemp(t)
		s := Settings{Friend: "ada", Harness: "claude", Home: home, Dir: work}
		p := installSettings(t, s)
		info, err := os.Lstat(p.ConfigDir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		assert.Equal(t, filepath.Join(home, ".claude"), p.ConfigDir)
		a := Agent{
			Friend: "ada", Harness: "claude", Dir: work, Home: home,
			Binary: "/opt/nova/bin/nova-friend", Redis: "127.0.0.1:1", Server: "127.0.0.1:1",
			Path: "/usr/bin:/bin", LaunchdLog: filepath.Join(home, "log"), ConfigDir: p.ConfigDir,
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(a.PlistPath()), 0o755))
		require.NoError(t, os.WriteFile(a.PlistPath(), []byte(a.Plist()), 0o644))
		drifts, err := SettingsDrift(home, "ada")
		require.NoError(t, err)
		assert.Empty(t, drifts)

		wrong := strings.ReplaceAll(a.Plist(), p.ConfigDir, "/elsewhere")
		require.NoError(t, os.WriteFile(a.PlistPath(), []byte(wrong), 0o644))
		drifts, err = SettingsDrift(home, "ada")
		require.NoError(t, err)
		assert.Contains(t, strings.Join(drifts, "\n"), `claude config_dir: installed "/elsewhere", want `+strconv.Quote(p.ConfigDir))
		raw, err := os.ReadFile(a.PlistPath())
		require.NoError(t, err)
		assert.Contains(t, string(raw), "/elsewhere")
	})

	t.Run("claude symlink refused", func(t *testing.T) {
		root, home, work := jobTemp(t)
		real := filepath.Join(root, "real-claude")
		require.NoError(t, os.Mkdir(real, 0o755))
		require.NoError(t, os.Symlink(real, filepath.Join(home, ".claude")))
		_, err := (Settings{Friend: "ada", Harness: "claude", Home: home, Dir: work}).Prepare()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
	})

	t.Run("opencode", func(t *testing.T) {
		_, home, work := jobTemp(t)
		dir := filepath.Join(home, ".config", "opencode")
		require.NoError(t, os.MkdirAll(dir, 0o755))
		path := filepath.Join(dir, openCodeGlobalConfig)
		seed := "{\n  \"theme\": \"keep\"\n}\n"
		require.NoError(t, os.WriteFile(path, []byte(seed), 0o644))
		s := Settings{Friend: "ada", Harness: "opencode", Home: home, Dir: work, Model: "opencode/friend-model"}
		p := installSettings(t, s)
		var cfg map[string]any
		require.NoError(t, json.Unmarshal([]byte(p.Files[path]), &cfg))
		assert.Equal(t, "keep", cfg["theme"])
		assert.Equal(t, "opencode/friend-model", cfg["model"])

		require.NoError(t, os.WriteFile(path, []byte("{\n  \"theme\": \"keep\",\n  \"model\": \"other/model\"\n}\n"), 0o644))
		drifts, err := SettingsDrift(home, "ada")
		require.NoError(t, err)
		assert.Contains(t, strings.Join(drifts, "\n"), `opencode model: installed "other/model", want "opencode/friend-model"`)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(raw), "other/model")
	})

	t.Run("opencode without a model writes nothing", func(t *testing.T) {
		_, home, work := jobTemp(t)
		s := Settings{Friend: "ada", Harness: "opencode", Home: home, Dir: work}
		p, err := s.Prepare()
		require.NoError(t, err)
		assert.Empty(t, p.Files)
		require.NoError(t, p.Write())
		_, err = os.Stat(filepath.Join(home, ".config"))
		assert.True(t, os.IsNotExist(err))
		_, err = os.Stat(intentPath(home, "ada"))
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("antigravity", func(t *testing.T) {
		root, home, work := jobTemp(t)
		s := Settings{Friend: "ada", Harness: "antigravity", Home: home, Dir: work}
		installSettings(t, s)
		data := filepath.Join(home, filepath.FromSlash(AntigravityData))
		info, err := os.Lstat(data)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		require.NoError(t, os.Remove(data))
		real := filepath.Join(root, "real-ag")
		require.NoError(t, os.Mkdir(real, 0o755))
		require.NoError(t, os.Symlink(real, data))
		drifts, err := SettingsDrift(home, "ada")
		require.NoError(t, err)
		assert.Contains(t, strings.Join(drifts, "\n"), "symlink")
		info, err = os.Lstat(data)
		require.NoError(t, err)
		assert.True(t, info.Mode()&os.ModeSymlink != 0)
	})

	t.Run("gemini writes nothing", func(t *testing.T) {
		_, home, work := jobTemp(t)
		p, err := (Settings{Friend: "ada", Harness: "gemini", Home: home, Dir: work}).Prepare()
		require.NoError(t, err)
		assert.Empty(t, p.Files)
		require.NoError(t, p.Write())
		_, err = os.Stat(intentPath(home, "ada"))
		assert.True(t, os.IsNotExist(err))
	})
}
