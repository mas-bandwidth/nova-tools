//go:build unix

package launch

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary plays two parts besides the tests. Started with argv[0]
// "nova-card" it is the fixture card wrapper: it reads its one line from
// stdin, records what it got, and runs on the way a card does. Started with
// NOVA_LAUNCH_TEST_ROLE=launcher it is the bench side of the ssh session:
// `card launch --stdin` over the fixture wrapper. Started with
// NOVA_LAUNCH_TEST_HARNESS set it is the harness the real nova-card runs
// (TestLaunchRealWrapper).
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == WrapperName {
		os.Exit(fixtureWrapper())
	}
	if os.Getenv(realHarnessEnv) != "" {
		os.Exit(realHarness())
	}
	if os.Getenv("NOVA_LAUNCH_TEST_ROLE") == "launcher" {
		res, err := Launch(os.Stdin, os.Stdout, Config{Wrapper: os.Getenv("NOVA_LAUNCH_TEST_WRAPPER")})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if res.Refused > 0 || res.Overran {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fixtureWrapper is a card that outlives the launcher: it writes
// <dir>/<sprint>.<label>.<attempt> = "<pid> <token> <argv>" and sleeps.
func fixtureWrapper() int {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return 3
	}
	l, err := ParseLine(strings.TrimSuffix(line, "\n"))
	if err != nil || len(os.Args) != 2 || os.Args[1] != l.Card() {
		return 4
	}
	if l.Label == os.Getenv("NOVA_LAUNCH_TEST_REFUSE_LABEL") {
		fixtureLaunchAck("REFUSED not dealt")
		return 4
	}
	fixtureLaunchAck("LAUNCHED")
	dir := os.Getenv("NOVA_LAUNCH_TEST_DIR")
	name := filepath.Join(dir, fmt.Sprintf("%s.%s.%d", l.Sprint, l.Label, l.Attempt))
	body := fmt.Sprintf("%d %s %s\n", os.Getpid(), l.Token, strings.Join(os.Args, " "))
	if err := os.WriteFile(name+".tmp", []byte(body), 0o644); err != nil {
		return 5
	}
	if err := os.Rename(name+".tmp", name); err != nil {
		return 5
	}
	// A card that runs on: only a kill ends it before the test is long over.
	time.Sleep(4 * testWait())
	return 0
}

func fixtureLaunchAck(status string) {
	fd, err := strconv.Atoi(os.Getenv(LaunchAckFDEnv))
	if err != nil || fd < 3 {
		return
	}
	f := os.NewFile(uintptr(fd), "fixture-launch-ack")
	fmt.Fprintln(f, status)
	_ = f.Close()
}

// testWait is the poll bound, NOVA_TEST_WAIT or thirty seconds.
func testWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("NOVA_TEST_WAIT")); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

func fixtureLines(n int) []Line {
	lines := make([]Line, n)
	for i := range lines {
		attempt := 1 + i%3
		lines[i] = Line{
			Sprint:  "s-launch",
			Label:   fmt.Sprintf("card-%02d", i+1),
			Attempt: attempt,
			Token:   fmt.Sprintf("%d.%032x", attempt, 0xc0ffee0000+i),
		}
	}
	return lines
}

// waitReady returns each wrapper's record, "<pid> <token> nova-card <card>",
// once the wrapper has written it; a wrapper still starting would otherwise
// write into a temp dir the test is removing.
func waitReady(t *testing.T, dir string, lines []Line) map[string]string {
	t.Helper()
	got := map[string]string{}
	until := time.Now().Add(testWait())
	for _, l := range lines {
		name := filepath.Join(dir, fmt.Sprintf("%s.%s.%d", l.Sprint, l.Label, l.Attempt))
		for {
			body, err := os.ReadFile(name)
			if err == nil {
				got[l.Card()] = string(body)
				break
			}
			if time.Now().After(until) {
				t.Fatalf("wrapper %s never started: %v", l.Card(), err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	return got
}

func killAll(pids map[string]int) {
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func commandOf(t *testing.T, pid int) string {
	t.Helper()
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func TestParseLineTakesOnlyTheCanonicalLine(t *testing.T) {
	t.Parallel()

	good := "s-1 card-a 2 2." + strings.Repeat("ab", 16)
	l, err := ParseLine(good)
	if err != nil || l.String() != good || l.CommandIdentity() != "nova-card s-1/card-a/2" {
		t.Fatalf("ParseLine(%q) = %+v, %v", good, l, err)
	}
	for _, bad := range []string{
		"",
		"s-1 card-a 2",
		"s-1 card-a 2 2." + strings.Repeat("ab", 16) + " extra",
		"s-1 card-a 0 0." + strings.Repeat("ab", 16),
		"s-1 card-a 02 2." + strings.Repeat("ab", 16),
		"s-1 card-a 2 3." + strings.Repeat("ab", 16),
		"s-1 card-a 2 2." + strings.Repeat("AB", 16),
		"s-1 card-a 2 2." + strings.Repeat("ab", 15),
		"S1 card-a 2 2." + strings.Repeat("ab", 16),
		"s-1 card/a 2 2." + strings.Repeat("ab", 16),
		"s-1 card-a 2 2." + strings.Repeat("ab", 16) + "\x00",
	} {
		if _, err := ParseLine(bad); err == nil {
			t.Errorf("ParseLine(%q) accepted", bad)
		}
	}
}

// A second line for the same attempt in one batch never starts a second
// wrapper, and a malformed line is refused by number without its content.
func TestLaunchRefusesASecondLaunchOfOneAttempt(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("NOVA_LAUNCH_TEST_DIR", dir)
	l := fixtureLines(1)[0]
	in := l.String() + "\n" + "not a card line\n" + l.String() + "\n"
	var out strings.Builder
	res, err := Launch(strings.NewReader(in), &out, Config{Wrapper: exe})
	pids := map[string]int{}
	t.Cleanup(func() { killAll(pids) })
	for _, s := range strings.Split(out.String(), "\n") {
		f := strings.Fields(s)
		if len(f) == 3 && f[0] == "LAUNCHED" {
			pid, _ := strconv.Atoi(strings.TrimPrefix(f[2], "pid="))
			pids[f[1]+"#"+strconv.Itoa(len(pids))] = pid
		}
	}
	if err != nil || res.Started != 1 || res.Refused != 2 || len(pids) != 1 {
		t.Fatalf("Launch = %+v, %v; output %q", res, err, out.String())
	}
	waitReady(t, dir, []Line{l})
	want := []string{"REFUSED line=2 malformed", "REFUSED line=3 duplicate " + l.Card(), "LAUNCH started=1 refused=2 "}
	for _, w := range want {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output %q lacks %q", out.String(), w)
		}
	}
	if strings.Contains(out.String(), l.Token) {
		t.Errorf("output carries the token: %q", out.String())
	}
}

func TestLaunchRefusesAMissingWrapper(t *testing.T) {
	t.Parallel()

	_, err := Launch(strings.NewReader(""), io.Discard, Config{Wrapper: filepath.Join(t.TempDir(), "nova-card")})
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("Launch with no wrapper: %v", err)
	}
}

// TestLaunchSharesOneBudgetAcrossAcknowledgements is the regression for the
// per-child acknowledgement wait renewing the whole batch budget. The fake
// starter consumes three seconds for each child without sleeping: the first
// child fits, while the second gets only the two seconds still left and must
// time out without launching.
func TestLaunchSharesOneBudgetAcrossAcknowledgements(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	lines := fixtureLines(2)
	var in strings.Builder
	for _, l := range lines {
		in.WriteString(l.String() + "\n")
	}
	clock := time.Unix(1_800_000_000, 0)
	batchDeadline := clock.Add(5 * time.Second)
	var deadlines []time.Time
	start := func(_ string, _ Line, deadline time.Time) (int, string, error) {
		deadlines = append(deadlines, deadline)
		const ack = 3 * time.Second
		if left := deadline.Sub(clock); left < ack {
			clock = deadline
			return 0, "", fmt.Errorf("wrapper acknowledgement timed out")
		}
		clock = clock.Add(ack)
		return len(deadlines), "LAUNCHED", nil
	}
	var out strings.Builder
	res, err := Launch(strings.NewReader(in.String()), &out, Config{
		Wrapper: exe,
		Budget:  5 * time.Second,
		Now:     func() time.Time { return clock },
		start:   start,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Started != 1 || res.Refused != 1 || res.Overran {
		t.Fatalf("Launch = %+v, want started=1 refused=1 overran=false; output %q", res, out.String())
	}
	if len(deadlines) != 2 || !deadlines[0].Equal(batchDeadline) || !deadlines[1].Equal(batchDeadline) {
		t.Fatalf("ack deadlines = %v, want the one batch deadline %s", deadlines, batchDeadline)
	}
}

// The budget is the verb's own clock: on a clock that moves one second per
// read, the lines reached at or after five seconds are REFUSED timeout and never
// started, and the verb still returns.
func TestLaunchRefusesLinesPastTheBudget(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("NOVA_LAUNCH_TEST_DIR", dir)
	var in strings.Builder
	for _, l := range fixtureLines(8) {
		in.WriteString(l.String() + "\n")
	}
	clock := time.Now()
	tick := func() time.Time { clock = clock.Add(time.Second); return clock }
	var out strings.Builder
	res, err := Launch(strings.NewReader(in.String()), &out, Config{Wrapper: exe, Budget: DefaultBudget, Now: tick})
	pids := map[string]int{}
	t.Cleanup(func() { killAll(pids) })
	for _, s := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(s); len(f) == 3 && f[0] == "LAUNCHED" {
			pids[f[1]], _ = strconv.Atoi(strings.TrimPrefix(f[2], "pid="))
		}
	}
	if err != nil || res.Started != 4 || res.Refused != 4 || !res.Overran || len(pids) != 4 {
		t.Fatalf("Launch = %+v, %v; output %q", res, err, out.String())
	}
	waitReady(t, dir, fixtureLines(4))
	for _, w := range []string{"REFUSED line=5 timeout s-launch/card-05/", "REFUSED line=8 timeout ", "LAUNCH started=4 refused=4 ms=9000 over=true"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output %q lacks %q", out.String(), w)
		}
	}
}

// TestLaunchOverrunsWithNothingRefused is #2931 HOLD 7 (stella): the
// per-line budget check only bounds the time reached BEFORE that line's own
// start, so a slow final start can push the whole batch's wall time past
// budget without ever refusing a line. Every line here is on time by the
// per-line check (a fake clock that has barely moved when each is reached),
// but the clock jumps once more, past budget, for the elapsed time measured
// after the loop -- modeling a last startDetached that itself ran long.
// Refused must stay 0 (nothing was actually late by the per-line rule) and
// Overran must be true: a caller (cmd/nova-sprint's runCardLaunch) reading
// Refused==0 alone would wrongly call this batch a success.
func TestLaunchOverrunsWithNothingRefused(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("NOVA_LAUNCH_TEST_DIR", dir)
	lines := fixtureLines(3)
	var in strings.Builder
	for _, l := range lines {
		in.WriteString(l.String() + "\n")
	}
	clock := time.Now()
	// calls: began (+0), then one pre-start check per line (+1s each, well
	// inside the 5s budget), then the post-loop elapsed check (+3s more,
	// total 6s: past budget though no per-line check ever saw it).
	increments := []time.Duration{0, time.Second, time.Second, time.Second, 3 * time.Second}
	i := 0
	tick := func() time.Time {
		clock = clock.Add(increments[i])
		if i < len(increments)-1 {
			i++
		}
		return clock
	}
	var out strings.Builder
	res, err := Launch(strings.NewReader(in.String()), &out, Config{Wrapper: exe, Budget: DefaultBudget, Now: tick})
	pids := map[string]int{}
	t.Cleanup(func() { killAll(pids) })
	for _, s := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(s); len(f) == 3 && f[0] == "LAUNCHED" {
			pids[f[1]], _ = strconv.Atoi(strings.TrimPrefix(f[2], "pid="))
		}
	}
	if err != nil || res.Started != 3 || res.Refused != 0 || !res.Overran || len(pids) != 3 {
		t.Fatalf("Launch = %+v, %v; output %q", res, err, out.String())
	}
	waitReady(t, dir, lines)
	if !strings.Contains(out.String(), "LAUNCH started=3 refused=0 ms=6000 over=true") {
		t.Errorf("output %q lacks the overrun summary", out.String())
	}
}

// TestLaunchEchoesRefusalsToStderr is nova-tools #3700 part 3: every REFUSED
// line goes to Config.Err as well as out, so a session log on the bench
// shows why a card did not start; a LAUNCHED line stays on out only.
func TestLaunchEchoesRefusalsToStderr(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	lines := fixtureLines(2)
	in := lines[0].String() + "\n" + lines[1].String() + "\n"
	start := func(_ string, l Line, _ time.Time) (int, string, error) {
		if l == lines[1] {
			return 0, "REFUSED card launched NOPERM on ws:*", nil
		}
		return 7, "LAUNCHED", nil
	}
	var out, errOut strings.Builder
	res, err := Launch(strings.NewReader(in), &out, Config{Wrapper: exe, Err: &errOut, start: start})
	if err != nil {
		t.Fatal(err)
	}
	want := "REFUSED line=2 wrapper " + lines[1].Card() + ": REFUSED card launched NOPERM on ws:*\n"
	if res.Refused != 1 || res.Started != 1 || !strings.Contains(out.String(), want) {
		t.Fatalf("Launch = %+v, out %q, want the refusal %q", res, out.String(), want)
	}
	if errOut.String() != want {
		t.Fatalf("stderr %q, want exactly the REFUSED line %q", errOut.String(), want)
	}
}
