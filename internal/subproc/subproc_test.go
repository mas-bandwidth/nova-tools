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
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
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
		if got := c.kind.Budget(); got != c.want {
			t.Errorf("%s budget is %s, want %s", c.kind, got, c.want)
		}
	}
	if subproc.WaitDelay != 5*time.Second {
		t.Errorf("WaitDelay is %s, want 5s", subproc.WaitDelay)
	}
}

func TestKindOfNamesTheProgram(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]subproc.Kind{
		"git": subproc.Git, "/usr/bin/git": subproc.Git,
		"gh": subproc.GH, "ssh": subproc.SSH, "scp": subproc.SSH, "rsync": subproc.SSH,
		"go": subproc.Go, "sops": subproc.Tool, "tailscale": subproc.Tool, "": subproc.Tool,
	} {
		if got := subproc.KindOf(name); got != want {
			t.Errorf("KindOf(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestBoundForIsTheBudgetUnlessTheCallerIsSooner(t *testing.T) {
	t.Parallel()

	for _, k := range []subproc.Kind{subproc.Git, subproc.GH, subproc.SSH, subproc.Go, subproc.Tool} {
		ctx, cancel := subproc.BoundFor(context.Background(), k.Budget())
		d, ok := ctx.Deadline()
		cancel()
		if !ok {
			t.Fatalf("%s: a background caller got no deadline", k)
		}
		if left := time.Until(d); left > k.Budget() || left < k.Budget()-30*time.Second {
			t.Errorf("%s: deadline is %s away, want about %s", k, left, k.Budget())
		}

		// A caller whose own deadline is sooner than the budget keeps it.
		sooner := time.Now().Add(k.Budget() / 2)
		parent, stop := context.WithDeadline(context.Background(), sooner)
		ctx, cancel = subproc.BoundFor(parent, k.Budget())
		d, _ = ctx.Deadline()
		cancel()
		stop()
		if !d.Equal(sooner) {
			t.Errorf("%s: a caller deadline of %s was replaced by %s", k, sooner, d)
		}

		// A caller whose deadline is later is cut to the budget.
		later := time.Now().Add(2 * k.Budget())
		parent, stop = context.WithDeadline(context.Background(), later)
		ctx, cancel = subproc.BoundFor(parent, k.Budget())
		d, _ = ctx.Deadline()
		cancel()
		stop()
		if !d.Before(later) {
			t.Errorf("%s: a caller deadline of %s past the budget was kept (%s)", k, later, d)
		}
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
			if cmd.WaitDelay != subproc.WaitDelay {
				t.Fatalf("Command left WaitDelay at %s, want %s", cmd.WaitDelay, subproc.WaitDelay)
			}
			cmd.WaitDelay = 100 * time.Millisecond
			out := newStanding()
			cmd.Stdout = out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			<-out.ready
			cancel()
			if err := cmd.Wait(); err == nil {
				t.Fatal("a child killed while it slept returned no error")
			}
		})
	}
}

func TestBoundedWrapNamesADeadlineKillAndLeavesOtherErrorsAlone(t *testing.T) {
	t.Parallel()

	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer stop()
	boom := errors.New("signal: killed")
	var te *subproc.TimeoutError
	if err := (subproc.Bounded{Ctx: expired, Budget: time.Minute}).Wrap("git status", boom); !errors.As(err, &te) || !errors.Is(err, boom) {
		t.Fatalf("a deadline kill was reported as %v", err)
	}
	if !strings.Contains(te.Error(), "git status did not finish within 1m0s and was killed") {
		t.Fatalf("the message is %q", te.Error())
	}
	if err := (subproc.Bounded{Ctx: context.Background(), Budget: time.Minute}).Wrap("git status", boom); err != boom {
		t.Fatalf("an error with no deadline was changed to %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (subproc.Bounded{Ctx: cancelled, Budget: time.Minute}).Wrap("git status", boom); err != boom {
		t.Fatalf("a cancellation was reported as a deadline: %v", err)
	}
	if err := (subproc.Bounded{Ctx: expired, Budget: time.Minute}).Wrap("git status", nil); err != nil {
		t.Fatalf("nil became %v", err)
	}
}

// TestLongHasNoDeadlineAndIsCancellable: a long-lived child is not given a budget, and
// the caller's cancel ends it.
func TestLongHasNoDeadlineAndIsCancellable(t *testing.T) {
	t.Parallel()

	script := slowChild(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := subproc.Long(ctx, script)
	if cmd.WaitDelay != subproc.WaitDelay {
		t.Fatalf("Long left WaitDelay at %s", cmd.WaitDelay)
	}
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("the caller's context gained a deadline")
	}
	cmd.WaitDelay = 100 * time.Millisecond
	out := newStanding()
	cmd.Stdout = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	<-out.ready
	if cmd.ProcessState != nil {
		t.Fatal("the child ended before it was cancelled")
	}
	cancel()
	err := cmd.Wait()
	var ee *exec.ExitError
	if !errors.As(err, &ee) && !errors.Is(err, exec.ErrWaitDelay) && !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancelled child ended with %v", err)
	}
}

// Context keeps the caller's own context and adds WaitDelay.
func TestContextKeepsTheCallersContextAndSetsWaitDelay(t *testing.T) {
	t.Parallel()

	cmd := subproc.Context(context.Background(), "git")
	if cmd.WaitDelay != subproc.WaitDelay {
		t.Fatalf("WaitDelay is %s", cmd.WaitDelay)
	}
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
		if got := subproc.GitBudgetFor(c.args); got != c.want {
			t.Errorf("GitBudgetFor(%v) = %s, want %s", c.args, got, c.want)
		}
	}
	if got := subproc.BudgetOf("/usr/bin/git", []string{"push"}); got != subproc.GitLongBudget {
		t.Errorf("BudgetOf(git push) = %s", got)
	}
	if got := subproc.BudgetOf("sops", []string{"-d", "x"}); got != subproc.ToolBudget {
		t.Errorf("BudgetOf(sops) = %s", got)
	}
}

// Prepare names the budget it gave, and names none when the caller's own deadline was the
// sooner one.
func TestPrepareNamesTheBudgetOnlyWhenItApplied(t *testing.T) {
	t.Parallel()

	b := subproc.Prepare(context.Background(), time.Minute, "git", "status")
	defer b.Cancel()
	if b.Budget != time.Minute || b.Cmd.WaitDelay != subproc.WaitDelay {
		t.Fatalf("budget %s, WaitDelay %s", b.Budget, b.Cmd.WaitDelay)
	}
	parent, stop := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer stop()
	sooner := subproc.Prepare(parent, 2*time.Hour, "git", "status")
	defer sooner.Cancel()
	if sooner.Budget != 0 {
		t.Fatalf("the caller's sooner deadline still named a budget: %s", sooner.Budget)
	}
}
