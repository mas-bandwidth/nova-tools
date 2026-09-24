package merge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
)

// ciFakeRunner answers GitHub's check-runs for any commit with out. Exec is
// embedded so the fake satisfies Runner whatever else that interface grows,
// while Run is overridden.
type ciFakeRunner struct {
	Exec
	out   string
	err   error
	calls int
}

func (r *ciFakeRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	r.calls++
	return r.out, r.err
}

const (
	ghGreen = "lint\tsuccess\tdeadbeef\ntest\tsuccess\tdeadbeef\nci-ok\tsuccess\tdeadbeef\n"
	ghRed   = "lint\tsuccess\tdeadbeef\ntest\tfailure\tdeadbeef\n"
)

// ciFakeSource is an injectable CISource over a map of verdict words, for the
// test; err, when set, is every read's answer (an unreadable store).
type ciFakeSource struct {
	values map[string]string
	err    error
	calls  int
}

func (s *ciFakeSource) Read(repo, sha string) (string, bool, error) {
	s.calls++
	if s.err != nil {
		return "", false, s.err
	}
	v, ok := s.values[repo+":"+sha]
	return v, ok && strings.TrimSpace(v) != "", nil
}

// A missing record is not a verdict (no-evidence-is-not-negative-evidence):
// the record answers when it carries a verdict, the forge answers when it does
// not, and ci: MISSING is only the answer when both say nothing.
func TestChecksReadsTheRecordThenFallsBackToGitHub(t *testing.T) {
	const repo, sha = "owner/repo", "deadbeef"

	t.Run("production address has safe local default", func(t *testing.T) {
		t.Setenv("REDIS_ADDR", "")
		t.Setenv("NOVA_REDIS_HOST", "")
		t.Setenv("NOVA_REDIS_PORT", "")
		if got := redisAddrFromEnv(); got != "localhost:6379" {
			t.Fatalf("redisAddrFromEnv() = %q, want localhost:6379", got)
		}
	})

	t.Run("production address accepts land-lane endpoint", func(t *testing.T) {
		t.Setenv("REDIS_ADDR", "")
		t.Setenv("NOVA_REDIS_HOST", "redis.example.test")
		t.Setenv("NOVA_REDIS_PORT", "6380")
		if got := redisAddrFromEnv(); got != "redis.example.test:6380" {
			t.Fatalf("redisAddrFromEnv() = %q, want redis.example.test:6380", got)
		}
	})

	t.Run("record OK is green from redis without a check-run read", func(t *testing.T) {
		runner := &ciFakeRunner{out: ghRed}
		src := &ciFakeSource{values: map[string]string{repo + ":" + sha: "OK"}}
		c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
		if err != nil {
			t.Fatalf("Checks = %v, want nil", err)
		}
		if c.Verdict() != "GREEN" || c.Source != CIFromRedis {
			t.Fatalf("Verdict %s source %q, want GREEN from redis", c.Verdict(), c.Source)
		}
		if runner.calls != 0 {
			t.Fatalf("a record with a verdict read GitHub %d time(s)", runner.calls)
		}
	})

	t.Run("record FAIL is red from redis without a check-run read", func(t *testing.T) {
		runner := &ciFakeRunner{out: ghGreen}
		src := &ciFakeSource{values: map[string]string{repo + ":" + sha: "FAIL internal/merge TestX"}}
		c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
		if err != nil {
			t.Fatalf("Checks = %v, want nil", err)
		}
		if c.Verdict() != "RED" || runner.calls != 0 {
			t.Fatalf("Verdict %s after %d GitHub reads, want RED and none", c.Verdict(), runner.calls)
		}
	})

	for _, value := range []string{"ok", "success", "green", "pending", "NOTOK"} {
		value := value
		t.Run("record "+value+" is not green", func(t *testing.T) {
			runner := &ciFakeRunner{out: ghGreen}
			src := &ciFakeSource{values: map[string]string{repo + ":" + sha: value}}
			c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
			if err != nil {
				t.Fatalf("Checks(%q) = %v", value, err)
			}
			if c.Verdict() == "GREEN" || runner.calls != 0 {
				t.Fatalf("Checks(%q) = %s after %d GitHub reads, want not GREEN and none", value, c.Verdict(), runner.calls)
			}
		})
	}

	t.Run("record absent and GitHub green is green from-github", func(t *testing.T) {
		runner := &ciFakeRunner{out: ghGreen}
		src := &ciFakeSource{values: map[string]string{}}
		c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
		if err != nil {
			t.Fatalf("Checks = %v, want nil", err)
		}
		if c.Verdict() != "GREEN" || c.Source != CIFromGitHub {
			t.Fatalf("Verdict %s source %q, want GREEN from-github", c.Verdict(), c.Source)
		}
		if !strings.Contains(c.SourceWhy, "absent") {
			t.Fatalf("SourceWhy = %q, want it to say the record was absent", c.SourceWhy)
		}
		if runner.calls != 1 || src.calls != 1 {
			t.Fatalf("reads: GitHub %d, record %d; want 1 and 1", runner.calls, src.calls)
		}
	})

	t.Run("record absent and GitHub red is red", func(t *testing.T) {
		runner := &ciFakeRunner{out: ghRed}
		c, err := NewGH(repo, time.Second, runner, WithCISource(&ciFakeSource{})).Checks(sha)
		if err != nil {
			t.Fatalf("Checks = %v, want nil", err)
		}
		if c.Verdict() != "RED" || c.Source != CIFromGitHub {
			t.Fatalf("Verdict %s source %q, want RED from-github", c.Verdict(), c.Source)
		}
	})

	t.Run("record unreadable falls back to GitHub", func(t *testing.T) {
		runner := &ciFakeRunner{out: ghGreen}
		src := &ciFakeSource{err: errors.New("NOPERM User bench has no permissions to access the 'ci:owner/repo:deadbeef' key")}
		c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
		if err != nil {
			t.Fatalf("Checks = %v, want nil", err)
		}
		if c.Verdict() != "GREEN" || c.Source != CIFromGitHub || !strings.Contains(c.SourceWhy, "NOPERM") {
			t.Fatalf("Verdict %s source %q why %q, want GREEN from-github naming NOPERM", c.Verdict(), c.Source, c.SourceWhy)
		}
	})

	t.Run("record absent and no check-runs is MISSING", func(t *testing.T) {
		runner := &ciFakeRunner{out: ""}
		_, err := NewGH(repo, time.Second, runner, WithCISource(&ciFakeSource{})).Checks(sha)
		if !errors.Is(err, ErrCIMissing) {
			t.Fatalf("Checks = %v, want ErrCIMissing", err)
		}
	})

	t.Run("record absent and GitHub unreadable is an error, not MISSING", func(t *testing.T) {
		runner := &ciFakeRunner{err: errors.New("HTTP 502")}
		_, err := NewGH(repo, time.Second, runner, WithCISource(&ciFakeSource{})).Checks(sha)
		if err == nil || errors.Is(err, ErrCIMissing) {
			t.Fatalf("Checks = %v, want an unreadable-forge error that is not ErrCIMissing", err)
		}
	})

	// The key is exactly ci:<owner/repo>:<sha>:<gid>, the one land reads.
	const gid = "1234567890abcdef"
	if got := CIKey("o/r", "abc123", gid); got != "ci:o/r:abc123:"+gid || got != civerdict.Key("o/r", "abc123", gid) {
		t.Fatalf("CIKey = %q, want ci:o/r:abc123:%s", got, gid)
	}
}

// The production source reads the record as the HASH land reads. A string at
// the key (the pre-fix GET shape) is WRONGTYPE: an unreadable record, so the
// forge answers -- never ci: MISSING.
func TestRedisCISourceReadsTheHashLandReads(t *testing.T) {
	const repo, sha = "owner/repo", "deadbeef"
	const base, tip = "dev", "tip1"
	gid := civerdict.GID("single", base, tip, "req1", "pol1", "run1")
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	src := &RedisCISource{Client: client}
	key := CIKey(repo, sha, gid)

	seedPolicy := func() {
		mr.HSet(civerdict.PolicyKey(repo, base), "policy_id", "pol1", "required_set_id", "req1", "runner_id", "run1")
		mr.HSet(civerdict.TipKey(repo, base), "sha", tip)
	}

	t.Run("hash verdict OK lands from redis", func(t *testing.T) {
		mr.FlushAll()
		seedPolicy()
		mr.SAdd(civerdict.GIDsKey(repo, sha), gid)
		mr.HSet(key, civerdict.Field, "OK", "base", base, "base_sha", tip, "gid", gid)
		runner := &ciFakeRunner{out: ghRed}
		c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
		if err != nil || c.Verdict() != "GREEN" || c.Source != CIFromRedis || runner.calls != 0 {
			t.Fatalf("Checks = %s/%q, %v after %d GitHub reads; want GREEN from redis, none", c.Verdict(), c.Source, err, runner.calls)
		}
	})

	t.Run("stale base tip falls back to from-github", func(t *testing.T) {
		mr.FlushAll()
		seedPolicy()
		mr.SAdd(civerdict.GIDsKey(repo, sha), gid)
		mr.HSet(key, civerdict.Field, "OK", "base", base, "base_sha", tip, "gid", gid)
		// Move the base tip in redis
		mr.HSet(civerdict.TipKey(repo, base), "sha", "tip2")
		runner := &ciFakeRunner{out: ghGreen}
		c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
		if err != nil || c.Source != CIFromGitHub || c.Verdict() != "GREEN" {
			t.Fatalf("Checks = %s/%q, %v; want fallback to GREEN from-github on stale base tip", c.Verdict(), c.Source, err)
		}
	})

	t.Run("hash without a verdict falls back", func(t *testing.T) {
		mr.FlushAll()
		seedPolicy()
		mr.SAdd(civerdict.GIDsKey(repo, sha), gid)
		mr.HSet(key, "bench", "studio", "base", base, "base_sha", tip, "gid", gid)
		runner := &ciFakeRunner{out: ghGreen}
		c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
		if err != nil || c.Source != CIFromGitHub || c.Verdict() != "GREEN" {
			t.Fatalf("Checks = %s/%q, %v; want GREEN from-github", c.Verdict(), c.Source, err)
		}
	})

	t.Run("WRONGTYPE falls back to from-github", func(t *testing.T) {
		mr.FlushAll()
		seedPolicy()
		mr.SAdd(civerdict.GIDsKey(repo, sha), gid)
		if err := mr.Set(key, "OK"); err != nil {
			t.Fatal(err)
		}
		runner := &ciFakeRunner{out: ghGreen}
		c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
		if err != nil {
			t.Fatalf("Checks = %v, want the forge's answer", err)
		}
		if c.Source != CIFromGitHub || c.Verdict() != "GREEN" || !strings.Contains(c.SourceWhy, "WRONGTYPE") {
			t.Fatalf("Checks = %s/%q why %q; want GREEN from-github naming WRONGTYPE", c.Verdict(), c.Source, c.SourceWhy)
		}
	})

	t.Run("store down falls back to from-github", func(t *testing.T) {
		dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
		t.Cleanup(func() { _ = dead.Close() })
		runner := &ciFakeRunner{out: ghGreen}
		c, err := NewGH(repo, time.Second, runner, WithCISource(&RedisCISource{Client: dead})).Checks(sha)
		if err != nil || c.Source != CIFromGitHub || c.Verdict() != "GREEN" {
			t.Fatalf("Checks = %s/%q, %v; want GREEN from-github", c.Verdict(), c.Source, err)
		}
	})
}
