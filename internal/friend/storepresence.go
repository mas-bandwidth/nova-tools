package friend

import (
	"context"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// Presence on the bus store (docs/SPEC-FRIEND.md, Presence). One hash per
// friend, written by the daemon every step; the state is PresenceState.

const (
	PresenceTTL     = 60 * time.Second
	PresenceVersion = "1"

	// BusUp, BusAsleep and BusDown are PresenceState's answers.
	BusUp     = "up"
	BusAsleep = "asleep"
	BusDown   = "down"
)

// PresenceKey is the hash one friend occupies on the bus store.
func PresenceKey(name string) string { return "bus2:presence:" + name }

// presenceSnap is what the daemon knows at one step, apart from the record
// the coordinator writes.
type presenceSnap struct {
	At       time.Time
	Route    string
	Sleeping bool
	Queue    int
	Working  int
	Width    int
	BrokenAt time.Time
	Reason   string
}

// PresenceState is the record's state at now (docs/SPEC-FRIEND.md, Presence):
// up when seen is within DownAfter and the sleep flag is clear; the sleep
// state when it is within DownAfter and the flag is set; down when seen is
// missing, zero, or at least DownAfter old, when the record is gone, or when
// the session is marked broken. At exactly DownAfter the state is down.
func PresenceState(fields map[string]string, now time.Time) string {
	if len(fields) == 0 || fields["seen"] == "" || fields["broken"] != "" {
		return BusDown
	}
	seen, err := time.Parse(time.RFC3339Nano, fields["seen"])
	if err != nil || seen.IsZero() {
		return BusDown
	}
	if now.Sub(seen) >= DownAfter {
		return BusDown
	}
	if fields["asleep"] == "1" {
		return BusAsleep
	}
	return BusUp
}

// writePresence merges the daemon's own fields into its hash and refreshes
// the minute expiry. It does not write proved, so a coordinator's field stays.
func (d *Daemon) writePresence(ctx context.Context, s presenceSnap) {
	if d == nil || d.Store == nil || d.Friend == "" {
		return
	}
	if d.Instance == "" {
		d.Instance = s.At.UTC().Format(time.RFC3339Nano)
	}
	flag := "0"
	if s.Sleeping {
		flag = "1"
	}
	broken, reason := "", ""
	if !s.BrokenAt.IsZero() {
		broken = s.BrokenAt.UTC().Format(time.RFC3339)
		reason = s.Reason
	}
	fields := map[string]string{
		"name":     d.Friend,
		"seen":     s.At.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		"instance": d.Instance,
		"harness":  d.Harness,
		"route":    s.Route,
		"asleep":   flag,
		"queue":    strconv.Itoa(s.Queue),
		"working":  strconv.Itoa(s.Working),
		"width":    strconv.Itoa(s.Width),
		"version":  PresenceVersion,
		"broken":   broken,
		"reason":   reason,
	}
	if err := d.Store.PutHash(ctx, PresenceKey(d.Friend), fields, PresenceTTL); err != nil {
		d.status.StoreError = "presence: " + err.Error()
	}
}

// Authority is who writes proved: the coordinator's name, and an optional
// generation a caller may pass. The generation is not stored.
type Authority struct {
	Name       string
	Generation string
}

// HealthBatch writes proved on each friend that has a counted pong and a
// live record. It does not create a missing record, does not refresh the
// expiry, and writes no other field. Generation is ignored.
func (k *Keepalive) HealthBatch(ctx context.Context, st bus.Store, _ Authority) error {
	if k == nil || st == nil {
		return nil
	}
	var first error
	note := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, n := range k.Names() {
		p := k.peers[n]
		if p == nil || p.last.IsZero() {
			continue
		}
		key := PresenceKey(n)
		got, err := st.Marks(ctx, key)
		if err != nil {
			note(err)
			continue
		}
		if len(got) == 0 || len(got[0]) == 0 {
			continue
		}
		note(st.PutHash(ctx, key, map[string]string{"proved": p.last.UTC().Format(time.RFC3339)}, 0))
	}
	return first
}
