package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
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
	require.Equal(t, "0a1b2c3d", head, "readResult = (%q, %q), want (0a1b2c3d, landed the member loop)", head, report)
	require.Equal(t, "landed the member loop", report, "readResult = (%q, %q), want (0a1b2c3d, landed the member loop)", head, report)
}

// TestReadResultOneLineHeadingIsCaseInsensitive pins the section's name.
func TestReadResultOneLineHeadingIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	_, _, report := readResult(resultFixture(t, "## ONE line\nshouted\n"))
	require.Equal(t, "shouted", report, "report = %q, want shouted", report)
}

// TestReadResultWithoutAOneLineUsesTheFirstProse pins the fallback: the
// first non-empty line that is not a heading and carries no colon (so the
// `rev:` line is never the report).
func TestReadResultWithoutAOneLineUsesTheFirstProse(t *testing.T) {
	t.Parallel()
	head, _, report := readResult(resultFixture(t, "# Result\n\nrev: cafe0123\nstatus: done\n\nDid the thing.\nAnd then more.\n"))
	require.Equal(t, "cafe0123", head, "head = %q, want cafe0123", head)
	require.Equal(t, "Did the thing.", report, "report = %q, want the first prose line", report)
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
		assert.Empty(t, head, "%s: head = %q, want empty", name, head)
		assert.Equal(t, "the line", report, "%s: report = %q, the report is kept when the head is refused", name, report)
	}
	head, _, _ := readResult(resultFixture(t, "rev: "+strings.Repeat("b", 64)+"\n"))
	assert.Equal(t, strings.Repeat("b", 64), head, "a 64-byte rev is kept, got %q", head)
}

// TestReadResultOfNothing pins the two ways there is no result: no path and a
// path that does not open.
func TestReadResultOfNothing(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", filepath.Join(t.TempDir(), "absent", "RESULT.md")} {
		head, _, report := readResult(p)
		assert.Equal(t, "", head, "readResult(%q) = (%q, %q), want empty", p, head, report)
		assert.Equal(t, "", report, "readResult(%q) = (%q, %q), want empty", p, head, report)
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
		require.NoError(t, os.Chtimes(p, at, at))
	}
	got := newestResult(dir)
	require.Equal(t, recent, got, "newestResult = %q, want %q", got, recent)
	got = newestResult(filepath.Join(dir, "absent"))
	require.Empty(t, got, "newestResult of a directory that is not there = %q, want empty", got)
	got = newestResult(t.TempDir())
	require.Empty(t, got, "newestResult of an empty directory = %q, want empty", got)
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
		finish            string // what the gh shim recorded in the job
	}{
		{"ok with a result", native("OK", 0, "ok"), "rev: abc\n## One line\nall good\n", true, "abc", "all good", "", ""},
		{"ok without a one-line report", native("OK", 0, "ok"), "", true, "", "finished; the child published no one-line report", "", ""},
		{"incomplete", native("INCOMPLETE", 0, "silent"), "", false, "", "the child ended without a result (see LOG)", "", ""},
		{"ok word with rc 1", native("OK", 1, "ok"), "## One line\nhalf\n", false, "", "half", "", ""},
		{"no NATIVE line", "the child died\n", "", false, "", "the child ended without a result (see LOG)", "", ""},
		{"the contract's shape", native("OK", 0, "ok"), "head: " + fullSha + "\nbranch: b\nverdict: ok\ngate: -\noutput: -\nreport: shaped\ntitle: T\n\n## Body\n\nB\n", true, fullSha, "shaped", "", ""},
		{"the shim's finish, the child's own RESULT.md riding in its body", native("OK", 0, "ok"), "RESULT: c1 sha=0123\n## One line\ngate green\n", true, fullSha, "The change", "", "head: " + fullSha + "\nbranch: sprint/c1\nverdict: ok\ngate: -\noutput: -\nreport: The change\ntitle: The change\n\n## Body\n\nthe body\n"},
		{"a shape naming no head, a push recorded", native("OK", 0, "ok"), "head: -\nbranch: b\nverdict: not-done\ngate: -\noutput: -\nreport: stuck\n", true, pushedSha, "stuck", "sprint/c1\t" + pushedSha + "\t/j/repo\n", ""},
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
		write(t, filepath.Join(job, cardcontract.FinishName), tc.finish)
		c := &nativeChild{card: "c1", logPath: logPath, results: results, job: job, done: done}
		require.True(t, c.Done(), "%s: a child whose done channel is closed is done", tc.name)
		r := c.Result()
		wantReport := strings.ReplaceAll(tc.report, "LOG", logPath)
		if tc.finish != "" {
			assert.Equal(t, "the body\n\n## RESULT.md\n\nRESULT: c1 sha=0123\n## One line\ngate green", r.Body, "the child's RESULT.md rides in the body")
		}
		assert.Equal(t, tc.ok, r.OK, "%s: Result = %+v, want ok=%t head=%q report=%q", tc.name, r, tc.ok, tc.head, wantReport)
		assert.Equal(t, tc.head, r.Head, "%s: Result = %+v, want ok=%t head=%q report=%q", tc.name, r, tc.ok, tc.head, wantReport)
		assert.Equal(t, wantReport, r.Report, "%s: Result = %+v, want ok=%t head=%q report=%q", tc.name, r, tc.ok, tc.head, wantReport)
	}
}

// memberFull is every flag of `member` the verb requires, valid.
func memberFull(root string) []string {
	return []string{"member", "--as", "m1", "--server", "sprint.test:6390", "--width", "2", "--harness", "/bin/true", "--model", "p/m",
		"--root", root, "--tokens", "unmetered", "--deadline", "30s", "--once"}
}

// noServer is a sprint server that never answers, handed to cmdMember as its send: a
// member run in a test sends no verb anywhere (no socket), and each verb of its tick
// fails as it does when the server is down.
func noServer(context.Context, ...[]string) ([]sprintwire.Result, error) {
	return nil, errors.New("no sprint server in this test")
}

// TestMemberWithNoFlagsRefusesAndNamesEachMissingOne pins refusing to guess:
// exit 2, nothing on stdout, and one line for each required flag (all of them
// in one run, not one a run). The width, the model, the budget and the deadline
// are not among them: the fleet row names the width and the card's route the
// rest; the flags are an override. --server is among them, naming the server's own
// verb: a member has no sprint but the server.
func TestMemberWithNoFlagsRefusesAndNamesEachMissingOne(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := run([]string{"member"}, strings.NewReader(""), &out, &errb, time.Now())
	require.Equal(t, 2, code, "exit %d, want 2", code)
	require.Zero(t, out.Len(), "stdout %q, want empty", out.String())
	lines := strings.Split(strings.TrimSpace(errb.String()), "\n")
	require.Len(t, lines, 4, "%d lines, want 4 (one per missing flag):\n%s", len(lines), errb.String())
	assert.Contains(t, errb.String(), "nova-swarm member: --server is required; it wants the sprint server's host:port, the run loop started with nova-sprint run --listen")
	for _, flag := range []string{"--as", "--server", "--harness", "--root"} {
		n := 0
		for _, l := range lines {
			if strings.HasPrefix(l, "nova-swarm member: "+flag+" is required") && strings.Contains(l, "refusing to guess") {
				n++
			}
		}
		assert.Equal(t, 1, n, "%s is named on %d lines, want 1:\n%s", flag, n, errb.String())
	}
}

// TestMemberWithAWidthOfZeroRefuses pins that zero is not "unlimited": a reader
// names its width and is refused 0; a member's --width is an override of its
// fleet row's and is refused below 0.
func TestMemberWithAWidthOfZeroRefuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		width string
		extra []string
		want  string
	}{
		{"0", []string{"--reader"}, "--width is required and is at least 1"},
		{"-1", nil, "--width is an override of the fleet row's width and is at least 1"},
	} {
		args := memberFull(t.TempDir())
		for i, a := range args {
			if a == "--width" {
				args[i+1] = c.width
			}
		}
		args = append(args, c.extra...)
		var out, errb bytes.Buffer
		code := run(args, strings.NewReader(""), &out, &errb, time.Now())
		require.Equal(t, 2, code, "--width %s %v: exit %d, stderr %q", c.width, c.extra, code, errb.String())
		require.Contains(t, errb.String(), c.want, "--width %s %v: exit %d, stderr %q", c.width, c.extra, code, errb.String())
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
		code := run(args, strings.NewReader(""), &out, &errb, time.Now())
		assert.Equal(t, 2, code, "--as %q: exit %d, want 2", as, code)
		assert.Contains(t, errb.String(), "is not a name", "--as %q: stderr %q does not say it is not a name", as, errb.String())
		assert.Zero(t, out.Len(), "--as %q: stdout %q, want empty", as, out.String())
		for _, d := range []string{"slots", "results"} {
			_, err := os.Stat(filepath.Join(root, d))
			assert.True(t, os.IsNotExist(err), "--as %q made %s before refusing: %v", as, d, err)
		}
	}
}

// A verb the server ran and that did not exit 0 reaches the member's log as one bounded
// line: stderr first, then any stdout receipt, secret-shaped values redacted, each half
// keeping room for the other (sprintFailureOutput, the member's sprintwire.Worker Failed);
// a verb that succeeded hands its stdout back whole.
func TestAFailedVerbsWordsAreOneRedactedBoundedLine(t *testing.T) {
	t.Parallel()
	secret := "sk-" + strings.Repeat("Ab1", 8)
	answers := map[string]sprintwire.Result{
		"ok":      {Stdout: "body\n"},
		"refuse":  {Code: 1, Stderr: "refused: why\n"},
		"mixed":   {Code: 1, Stdout: "body\n", Stderr: "noise\n"},
		"secret":  {Code: 2, Stdout: "receipt\n", Stderr: "token=" + secret + "\n"},
		"large":   {Code: 2, Stdout: strings.Repeat("0", 600) + "\n", Stderr: "actual failure\n"},
		"control": {Code: 2, Stdout: "receipt\nsecond\tline\n", Stderr: "store\nfailed\tbad\n"},
	}
	w := &sprintwire.Worker{Failed: sprintFailureOutput, Send: func(_ context.Context, verbs ...[]string) ([]sprintwire.Result, error) {
		return []sprintwire.Result{answers[verbs[0][0]]}, nil
	}}
	code, out := w.Run("ok")
	require.Equal(t, 0, code)
	require.Equal(t, "body\n", string(out))
	code, out = w.Run("refuse")
	require.Equal(t, 1, code)
	require.Equal(t, "refused: why", string(out))
	for _, c := range []struct {
		name, want string
		code       int
		contains   []string
		excludes   []string
		bounded    bool
	}{
		{name: "mixed", code: 1, want: "stderr: noise; stdout: body"},
		{name: "secret", code: 2, contains: []string{"stderr: token=[redacted]; stdout: receipt"}, excludes: []string{secret}},
		{name: "large", code: 2, contains: []string{"stderr: actual failure", "+"}, bounded: true},
		{name: "control", code: 2, want: `stderr: store\x0afailed\x09bad; stdout: receipt\x0asecond\x09line`, excludes: []string{"\n", "\t"}, bounded: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			code, out := w.Run(c.name)
			assert.Equal(t, c.code, code, c.name)
			if c.want != "" {
				assert.Equal(t, c.want, string(out), c.name)
			}
			for _, want := range c.contains {
				assert.Contains(t, string(out), want, c.name)
			}
			for _, unwanted := range c.excludes {
				assert.NotContains(t, string(out), unwanted, c.name)
			}
			if c.bounded {
				assert.LessOrEqual(t, len(out), oneline.TailBytes, c.name)
			}
		})
	}
	controlHeavy := sprintFailureOutput([]byte(strings.Repeat("a\n", oneline.TailBytes)), []byte("actual failure"))
	assert.LessOrEqual(t, len(controlHeavy), oneline.TailBytes, "escaping many controls stays inside the final bound")
	assert.Contains(t, string(controlHeavy), "stderr: actual failure", "bounding stdout keeps the failure")
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
		code := run(args, strings.NewReader(""), &out, &errb, time.Now())
		assert.Equal(t, 2, code, "%s %s: exit %d, stderr %q, want exit 2 naming %q", tc.flag, tc.value, code, errb.String(), tc.want)
		assert.Contains(t, errb.String(), tc.want, "%s %s: exit %d, stderr %q, want exit 2 naming %q", tc.flag, tc.value, code, errb.String(), tc.want)
		_, err := os.Stat(filepath.Join(root, "slots"))
		assert.True(t, os.IsNotExist(err), "%s %s: a directory was made before the refusal: %v", tc.flag, tc.value, err)
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
	got := launchName(member.Packet{Card: "c", Kind: "work", Gen: 2, Attempt: 1, Epoch: 7})
	assert.Equal(t, "c.g2.e7", got, "work launch name = %q, want c.g2.e7", got)
	got = launchName(member.Packet{Card: "r", Kind: "read", Gen: 0, Attempt: 1, Epoch: 7})
	assert.Equal(t, "r.a1.e7", got, "read launch name = %q, want r.a1.e7", got)
	a, b := launchName(member.Packet{Card: "c", Kind: "work", Gen: 1, Epoch: 7}), launchName(member.Packet{Card: "c", Kind: "work", Gen: 2, Epoch: 7})
	assert.NotEqual(t, a, b, "one card at two generations is one launch name %q", a)
	a, b = launchName(member.Packet{Card: "c", Kind: "work", Gen: 1, Epoch: 7}), launchName(member.Packet{Card: "c", Kind: "work", Gen: 1, Epoch: 8})
	assert.NotEqual(t, a, b, "one card at one generation in two epochs is one launch name %q", a)
	a, b = launchName(member.Packet{Card: "r", Kind: "read", Attempt: 1, Epoch: 7}), launchName(member.Packet{Card: "r", Kind: "read", Attempt: 2, Epoch: 7})
	assert.NotEqual(t, a, b, "one read at two attempts is one launch name %q", a)
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
		assert.Equal(t, tc.want, verdict, "%s: verdict = %q, want %q", tc.name, verdict, tc.want)
	}
	_, verdict, _ := readResult("")
	assert.Empty(t, verdict, "no result file: verdict = %q, want empty", verdict)
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
	require.NoError(t, testbin.WriteExecutable(self, []byte("#!/bin/sh\necho started > '"+marker+"'\n"), 0o755))
	slots = filepath.Join(dir, "slots")
	require.NoError(t, os.MkdirAll(slots, 0o755))
	r = &nativeRunner{
		self: self, harness: "/bin/true", model: "p/m", root: dir, slots: slots,
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
	require.NoError(t, err)
	require.False(t, ch.Done(), "the adopted child is done while its pid is alive")
	b, _ := os.ReadFile(pidPath)
	require.Equal(t, self, string(b), "the pid file was rewritten: %q", b)
	for _, name := range []string{name, name + ".card.md", name + ".native.log"} {
		_, err := os.Stat(filepath.Join(slots, name))
		assert.True(t, os.IsNotExist(err), "%s exists: a launch was begun over a live one (%v)", name, err)
	}
	_, err = os.Stat(marker)
	require.True(t, os.IsNotExist(err), "the harness path ran: %v", err)
}

// TestADeadPidFileIsIgnored pins the other half: a pid file that names a
// process that is gone (one started and waited for) is stale, and the launch
// starts fresh: the script runs, and the pid file is replaced then removed
// when the child ends.
func TestADeadPidFileIsIgnored(t *testing.T) {
	t.Parallel()
	r, slots, marker := markerRunner(t)
	gone := exec.Command("/bin/sh", "-c", "exit 0")
	require.NoError(t, gone.Run())
	if processAlive(gone.Process.Pid) {
		t.Skip("the pid of the finished process was reused")
	}
	p := member.Packet{Card: "c1", Kind: "work", Gen: 1, Attempt: 1, Epoch: 7, Branch: "work/c1"}
	pidPath := filepath.Join(slots, launchName(p)+".pid")
	write(t, pidPath, strconv.Itoa(gone.Process.Pid)+"\n")
	// Removing the `!processAlive(pid)` test in livePID makes this fail: the
	// dead pid is adopted, and nothing is ever started.
	ch, err := r.Start(p)
	require.NoError(t, err)
	// the child ends by itself (the script is one echo; an adopted dead pid
	// closes the channel at once), so no timer is needed here
	<-ch.(*nativeChild).done
	_, err = os.Stat(marker)
	require.NoError(t, err, "a dead pid file led to no fresh start")
}

// TestEveryIsBoundedUnderTheBeatDeadline pins --every at 5s, under the beat's
// 15s deadline in the fleet: 6s is refused with exit 2 naming 5s and nothing
// is made; 5s is accepted. Its sprint server never answers (noServer), so a run
// that gets past the refusal sends nothing anywhere.
func TestEveryIsBoundedUnderTheBeatDeadline(t *testing.T) {
	t.Parallel()
	with := func(root, every string) []string {
		return append(memberFull(root), "--every", every)
	}
	root := t.TempDir()
	var out, errb bytes.Buffer
	// Removing the `every.d > 5*time.Second` bound in cmdMember makes this fail.
	code := run(with(root, "6s"), strings.NewReader(""), &out, &errb, time.Now())
	require.Equal(t, 2, code, "--every 6s: exit %d, stderr %q, want exit 2 naming 5s", code, errb.String())
	require.Contains(t, errb.String(), "5s", "--every 6s: exit %d, stderr %q, want exit 2 naming 5s", code, errb.String())
	require.Zero(t, out.Len(), "--every 6s: stdout %q, want empty", out.String())
	_, err := os.Stat(filepath.Join(root, "slots"))
	require.True(t, os.IsNotExist(err), "--every 6s made the slots before refusing: %v", err)
	out.Reset()
	errb.Reset()
	code = cmdMember(with(t.TempDir(), "5s")[1:], &out, &errb, noServer)
	require.Equal(t, 0, code, "--every 5s: exit %d, stderr %q, want it accepted", code, errb.String())
}

// memberWithoutOnce returns the required member flags without --once.
func memberWithoutOnce(root string) []string {
	var args []string
	for _, a := range memberFull(root) {
		if a != "--once" {
			args = append(args, a)
		}
	}
	return args
}

// TestMemberRefusesCombiningOnceWithTicks pins the mutual exclusion between
// --once and --ticks: specifying both is refused with exit 2 before anything
// is made or asked.
func TestMemberRefusesCombiningOnceWithTicks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	args := append(memberFull(root), "--ticks", "3")
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errb, time.Now())
	require.Equal(t, 2, code, "stderr %q", errb.String())
	require.Contains(t, errb.String(), "give --once or --ticks <n>, not both")
	require.Empty(t, out.String())
	_, err := os.Stat(filepath.Join(root, "slots"))
	require.True(t, os.IsNotExist(err), "directories made before refusal: %v", err)
}

// TestMemberRefusesZeroOrNegativeTicks pins that --ticks requires a positive count:
// 0, -1, and negative values are refused with exit 2 and not read as unbounded.
func TestMemberRefusesZeroOrNegativeTicks(t *testing.T) {
	t.Parallel()
	for _, val := range []string{"0", "-1", "-5"} {
		t.Run(val, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			args := append(memberWithoutOnce(root), "--ticks", val)
			var out, errb bytes.Buffer
			code := run(args, strings.NewReader(""), &out, &errb, time.Now())
			require.Equal(t, 2, code, "stderr %q", errb.String())
			require.Contains(t, errb.String(), "give --ticks 1 or more, or leave it out to run until stopped")
			require.Empty(t, out.String())
			_, err := os.Stat(filepath.Join(root, "slots"))
			require.True(t, os.IsNotExist(err), "directories made before refusal: %v", err)
		})
	}
}

// TestMemberRefusesBothOnceAndNonPositiveTicksNamesBothPins pins collecting all
// problems on one run: combining --once with --ticks 0 reports both faults.
func TestMemberRefusesBothOnceAndNonPositiveTicksNamesBothPins(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	args := append(memberFull(root), "--ticks", "0")
	var out, errb bytes.Buffer
	require.Equal(t, 2, run(args, strings.NewReader(""), &out, &errb, time.Now()))
	require.Contains(t, errb.String(), "give --once or --ticks <n>, not both")
	require.Contains(t, errb.String(), "give --ticks 1 or more, or leave it out to run until stopped")
}

// TestMemberAcceptsPositiveTicks pins that valid positive --ticks runs for the
// specified tick count and terminates cleanly.
func TestMemberAcceptsPositiveTicks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	args := append(memberWithoutOnce(root), "--ticks", "2", "--every", "1ms")
	var out, errb bytes.Buffer
	require.Equal(t, 0, cmdMember(args[1:], &out, &errb, noServer), "stderr %q", errb.String())
	require.Contains(t, out.String(), "MEMBER OK as=m1 ticks=2 running=0")
}

// A card's native child starts with an allowlist environment
// (docs/SPEC-CARD-CONTRACT.md): what native, git and the harness need, the
// secrets --pass names, and nothing else; a name that carries a credential is
// dropped unless --pass names it, even in an allowed family.
func TestTheChildEnvironmentIsAnAllowlist(t *testing.T) {
	t.Parallel()
	got := childEnviron([]string{"GH_TOKEN=t", "GITHUB_TOKEN=t", "SSH_AUTH_SOCK=/s", "GIT_ASKPASS=/a", "CLAUDE_CODE_MESSAGING_TOKEN=m",
		"AWS_SECRET_ACCESS_KEY=a", "FOO_PASSWORD=p", "FOO=bar", "ANTHROPIC_API_KEY=k", "OPENCODE_API_KEY=o", "GOAUTH=g",
		"PATH=/bin", "HOME=/h", "TMPDIR=/t", "LANG=C.UTF-8", "LC_ALL=C", "GOFLAGS=-mod=readonly", "XDG_DATA_HOME=/x", "NOVA_SWARM_JOB=/j"},
		[]string{"OPENCODE_API_KEY"})
	require.Equal(t, []string{"OPENCODE_API_KEY=o", "PATH=/bin", "HOME=/h", "TMPDIR=/t", "LANG=C.UTF-8", "LC_ALL=C",
		"GOFLAGS=-mod=readonly", "XDG_DATA_HOME=/x", "NOVA_SWARM_JOB=/j"}, got)
}

// A member whose children would start with no provider key says so once at its
// start, unless the model is local or a key is handed some other way.
func TestAMemberWithNoPassSaysSo(t *testing.T) {
	t.Parallel()
	assert.Contains(t, passNote("anthropic/claude-x", nil, ""), "NOTE member --pass names no secret")
	assert.Empty(t, passNote("anthropic/claude-x", []string{"ANTHROPIC_API_KEY"}, ""))
	assert.Empty(t, passNote("anthropic/claude-x", nil, "/auth.json"))
	assert.Empty(t, passNote("ollama/qwen3", nil, ""))
}
