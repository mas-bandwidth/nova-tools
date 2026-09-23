package merge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ciFakeRunner answers a GREEN check-run to anything asked of it. If GH.Checks
// ever reached GitHub, it would read this green run and call the commit green;
// the test asserts this runner is never invoked. Exec is embedded so the fake
// satisfies Runner whatever else that interface grows, while Run is overridden.
type ciFakeRunner struct {
	Exec
	calls int
}

func (r *ciFakeRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	r.calls++
	return "ci-ok\tsuccess\tdeadbeef\n", nil
}

// ciFakeSource is an injectable CISource over a map, for the test.
type ciFakeSource struct {
	values map[string]string
	calls  int
}

func (s *ciFakeSource) Read(repo, sha string) (string, bool, error) {
	s.calls++
	v, ok := s.values[CIKey(repo, sha)]
	return v, ok, nil
}

func TestLanderReadsCIFromRedisNeverCheckRuns(t *testing.T) {
	const repo, sha = "owner/repo", "deadbeef"

	t.Run("production address matches land-lane defaults", func(t *testing.T) {
		t.Setenv("REDIS_ADDR", "")
		t.Setenv("NOVA_REDIS_HOST", "")
		t.Setenv("NOVA_REDIS_PORT", "")
		if got := redisAddrFromEnv(); got != "100.115.99.19:6380" {
			t.Fatalf("redisAddrFromEnv() = %q, want land-lane default 100.115.99.19:6380", got)
		}
	})

	// A green check-run sits behind the runner, and NOTHING in the injectable
	// source. The forge would say green; the source says missing. The lander
	// must report ci: MISSING and never read the check-run.
	t.Run("green check-run without redis key stays MISSING", func(t *testing.T) {
		runner := &ciFakeRunner{}
		src := &ciFakeSource{values: map[string]string{}}
		h := NewGH(repo, time.Second, runner, WithCISource(src))

		_, err := h.Checks(sha)
		if !errors.Is(err, ErrCIMissing) {
			t.Fatalf("Checks with no redis key = %v, want ErrCIMissing", err)
		}
		if runner.calls != 0 {
			t.Fatalf("Checks read GitHub check-runs %d time(s); it must never read them", runner.calls)
		}
		if src.calls != 1 {
			t.Fatalf("Checks read the CI source %d time(s), want 1", src.calls)
		}
	})

	// The same commit, with an OK key, is green -- buckets and all -- still
	// without touching a check-run.
	t.Run("injected OK becomes green", func(t *testing.T) {
		runner := &ciFakeRunner{}
		src := &ciFakeSource{values: map[string]string{CIKey(repo, sha): "OK"}}
		h := NewGH(repo, time.Second, runner, WithCISource(src))

		c, err := h.Checks(sha)
		if err != nil {
			t.Fatalf("Checks with injected OK = %v, want nil", err)
		}
		if c.Verdict() != "GREEN" {
			t.Fatalf("Verdict() = %q, want GREEN", c.Verdict())
		}
		if c.Green != 1 || c.Pending != 0 || c.Red != 0 {
			t.Fatalf("buckets = g%d/p%d/r%d, want g1/p0/r0", c.Green, c.Pending, c.Red)
		}
		if runner.calls != 0 {
			t.Fatalf("Checks read GitHub check-runs %d time(s); it must never read them", runner.calls)
		}
	})

	// Empty and every value other than the protocol's exact OK token are
	// neither green nor a check-run read.
	for _, value := range []string{"", "ok", "success", "green", "fail", "pending", "NOTOK"} {
		value := value
		t.Run("non-OK "+strings.TrimSpace(value)+" is MISSING", func(t *testing.T) {
			runner := &ciFakeRunner{}
			src := &ciFakeSource{values: map[string]string{CIKey(repo, sha): value}}
			h := NewGH(repo, time.Second, runner, WithCISource(src))
			if _, err := h.Checks(sha); !errors.Is(err, ErrCIMissing) {
				t.Fatalf("Checks(%q) = %v, want ErrCIMissing", value, err)
			}
			if runner.calls != 0 {
				t.Fatalf("Checks read GitHub check-runs %d time(s)", runner.calls)
			}
		})
	}

	// The key is exactly ci:<owner/repo>:<sha>.
	if got := CIKey("o/r", "abc123"); got != "ci:o/r:abc123" {
		t.Fatalf("CIKey = %q, want ci:o/r:abc123", got)
	}
}
