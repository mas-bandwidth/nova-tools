package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// fakeSteps records the order of one tick's calls; each step answers what
// the test set. The clock is the test's: ticks are sent on a channel, never
// waited for on the wall.
type fakeSteps struct {
	mu     sync.Mutex
	order  []string
	beat   func() error
	leases func() ([]string, error)
	wake   string
	claims []task.Claim
	byes   int
	byeErr error
	// beats, when set, receives one value after each beat the fake
	// answered, so a test can change what the next beat answers without a
	// race against the loop.
	beats chan struct{}
}

func (f *fakeSteps) steps() hereSteps {
	note := func(s string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.order = append(f.order, s)
	}
	return hereSteps{
		beat: func(context.Context) error {
			note("beat")
			var err error
			if f.beat != nil {
				err = f.beat()
			}
			if f.beats != nil {
				f.beats <- struct{}{}
			}
			return err
		},
		leases: func(context.Context) ([]string, error) {
			note("leases")
			if f.leases != nil {
				return f.leases()
			}
			return nil, nil
		},
		poll: func(context.Context) (string, error) { note("poll"); return f.wake, nil },
		take: func(context.Context) ([]task.Claim, error) { note("take"); return f.claims, nil },
		bye: func(context.Context) error {
			note("bye")
			f.mu.Lock()
			defer f.mu.Unlock()
			f.byes++
			return f.byeErr
		},
	}
}

func (f *fakeSteps) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.order...)
}

var testPresence = presence{Name: "rowan", Host: "studio", Session: "s1"}

// startLoop runs hereLoop on a tick channel the test drives and returns the
// channel, a stop that cancels and waits for the exit code, and the streams.
func startLoop(t *testing.T, f *fakeSteps) (chan time.Time, func() int, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- hereLoop(ctx, ticks, testPresence, f.steps(), &out, &errOut) }()
	stop := func() int { cancel(); return <-done }
	t.Cleanup(cancel)
	return ticks, stop, &out, &errOut
}

// TestHereTickIsBeatLeasesPollTake: one tick calls the steps in order,
// prints the leases' lines, the wake it took and the claims, and ends
// nothing.
func TestHereTickIsBeatLeasesPollTake(t *testing.T) {
	t.Parallel()

	f := &fakeSteps{wake: "1:reconciler", claims: []task.Claim{{Sprint: "fix", ID: "t1", Attempt: 2, Token: "tok"}},
		leases: func() ([]string, error) { return []string{"FRIEND HERE OWNER id=q1~1 state=bound as=rowan pid=7"}, nil }}
	var out, errOut bytes.Buffer
	fails := 0
	code, end := hereCycle(context.Background(), testPresence, f.steps(), &fails, &out, &errOut)
	if code != 0 || end != keepGoing || fails != 0 {
		t.Fatalf("tick: code %d end %d fails %d", code, end, fails)
	}
	if got := strings.Join(f.calls(), ","); got != "beat,leases,poll,take" {
		t.Fatalf("order %s", got)
	}
	want := "FRIEND HERE OWNER id=q1~1 state=bound as=rowan pid=7\nFRIEND HERE WOKEN as=rowan wake=1:reconciler\nFRIEND HERE TOOK sprint=fix id=t1 attempt=2 token=tok\n"
	if out.String() != want || errOut.String() != "" {
		t.Fatalf("stdout %q stderr %q", out.String(), errOut.String())
	}
}

// TestHereLoopSaysByeOnSignal: the context ending (SIGINT, SIGTERM) says
// bye once and prints DOWN, exit 0.
func TestHereLoopSaysByeOnSignal(t *testing.T) {
	t.Parallel()

	f := &fakeSteps{}
	ticks, stop, out, _ := startLoop(t, f)
	ticks <- time.Time{}
	ticks <- time.Time{}
	if code := stop(); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if f.byes != 1 || !strings.HasSuffix(out.String(), "FRIEND HERE DOWN as=rowan session=s1\n") {
		t.Fatalf("byes %d stdout %q", f.byes, out.String())
	}
	if got := strings.Join(f.calls(), ","); got != "beat,leases,poll,take,beat,leases,poll,take,bye" {
		t.Fatalf("order %s", got)
	}
}

// TestHereLoopExitsThreeAfterFiveBeatFailures: five failed beats in a row
// end the loop with bye and exit 3; a success in between resets the count.
func TestHereLoopExitsThreeAfterFiveBeatFailures(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	failing := true
	f := &fakeSteps{beats: make(chan struct{}, 16)}
	f.beat = func() error {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			return errors.New("dial tcp: connection refused")
		}
		return nil
	}
	ticks, stop, out, errOut := startLoop(t, f)
	tick := func() { ticks <- time.Time{}; <-f.beats }
	for i := 0; i < 4; i++ {
		tick()
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	tick() // resets the count
	mu.Lock()
	failing = true
	mu.Unlock()
	for i := 0; i < 4; i++ {
		tick()
	}
	// nine failures, never five in a row: still running; the tenth tick
	// is the fifth in a row
	tick()
	if code := stop(); code != 3 {
		t.Fatalf("exit %d, want 3; stderr %s", code, errOut.String())
	}
	if f.byes != 1 || !strings.HasSuffix(out.String(), "FRIEND HERE DOWN as=rowan session=s1\n") {
		t.Fatalf("byes %d stdout %q", f.byes, out.String())
	}
	if !strings.Contains(errOut.String(), "(5 of 5)") || strings.Contains(errOut.String(), "(6 of 5)") {
		t.Fatalf("stderr %s", errOut.String())
	}
	// no leases, poll or take followed a failed beat: the one succeeding
	// tick ran the whole cycle, the nine failures nothing but the beat
	leases := 0
	for _, c := range f.calls() {
		if c == "leases" {
			leases++
		}
	}
	if leases != 1 || len(f.calls()) != 10+3+1 {
		t.Fatalf("calls %v", f.calls())
	}
}

// TestHereLoopFencedExitsWithoutBye: another session holds the beat; the
// loop is no longer the presence, so it exits 3 and deletes nothing.
func TestHereLoopFencedExitsWithoutBye(t *testing.T) {
	t.Parallel()

	f := &fakeSteps{}
	f.beat = func() error { return fmt.Errorf("%w: session s2 on laptop holds the beat", errFenced) }
	ticks, stop, out, errOut := startLoop(t, f)
	ticks <- time.Time{}
	if code := stop(); code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	if f.byes != 0 || strings.Contains(out.String(), "DOWN") || !strings.Contains(errOut.String(), "no longer the presence") {
		t.Fatalf("byes %d stdout %q stderr %q", f.byes, out.String(), errOut.String())
	}
}

// TestHereLoopUnregisteredExitsWithBye: the roster dropped the name; the
// loop says bye and exits 3.
func TestHereLoopUnregisteredExitsWithBye(t *testing.T) {
	t.Parallel()

	f := &fakeSteps{byeErr: errors.New("dial tcp: connection refused")}
	f.beat = func() error { return fmt.Errorf("%w: rowan left the roster", errUnregistered) }
	ticks, stop, out, errOut := startLoop(t, f)
	ticks <- time.Time{}
	if code := stop(); code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	if f.byes != 1 || !strings.HasSuffix(out.String(), "FRIEND HERE DOWN as=rowan session=s1 bye=failed\n") || !strings.Contains(errOut.String(), "friend rowan bye: dial tcp") {
		t.Fatalf("byes %d stdout %q stderr %q", f.byes, out.String(), errOut.String())
	}
}

// TestHereTickSaysALeaseOrTakeFailureAndGoesOn: a failed lease pass, poll
// or take is on stderr and never ends the loop.
func TestHereTickSaysALeaseOrTakeFailureAndGoesOn(t *testing.T) {
	t.Parallel()

	f := &fakeSteps{leases: func() ([]string, error) {
		return []string{"FRIEND HERE OWNER id=q1~1 state=dead as=rowan why=x"}, errors.New("read working: boom")
	}}
	var out, errOut bytes.Buffer
	fails := 0
	if code, end := hereCycle(context.Background(), testPresence, f.steps(), &fails, &out, &errOut); code != 0 || end != keepGoing {
		t.Fatalf("code %d end %d", code, end)
	}
	if out.String() != "FRIEND HERE OWNER id=q1~1 state=dead as=rowan why=x\n" || errOut.String() != "friend rowan leases: read working: boom\n" {
		t.Fatalf("stdout %q stderr %q", out.String(), errOut.String())
	}
	if got := strings.Join(f.calls(), ","); got != "beat,leases,poll,take" {
		t.Fatalf("order %s", got)
	}
}

// TestParseHereReply: every answer of ns_friend_here, as the verb reads it.
func TestParseHereReply(t *testing.T) {
	t.Parallel()

	up, err := parseHereReply("rowan", []string{"UP", "4", "0", "1"})
	if err != nil || up.Slots != 4 || up.WasUp || !up.TookOver {
		t.Fatalf("UP: %+v %v", up, err)
	}
	rows := []struct {
		reply []string
		want  string
	}{
		{[]string{"UNREGISTERED"}, "UNREGISTERED rowan: not in the roster; run: nova-config friend add rowan --slots <n> --as <you>, then nova-config apply"},
		{[]string{"NAME-IS-LOGIN", "rowan"}, "NAME-IS-LOGIN rowan: that name is a login alias of a friend, not a friend; run: nova-friend here --as <the friend>"},
		{[]string{"BUSY", "laptop", "s2", "12345"}, "BUSY rowan: a live session is here already on laptop (session s2, beat 12s ago); stop it, or wait a minute and it is taken over"},
		{[]string{"INVALID", "bad alias"}, "--login bad alias wants letters, digits and dashes"},
		{[]string{"LOGIN-IS-FRIEND", "stella"}, "LOGIN-IS-FRIEND rowan: --login stella is a friend's name, not a login alias"},
		{[]string{"LOGIN-TAKEN", "x", "stella"}, "LOGIN-TAKEN rowan: --login x is stella's login already"},
	}
	for _, r := range rows {
		_, err := parseHereReply("rowan", r.reply)
		var ref refusal
		if !errors.As(err, &ref) || ref.why != r.want {
			t.Errorf("%v: %v\nwant refusal %q", r.reply, err, r.want)
		}
	}
	for _, junk := range [][]string{nil, {"INVALID"}, {"WHAT"}, {"UP", "x"}} {
		_, err := parseHereReply("rowan", junk)
		var ref refusal
		if err == nil || errors.As(err, &ref) {
			t.Errorf("%v: %v, want an error that is not a refusal", junk, err)
		}
	}
}

// TestOwnerReportSaysAChangeOnce: a copy's dead, unknown or refused owner
// is one line when it appears, none while it stays, one when it changes.
func TestOwnerReportSaysAChangeOnce(t *testing.T) {
	t.Parallel()

	r := &ownerReport{states: map[string]string{}, refusals: map[string]string{}}
	res := life.FriendBeatResult{Dead: []string{"a~1"}, Unknown: []string{"b~1"}, Refused: []life.OwnerRefusal{{ID: "c~1", Why: "REFUSED STALE"}}}
	first := r.lines("rowan", res)
	if len(first) != 3 || !strings.HasPrefix(first[0], "FRIEND HERE OWNER id=c~1 state=refused as=rowan why=\"REFUSED STALE\"") ||
		!strings.HasPrefix(first[1], "FRIEND HERE OWNER id=a~1 state=dead as=rowan") || !strings.HasPrefix(first[2], "FRIEND HERE OWNER id=b~1 state=unknown as=rowan") {
		t.Fatalf("first pass: %q", first)
	}
	if again := r.lines("rowan", res); len(again) != 0 {
		t.Fatalf("the same states again printed %q", again)
	}
	moved := life.FriendBeatResult{Dead: []string{"b~1"}}
	if lines := r.lines("rowan", moved); len(lines) != 1 || !strings.HasPrefix(lines[0], "FRIEND HERE OWNER id=b~1 state=dead") {
		t.Fatalf("a changed state: %q", lines)
	}
	if lines := r.lines("rowan", res); len(lines) != 3 {
		t.Fatalf("states that came back: %q", lines)
	}
}
