package deal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// fixtureSSHD is a fake `ssh` that stands in for a bench's sshd configured
// like the Studio's on 2026-09-22: it allows TWO concurrent sessions and
// closes every further connection before the remote command runs (exit 255,
// the OpenSSH client's messages). A bench whose dir holds `wedged` closes
// every connection. A bench whose dir holds `dropafter` accepts the session,
// reads the whole batch off stdin (the remote launch already has it) and
// only then drops the connection the way a mid-command network blip would:
// no kex_exchange_identification prefix, so the pre-exec phase is not named
// (#3061 hold 7). A bench whose dir holds `dropafter-reset` does the same but
// with the "Connection reset by" wording instead of "Connection closed by"
// (#3061 hold 7, stella's second pass: the reset wording was still
// unconditional in preExecRefused). A bench whose dir holds
// `dropafter-refused` does the same with plain "Connection refused" (no
// "ssh: connect to host ..." prefix), and `dropafter-timedout` with plain
// "Connection timed out" (#3061 hold 6: both generic phrases were still
// accepted unconditionally, with no connect-phase proof, before this fix).
// A bench whose dir holds `hang` is an ssh client that never returns: it
// starts a grandchild in its process group (pid in grandchild), writes
// nothing on stdout and sleeps, the shape of a session stuck in key
// exchange after the banner, which no ConnectTimeout bounds (#3322); with
// `hang-noisy` it first prints a login profile's noise on stdout, which is
// not the launch verb's voice. Every other accepted session appends one line
// to sessions.log and its stdin to launched, then holds the session for a
// second (or for the seconds in the bench's `sleep` file, #3706), as a slow
// remote verb would; while the bench's `hold` file exists it holds first, so
// a test that needs sessions open together removes the file instead of
// waiting out a second (the unit tier's 1 s budget, #4328). It lives in t.TempDir(), so testguard sees a fake.
const fixtureSSHD = `#!/bin/bash
set -u
FIX=%q
while [ $# -gt 0 ]; do
  case "$1" in
    -o) shift 2 ;;
    *) break ;;
  esac
done
target="$1"; shift
dir="$FIX/$target"
mkdir -p "$dir"
refuse() {
  echo "kex_exchange_identification: Connection closed by remote host" >&2
  echo "Connection closed by 127.0.0.1 port 22" >&2
  echo refused >> "$dir/refused.log"
  exit 255
}
[ -e "$dir/wedged" ] && refuse
if [ -e "$dir/hang" ]; then
  if [ -e "$dir/hang-noisy" ]; then
    echo "Welcome to the bench (bash -lc profile noise)"
    echo "LAUNCHER=local PATH set"
  fi
  sleep 300 &
  echo $! > "$dir/grandchild"
  wait
  exit 0
fi
slot=""
for s in 1 2; do
  if mkdir "$dir/slot$s" 2>/dev/null; then slot="$dir/slot$s"; break; fi
done
[ -z "$slot" ] && refuse
trap 'rmdir "$slot"' EXIT
echo "open $*" >> "$dir/sessions.log"
cat >> "$dir/launched"
if [ -e "$dir/dropafter" ]; then
  echo "Connection closed by 127.0.0.1 port 22" >&2
  exit 255
fi
if [ -e "$dir/dropafter-reset" ]; then
  echo "Connection reset by 127.0.0.1 port 22" >&2
  exit 255
fi
if [ -e "$dir/dropafter-refused" ]; then
  echo "Connection refused" >&2
  exit 255
fi
if [ -e "$dir/dropafter-timedout" ]; then
  echo "Connection timed out" >&2
  exit 255
fi
while [ -e "$dir/hold" ]; do sleep 0.01; done
secs=1
[ -e "$dir/sleep" ] && secs=$(cat "$dir/sleep")
sleep "$secs"
exit 0
`

type fixture struct {
	dir  string
	prog string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	prog := filepath.Join(dir, "ssh")
	if err := testbin.WriteExecutable(prog, []byte(fmt.Sprintf(fixtureSSHD, dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fixture{dir: dir, prog: prog}
}

func (f *fixture) wedge(t *testing.T, bench string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir, bench), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, bench, "wedged"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// hang makes the bench's ssh client hang forever with no output (#3322).
func (f *fixture) hang(t *testing.T, bench string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir, bench), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, bench, "hang"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// hangNoisy is hang with a login profile's noise on stdout first (#3322
// reader's item: output that is not the launch verb's is not a start ack).
func (f *fixture) hangNoisy(t *testing.T, bench string) {
	t.Helper()
	f.hang(t, bench)
	if err := os.WriteFile(filepath.Join(f.dir, bench, "hang-noisy"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// grandchild is the pid the hung ssh client's grandchild wrote, 0 if none.
func (f *fixture) grandchild(bench string) int {
	raw, err := os.ReadFile(filepath.Join(f.dir, bench, "grandchild"))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}

func (f *fixture) dropAfter(t *testing.T, bench string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir, bench), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, bench, "dropafter"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// dropAfterReset is dropAfter's twin for the "Connection reset by" wording
// (#3061 hold 7, stella's second pass).
func (f *fixture) dropAfterReset(t *testing.T, bench string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir, bench), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, bench, "dropafter-reset"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// dropAfterRefused is dropAfter's twin for the plain "Connection refused"
// wording, with no "ssh: connect to host ..." prefix (#3061 hold 6).
func (f *fixture) dropAfterRefused(t *testing.T, bench string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir, bench), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, bench, "dropafter-refused"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// dropAfterTimedOut is dropAfter's twin for the plain "Connection timed out"
// wording, with no "ssh: connect to host ..." prefix (#3061 hold 6).
func (f *fixture) dropAfterTimedOut(t *testing.T, bench string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir, bench), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, bench, "dropafter-timedout"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) lines(bench, name string) []string {
	raw, err := os.ReadFile(filepath.Join(f.dir, bench, name))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

func (f *fixture) remote() Remote {
	return Remote{Program: f.prog, Command: DefaultRemote, ConnectTimeout: 5 * time.Second, RunTimeout: 20 * time.Second}
}

// fakeStore is the reservation and row seam over memory: one lease token,
// card states, per-bench starting reservations, and the row's ssh cells with
// their times. Every write checks the fence as the function would. Fail is
// the one-call shape of ns_card_deal_fail (#3322): row, return, timeouts
// counted, hold reported at failAfter (3, as the function's default).
type fakeStore struct {
	mu        sync.Mutex
	lease     string
	state     map[string]string // sprint/label -> queued|dealt
	bench     map[string]string // sprint/label -> bench while dealt
	attempt   map[string]int
	calls     map[string]int // bench -> Reserve calls
	row       map[string]rowCell
	fails     map[string]int // bench -> Fail calls
	failAfter int
	held      map[string]bool // bench -> a Fail reported the hold
}

type rowCell struct {
	state, why string
	at         time.Time
	timeouts   int
}

func newFakeStore(lease string, in Input) *fakeStore {
	s := &fakeStore{lease: lease, state: map[string]string{}, bench: map[string]string{}, attempt: map[string]int{}, calls: map[string]int{},
		row: map[string]rowCell{}, fails: map[string]int{}, failAfter: 3, held: map[string]bool{}}
	for _, sp := range in.Sprints {
		for _, c := range sp.Pool {
			s.state[key(c)] = "queued"
		}
	}
	return s
}

func (s *fakeStore) Reserve(_ context.Context, fence, bench string, cards []Card) ([]Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fence != s.lease {
		return nil, ErrFenced
	}
	s.calls[bench]++
	var out []Reservation
	for _, c := range cards {
		if s.state[key(c)] != "queued" {
			continue
		}
		s.attempt[key(c)]++
		a := s.attempt[key(c)]
		s.state[key(c)], s.bench[key(c)] = "dealt", bench
		out = append(out, Reservation{Card: c, Bench: bench, Attempt: a, Token: fmt.Sprintf("%d.%s", a, randHex())})
	}
	return out, nil
}

func (s *fakeStore) Unreserve(_ context.Context, fence, bench string, res []Reservation, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fence != s.lease {
		return ErrFenced
	}
	for _, r := range res {
		if s.state[key(r.Card)] == "dealt" && s.bench[key(r.Card)] == bench {
			s.state[key(r.Card)], s.bench[key(r.Card)] = "queued", ""
		}
	}
	return nil
}

func (s *fakeStore) SSH(_ context.Context, fence, bench, state, why string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fence != s.lease {
		return ErrFenced
	}
	timeouts := s.row[bench].timeouts
	if state == SSHOK {
		timeouts = 0
	}
	s.row[bench] = rowCell{state, why, time.Now(), timeouts}
	return nil
}

// Fail is the row write and the batch's return in one fenced step, as
// ns_card_deal_fail does it: one call, so a test that finds the row without
// the return (or the return without the row) has found a second call.
func (s *fakeStore) Fail(_ context.Context, fence, bench, state, why string, res []Reservation) (Failed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fence != s.lease {
		return Failed{}, ErrFenced
	}
	if state != SSHRefused && state != SSHTimeout {
		return Failed{}, fmt.Errorf("fakeStore.Fail: state %q is not refused or timeout", state)
	}
	s.fails[bench]++
	timeouts := s.row[bench].timeouts
	if state == SSHTimeout {
		timeouts++
	}
	s.row[bench] = rowCell{state, why, time.Now(), timeouts}
	n := 0
	for _, r := range res {
		if s.state[key(r.Card)] == "dealt" && s.bench[key(r.Card)] == bench {
			s.state[key(r.Card)], s.bench[key(r.Card)] = "queued", ""
			n++
		}
	}
	hold := state == SSHTimeout && timeouts >= s.failAfter
	if hold {
		s.held[bench] = true
	}
	return Failed{Returned: n, Timeouts: timeouts, State: "UP", Hold: hold}, nil
}

// cell renders the bench row's ssh cell as the table shows it.
func (s *fakeStore) cell(bench string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.row[bench]
	if !ok {
		return "ssh: ?"
	}
	return "ssh: " + c.state
}

func (s *fakeStore) dealtOn(bench string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, st := range s.state {
		if st == "dealt" && s.bench[k] == bench {
			n++
		}
	}
	return n
}

type staticSource struct{ in Input }

func (s staticSource) Read(context.Context) (Input, error) { return s.in, nil }

// freshSource builds a new Input on every read, as the Redis source does.
type freshSource func() Input

func (f freshSource) Read(context.Context) (Input, error) { return f(), nil }

// eventWait is the generous bound for an event, NOVA_TEST_WAIT or thirty
// seconds; the assertions are on events, never on elapsed time.
func eventWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("NOVA_TEST_WAIT")); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

// runPass runs one deal pass and fails the test if it has not returned
// inside the event wait: a session the deadline did not cut holds it.
func runPass(t *testing.T, p *Pass) (Result, error) {
	t.Helper()
	type out struct {
		res Result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := p.Run(context.Background())
		done <- out{res, err}
	}()
	select {
	case o := <-done:
		return o.res, o.err
	case <-time.After(eventWait()):
		t.Fatal("the deal pass did not return inside the event wait: a bench session held it past its deadline")
		return Result{}, nil
	}
}

// processGone polls until the pid names no live process, up to the event
// wait, and reports whether it did.
func processGone(pid int) bool {
	bound := time.After(eventWait())
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if !processAlive(pid) {
			return true
		}
		select {
		case <-bound:
			return false
		case <-tick.C:
		}
	}
}

type fence string

func (f fence) Token(context.Context) (string, error) { return string(f), nil }

func randHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func fiftyCards(sprint string) []Card {
	cards := make([]Card, 50)
	for i := range cards {
		cards[i] = Card{Sprint: sprint, Label: fmt.Sprintf("card-%02d", i), Priority: float64(i)}
	}
	return cards
}

func upBench(name string, slots int) Bench {
	return Bench{Name: name, Host: name, Up: true, Slots: slots}
}

// perCardLauncher is the launcher of 2026-09-22: one session per card, all at
// once, each held for the card's run.
type perCardLauncher struct{}

func (perCardLauncher) Launch(ctx context.Context, open Opener, _ Bench, res []Reservation) error {
	errs := make([]error, len(res))
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := open(ctx)
			if err != nil {
				errs[i] = err
				return
			}
			errs[i] = s.Run(ctx, Lines(res[i:i+1]))
		}(i)
	}
	wg.Wait()
	return errors.Join(errs...)
}

var lineRE = regexp.MustCompile(`^control-0000c012 card-\d\d 1 1\.[0-9a-f]{32}$`)

// TestControl12FiftyCardsOneSession is #2756 control 12 (#2743): a 50-card
// batch to a bench whose sshd allows two sessions launches over ONE session;
// a launcher that opens a session per card is refused; a wedged sshd shows
// `ssh: refused` on the bench row within 10 s and its cards go elsewhere.
func TestControl12FiftyCardsOneSession(t *testing.T) {
	t.Parallel()

	const sprint = "control-0000c012"
	ctx := context.Background()

	t.Run("fixture sshd allows two sessions", func(t *testing.T) {
		// The positive control on the fixture itself: 50 sessions at once,
		// the old launcher's shape, are mostly closed before the command.
		// The two sessions it accepts hold until every other one is refused,
		// so the control is exact (48 of 50) and waits on no clock.
		f := newFixture(t)
		r := f.remote()
		b := upBench("ctl-probe", 64)
		f.set(t, b.Name, "sleep", "0")
		f.set(t, b.Name, "hold", "")
		hold := filepath.Join(f.dir, b.Name, "hold")
		results := make(chan error, 50)
		for i := 0; i < 50; i++ {
			go func() { results <- r.Dial(b).Run(ctx, []byte("x\n")) }()
		}
		bound := time.NewTimer(guard)
		defer bound.Stop()
		refused := 0
		for got := 0; got < 50; got++ {
			select {
			case err := <-results:
				var se *SessionError
				if !errors.As(err, &se) || se.State != SSHRefused {
					continue
				}
				if refused++; refused == 48 {
					if err := os.Remove(hold); err != nil {
						t.Fatal(err)
					}
				}
			case <-bound.C:
				_ = os.Remove(hold)
				t.Fatalf("fixture refused %d of 50 concurrent sessions within %s; it must allow only two", refused, guard)
			}
		}
		if refused != 48 {
			t.Fatalf("fixture refused %d of 50 concurrent sessions; it must allow exactly two", refused)
		}
	})

	t.Run("fifty cards launch over one session", func(t *testing.T) {
		f := newFixture(t)
		f.set(t, "ctl-a", "sleep", "0")
		in := Input{Now: time.Now(), Benches: []Bench{upBench("ctl-a", 64)}, Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)}}}
		st := newFakeStore("lease-1", in)
		p := &Pass{Source: staticSource{in}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
		start := time.Now()
		res, err := p.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Launched(); got != 50 {
			t.Fatalf("launched %d, want 50 (ssh=%s why=%q sessions=%d)", got, res.Benches[0].SSH, res.Benches[0].Why, res.Benches[0].Sessions)
		}
		if st.calls["ctl-a"] != 1 {
			t.Fatalf("reserve calls on ctl-a = %d, want 1 (one function call per bench)", st.calls["ctl-a"])
		}
		sessions := f.lines("ctl-a", "sessions.log")
		if len(sessions) != 1 {
			t.Fatalf("ssh sessions to ctl-a = %d, want exactly 1: %q", len(sessions), sessions)
		}
		if refused := f.lines("ctl-a", "refused.log"); len(refused) != 0 {
			t.Fatalf("fixture sshd refused %d sessions", len(refused))
		}
		launched := f.lines("ctl-a", "launched")
		if len(launched) != 50 {
			t.Fatalf("card launch --stdin got %d lines, want 50", len(launched))
		}
		seen := map[string]bool{}
		for _, l := range launched {
			if !lineRE.MatchString(l) {
				t.Fatalf("stdin line %q is not `<S> <label> <attempt> <token>`", l)
			}
			seen[strings.Fields(l)[1]] = true
		}
		if len(seen) != 50 {
			t.Fatalf("%d distinct cards on stdin, want 50", len(seen))
		}
		if c := st.cell("ctl-a"); c != "ssh: ok" {
			t.Fatalf("row cell %q, want ssh: ok", c)
		}
		if res.Benches[0].Sessions != 1 {
			t.Fatalf("result counts %d sessions", res.Benches[0].Sessions)
		}
		t.Logf("50 cards, 1 session, 1 reserve call, %s", time.Since(start).Round(time.Millisecond))
	})

	t.Run("a launcher that opens a session per card is refused", func(t *testing.T) {
		f := newFixture(t)
		f.set(t, "ctl-a", "sleep", "0")
		in := Input{Now: time.Now(), Benches: []Bench{upBench("ctl-a", 64)}, Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)}}}
		st := newFakeStore("lease-1", in)
		p := &Pass{Source: staticSource{in}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote(), Launcher: perCardLauncher{}}
		res, err := p.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		br := res.Benches[0]
		if br.SSH == SSHOK || !strings.Contains(br.Why, ErrSessionPerCard.Error()) {
			t.Fatalf("per-card launcher was not refused: ssh=%s why=%q", br.SSH, br.Why)
		}
		if br.Sessions != 1 {
			t.Fatalf("the pass let a per-card launcher open %d sessions", br.Sessions)
		}
		if got := len(f.lines("ctl-a", "sessions.log")); got > 1 {
			t.Fatalf("per-card launcher reached the bench's sshd %d times", got)
		}
		if res.Launched() != 0 {
			t.Fatalf("a refused launcher counted %d launched", res.Launched())
		}
	})

	t.Run("a wedged sshd shows ssh refused within 10 s and its cards go elsewhere", func(t *testing.T) {
		f := newFixture(t)
		f.wedge(t, "ctl-a")
		f.set(t, "ctl-b", "sleep", "0")
		in := Input{Now: time.Now(), Benches: []Bench{upBench("ctl-a", 64), upBench("ctl-b", 50)}, Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)}}}
		st := newFakeStore("lease-1", in)
		p := &Pass{Source: staticSource{in}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
		start := time.Now()
		res, err := p.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if c := st.cell("ctl-a"); c != "ssh: refused" {
			t.Fatalf("wedged bench row %q, want ssh: refused", c)
		}
		if at := st.row["ctl-a"].at.Sub(start); at > 10*time.Second {
			t.Fatalf("ssh: refused reached the row after %s, want within 10 s", at)
		}
		if n := st.dealtOn("ctl-a"); n != 0 {
			t.Fatalf("%d reservations stayed on the wedged bench", n)
		}
		if st.fails["ctl-a"] != 1 {
			t.Fatalf("Fail calls on ctl-a = %d, want 1: the row and the return are one call", st.fails["ctl-a"])
		}
		if n := st.dealtOn("ctl-b"); n != 50 {
			t.Fatalf("%d cards went to ctl-b, want all 50", n)
		}
		if got := len(f.lines("ctl-b", "launched")); got != 50 {
			t.Fatalf("ctl-b launched %d, want 50", got)
		}
		if got := len(f.lines("ctl-b", "sessions.log")); got != 1 {
			t.Fatalf("ctl-b sessions %d, want 1", got)
		}
		if res.Launched() != 50 || res.Rounds != 2 {
			t.Fatalf("launched %d in %d rounds, want 50 in 2", res.Launched(), res.Rounds)
		}
		// The next pass skips the refused bench while the hold stands.
		for _, b := range Plan(Input{Now: time.Now(), Benches: []Bench{{Name: "ctl-a", Up: true, Slots: 64, SSH: SSHRefused, SSHAt: time.Now()}}, Sprints: in.Sprints}, DefaultRefusedHold) {
			t.Fatalf("a refused bench was planned %d cards inside the hold", len(b.Cards))
		}
	})

	t.Run("a stale reconciler token deals nothing", func(t *testing.T) {
		f := newFixture(t)
		in := Input{Now: time.Now(), Benches: []Bench{upBench("ctl-a", 64)}, Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)}}}
		st := newFakeStore("lease-2", in)
		p := &Pass{Source: staticSource{in}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
		if _, err := p.Run(ctx); !errors.Is(err, ErrFenced) {
			t.Fatalf("stale token: err %v, want FENCED", err)
		}
		if got := len(f.lines("ctl-a", "sessions.log")); got != 0 {
			t.Fatalf("a fenced pass opened %d sessions", got)
		}
	})
}

// TestPostCommandDisconnectKeepsReservationsDealt is #3061 hold 7 (stella):
// a connection that drops AFTER the remote command already has the batch on
// stdin must not be classified as pre-exec. Classifying it SSHRefused would
// return the reservations to the pool and this pass (or the next one) would
// deal the same 50 cards again while the first launch may still be running.
func TestPostCommandDisconnectKeepsReservationsDealt(t *testing.T) {
	t.Parallel()

	const sprint = "control-3061-hold7"
	ctx := context.Background()
	f := newFixture(t)
	f.dropAfter(t, "ctl-a")
	in := Input{Now: time.Now(), Benches: []Bench{upBench("ctl-a", 64)}, Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)}}}
	st := newFakeStore("lease-1", in)
	p := &Pass{Source: staticSource{in}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(f.lines("ctl-a", "launched")); got != 50 {
		t.Fatalf("card launch --stdin got %d lines, want 50 (the batch must reach the remote command before the drop)", got)
	}
	br := res.Benches[0]
	if br.SSH != SSHError {
		t.Fatalf("post-command disconnect classified %s, want %s: it is ambiguous, not pre-exec", br.SSH, SSHError)
	}
	if br.Returned != 0 {
		t.Fatalf("post-command disconnect returned %d reservations to the pool, want 0", br.Returned)
	}
	if n := st.dealtOn("ctl-a"); n != 50 {
		t.Fatalf("%d of 50 cards stayed dealt on ctl-a after a post-command disconnect, want all 50 retained", n)
	}
	if res.Launched() != 0 {
		t.Fatalf("res.Launched() = %d, want 0: the pass does not know the batch succeeded", res.Launched())
	}
}

// TestPostCommandResetKeepsReservationsDealt is #3061 hold 7's second pass
// (stella): the same ambiguity as TestPostCommandDisconnectKeepsReservationsDealt,
// but for "Connection reset by" instead of "Connection closed by". Both
// bare messages used to be accepted unconditionally by preExecRefused before
// any connect-phase-context check ran, so a mid-command reset would return
// the reservations to the pool and deal the same 50 cards again.
func TestPostCommandResetKeepsReservationsDealt(t *testing.T) {
	t.Parallel()

	const sprint = "control-3061-hold7-reset"
	ctx := context.Background()
	f := newFixture(t)
	f.dropAfterReset(t, "ctl-a")
	in := Input{Now: time.Now(), Benches: []Bench{upBench("ctl-a", 64)}, Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)}}}
	st := newFakeStore("lease-1", in)
	p := &Pass{Source: staticSource{in}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(f.lines("ctl-a", "launched")); got != 50 {
		t.Fatalf("card launch --stdin got %d lines, want 50 (the batch must reach the remote command before the drop)", got)
	}
	br := res.Benches[0]
	if br.SSH != SSHError {
		t.Fatalf("post-command reset classified %s, want %s: it is ambiguous, not pre-exec", br.SSH, SSHError)
	}
	if br.Returned != 0 {
		t.Fatalf("post-command reset returned %d reservations to the pool, want 0", br.Returned)
	}
	if n := st.dealtOn("ctl-a"); n != 50 {
		t.Fatalf("%d of 50 cards stayed dealt on ctl-a after a post-command reset, want all 50 retained", n)
	}
	if res.Launched() != 0 {
		t.Fatalf("res.Launched() = %d, want 0: the pass does not know the batch succeeded", res.Launched())
	}
}

// TestPostCommandRefusedKeepsReservationsDealt is #3061 hold 6 (stella): the
// generic "Connection refused" wording was still accepted unconditionally by
// preExecRefused, with no connect-phase proof, even though it can be printed
// after the remote launch already has the batch on stdin.
func TestPostCommandRefusedKeepsReservationsDealt(t *testing.T) {
	t.Parallel()

	const sprint = "control-3061-hold6-refused"
	ctx := context.Background()
	f := newFixture(t)
	f.dropAfterRefused(t, "ctl-a")
	in := Input{Now: time.Now(), Benches: []Bench{upBench("ctl-a", 64)}, Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)}}}
	st := newFakeStore("lease-1", in)
	p := &Pass{Source: staticSource{in}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(f.lines("ctl-a", "launched")); got != 50 {
		t.Fatalf("card launch --stdin got %d lines, want 50 (the batch must reach the remote command before the drop)", got)
	}
	br := res.Benches[0]
	if br.SSH != SSHError {
		t.Fatalf("post-command refused classified %s, want %s: it is ambiguous, not pre-exec", br.SSH, SSHError)
	}
	if br.Returned != 0 {
		t.Fatalf("post-command refused returned %d reservations to the pool, want 0", br.Returned)
	}
	if n := st.dealtOn("ctl-a"); n != 50 {
		t.Fatalf("%d of 50 cards stayed dealt on ctl-a after a post-command refused, want all 50 retained", n)
	}
	if res.Launched() != 0 {
		t.Fatalf("res.Launched() = %d, want 0: the pass does not know the batch succeeded", res.Launched())
	}
}

// TestPostCommandTimedOutKeepsReservationsDealt is #3061 hold 6's twin for
// the generic "Connection timed out" wording, the second phrase stella
// named as still unconditional in preExecTimeout.
func TestPostCommandTimedOutKeepsReservationsDealt(t *testing.T) {
	t.Parallel()

	const sprint = "control-3061-hold6-timedout"
	ctx := context.Background()
	f := newFixture(t)
	f.dropAfterTimedOut(t, "ctl-a")
	in := Input{Now: time.Now(), Benches: []Bench{upBench("ctl-a", 64)}, Sprints: []Sprint{{Name: sprint, Pool: fiftyCards(sprint)}}}
	st := newFakeStore("lease-1", in)
	p := &Pass{Source: staticSource{in}, Fence: fence("lease-1"), Reserver: st, Row: st, Dialer: f.remote()}
	res, err := p.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(f.lines("ctl-a", "launched")); got != 50 {
		t.Fatalf("card launch --stdin got %d lines, want 50 (the batch must reach the remote command before the drop)", got)
	}
	br := res.Benches[0]
	if br.SSH != SSHError {
		t.Fatalf("post-command timed out classified %s, want %s: it is ambiguous, not pre-exec", br.SSH, SSHError)
	}
	if br.Returned != 0 {
		t.Fatalf("post-command timed out returned %d reservations to the pool, want 0", br.Returned)
	}
	if n := st.dealtOn("ctl-a"); n != 50 {
		t.Fatalf("%d of 50 cards stayed dealt on ctl-a after a post-command timed out, want all 50 retained", n)
	}
	if res.Launched() != 0 {
		t.Fatalf("res.Launched() = %d, want 0: the pass does not know the batch succeeded", res.Launched())
	}
}

func TestPlanSharesAndFilters(t *testing.T) {
	t.Parallel()

	now := time.Now()
	a := []Card{{Sprint: "a", Label: "a1", Priority: 1}, {Sprint: "a", Label: "a2", Priority: 2}, {Sprint: "a", Label: "a3", Priority: 3}, {Sprint: "a", Label: "a4", Priority: 4}}
	b := []Card{{Sprint: "b", Label: "b1", Priority: 1, Tier: TierPriority}, {Sprint: "b", Label: "b2", Priority: 0}}
	in := Input{Now: now,
		Benches: []Bench{{Name: "x", Up: true, Slots: 4, Leased: 1}, {Name: "y", Up: true, Paused: true, Slots: 9}, {Name: "z", Up: false, Slots: 9}},
		Sprints: []Sprint{{Name: "a", Share: 2, Pool: a}, {Name: "b", Share: 1, Backpressure: true, Pool: b}},
	}
	got := Plan(in, DefaultRefusedHold)
	if len(got) != 1 || got[0].Bench.Name != "x" {
		t.Fatalf("plan %+v: only the up, unpaused bench deals", got)
	}
	var labels []string
	for _, c := range got[0].Cards {
		labels = append(labels, c.Label)
	}
	// free 3, shares 2:1; b is under backpressure so only its priority tier flows.
	if strings.Join(labels, ",") != "a1,a2,b1" {
		t.Fatalf("planned %v, want a1,a2,b1", labels)
	}
	in.Sprints[0].Pool = append(in.Sprints[0].Pool, Card{Sprint: "a", Label: "pinned", Priority: -1, Bench: "w"}, Card{Sprint: "a", Label: "gpu", Priority: -2, Leg: "gpu"})
	in.Benches[0].Legs = []string{"go"}
	for _, c := range Plan(in, DefaultRefusedHold)[0].Cards {
		if c.Label == "pinned" || c.Label == "gpu" {
			t.Fatalf("card %s dealt to a bench it is not eligible for", c.Label)
		}
	}
}

func TestClassifyOpenSSHMessages(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		exit   int
		stderr string
		want   string
	}{
		{0, "", SSHOK},
		{255, "kex_exchange_identification: Connection closed by remote host\r\nConnection closed by 100.64.0.7 port 22", SSHRefused},
		{255, "ssh: connect to host studio port 22: Connection refused", SSHRefused},
		{255, "ssh: connect to host studio port 22: Connection reset by peer", SSHRefused},
		{255, "Connection timed out during banner exchange", SSHTimeout},
		{255, "ssh: connect to host studio port 22: Operation timed out", SSHTimeout},
		{255, "Host key verification failed.", SSHError},
		{1, "card launch: refused", SSHError},
		// #3061 hold 7: a mid-command drop can print the same wording the
		// pre-exec phase uses, but with no phase context (no "kex_exchange_
		// identification" prefix, no "connect to host"). Ambiguous, so it
		// stays SSHError rather than returning reservations to the pool.
		{255, "Connection closed by 100.64.0.7 port 22", SSHError},
		{255, "Operation timed out", SSHError},
		// #3061 hold 7, stella's second pass: "connection reset by" is the
		// same ambiguity as "connection closed by" and must require the
		// same connect-phase context.
		{255, "Connection reset by 100.64.0.7 port 22", SSHError},
		// #3061 hold 6: the generic "connection refused" and "connection
		// timed out" phrases were still accepted unconditionally, with no
		// connect-phase proof; bare, they are just as ambiguous as the
		// closed/reset/operation-timed-out cases above.
		{255, "Connection refused", SSHError},
		{255, "Connection timed out", SSHError},
	} {
		if got := Classify(tc.exit, tc.stderr); got != tc.want {
			t.Errorf("Classify(%d, %q) = %s, want %s", tc.exit, tc.stderr, got, tc.want)
		}
	}
}

// TestLaunchAckedReadsOnlyTheVerbsLines: the start ack is a line the launch
// verb wrote (LAUNCHED, REFUSED, LAUNCH); a login profile's noise is not.
func TestLaunchAckedReadsOnlyTheVerbsLines(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		out  string
		want bool
	}{
		{"", false},
		{"Welcome to the bench\nLAUNCHER=local PATH set\n", false},
		{"launched card-00 pid=1\n", false},
		{"Welcome\nLAUNCHED control/card-00/1 pid=4242\n", true},
		{"REFUSED line=1 malformed: x\n", true},
		{"LAUNCH started=3 refused=0 ms=12 over=false\n", true},
		{"  LAUNCHED control/card-01/1 pid=1\n", true},
	} {
		if got := LaunchAcked(tc.out); got != tc.want {
			t.Errorf("LaunchAcked(%q) = %t, want %t", tc.out, got, tc.want)
		}
	}
}
