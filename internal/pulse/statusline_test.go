package pulse

// G4's red test: the day-sized fixture's status is one line under 400 bytes and carries
// every field a fresh window needs. The mutation that matters: a line that grows with the
// bench count, which is how a "one line" answer becomes eight hundred bytes on the day the
// estate grows.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dayQueue writes the day-sized queue the other tests simulate: 200 cards done, 6 failed,
// 14 pending, two reds, nine merges, a day of usage and an open pit stop.
func dayQueue(t *testing.T) string {
	t.Helper()
	queue := t.TempDir()
	for sub, n := range map[string]int{"done": 200, "failed": 6, "pending": 14} {
		if err := os.MkdirAll(filepath.Join(queue, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if err := os.WriteFile(filepath.Join(queue, sub, fmt.Sprintf("card-%04d.md", i)), []byte("RESULT x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(t, filepath.Join(queue, "REDS"),
		"2026-09-16T09:05:00Z\tMAIN-RED\tdev\taaaa1111\n"+
			"2026-09-16T09:06:00Z\tRESUMED\tdev\tsuccess\n"+
			"2026-09-16T09:30:00Z\tMAIN-RED\tdev\tbbbb2222\n"+
			"2026-09-15T22:00:00Z\tMAIN-RED\tdev\tcccc3333\n") // yesterday's red is not today's
	var merges strings.Builder
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&merges, "2026-09-16T1%d:00:00Z\tMERGED\tnova-tools#%d\n", i, 800+i)
	}
	merges.WriteString("2026-09-15T10:00:00Z\tMERGED\tnova-tools#700\n")
	write(t, filepath.Join(queue, "MERGED"), merges.String())
	write(t, filepath.Join(queue, "PITSTOP"), "pit stop 3: class G, the coordinator's own tokens (#828)\n")
	return queue
}

// dayBench writes one bench: slot lock files and a usage.tsv of the day's cards.
func dayBench(t *testing.T, parent, name string, slots, busy int, usd float64, day string) string {
	t.Helper()
	root := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(root, "pool", "slots"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= slots; i++ {
		state := "free"
		if i <= busy {
			state = "launched"
		}
		write(t, filepath.Join(root, "pool", "slots", fmt.Sprintf("%d.json", i)), fmt.Sprintf(`{"state":%q}`, state))
	}
	// Two jobs, each with a usage.tsv of two rows: the day's, and yesterday's. A bench
	// declared with usd < 0 writes none, so a test about bench COUNT pays for slot files
	// alone -- a fixture bigger than its claim is a slow suite, and a slow suite is the
	// thing that brings everything to a crawl.
	for j := 0; usd >= 0 && j < 2; j++ {
		dir := filepath.Join(root, "1", "jobs", fmt.Sprintf("card-%d", j))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		b.WriteString("job\tlabel\tstarted\tended\trc\ta\tb\tc\td\te\tf\tg\tusd\n")
		fmt.Fprintf(&b, "j%d\tcard\t%sT10:00:00Z\t%sT10:10:00Z\t0\t-\t-\t-\t-\t-\t-\t-\t%.4f\n", j, day, day, usd)
		fmt.Fprintf(&b, "j%d\tcard\t2026-09-15T10:00:00Z\t2026-09-15T10:10:00Z\t0\t-\t-\t-\t-\t-\t-\t-\t99.0000\n", j)
		write(t, filepath.Join(dir, "usage.tsv"), b.String())
	}
	return root
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStatusLineIsOneLineUnderFourHundredBytes is the claim of G4: a fresh window
// reconstructs the day from this line and the policy, and never from the transcript.
func TestStatusLineIsOneLineUnderFourHundredBytes(t *testing.T) {
	const day = "2026-09-16"
	queue := dayQueue(t)
	benches := t.TempDir()
	studio := dayBench(t, benches, "studio", 8, 5, 0.1800, day)
	space := dayBench(t, benches, "space", 64, 40, 0.0600, day)

	var out, errs bytes.Buffer
	exit := StatusLine(StatusInput{
		Queue: queue, Roots: studio + "," + space, Day: day,
		Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC) },
	})
	if exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, out.String(), errs.String())
	}
	line := strings.TrimSuffix(out.String(), "\n")
	if strings.Contains(line, "\n") {
		t.Fatalf("status --oneline printed more than one line:\n%s", out.String())
	}
	if len(line) >= StatusLineMax {
		t.Errorf("the line is %d bytes, want under %d:\n%s", len(line), StatusLineMax, line)
	}
	for _, want := range []string{
		"STATUS 2026-09-16",
		"stop=no",
		"pool=14",
		"cards=200/6",
		"reds=2",       // today's two, never yesterday's
		"merges=9",     // today's nine, never yesterday's
		"spend=0.4800", // two benches, two jobs each, the day's rows only
		"studio:5/8",
		"space:40/64",
		"pitstop=pit",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the line does not carry %q:\n%s", want, line)
		}
	}
	if errs.Len() != 0 {
		t.Errorf("stderr is not empty: %s", errs.String())
	}
}

// TestStatusLineHoldsTheCeilingAtEveryBench: at the largest plausible state the line is
// still one line under the ceiling, and the benches that did not fit are COUNTED. A
// listing whose length is the state's length is the thing internal/bounded exists to end.
func TestStatusLineHoldsTheCeilingAtEveryBench(t *testing.T) {
	queue := t.TempDir() // this claim is about the bench count, not the card count
	benches := t.TempDir()
	var roots []string
	for i := 0; i < 40; i++ {
		roots = append(roots, dayBench(t, benches, fmt.Sprintf("bench-with-a-long-name-%02d", i), 2, 1, -1, "2026-09-16"))
	}
	var out, errs bytes.Buffer
	StatusLine(StatusInput{
		Queue: queue, Roots: strings.Join(roots, ","), Day: "2026-09-16",
		Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC) },
	})
	line := strings.TrimSuffix(out.String(), "\n")
	if len(line) >= StatusLineMax {
		t.Fatalf("the line is %d bytes at 40 benches, want under %d:\n%s", len(line), StatusLineMax, line)
	}
	if !strings.Contains(line, "benches_more=") {
		t.Errorf("the benches that did not fit are not counted:\n%s", line)
	}
}

// TestStatusLineSaysStopAndAnUnmeasuredSpend: a STOP is the first thing a window must see,
// and a bench nobody measured is a bench with no slots and no spend -- printed as the zeros
// they are, never left off the line, because a bench missing from the line reads as a bench
// that is not in scope.
func TestStatusLineSaysStopAndAnUnmeasuredSpend(t *testing.T) {
	queue := t.TempDir()
	write(t, filepath.Join(queue, "STOP"), "MAIN-RED aaaa1111: revert first\n")
	bench := filepath.Join(t.TempDir(), "empty-bench")
	if err := os.MkdirAll(bench, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	StatusLine(StatusInput{
		Queue: queue, Roots: bench, Day: "2026-09-16", Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC) },
	})
	line := out.String()
	for _, want := range []string{"stop=yes", "pool=0", "cards=0/0", "reds=0", "merges=0", "spend=0.0000", "pitstop=-", "width=empty-bench:0/0"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line does not carry %q: %s", want, line)
		}
	}
}

// TestStatusLineRefusesWithoutQueueOrRoots and refuses a day it cannot read.
func TestStatusLineRefusesWithoutQueueOrRoots(t *testing.T) {
	for _, c := range []struct{ queue, roots, day, want string }{
		{"", "x", "", "--queue"},
		{"x", "", "", "--roots"},
		{"x", "y", "yesterday", "YYYY-MM-DD"},
	} {
		var out, errs bytes.Buffer
		if exit := StatusLine(StatusInput{Queue: c.queue, Roots: c.roots, Day: c.day, Stdout: &out, Stderr: &errs}); exit != 2 {
			t.Errorf("exit %d, want 2 for %+v", exit, c)
		}
		if !strings.Contains(errs.String(), c.want) {
			t.Errorf("the refusal does not name %s: %s", c.want, errs.String())
		}
	}
}
