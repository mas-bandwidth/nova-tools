package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// readStartRE is the line of a read's JOB.md that shows the work's change.
var readStartRE = regexp.MustCompile(`(?m)^    git diff --stat ([0-9a-f]{40})\.\.HEAD$`)

// readFixture is a work card's one file on its sprint branch in an origin whose main moves,
// and, when stale, a bench mirror cloned before any of it, as on a bench whose mirror lags.
type readFixture struct {
	repo, bench, head, start string
}

// newReadFixture lands landedBefore on main, cuts the work from main there (start), tags
// start v1, then lands landedAfter. A stale fixture names origin by URL, so a stage clones
// from the bench mirror, which holds only the first commit.
func newReadFixture(t *testing.T, landedBefore, landedAfter []string, stale bool) readFixture {
	t.Helper()
	root := t.TempDir()
	seed, origin := filepath.Join(root, "seed"), filepath.Join(root, "origin.git")
	f := readFixture{repo: origin, bench: filepath.Join(root, "bench")}
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	write(t, filepath.Join(seed, "f"), "base\n")
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)
	if stale {
		f.repo = "file://" + origin
		runGit(t, "", "clone", "-q", "--mirror", "--", origin, filepath.Join(f.bench, "nova-bench", "mirror", "origin.git"))
	}
	land := func(names []string) {
		if len(names) == 0 {
			return
		}
		l := filepath.Join(root, "land-"+names[0])
		runGit(t, "", "clone", "-q", "--", origin, l)
		for _, name := range names {
			write(t, filepath.Join(l, name), name+"\n")
			gitAs(t, l, "add", name)
			gitAs(t, l, "commit", "-q", "-m", name)
		}
		runGit(t, l, "push", "-q", "origin", "main")
	}
	land(landedBefore)
	f.start = gitAs(t, origin, "rev-parse", "main")
	runGit(t, origin, "tag", "v1", f.start)

	w := filepath.Join(root, "w")
	runGit(t, "", "clone", "-q", "--", origin, w)
	require.NoError(t, os.MkdirAll(filepath.Join(w, "quacks"), 0o755))
	write(t, filepath.Join(w, "quacks", "w.txt"), "quack\n")
	gitAs(t, w, "add", "quacks/w.txt")
	gitAs(t, w, "commit", "-q", "-m", "quack: w")
	f.head = gitAs(t, w, "rev-parse", "HEAD")
	runGit(t, w, "push", "-q", "origin", "HEAD:refs/heads/sprint/w")
	land(landedAfter)
	return f
}

// frame is the read of the fixture's work, reviewed against base.
func (f readFixture) frame(base string) *cardcontract.Frame {
	return &cardcontract.Frame{Kind: "read", Card: "w.r1", Attempt: 1, Model: "fake/fake-model", Repo: f.repo,
		BaseRef: "main", ReviewBase: base, StageSha: f.head, Branch: "sprint/w"}
}

// stage stages the read's checkout into <slot>/jobs/w.r1 and returns the job directory.
func (f readFixture) stage(t *testing.T, slot string) string {
	t.Helper()
	job := filepath.Join(slot, "jobs", "w.r1")
	require.NoError(t, os.MkdirAll(job, 0o755))
	st, err := swarm.StageCard(swarm.StageOptions{TargetDir: filepath.Join(job, swarm.JobRepo), JobDir: job,
		BenchHome: f.bench, Base: &swarm.CardBase{Repo: f.repo, Sha: f.head, Ref: "main", Named: f.repo}, Branch: "sprint/w"})
	require.NoError(t, err)
	require.Equal(t, f.head, st.BaseSha)
	return job
}

// assertExactlyTheWork reads the job's JOB.md and asserts it names the work's start and
// that its diff is exactly the work's one file.
func (f readFixture) assertExactlyTheWork(t *testing.T, job string) {
	t.Helper()
	jobText, err := os.ReadFile(filepath.Join(job, cardcontract.JobName))
	require.NoError(t, err)
	m := readStartRE.FindStringSubmatch(string(jobText))
	require.NotNil(t, m, "JOB.md names the command that shows the work's change:\n%s", jobText)
	assert.Equal(t, f.start, m[1], "the start is the commit the work began from")
	assert.Equal(t, []string{"quacks/w.txt"}, strings.Fields(runGit(t, filepath.Join(job, swarm.JobRepo), "diff", "--name-only", m[1]+"..HEAD")),
		"the diff JOB.md names is exactly the work's one file")
}

// The diff a read's JOB.md names is exactly the work's one file wherever the base is when
// the read is staged: not moved; moved after the work began, with the read's checkout at
// the newer base; and moved with the read cloned from a bench mirror whose base is older
// than the commit the work started from. On the 1000-card load test of 2026-10-01 the
// mirror's base lagged the work's start by many landed cards, the start was the merge base
// against that older tip, and readers judged correct work broken: "diff has 22 files not
// exactly one" (docs/SPEC-CARD-CONTRACT.md, JOB.md).
func TestAReadsDiffIsExactlyTheWorkWhereverTheBaseIs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                      string
		landedBefore, landedAfter []string
		stale                     bool
	}{
		{name: "base not moved"},
		{name: "base moved after the work began", landedAfter: []string{"landed-a.txt", "landed-b.txt"}},
		{name: "mirror older than the work's start", landedBefore: []string{"landed-a.txt", "landed-b.txt"},
			landedAfter: []string{"landed-c.txt"}, stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newReadFixture(t, tc.landedBefore, tc.landedAfter, tc.stale)
			slot := filepath.Join(t.TempDir(), "slot")
			job := f.stage(t, slot)
			if tc.stale {
				require.NotEqual(t, f.start, gitAs(t, filepath.Join(job, swarm.JobRepo), "merge-base", "HEAD", "origin/main"),
					"the clone's base is the mirror's, older than the work's start")
			}
			_, ferr := installFrame(nativeRunConfig{slotDir: slot, model: "fake/fake-model", frame: f.frame("main")}, job, f.head, nil)
			require.NoError(t, ferr)
			f.assertExactlyTheWork(t, job)
		})
	}
}
