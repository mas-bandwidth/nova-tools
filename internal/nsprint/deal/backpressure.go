// Package deal is the reconciler's deal pass (nova-sprint #2756 section 5.3).
//
// This file is the backpressure read (issue #2725, control 11). There is one
// source of truth per sprint, the hash s:<S>:backpressure (state ON|OFF, debt,
// cap, at), written by the reconciler each pass and read here by the deal pass.
// There is no file. A missing hash, a stale one (at older than StaleAfter), or
// one without a readable state applies the sprint's declared policy
// s:<S>:policy backpressure_missing (open or closed) and the verdict prints
// which case applied, so a stopped loop can never leave the dealer failing
// closed with nothing on the table. An undeclared policy is refused, never
// guessed (preflight 7.9).
package deal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// StaleAfter is the age past which the backpressure hash is not believed
// (#2756 section 2.3: stale at 20 s, two reconciler sweeps).
const StaleAfter = 20 * time.Second

// Policy values for s:<S>:policy backpressure_missing.
const (
	// PolicyOpen: missing or stale means OFF, bulk flows (fail-open, Johnny 3).
	PolicyOpen = "open"
	// PolicyClosed: missing or stale holds bulk; the priority tier still flows.
	PolicyClosed = "closed"
)

// PolicyField is the policy hash field that declares the missing behaviour.
const PolicyField = "backpressure_missing"

var (
	// ErrPolicyUndeclared: s:<S>:policy has no backpressure_missing of open or
	// closed. The dealer must not guess; preflight 7.9 is red.
	ErrPolicyUndeclared = errors.New("backpressure: policy undeclared")
	// ErrTwoBackpressureKeys: a key other than s:<S>:backpressure carries
	// backpressure state (the two-files defect of 2026-09-22).
	ErrTwoBackpressureKeys = errors.New("backpressure: two backpressure keys")
)

// TierBulk is the tier a blocked sprint stops dealing (the width duty's
// READ-BOUND); pro and above still go.
const TierBulk = "bulk"

// BackpressureKey is the one backpressure hash of sprint S.
func BackpressureKey(sprint string) string { return "s:" + sprint + ":backpressure" }

// ProcBackpressureKey is the backpressure process beat. It is not state, so
// it is never a second source of truth; the check reports whether it exists.
const ProcBackpressureKey = "proc:backpressure"

// LegacyBackpressureKeys are the key names that carried backpressure state
// before s:<S>:backpressure: the v1 global hash `backpressure` (the two-files
// defect of 2026-09-22). Any of them present beside the sprint's own hash is
// a second source of truth. A new legacy name is one entry here.
var LegacyBackpressureKeys = []string{"backpressure"}

// KeyCheck is what CheckOneBackpressureKey read.
type KeyCheck struct {
	// Sprint is the sprint checked.
	Sprint string
	// Own is this sprint's key: empty, or exactly [s:<S>:backpressure].
	Own []string
	// Beat is true when proc:backpressure exists.
	Beat bool
	// Extra are the legacy keys present, sorted.
	Extra []string
	// RoundTrips is the number of Redis round trips the check made (1).
	RoundTrips int
}

// Line is the check's one receipt line.
func (k KeyCheck) Line() string {
	beat := 0
	if k.Beat {
		beat = 1
	}
	if len(k.Extra) > 0 {
		return fmt.Sprintf("BACKPRESSURE CHECK REFUSED sprint=%s own=%d beat=%d legacy=%s round_trips=%d",
			k.Sprint, len(k.Own), beat, strings.Join(k.Extra, ","), k.RoundTrips)
	}
	return fmt.Sprintf("BACKPRESSURE CHECK OK sprint=%s own=%d beat=%d legacy=0 round_trips=%d",
		k.Sprint, len(k.Own), beat, k.RoundTrips)
}

// CheckOneBackpressureKey refuses any second source of truth for sprint S. It
// reads the named keys it needs, s:<S>:backpressure, proc:backpressure and
// LegacyBackpressureKeys, with one EXISTS each in one pipeline: one round trip
// whatever the size of the keyspace, and no SCAN or KEYS (#3276; the SCAN walk
// it replaces took 7 round trips at 6,357 keys). Other sprints' own
// s:<T>:backpressure hashes are theirs and are not read. A legacy key present
// is ErrTwoBackpressureKeys naming the keys; the KeyCheck is returned either
// way so the receipt can print it.
func CheckOneBackpressureKey(ctx context.Context, st *store.Store, sprint string) (KeyCheck, error) {
	check := KeyCheck{Sprint: sprint}
	if st == nil || st.Client() == nil {
		return check, fmt.Errorf("backpressure: nil store")
	}
	if sprint == "" {
		return check, fmt.Errorf("backpressure: sprint is required")
	}
	mine := BackpressureKey(sprint)
	pipe := st.Client().Pipeline()
	ownCmd := pipe.Exists(ctx, mine)
	beatCmd := pipe.Exists(ctx, ProcBackpressureKey)
	legacy := make([]*redis.IntCmd, len(LegacyBackpressureKeys))
	for i, k := range LegacyBackpressureKeys {
		legacy[i] = pipe.Exists(ctx, k)
	}
	check.RoundTrips = 1
	if _, err := pipe.Exec(ctx); err != nil {
		return check, fmt.Errorf("backpressure: exists %s: %w", mine, err)
	}
	if ownCmd.Val() > 0 {
		check.Own = []string{mine}
	}
	check.Beat = beatCmd.Val() > 0
	for i, cmd := range legacy {
		if cmd.Val() > 0 {
			check.Extra = append(check.Extra, LegacyBackpressureKeys[i])
		}
	}
	if len(check.Extra) > 0 {
		sort.Strings(check.Extra)
		quoted := make([]string, len(check.Extra))
		for i, k := range check.Extra {
			quoted[i] = strconv.Quote(k)
		}
		return check, fmt.Errorf("%w: %s beside %q", ErrTwoBackpressureKeys, strings.Join(quoted, ", "), mine)
	}
	return check, nil
}
