// The stop kills a process GROUP and then asks whether a grandchild survived, which is a
// unix question on a unix bench; windows is not a bench for this tool (windows_not_a_bench_test.go).
//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE STOP (SPEC-SWARM rule 13d, demanded test 13d, issue #1545). Slice 4 of the cap.
//
// The clauses this file holds:
//
//	"a fake harness that publishes a `RESULT.md`, forks a grandchild, ignores the terminate
//	 and writes usage rows past `--tokens` is ended with no process of its group alive, the
//	 `RESULT.md` byte for byte what it published, exactly one launch, exit 1, a usage row
//	 with `end=budget` and `rc` a dash ... and a `NATIVE OK` line carrying `rc=-1`,
//	 `budget=<spent>/<n>` with `spent` at least `n`, and `stopped=tokens`"
//	"a fake harness whose sum is exactly `--tokens` is ended, and one whose `cache_read`
//	 alone passes the number is not"
//	"THE TWO-LAUNCH ACCOUNTING: under `--tokens 100`, a first launch that reports 40 and
//	 dies on a provider 5xx inside the launch grace, then a second that reports 70, is
//	 stopped in the second, the line prints `budget=110/100`, `usage.tsv` holds two rows
//	 whose summed columns are 40 and 70, never 40 and 110, the second row alone has
//	 `end=budget`, and the rows add to the line's 110"
//	"a first launch that reached the budget alone is never launched again"
//	"a usage reader that errors on three consecutive samples ends the card with
//	 `end=budget-unverifiable`, `stopped=unverifiable` and exit 1, and one that errors twice
//	 then answers does not"
//	"a description's `max_turns` passed, and in a second case its `max_cache_read`, ends the
//	 card with `end=budget`, `stopped=max_turns` or `stopped=max_cache_read`, the
//	 `PROMPT-DEFECT` line on `native`'s stdout after `NATIVE OK`, and a published
//	 `RESULT.md` unchanged by a byte"

// usageRows reads a card's usage.tsv into a header and its rows, split on tabs.
func usageRows(t *testing.T, jobDir string) ([]string, [][]string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(jobDir, "usage.tsv"))
	if err != nil {
		t.Fatalf("the card wrote no usage.tsv under %s: %v", jobDir, err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("usage.tsv holds a header and at least one row:\n%s", raw)
	}
	var rows [][]string
	for _, l := range lines[1:] {
		rows = append(rows, strings.Split(l, "\t"))
	}
	return strings.Split(lines[0], "\t"), rows
}

// cell is one named column of one usage row.
func cell(t *testing.T, head []string, row []string, name string) string {
	t.Helper()
	for i, h := range head {
		if h == name && i < len(row) {
			return row[i]
		}
	}
	t.Fatalf("usage.tsv has no column %q in %v", name, head)
	return ""
}

// TestNativeBudgetStopsTheCardAndKeepsWhatItPublished is the heart of demanded test 13d.
func TestNativeBudgetStopsTheCardAndKeepsWhatItPublished(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := filepath.Join(root, "card.md")
	// A card that publishes FIRST (so there is a report to keep), spends past the budget,
	// leaves a grandchild behind and then declines the terminate: every hard case of the
	// rule's own sentence in one card.
	body := "a card\nFAKE-PUBLISH-FIRST\nFAKE-FINDINGS 2\n" +
		"FAKE-USAGE-DB 60000 40000 0 0 0 1.0\n" +
		"FAKE-BACKGROUND-SLEEP 30\nFAKE-IGNORE-TERM\nFAKE-SLEEP 60\n"
	if err := os.WriteFile(card, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(budgetNativeArgs(t, bin, card, slot, root, "100000"), "--usage-interval", "1s")
	for i := range args {
		if args[i] == "--deadline" {
			args[i+1] = "120s" // the BUDGET must be what ends this card, never the deadline
		}
	}
	var stdout, stderr strings.Builder
	start := time.Now()
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	elapsed := time.Since(start)

	// EXIT 1: "it ran, and the answer is no".
	if rc != 1 {
		t.Fatalf("a card the budget stopped exits 1, got %d after %v\nstdout:\n%s\nstderr:\n%s", rc, elapsed, stdout.String(), stderr.String())
	}
	// THE EVENT, NOT THE CLOCK. What proves the BUDGET ended this card rather than its
	// deadline or its own exit is the `stopped=tokens` field asserted below: a deadline
	// leaves that field off the line entirely. The repo refuses a wall-clock assertion and
	// it is right to -- the bound would only be a slower way of reading the same field.
	line := nativeOKLine(t, stdout.String())
	if got := fieldOf(line, "rc"); got != "-1" {
		t.Errorf("the line prints rc=-1 as it does for a deadline and a TERM, got %q:\n%s", got, line)
	}
	if got := fieldOf(line, "stopped"); got != "tokens" {
		t.Errorf("the line names which budget fired, stopped=tokens, got %q:\n%s", got, line)
	}
	budget := fieldOf(line, "budget")
	spentWord, ofWord, _ := strings.Cut(budget, "/")
	if ofWord != "100000" {
		t.Errorf("budget= names the number the caller gave, got %q:\n%s", budget, line)
	}
	spent, err := strconv.Atoi(strings.TrimSuffix(spentWord, "+"))
	if err != nil || spent < 100000 {
		t.Errorf("budget= carries a spend of at least the budget, got %q:\n%s", budget, line)
	}

	jobDir := filepath.Join(slot, "jobs", "lbl")
	// EXACTLY ONE LAUNCH: a budget stop is terminal, and a stopped card is never relaunched.
	head, rows := usageRows(t, jobDir)
	if len(rows) != 1 {
		t.Fatalf("a stopped card is launched exactly once, and usage.tsv holds %d rows:\n%v", len(rows), rows)
	}
	if got := cell(t, head, rows[0], "end"); got != swarm.EndBudget {
		t.Errorf("the stopping launch's row carries end=%s, got %q", swarm.EndBudget, got)
	}
	// `rc` IS A DASH IN THE ROW (rule 12's closed list, decision 14): a native launch the
	// machinery ended has no exit code of its own to report.
	if got := cell(t, head, rows[0], "rc"); got != swarm.Dash {
		t.Errorf("the row's rc is a dash for a launch the machinery ended, got %q", got)
	}
	// THE ROW'S COLUMNS ARE THE DATABASE'S FINAL FIGURES, not the figures at the stop.
	if got := cell(t, head, rows[0], "tokens_in"); got != "60000" {
		t.Errorf("the row carries the final reported tokens_in 60000, got %q", got)
	}
	if got := cell(t, head, rows[0], "tokens_out"); got != "40000" {
		t.Errorf("the row carries the final reported tokens_out 40000, got %q", got)
	}

	// WHAT THE CARD PUBLISHED IS KEPT BYTE FOR BYTE.
	result, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		t.Fatalf("a card the budget stopped keeps the findings it published: %v", err)
	}
	if !strings.Contains(string(result), "findings: 2") {
		t.Errorf("the published report is the card's own, unchanged:\n%s", result)
	}
	// AND THE TOOL WROTE NOTHING INTO IT: on this route a PROMPT-DEFECT goes on stdout.
	if strings.Contains(string(result), "PROMPT-DEFECT") {
		t.Errorf("on the native route the tool writes nothing into a card's report:\n%s", result)
	}

	// NO PROCESS OF THE GROUP IS ALIVE, grandchildren included.
	bgRaw, err := os.ReadFile(filepath.Join(jobDir, "background.pid"))
	if err != nil {
		t.Fatalf("the harness recorded no background pid: %v", err)
	}
	bg, err := strconv.Atoi(strings.TrimSpace(string(bgRaw)))
	if err != nil {
		t.Fatalf("the background pid is a number: %q", bgRaw)
	}
	// The kill is a signal to a group and the kernel reaps at its own pace; the wait is
	// bounded and ends on the OBSERVABLE -- the pid going away -- never on a fixed sleep.
	gone := time.Now().Add(5 * time.Second)
	for time.Now().Before(gone) {
		if syscall.Kill(bg, 0) != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := syscall.Kill(bg, 0); err == nil {
		t.Errorf("after the stop no process of the card's group is alive; the grandchild pid %d survived", bg)
	}
}

// TestNativeBudgetFiresAtExactlyTheNumberAndNotOnCacheAlone: two halves of one sentence.
// `spent >= n`, so a sum exactly equal to the budget ends the card; and the sum is
// `tokens_in + tokens_out + reasoning`, so a `cache_read` that alone passes the number ends
// nothing.
func TestNativeBudgetFiresAtExactlyTheNumberAndNotOnCacheAlone(t *testing.T) {
	needsSQLite(t)
	for _, tc := range []struct {
		name       string
		directives string
		wantRC     int
		wantStop   string
	}{
		{
			name:       "exactly_the_budget",
			directives: "FAKE-USAGE-DB 600 400 0 0 0 0.1\nFAKE-SLEEP 20\n",
			wantRC:     1, wantStop: "tokens",
		},
		{
			name: "cache_read_alone_is_not_the_sum",
			// cache_read is ninety times the budget and the sum is 3: the card runs.
			directives: "FAKE-USAGE-DB 1 1 900000 90000 1 0.1\nFAKE-FINDINGS 1\n",
			wantRC:     0, wantStop: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, stdout, stderr := runBudgetCard(t, "1000", tc.directives, "--usage-interval", "1s")
			if rc != tc.wantRC {
				t.Fatalf("this card exits %d, got %d\nstdout:\n%s\nstderr:\n%s", tc.wantRC, rc, stdout, stderr)
			}
			if got := fieldOf(nativeOKLine(t, stdout), "stopped"); got != tc.wantStop {
				t.Fatalf("stopped= is %q, want %q:\n%s", got, tc.wantStop, stdout)
			}
		})
	}
}

// TestNativeTwoLaunchAccounting is rule 13d's own worked example, and the mandatory case of
// demanded test 13d. Under `--tokens 100`: a first launch that reports 40 and dies on a
// provider 5xx inside the launch grace, then a second that reports 70.
//
// THE ROW IS THE LAUNCH'S AND THE LINE IS THE JOB'S. The rows hold 40 and 70 -- never 40 and
// 110 -- because "a job's rows are disjoint, so that adding them counts each launch once"
// and downstream readers ADD them; the line holds 110/100, which is the job's cumulative sum
// and the thing the stop was tested against.
func TestNativeTwoLaunchAccounting(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	// The retry's own jitter is 5-20s; pinned here so this case is a test of the accounting
	// and not of a wait.
	t.Setenv("NOVA_SWARM_PROVIDER_BACKOFF", "1s")
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := filepath.Join(root, "card.md")
	// FAKE-LAUNCHES records the launch number, which is what picks the per-launch counts
	// out of FAKE-USAGE-DB's two sets; FAKE-5XX-FIRST fails the first launch fast, inside
	// the grace, which is the one failure rule 13d says is retried.
	body := "a card\nFAKE-LAUNCHES\n" +
		"FAKE-USAGE-DB 40 0 0 0 0 0.1 ; 70 0 0 0 0 0.2\n" +
		"FAKE-5XX-FIRST\nFAKE-SLEEP 20\n"
	if err := os.WriteFile(card, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(budgetNativeArgs(t, bin, card, slot, root, "100"), "--usage-interval", "1s")
	for i := range args {
		if args[i] == "--deadline" {
			args[i+1] = "120s"
		}
	}
	var stdout, stderr strings.Builder
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 1 {
		t.Fatalf("the card is stopped in its second launch and exits 1, got %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	line := nativeOKLine(t, stdout.String())
	if got := fieldOf(line, "budget"); got != "110/100" {
		t.Errorf("the LINE is the job's: two launches at 40 and 70 under --tokens 100 print budget=110/100, got %q:\n%s", got, line)
	}
	if got := fieldOf(line, "stopped"); got != "tokens" {
		t.Errorf("stopped=tokens, got %q:\n%s", got, line)
	}

	jobDir := filepath.Join(slot, "jobs", "lbl")
	head, rows := usageRows(t, jobDir)
	if len(rows) != 2 {
		t.Fatalf("the job had two launches, so usage.tsv holds two rows, got %d:\n%v", len(rows), rows)
	}
	// THE ROWS ARE 40 AND 70, NEVER 40 AND 110.
	if got := cell(t, head, rows[0], "tokens_in"); got != "40" {
		t.Errorf("the first launch's row holds its OWN 40, got %q", got)
	}
	if got := cell(t, head, rows[1], "tokens_in"); got != "70" {
		t.Errorf("the second launch's row holds its OWN 70 and never the job's 110, got %q", got)
	}
	// THE SECOND ROW ALONE HAS end=budget.
	if got := cell(t, head, rows[1], "end"); got != swarm.EndBudget {
		t.Errorf("the stopping launch's row carries end=budget, got %q", got)
	}
	if got := cell(t, head, rows[0], "end"); got == swarm.EndBudget {
		t.Errorf("the launch that died on a provider 5xx did not end on the budget, and its row says so; got end=%q", got)
	}
	// AND THE ROWS ADD TO THE LINE'S 110.
	a, _ := strconv.Atoi(cell(t, head, rows[0], "tokens_in"))
	b, _ := strconv.Atoi(cell(t, head, rows[1], "tokens_in"))
	if a+b != 110 {
		t.Errorf("the rows add to the line's 110, got %d + %d", a, b)
	}
}

// TestNativeALaunchThatReachedTheBudgetAloneIsNeverLaunchedAgain: rule 13d tests the stop
// "at every sample and once more before any relaunch". A first launch that spent the whole
// budget and then died on a provider 5xx buys no second launch.
func TestNativeALaunchThatReachedTheBudgetAloneIsNeverLaunchedAgain(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	t.Setenv("NOVA_SWARM_PROVIDER_BACKOFF", "1s")
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := filepath.Join(root, "card.md")
	// The FIRST launch spends the whole budget and then dies on a 5xx inside the grace.
	// A card that sleeps a moment first gives the sampler its reading before the death.
	body := "a card\nFAKE-LAUNCHES\nFAKE-USAGE-DB 200 0 0 0 0 0.1\nFAKE-SLEEP 3\nFAKE-5XX-FIRST\n"
	if err := os.WriteFile(card, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(budgetNativeArgs(t, bin, card, slot, root, "100"), "--usage-interval", "1s")
	for i := range args {
		if args[i] == "--deadline" {
			args[i+1] = "120s"
		}
	}
	var stdout, stderr strings.Builder
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 1 {
		t.Fatalf("the card is stopped and exits 1, got %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	jobDir := filepath.Join(slot, "jobs", "lbl")
	raw, err := os.ReadFile(filepath.Join(jobDir, "launches"))
	if err != nil {
		t.Fatalf("the harness recorded no launches: %v", err)
	}
	launches := len(strings.Split(strings.TrimRight(string(raw), "\n"), "\n"))
	if launches != 1 {
		t.Fatalf("a first launch that reached the budget alone is never launched again; the harness ran %d times:\n%s", launches, raw)
	}
	if got := fieldOf(nativeOKLine(t, stdout.String()), "stopped"); got != "tokens" {
		t.Errorf("stopped=tokens, got %q:\n%s", got, stdout.String())
	}
}

// TestNativeThreeFailedReadsEndTheCardUnverifiable, and two then an answer end nothing.
//
// A READ THAT FAILS IS NOT A SOURCE THAT REPORTED NOTHING: the first ends a card, because a
// numeric budget the tool has stopped being able to see is a budget the caller believes is
// enforced and is not; the second leaves the budget unable to fire and the deadline to end
// the job.
func TestNativeThreeFailedReadsEndTheCardUnverifiable(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	bin := nativeHarness(t)
	for _, tc := range []struct {
		name     string
		failures int // how many refusals the reader gives before it answers
		wantRC   int
		wantStop string
		wantEnd  string
	}{
		{"three_in_a_row_ends_it", 1000, 1, "unverifiable", swarm.EndUnverifiable},
		{"twice_then_an_answer_ends_nothing", 2, 0, "", swarm.EndDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			// A DATABASE MUST EXIST for a read to be attempted at all: an absent one is an
			// absence, and rule 13d keeps the two apart.
			db := filepath.Join(slot, "data", "opencode", "opencode.db")
			if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(db, []byte("a database\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			// A reader that refuses its first `failures` calls and answers after that,
			// counting in a file of its own so the count survives across processes.
			dir := t.TempDir()
			counter := filepath.Join(dir, "calls")
			script := "#!/bin/sh\n" +
				"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
				"n=$((n+1)); echo $n > " + counter + "\n" +
				"if [ \"$n\" -le " + strconv.Itoa(tc.failures) + " ]; then echo 'Error: file is not a database' >&2; exit 1; fi\n" +
				"exit 0\n"
			if err := os.WriteFile(filepath.Join(dir, swarm.SQLiteBinary), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			card := filepath.Join(root, "card.md")
			if err := os.WriteFile(card, []byte("a card\nFAKE-SLEEP 8\nFAKE-FINDINGS 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append(budgetNativeArgs(t, bin, card, slot, root, "100000"), "--usage-interval", "1s")
			for i := range args {
				if args[i] == "--deadline" {
					args[i+1] = "60s"
				}
			}
			var stdout, stderr strings.Builder
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != tc.wantRC {
				t.Fatalf("this card exits %d, got %d\nstdout:\n%s\nstderr:\n%s", tc.wantRC, rc, stdout.String(), stderr.String())
			}
			if got := fieldOf(nativeOKLine(t, stdout.String()), "stopped"); got != tc.wantStop {
				t.Errorf("stopped= is %q, want %q:\n%s", got, tc.wantStop, stdout.String())
			}
			head, rows := usageRows(t, filepath.Join(slot, "jobs", "lbl"))
			if got := cell(t, head, rows[len(rows)-1], "end"); got != tc.wantEnd {
				t.Errorf("the last row carries end=%s, got %q", tc.wantEnd, got)
			}
		})
	}
}

// TestNativeCardBudgetStopsAndPrintsThePromptDefect: rule 13b's budgets on this route,
// enforced by the same samples. The `PROMPT-DEFECT` line goes on native's own stdout AFTER
// `NATIVE OK` and is written into NO file, so a published report is kept byte for byte.
func TestNativeCardBudgetStopsAndPrintsThePromptDefect(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	t.Setenv("CAP_BUDGET_ENV", "a fake key")
	bin := nativeHarness(t)
	for _, tc := range []struct {
		name       string
		maxTurns   int
		maxCache   int
		directives string
		wantStop   string
	}{
		{
			name: "max_cache_read", maxCache: 1000,
			directives: "FAKE-USAGE-DB 1 1 0 900000 0 0.1\n",
			wantStop:   "max_cache_read",
		},
		{
			// The count rule 13b reads is the harness log's own assistant turns, or the
			// usage row count where the log has fewer. FAKE-TURNS prints four of them in
			// the harness's own voice onto the capture, against a max_turns of 2.
			name: "max_turns", maxTurns: 2,
			directives: "FAKE-TURNS 4\nFAKE-USAGE-DB 1 1 0 0 0 0.1\n",
			wantStop:   "max_turns",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			card := filepath.Join(root, "card.md")
			body := "a card\nFAKE-PUBLISH-FIRST\nFAKE-FINDINGS 3\n" + tc.directives + "FAKE-SLEEP 20\n"
			if err := os.WriteFile(card, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append(budgetNativeArgs(t, bin, card, slot, root, "unmetered"),
				"--worker", budgetWorker(t, swarm.UsageOpenCode, tc.maxTurns, tc.maxCache),
				"--usage-interval", "1s")
			for i := range args {
				if args[i] == "--deadline" {
					args[i+1] = "60s"
				}
			}
			var stdout, stderr strings.Builder
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			if rc != 1 {
				t.Fatalf("a card its own budget stopped exits 1, got %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
			}
			out := stdout.String()
			if got := fieldOf(nativeOKLine(t, out), "stopped"); got != tc.wantStop {
				t.Fatalf("stopped= is %q, want %q:\n%s", got, tc.wantStop, out)
			}
			// THE PROMPT-DEFECT LINE IS ON STDOUT, AFTER THE NATIVE OK LINE.
			lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
			okAt, defectAt := -1, -1
			for i, l := range lines {
				if strings.HasPrefix(l, "NATIVE OK ") {
					okAt = i
				}
				if strings.HasPrefix(l, "PROMPT-DEFECT ") {
					defectAt = i
				}
			}
			if defectAt < 0 {
				t.Fatalf("a card budget's stop prints the PROMPT-DEFECT line on native's stdout:\n%s", out)
			}
			if okAt < 0 || defectAt < okAt {
				t.Fatalf("the PROMPT-DEFECT line comes AFTER the NATIVE OK line:\n%s", out)
			}
			if !strings.Contains(lines[defectAt], "reason=budget") {
				t.Errorf("the PROMPT-DEFECT line is rule 13b's own:\n%s", lines[defectAt])
			}
			// AND IT IS IN NO FILE: the published report is kept byte for byte.
			jobDir := filepath.Join(slot, "jobs", "lbl")
			result, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
			if err != nil {
				t.Fatalf("the card's published report is kept: %v", err)
			}
			if strings.Contains(string(result), "PROMPT-DEFECT") {
				t.Errorf("on the native route the tool writes nothing into a card's report:\n%s", result)
			}
			if !strings.Contains(string(result), "findings: 3") {
				t.Errorf("the published report is unchanged by a byte:\n%s", result)
			}
			head, rows := usageRows(t, jobDir)
			if got := cell(t, head, rows[len(rows)-1], "end"); got != swarm.EndBudget {
				t.Errorf("a card budget's stop carries end=budget in the row, got %q", got)
			}
		})
	}
}
