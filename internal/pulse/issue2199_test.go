package pulse

// SPEC-STATE.md §14, §15 and §27 (nova-tools #2199):
//   - `nova-pulse watch` SUBSCRIBEs to `nova:events:job` and returns once per change
//     inside a second, then re-reads the file.
//   - `nova-pulse status` prints the live unknown call count beside the cap counts.
//   - `nova-pulse status --fleet` reads the `nodes`/`receipts` projection in the queue
//     directory instead of opening every job file under --roots.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssue2199(t *testing.T) {
	t.Run("watch subscriber wakes on job done event within a second", func(t *testing.T) {
		queue, bus, jobs := watchEstate(t)
		events := make(chan string, 1)
		clock := newWatchClock()
		labelDir := filepath.Join(jobs, "slot-1", "jobs", "card-test")
		ticks := &watchTicks{clock: clock, hooks: map[int]func(){
			1: func() {
				if err := os.MkdirAll(labelDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(labelDir, "RESULT.md"), []byte("RESULT: the card\ndone\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		}}

		var out, errs bytes.Buffer
		done := make(chan int, 1)
		go func() {
			done <- Watch(WatchInput{
				Queue: queue, Bus: bus, Jobs: jobs, Until: "job=card-test done",
				Cap: 120 * time.Second, Now: clock.Now, Sleep: ticks.sleep,
				Events: events, Stdout: &out, Stderr: &errs,
			})
		}()

		// Publish a job event. The watch's event path calls Sleep(0) before the
		// re-read, so the hook above lands the RESULT.md between wake and re-read.
		events <- "job"

		select {
		case code := <-done:
			if code != 0 {
				t.Fatalf("watch exit = %d, want 0; stderr=%s\nout=%s", code, errs.String(), out.String())
			}
		case <-time.After(time.Second):
			t.Fatalf("watch did not wake on the event within 1s; stderr=%s\nout=%s", errs.String(), out.String())
		}

		got := out.String()
		want := "JOB label=card-test state=done"
		if !strings.Contains(got, want) {
			t.Fatalf("watch output missing job line:\n%s\nwant substring %q", got, want)
		}
	})

	t.Run("status prints the live unknown call count", func(t *testing.T) {
		base := t.TempDir()
		rootA := filepath.Join(base, "a")
		queue, roots, specs, now := setupStatus(t, rootA)
		fakeGh(t, specs, "[]")
		writeStatusFile(t, queue, "COORDINATOR", "glenn\n")
		writeSlots(t, rootA, []string{"free"})

		// Three live unknown calls, one per non-empty line.
		writeStatusFile(t, queue, "UNKNOWN", "call-1\ncall-2\ncall-3\n")

		var out, errs bytes.Buffer
		code := Status(StatusInput{
			Queue: queue, Roots: roots, Max: 20, Day: "2026-09-15",
			Stdout: &out, Stderr: &errs, Now: func() time.Time { return now },
		})
		if code != 0 {
			t.Fatalf("status exit = %d, want 0; stderr=%s\nout=%s", code, errs.String(), out.String())
		}

		if !strings.Contains(out.String(), "STATUS UNKNOWN count=3") {
			t.Fatalf("status output missing unknown count:\n%s", out.String())
		}
	})

	t.Run("status fleet reads the projection not job files", func(t *testing.T) {
		base := t.TempDir()
		rootA := filepath.Join(base, "a")
		queue := filepath.Join(base, "queue")
		roots := rootA
		if err := os.MkdirAll(queue, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(rootA, 0o755); err != nil {
			t.Fatal(err)
		}
		writeStatusFile(t, queue, "COORDINATOR", "glenn\n")

		// The projection: one node and two receipts.
		writeStatusFile(t, queue, "nodes.tsv",
			"node_1\tcard\tdoing\tglenn\tmas-bandwidth/nova-tools\tdev\trowan/fix\tfalse\tabc\t2026-09-15T12:00:00Z\n")
		writeStatusFile(t, queue, "receipts.tsv",
			"req-1\tnode_1\tlaunch\tabc\tglenn\t2026-09-15T12:00:00Z\tsha-1\n"+
				"req-2\tnode_1\tlaunch\tabc\tglenn\t2026-09-15T12:01:00Z\tsha-2\n")

		// A job directory under --roots that fleet mode must not walk: if it did,
		// usage.tsv parsing would dominate the output.
		writeStatusFile(t, rootA, filepath.Join("1", "jobs", "card-wrong", "usage.tsv"),
			"job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n"+
				"card-wrong\t1\t-\t-\t-\t-\t-\t-\t-\t-\t-\t-\t-\n")

		var out, errs bytes.Buffer
		code := Main("nova-pulse", []string{"status", "--queue", queue, "--roots", roots, "--fleet"}, "test", &out, &errs)
		if code != 0 {
			t.Fatalf("status --fleet exit = %d, want 0; stderr=%s\nout=%s", code, errs.String(), out.String())
		}
		got := out.String()
		if !strings.Contains(got, "STATUS FLEET nodes=1 receipts=2") {
			t.Fatalf("status --fleet missing projection line:\n%s", got)
		}
		if strings.Contains(got, "card-wrong") {
			t.Fatalf("status --fleet walked job files, got card-wrong in output:\n%s", got)
		}
	})
}
