package friend

import (
	"time"
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
