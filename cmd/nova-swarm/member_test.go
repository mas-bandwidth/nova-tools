package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
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

// fullSha and pushedSha are two commits a result or the git shim names.
const (
	fullSha   = "0123456789abcdef0123456789abcdef01234567"
	pushedSha = "89abcdef0123456789abcdef0123456789abcdef"
)

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
		pushed            string // what the git shim recorded in the job
	}{
		{"ok with a result", native("OK", 0, "ok"), "rev: abc\n## One line\nall good\n", true, "abc", "all good", ""},
		{"ok without a one-line report", native("OK", 0, "ok"), "", true, "", "finished; the child published no one-line report", ""},
		{"incomplete", native("INCOMPLETE", 0, "silent"), "", false, "", "the child ended without a result (see LOG)", ""},
		{"ok word with rc 1", native("OK", 1, "ok"), "## One line\nhalf\n", false, "", "half", ""},
		{"no NATIVE line", "the child died\n", "", false, "", "the child ended without a result (see LOG)", ""},
		{"the contract's shape", native("OK", 0, "ok"), "head: " + fullSha + "\nbranch: b\nverdict: ok\ngate: -\noutput: -\nreport: shaped\ntitle: T\n\n## Body\n\nB\n", true, fullSha, "shaped", ""},
		{"a shape naming no head, a push recorded", native("OK", 0, "ok"), "head: -\nbranch: b\nverdict: not-done\ngate: -\noutput: -\nreport: stuck\n", true, pushedSha, "stuck", "sprint/c1\t" + pushedSha + "\t/j/repo\n"},
	} {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "c1.native.log")
		write(t, logPath, tc.log)
		results := filepath.Join(dir, "results", "c1")
		if tc.result != "" {
			write(t, filepath.Join(results, "run", "1", "RESULT.md"), tc.result)
		}
		job := filepath.Join(dir, "job")
		write(t, filepath.Join(job, cardcontract.PushedName), tc.pushed)
		c := &nativeChild{card: "c1", logPath: logPath, results: results, job: job, done: done}
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

// TestLaunchNameIsTheCardAtItsGenerationInItsEpoch pins the name of one
// launch, which names its slot, results root, log and pid file: a work card
// is its id at its generation in its epoch, a read its id at its attempt in
// its epoch, and a card dealt again is another launch.
func TestLaunchNameIsTheCardAtItsGenerationInItsEpoch(t *testing.T) {
	t.Parallel()
	// Removing the `.g` + Gen part of the work name in launchName, or the
	// `.a` + Attempt part of the read name, makes this fail.
	if got := launchName(member.Packet{Card: "c", Kind: "work", Gen: 2, Attempt: 1, Epoch: 7}); got != "c.g2.e7" {
		t.Errorf("work launch name = %q, want c.g2.e7", got)
	}
	if got := launchName(member.Packet{Card: "r", Kind: "read", Gen: 0, Attempt: 1, Epoch: 7}); got != "r.a1.e7" {
		t.Errorf("read launch name = %q, want r.a1.e7", got)
	}
	if a, b := launchName(member.Packet{Card: "c", Kind: "work", Gen: 1, Epoch: 7}), launchName(member.Packet{Card: "c", Kind: "work", Gen: 2, Epoch: 7}); a == b {
		t.Errorf("one card at two generations is one launch name %q", a)
	}
	if a, b := launchName(member.Packet{Card: "c", Kind: "work", Gen: 1, Epoch: 7}), launchName(member.Packet{Card: "c", Kind: "work", Gen: 1, Epoch: 8}); a == b {
		t.Errorf("one card at one generation in two epochs is one launch name %q", a)
	}
	if a, b := launchName(member.Packet{Card: "r", Kind: "read", Attempt: 1, Epoch: 7}), launchName(member.Packet{Card: "r", Kind: "read", Attempt: 2, Epoch: 7}); a == b {
		t.Errorf("one read at two attempts is one launch name %q", a)
	}
}

// TestReadResultReadsTheVerdictLine pins a read's verdict: the `verdict:`
// line of RESULT.md, lower-cased, the first one kept, and "" when absent.
func TestReadResultReadsTheVerdictLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, want string }{
		{"ok", "rev: abc\nverdict: ok\n## One line\nclean\n", "ok"},
		{"broken", "verdict: broken\n## One line\nthe merge is wrong\n", "broken"},
		{"shouted is lower-cased", "verdict: BROKEN\n## One line\nwrong\n", "broken"},
		{"spaces are trimmed", "  verdict:   Ok  \n", "ok"},
		{"the first line wins", "verdict: ok\nverdict: broken\n", "ok"},
		{"absent", "rev: abc\n## One line\nclean\n", ""},
	} {
		// Removing the strings.ToLower in readResult makes "shouted" fail;
		// removing the `verdict:` branch makes every case but "absent" fail.
		_, verdict, _ := readResult(resultFixture(t, tc.body))
		if verdict != tc.want {
			t.Errorf("%s: verdict = %q, want %q", tc.name, verdict, tc.want)
		}
	}
	if _, verdict, _ := readResult(""); verdict != "" {
		t.Errorf("no result file: verdict = %q, want empty", verdict)
	}
}

// markerRunner is a nativeRunner whose own executable is a script that writes
// a marker file when it is run: a fresh start leaves the marker, an adoption
// never runs it. The slots and results sit under dir.
func markerRunner(t *testing.T) (r *nativeRunner, slots, marker string) {
	t.Helper()
	windowsIsNotABench(t)
	dir := t.TempDir()
	marker = filepath.Join(dir, "marker")
	self := filepath.Join(dir, "self.sh")
	if err := testbin.WriteExecutable(self, []byte("#!/bin/sh\necho started > '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	slots = filepath.Join(dir, "slots")
	if err := os.MkdirAll(slots, 0o755); err != nil {
		t.Fatal(err)
	}
	r = &nativeRunner{
		self: self, sprintBin: "nova-sprint", harness: "/bin/true", model: "p/m", root: dir, slots: slots,
		resultsRoot: filepath.Join(dir, "results"), deadline: 30 * time.Second, tokens: "unmetered", stderr: &bytes.Buffer{},
	}
	return r, slots, marker
}

// TestALiveChildIsAdoptedNotRunTwice pins the restart path: a launch whose
// pid file names a live process is adopted, not started again. The pid file
// here names this test's own process; Start starts nothing (no marker, no slot
// or card file made, the pid file untouched) and the child it returns is not
// done while that pid is alive.
func TestALiveChildIsAdoptedNotRunTwice(t *testing.T) {
	t.Parallel()
	r, slots, marker := markerRunner(t)
	p := member.Packet{Card: "c1", Kind: "work", Gen: 2, Attempt: 1, Epoch: 7, Branch: "work/c1"}
	name := launchName(p)
	pidPath := filepath.Join(slots, name+".pid")
	self := strconv.Itoa(os.Getpid()) + "\n"
	write(t, pidPath, self)
	// Removing the `if pid := livePID(pidPath); pid > 0 {` adoption branch in
	// nativeRunner.Start makes this fail: a second child is started over the
	// first (the slot and card file appear, the pid file is overwritten).
	ch, err := r.Start(p)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Done() {
		t.Fatal("the adopted child is done while its pid is alive")
	}
	if b, _ := os.ReadFile(pidPath); string(b) != self {
		t.Fatalf("the pid file was rewritten: %q", b)
	}
	for _, name := range []string{name, name + ".card.md", name + ".native.log"} {
		if _, err := os.Stat(filepath.Join(slots, name)); !os.IsNotExist(err) {
			t.Errorf("%s exists: a launch was begun over a live one (%v)", name, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the harness path ran: %v", err)
	}
}

// TestADeadPidFileIsIgnored pins the other half: a pid file that names a
// process that is gone (one started and waited for) is stale, and the launch
// starts fresh: the script runs, and the pid file is replaced then removed
// when the child ends.
func TestADeadPidFileIsIgnored(t *testing.T) {
	t.Parallel()
	r, slots, marker := markerRunner(t)
	gone := exec.Command("/bin/sh", "-c", "exit 0")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	if processAlive(gone.Process.Pid) {
		t.Skip("the pid of the finished process was reused")
	}
	p := member.Packet{Card: "c1", Kind: "work", Gen: 1, Attempt: 1, Epoch: 7, Branch: "work/c1"}
	pidPath := filepath.Join(slots, launchName(p)+".pid")
	write(t, pidPath, strconv.Itoa(gone.Process.Pid)+"\n")
	// Removing the `!processAlive(pid)` test in livePID makes this fail: the
	// dead pid is adopted, and nothing is ever started.
	ch, err := r.Start(p)
	if err != nil {
		t.Fatal(err)
	}
	// the child ends by itself (the script is one echo; an adopted dead pid
	// closes the channel at once), so no timer is needed here
	<-ch.(*nativeChild).done
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a dead pid file led to no fresh start: %v", err)
	}
}

// TestEveryIsBoundedUnderTheBeatDeadline pins --every at 5s, under the beat's
// 15s deadline in the fleet: 6s is refused with exit 2 naming 5s and nothing
// is made; 5s is accepted. The sprint binary is a path that does not exist,
// so a run that gets past the refusal asks no store anything.
func TestEveryIsBoundedUnderTheBeatDeadline(t *testing.T) {
	t.Parallel()
	with := func(root, every string) []string {
		return append(memberFull(root), "--every", every, "--sprint", filepath.Join(root, "absent-sprint"))
	}
	root := t.TempDir()
	var out, errb bytes.Buffer
	// Removing the `every.d > 5*time.Second` bound in cmdMember makes this fail.
	if code := run(with(root, "6s"), strings.NewReader(""), &out, &errb, time.Now()); code != 2 || !strings.Contains(errb.String(), "5s") {
		t.Fatalf("--every 6s: exit %d, stderr %q, want exit 2 naming 5s", code, errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("--every 6s: stdout %q, want empty", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "slots")); !os.IsNotExist(err) {
		t.Fatalf("--every 6s made the slots before refusing: %v", err)
	}
	out.Reset()
	errb.Reset()
	if code := run(with(t.TempDir(), "5s"), strings.NewReader(""), &out, &errb, time.Now()); code != 0 {
		t.Fatalf("--every 5s: exit %d, stderr %q, want it accepted", code, errb.String())
	}
}
