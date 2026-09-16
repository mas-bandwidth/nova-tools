package pulse

// The status verb's acceptance replays (SPEC-PULSE.md, "Status"):
// status-is-bounded, status-contraction-from-counts,
// status-adoption-includes-coordinator, status-remaining-counts-in-scope-only.
// Every gh is a fixture on PATH; the queue, the ADOPT files and the usage.tsv rows
// are files a test writes under t.TempDir.

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const statusStamp = "2026-09-15T12:00:00Z"

func setupStatus(t *testing.T, roots ...string) (queue, rootsArg, specs string, now time.Time) {
	t.Helper()
	fakeBins(t)
	base := t.TempDir()
	queue = filepath.Join(base, "queue")
	rootsArg = strings.Join(roots, ",")
	specs = fakePATH(t)
	now, _ = time.Parse(time.RFC3339, statusStamp)
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	return queue, rootsArg, specs, now
}

func fakeGh(t *testing.T, specs, body string) {
	t.Helper()
	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{Stdout: body}})
}

// fakePrIssueGh answers gh pr list and gh issue list with one fixture keyed on the argv.
func fakePrIssueGh(t *testing.T, specs, prs, issues string) {
	t.Helper()
	fakeTool(t, specs, "gh", fakeSpec{Rules: []fakeRule{
		{Arg: 1, Equals: "issue", Stdout: issues},
		{Arg: 1, Equals: "pr", Stdout: prs},
	}})
}

func writeStatusFile(t *testing.T, base, rel, body string) {
	t.Helper()
	p := filepath.Join(base, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSlots(t *testing.T, root string, states []string) {
	t.Helper()
	for i, s := range states {
		writeStatusFile(t, root, filepath.Join("pool", "slots", strconv.Itoa(i+1)+".json"),
			`{"state":"`+s+`"}`)
	}
}

// writeUsage writes one usage.tsv row (started, ended, rc, usd) under a bench.
func writeUsage(t *testing.T, root, job, started, ended, rc, usd string) {
	t.Helper()
	writeStatusFile(t, root, filepath.Join("1", "jobs", job, "usage.tsv"),
		"job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n"+
			job+"\t1\t"+started+"\t"+ended+"\t"+rc+"\t-\tgo\t-\t-\t-\t-\t-\t"+usd+"\n")
}

func runStatus(t *testing.T, queue, roots string, now time.Time, max int) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Status(StatusInput{
		Queue: queue, Roots: roots, Max: max, Day: "2026-09-15",
		Stdout: &out, Stderr: &errs, Now: func() time.Time { return now },
	})
	return out.String(), errs.String(), code
}

func countStatusLines(out string) (widths, friends int) {
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "STATUS WIDTH "):
			widths++
		case strings.HasPrefix(l, "STATUS ADOPTION "):
			friends++
		}
	}
	return widths, friends
}

// status-is-bounded: at the largest plausible state -- every bench and every friend in
// scope -- status prints at most --max lines per capped kind with one MORE line each.
func TestStatusIsBounded(t *testing.T) {
	base := t.TempDir()
	roots := make([]string, 5)
	for i := range roots {
		roots[i] = filepath.Join(base, "bench-"+strconv.Itoa(i))
	}
	queue, rootsArg, specs, now := setupStatus(t, roots...)
	writeStatusFile(t, queue, "COORDINATOR", "glenn\n")
	fakeGh(t, specs, "[]")
	for _, r := range roots {
		states := make([]string, 30)
		for i := range states {
			states[i] = "launched"
		}
		writeSlots(t, r, states)
		for i := 0; i < 30; i++ {
			writeStatusFile(t, r, filepath.Join("ADOPT", "friend-"+strconv.Itoa(i)),
				"version=1 receipt=1 edges=1\n")
		}
	}
	out, _, code := runStatus(t, queue, rootsArg, now, 3)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	widths, friends := countStatusLines(out)
	if widths > 3 {
		t.Errorf("WIDTH lines = %d, want at most 3 (--max):\n%s", widths, out)
	}
	if friends > 3 {
		t.Errorf("ADOPTION lines = %d, want at most 3 (--max)", friends)
	}
	if !strings.Contains(out, "STATUS MORE kind=width") {
		t.Errorf("capped WIDTH wants a MORE line, got:\n%s", out)
	}
	if !strings.Contains(out, "STATUS MORE kind=friend") {
		t.Errorf("capped ADOPTION wants a MORE line, got:\n%s", out)
	}
}

// status-contraction-from-counts: the CONTRACTION lines equal the counted cards cut/done,
// prs opened/merged and issues filed/closed, never a list or a report body.
func TestStatusContractionFromCounts(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a")
	queue, roots, specs, now := setupStatus(t, rootA)
	writeStatusFile(t, queue, "COORDINATOR", "glenn\n")
	writeStatusFile(t, queue, "REPO", "mas-bandwidth/nova-tools\n")
	writeSlots(t, rootA, []string{"free"})
	writeUsage(t, rootA, "j1", "2026-09-15T11:40:00Z", "2026-09-15T11:50:00Z", "0", "0.1")
	writeUsage(t, rootA, "j2", "2026-09-15T11:42:00Z", "2026-09-15T11:45:00Z", "0", "0.2")
	writeUsage(t, rootA, "j3", "2026-09-15T11:44:00Z", "2026-09-15T11:52:00Z", "1", "0.3")
	fakePrIssueGh(t, specs,
		`[{"number":1,"title":"t1","createdAt":"2026-09-15T11:30:00Z","mergedAt":"2026-09-15T11:55:00Z"},{"number":2,"title":"t2","createdAt":"2026-09-15T11:31:00Z","mergedAt":null}]`,
		`[{"number":1,"title":"i1","createdAt":"2026-09-15T11:20:00Z","closedAt":"2026-09-15T11:56:00Z"}]`)
	out, _, code := runStatus(t, queue, roots, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "STATUS CONTRACTION hour cards=3/3 prs=2/1 issues=1/1") {
		t.Errorf("CONTRACTION hour not from counts:\n%s", out)
	}
	if !strings.Contains(out, "STATUS CONTRACTION day cards=3/3 prs=2/1 issues=1/1") {
		t.Errorf("CONTRACTION day not from counts:\n%s", out)
	}
	if !strings.Contains(out, "verdict=CONVERGING") {
		t.Errorf("a balanced stream must read CONVERGING:\n%s", out)
	}
	bin := fakeBins(t)
	for _, name := range fakeTools {
		p := filepath.Join(bin, name+exeSuffix())
		if !strings.HasSuffix(p, exeSuffix()) {
			t.Errorf("fake path %q must end with exeSuffix %q", p, exeSuffix())
		}
	}
}

// status-adoption-includes-coordinator: the ADOPTION lines name every friend, the
// coordinator included, one line each; a missing coordinator line is red.
func TestStatusAdoptionIncludesCoordinator(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a")
	queue, roots, specs, now := setupStatus(t, rootA)
	fakeGh(t, specs, "[]")
	writeStatusFile(t, queue, "COORDINATOR", "glenn\n")
	writeSlots(t, rootA, []string{"free"})
	writeStatusFile(t, rootA, filepath.Join("ADOPT", "stella"), "version=1.2.3 receipt=7 edges=4\n")
	writeStatusFile(t, rootA, filepath.Join("ADOPT", "rowan"), "version=0.9 receipt=2 edges=1\n")
	out, _, code := runStatus(t, queue, roots, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var friends []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "STATUS ADOPTION ") {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(l, "STATUS ADOPTION "))
		if len(f) > 0 {
			friends = append(friends, f[0])
		}
	}
	sort.Strings(friends)
	want := "glenn,rowan,stella"
	if strings.Join(friends, ",") != want {
		t.Fatalf("ADOPTION friends = %v, want %v (coordinator glenn included):\n%s", friends, want, out)
	}
	if !strings.Contains(out, "STATUS ADOPTION glenn version=- receipt=0 edges=0") {
		t.Errorf("the coordinator with no ADOPT file wants its own dash line, got:\n%s", out)
	}
	if !strings.Contains(out, "STATUS ADOPTION stella version=1.2.3 receipt=7 edges=4") {
		t.Errorf("a friend's ADOPT line wants its values, got:\n%s", out)
	}
}

// status-rate-unknowns-stay-visible: with no measured usage in the window, the RATE line
// leaves every metric unknown (-) rather than inventing a zero cost and latency (#186:
// "Do not invent the cost or quality", "Unknowns remain visible").
func TestStatusRateUnknownsStayVisible(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a")
	queue, roots, bindir, now := setupStatus(t, rootA)
	fakeGh(t, bindir, "[]")
	writeStatusFile(t, queue, "COORDINATOR", "glenn\n")
	writeSlots(t, rootA, []string{"free"})
	out, _, code := runStatus(t, queue, roots, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "STATUS RATE cards_per_hour=- p50_s=- p90_s=- usd_per_card=- parallelism=-") {
		t.Errorf("RATE must leave unmeasured metrics unknown (-), got:\n%s", out)
	}
}

// status-remaining-counts-in-scope-only: a bench or friend outside --roots is nowhere on
// the line; only in-scope benches get a WIDTH line and only their friends an ADOPTION line.
func TestStatusRemainingCountsInScopeOnly(t *testing.T) {
	base := t.TempDir()
	rootA, rootB := filepath.Join(base, "a"), filepath.Join(base, "b")
	queue, roots, specs, now := setupStatus(t, rootA) // rootB exists but is out of scope
	fakeGh(t, specs, "[]")
	writeStatusFile(t, queue, "COORDINATOR", "glenn\n")
	writeStatusFile(t, queue, "UNREAD", "owner/repo#1\n")
	writeStatusFile(t, queue, "DIRTY", "owner/repo#2\n")
	writeStatusFile(t, queue, "UNCARDED", "owner/repo#3\n")
	writeSlots(t, rootA, []string{"free", "launched"})
	writeStatusFile(t, rootA, filepath.Join("ADOPT", "stella"), "version=1 receipt=1 edges=1\n")
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSlots(t, rootB, []string{"free"})
	writeStatusFile(t, rootB, filepath.Join("ADOPT", "rowan"), "version=2 receipt=2 edges=2\n")
	out, _, code := runStatus(t, queue, roots, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(out, "STATUS WIDTH b ") {
		t.Errorf("bench b is out of scope and must have no WIDTH line:\n%s", out)
	}
	if strings.Contains(out, "STATUS ADOPTION rowan") {
		t.Errorf("rowan lives on bench b and must have no ADOPTION line:\n%s", out)
	}
	if !strings.Contains(out, "STATUS WIDTH a running=1 slots=2 load=1 headroom=1") {
		t.Errorf("bench a wants its WIDTH line, got:\n%s", out)
	}
	if !strings.Contains(out, "STATUS REMAINING queue=0 unread_prs=1 dirty_prs=1 uncarded_issues=1") {
		t.Errorf("REMAINING wants the in-scope queue and PR counts, got:\n%s", out)
	}
}

// statusLine returns the one full line beginning with prefix, so a grammar change is a
// red test rather than a substring that still matches.
func statusLine(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no %q line in:\n%s", prefix, out)
	return ""
}

// status-contraction-window-and-threshold-stay-visible (#177: "Avoid reacting to one
// arbitrary sampling instant; configurable windows and thresholds must stay visible"):
// every CONTRACTION line names the sustained window and the threshold that produced its
// verdict; one above-threshold hour is never enough, two consecutive ticks are; the day
// line prints the same sustained verdict as the hour line (the two windows shared one
// marker before, and the second read of a tick could never see the run the first wrote);
// a marker in the old shape -- one bare hour key -- is a run of one; and
// --expanding-hours changes the hours the verdict requires and the window the line names.
func TestStatusContractionWindowAndThresholdStayVisible(t *testing.T) {
	base := t.TempDir()
	rootA, rootB := filepath.Join(base, "a"), filepath.Join(base, "b")
	queueA, roots, specs, now := setupStatus(t, rootA)
	writeSlots(t, rootA, []string{"free"})
	writeUsage(t, rootA, "j1", "2026-09-15T11:40:00Z", "2026-09-15T11:50:00Z", "0", "0.1")
	writeSlots(t, rootB, []string{"free"})
	writeUsage(t, rootB, "j0", "2026-09-15T10:40:00Z", "2026-09-15T10:50:00Z", "0", "0.1")
	writeUsage(t, rootB, "j1", "2026-09-15T11:40:00Z", "2026-09-15T11:50:00Z", "0", "0.1")
	tick1, _ := time.Parse(time.RFC3339, "2026-09-15T11:00:00Z")
	// Two PRs opened to one merged in the 11:00 hour, none anywhere else.
	prs11 := `[{"number":3,"title":"t3","createdAt":"2026-09-15T11:30:00Z","mergedAt":"2026-09-15T11:55:00Z"},{"number":4,"title":"t4","createdAt":"2026-09-15T11:31:00Z","mergedAt":null}]`
	prs1011 := `[{"number":1,"title":"t1","createdAt":"2026-09-15T10:30:00Z","mergedAt":"2026-09-15T10:55:00Z"},{"number":2,"title":"t2","createdAt":"2026-09-15T10:31:00Z","mergedAt":null},` + prs11[1:]

	// A ratio at 1 is CONVERGING, and the line names the window and the threshold.
	writeStatusFile(t, queueA, "REPO", "owner/repo\n")
	fakePrIssueGh(t, specs, "[]", "[]")
	out, _, code := runStatus(t, queueA, roots, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := statusLine(t, out, "STATUS CONTRACTION hour"); got != "STATUS CONTRACTION hour cards=1/1 prs=0/0 issues=0/0 verdict=CONVERGING window=2h above=1" {
		t.Errorf("a balanced hour must read CONVERGING with its window and threshold visible:\n%s", out)
	}
	if got := statusLine(t, out, "STATUS CONTRACTION day"); got != "STATUS CONTRACTION day cards=1/1 prs=0/0 issues=0/0 verdict=CONVERGING window=2h above=1" {
		t.Errorf("a balanced day must read CONVERGING with its window and threshold visible:\n%s", out)
	}

	// One above-threshold hour is not divergence: still CONVERGING, window visible.
	fakePrIssueGh(t, specs, prs11, "[]")
	out, _, code = runStatus(t, queueA, roots, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := statusLine(t, out, "STATUS CONTRACTION hour"); got != "STATUS CONTRACTION hour cards=1/1 prs=2/1 issues=0/0 verdict=CONVERGING window=2h above=1" {
		t.Errorf("one above-threshold hour is not sustained and must read CONVERGING:\n%s", out)
	}

	// A marker in the old shape -- one bare hour key from before this change -- is a run
	// of one, so the second consecutive above-threshold hour is EXPANDING on both lines.
	queueC := filepath.Join(base, "queue-c")
	if err := os.MkdirAll(queueC, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatusFile(t, queueC, "REPO", "owner/repo\n")
	writeStatusFile(t, queueC, "EXPANDING", "2026-09-15T11\n")
	out, _, code = runStatus(t, queueC, roots, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := statusLine(t, out, "STATUS CONTRACTION hour"); got != "STATUS CONTRACTION hour cards=1/1 prs=2/1 issues=0/0 verdict=EXPANDING window=2h above=1" {
		t.Errorf("two consecutive above-threshold hours must read EXPANDING on the hour line:\n%s", out)
	}
	if got := statusLine(t, out, "STATUS CONTRACTION day"); got != "STATUS CONTRACTION day cards=1/1 prs=2/1 issues=0/0 verdict=EXPANDING window=2h above=1" {
		t.Errorf("the sustained verdict is one fact and must read EXPANDING on the day line too:\n%s", out)
	}

	// Two ticks in a row above the threshold, remembered between them by the marker the
	// tool itself writes: the second tick is EXPANDING on both lines.
	queueD := filepath.Join(base, "queue-d")
	if err := os.MkdirAll(queueD, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatusFile(t, queueD, "REPO", "owner/repo\n")
	fakePrIssueGh(t, specs, prs1011, "[]")
	out, _, code = runStatus(t, queueD, rootB, tick1, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := statusLine(t, out, "STATUS CONTRACTION hour"); got != "STATUS CONTRACTION hour cards=1/1 prs=2/1 issues=0/0 verdict=CONVERGING window=2h above=1" {
		t.Errorf("the first above-threshold tick must read CONVERGING:\n%s", out)
	}
	out, _, code = runStatus(t, queueD, rootB, now, 20)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := statusLine(t, out, "STATUS CONTRACTION hour"); got != "STATUS CONTRACTION hour cards=1/1 prs=2/1 issues=0/0 verdict=EXPANDING window=2h above=1" {
		t.Errorf("the second consecutive above-threshold tick must read EXPANDING on the hour line:\n%s", out)
	}
	if got := statusLine(t, out, "STATUS CONTRACTION day"); got != "STATUS CONTRACTION day cards=2/2 prs=4/2 issues=0/0 verdict=EXPANDING window=2h above=1" {
		t.Errorf("the day line carries its own counts and the same sustained verdict:\n%s", out)
	}

	// --expanding-hours changes the window the verdict requires and the window the line
	// names; a window that is not a whole number of hours is refused.
	queueE, rootE := filepath.Join(base, "queue-e"), filepath.Join(base, "e")
	if err := os.MkdirAll(queueE, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatusFile(t, queueE, "REPO", "owner/repo\n")
	if err := os.MkdirAll(rootE, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGh(t, specs, "[]")
	var flagOut, flagErrs bytes.Buffer
	code = Main("nova-pulse", []string{"status", "--queue", queueE, "--roots", rootE, "--expanding-hours", "3"}, "test", &flagOut, &flagErrs)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, flagErrs.String())
	}
	if got := statusLine(t, flagOut.String(), "STATUS CONTRACTION hour"); got != "STATUS CONTRACTION hour cards=0/0 prs=0/0 issues=0/0 verdict=CONVERGING window=3h above=1" {
		t.Errorf("--expanding-hours 3 must name window=3h on the hour line:\n%s", flagOut.String())
	}
	if got := statusLine(t, flagOut.String(), "STATUS CONTRACTION day"); got != "STATUS CONTRACTION day cards=0/0 prs=0/0 issues=0/0 verdict=CONVERGING window=3h above=1" {
		t.Errorf("--expanding-hours 3 must name window=3h on the day line:\n%s", flagOut.String())
	}
	flagOut.Reset()
	code = Main("nova-pulse", []string{"status", "--queue", queueE, "--roots", rootE, "--expanding-hours", "0"}, "test", &flagOut, &flagErrs)
	if code != 2 {
		t.Errorf("--expanding-hours 0 is not a whole number of hours and must be refused at exit 2, got %d; stderr: %s", code, flagErrs.String())
	}
}
