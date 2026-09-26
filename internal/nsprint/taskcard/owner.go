package taskcard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ProcessOwner identifies a process independently of its reusable PID. Start
// is an OS process creation identity, never a harness label or a child UUID.
// The binding is immutable for the copy's current lease token.
type ProcessOwner struct {
	Host  string
	PID   int
	Start string
}

func (p ProcessOwner) check() error {
	if p.Host == "" || p.PID <= 0 || p.Start == "" || p.Start == "-" || strings.ContainsAny(p.Host+p.Start, " \t\r\n") {
		return fmt.Errorf("owner requires host, positive pid and process creation identity")
	}
	return nil
}

const FnOwner = "ns_cm_owner"

// BindOwner records a process for a currently leased copy. Repeating the same
// binding is harmless; changing it requires a new attempt/token. It never beats.
func BindOwner(ctx context.Context, c redis.Cmdable, as Consumer, id, token string, p ProcessOwner) error {
	if err := p.check(); err != nil {
		return err
	}
	_, err := ownerCall(ctx, c, as, id, token, "bind", p, "", 0)
	return err
}

// OwnerObservation is an observation of exactly the process bound to this
// token. At is server time sampled BEFORE observing the process. A delayed
// observation (older than two beat ticks), a future time, or a changed binding
// cannot renew a lease. An empty Owner is valid only for unknown observations.
type OwnerObservation struct {
	Owner ProcessOwner
	State string // live, dead, unknown
	At    time.Time
}

type OwnerReceipt struct {
	State      string
	Changed    bool
	LeaseUntil int64
}

// ObserveOwner records the observation and renews only a live, current owner.
// The store rechecks token, binding, membership, lease and observation age in
// the same operation as renewal. It never moves or resurrects a copy.
func ObserveOwner(ctx context.Context, c redis.Cmdable, as Consumer, id, token string, o OwnerObservation) (OwnerReceipt, error) {
	if o.State != "live" && o.State != "dead" && o.State != "unknown" {
		return OwnerReceipt{}, fmt.Errorf("owner state wants live, dead or unknown")
	}
	if o.State != "unknown" || o.Owner != (ProcessOwner{}) {
		if err := o.Owner.check(); err != nil {
			return OwnerReceipt{}, err
		}
	}
	r, err := ownerCall(ctx, c, as, id, token, "observe", o.Owner, o.State, o.At.UnixMilli())
	if err != nil {
		return OwnerReceipt{}, err
	}
	if len(r) != 4 || r[0] != "OWNER" {
		return OwnerReceipt{}, fmt.Errorf("owner: unexpected reply %v", r)
	}
	until, err := strconv.ParseInt(r[3], 10, 64)
	if err != nil {
		return OwnerReceipt{}, fmt.Errorf("owner: bad lease: %w", err)
	}
	return OwnerReceipt{State: r[1], Changed: r[2] == "1", LeaseUntil: until}, nil
}

func ownerCall(ctx context.Context, c redis.Cmdable, as Consumer, id, token, op string, p ProcessOwner, state string, at int64) ([]string, error) {
	if as.Kind != "friend" || as.Name == "" || !IsCopy(id) || token == "" {
		return nil, fmt.Errorf("owner requires friend consumer, copy id and current token")
	}
	r, err := c.FCall(ctx, FnOwner, []string{as.Key("working"), Key(id)}, op, as.String(), id, token, p.Host, p.PID, p.Start, state, at).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("owner: %w", err)
	}
	if err = refusal(r); err != nil {
		return nil, err
	}
	return r, nil
}
