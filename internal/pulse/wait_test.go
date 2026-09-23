package pulse

// The wait loop, its six conditions and its two receipts. Nothing here opens a socket
// except the two tests that say so: the store tests run against a miniredis, and the one
// live-store test is gated on NOVA_REDIS_TEST=1 and deletes the keys it wrote.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// clock is a test clock the loop's sleep advances, so a thirty-minute timeout costs
// nothing and a receipt's spent= is a constant a reader can check.
type clock struct{ t time.Time }

func (c *clock) now() time.Time        { return c.t }
func (c *clock) sleep(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                 { return &clock{t: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)} }
func (c *clock) input(in WaitInput) WaitInput {
	in.Now = c.now
	in.Sleep = c.sleep
	return in
}

// runWait runs one wait and hands back its exit code and the two streams.
func runWait(t *testing.T, in WaitInput) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout, in.Stderr = &out, &errb
	code := Wait(in)
	return code, out.String(), errb.String()
}

func TestWaitHoldsAtOnceAndPrintsOneReceipt(t *testing.T) {
	t.Parallel()
	c := newClock()
	path := filepath.Join(t.TempDir(), "RESULT.md")
	if err := os.WriteFile(path, []byte("RESULT: DONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb := runWait(t, c.input(WaitInput{
		Until: []string{"file-exists", path}, Every: time.Second, Timeout: 10 * time.Second,
	}))
	if code != 0 {
		t.Fatalf("a condition that already holds exits 0, got %d (%s)", code, errb)
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n"); lines != 0 {
		t.Fatalf("the receipt is ONE line, got:\n%s", out)
	}
	if !strings.Contains(out, "WAIT HELD until=file-exists") || !strings.Contains(out, "polls=1") {
		t.Fatalf("receipt does not say what held: %s", out)
	}
}

func TestWaitTimesOutWithExitTwoAndAReceiptOnStderr(t *testing.T) {
	t.Parallel()
	c := newClock()
	code, out, errb := runWait(t, c.input(WaitInput{
		Until: []string{"file-exists", filepath.Join(t.TempDir(), "never")},
		Every: 20 * time.Second, Timeout: 60 * time.Second,
	}))
	if code != 2 {
		t.Fatalf("a timeout exits 2, got %d", code)
	}
	if out != "" {
		t.Fatalf("nothing is printed to stdout on a timeout, got %q", out)
	}
	if !strings.Contains(errb, "WAIT TIMEOUT until=file-exists") ||
		!strings.Contains(errb, "timeout=1m0s") || !strings.Contains(errb, "spent=1m0s") {
		t.Fatalf("the timeout receipt does not carry the bound and the spend: %s", errb)
	}
}

// The loop must not sleep past its own deadline: a --every of an hour under a --timeout of
// a minute has to answer in a minute.
func TestWaitNeverSleepsPastItsTimeout(t *testing.T) {
	t.Parallel()
	c := newClock()
	start := c.t
	code, _, errb := runWait(t, c.input(WaitInput{
		Until: []string{"file-exists", filepath.Join(t.TempDir(), "never")},
		Every: time.Hour, Timeout: 90 * time.Second,
	}))
	if code != 2 {
		t.Fatalf("want 2, got %d", code)
	}
	if spent := c.t.Sub(start); spent != 90*time.Second {
		t.Fatalf("the wait spent %s against a 90s timeout; the last sleep must be clipped: %s", spent, errb)
	}
}

func TestWaitFileHasReturnsTheMatchedLineAsEvidence(t *testing.T) {
	t.Parallel()
	c := newClock()
	path := filepath.Join(t.TempDir(), "harvest.log")
	if err := os.WriteFile(path, []byte("starting\nnothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Negative first, the way the bats control did.
	code, _, _ := runWait(t, c.input(WaitInput{
		Until: []string{"file-has", path, `LANDED #[0-9]+`}, Every: time.Second, Timeout: 2 * time.Second,
	}))
	if code != 2 {
		t.Fatalf("the line is not there yet; want 2, got %d", code)
	}
	if err := os.WriteFile(path, []byte("starting\nLANDED #1234 head=abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runWait(t, c.input(WaitInput{
		Until: []string{"file-has", path, `LANDED #[0-9]+`}, Every: time.Second, Timeout: 5 * time.Second,
	}))
	if code != 0 {
		t.Fatalf("want 0, got %d", code)
	}
	if !strings.Contains(out, "LANDED #1234 head=abc") {
		t.Fatalf("the matched LINE is the evidence, got: %s", out)
	}
}

// bin/wait-for's `file` mode wanted a NON-EMPTY path and `file-exists` does not. The
// difference is one silent early return in a lane script, so it is asserted, and the
// replacement for the old mode is asserted beside it.
func TestWaitFileExistsHoldsOnAnEmptyFileAndFileHasDotIsTheOldMode(t *testing.T) {
	t.Parallel()
	c := newClock()
	path := filepath.Join(t.TempDir(), "RESULT.md")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runWait(t, c.input(WaitInput{
		Until: []string{"file-exists", path}, Every: time.Second, Timeout: 2 * time.Second,
	})); code != 0 {
		t.Fatalf("file-exists holds on an empty file, got %d", code)
	}
	if code, _, _ := runWait(t, c.input(WaitInput{
		Until: []string{"file-has", path, "."}, Every: time.Second, Timeout: 2 * time.Second,
	})); code != 2 {
		t.Fatalf("file-has . is the old non-empty mode and must NOT hold on an empty file, got %d", code)
	}
	if err := os.WriteFile(path, []byte("RESULT: DONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runWait(t, c.input(WaitInput{
		Until: []string{"file-has", path, "."}, Every: time.Second, Timeout: 2 * time.Second,
	})); code != 0 {
		t.Fatalf("file-has . holds once there is a byte, got %d", code)
	}
}

func TestWaitProcessGoneThroughTheWholeLoop(t *testing.T) {
	t.Parallel()
	c := newClock()
	polls := 0
	procs := func() ([]WaitProc, error) {
		polls++
		if polls < 3 {
			return table("100 1 /bin/bash /tmp/waitfor-probe"), nil
		}
		return table("200 1 /usr/sbin/cupsd"), nil
	}
	code, out, _ := runWait(t, c.input(WaitInput{
		Until: []string{"process-gone", "waitfor-probe"}, Every: 20 * time.Second,
		Timeout: 10 * time.Minute, Self: 999, Procs: procs,
	}))
	if code != 0 {
		t.Fatalf("want 0 once the process is gone, got %d", code)
	}
	if !strings.Contains(out, "polls=3") || !strings.Contains(out, "spent=40s") {
		t.Fatalf("the receipt must count the polls and the spend: %s", out)
	}
}

func TestWaitPRCheckAsksForOneStateAndSaysWhatItSaw(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		want  string
		state PRState
		hold  bool
	}{
		{"green holds on green", "green", PRState{State: "OPEN", Checks: "green"}, true},
		{"green does not hold on pending", "green", PRState{State: "OPEN", Checks: "pending"}, false},
		{"green does not hold on a head with no checks", "green", PRState{State: "OPEN", Checks: "none"}, false},
		{"red holds on red", "red", PRState{State: "OPEN", Checks: "red"}, true},
		{"merged reads the PR state, not the checks", "merged", PRState{State: "MERGED", Checks: "none"}, true},
		{"closed does not hold on an open PR", "closed", PRState{State: "OPEN", Checks: "green"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newClock()
			code, out, errb := runWait(t, c.input(WaitInput{
				Until: []string{"pr-check", "mas-bandwidth/nova-tools", "2546", tc.want},
				Every: time.Second, Timeout: 2 * time.Second,
				PR: func(string, int) (PRState, error) { return tc.state, nil },
			}))
			if tc.hold && code != 0 {
				t.Fatalf("want held, got %d %s", code, errb)
			}
			if !tc.hold && code != 2 {
				t.Fatalf("want timeout, got %d %s", code, out)
			}
			// Either receipt names what the forge actually said.
			if !strings.Contains(out+errb, "state="+tc.state.State) {
				t.Fatalf("the receipt must carry what was seen: %s%s", out, errb)
			}
		})
	}
}

// A reader that fails is not a condition that held, and the timeout receipt says which.
func TestWaitCarriesTheReadersLastErrorOntoTheTimeoutReceipt(t *testing.T) {
	t.Parallel()
	c := newClock()
	code, _, errb := runWait(t, c.input(WaitInput{
		Until: []string{"pr-check", "mas-bandwidth/nova-tools", "2546", "green"},
		Every: time.Second, Timeout: 3 * time.Second,
		PR: func(string, int) (PRState, error) { return PRState{}, errors.New("gh: could not resolve host") },
	}))
	if code != 2 {
		t.Fatalf("a reader that never answered is not a hold; want 2, got %d", code)
	}
	if !strings.Contains(errb, "could not resolve host") {
		t.Fatalf("the timeout receipt must name the reader's failure: %s", errb)
	}
}

func TestRollupStateFoldsTheHeadsChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{nil, "none"},
		{[]string{"SUCCESS", "SKIPPED", "NEUTRAL"}, "green"},
		{[]string{"SUCCESS", "IN_PROGRESS"}, "pending"},
		{[]string{"SUCCESS", "FAILURE"}, "red"},
		// red wins over pending: a head with one failure and six still running is red now.
		{[]string{"IN_PROGRESS", "FAILURE", "QUEUED"}, "red"},
		{[]string{"TIMED_OUT"}, "red"},
		{[]string{"something nobody has seen"}, "pending"},
	} {
		if got := RollupState(tc.words); got != tc.want {
			t.Errorf("RollupState(%v) = %s, want %s", tc.words, got, tc.want)
		}
	}
}

func TestWaitRedisKeyAgainstAFakeStore(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := newClock()
	in := WaitInput{
		Until: []string{"redis-key", "sprint:current", "cards-v2"},
		Every: time.Second, Timeout: 3 * time.Second,
		Store: StoreOptions{Addr: mr.Addr()},
	}
	if code, _, _ := runWait(t, c.input(in)); code != 2 {
		t.Fatal("an absent key is not a value")
	}
	mr.Set("sprint:current", "something-else")
	if code, _, _ := runWait(t, c.input(in)); code != 2 {
		t.Fatal("another value is not the value asked for")
	}
	mr.Set("sprint:current", "cards-v2")
	code, out, errb := runWait(t, c.input(in))
	if code != 0 {
		t.Fatalf("want 0 once the key carries the value, got %d %s", code, errb)
	}
	if !strings.Contains(out, "cards-v2") {
		t.Fatalf("the receipt carries the value it read: %s", out)
	}
	// `*` asks only that the key exist.
	star := in
	star.Until = []string{"redis-key", "bench:hulk:at", "*"}
	if code, _, _ := runWait(t, c.input(star)); code != 2 {
		t.Fatal("* still wants the key to exist")
	}
	mr.Set("bench:hulk:at", "2026-09-22T18:00:00Z")
	if code, _, _ := runWait(t, c.input(star)); code != 0 {
		t.Fatal("* holds as soon as the key is there")
	}
}

// One live-store test, gated. It writes only bench:test-* -- the prefix the bench ACL user
// may write -- and deletes what it wrote in Cleanup.
func TestWaitRedisKeyAgainstTheLiveStore(t *testing.T) {
	if os.Getenv("NOVA_REDIS_TEST") != "1" {
		t.Skip("set NOVA_REDIS_TEST=1 and NOVA_REDIS_ADDR=<host:port>, under nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD, to reach the fleet store")
	}
	// The address is an environment variable and not a literal here, because a host name
	// written into a test is a test that tries to reach a machine on somebody else's CI.
	addr := os.Getenv("NOVA_REDIS_ADDR")
	if addr == "" {
		t.Skip("NOVA_REDIS_TEST=1 without NOVA_REDIS_ADDR names no store")
	}
	key := fmt.Sprintf("bench:test-wait-%d", os.Getpid())
	rdb, err := DialStore(context.Background(), StoreOptions{Addr: addr})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		_ = rdb.Del(context.Background(), key).Err()
		_ = rdb.Close()
	})
	if err := rdb.Set(context.Background(), key, "held", time.Minute).Err(); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
	c := newClock()
	code, out, errb := runWait(t, c.input(WaitInput{
		Until: []string{"redis-key", key, "held"}, Every: time.Second, Timeout: 10 * time.Second,
		Store: StoreOptions{Addr: addr},
	}))
	if code != 0 {
		t.Fatalf("the live store did not answer: %d %s", code, errb)
	}
	if !strings.Contains(out, "WAIT HELD") {
		t.Fatalf("receipt: %s", out)
	}
}

// The bus's answered rule, through the verb: a reply in another lane holds, a receipt does
// not, and the note's own lane is never asked.
func TestWaitBusNoteHoldsOnAReplyAndNotOnAReceipt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBusFile(t, root, "participants.json", `{"participants":[
  {"name":"Rowan","lane":"from-rowan","git_name":"Rowan","git_email":"rowan@example.com"},
  {"name":"Johnny","lane":"from-johnny","git_name":"Johnny","git_email":"johnny@example.com"}
]}`)
	writeBusFile(t, root, "from-rowan/2026-09-22T1200Z-the-question-abcdef012345.md", `From: Rowan
To: Johnny
Date: Tue Sep 22 12:00:00 UTC 2026
Id: rowan-abcdef012345
Subject: The mechanical-control ruling

Does a scripted CI pass count as a control?
`)
	c := newClock()
	in := WaitInput{
		Until: []string{"bus-note", "rowan-abcdef012345"}, Bus: root,
		Every: time.Second, Timeout: 3 * time.Second,
	}
	if code, _, _ := runWait(t, c.input(in)); code != 2 {
		t.Fatal("a note nobody answered must not hold")
	}

	// A receipt is HEARD, not answered: still waiting, and the receipt says so.
	writeBusFile(t, root, "from-johnny/RECEIPTS", "2026-09-22T12:05:00Z rowan-abcdef012345\n")
	code, _, errb := runWait(t, c.input(in))
	if code != 2 {
		t.Fatal("heard is not answered")
	}
	if !strings.Contains(errb, "heard by") {
		t.Fatalf("the timeout receipt must say the note was heard: %s", errb)
	}

	// A reply in another lane holds.
	writeBusFile(t, root, "from-johnny/2026-09-22T1210Z-the-answer-111111111111.md", `From: Johnny
To: Rowan
Date: Tue Sep 22 12:10:00 UTC 2026
Id: johnny-111111111111
Re: rowan-abcdef012345
Subject: Re: The mechanical-control ruling

It does.
`)
	code, out, _ := runWait(t, c.input(in))
	if code != 0 {
		t.Fatalf("a reply answers the note; got %d", code)
	}
	if !strings.Contains(out, "answered by from-johnny/") {
		t.Fatalf("the receipt names the answering note: %s", out)
	}
}

// A note answered only in its OWN lane is not answered: the bus's rule is per reader, and
// asking the sender's own lane would make every note self-answering.
func TestWaitBusNoteIgnoresTheSendersOwnLane(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeBusFile(t, root, "participants.json", `{"participants":[{"name":"Rowan","lane":"from-rowan","git_name":"Rowan","git_email":"rowan@example.com"}]}`)
	writeBusFile(t, root, "from-rowan/2026-09-22T1200Z-the-question-abcdef012345.md", `From: Rowan
To: Johnny
Date: Tue Sep 22 12:00:00 UTC 2026
Id: rowan-abcdef012345
Subject: The question

Body.
`)
	writeBusFile(t, root, "from-rowan/2026-09-22T1230Z-my-own-follow-up-222222222222.md", `From: Rowan
To: Johnny
Date: Tue Sep 22 12:30:00 UTC 2026
Id: rowan-222222222222
Re: rowan-abcdef012345
Subject: Re: The question

Bumping my own note.
`)
	c := newClock()
	code, _, _ := runWait(t, c.input(WaitInput{
		Until: []string{"bus-note", "rowan-abcdef012345"}, Bus: root,
		Every: time.Second, Timeout: 2 * time.Second,
	}))
	if code != 2 {
		t.Fatal("answering your own note is not an answer")
	}
}

func writeBusFile(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Every refusal is exit 2 before the first poll, naming what was wrong -- bin/wait-for's
// unknown mode exited 2 and a wait that can never hold must never be started.
func TestWaitRefusesBeforeItStarts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   WaitInput
		says string
	}{
		{"no condition", WaitInput{}, "--until is required"},
		{"unknown condition", WaitInput{Until: []string{"whenever", "something"}}, "is not a condition"},
		{"process-gone with no pattern", WaitInput{Until: []string{"process-gone"}}, "wants <pattern>"},
		{"file-has with no regex", WaitInput{Until: []string{"file-has", "./log"}}, "wants <path> <regex>"},
		{"file-has with a bad regex", WaitInput{Until: []string{"file-has", "./log", "("}}, "does not compile"},
		{"pr-check with a word for a number", WaitInput{Until: []string{"pr-check", "o/n", "x", "green"}}, "is not a pull request number"},
		{"pr-check with an unknown state", WaitInput{Until: []string{"pr-check", "o/n", "5", "purple"}}, "is not a state"},
		{"redis-key with no store", WaitInput{Until: []string{"redis-key", "k", "v"}}, "wants --store"},
		{"bus-note with no bus", WaitInput{Until: []string{"bus-note", "rowan-1"}}, "wants --bus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newClock()
			in := c.input(tc.in)
			in.Every, in.Timeout = time.Second, time.Second
			code, out, errb := runWait(t, in)
			if code != 2 {
				t.Fatalf("want 2, got %d (%s)", code, out)
			}
			if !strings.Contains(errb, tc.says) {
				t.Fatalf("the refusal must say %q, got: %s", tc.says, errb)
			}
		})
	}
}

// `-- cmd...` runs after the condition holds and its exit status is the verb's: a harvest
// that failed must not read as a wait that succeeded.
func TestWaitRunsTheCommandAndCarriesItsExitStatus(t *testing.T) {
	t.Parallel()
	c := newClock()
	path := filepath.Join(t.TempDir(), "there")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	code, out, _ := runWait(t, c.input(WaitInput{
		Until: []string{"file-exists", path}, Every: time.Second, Timeout: time.Second,
		Cmd:  []string{"nova-pulse", "harvest", "--bench", "hulk"},
		Exec: func(cmd []string) int { got = cmd; return 3 },
	}))
	if code != 3 {
		t.Fatalf("the command's exit status is the verb's, got %d", code)
	}
	if strings.Join(got, " ") != "nova-pulse harvest --bench hulk" {
		t.Fatalf("the argv goes through as given, got %v", got)
	}
	if !strings.Contains(out, "WAIT HELD") {
		t.Fatalf("the receipt is printed before the command runs: %s", out)
	}
}

// The command never runs on a timeout.
func TestWaitDoesNotRunTheCommandOnTimeout(t *testing.T) {
	t.Parallel()
	c := newClock()
	ran := false
	code, _, _ := runWait(t, c.input(WaitInput{
		Until: []string{"file-exists", filepath.Join(t.TempDir(), "never")},
		Every: time.Second, Timeout: 2 * time.Second,
		Cmd:  []string{"true"},
		Exec: func([]string) int { ran = true; return 0 },
	}))
	if code != 2 || ran {
		t.Fatalf("a timeout runs nothing; code=%d ran=%v", code, ran)
	}
}

func TestParsePollInterval(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want time.Duration
		bad  bool
	}{
		{"20", 20 * time.Second, false}, // the bare number bin/wait-for took
		{"1s", time.Second, false},
		{"500ms", 500 * time.Millisecond, false},
		{"2m", 2 * time.Minute, false},
		{"", 0, true},
		{"0", 0, true},
		{"1ms", 0, true}, // under the floor: a poll that fast is a spin
		{"soon", 0, true},
	} {
		got, err := ParsePollInterval(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("ParsePollInterval(%q) wanted a refusal, got %s", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParsePollInterval(%q) = %s %v, want %s", tc.in, got, err, tc.want)
		}
	}
}
