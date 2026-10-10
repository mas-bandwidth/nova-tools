package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/gocache"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
)

// friend clean (friendclean.go; docs/FRIENDS.md; ideas#833, the owner 2026-10-02: "we
// might just need the same thing for friends! eg. working directories." and "cleanup
// must be auto!"): a done job's clean clones and build output older than three days go,
// a dirty clone is listed and goes at fourteen days, a job not done is kept, and no text
// is touched. The fixture is real directories and real git; the clock is the test app's.

// cleanGit runs git for the fixture, with no global configuration and an identity.
func cleanGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: dir, Env: env, OwnRepo: dir != ""}, args...)
	require.NoError(t, err, "git %v: %s", args, res.Stderr)
}

// cleanOrigin is a bare origin holding one commit; the clone it returns makes a clean
// clone of it at a directory: nothing uncommitted, nothing unpushed.
func cleanOrigin(t *testing.T) (clone func(dir string)) {
	t.Helper()
	base := t.TempDir()
	origin, seed := filepath.Join(base, "origin.git"), filepath.Join(base, "seed")
	cleanGit(t, "", "init", "-q", "--bare", origin)
	cleanGit(t, "", "clone", "-q", origin, seed)
	testkit.Tree(t, seed, map[string]string{"main.go": "package main\n"})
	cleanGit(t, seed, "add", ".")
	cleanGit(t, seed, "commit", "-q", "-m", "one")
	cleanGit(t, seed, "push", "-q", "origin", "HEAD")
	return func(dir string) { cleanGit(t, "", "clone", "-q", origin, dir) }
}

// doneAt writes outbox/<job>/REPORT.md under w, its time age before now.
func doneAt(t *testing.T, w, job string, now time.Time, age time.Duration) {
	t.Helper()
	testkit.Tree(t, w, map[string]string{"outbox/" + job + "/REPORT.md": "Verdict: OK\n"})
	report := filepath.Join(w, "outbox", job, "REPORT.md")
	require.NoError(t, os.Chtimes(report, now.Add(-age), now.Add(-age)))
}

// friendTrees is the fixture: ada's and bo's working directories under root, a job of
// each kind the rule tells apart, and cy, on the roster with no directory here.
type friendTrees struct {
	ta   *testApp
	root string
	// gone are the paths the rule removes, kept the ones it never touches, dirty the
	// clones it lists and keeps
	gone, kept, dirty []string
}

func newFriendTrees(t *testing.T) friendTrees {
	t.Helper()
	ta := newTestApp(t)
	ta.a.friends = friendRows("ada", "bo", "cy")
	now, day := ta.a.now(), 24*time.Hour
	root := t.TempDir()
	ada, bo := filepath.Join(root, "ada-working"), filepath.Join(root, "bo-working")
	f := friendTrees{ta: ta, root: root}
	clone := cleanOrigin(t)

	// done, clean, old, today's layout (the clone inside the inbox job): the clone, the
	// build output and a worktree's leftovers go; the brief, the notes and the report stay
	j := "2026-09-20-done-clean"
	testkit.Tree(t, ada, map[string]string{
		"inbox/" + j + "/BRIEF.md":            "the brief\n",
		"inbox/" + j + "/gocache/00/x":        "cache",
		"inbox/" + j + "/wt-agent7/notes.txt": "a worktree's leftovers",
		"inbox/" + j + "/node_modules/a/i.js": "x",
		"inbox/" + j + "/notes/finding.md":    "text stays",
		"outbox/" + j + "/evidence.md":        "evidence\n",
	})
	clone(filepath.Join(ada, "inbox", j, "nova-tools"))
	doneAt(t, ada, j, now, 5*day)
	in := filepath.Join(ada, "inbox", j)
	f.gone = append(f.gone, filepath.Join(in, "nova-tools"), filepath.Join(in, "gocache"), filepath.Join(in, "wt-agent7"), filepath.Join(in, "node_modules"))
	f.kept = append(f.kept, filepath.Join(in, "BRIEF.md"), filepath.Join(in, "notes", "finding.md"),
		filepath.Join(ada, "outbox", j, "REPORT.md"), filepath.Join(ada, "outbox", j, "evidence.md"))

	// done, clean, old, the brief's layout (jobs/<job>/): its clone and target go
	j = "2026-09-21-jobs-layout"
	clone(filepath.Join(bo, "jobs", j, "schema"))
	testkit.Tree(t, bo, map[string]string{"inbox/" + j + "/BRIEF.md": "brief\n", "jobs/" + j + "/target/debug/x": "rust"})
	doneAt(t, bo, j, now, 4*day)
	f.gone = append(f.gone, filepath.Join(bo, "jobs", j, "schema"), filepath.Join(bo, "jobs", j, "target"))
	f.kept = append(f.kept, filepath.Join(bo, "inbox", j, "BRIEF.md"))

	// done, old, an uncommitted file: listed and kept
	j = "2026-09-22-done-dirty"
	c := filepath.Join(ada, "jobs", j, "message-bus")
	clone(c)
	testkit.Tree(t, c, map[string]string{"wip.go": "package main\n"})
	doneAt(t, ada, j, now, 6*day)
	f.dirty = append(f.dirty, c)
	f.kept = append(f.kept, filepath.Join(c, "wip.go"))

	// done, old, a commit never pushed: listed and kept
	j = "2026-09-23-done-unpushed"
	c = filepath.Join(bo, "inbox", j, "netcode")
	clone(c)
	cleanGit(t, c, "commit", "-q", "--allow-empty", "-m", "local only")
	doneAt(t, bo, j, now, 6*day)
	f.dirty = append(f.dirty, c)
	f.kept = append(f.kept, filepath.Join(c, "main.go"))

	// not done (outbox/<job>/ there, no REPORT.md), however old: kept
	j = "2026-09-01-working"
	c = filepath.Join(ada, "inbox", j, "nova")
	clone(c)
	testkit.Tree(t, ada, map[string]string{"outbox/" + j + "/progress.md": "half\n", "inbox/" + j + "/gocache/x": "c"})
	f.kept = append(f.kept, filepath.Join(c, "main.go"), filepath.Join(ada, "inbox", j, "gocache", "x"))

	// done, clean, but a day ago: kept
	j = "2026-10-01-done-young"
	c = filepath.Join(ada, "inbox", j, "rocketnet")
	clone(c)
	doneAt(t, ada, j, now, day)
	f.kept = append(f.kept, filepath.Join(c, "main.go"))

	// done, dirty, past fourteen days: removed whatever its state, and said so
	j = "2026-09-10-done-dirty-old"
	c = filepath.Join(bo, "jobs", j, "reliable")
	clone(c)
	testkit.Tree(t, c, map[string]string{"forgotten.go": "package main\n"})
	doneAt(t, bo, j, now, 15*day)
	f.gone = append(f.gone, c)

	// the friend's own files beside the jobs are no job
	testkit.Tree(t, ada, map[string]string{"ada/memory.md": "mine\n", "inbox/README.md": "how to read the inbox\n"})
	f.kept = append(f.kept, filepath.Join(ada, "ada", "memory.md"), filepath.Join(ada, "inbox", "README.md"))
	return f
}

func (f friendTrees) run(extra ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := f.ta.a.run(append([]string{"friend", "clean", "--root", f.root}, extra...), &out, &errb)
	return code, out.String(), errb.String()
}

func pathThere(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestFriendCleanRemovesDoneCleanOldClonesAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	f := newFriendTrees(t)
	code, out, errs := f.run()
	require.Equal(t, 0, code, "out:\n%s\nerr:\n%s", out, errs)
	for _, p := range f.gone {
		assert.False(t, pathThere(p), "%s should be gone\n%s", p, out)
		assert.Contains(t, out, "FRIENDS-CLEAN REMOVED friend=", "the removal is said")
	}
	for _, p := range append(f.kept, f.dirty...) {
		assert.True(t, pathThere(p), "%s should be kept\n%s", p, out)
	}
	for _, p := range f.dirty {
		assert.Contains(t, out, "FRIENDS-CLEAN DIRTY friend=", "a dirty clone is listed")
		assert.Contains(t, out, "path="+p+" ", "the dirty clone is listed by its path")
	}
	assert.Contains(t, out, "kind=clone dirty=1 uncommitted path", "the clone removed at fourteen days says it was dirty")
	assert.Contains(t, out, "FRIENDS-CLEAN FRIEND cy dir=", "a friend with no directory here is named")
	assert.Regexp(t, `^FRIENDS-CLEAN OK freed=[1-9][0-9]* listed=2$`, lastLine(out))

	// a second run has nothing left to remove and lists the same two
	code, out, _ = f.run()
	require.Equal(t, 0, code)
	assert.NotContains(t, out, "REMOVED")
	assert.Equal(t, "FRIENDS-CLEAN OK freed=0 listed=2", lastLine(out))
}

// --dry-run prints every action with the bytes it would free, and removes nothing.
func TestFriendCleanDryRunRemovesNothing(t *testing.T) {
	t.Parallel()
	f := newFriendTrees(t)
	code, out, errs := f.run("--dry-run")
	require.Equal(t, 0, code, "out:\n%s\nerr:\n%s", out, errs)
	for _, p := range append(append(f.gone, f.kept...), f.dirty...) {
		assert.True(t, pathThere(p), "a dry run removed %s\n%s", p, out)
	}
	for _, p := range f.gone {
		assert.Contains(t, out, "FRIENDS-CLEAN WOULD-REMOVE friend=", "the dry run names what it would remove")
		assert.Contains(t, out, "path="+p+" ")
	}
	assert.NotContains(t, out, "REMOVED friend=")
	dry := lastLine(out)
	assert.Regexp(t, `^FRIENDS-CLEAN OK freed=[1-9][0-9]* listed=2 dry-run: nothing was removed$`, dry)

	// the real run frees what the dry run said it would
	_, out, _ = f.run()
	assert.Equal(t, strings.TrimSuffix(dry, " dry-run: nothing was removed"), lastLine(out))
}

// --days is the age a done job's clean clones are kept for; a dirty clone still waits
// for fourteen. --json is one object with the same lines.
func TestFriendCleanDaysIsTheAgeOfADoneJob(t *testing.T) {
	t.Parallel()
	f := newFriendTrees(t)
	code, out, _ := f.run("--days", "10", "--dry-run", "--json")
	require.Equal(t, 0, code, out)
	var got struct {
		Freed  int64    `json:"freed"`
		Listed int      `json:"listed"`
		DryRun bool     `json:"dry_run"`
		Lines  []string `json:"lines"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	all := strings.Join(got.Lines, "\n")
	assert.NotContains(t, all, "2026-09-20-done-clean", "a job done five days ago is young at --days 10")
	assert.Contains(t, all, "2026-09-10-done-dirty-old", "fifteen days is past both")
	assert.Zero(t, got.Listed)
	assert.True(t, got.DryRun)
	assert.Positive(t, got.Freed)
}

// A friend's build cache, <name>-working/.cache/go-build, is held under the limit as the
// pool's is: least recently used first, never an entry used in the last two hours.
func TestFriendCleanCapsAFriendsGoBuildCache(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	root := t.TempDir()
	cache := filepath.Join(root, "ada-working", ".cache", "go-build")
	hex := "0123456789abcdef"
	entry := func(i int, age time.Duration) string {
		sub := "0" + string(hex[i])
		name := strings.Repeat(string(hex[i]), 64) + "-d"
		testkit.Tree(t, cache, map[string]string{sub + "/" + name: strings.Repeat("x", 1000)})
		p := filepath.Join(cache, sub, name)
		require.NoError(t, os.Chtimes(p, now.Add(-age), now.Add(-age)))
		return p
	}
	old := []string{entry(0, 30*time.Hour), entry(1, 20*time.Hour), entry(2, 10*time.Hour)}
	fresh := entry(3, time.Hour)
	for _, dry := range []bool{true, false} {
		cl := &friendClean{root: root, days: cleanDays, dry: dry, now: now, cache: gocache.Bounds{Limit: 2500, Slack: 500, Remove: 1 << 30}}
		cl.cleanCache("ada", filepath.Join(root, "ada-working"))
		assert.Equal(t, int64(2000), cl.freed, "dry=%v", dry)
		assert.Contains(t, strings.Join(cl.lines, "\n"), "FRIENDS-CLEAN CACHE friend=ada")
		assert.Equal(t, dry, pathThere(old[0]), "dry=%v", dry)
		assert.Equal(t, dry, pathThere(old[1]), "dry=%v", dry)
		assert.True(t, pathThere(old[2]), "the trim stops once under the limit less the slack")
		assert.True(t, pathThere(fresh))
	}
}

// --file reads the roster from a nova-config store file, as nova-config --file keeps it.
func TestFriendCleanReadsTheRosterFromAStoreFile(t *testing.T) {
	t.Parallel()
	f := newFriendTrees(t)
	path := filepath.Join(t.TempDir(), "config.json")
	st, err := config.OpenFile(path)
	require.NoError(t, err)
	_, _, _, err = st.Migrate(context.Background())
	require.NoError(t, err)
	_, err = st.Insert(context.Background(), config.KindFriend, config.Row{Name: "bo", Fields: map[string]string{"slots": "1", "tiers": "flash"}}, "t")
	require.NoError(t, err)
	code, out, errs := f.run("--file", path, "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "FRIENDS-CLEAN FRIEND bo ")
	assert.NotContains(t, out, "FRIENDS-CLEAN FRIEND ada ", "only the file's friends")
}

// Every refusal names what it wants and removes nothing.
func TestFriendCleanRefusals(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.friends = friendRows()
	dir := t.TempDir()
	for _, tc := range []struct {
		line string
		code int
		says string
	}{
		{"friend clean --root " + dir + " --days 0", 2, "--days is at least 1"},
		{"friend clean --root " + filepath.Join(dir, "nope"), 2, "--root wants the directory"},
		{"friend clean --root " + dir + " ada", 2, "takes no words"},
		{"friend clean --root " + dir, exitCannotRead, "holds no friend row"},
		{"friend clean --root " + dir + " --pg postgres://x@h/db --file f.json", 2, "exclusive"},
		{"friend clean --root " + dir + " --file " + filepath.Join(dir, "absent.json"), exitCannotRead, "the config cannot be read"},
	} {
		code, _, errs := ta.do(tc.line)
		assert.Equal(t, tc.code, code, "%s: %s", tc.line, errs)
		assert.Contains(t, errs, tc.says, tc.line)
	}
}
