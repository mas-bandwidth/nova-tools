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
// `card launch --stdin` over the fixture wrapper.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == WrapperName {
		os.Exit(fixtureWrapper())
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

// TestLaunchReturnsBeforeTheCardEnds is #2931's DONE-WHEN: 50 lines on stdin
// start 50 detached wrappers with command identity `nova-card <S>/<label>/<attempt>`,
// the verb exits within 5 s while every wrapper runs on, and killing the
// parent ssh leaves every wrapper alive.
func TestLaunchReturnsBeforeTheCardEnds(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	lines := fixtureLines(50)
	var in strings.Builder
	for _, l := range lines {
		in.WriteString(l.String() + "\n")
	}

	// The parent ssh: a shell in its own session, as sshd starts one, that
	// starts a canary in the session's process group, runs the verb, and then
	// stays open the way a session does. The wrappers cannot end on their
	// own before the test is over, so a verb that waited for a card would
	// never print EXIT.
	sess := exec.Command("/bin/sh", "-c", `sleep 600 & echo "CANARY $!"; "$0"; echo "EXIT $?"; exec sleep 600`, exe)
	sess.Env = append(os.Environ(),
		"NOVA_LAUNCH_TEST_ROLE=launcher",
		"NOVA_LAUNCH_TEST_WRAPPER="+exe,
		"NOVA_LAUNCH_TEST_DIR="+dir)
	sess.Stdin = strings.NewReader(in.String())
	sess.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	sess.Stderr = &stderr
	pids := map[string]int{}
	t.Cleanup(func() { killAll(pids) })

	began := time.Now()
	if err := sess.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-sess.Process.Pid, syscall.SIGKILL) })

	got := make(chan string)
	eof := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			got <- sc.Text()
		}
		close(eof)
	}()

	var exitLine, summary string
	canary := 0
	deadline := time.After(testWait())
	for exitLine == "" {
		select {
		case s := <-got:
			if strings.HasPrefix(s, "EXIT ") {
				exitLine = s
				continue
			}
			if strings.HasPrefix(s, "LAUNCH ") {
				summary = s
			}
			if c, ok := strings.CutPrefix(s, "CANARY "); ok {
				canary, _ = strconv.Atoi(c)
			}
			f := strings.Fields(s)
			if len(f) == 3 && f[0] == "LAUNCHED" && strings.HasPrefix(f[2], "pid=") {
				pid, err := strconv.Atoi(strings.TrimPrefix(f[2], "pid="))
				if err != nil {
					t.Fatalf("launch line %q", s)
				}
				pids[f[1]] = pid
			}
		case <-eof:
			t.Fatalf("session output ended before the verb exited; stderr %q", stderr.String())
		case <-deadline:
			t.Fatalf("card launch never exited while its cards ran (%d of 50 launched); stderr %q", len(pids), stderr.String())
		}
	}
	wall := time.Now().Sub(began)
	// EXIT 0 is every line started inside the verb's own 5 s budget
	// (DefaultBudget): a line past it is REFUSED timeout and exits 1, and
	// TestMain above also exits 1 on Overran, so EXIT 0 here is itself an
	// assertion that the batch's own measured wall time did not overrun.
	if exitLine != "EXIT 0" || !strings.HasPrefix(summary, "LAUNCH started=50 refused=0 ") || !strings.Contains(summary, "over=false") {
		t.Fatalf("card launch %s %q; stderr %q", exitLine, summary, stderr.String())
	}
	// The DONE-WHEN bound itself, asserted rather than only logged: the
	// harness's EXIT 0 already means the launcher's own budget check
	// passed, but that check happens before this test process ever saw
	// the exit; this pins the real wall clock too.
	if wall > DefaultBudget {
		t.Fatalf("card launch took %s wall, want at most the %s budget", wall, DefaultBudget)
	}
	if len(pids) != 50 || canary == 0 {
		t.Fatalf("card launch reported %d LAUNCHED lines (want 50), canary %d", len(pids), canary)
	}
	t.Logf("%s; the session saw EXIT after %dms", summary, wall.Milliseconds())

	// Every wrapper runs on after the verb returned: it is alive, it is the
	// leader of its own process group, its command identity is nova-card
	// <S>/<label>/<attempt>, and its token came on stdin, never argv.
	records := waitReady(t, dir, lines)
	for _, l := range lines {
		body := records[l.Card()]
		f := strings.Fields(body)
		pid := pids[l.Card()]
		if len(f) != 4 || f[0] != strconv.Itoa(pid) || f[1] != l.Token || f[2]+" "+f[3] != l.CommandIdentity() {
			t.Fatalf("wrapper %s recorded %q; want pid %d, its token, argv %q", l.Card(), body, pid, l.CommandIdentity())
		}
	}
	alive := func(when string) {
		t.Helper()
		for _, l := range lines {
			pid := pids[l.Card()]
			if err := syscall.Kill(pid, 0); err != nil {
				t.Fatalf("%s: wrapper %s pid %d is gone: %v", when, l.Card(), pid, err)
			}
			if cmd := commandOf(t, pid); cmd != l.CommandIdentity() {
				t.Fatalf("%s: pid %d command %q, want %q", when, pid, cmd, l.CommandIdentity())
			}
			if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
				t.Fatalf("%s: wrapper %s pid %d process group %d (%v); want its own (setsid)", when, l.Card(), pid, pgid, err)
			}
		}
	}
	alive("after the verb exited")

	// Kill the parent ssh: hang up and then kill its whole process group. The
	// session's output must end, which it cannot while any wrapper holds it,
	// and the canary in the group must die: the kill landed. Then every
	// wrapper is still alive.
	_ = syscall.Kill(-sess.Process.Pid, syscall.SIGHUP)
	_ = syscall.Kill(-sess.Process.Pid, syscall.SIGKILL)
	select {
	case <-eof:
	case s := <-got:
		t.Fatalf("unexpected session output after the kill: %q", s)
	case <-time.After(testWait()):
		t.Fatal("the session's output stayed open after the session died: a wrapper holds the ssh session")
	}
	_ = sess.Wait()
	gone := time.Now().Add(testWait())
	for commandOf(t, canary) != "" && !strings.Contains(commandOf(t, canary), "defunct") {
		if time.Now().After(gone) {
			t.Fatalf("canary %d in the session's group outlived the kill", canary)
		}
		time.Sleep(20 * time.Millisecond)
	}
	alive("after the parent ssh was killed")
}

func TestParseLineTakesOnlyTheCanonicalLine(t *testing.T) {
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
	_, err := Launch(strings.NewReader(""), io.Discard, Config{Wrapper: filepath.Join(t.TempDir(), "nova-card")})
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("Launch with no wrapper: %v", err)
	}
}

// The budget is the verb's own clock: on a clock that moves one second per
// read, the lines reached after five seconds are REFUSED timeout and never
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
	clock := time.Unix(1_800_000_000, 0)
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
	if err != nil || res.Started != 5 || res.Refused != 3 || !res.Overran || len(pids) != 5 {
		t.Fatalf("Launch = %+v, %v; output %q", res, err, out.String())
	}
	waitReady(t, dir, fixtureLines(5))
	for _, w := range []string{"REFUSED line=6 timeout s-launch/card-06/", "REFUSED line=8 timeout ", "LAUNCH started=5 refused=3 ms=9000 over=true"} {
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
	clock := time.Unix(1_800_000_000, 0)
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
