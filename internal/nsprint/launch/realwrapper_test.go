//go:build unix

package launch

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
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
	if entries, _ := os.ReadDir(jobs); len(entries) != 0 {
		t.Fatalf("job directory left behind under %s: %v", jobs, entries)
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
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skipf("redis-server unavailable; run this integration control on a Redis bench: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", strings.TrimPrefix(addr, "127.0.0.1:"),
		"--save", "", "--appendonly", "no", "--dir", dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	until := time.Now().Add(testWait())
	for {
		ctx, cancel := context.WithTimeout(context.Background(), testWait())
		err := client.Ping(ctx).Err()
		cancel()
		if err == nil {
			return addr
		}
		if time.Now().After(until) {
			t.Fatalf("throwaway redis did not start: %v", err)
		}
		// Waits only for the next readiness probe, never as the assertion.
		time.Sleep(10 * time.Millisecond)
	}
}
