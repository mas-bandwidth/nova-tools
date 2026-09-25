package merge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
)

// ciFakeRunner answers any gh call with out and counts the calls, so a test
// proves GH.Checks never asks GitHub. Exec is embedded so the fake satisfies
// Runner whatever else that interface grows, while Run is overridden.
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

// ghGreen is what GitHub's check-runs would answer for a green head: every
// test hands it to the runner, and no test may see it read.
const ghGreen = "lint\tsuccess\tdeadbeef\ntest\tsuccess\tdeadbeef\nci-ok\tsuccess\tdeadbeef\n"

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

// TestLanderReadsCIFromRedisNeverCheckRuns is #2924's DONE-WHEN: a fixture PR
// head with a green check-run and no ci key is refused ci: MISSING, and with
// ci:<repo>:<head> = OK it is admitted to the batch. GitHub is never asked.
func TestLanderReadsCIFromRedisNeverCheckRuns(t *testing.T) {
	const repo, sha = "owner/repo", "deadbeef"
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	check := func(t *testing.T) (Checks, error) {
		t.Helper()
		runner := &ciFakeRunner{out: ghGreen}
		c, err := NewGH(repo, time.Second, runner, WithCISource(&RedisCISource{Client: client})).Checks(sha)
		if runner.calls != 0 {
			t.Fatalf("Checks asked GitHub %d time(s); CI is read from Redis only", runner.calls)
		}
		return c, err
	}
	admitted := func(c Checks) string {
		_, a := standing(true, c, Standing{Satisfied: true}, GateStand{}, false)
		return a
	}

	t.Run("green check-run and no ci key is ci: MISSING", func(t *testing.T) {
		mr.FlushAll()
		_, err := check(t)
		if !errors.Is(err, ErrCIMissing) {
			t.Fatalf("Checks = %v, want ci: MISSING", err)
		}
		for _, key := range []string{CIRequestKey(repo, sha), CIGitHubKey(repo, sha)} {
			if !strings.Contains(err.Error(), key) {
				t.Fatalf("the refusal %q does not name %s", err, key)
			}
		}
	})

	t.Run("ci:<repo>:<head> OK batches", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet(CIRequestKey(repo, sha), "ci", "green", "final", "OK")
		c, err := check(t)
		if err != nil || c.Verdict() != "GREEN" || c.Source != CIFromRedis {
			t.Fatalf("Checks = %s/%q, %v; want GREEN from redis", c.Verdict(), c.Source, err)
		}
		if a := admitted(c); a != "hosted" {
			t.Fatalf("admitted = %q, want hosted (the head joins the batch)", a)
		}
	})

	t.Run("ci:<repo>:<head> red is red with its why", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet(CIRequestKey(repo, sha), "ci", "red", "why", "go-test: FAIL TestX")
		c, err := check(t)
		if err != nil || c.Verdict() != "RED" {
			t.Fatalf("Checks = %s, %v; want RED", c.Verdict(), err)
		}
	})

	t.Run("ci:<repo>:<head> pending waits, never batches", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet(CIRequestKey(repo, sha), "ci", "pending")
		mr.HSet(CIGitHubKey(repo, sha), "gh", "green")
		c, err := check(t)
		if err != nil || c.Verdict() == "GREEN" || admitted(c) != "" {
			t.Fatalf("Checks = %s admitted %q, %v; want not GREEN and not admitted", c.Verdict(), admitted(c), err)
		}
	})

	t.Run("the GitHub leg from the webhook answers when our CI has no record", func(t *testing.T) {
		mr.FlushAll()
		mr.HSet(CIGitHubKey(repo, sha), "gh", "green", "gh_fail", "")
		c, err := check(t)
		if err != nil || c.Verdict() != "GREEN" || admitted(c) != "hosted" {
			t.Fatalf("Checks = %s, %v; want GREEN and admitted", c.Verdict(), err)
		}
		mr.HSet(CIGitHubKey(repo, sha), "gh", "red", "gh_fail", "check:test")
		if c, err = check(t); err != nil || c.Verdict() != "RED" {
			t.Fatalf("Checks = %s, %v; want RED", c.Verdict(), err)
		}
	})

	t.Run("an unreadable record is an error, never MISSING, never GitHub", func(t *testing.T) {
		mr.FlushAll()
		if err := mr.Set(CIRequestKey(repo, sha), "OK"); err != nil {
			t.Fatal(err)
		}
		_, err := check(t)
		if err == nil || errors.Is(err, ErrCIMissing) || !strings.Contains(err.Error(), "WRONGTYPE") {
			t.Fatalf("Checks = %v, want an unreadable-record error naming WRONGTYPE", err)
		}
	})

	t.Run("a store that is down is an error, never GitHub", func(t *testing.T) {
		dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1})
		t.Cleanup(func() { _ = dead.Close() })
		runner := &ciFakeRunner{out: ghGreen}
		_, err := NewGH(repo, time.Second, runner, WithCISource(&RedisCISource{Client: dead})).Checks(sha)
		if err == nil || errors.Is(err, ErrCIMissing) || runner.calls != 0 {
			t.Fatalf("Checks = %v after %d GitHub reads; want an error and none", err, runner.calls)
		}
	})

	t.Run("no CI source is MISSING", func(t *testing.T) {
		runner := &ciFakeRunner{out: ghGreen}
		_, err := NewGH(repo, time.Second, runner, WithCISource(nil)).Checks(sha)
		if !errors.Is(err, ErrCIMissing) || runner.calls != 0 {
			t.Fatalf("Checks = %v after %d GitHub reads; want ci: MISSING and none", err, runner.calls)
		}
	})

	// The package names no check-runs endpoint outside its tests, so no
	// later host method can read one either.
	t.Run("no check-runs endpoint in the lander's source", func(t *testing.T) {
		files, err := filepath.Glob("*.go")
		if err != nil {
			t.Fatal(err)
		}
		endpoint := regexp.MustCompile(`/check-runs|check_runs|statusCheckRollup`)
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if loc := endpoint.FindIndex(b); loc != nil {
				t.Fatalf("%s reads a forge check state (%q)", f, b[loc[0]:loc[1]])
			}
		}
	})
}

// The injected source's words: exactly OK is green, FAIL... is red, anything
// else waits. GitHub is never asked whatever the word.
func TestChecksMapsTheRecordWord(t *testing.T) {
	const repo, sha = "owner/repo", "deadbeef"

	t.Run("production address refuses when nothing is set", func(t *testing.T) {
		t.Setenv("REDIS_ADDR", "")
		t.Setenv("NOVA_REDIS_HOST", "")
		t.Setenv("NOVA_REDIS_PORT", "")
		addr, configured := redisAddrFromEnv()
		if configured {
			t.Fatalf("redisAddrFromEnv() = %q, configured=true; want configured=false", addr)
		}
		src := RedisFromEnv()
		_, _, err := src.Read("owner/repo", "deadbeef")
		if !errors.Is(err, ErrRedisAddrNotConfigured) {
			t.Fatalf("RedisFromEnv().Read() err = %v, want ErrRedisAddrNotConfigured", err)
		}
	})

	t.Run("production address follows NOVA_REDIS_HOST and PORT", func(t *testing.T) {
		t.Setenv("REDIS_ADDR", "")
		t.Setenv("NOVA_REDIS_HOST", "redis.example.test")
		t.Setenv("NOVA_REDIS_PORT", "6380")
		addr, configured := redisAddrFromEnv()
		if !configured || addr != "redis.example.test:6380" {
			t.Fatalf("redisAddrFromEnv() = %q, configured=%v; want redis.example.test:6380, true", addr, configured)
		}
	})

	for value, want := range map[string]string{
		"OK": "GREEN", "FAIL internal/merge TestX": "RED",
		"ok": "PENDING", "success": "PENDING", "green": "PENDING", "pending": "PENDING", "NOTOK": "PENDING",
	} {
		value, want := value, want
		t.Run("record "+value, func(t *testing.T) {
			runner := &ciFakeRunner{out: ghGreen}
			src := &ciFakeSource{values: map[string]string{repo + ":" + sha: value}}
			c, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
			if err != nil {
				t.Fatalf("Checks(%q) = %v", value, err)
			}
			got := "PENDING"
			if c.Verdict() == "GREEN" || c.Verdict() == "RED" {
				got = c.Verdict()
			}
			if got != want || c.Source != CIFromRedis || runner.calls != 0 {
				t.Fatalf("Checks(%q) = %s/%q after %d GitHub reads, want %s from redis and none", value, c.Verdict(), c.Source, runner.calls, want)
			}
		})
	}

	t.Run("unreadable source is an error, not MISSING", func(t *testing.T) {
		runner := &ciFakeRunner{out: ghGreen}
		src := &ciFakeSource{err: errors.New("NOPERM User bench has no permissions to access the 'ci:owner/repo:deadbeef' key")}
		_, err := NewGH(repo, time.Second, runner, WithCISource(src)).Checks(sha)
		if err == nil || errors.Is(err, ErrCIMissing) || !strings.Contains(err.Error(), "NOPERM") || runner.calls != 0 {
			t.Fatalf("Checks = %v after %d GitHub reads; want an error naming NOPERM and none", err, runner.calls)
		}
	})

	// The keys are exactly the ones nova-sprint writes.
	const gid = "1234567890abcdef"
	if got := CIKey("o/r", "abc123", gid); got != "ci:o/r:abc123:"+gid || got != civerdict.Key("o/r", "abc123", gid) {
		t.Fatalf("CIKey = %q, want ci:o/r:abc123:%s", got, gid)
	}
	if got := CIRequestKey("o/r", "abc123"); got != "ci:o/r:abc123" {
		t.Fatalf("CIRequestKey = %q, want ci:o/r:abc123", got)
	}
	if got := CIGitHubKey("o/r", "abc123"); got != "ci:o/r:abc123:gh" {
		t.Fatalf("CIGitHubKey = %q, want ci:o/r:abc123:gh", got)
	}
}

// The ci card receipt answers first, read as the HASH land reads. A stale
// receipt (the base tip moved) says nothing, so the request record answers.
func TestRedisCISourceReadsTheReceiptLandReads(t *testing.T) {
	const repo, sha = "owner/repo", "deadbeef"
	const base, tip = "dev", "tip1"
	gid := civerdict.GID("single", base, tip, "req1", "pol1", "run1")
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	src := &RedisCISource{Client: client}
	key := CIKey(repo, sha, gid)

	seed := func() {
		mr.FlushAll()
		mr.HSet(civerdict.PolicyKey(repo, base), "policy_id", "pol1", "required_set_id", "req1", "runner_id", "run1")
		mr.HSet(civerdict.TipKey(repo, base), "sha", tip)
		mr.SAdd(civerdict.GIDsKey(repo, sha), gid)
		mr.HSet(key, civerdict.Field, "OK", "base", base, "base_sha", tip, "gid", gid)
	}

	t.Run("receipt OK is green over a red request record", func(t *testing.T) {
		seed()
		mr.HSet(CIRequestKey(repo, sha), "ci", "red")
		v, ok, err := src.Read(repo, sha)
		if err != nil || !ok || v != "OK" {
			t.Fatalf("Read = %q %v %v; want OK", v, ok, err)
		}
	})

	t.Run("stale receipt says nothing; the request record answers", func(t *testing.T) {
		seed()
		mr.HSet(civerdict.TipKey(repo, base), "sha", "tip2")
		if _, ok, err := src.Read(repo, sha); err != nil || ok {
			t.Fatalf("Read on a stale receipt alone = %v %v; want nothing", ok, err)
		}
		mr.HSet(CIRequestKey(repo, sha), "ci", "red", "why", "go-test: FAIL TestY")
		v, ok, err := src.Read(repo, sha)
		if err != nil || !ok || v != "FAIL go-test: FAIL TestY" {
			t.Fatalf("Read = %q %v %v; want the request record's red", v, ok, err)
		}
	})
}
