//go:build unix && functional

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
