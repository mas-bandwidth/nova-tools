package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// resultFixture writes a RESULT.md under a fresh directory and returns its path.
func resultFixture(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "RESULT.md")
	write(t, p, body)
	return p
}

// TestReadResultTakesTheHeadFromRevAndTheReportFromOneLine pins a whole
// RESULT.md: the `rev:` line is the head, the first non-empty line of the
// "## One line" section is the report, and the section ends at the next heading.
func TestReadResultTakesTheHeadFromRevAndTheReportFromOneLine(t *testing.T) {
	t.Parallel()
	p := resultFixture(t, "# Result\n\nrev: 0a1b2c3d\n\n## One line\n\n  landed the member loop  \nsecond line of it\n\n## Details\n\nnot this\n")
	head, _, report := readResult(p)
	if head != "0a1b2c3d" || report != "landed the member loop" {
		t.Fatalf("readResult = (%q, %q), want (0a1b2c3d, landed the member loop)", head, report)
	}
}

// TestReadResultOneLineHeadingIsCaseInsensitive pins the section's name.
func TestReadResultOneLineHeadingIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	_, _, report := readResult(resultFixture(t, "## ONE line\nshouted\n"))
	if report != "shouted" {
		t.Fatalf("report = %q, want shouted", report)
	}
}

// TestReadResultWithoutAOneLineUsesTheFirstProse pins the fallback: the
// first non-empty line that is not a heading and carries no colon (so the
// `rev:` line is never the report).
func TestReadResultWithoutAOneLineUsesTheFirstProse(t *testing.T) {
	t.Parallel()
	head, _, report := readResult(resultFixture(t, "# Result\n\nrev: cafe0123\nstatus: done\n\nDid the thing.\nAnd then more.\n"))
	if head != "cafe0123" {
		t.Fatalf("head = %q, want cafe0123", head)
	}
	if report != "Did the thing." {
		t.Fatalf("report = %q, want the first prose line", report)
	}
}

// TestReadResultRefusesARevThatIsNotASha pins that a rev with spaces (prose,
// not a sha), or longer than 64 bytes, is no head at all.
func TestReadResultRefusesARevThatIsNotASha(t *testing.T) {
	t.Parallel()
	for name, rev := range map[string]string{
		"spaces":   "abc def",
		"a tab":    "abc\tdef",
		"too long": strings.Repeat("a", 65),
	} {
		head, _, report := readResult(resultFixture(t, "rev: "+rev+"\n## One line\nthe line\n"))
		if head != "" {
			t.Errorf("%s: head = %q, want empty", name, head)
		}
		if report != "the line" {
			t.Errorf("%s: report = %q, the report is kept when the head is refused", name, report)
		}
	}
	if head, _, _ := readResult(resultFixture(t, "rev: "+strings.Repeat("b", 64)+"\n")); head != strings.Repeat("b", 64) {
		t.Errorf("a 64-byte rev is kept, got %q", head)
	}
}

// TestReadResultOfNothing pins the two ways there is no result: no path and a
// path that does not open.
func TestReadResultOfNothing(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", filepath.Join(t.TempDir(), "absent", "RESULT.md")} {
		if head, _, report := readResult(p); head != "" || report != "" {
			t.Errorf("readResult(%q) = (%q, %q), want empty", p, head, report)
		}
	}
}

// TestNewestResultPicksTheNewestOfTwo pins the choice across attempts, deep
// in the tree, and that only a file named RESULT.md counts.
func TestNewestResultPicksTheNewestOfTwo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old := filepath.Join(dir, "run-1", "1", "RESULT.md")
	recent := filepath.Join(dir, "run-2", "1", "RESULT.md")
	decoy := filepath.Join(dir, "run-3", "1", "RESULT.md.tmp")
	for _, p := range []string{old, recent, decoy} {
		write(t, p, "x\n")
	}
	now := time.Now()
	for p, at := range map[string]time.Time{old: now.Add(-time.Hour), recent: now.Add(-time.Minute), decoy: now} {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if got := newestResult(dir); got != recent {
		t.Fatalf("newestResult = %q, want %q", got, recent)
	}
	if got := newestResult(filepath.Join(dir, "absent")); got != "" {
		t.Fatalf("newestResult of a directory that is not there = %q, want empty", got)
	}
	if got := newestResult(t.TempDir()); got != "" {
		t.Fatalf("newestResult of an empty directory = %q, want empty", got)
	}
}

// TestNativeChildReadsHowItEnded pins Result: the verdict word, rc and
// harness word of the NATIVE line decide ok, and the report falls back to a
// sentence that names the log when the child published none.
func TestNativeChildReadsHowItEnded(t *testing.T) {
	t.Parallel()
	native := func(verdict string, rc int, harness string) string {
		rcs := map[int]string{0: "0", 1: "1"}[rc]
		return "NATIVE " + verdict + " label=c1 job=/j tmp=/t rc=" + rcs + " wall=1.00s sandbox=none card_sha256=x binary_sha256=y config=- harness=" + harness + " budget=unmetered\n"
	}
	done := make(chan struct{})
	close(done)
	for _, tc := range []struct {
		name, log, result string
		ok                bool
		head, report      string
	}{
		{"ok with a result", native("OK", 0, "ok"), "rev: abc\n## One line\nall good\n", true, "abc", "all good"},
		{"ok without a one-line report", native("OK", 0, "ok"), "", true, "", "finished; the child published no one-line report"},
		{"incomplete", native("INCOMPLETE", 0, "silent"), "", false, "", "the child ended without a result (see LOG)"},
		{"ok word with rc 1", native("OK", 1, "ok"), "## One line\nhalf\n", false, "", "half"},
		{"no NATIVE line", "the child died\n", "", false, "", "the child ended without a result (see LOG)"},
	} {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "c1.native.log")
		write(t, logPath, tc.log)
		results := filepath.Join(dir, "results", "c1")
		if tc.result != "" {
			write(t, filepath.Join(results, "run", "1", "RESULT.md"), tc.result)
		}
		c := &nativeChild{card: "c1", logPath: logPath, results: results, done: done}
		if !c.Done() {
			t.Fatalf("%s: a child whose done channel is closed is done", tc.name)
		}
		r := c.Result()
		wantReport := strings.ReplaceAll(tc.report, "LOG", logPath)
		if r.OK != tc.ok || r.Head != tc.head || r.Report != wantReport {
			t.Errorf("%s: Result = %+v, want ok=%t head=%q report=%q", tc.name, r, tc.ok, tc.head, wantReport)
		}
	}
}

// memberFull is every flag of `member` the verb requires, valid.
func memberFull(root string) []string {
	return []string{"member", "--as", "m1", "--width", "2", "--harness", "/bin/true", "--model", "p/m",
		"--root", root, "--tokens", "unmetered", "--deadline", "30s", "--once"}
}

// TestMemberWithNoFlagsRefusesAndNamesEachMissingOne pins refusing to guess:
// exit 2, nothing on stdout, and one line for each required flag (all of them
// in one run, not one a run).
func TestMemberWithNoFlagsRefusesAndNamesEachMissingOne(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run([]string{"member"}, strings.NewReader(""), &out, &errb, time.Now()); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout %q, want empty", out.String())
	}
	lines := strings.Split(strings.TrimSpace(errb.String()), "\n")
	if len(lines) != 7 {
		t.Fatalf("%d lines, want 7 (one per missing flag):\n%s", len(lines), errb.String())
	}
	for _, flag := range []string{"--as", "--width", "--harness", "--model", "--root", "--tokens", "--deadline"} {
		n := 0
		for _, l := range lines {
			if strings.HasPrefix(l, "nova-swarm member: "+flag+" is required") && strings.Contains(l, "refusing to guess") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s is named on %d lines, want 1:\n%s", flag, n, errb.String())
		}
	}
}

// TestMemberWithAWidthOfZeroRefuses pins that zero is not "unlimited".
func TestMemberWithAWidthOfZeroRefuses(t *testing.T) {
	t.Parallel()
	args := memberFull(t.TempDir())
	for i, a := range args {
		if a == "--width" {
			args[i+1] = "0"
		}
	}
	var out, errb bytes.Buffer
	if code := run(args, strings.NewReader(""), &out, &errb, time.Now()); code != 2 || !strings.Contains(errb.String(), "--width is required and is at least 1") {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
}

// TestMemberRefusesANameWithASlashBeforeItMakesAnything pins --as as one path
// element: a slash (or ..) is refused, and no slots or results directory is made.
func TestMemberRefusesANameWithASlashBeforeItMakesAnything(t *testing.T) {
	t.Parallel()
	for _, as := range []string{"a/b", "..", "-x", "a b"} {
		root := t.TempDir()
		args := memberFull(root)
		for i, a := range args {
			if a == "--as" {
				args[i+1] = as
			}
		}
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb, time.Now()); code != 2 {
			t.Errorf("--as %q: exit %d, want 2", as, code)
		}
		if !strings.Contains(errb.String(), "is not a name") {
			t.Errorf("--as %q: stderr %q does not say it is not a name", as, errb.String())
		}
		if out.Len() != 0 {
			t.Errorf("--as %q: stdout %q, want empty", as, out.String())
		}
		for _, d := range []string{"slots", "results"} {
			if _, err := os.Stat(filepath.Join(root, d)); !os.IsNotExist(err) {
				t.Errorf("--as %q made %s before refusing: %v", as, d, err)
			}
		}
	}
}

// TestExecSprintReturnsTheVerbsExitAndBody pins the seam to nova-sprint: the
// actor rides in the environment, stdout is the body, a failing verb with no
// stdout gives its stderr, and a binary that will not start is exit 2.
func TestExecSprintReturnsTheVerbsExitAndBody(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)
	bin := filepath.Join(t.TempDir(), "sprint")
	script := "#!/bin/sh\ncase \"$1\" in\nok) echo \"actor=$NOVA_SPRINT_ACTOR args=$*\";;\nrefuse) echo \"refused: $2\" >&2; exit 1;;\nmixed) echo body; echo noise >&2; exit 1;;\nesac\n"
	if err := testbin.WriteExecutable(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &execSprint{bin: bin, actor: "m1"}
	if code, out := s.Run("ok", "--json"); code != 0 || strings.TrimSpace(string(out)) != "actor=m1 args=ok --json" {
		t.Fatalf("ok: (%d, %q)", code, out)
	}
	if code, out := s.Run("refuse", "why"); code != 1 || strings.TrimSpace(string(out)) != "refused: why" {
		t.Fatalf("refuse: (%d, %q)", code, out)
	}
	if code, out := s.Run("mixed"); code != 1 || strings.TrimSpace(string(out)) != "body" {
		t.Fatalf("mixed: (%d, %q), want the stdout body", code, out)
	}
	gone := &execSprint{bin: filepath.Join(t.TempDir(), "absent"), actor: "m1"}
	if code, _ := gone.Run("queue"); code != 2 {
		t.Fatalf("a binary that will not start: exit %d, want 2 (the store did not answer)", code)
	}
}

// TestMemberRefusesAModelAndATokenBudgetThatEveryCardWouldRefuse pins that
// the verb reads --model and --tokens once, at the start, so a typo is one
// refusal and not one failed card for each card it would run.
func TestMemberRefusesAModelAndATokenBudgetThatEveryCardWouldRefuse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ flag, value, want string }{
		{"--model", "nomodel", "is not provider/model"},
		{"--tokens", "50oops", "--tokens"},
	} {
		root := t.TempDir()
		args := memberFull(root)
		for i, a := range args {
			if a == tc.flag {
				args[i+1] = tc.value
			}
		}
		var out, errb bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errb, time.Now()); code != 2 || !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%s %s: exit %d, stderr %q, want exit 2 naming %q", tc.flag, tc.value, code, errb.String(), tc.want)
		}
		if _, err := os.Stat(filepath.Join(root, "slots")); !os.IsNotExist(err) {
			t.Errorf("%s %s: a directory was made before the refusal: %v", tc.flag, tc.value, err)
		}
	}
}
