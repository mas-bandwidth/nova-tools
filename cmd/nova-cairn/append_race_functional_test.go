//go:build functional && !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Twelve nova-cairn processes append one entry id at once, round after round,
// in each shape. Before the store lock, the own shape answered APPEND OK
// duplicate=false to several of them in every round, with one text surviving.
func TestConcurrentProcessAppendsOfOneIDHaveOneWinner(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bin := builtBinary(t, root)
	const procs, rounds = 12, 12
	for _, shape := range []string{"bench", "own"} {
		for round := 0; round < rounds; round++ {
			store := filepath.Join(root, shape+strconv.Itoa(round))
			if err := os.Mkdir(store, 0o755); err != nil {
				t.Fatal(err)
			}
			if shape == "bench" {
				if err := os.WriteFile(filepath.Join(store, "other.md"), []byte("# other\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			run := func(args ...string) (string, int) {
				cmd := exec.Command(filepath.Join(bin, "nova-cairn"), args...)
				out, err := cmd.CombinedOutput()
				code := 0
				if ee, ok := err.(*exec.ExitError); ok {
					code = ee.ExitCode()
				} else if err != nil {
					t.Fatal(err)
				}
				return string(out), code
			}
			if out, code := run("open", "--store", store, "--session", "NEW", "--publish", "manual"); code != 0 {
				t.Fatalf("open: %d %s", code, out)
			}
			type result struct {
				out  string
				code int
			}
			start := make(chan struct{})
			results := make(chan result, procs)
			var wg sync.WaitGroup
			for i := 0; i < procs; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					out, code := run("append", "--store", store, "--session", "NEW", "--entry", "e1", "--text", "words "+strconv.Itoa(i), "--publish", "manual")
					results <- result{out, code}
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			wrote, conflicts := 0, 0
			for r := range results {
				switch {
				case r.code == 0 && strings.Contains(r.out, "duplicate=false"):
					wrote++
				case r.code == 1 && strings.Contains(r.out, "APPEND FAIL"):
					conflicts++
				default:
					t.Fatalf("%s round %d: unexpected answer exit=%d %q", shape, round, r.code, r.out)
				}
			}
			if wrote != 1 || conflicts != procs-1 {
				t.Fatalf("%s round %d: %d writers and %d conflicts; want exactly 1 and %d", shape, round, wrote, conflicts, procs-1)
			}
			out, code := run("index", "--store", store, "--session", "NEW")
			if code != 0 || !strings.Contains(out, "entries=1") {
				t.Fatalf("%s round %d: index %d %q", shape, round, code, out)
			}
		}
	}
}
