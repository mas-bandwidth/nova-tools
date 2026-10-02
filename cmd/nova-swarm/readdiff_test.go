package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// readStartRE is the line of a read's JOB.md that shows the work's change.
var readStartRE = regexp.MustCompile(`(?m)^    git diff --stat ([0-9a-f]{40})\.\.HEAD$`)

// A read staged after its base branch moved (cards landed on it while the work was read) is
// told the commit the work started from and the command that shows exactly the work's
// change, in every profile; a diff against the moved base shows the landed files too, which
// a reader on the 1000-card load test (2026-10-01) took for deletions and sent a correct
// work card back for (docs/SPEC-CARD-CONTRACT.md, JOB.md).
func TestAReadIsToldTheWorksChangeWhenTheBaseMoved(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seed, origin := filepath.Join(root, "seed"), filepath.Join(root, "origin.git")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	write(t, filepath.Join(seed, "f"), "base\n")
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)
	start := gitAs(t, origin, "rev-parse", "main")

	// the work: one file, on its sprint branch
	w := filepath.Join(root, "w")
	runGit(t, "", "clone", "-q", "--", origin, w)
	require.NoError(t, os.MkdirAll(filepath.Join(w, "quacks"), 0o755))
	write(t, filepath.Join(w, "quacks", "w.txt"), "quack\n")
	gitAs(t, w, "add", "quacks/w.txt")
	gitAs(t, w, "commit", "-q", "-m", "quack: w")
	head := gitAs(t, w, "rev-parse", "HEAD")
	runGit(t, w, "push", "-q", "origin", "HEAD:refs/heads/sprint/w")

	// the base moves: two other cards land on main after the work began
	l := filepath.Join(root, "l")
	runGit(t, "", "clone", "-q", "--", origin, l)
	for _, name := range []string{"landed-a.txt", "landed-b.txt"} {
		write(t, filepath.Join(l, name), name+"\n")
		gitAs(t, l, "add", name)
		gitAs(t, l, "commit", "-q", "-m", name)
	}
	runGit(t, l, "push", "-q", "origin", "main")

	for _, model := range []string{"anthropic/claude-x", "openai/gpt-x", "fake/model-x"} {
		t.Run(cardcontract.FamilyOf(model), func(t *testing.T) {
			t.Parallel()
			slot := filepath.Join(root, "slot-"+cardcontract.FamilyOf(model))
			job := filepath.Join(slot, "jobs", "w.r1")
			require.NoError(t, os.MkdirAll(job, 0o755))
			fr := &cardcontract.Frame{Kind: "read", Card: "w.r1", Attempt: 1, Model: model, Repo: origin,
				BaseRef: "main", ReviewBase: "main", StageSha: head, Branch: "sprint/w"}
			st, err := swarm.StageCard(swarm.StageOptions{Identity: testStageIdentity, TargetDir: filepath.Join(job, swarm.JobRepo), JobDir: job,
				BenchHome: filepath.Join(root, "no-bench"), Base: &swarm.CardBase{Repo: origin, Sha: head, Ref: "main", Named: origin}, Branch: fr.Branch})
			require.NoError(t, err)
			require.Equal(t, head, st.BaseSha)
			checkout := filepath.Join(job, swarm.JobRepo)
			require.ElementsMatch(t, []string{"landed-a.txt", "landed-b.txt", "quacks/w.txt"},
				strings.Fields(runGit(t, checkout, "diff", "--name-only", "origin/main")), "the base moved: a diff against it is more than the work")

			require.NoError(t, installFrame(nativeRunConfig{slotDir: slot, model: model, frame: fr}, job, st.BaseSha))
			jobText, err := os.ReadFile(filepath.Join(job, cardcontract.JobName))
			require.NoError(t, err)
			m := readStartRE.FindStringSubmatch(string(jobText))
			require.NotNil(t, m, "JOB.md names the command that shows the work's change:\n%s", jobText)
			assert.Equal(t, start, m[1], "the start is the commit the work began from")
			assert.Contains(t, string(jobText), "    git diff "+start+"..HEAD\n")
			assert.Contains(t, string(jobText), "main may have moved since the work began")
			assert.Contains(t, string(jobText), "a diff against the tip of main or origin/main shows every change landed since as a deletion")
			assert.Contains(t, string(jobText), "Those deletions are never the work's and never a finding: judge the work by the diff above alone.")
			assert.Equal(t, []string{"quacks/w.txt"}, strings.Fields(runGit(t, checkout, "diff", "--name-only", m[1]+"..HEAD")),
				"the command JOB.md names shows exactly the work's change")
		})
	}
}

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
	st, err := swarm.StageCard(swarm.StageOptions{Identity: testStageIdentity, TargetDir: filepath.Join(job, swarm.JobRepo), JobDir: job,
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
			require.NoError(t, installFrame(nativeRunConfig{slotDir: slot, model: "fake/fake-model", frame: f.frame("main")}, job, f.head))
			f.assertExactlyTheWork(t, job)
		})
	}
}

// A base that never moves needs no fetch: a tag, or a full sha, reviews the work against
// itself with origin unreachable, and the start is still the commit the work began from.
func TestAReadAgainstATagOrAShaNeedsNoFetch(t *testing.T) {
	t.Parallel()
	f := newReadFixture(t, []string{"landed-a.txt"}, []string{"landed-b.txt"}, false)
	for name, base := range map[string]string{"tag": "v1", "sha": f.start} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			slot := filepath.Join(t.TempDir(), "slot")
			job := f.stage(t, slot)
			runGit(t, filepath.Join(job, swarm.JobRepo), "remote", "set-url", "origin", filepath.Join(t.TempDir(), "no-such-origin.git"))
			require.NoError(t, installFrame(nativeRunConfig{slotDir: slot, model: "fake/fake-model", frame: f.frame(base)}, job, f.head))
			f.assertExactlyTheWork(t, job)
		})
	}
}

// A read whose base branch cannot be fetched from origin is refused at staging, visibly, and
// writes no JOB.md: the checkout's own branch is the bench mirror's and may be older than
// the work's start, and a start taken from it would be the wrong diff named as exactly the
// work's. The witness stages the stale-mirror read, then points that checkout's origin at a
// directory that does not exist before the frame is installed: native prints a STAGE FAIL
// line naming the base and the fetch error, exits 2, and the member reads the launch as a
// staging refusal (no child ran; the sprint deals the read again). native runs as the member
// runs it: the built binary, the frame in a file, its output in the launch's log.
func TestAReadWhoseBaseCannotBeFetchedIsRefusedAtStaging(t *testing.T) {
	t.Parallel()
	require.NoError(t, buildShared())
	f := newReadFixture(t, []string{"landed-a.txt", "landed-b.txt"}, []string{"landed-c.txt"}, true)
	root, slot := aSlot(t)
	job := f.stage(t, slot)
	runGit(t, filepath.Join(job, swarm.JobRepo), "remote", "set-url", "origin", filepath.Join(t.TempDir(), "no-such-origin.git"))

	framePath, cardPath, logPath := filepath.Join(root, "w.r1"+cardcontract.FrameName), filepath.Join(root, "w.r1.card.md"), filepath.Join(root, "w.r1.native.log")
	require.NoError(t, cardcontract.WriteFrame(framePath, *f.frame("main")))
	write(t, cardPath, "read the work\n")
	logf, err := os.Create(logPath)
	require.NoError(t, err)
	cmd := exec.Command(builtTool, "native", "--harness", builtHarness, "--model", "fake/fake-model", "--card", cardPath, "--frame", framePath,
		"--slot", slot, "--root", root, "--deadline", "30s", "--tokens", "unmetered", "--label", "w.r1", "--no-wall")
	cmd.Stdout, cmd.Stderr = logf, logf
	runErr := cmd.Run()
	require.NoError(t, logf.Close())
	log, err := os.ReadFile(logPath)
	require.NoError(t, err)
	var exit *exec.ExitError
	require.ErrorAs(t, runErr, &exit, "native refuses:\n%s", log)
	assert.Equal(t, 2, exit.ExitCode(), "%s", log)

	m := nativeStageFail.FindSubmatch(log)
	require.NotNil(t, m, "a STAGE FAIL line:\n%s", log)
	reason := string(m[1])
	assert.Contains(t, reason, "staging refused: the read's start")
	assert.Contains(t, reason, "the base branch main could not be fetched from origin")
	assert.Contains(t, reason, "no-such-origin.git", "the fetch error is in the reason")
	assert.Contains(t, string(log), "NATIVE REFUSED: staging refused: the read's start")
	assert.NoFileExists(t, filepath.Join(job, cardcontract.JobName), "no JOB.md names a start the read cannot back")

	c := &nativeChild{card: "w.r1", logPath: logPath, results: filepath.Join(slot, "results"), job: job, done: make(chan struct{})}
	res := c.Result()
	assert.Equal(t, member.EndStaging, res.End, "the member reads the launch as refused at staging")
	assert.Contains(t, res.Staging, "the base branch main could not be fetched from origin")
}

// testStageIdentity is the commit identity these tests stage under: StageCard carries none
// of its own.
var testStageIdentity = swarm.StagingIdentity{Owner: "test-owner", Name: "Pool Worker", Email: "pool@example.com"}
