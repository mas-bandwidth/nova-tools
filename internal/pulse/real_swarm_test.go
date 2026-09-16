package pulse

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The bundled example's bin/nova-swarm is a sh fixture that records its argv and exits 0, so
// it swallows an interface mismatch with the real binary: launch used to pass --then to
// nova-swarm batch, which the same-revision pool/tasks admission mode does not define. This
// test builds the real nova-swarm from this repository once and holds launch to the exact
// contract the shipped binary accepts.

var (
	realSwarmOnce sync.Once
	realSwarmBin  string
	realSwarmErr  error
)

// realSwarm builds the real nova-swarm from the repository root once and returns a directory
// holding it under the name `nova-swarm`, so a launch composes with the same-revision binary
// rather than a fake.
func realSwarm(t *testing.T) string {
	t.Helper()
	realSwarmOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nova-pulse-real-swarm-")
		if err != nil {
			realSwarmErr = err
			return
		}
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			realSwarmErr = err
			return
		}
		realSwarmBin = filepath.Join(dir, "nova-swarm")
		cmd := exec.Command("go", "build", "-o", realSwarmBin, "./cmd/nova-swarm")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			realSwarmErr = fmt.Errorf("building nova-swarm: %v\n%s", err, out)
			return
		}
	})
	if realSwarmErr != nil {
		t.Fatal(realSwarmErr)
	}
	t.Setenv("PATH", filepath.Dir(realSwarmBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return realSwarmBin
}

// TestLaunchComposesWithRealSwarm admits one model route through the real nova-swarm
// binary from this same revision: the pool/tasks admission contract defines --pool,
// --tasks, --label, --deadline (a duration), --files and --tokens -- and not --then.
func TestLaunchComposesWithRealSwarm(t *testing.T) {
	realSwarm(t)

	root := t.TempDir()
	// The swarm pool is pre-existing infrastructure: quickstart makes it, and launch only
	// admits into it. Create the directory the real work run already had.
	if err := os.MkdirAll(filepath.Join(root, "pool"), 0o755); err != nil {
		t.Fatal(err)
	}
	cards, _ := writeCards(t, root, 3)

	var out, errb bytes.Buffer
	code := Launch(LaunchInput{
		Cards: cards, Root: root, Slots: 3, Deadline: "120",
		Now:    func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) },
		Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, errb.String())
	}
	if errb.String() != "" {
		t.Fatalf("stderr=%q, want empty", errb.String())
	}
	// The real swarm admitted the batch: three tasks sit in the pool's pending directory.
	pending := filepath.Join(root, "pool", "pending")
	entries, err := os.ReadDir(pending)
	if err != nil {
		t.Fatalf("no pending tasks under the pool: %v", err)
	}
	tasks := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".task" {
			tasks++
		}
	}
	if tasks != 3 {
		t.Fatalf("the real swarm admitted %d tasks, want 3; entries=%v", tasks, entries)
	}
}
