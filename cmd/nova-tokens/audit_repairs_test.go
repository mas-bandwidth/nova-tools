package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A path that is not what its flag wants is refused, exit 2, naming the flag and the path,
// and the run writes nothing: a regular file at --out is never overwritten (#1502), and a
// fold.lock that is a symlink is refused before anything is written through it, whether its
// target exists or not.
func TestEveryPathAFlagCannotUseIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	repos := reposFile(t, dir)
	no := func(name string) string { return filepath.Join(dir, "no-"+name) }
	tr := filepath.Dir(testkit.WriteFile(t, filepath.Join(dir, "tr", "a.jsonl"), msg("m1", "2026-09-11T10:00:00Z", "fable", map[string]int{"input_tokens": 100, "output_tokens": 10}, "/x/schema/a.go")+"\n"))
	fold := func(out string) []string {
		return []string{"fold", "--out", out, "--day", "2026-09-11", "--repos", repos, "--claude", "glenn=" + tr}
	}
	sources := func(flags ...string) []string {
		return append([]string{"sources", "--all", "--repos", repos}, flags...)
	}
	file := testkit.WriteFile(t, filepath.Join(dir, "outfile"), "a regular file\n")
	const body = "unrelated data must survive\n"
	kept := testkit.WriteFile(t, filepath.Join(dir, "unrelated"), body)
	linked := func(name, target string) string {
		out := testkit.Mkdir(t, filepath.Join(dir, name))
		require.NoError(t, os.Symlink(target, filepath.Join(out, tokens.LockName)))
		return out
	}
	lockOnly := func(t *testing.T, out string) {
		entries, err := os.ReadDir(out)
		require.NoError(t, err)
		require.Len(t, entries, 1, "the out directory holds more than the link")
		assert.Equal(t, tokens.LockName, entries[0].Name())
		info, err := os.Lstat(filepath.Join(out, tokens.LockName))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link is no longer a symlink")
	}
	for _, row := range []struct {
		name string
		args []string
		says []string
		then func(t *testing.T)
	}{
		{"sources: an absent --claude root", sources("--claude", "bench="+no("claude")), []string{"SOURCES REFUSED:", "--claude bench=" + no("claude") + " does not exist"}, nil},
		{"sources: an absent --bus root", sources("--bus", no("bus")), []string{"SOURCES REFUSED:", "--bus does not exist: " + no("bus")}, nil},
		{"sources: an absent --swarm root", sources("--swarm", "bench="+no("swarm")), []string{"SOURCES REFUSED:", "--swarm bench=" + no("swarm") + " does not exist"}, nil},
		{"sources: an absent --scratch root beside --opencode", sources("--opencode", "bench="+filepath.Join(dir, "db.sqlite"), "--scratch", no("scratch")), []string{"SOURCES REFUSED:", "--scratch does not exist: " + no("scratch")}, nil},
		{"profiles: an absent --swarm-root", []string{"profiles", "--swarm-root", no("pe")}, []string{"PROFILES REFUSED:", "does not exist", no("pe")}, nil},
		{"fold: an absent out directory is refused and not created", fold(no("out")), []string{"does not exist", no("out")}, func(t *testing.T) {
			assert.NoDirExists(t, no("out"))
		}},
		{"fold: a regular file at the out path is refused and untouched", fold(file), []string{"is not a directory", file}, func(t *testing.T) {
			assert.Equal(t, "a regular file\n", testkit.ReadFile(t, file), "the file at --out was overwritten")
		}},
		{"fold: a fold.lock linked at an existing target", fold(linked("out-kept", kept)), []string{"symlink", "fold.lock"}, func(t *testing.T) {
			assert.Equal(t, body, testkit.ReadFile(t, kept), "the symlink's target changed")
			lockOnly(t, filepath.Join(dir, "out-kept"))
		}},
		{"fold: a fold.lock linked at a missing target", fold(linked("out-gone", no("target"))), []string{"symlink", "fold.lock"}, func(t *testing.T) {
			assert.NoFileExists(t, no("target"), "the symlink's target was created")
			lockOnly(t, filepath.Join(dir, "out-gone"))
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			novaTokens.Do(t, row.args...).Exit(2).Err(row.says...)
			if row.then != nil {
				row.then(t)
			}
		})
	}
}

// check --through <day> is FAIL stale while the last folded day is older than it and OK from
// that day on, and refuses what is not a day (without naming --all, which it does not take).
func TestCheckThroughFlag(t *testing.T) {
	t.Parallel()

	out := filepath.Dir(testkit.WriteFile(t, filepath.Join(t.TempDir(), "2026-09-18.tsv"),
		"nova-tokens v1 day=2026-09-18 at=2026-09-18T23:55:00Z build=b turns=1 sources=x\n"+
			"date\tmodel\trepo\tinput\toutput\tcache_write\tcache_read\treasoning\trough\tday_basis\tsources\n"+
			"2026-09-18\tm\tr\t10\t10\t-\t-\t-\t0\tUTC\tx\n"))
	check := func(through string) testkit.Ran { return novaTokens.Do(t, "check", "--out", out, "--through", through) }
	check("not-a-day").Exit(2).Err("CHECK REFUSED: --through is not a day: not-a-day", "it wants YYYY-MM-DD").NotErr("--all")
	check("2026-09-20").Exit(1).Err("CHECK FAIL stale last=2026-09-18 through=2026-09-20")
	check("2026-09-18").Exit(0).Out("CHECK OK")
	check("2026-09-15").Exit(0).Out("CHECK OK")
}
