//go:build unix

package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaude is a claude on PATH that records what it was run with into rec:
// its CLAUDE_CONFIG_DIR, its stdin and its arguments (NUL-separated), and
// writes outbox's REPORT.md and RESULT.md when write is true. It answers the
// Exec a lane runs: the real one, over a PATH holding only the fake's
// directory and the system's.
func fakeClaude(t *testing.T, outbox string, write bool) (run Exec, rec string) {
	t.Helper()
	bin, rec := t.TempDir(), t.TempDir()
	script := "#!/bin/sh\n" +
		"printf '%s' \"$CLAUDE_CONFIG_DIR\" > '" + rec + "/config_dir'\n" +
		"cat > '" + rec + "/stdin'\n" +
		"printf '%s\\0' \"$@\" > '" + rec + "/args'\n" +
		"echo '{\"type\":\"result\",\"result\":\"done, and nothing here is read\"}'\n"
	if write {
		script += "mkdir -p '" + outbox + "' && echo 'Verdict: LAND' > '" + outbox + "/REPORT.md' && echo 'RESULT: c1' > '" + outbox + "/RESULT.md'\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755))
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		return RealExec(ctx, dir, "/usr/bin/env", append([]string{"PATH=" + bin + ":/usr/bin:/bin", name}, args...), stdin)
	}, rec
}

// A claude one-shot lane runs the card as `claude -p` with the friend row's
// config_dir as CLAUDE_CONFIG_DIR, stdin empty and the brief as the prompt,
// and reads the result from the card's outbox: written, the card ran; not
// written, the run is a failed attempt whatever the model printed; a row
// without config_dir runs nothing and names the remedy.
func TestClaudeOneShotLanePassesConfigDir(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, configDir string
		write           bool
		wantErr         string
	}{
		{name: "the outbox written", configDir: "/accounts/heavy-a", write: true},
		{name: "no outbox result", configDir: "/accounts/heavy-a", wantErr: "holds no REPORT.md and no RESULT.md"},
		{name: "no config_dir", wantErr: "friend bob is a claude friend in one-shot mode with no config_dir, and a lane runs only as her own account (CLAUDE_CONFIG_DIR); run: nova-config friend set bob --config_dir <her account's absolute config directory>, or nova-friend run --config-dir <dir>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
			brief := "STATUS: nova-sprint card c1, epoch 15\n\nTHE TASK. Write 'both' files; a line that starts -p is still the prompt.\n"
			c := Card{ID: "c1", Brief: filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c1~15")}
			require.NoError(t, os.WriteFile(c.Brief, []byte(brief), 0o644))
			run, rec := fakeClaude(t, c.Outbox, tc.write)
			var out strings.Builder
			cl := &Claude{Stub: Stub{Harness: "claude"}, Friend: "bob", Dir: dir, Run: run, Out: &out, ConfigDir: func() string { return tc.configDir }}
			var _ CardRunner = cl

			lt, err := cl.RunCard(context.Background(), c)
			if tc.configDir == "" {
				assert.EqualError(t, err, tc.wantErr)
				assert.Equal(t, tc.wantErr, cl.Refusal())
				assert.NoFileExists(t, filepath.Join(rec, "args"), "nothing ran")
				return
			}
			assert.Empty(t, cl.Refusal())
			assert.Equal(t, 0, lt.Exit)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				assert.ErrorContains(t, err, "claude -p exited 0 and "+c.Outbox)
			} else {
				assert.NoError(t, err)
				assert.FileExists(t, c.Result())
			}
			got, err := os.ReadFile(filepath.Join(rec, "config_dir"))
			require.NoError(t, err)
			assert.Equal(t, tc.configDir, string(got), "CLAUDE_CONFIG_DIR is the row's config_dir")
			got, err = os.ReadFile(filepath.Join(rec, "stdin"))
			require.NoError(t, err)
			assert.Empty(t, got, "stdin is /dev/null")
			got, err = os.ReadFile(filepath.Join(rec, "args"))
			require.NoError(t, err)
			assert.Equal(t, append([]string{"-p", brief, "--add-dir", dir, "--output-format", "stream-json", "--verbose"}, ClaudeTrim...), strings.Split(strings.TrimSuffix(string(got), "\x00"), "\x00"), "the prompt is the brief")
			assert.Contains(t, out.String(), "nothing here is read", "the output goes to the record, and only there")
		})
	}
}
