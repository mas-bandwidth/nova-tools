//go:build unix

package launch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
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

// jobsGoneWait bounds the wait for the wrapper to remove the job directory
// after card end.
const jobsGoneWait = 5 * time.Second

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
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

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

// startLaunchRedis starts a throwaway redis-server on a free local port and
// loads the nova_sprint library as the owner, as ns-deploy does on the fleet:
// the card path never loads it itself (#3551).
func startLaunchRedis(t *testing.T) string {
	t.Helper()
	addr := testutil.Start(t)
	owner := redis.NewClient(&redis.Options{Addr: addr})
	defer func() { _ = owner.Close() }()
	if err := fn.Load(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	return addr
}
