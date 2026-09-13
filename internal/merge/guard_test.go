package merge

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Demanded test 4: the four spellings, in any argument, refused BEFORE the command is
// built -- and the one lease the publication step is allowed, refused from anywhere else.

// watcher is a runner that records what it was asked to run and runs nothing. A guard
// that refuses after the subprocess started is not a guard.
type watcher struct{ seen [][]string }

func (w *watcher) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	w.seen = append(w.seen, append([]string{name}, args...))
	return "", nil
}

func TestTheMutationGuardRefusesBeforeTheCommandIsBuilt(t *testing.T) {
	lease := "--force-with-lease=refs/heads/main:" + strings.Repeat("a", 40)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"--auto", []string{"pr", "merge", "--auto"}, "does not queue on this host"},
		{"--force", []string{"push", "origin", "--force"}, "never force-pushes"},
		{"-f", []string{"push", "-f", "origin"}, "never force-pushes"},
		{"bare --force-with-lease", []string{"push", "origin", "--force-with-lease"}, "is not a compare-and-swap"},
		{"a lease with no sha", []string{"push", "origin", "--force-with-lease=refs/heads/main"}, "names a ref and no sha"},
		{"a lease with a short sha", []string{"push", "origin", "--force-with-lease=refs/heads/main:cbde1fc6ba10"}, "wants the full 40-character sha"},
		{"the allowed lease from anywhere else", []string{"push", "origin", lease}, "was not built by the publication step"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &watcher{}
			g := NewGit(t.TempDir(), 0, w)
			_, err := g.Run(tc.args...)
			if err == nil {
				t.Fatal("the guard must refuse this")
			}
			if _, ok := AsGuardError(err); !ok {
				t.Errorf("the refusal is the guard's, so it is exit 1 rather than a failure to run: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal must say why, got %v", err)
			}
			if len(w.seen) != 0 {
				t.Errorf("the guard runs BEFORE the command is built; the runner saw %v", w.seen)
			}
		})
	}
}

func TestTheOneAllowedLeaseIsBuiltByPublishAndNowhereElse(t *testing.T) {
	w := &watcher{}
	g := NewGit(t.TempDir(), 0, w)
	sha := strings.Repeat("a", 40)
	merge := strings.Repeat("b", 40)
	if _, err := g.Publish("origin", "main", sha, merge); err != nil {
		t.Fatalf("the publication step's own lease must pass the guard: %v", err)
	}
	if len(w.seen) != 1 {
		t.Fatalf("one publication is one command, got %v", w.seen)
	}
	want := "--force-with-lease=refs/heads/main:" + sha
	if !contains(w.seen[0], want) {
		t.Errorf("the push must carry %q, got %v", want, w.seen[0])
	}
	if !contains(w.seen[0], merge+":refs/heads/main") {
		t.Errorf("the object pushed is the gated object by sha, got %v", w.seen[0])
	}
	// And the lease is closed again the moment the publication is over.
	if _, err := g.Run("push", "origin", want); err == nil {
		t.Error("the lease is open for one publication and closed after it")
	}
}

// A SOURCE TEST, because "one call site" is a property of the code and not of any output:
// the lease spelling is built in exactly one place, and it is Publish.
func TestTheLeaseSpellingHasOneCallSite(t *testing.T) {
	sites := 0
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, `"--force-with-lease="+`) && !strings.Contains(line, `"--force-with-lease=" +`) {
				continue
			}
			sites++
			t.Logf("%s:%d: %s", e.Name(), i+1, strings.TrimSpace(line))
		}
	}
	if sites != 1 {
		t.Errorf("the lease is built at %d call sites, want exactly 1 (rule 21's publication step)", sites)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// repeatByte is an endless reader, used by the child below to make a runaway writer.
type repeatByte struct{}

func (repeatByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// security#30 L8c, Alex's anchor: "gh/git output buffered unbounded." Exec.Run used
// cmd.CombinedOutput() with no cap, so a hostile or runaway gh/git filled memory. The
// capture is bounded and the result says it was cut.
func TestExecRunCapsRunawayOutputAndMarksIt(t *testing.T) {
	if os.Getenv("NOVA_MERGE_EXEC_CAP_HELPER") == "1" {
		_, _ = io.Copy(os.Stdout, io.LimitReader(repeatByte{}, 1<<20))
		os.Exit(0)
	}
	t.Setenv("NOVA_MERGE_EXEC_CAP_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, _ := (Exec{}).Run(ctx, "", os.Args[0], "-test.run=TestExecRunCapsRunawayOutputAndMarksIt")
	if len(out) >= 1<<19 {
		t.Fatalf("a runaway child produced %d bytes of captured output: the cap did not hold at its named line", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("the captured result does not mark itself truncated: %q", out)
	}
}

func TestExecRunLeavesBelowCapSuccess(t *testing.T) {
	if os.Getenv("NOVA_MERGE_EXEC_SMALL_HELPER") == "1" {
		_, _ = io.WriteString(os.Stdout, "small output\n")
		os.Exit(0)
	}
	t.Setenv("NOVA_MERGE_EXEC_SMALL_HELPER", "1")
	out, err := (Exec{}).Run(context.Background(), "", os.Args[0], "-test.run=TestExecRunLeavesBelowCapSuccess")
	if err != nil || out != "small output\n" {
		t.Fatalf("below-cap command returned out=%q err=%v", out, err)
	}
}

// The child can finish in the same scheduling window in which Capture cancels
// it. The cap is still a refusal: a nil child status must never turn a capped
// prefix into an apparently complete command result.
func TestExecRunReturnsErrorWhenChildEndsAtCaptureCap(t *testing.T) {
	if os.Getenv("NOVA_MERGE_EXEC_CAP_EXACT_HELPER") == "1" {
		_, _ = io.CopyN(os.Stdout, repeatByte{}, execOutputCap)
		os.Exit(0)
	}
	t.Setenv("NOVA_MERGE_EXEC_CAP_EXACT_HELPER", "1")
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := (Exec{}).Run(ctx, "", os.Args[0], "-test.run=TestExecRunReturnsErrorWhenChildEndsAtCaptureCap")
		cancel()
		if !strings.Contains(out, "truncated") {
			t.Fatalf("iteration %d: capped child output was not marked truncated", i)
		}
		if err == nil || !strings.Contains(err.Error(), "output capture cap") {
			t.Fatalf("iteration %d: capped child returned err=%v, len=%d", i, err, len(out))
		}
	}
}
