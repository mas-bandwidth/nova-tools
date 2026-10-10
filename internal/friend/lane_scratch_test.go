package friend

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lane that has written its report, with that head on origin, leaves no job
// directory. The keep cap holds a finished job whose head is not confirmed; it
// does not hold one whose report names a head origin has, and it does not remove
// a checkout that still holds tracked work. The branch in the mirror is the
// record (docs/SPEC-FRIEND.md, what is scratch; tla/DeliveryLane.tla).
func TestAFinishedLaneLeavesNoJobDirectory(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")

	dir := t.TempDir()
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	finished, ok := PacketOf(stagedCard("done.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	stay, ok := PacketOf(stagedCard("kept.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	dirty, ok := PacketOf(stagedCard("dirty.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	for _, p := range []Packet{finished, stay, dirty} {
		_, err := stager.Stage(context.Background(), p)
		require.NoError(t, err)
	}
	checkout := filepath.Join(JobDir(dir, finished.Job), "repo")
	g.Commit(checkout, map[string]string{"work.md": "the work\n"})
	work := gitIn(t, env, checkout, "rev-parse", "HEAD")
	gitIn(t, env, checkout, "push", "-q", "origin", finished.Branch)

	dirtyCheckout := filepath.Join(JobDir(dir, dirty.Job), "repo")
	g.Commit(dirtyCheckout, map[string]string{"work.md": "pushed\n"})
	dirtyHead := gitIn(t, env, dirtyCheckout, "rev-parse", "HEAD")
	gitIn(t, env, dirtyCheckout, "push", "-q", "origin", dirty.Branch)
	require.NoError(t, os.WriteFile(filepath.Join(dirtyCheckout, "work.md"), []byte("not pushed\n"), 0o644))

	for _, row := range []struct{ job, head string }{{finished.Job, work}, {dirty.Job, dirtyHead}} {
		report := filepath.Join(dir, "outbox", row.job, "REPORT.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(report), 0o755))
		require.NoError(t, os.WriteFile(report, []byte("Verdict: LAND\nHead: "+row.head+"\n\nthe lane finished\n"), 0o644))
	}
	// every card has left her row: no brief, no lane. The keep cap would
	// have held them. The one whose head is on origin and whose checkout is
	// clean must still go. The dirty one must not.
	pruned, err := stager.Prune(context.Background(), nil, FinishedJobsKept)
	require.NoError(t, err)
	assert.Contains(t, pruned, finished.Job)
	assert.NotContains(t, pruned, dirty.Job)
	assert.NoDirExists(t, JobDir(dir, finished.Job), "a finished lane leaves no job directory")
	assert.DirExists(t, filepath.Join(JobDir(dir, stay.Job), "repo"), "a job whose head is not on origin stays inside the keep cap")
	assert.DirExists(t, dirtyCheckout, "tracked work origin does not hold is not removed")
	assert.Equal(t, "not pushed\n", mustRead(t, filepath.Join(dirtyCheckout, "work.md")))
	mirror := filepath.Join(dir, MirrorsDir, "mas-bandwidth", "nova-tools.git")
	assert.Equal(t, work, gitIn(t, env, mirror, "rev-parse", "refs/heads/"+finished.Branch), "the branch stays in the mirror")
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

// A recorded read removes the checkout it named and, when the bench root is on
// this machine, the bench copy under it. The read's own files stay.
func TestARecordedReadRemovesItsCheckoutAndBenchCopy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bench := t.TempDir()
	const id = "read.w1"
	dir := filepath.Join(root, "reads", id)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "repo"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "RESULT.md"), []byte("verdict: ok\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(bench, "buds", "ada", "reads", id, "repo"), 0o755))
	l := &loop{d: &Daemon{Dir: root, Friend: "ada", BenchRoot: bench}}
	l.releaseRead(id, time.Unix(0, 0).UTC())
	assert.NoDirExists(t, filepath.Join(dir, "repo"))
	assert.FileExists(t, filepath.Join(dir, "RESULT.md"))
	assert.NoDirExists(t, filepath.Join(bench, "buds", "ada", "reads", id))
}

// benchTransport records the Shell lines a read's remote cleanup sends and answers each with
// a fixed status; only Shell is asked by the cleanup, so Copy is a no-op.
type benchTransport struct {
	mu    sync.Mutex
	lines []string
	code  int
}

func (f *benchTransport) Shell(_ context.Context, _, line string, _, _ io.Writer) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = append(f.lines, line)
	return f.code, nil
}

func (f *benchTransport) Copy(context.Context, string, string, string, bool, io.Writer) error {
	return nil
}

func (f *benchTransport) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

func (f *benchTransport) set(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.code = code
}

// A recorded read whose RESULT.md names a Linux bench removes that bench's copy through the
// transport (the production ssh path, not the same-host BenchRoot), with the guarded line of
// the directory the prompt named; a bench that does not answer is owed its removal and the
// next step removes it once it answers, so a finished read never leaves its bench directory.
func TestARecordedReadRemovesItsRemoteBenchCopyAndRetriesUntilItSucceeds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const id = "read.w1"
	dir := filepath.Join(root, "reads", id)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "repo"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "RESULT.md"), []byte("verdict: ok\nbench: vision\n"), 0o644))

	fake := &benchTransport{}
	l := &loop{
		d:     &Daemon{Dir: root, Friend: "ada", Bench: fake, Record: func(string) {}},
		reads: newReadSet(),
		ctx:   context.Background(),
	}
	now := time.Unix(0, 0).UTC()
	l.releaseRead(id, now)
	require.Equal(t, []string{"rm -rf -- ~/nova-bench/buds/ada/reads/read.w1"}, fake.got(),
		"the bench copy on the named host is removed through the transport, the exact directory the prompt named")
	assert.NotContains(t, l.reads.owedBench, id, "a bench that answers is not owed")
	assert.NoDirExists(t, filepath.Join(dir, "repo"))

	// a bench that does not answer is owed, and removed once it answers
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "repo"), 0o755)) // the checkout is back for the second read
	fake.set(255)
	l.releaseRead(id, now)
	_, owed := l.reads.owedBench[id]
	assert.True(t, owed, "a bench that does not answer is owed its removal")
	assert.Equal(t, 2, len(fake.got()))

	fake.set(0)
	before := len(fake.got())
	l.retryBenchCleanup(now) // not due yet
	assert.Equal(t, before, len(fake.got()), "an owed cleanup is not retried before its time")

	l.retryBenchCleanup(now.Add(ReadBenchRetry))
	_, still := l.reads.owedBench[id]
	assert.False(t, still, "the owed cleanup is dropped once it succeeds")
	assert.Equal(t, before+1, len(fake.got()))
}
