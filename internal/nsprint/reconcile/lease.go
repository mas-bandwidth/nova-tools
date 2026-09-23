// Package reconcile is the nova-sprint reconciler (nova-tools #2756 section
// 5): one process serving every open sprint, holding lease:reconciler with a
// random instance id and a fencing token, and running a pass every second.
//
// A pid is never evidence of liveness (#2726: a dealer lock naming a pid that
// macOS had reused idled the fleet for 4.5 h). The lease is a Redis hash with
// a TTL; a holder that stops renewing loses it when the TTL lapses, and every
// write the holder makes presents its token, so a stale instance that wakes
// up after another took over is refused FENCED and must exit.
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
	// 2 s). Every pass renews at its start and again when it records itself,
	// so the 1 s loop renews every second.
	DefaultTTL = 6 * time.Second

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
}

// Lease is one instance's hold on lease:reconciler.
type Lease struct {
	st       *store.Store
	instance string
	host     string
	token    string
	ttl      time.Duration
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
	reply, err := st.Client().FCall(ctx, fnAcquire, nil,
		instance, nonce, opt.Host, strconv.Itoa(os.Getpid()), ttl.Milliseconds()).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("reconcile acquire: %w", err)
	}
	switch {
	case len(reply) >= 2 && reply[0] == "ACQUIRED":
		return &Lease{st: st, instance: instance, host: opt.Host, token: reply[1], ttl: ttl}, nil
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

// Renew extends the lease to its TTL. ErrFenced means another instance holds
// it, or it lapsed; the caller must stop.
func (l *Lease) Renew(ctx context.Context) error {
	_, err := l.call(ctx, fnRenew, l.token, l.ttl.Milliseconds())
	return err
}

// Release deletes the lease if this instance still holds it, so the next
// start does not wait out the TTL. It never deletes another holder's lease.
func (l *Lease) Release(ctx context.Context) error {
	_, err := l.call(ctx, fnRelease, l.token)
	return err
}

// call runs one fenced reconciler function and maps FENCED to ErrFenced.
func (l *Lease) call(ctx context.Context, name string, args ...any) ([]string, error) {
	if l == nil || l.st == nil {
		return nil, fmt.Errorf("reconcile %s: no lease", name)
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
		return nil, fmt.Errorf("instance=%s%s: %w", l.instance, holder, ErrFenced)
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
