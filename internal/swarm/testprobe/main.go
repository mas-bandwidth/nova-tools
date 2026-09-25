//go:build swarmtest

// Command testprobe is the helper binary TestPausePointThreadDirected
// (internal/swarm/killpoint_linux_test.go) builds with -tags swarmtest. It is
// committed rather than written into the tree at test time, because a test
// that creates and deletes files under the repository races every other
// package that walks the tree in parallel (internal/ci's class tests failed
// with "open .../testprobe/main.go: no such file or directory").
package main

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
