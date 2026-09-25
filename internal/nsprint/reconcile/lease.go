// Package reconcile is the nova-sprint reconciler (nova-tools #2756 section
// 5): one process serving every open sprint, holding lease:reconciler with a
// random instance id and a fencing token, and running a pass every second.
//
// A pid is never evidence of liveness (#2726: a dealer lock naming a pid that
// macOS had reused idled the fleet for 4.5 h). The lease is a Redis hash with
// a TTL; a holder that stops renewing loses it when the TTL lapses, and every
// write the holder makes presents its token, so a stale instance that wakes
// up after another took over is refused FENCED and must exit.
//
// THE HEARTBEAT (#3737, the Studio 2026-09-24 ~04:00Z to 2026-09-25 05:07Z,
// 214 restarts). The lease was renewed only when a pass began and recorded
// itself, and by the deal pass before each bench session (#3706). A pass whose
// duties ran past the 6 s TTL lost the lease mid-duty, the next fenced write
// was FENCED, and every new instance died the same way after one pass,
// killing its harvest workers before they recorded (proc:harvest:<b> n=0
// err=) and leaving lease:harvest:<b> held. So a lease started with
// AcquireOptions.Heartbeat renews itself on its own goroutine every TTL/3
// from Acquire until Release, whatever the duties are doing. Every renewal
// (the heartbeat, the pass start, the deal pass before a session) is the one
// coalesced Renew: a renewal sent within RenewAfter of the last successful
// one is served by it, and concurrent callers share the one in flight. A
// renewal refused FENCED, or no successful renewal for a whole TTL (Redis
// unreachable), fences the lease for good: Fenced and Done report it, every
// call through the lease refuses without a round trip, Deadline is the zero
// time (Remaining 0, so no bench session opens), and the pass loop exits as
// before. The token never changes.
package reconcile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

const (
	// LeaseKey is the reconciler lease hash (spec 2.2): instance, token,
	// host, pid (informational only, never read by any guard), at.
	LeaseKey = "lease:reconciler"
	// ProcKey is the reconciler's pass record (spec 2.2 proc:<name>, 5.1.2):
	// pass_at, took_ms, dealt, routed, expired, n, err, instance, host, at.
	ProcKey = "proc:reconciler"

	// DefaultTTL is the lease TTL (spec 2.2: TTL 6 s, renew at least every
	// 2 s). The heartbeat renews every TTL/3 (#3737); every pass also renews
	// at its start and again when it records itself.
	DefaultTTL = 6 * time.Second

	// DefaultRenewAfter is how long one successful renewal serves the
	// renewals asked for after it (#3706's window, now the lease's own): the
	// heartbeat, the pass start and every deal worker share it.
	DefaultRenewAfter = 500 * time.Millisecond

	fnAcquire = "ns_reconciler_acquire"
	fnRenew   = "ns_reconciler_renew"
	fnPass    = "ns_reconciler_pass"
	fnRelease = "ns_reconciler_release"
)

// ErrFenced is a call made with a token that no longer holds the lease. The
// caller MUST stop: another instance owns the reconciler (exit 3).
var ErrFenced = errors.New("FENCED: lease:reconciler is held by another instance")

// HeldError is an acquire refused because another instance holds the lease
// (a second instance refuses and exits, spec 5.1.1).
type HeldError struct {
	Instance string
	Host     string
	AgeMS    int64 // since the holder's last renewal, Redis time
	TTLMS    int64 // left on the holder's lease
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("lease:reconciler held by instance=%s host=%s renewed=%dms ago ttl=%dms",
		e.Instance, e.Host, e.AgeMS, e.TTLMS)
}

// AcquireOptions names the instance taking the lease.
type AcquireOptions struct {
	Host     string        // the machine, for the table and the refusal line
	TTL      time.Duration // DefaultTTL when zero
	Instance string        // random when empty; a restart is a new instance
	// Clock is the lease's local clock (#3322): when each renewal was sent,
	// and the timer a bench session is bounded by. Nil is the wall clock; a
	// test injects a fake one.
	Clock Clock
	// Heartbeat starts the lease's own renewal goroutine (#3737): every
	// TTL/3 on Clock from Acquire until Release. The reconcile verb always
	// sets it; a test that models a hung holder leaves it off.
	Heartbeat bool
	// RenewAfter is the coalescing window of Renew; zero is
	// DefaultRenewAfter. It is capped at TTL/12.
	RenewAfter time.Duration
	// RenewSeam, when set, is injected into Lease.RenewSeam (#3838).
	RenewSeam func(ctx context.Context) error
}

// Clock is the lease's local clock. The lease deadline is the local time a
// renewal was sent plus the TTL: Redis sets the TTL when the call executes,
// which is never before it was sent, so the deadline is never late.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time                         { return time.Now() }
func (wallClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Lease is one instance's hold on lease:reconciler.
type Lease struct {
	st       *store.Store
	instance string
	host     string
	token    string
	ttl      time.Duration
	clock    Clock

	renewAfter time.Duration
	every      time.Duration // heartbeat interval; zero when there is none

	// RenewSeam, when set, is called by Renew instead of the Redis call; a
	// test injects a failure here to test a missed renewal (#3838).
	RenewSeam func(ctx context.Context) error

	mu      sync.Mutex
	renewed time.Time // local time the last successful renewal was sent
	fence   error     // why the lease is fenced; nil while it holds

	rmu    sync.Mutex    // one renewal on the wire at a time; waiters share it
	fenced chan struct{} // closed when the lease is fenced
	stop   chan struct{} // closed by Release to end the heartbeat
	beat   chan struct{} // closed when the heartbeat goroutine has returned
	once   sync.Once     // closes stop
}

// Acquire takes lease:reconciler if no instance holds it. It never inspects a
// pid: an unrenewed lease lapses on its Redis TTL and nothing else.
func Acquire(ctx context.Context, st *store.Store, opt AcquireOptions) (*Lease, error) {
	if st == nil {
		return nil, fmt.Errorf("reconcile acquire: nil store")
	}
	ttl := opt.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl < time.Millisecond {
		return nil, fmt.Errorf("reconcile acquire: ttl %s is below 1 ms", ttl)
	}
	instance := opt.Instance
	if instance == "" {
		instance = randomHex(8)
	}
	nonce := randomHex(16)
	clock := opt.Clock
	if clock == nil {
		clock = wallClock{}
	}
	sent := clock.Now()
	reply, err := st.Client().FCall(ctx, fnAcquire, nil,
		instance, nonce, opt.Host, strconv.Itoa(os.Getpid()), ttl.Milliseconds()).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("reconcile acquire: %w", err)
	}
	switch {
	case len(reply) >= 2 && reply[0] == "ACQUIRED":
		l := &Lease{st: st, instance: instance, host: opt.Host, token: reply[1], ttl: ttl, clock: clock, renewed: sent,
			renewAfter: opt.RenewAfter, RenewSeam: opt.RenewSeam, fenced: make(chan struct{}), stop: make(chan struct{}), beat: make(chan struct{})}
		if l.renewAfter <= 0 {
			l.renewAfter = DefaultRenewAfter
		}
		// A renewal serves others only while it is fresh against the TTL:
		// never more than TTL/12 (500 ms at 6 s), so a short test TTL is
		// never served by a renewal that has since lapsed.
		l.renewAfter = min(l.renewAfter, ttl/12)
		if opt.Heartbeat {
			l.every = HeartbeatEvery(ttl)
			go l.heartbeat()
		} else {
			close(l.beat)
		}
		return l, nil
	case len(reply) >= 5 && reply[0] == "HELD":
		age, _ := strconv.ParseInt(reply[3], 10, 64)
		left, _ := strconv.ParseInt(reply[4], 10, 64)
		return nil, &HeldError{Instance: reply[1], Host: reply[2], AgeMS: age, TTLMS: left}
	default:
		return nil, fmt.Errorf("reconcile acquire: unexpected reply %q", reply)
	}
}

// Instance is this holder's random id.
func (l *Lease) Instance() string { return l.instance }

// Host is the machine this instance named at acquire.
func (l *Lease) Host() string { return l.host }

// Token is the fencing token. A reconciler duty passes it into every Redis
// Function it calls, and that function refuses FENCED unless it equals the
// stored lease:reconciler token (see reconciler.lua).
func (l *Lease) Token() string { return l.token }

// TokenSHA is the first 12 hex of sha256(token), the form receipts carry.
func (l *Lease) TokenSHA() string {
	sum := sha256.Sum256([]byte(l.token))
	return hex.EncodeToString(sum[:])[:12]
}

// TTL is the lease TTL this instance renews to.
func (l *Lease) TTL() time.Duration { return l.ttl }

// HeartbeatEvery is the heartbeat interval for a lease TTL: TTL/3, so two
// renewals can be lost before the lease lapses (2 s at the 6 s TTL).
func HeartbeatEvery(ttl time.Duration) time.Duration { return ttl / 3 }

// Heartbeat is this lease's heartbeat interval, zero when it has none.
func (l *Lease) Heartbeat() time.Duration { return l.every }

// Renew extends the lease to its TTL. It is the one renewal path (#3737): a
// renewal sent within RenewAfter of the last successful one is served by it
// and sends nothing, and a caller that finds a renewal on the wire waits for
// it and is served by it too. ErrFenced means another instance holds the
// lease, or it lapsed; the caller must stop, and every later call through
// this lease refuses the same way.
func (l *Lease) Renew(ctx context.Context) error {
	if err := l.fencedErr(); err != nil {
		return err
	}
	l.rmu.Lock()
	defer l.rmu.Unlock()
	if err := l.fencedErr(); err != nil {
		return err
	}
	if last := l.lastRenewed(); !last.IsZero() && l.now().Sub(last) < l.renewAfter {
		return nil
	}
	sent := l.now()
	if l.RenewSeam != nil {
		err := l.RenewSeam(ctx)
		if errors.Is(err, ErrFenced) {
			l.Fence(err)
		}
		if err == nil {
			l.renewedAt(sent)
		}
		return err
	}
	_, err := l.call(ctx, fnRenew, l.token, l.ttl.Milliseconds())
	if err == nil {
		l.renewedAt(sent)
	}
	return err
}

// heartbeat renews the lease every l.every until Release or a fence. A
// renewal that fails on the wire is retried at the next beat; once a whole
// TTL has passed since the last successful renewal the Redis lease has
// lapsed, so the lease is fenced.
func (l *Lease) heartbeat() {
	defer close(l.beat)
	for {
		select {
		case <-l.stop:
			return
		case <-l.fenced:
			return
		case <-l.Clock().After(l.every):
		}
		ctx, cancel := context.WithTimeout(context.Background(), l.every)
		err := l.Renew(ctx)
		cancel()
		switch {
		case err == nil:
		case errors.Is(err, ErrFenced):
			return
		case !l.now().Before(l.deadline()):
			l.Fence(fmt.Errorf("instance=%s: heartbeat: no renewal for the lease TTL %s (%v): %w", l.instance, l.ttl, err, ErrFenced))
			return
		}
	}
}

// Fence marks the lease lost for good; err must wrap ErrFenced. The loop
// calls it when a duty's own fenced write was refused, so every other
// holder of the lease (a harvest worker, a bench session) sees it at once.
func (l *Lease) Fence(err error) {
	if err == nil || !errors.Is(err, ErrFenced) {
		err = fmt.Errorf("instance=%s: %v: %w", l.instance, err, ErrFenced)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fence != nil {
		return
	}
	l.fence = err
	if l.fenced != nil {
		close(l.fenced)
	}
}

// Fenced is true once the lease is lost: a renewal or pass record refused
// FENCED, a duty's fenced write refused (Fence), or no successful renewal
// for a whole TTL.
func (l *Lease) Fenced() bool { return l.fencedErr() != nil }

// Done is closed when the lease is fenced.
func (l *Lease) Done() <-chan struct{} { return l.fenced }

func (l *Lease) fencedErr() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fence
}

func (l *Lease) lastRenewed() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.renewed
}

// Deadline is when this instance's hold lapses on the lease clock: the local
// time the last successful renewal (acquire, renew, or the pass record) was
// sent, plus the TTL. A write presented after it may be refused FENCED, so
// every bench session in a pass is bounded inside it (#3322). A fenced lease
// has no deadline: the zero time, so Remaining is 0 and nothing new starts.
func (l *Lease) Deadline() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.renewed.IsZero() || l.fence != nil {
		return time.Time{}
	}
	return l.renewed.Add(l.ttl)
}

// deadline is the last renewal plus the TTL, fenced or not.
func (l *Lease) deadline() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.renewed.Add(l.ttl)
}

// Remaining is the lease time left on the lease clock; zero or less when the
// deadline has passed.
func (l *Lease) Remaining() time.Duration {
	d := l.Deadline()
	if d.IsZero() {
		return 0
	}
	return d.Sub(l.now())
}

// Clock is the lease's clock (the wall clock unless one was injected).
func (l *Lease) Clock() Clock {
	if l.clock == nil {
		return wallClock{}
	}
	return l.clock
}

func (l *Lease) now() time.Time { return l.Clock().Now() }

func (l *Lease) renewedAt(sent time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if sent.After(l.renewed) {
		l.renewed = sent
	}
}

// Release stops the heartbeat and deletes the lease if this instance still
// holds it, so the next start does not wait out the TTL. It never deletes
// another holder's lease.
func (l *Lease) Release(ctx context.Context) error {
	l.StopHeartbeat()
	_, err := l.call(ctx, fnRelease, l.token)
	return err
}

// StopHeartbeat ends the heartbeat goroutine and waits for it; the lease then
// lapses on its TTL unless something else renews it. Release calls it.
func (l *Lease) StopHeartbeat() {
	if l == nil || l.stop == nil {
		return
	}
	l.once.Do(func() { close(l.stop) })
	<-l.beat
}

// call runs one fenced reconciler function and maps FENCED to ErrFenced.
func (l *Lease) call(ctx context.Context, name string, args ...any) ([]string, error) {
	if l == nil || l.st == nil {
		return nil, fmt.Errorf("reconcile %s: no lease", name)
	}
	if err := l.fencedErr(); err != nil {
		return nil, err
	}
	reply, err := l.st.Client().FCall(ctx, name, nil, args...).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("reconcile %s: %w", name, err)
	}
	if len(reply) == 0 {
		return nil, fmt.Errorf("reconcile %s: empty reply", name)
	}
	if reply[0] == "FENCED" {
		holder := ""
		if len(reply) >= 3 && reply[1] != "" {
			holder = fmt.Sprintf(" (holder instance=%s host=%s)", reply[1], reply[2])
		}
		err := fmt.Errorf("instance=%s%s: %w", l.instance, holder, ErrFenced)
		l.Fence(err)
		return nil, err
	}
	return reply, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("reconcile: crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}
