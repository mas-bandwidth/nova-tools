package subproc_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowChild writes a script that starts a background sleeper, which inherits the
// script's standard output, says it stands, and then sleeps itself: killing the script
// leaves the sleeper holding the pipe, which is what hangs an Output call that has no
// WaitDelay.
func slowChild(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake slow child is a shell script")
	}
	path := filepath.Join(t.TempDir(), "slow-child")
	body := "#!/bin/sh\nsleep 30 &\necho standing\nsleep 30\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path
}

// standing is a writer that closes ready when the child has said it stands, so a test
// acts on the event and never on the clock.
type standing struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func newStanding() *standing { return &standing{ready: make(chan struct{})} }

func (s *standing) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.buf.Write(p)
	if strings.Contains(s.buf.String(), "standing") {
		s.once.Do(func() { close(s.ready) })
	}
	return n, err
}

func TestBudgetsAreTheNamedDefaults(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		kind subproc.Kind
		want time.Duration
	}{
		{subproc.Git, 60 * time.Second},
		{subproc.GH, 120 * time.Second},
		{subproc.SSH, 300 * time.Second},
		{subproc.Go, 300 * time.Second},
		{subproc.Tool, 60 * time.Second},
	} {
		assert.Equal(t, c.want, c.kind.Budget(), "%s budget", c.kind)
	}
	assert.Equal(t, 5*time.Second, subproc.WaitDelay, "WaitDelay")
}

func TestKindOfNamesTheProgram(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]subproc.Kind{
		"git": subproc.Git, "/usr/bin/git": subproc.Git,
		"gh": subproc.GH, "ssh": subproc.SSH, "scp": subproc.SSH, "rsync": subproc.SSH,
		"go": subproc.Go, "sops": subproc.Tool, "tailscale": subproc.Tool, "": subproc.Tool,
	} {
		assert.Equal(t, want, subproc.KindOf(name), "KindOf(%q)", name)
	}
}

func TestBoundForIsTheBudgetUnlessTheCallerIsSooner(t *testing.T) {
	t.Parallel()

	for _, k := range []subproc.Kind{subproc.Git, subproc.GH, subproc.SSH, subproc.Go, subproc.Tool} {
		ctx, cancel := subproc.BoundFor(context.Background(), k.Budget())
		d, ok := ctx.Deadline()
		cancel()
		require.True(t, ok, "%s: a background caller got no deadline", k)
		left := time.Until(d)
		assert.LessOrEqual(t, left, k.Budget(), "%s: deadline is %s away, want about %s", k, left, k.Budget())
		assert.GreaterOrEqual(t, left, k.Budget()-30*time.Second, "%s: deadline is %s away, want about %s", k, left, k.Budget())

		// A caller whose own deadline is sooner than the budget keeps it.
		sooner := time.Now().Add(k.Budget() / 2)
		parent, stop := context.WithDeadline(context.Background(), sooner)
		ctx, cancel = subproc.BoundFor(parent, k.Budget())
		d, _ = ctx.Deadline()
		cancel()
		stop()
		assert.True(t, d.Equal(sooner), "%s: a caller deadline of %s was replaced by %s", k, sooner, d)

		// A caller whose deadline is later is cut to the budget.
		later := time.Now().Add(2 * k.Budget())
		parent, stop = context.WithDeadline(context.Background(), later)
		ctx, cancel = subproc.BoundFor(parent, k.Budget())
		d, _ = ctx.Deadline()
		cancel()
		stop()
		assert.True(t, d.Before(later), "%s: a caller deadline of %s past the budget was kept (%s)", k, later, d)
	}
}

// TestEveryKindEndsASlowChildAndDoesNotHangOnItsPipe: the child is up (its sleeper holds
// the pipe), the bound is cancelled, and Wait returns. Cancel is the same mechanism a
// deadline uses (the context ends and exec kills the child), driven here by an event so
// the test waits on no clock; the real deadline per kind is the functional test. WaitDelay
// is shortened for the run (the constant itself is asserted above).
func TestEveryKindEndsASlowChildAndDoesNotHangOnItsPipe(t *testing.T) {
	t.Parallel()

	script := slowChild(t)
	for _, k := range []subproc.Kind{subproc.Git, subproc.GH, subproc.SSH, subproc.Go, subproc.Tool} {
		t.Run(k.String(), func(t *testing.T) {
			t.Parallel()

			cmd, cancel := subproc.Command(context.Background(), k, script)
			defer cancel()
			require.Equal(t, subproc.WaitDelay, cmd.WaitDelay, "Command left WaitDelay")
			cmd.WaitDelay = 100 * time.Millisecond
			out := newStanding()
			cmd.Stdout = out
			require.NoError(t, cmd.Start())
			<-out.ready
			cancel()
			require.Error(t, cmd.Wait(), "a child killed while it slept returned no error")
		})
	}
}

func TestBoundedWrapNamesADeadlineKillAndLeavesOtherErrorsAlone(t *testing.T) {
	t.Parallel()

	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer stop()
	boom := errors.New("signal: killed")
	var te *subproc.TimeoutError
	err := (subproc.Bounded{Ctx: expired, Budget: time.Minute}).Wrap("git status", boom)
	require.ErrorAs(t, err, &te, "a deadline kill was reported as %v", err)
	require.ErrorIs(t, err, boom, "a deadline kill was reported as %v", err)
	require.Contains(t, te.Error(), "git status did not finish within 1m0s and was killed", "the message is %q", te.Error())
	err = (subproc.Bounded{Ctx: context.Background(), Budget: time.Minute}).Wrap("git status", boom)
	require.Same(t, boom, err, "an error with no deadline was changed to %v", err)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err = (subproc.Bounded{Ctx: cancelled, Budget: time.Minute}).Wrap("git status", boom)
	require.Same(t, boom, err, "a cancellation was reported as a deadline: %v", err)
	require.NoError(t, (subproc.Bounded{Ctx: expired, Budget: time.Minute}).Wrap("git status", nil), "nil became an error")
}

// TestLongHasNoDeadlineAndIsCancellable: a long-lived child is not given a budget, and
// the caller's cancel ends it.
func TestLongHasNoDeadlineAndIsCancellable(t *testing.T) {
	t.Parallel()

	script := slowChild(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := subproc.Long(ctx, script)
	require.Equal(t, subproc.WaitDelay, cmd.WaitDelay, "Long left WaitDelay")
	_, ok := ctx.Deadline()
	require.False(t, ok, "the caller's context gained a deadline")
	cmd.WaitDelay = 100 * time.Millisecond
	out := newStanding()
	cmd.Stdout = out
	require.NoError(t, cmd.Start())
	<-out.ready
	require.Nil(t, cmd.ProcessState, "the child ended before it was cancelled")
	cancel()
	err := cmd.Wait()
	var ee *exec.ExitError
	require.True(t, errors.As(err, &ee) || errors.Is(err, exec.ErrWaitDelay) || errors.Is(err, context.Canceled), "the cancelled child ended with %v", err)
}

// Context keeps the caller's own context and adds WaitDelay.
func TestContextKeepsTheCallersContextAndSetsWaitDelay(t *testing.T) {
	t.Parallel()

	cmd := subproc.Context(context.Background(), "git")
	require.Equal(t, subproc.WaitDelay, cmd.WaitDelay, "Context left WaitDelay")
}

// Any git that goes to the network, or moves a whole repository, gets the long budget;
// the rest get the git budget; leading options are skipped.
func TestGitBudgetForTheCommandLine(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		args []string
		want time.Duration
	}{
		{[]string{"clone", "--local", "a", "b"}, subproc.GitLongBudget},
		{[]string{"-C", "/repo", "fetch", "origin", "x"}, subproc.GitLongBudget},
		{[]string{"-c", "core.sshCommand=ssh", "pull", "--ff-only"}, subproc.GitLongBudget},
		{[]string{"--no-pager", "push", "-u", "origin", "b"}, subproc.GitLongBudget},
		{[]string{"ls-remote", "origin"}, subproc.GitLongBudget},
		{[]string{"status", "--porcelain"}, subproc.GitBudget},
		{[]string{"-C", "/fetch", "rev-parse", "HEAD"}, subproc.GitBudget},
		{[]string{"ls-files", "-z"}, subproc.GitBudget},
		{nil, subproc.GitBudget},
	} {
		assert.Equal(t, c.want, subproc.GitBudgetFor(c.args), "GitBudgetFor(%v)", c.args)
	}
	assert.Equal(t, subproc.GitLongBudget, subproc.BudgetOf("/usr/bin/git", []string{"push"}), "BudgetOf(git push)")
	assert.Equal(t, subproc.ToolBudget, subproc.BudgetOf("sops", []string{"-d", "x"}), "BudgetOf(sops)")
}

// Prepare names the budget it gave, and names none when the caller's own deadline was the
// sooner one.
func TestPrepareNamesTheBudgetOnlyWhenItApplied(t *testing.T) {
	t.Parallel()

	b := subproc.Prepare(context.Background(), time.Minute, "git", "status")
	defer b.Cancel()
	require.Equal(t, time.Minute, b.Budget, "budget")
	require.Equal(t, subproc.WaitDelay, b.Cmd.WaitDelay, "WaitDelay")
	parent, stop := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer stop()
	sooner := subproc.Prepare(parent, 2*time.Hour, "git", "status")
	defer sooner.Cancel()
	require.Equal(t, time.Duration(0), sooner.Budget, "the caller's sooner deadline still named a budget")
}
