package sprint

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gcTree is a machine's scratch as the machinery leaves it, on a temp tree with a fake
// clock: a bud's working directory under the AI root (linked from the home, as the Studio
// links them) with its runner's log beside it, a friend's working directory outside the AI
// root, the bench root and land's clones.
type gcTree struct {
	t                               *testing.T
	home, ai, w, bench, land, other string
	now                             time.Time
}

func newGCTree(t *testing.T) *gcTree {
	base := t.TempDir()
	g := &gcTree{t: t, home: filepath.Join(base, "home"), ai: filepath.Join(base, "ai"),
		now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	g.w = filepath.Join(g.ai, "buds", "b1", "working")
	g.bench = filepath.Join(g.home, "nova-bench")
	g.land = filepath.Join(base, "cache", "land")
	g.other = filepath.Join(base, "elsewhere", "working")
	for _, d := range []string{g.home, g.w, g.bench, g.land, g.other} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	// the home's link to the bud's directory names the same directory as the AI root's
	// walk: it is gc'd once
	require.NoError(t, os.Symlink(g.w, filepath.Join(g.home, "b1-working")))
	// a working directory the home links to outside the AI root is never walked
	require.NoError(t, os.Symlink(g.other, filepath.Join(g.home, "stray-working")))
	return g
}

// file writes a file under the tree, its time age before now; its directories too.
func (g *gcTree) file(path, text string, age time.Duration) string {
	g.t.Helper()
	require.NoError(g.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(g.t, os.WriteFile(path, []byte(text), 0o644))
	g.age(path, age)
	return path
}

// age sets a path's time, and its directory's, age before now.
func (g *gcTree) age(path string, age time.Duration) {
	g.t.Helper()
	at := g.now.Add(-age)
	require.NoError(g.t, os.Chtimes(path, at, at))
	require.NoError(g.t, os.Chtimes(filepath.Dir(path), at, at))
}

// clone is a directory holding a .git directory, aged.
func (g *gcTree) clone(dir string, age time.Duration) string {
	g.t.Helper()
	g.file(filepath.Join(dir, "README.md"), strings.Repeat("x", 100), age)
	g.file(filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n", age)
	g.age(dir, age)
	return dir
}

func (g *gcTree) req(dry bool, dirty ...string) GCReq {
	return GCReq{
		Home: g.home, AIRoot: g.ai, BenchRoot: g.bench, LandRoot: g.land,
		MaxAge: GCMaxAge, Now: g.now, Dry: dry,
		Dirty: func(dir string) string {
			if slices.Contains(dirty, dir) {
				return "1 uncommitted path"
			}
			return ""
		},
		Worktrees: func(clone string) []string {
			return []string{filepath.Join(g.land, "wt-old"), filepath.Join(g.land, "wt-new"), filepath.Join(g.other, "wt-outside")}
		},
		Volume: func(string) (int, error) { return 83, nil },
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// gc removes exactly the scratch the machinery made and no longer needs, on the machine it
// runs on (the coordinator's clean-jobs.py freed 314 GiB on 2026-10-06 by hand; the owner,
// 2026-10-04: every coordinator need is a nova verb): job directories of finished or absent
// lanes, reader checkouts of recorded findings, lander worktrees and bench directories older
// than the max age; it refuses a path under no known scratch root and keeps a working tree
// with uncommitted work; it says one line per class with its count and bytes, and GC OK with
// what it freed and the volume's use. A dry run says the same and removes nothing.
func TestGcRemovesOnlyFinishedScratchUnderKnownRoots(t *testing.T) {
	t.Parallel()
	g := newGCTree(t)
	h := time.Hour
	jobs := func(j string) string { return filepath.Join(g.w, "jobs", j) }
	// finished: its report is written and its runner ENDed it
	g.clone(filepath.Join(jobs("done"), "repo"), 5*h)
	g.file(filepath.Join(g.w, "outbox", "done", "REPORT.md"), "Verdict: LAND\n", 4*h)
	// absent: the runner ENDed it with no report
	g.clone(filepath.Join(jobs("dead"), "repo"), 5*h)
	// live: a report is written but the runner has not ENDed it (its session is wrapping up)
	g.clone(filepath.Join(jobs("live"), "repo"), 5*h)
	g.file(filepath.Join(g.w, "outbox", "live", "REPORT.md"), "Verdict: LAND\n", 4*h)
	// finished, but its clone holds uncommitted work: never removed
	g.clone(filepath.Join(jobs("dirty"), "repo"), 5*h)
	g.file(filepath.Join(g.w, "outbox", "dirty", "REPORT.md"), "Verdict: HOLD\n", 4*h)
	// never started and new: no lane yet, kept
	g.file(filepath.Join(jobs("new"), "JOB.md"), "# JOB\n", h)
	// never in the runner's log and older than the max age: an absent lane
	g.file(filepath.Join(jobs("stale"), "JOB.md"), "# JOB\n", 3*24*h)
	g.file(filepath.Join(g.ai, "buds", "b1", "runner.log"), strings.Join([]string{
		"2026-10-06 06:00:00 AM START done tier=- model=m",
		"2026-10-06 06:00:00 AM START dead tier=- model=m",
		"2026-10-06 06:00:00 AM START live tier=- model=m",
		"2026-10-06 06:00:00 AM START dirty tier=- model=m",
		"2026-10-06 08:00:00 AM END done model=m exit=0 wall=1s report=Verdict: LAND",
		"2026-10-06 08:00:00 AM END dead model=m exit=1 wall=1s report=no",
		"2026-10-06 08:00:00 AM END dirty model=m exit=0 wall=1s report=Verdict: HOLD",
		"2026-10-06 11:00:00 AM START new tier=- model=m",
	}, "\n")+"\n", h)
	// the inbox and the outbox are never touched
	g.file(filepath.Join(g.w, "inbox", "done", "BRIEF.md"), "STATUS: x\n", 5*h)
	// reads: one with its finding recorded, one still running
	g.file(filepath.Join(g.w, "reads", "r-done", "RESULT.md"), "Verdict: ok\nReport: fine\n", 2*h)
	g.file(filepath.Join(g.w, "reads", "r-open", "READ.md"), "read it\n", 2*h)
	// a job in a working directory outside the AI root: never walked
	g.file(filepath.Join(g.other, "jobs", "x", "JOB.md"), "# JOB\n", 30*24*h)
	g.file(filepath.Join(g.other, "outbox", "x", "REPORT.md"), "Verdict: LAND\n", 30*24*h)
	// lander worktrees: one past the max age, one new, one outside every known root
	g.clone(filepath.Join(g.land, "github.com-o-r-0123456789abcdef"), 30*24*h)
	g.clone(filepath.Join(g.land, "wt-old"), 3*24*h)
	g.clone(filepath.Join(g.land, "wt-new"), h)
	g.clone(filepath.Join(g.other, "wt-outside"), 30*24*h)
	// bench directories: runs and a bud's job copy past the max age, one young
	g.file(filepath.Join(g.bench, "runs", "run.OLD00001", "repo", "go.mod"), "module x\n", 3*24*h)
	g.age(filepath.Join(g.bench, "runs", "run.OLD00001"), 3*24*h)
	g.file(filepath.Join(g.bench, "runs", "run.NEW00001", "repo", "go.mod"), "module x\n", h)
	g.file(filepath.Join(g.bench, "buds", "b1", "jobs", "j-old", "repo", "go.mod"), "module x\n", 3*24*h)
	g.age(filepath.Join(g.bench, "buds", "b1", "jobs", "j-old"), 3*24*h)
	// the bench's cache is a cache, never a bench directory
	g.file(filepath.Join(g.bench, "cache", "go-build", "README"), "cache\n", 30*24*h)

	dirty := filepath.Join(jobs("dirty"), "repo")
	removed := []string{jobs("done"), jobs("dead"), jobs("stale"), filepath.Join(g.w, "reads", "r-done"),
		filepath.Join(g.land, "wt-old"), filepath.Join(g.bench, "runs", "run.OLD00001"), filepath.Join(g.bench, "buds", "b1", "jobs", "j-old")}
	kept := []string{jobs("live"), jobs("dirty"), jobs("new"), filepath.Join(g.w, "reads", "r-open"),
		filepath.Join(g.w, "inbox", "done"), filepath.Join(g.w, "outbox", "done", "REPORT.md"),
		filepath.Join(g.other, "jobs", "x"), filepath.Join(g.land, "wt-new"), filepath.Join(g.other, "wt-outside"),
		filepath.Join(g.land, "github.com-o-r-0123456789abcdef"), filepath.Join(g.bench, "runs", "run.NEW00001"),
		filepath.Join(g.bench, "cache", "go-build", "README"), filepath.Join(g.ai, "buds", "b1", "runner.log")}

	// the dry run says what it would free and removes nothing
	dry := GC(g.req(true, dirty))
	for _, p := range append(append([]string(nil), removed...), kept...) {
		assert.True(t, exists(p), "a dry run removed %s", p)
	}
	assert.Positive(t, dry.Freed)

	res := GC(g.req(false, dirty))
	for _, p := range removed {
		assert.False(t, exists(p), "finished scratch under a known root was left: %s", p)
	}
	for _, p := range kept {
		assert.True(t, exists(p), "gc removed what the machinery still needs or never made: %s", p)
	}
	assert.Equal(t, dry.Freed, res.Freed, "the dry run's bytes are the run's")
	assert.Zero(t, res.Failed)

	byClass := map[string]GCClass{}
	for _, c := range res.Classes {
		byClass[c.Name] = c
	}
	assert.Equal(t, []string{GCJobs, GCReads, GCLanders, GCBench, GCCache}, func() (ns []string) {
		for _, c := range res.Classes {
			ns = append(ns, c.Name)
		}
		return ns
	}(), "one line per class, in order")
	assert.Equal(t, 3, byClass[GCJobs].Count)
	assert.Equal(t, 1, byClass[GCReads].Count)
	assert.Equal(t, 1, byClass[GCLanders].Count)
	assert.Equal(t, 2, byClass[GCBench].Count)
	assert.Positive(t, byClass[GCJobs].Bytes)

	text := strings.Join(res.Lines(), "\n")
	assert.Contains(t, text, "GC jobs count=3 bytes=")
	assert.Contains(t, text, "GC cache count=0 bytes=0")
	assert.Contains(t, text, "GC KEPT class=jobs path="+dirty+" why=1 uncommitted path")
	assert.Contains(t, text, "GC REFUSED class=jobs path="+filepath.Join(g.home, "stray-working"))
	assert.Contains(t, text, "GC REFUSED class=landers path="+filepath.Join(g.other, "wt-outside"))
	assert.Contains(t, text, "not under a known scratch root")
	last := res.Lines()[len(res.Lines())-1]
	assert.Regexp(t, `^GC OK freed=[1-9][0-9]* volume=83%$`, last)
	assert.Equal(t, res.Freed, byClass[GCJobs].Bytes+byClass[GCReads].Bytes+byClass[GCLanders].Bytes+byClass[GCBench].Bytes+byClass[GCCache].Bytes)

	// a second pass finds nothing left to free
	again := GC(g.req(false, dirty))
	assert.Zero(t, again.Freed)
}

// A known root that is itself no directory the machinery made is refused whole: an AI
// root, a bench root or a land root that is the home or the disk removes nothing.
func TestGcRefusesARootThatIsTheHome(t *testing.T) {
	t.Parallel()
	g := newGCTree(t)
	g.file(filepath.Join(g.home, "runs", "run.OLD00001", "x"), "x", 30*24*time.Hour)
	r := g.req(false)
	r.BenchRoot = g.home
	res := GC(r)
	assert.True(t, exists(filepath.Join(g.home, "runs", "run.OLD00001", "x")))
	assert.Contains(t, strings.Join(res.Lines(), "\n"), "GC REFUSED class=bench path="+g.home)
}
