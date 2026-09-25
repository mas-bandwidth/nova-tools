package fn

import "path"

// TTLAllow names the keys that may carry a TTL (#2947 rev 3, preflight 7.26):
// the leases, whose expiry is how a dead holder lets go, and the blocked-queue
// lock. Every other key the store holds lives until a verb removes it (keys do
// not expire; an old at is what a reader judges stale). Entries are path.Match
// patterns.
var TTLAllow = []string{"lease:reconciler", "lease:harvest:*", "lease:table:*", "q:blocked:lock"}

// TTLAllowed reports whether key may carry a TTL.
func TTLAllowed(key string) bool {
	for _, p := range TTLAllow {
		if ok, _ := path.Match(p, key); ok {
			return true
		}
	}
	return false
}

// IndexStates lists, per kind, the states of the index sets
// s:<S>:idx:<kind>:<state> the library writes (task_*.lua, 02_card_move.lua
// and the files that move tasks). A reader enumerates the index keys from
// this table and never SCANs for them (preflight 7.10).
var IndexStates = map[string][]string{
	"task": {"open", "claimed", "working", "waiting", "waiting-ci", "parked", "merging",
		"closed", "cancelled", "reconcile-required"},
	"card": {"queued", "dealt", "launched", "running", "reconcile-required", "orphan-effect",
		"ended", "harvested", "refused", "review-ready", "land-ready", "landed", "superseded"},
}

// IndexKinds is IndexStates' kinds in the order a reader walks them.
var IndexKinds = []string{"task", "card"}
