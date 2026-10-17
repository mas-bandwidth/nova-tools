package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// deadlineGit runs one git command in dir with a fixed identity and no global config.
func deadlineGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v:\n%s", args, out)
	return strings.TrimSpace(string(out))
}

// aDeadlineJob builds <job>/repo as a clone of a bare origin holding one commit on main, and
// returns the job directory. Nothing in it is a card's work yet.
func aDeadlineJob(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	require.NoError(t, os.MkdirAll(seed, 0o755))
	deadlineGit(t, root, "init", "-q", "--bare", "-b", "main", origin)
	deadlineGit(t, seed, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(seed, "a"), []byte("a\n"), 0o644))
	deadlineGit(t, seed, "add", "a")
	deadlineGit(t, seed, "commit", "-q", "-m", "base")
	deadlineGit(t, seed, "push", "-q", origin, "main")
	job := filepath.Join(root, "job")
	require.NoError(t, os.MkdirAll(job, 0o755))
	deadlineGit(t, job, "clone", "-q", origin, "repo")
	return job
}

func aDeadlineCommit(t *testing.T, job, name string) {
	t.Helper()
	repo := filepath.Join(job, "repo")
	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o644))
	deadlineGit(t, repo, "add", name)
	deadlineGit(t, repo, "commit", "-q", "-m", "work "+name)
}

// A card that pushed its commit and was killed at the deadline is REPORTED, with pushed=yes.
// The upstream of the pushed branch equals HEAD, so a count against the upstream (what
// repoCommits does) would read this card as having done nothing: that is the case the fix is
// for, and this test is red if the base is ever the upstream again.
func TestDeadlineReportNamesAPushedCommit(t *testing.T) {
	t.Parallel()
	job := aDeadlineJob(t)
	repo := filepath.Join(job, "repo")
	deadlineGit(t, repo, "checkout", "-q", "-b", "card/work")
	aDeadlineCommit(t, job, "one")
	deadlineGit(t, repo, "push", "-q", "-u", "origin", "card/work")

	_, _, upstreamCounts := repoCommits(job)
	require.False(t, upstreamCounts, "precondition: counting against the upstream sees nothing after a push")

	path, wrote, err := WriteDeadlineResult(job, "t1")
	require.NoError(t, err)
	require.True(t, wrote)
	require.Equal(t, filepath.Join(job, "RESULT.md"), path)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	head := deadlineGit(t, repo, "rev-parse", "HEAD")
	lines := strings.Split(string(raw), "\n")
	require.Equal(t, "RESULT: DEADLINE branch=card/work head="+head+" commits=1 pushed=yes", lines[0])
	require.Contains(t, string(raw), DeadlineWrittenBy)
	require.Contains(t, string(raw), "- work one")
	require.True(t, DeadlineReport(path), "the file this writes is recognised as the machinery's")
}

func TestDeadlineReportSaysWhenTheCommitWasNotPushed(t *testing.T) {
	t.Parallel()
	job := aDeadlineJob(t)
	deadlineGit(t, filepath.Join(job, "repo"), "checkout", "-q", "-b", "card/work")
	aDeadlineCommit(t, job, "one")
	path, wrote, err := WriteDeadlineResult(job, "t2")
	require.NoError(t, err)
	require.True(t, wrote)
	raw, _ := os.ReadFile(path)
	require.Contains(t, strings.SplitN(string(raw), "\n", 2)[0], "commits=1 pushed=no")
}

// No commit, no report: a card that did nothing keeps the plain no-result.
func TestDeadlineReportWritesNothingWithoutCommits(t *testing.T) {
	t.Parallel()
	job := aDeadlineJob(t)
	deadlineGit(t, filepath.Join(job, "repo"), "checkout", "-q", "-b", "card/work")
	_, wrote, err := WriteDeadlineResult(job, "t3")
	require.NoError(t, err)
	require.False(t, wrote)
	_, err = os.Stat(filepath.Join(job, "RESULT.md"))
	require.True(t, os.IsNotExist(err))

	_, wrote, err = WriteDeadlineResult(t.TempDir(), "t3b")
	require.NoError(t, err)
	require.False(t, wrote, "a job with no ./repo has no work to report")
}

// A card that published owns its report, however thin: it is never overwritten.
func TestDeadlineReportNeverOverwritesACardsResult(t *testing.T) {
	t.Parallel()
	job := aDeadlineJob(t)
	deadlineGit(t, filepath.Join(job, "repo"), "checkout", "-q", "-b", "card/work")
	aDeadlineCommit(t, job, "one")
	own := []byte("RESULT: the card's own\n")
	require.NoError(t, os.WriteFile(filepath.Join(job, "RESULT.md"), own, 0o644))
	_, wrote, err := WriteDeadlineResult(job, "t4")
	require.NoError(t, err)
	require.False(t, wrote)
	got, _ := os.ReadFile(filepath.Join(job, "RESULT.md"))
	require.Equal(t, own, got)
}

// A card cannot promote its own verdict by typing the opening: both marks are required.
func TestDeadlineReportNeedsBothMarks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	typed := filepath.Join(dir, "typed.md")
	require.NoError(t, os.WriteFile(typed, []byte(DeadlineResultPrefix+"branch=x head=y commits=1 pushed=no\nmy own findings\n"), 0o644))
	require.False(t, DeadlineReport(typed))
	other := filepath.Join(dir, "other.md")
	require.NoError(t, os.WriteFile(other, []byte("RESULT: OK\n"+DeadlineWrittenBy+"\n"), 0o644))
	require.False(t, DeadlineReport(other))
	require.False(t, DeadlineReport(filepath.Join(dir, "absent.md")))
}

// The subject list is bounded by DeadlineReportSubjects; the count stays exact.
func TestDeadlineReportBoundsItsSubjects(t *testing.T) {
	t.Parallel()
	job := aDeadlineJob(t)
	deadlineGit(t, filepath.Join(job, "repo"), "checkout", "-q", "-b", "card/work")
	total := DeadlineReportSubjects + 3
	for i := 0; i < total; i++ {
		aDeadlineCommit(t, job, "f"+strings.Repeat("x", i+1))
	}
	path, wrote, err := WriteDeadlineResult(job, "t5")
	require.NoError(t, err)
	require.True(t, wrote)
	raw, _ := os.ReadFile(path)
	text := string(raw)
	require.Equal(t, DeadlineReportSubjects, strings.Count(text, "\n- work "))
	require.Contains(t, text, "commits=23 ")
	require.Contains(t, text, "- ...and 3 older commit(s)")
}
