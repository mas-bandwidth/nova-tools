//go:build unix && slow && functional

package launch

// The real-wrapper tests build nova-card and run it against a private
// redis-server: functional tests by Glenn's rule (2026-09-25: a test with a
// process or a socket in the loop is not a unit test), behind the slow tag
// after they overran on the merge group's darwin leg (run 20106054139,
// superman: Started:0 Refused:1 Overran:true on a loaded x64 Mac). The
// coordinator runs them by hand (make test-slow, or go test -tags slow
// ./internal/nsprint/launch) when merging or changing the wrapper; nightly
// runs them whole.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/redis/go-redis/v9"
)

// TestLaunchRealWrapper is #3200's DONE-WHEN: `card launch` over the real
// nova-card, built from cmd/nova-card (not the fixture wrapper), launches one
// line; the wrapper calls `card launched` and then writes an end record under
// <results>/<sprint>/<label>/<base sha8>/<bench>/<attempt>.
func TestLaunchRealWrapper(t *testing.T) {
	wrapper := buildRealWrapper(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	addr := startLaunchRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	id := card.Identity{Sprint: "adopt-real", Label: "card-real-wrapper", BaseSHA: "4956ccb8", Bench: "real-bench", Attempt: 1}
	token := "1." + strings.Repeat("ab", 16)
	if err := client.HSet(ctx, card.CardKey(id.Sprint, id.Label), map[string]string{
		"state":     "dealt",
		"attempt":   "1",
		"token":     token,
		"token_sha": card.TokenSHA(token),
		"identity":  id.String(),
		"bench":     id.Bench,
		"base_sha":  id.BaseSHA,
	}).Err(); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	jobs, results := filepath.Join(root, "jobs"), filepath.Join(root, "results")
	for _, dir := range []string{jobs, results} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The detached wrapper inherits the bench configuration from the launcher.
	t.Setenv("NOVA_CARD_REDIS", addr)
	t.Setenv("NOVA_CARD_BENCH", id.Bench)
	t.Setenv("NOVA_CARD_HARNESS", self)
	t.Setenv("NOVA_CARD_JOBS", jobs)
	t.Setenv("NOVA_CARD_RESULTS", results)
	t.Setenv("NOVA_CARD_CLOCK", "45m")
	t.Setenv("NOVA_CARD_BEAT", "60s")
	t.Setenv(realHarnessEnv, "done")

	var out strings.Builder
	line := Line{Sprint: id.Sprint, Label: id.Label, Attempt: 1, Token: token}
	res, err := Launch(strings.NewReader(line.String()+"\n"), &out, Config{Wrapper: wrapper})
	if err != nil || res.Started != 1 || res.Refused != 0 {
		t.Fatalf("launch %+v err %v:\n%s", res, err, out.String())
	}
	if strings.Contains(out.String(), token) {
		t.Fatalf("launch output carries the token:\n%s", out.String())
	}

	dir := filepath.Join(results, id.Sprint, id.Label, id.BaseSHA, id.Bench, "1")
	record := filepath.Join(dir, card.EndRecordName)
	until := time.Now().Add(testWait())
	for {
		if _, err := os.Stat(record); err == nil {
			break
		}
		if time.Now().After(until) {
			hash, _ := client.HGetAll(ctx, card.CardKey(id.Sprint, id.Label)).Result()
			t.Fatalf("no end record at %s within %s; card hash %v", record, testWait(), hash)
		}
		// Waits only for the next probe; the bound above is the assertion.
		time.Sleep(20 * time.Millisecond)
	}
	rec, err := card.ReadEndRecord(dir)
	if err != nil || rec.Identity != id || rec.Outcome != "DONE" || rec.Reason != "done" || rec.TokenSHA != card.TokenSHA(token) {
		t.Fatalf("end record %+v err %v", rec, err)
	}

	// The end record is written before card end; wait for the hash to end.
	for {
		hash, err := client.HGetAll(ctx, card.CardKey(id.Sprint, id.Label)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if hash["state"] == "ended" {
			if hash["outcome"] != "DONE" {
				t.Fatalf("card hash %v, want ended DONE", hash)
			}
			break
		}
		if time.Now().After(until) {
			t.Fatalf("card hash never ended: %v", hash)
		}
		// Waits only for the next probe; the bound above is the assertion.
		time.Sleep(20 * time.Millisecond)
	}

	// launched, then ended, in that order on the sprint log.
	msgs, err := client.XRange(ctx, card.LogKey(id.Sprint), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, m := range msgs {
		if strings.Contains(fmt.Sprint(m.Values), token) {
			t.Fatalf("log entry %s carries the raw token", m.ID)
		}
		if to, _ := m.Values["to"].(string); to == "launched" || to == "ended" {
			order = append(order, to)
		}
	}
	if strings.Join(order, ",") != "launched,ended" {
		t.Fatalf("sprint log transitions %v, want launched then ended", order)
	}
	if _, err := os.Stat(filepath.Join(dir, "RESULT.md")); err != nil {
		t.Fatalf("the harness's out dir did not reach the results dir: %v", err)
	}
	// The wrapper removes the job dir after it ends the card, so the hash
	// reading ended does not mean the dir is gone yet (#3218's flake): poll.
	if left := waitJobsEmpty(jobs, jobsGoneWait); len(left) != 0 {
		t.Fatalf("job directory left behind under %s after %s: %v", jobs, jobsGoneWait, left)
	}
}

// TestLaunchRealWrapperReportsNotDealtAsRefused covers the production
// nova-card acknowledgement, rather than only the fixture protocol. A stale
// launch line whose card is no longer dealt must not be counted as started.
func TestLaunchRealWrapperReportsNotDealtAsRefused(t *testing.T) {
	wrapper := buildRealWrapper(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	addr := startLaunchRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	id := card.Identity{Sprint: "refuse-real", Label: "card-no-longer-dealt", BaseSHA: "4956ccb8", Bench: "real-bench", Attempt: 1}
	token := "1." + strings.Repeat("cd", 16)
	if err := client.HSet(ctx, card.CardKey(id.Sprint, id.Label), map[string]string{
		"state": "queued", "attempt": "1", "token": token, "token_sha": card.TokenSHA(token),
		"identity": id.String(), "bench": id.Bench, "base_sha": id.BaseSHA,
	}).Err(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	jobs, results := filepath.Join(root, "jobs"), filepath.Join(root, "results")
	for _, dir := range []string{jobs, results} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("NOVA_CARD_REDIS", addr)
	t.Setenv("NOVA_CARD_BENCH", id.Bench)
	t.Setenv("NOVA_CARD_HARNESS", self)
	t.Setenv("NOVA_CARD_JOBS", jobs)
	t.Setenv("NOVA_CARD_RESULTS", results)
	t.Setenv("NOVA_CARD_CLOCK", "45m")
	t.Setenv("NOVA_CARD_BEAT", "60s")
	t.Setenv(realHarnessEnv, "done")

	line := Line{Sprint: id.Sprint, Label: id.Label, Attempt: id.Attempt, Token: token}
	var out strings.Builder
	res, err := Launch(strings.NewReader(line.String()+"\n"), &out, Config{Wrapper: wrapper})
	if err != nil || res.Started != 0 || res.Refused != 1 {
		t.Fatalf("launch %+v err %v, want started=0 refused=1:\n%s", res, err, out.String())
	}
	if strings.Contains(out.String(), "LAUNCHED "+line.Card()) ||
		!strings.Contains(out.String(), "REFUSED line=1 wrapper "+line.Card()) ||
		!strings.Contains(out.String(), "LAUNCH started=0 refused=1") {
		t.Fatalf("launch output is not truthful:\n%s", out.String())
	}
	if state := client.HGet(ctx, card.CardKey(id.Sprint, id.Label), "state").Val(); state != "queued" {
		t.Fatalf("refused wrapper changed card state to %q", state)
	}
	for name, dir := range map[string]string{"jobs": jobs, "results": results} {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("refused wrapper left %s entries %v (%v)", name, entries, err)
		}
	}
}

// waitJobsEmpty polls dir until it has no entries or bound passes, and
// returns what is left (nil once empty).
