//go:build linux && swarmtest

package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const probeSource = `package main

import (
	"os"
	"path/filepath"
	"sync"

	swarm "github.com/mas-bandwidth/nova-tools/internal/swarm"
)

func main() {
	dir := os.Getenv("PROBE_DIR")
	if dir == "" {
		os.Exit(1)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		swarm.CheckPausePoint("test-pause")
		_ = os.WriteFile(filepath.Join(dir, "after.txt"), []byte("written after resume\n"), 0o644)
	}()
	wg.Wait()
}
`

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func TestPausePointThreadDirected(t *testing.T) {
	dir := t.TempDir()
	markPath := filepath.Join(dir, "mark.txt")
	afterPath := filepath.Join(dir, "after.txt")

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}

	probeDir := filepath.Join(repoRoot, "internal", "swarm", "testprobe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(probeDir)

	probeSrc := filepath.Join(probeDir, "main.go")
	if err := os.WriteFile(probeSrc, []byte(probeSource), 0o644); err != nil {
		t.Fatal(err)
	}

	binFile := filepath.Join(dir, "probe_bin")
	cmd := exec.Command("go", "build", "-tags", "swarmtest", "-o", binFile, "./internal/swarm/testprobe")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}

	for i := 0; i < 200; i++ {
		if err := os.Remove(markPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Remove(afterPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}

		child := exec.Command(binFile)
		child.Env = append(os.Environ(),
			"NOVA_SWARM_PAUSEPOINT=test-pause",
			"NOVA_SWARM_PAUSE_MARK="+markPath,
			"PROBE_DIR="+dir,
		)
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}

		for waited := 0; waited < 3000; waited++ {
			if _, err := os.Stat(markPath); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}

		time.Sleep(10 * time.Millisecond)

		if _, err := os.Stat(afterPath); err == nil {
			child.Process.Kill()
			child.Wait()
			t.Fatalf("iteration %d: after.txt existed while child should be stopped (proof of process-directed SIGSTOP race)", i)
		}

		_ = child.Process.Signal(syscall.SIGCONT)
		child.Wait()
	}
}
