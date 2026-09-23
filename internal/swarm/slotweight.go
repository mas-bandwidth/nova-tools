package swarm

import (
	"strings"
	"time"
)

// Slot admission weight (nova-tools#2033).
//
// Capacity is refused at take, before any child starts. A schema card spawns
// make/cargo/dotnet and is far heavier than a read-only card; charging the
// kind's weight against the store's share is the ceiling. A load reading after
// launch is a brake that arrives too late: captainamerica reached load 172 on
// 64 cores and fell off the network.

const (
	SlotWeightRead  = 1
	SlotWeightHeavy = 4
)

// heavySlotKinds are the kinds that spawn compilers, gates or test binaries.
var heavySlotKinds = map[string]bool{
	"schema":          true,
	"schema-leg":      true,
	"spec":            true,
	"fix":             true,
	"fix-red":         true,
	"replay":          true,
	"rebase":          true,
	"go":              true,
	"lisp":            true,
	"test":            true,
	"sweep":           true,
	"mutation-kill":   true,
	"transcript-test": true,
}

// SlotAdmissionWeight is how many share units a card of this kind consumes at
// take time. Unknown and empty kinds weigh 1, so an untyped launch stays one
// seat.
func SlotAdmissionWeight(kind string) int {
	if heavySlotKinds[strings.ToLower(strings.TrimSpace(kind))] {
		return SlotWeightHeavy
	}
	return SlotWeightRead
}

// CardKindFromText reads a card's kind from its typed KIND: header or a :kind
// pull field. An untyped card is "".
func CardKindFromText(text string) string {
	if k := cardFields(text)["kind"]; k != "" {
		return k
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == ":kind" {
				return strings.ToLower(fields[i+1])
			}
		}
	}
	return ""
}

// TakeSlotLeasesKind grants k leases each carrying the kind's admission weight.
func TakeSlotLeasesKind(store, owner string, k int, kind string, dur time.Duration, label string, now time.Time, pid int) (ids []string, held, share, free int, holders string, ok bool, err error) {
	return takeSlotLeases(store, owner, k, SlotAdmissionWeight(kind), kind, dur, label, now, pid)
}
