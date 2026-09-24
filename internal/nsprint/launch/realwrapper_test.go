//go:build unix

package launch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// realHarnessEnv turns the test binary into the harness the real nova-card
// runs. The wrapper strips NOVA_CARD_* and anything naming a token from the
// harness's environment, so the switch has its own name.
const realHarnessEnv = "NOVA_LAUNCH_TEST_HARNESS"

// realHarness writes a RESULT line into the card's out dir and exits DONE.
func realHarness() int {
	out := os.Getenv("NOVA_CARD_OUT")
	if out == "" {
		return 9
	}
	if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: "+os.Getenv("NOVA_CARD")+" sha=000000000000\n"), 0o644); err != nil {
		return 9
	}
	return 0
}

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

// jobsGoneWait bounds the wait for the wrapper to remove the job directory
// after card end.
const jobsGoneWait = 5 * time.Second

// waitJobsEmpty polls dir until it has no entries or bound passes, and
// returns what is left (nil once empty).
func waitJobsEmpty(dir string, bound time.Duration) []string {
	until := time.Now().Add(bound)
	for {
		entries, _ := os.ReadDir(dir)
		if len(entries) == 0 {
			return nil
		}
		if time.Now().After(until) {
			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}
			return names
		}
		// Waits only for the next probe; the bound above is the assertion.
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWaitJobsEmptyStillFailsWhenNothingDeletes is the control for the poll
// above: a job dir the wrapper never removes is still reported left behind,
// and one removed mid-wait is not.
func TestWaitJobsEmptyStillFailsWhenNothingDeletes(t *testing.T) {
	jobs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(jobs, "adopt-real", "card-real-wrapper", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if left := waitJobsEmpty(jobs, 100*time.Millisecond); len(left) != 1 || left[0] != "adopt-real" {
		t.Fatalf("never-deleted job dir: left %v, want [adopt-real]", left)
	}

	removed := make(chan error, 1)
	go func() {
		// The deleter runs a few probes after the poll starts.
		time.Sleep(60 * time.Millisecond)
		removed <- os.RemoveAll(filepath.Join(jobs, "adopt-real"))
	}()
	if left := waitJobsEmpty(jobs, jobsGoneWait); len(left) != 0 {
		t.Fatalf("job dir removed mid-wait still reported: %v", left)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
}

// buildRealWrapper builds cmd/nova-card from this module into a temp dir.
func buildRealWrapper(t *testing.T) string {
	t.Helper()
	env := exec.Command("go", "env", "GOMOD")
	env.Env = goenv.Clean(os.Environ())
	gomod, err := env.Output()
	if err != nil || strings.TrimSpace(string(gomod)) == "" {
		t.Fatalf("go env GOMOD: %v", err)
	}
	bin := filepath.Join(t.TempDir(), WrapperName)
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-card")
	build.Env = goenv.Clean(os.Environ())
	build.Dir = filepath.Dir(strings.TrimSpace(string(gomod)))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/nova-card: %v\n%s", err, out)
	}
	return bin
}

// startLaunchRedis starts a throwaway redis-server on a free local port.
func startLaunchRedis(t *testing.T) string {
	t.Helper()
	return testutil.Start(t)
}
