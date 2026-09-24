// Package backpressure is the land-rate gate (nova-sprint #3095, #2756 v6
// spec 4.10, control 48).
//
// land_debt is how many of the sprint's units are waiting to be read or
// landed, counted from the unit records of the #3139 contract (2.2, 2.4):
// the index s:<S>:units and each s:<S>:u:<unit> state (nova-tools#3491). A
// unit in opened, reading, landable, batched, gating, green, or landing is
// debt; landed is terminal and dropped and settled are exits. The retired PR
// keys s:<S>:idx:pr:reading and the global s:<S>:landable are not read: after
// #3139 B1 nothing writes them. This package reads the unit records and does
// not write them.
// land_cap is land_rate_per_h times land_horizon_h on s:<S>:policy. With the
// lines absent the interim is 5/h times 4h, which is 20. While land_debt is
// above that cap, only a ci, fix, rebase, or front card flows. A bulk card,
// including a bulk nx card, waits. Debt equal to the cap is not above it.
//
// The v5 hash s:<S>:backpressure is a different gate (missing policy, priority
// tier). This package does not write it: an ON there would hold fix and
// rebase, and those cards still deal while land debt is above the cap.
package backpressure

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

const (
	// InterimLandRatePerHour is the land rate until a measurement replaces it.
	InterimLandRatePerHour = 5
	// InterimLandHorizonHours is the horizon the cap multiplies the rate by.
	InterimLandHorizonHours = 4
	// InterimLandCap is 5/h × 4h. While land_debt is above this, bulk waits.
	InterimLandCap = InterimLandRatePerHour * InterimLandHorizonHours
)

// Policy fields on s:<S>:policy. Absent lines take the interim values; a
// present line that does not parse is refused, never guessed.
const (
	PolicyLandRate    = "land_rate_per_h"
	PolicyLandHorizon = "land_horizon_h"
)

// Work types the land-rate gate names. nx is the bulk PR-producing type in
// the router's table; it is not exempt.
const (
	TypeCI     = "ci"
	TypeFix    = "fix"
	TypeRebase = "rebase"
	TypeNX     = "nx"
)

var sprintNameRx = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// tokenRx is a sprint-key token: a work type or a repo name. It cannot carry
// a colon, so it cannot extend a key.
var tokenRx = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// PolicyKey is s:<S>:policy.
func PolicyKey(sprint string) string { return "s:" + sprint + ":policy" }

// PRID is the member id of one sprint PR in the type gate sets (cut.go).
func PRID(repo string, n int) (string, error) {
	if !tokenRx.MatchString(repo) || n < 1 {
		return "", fmt.Errorf("backpressure: pr id repo %q number %d", repo, n)
	}
	return repo + "#" + strconv.Itoa(n), nil
}

// Card is a queued card as the land-rate gate sees it. Front is the card's
// --front flag, not its place in the queue: the first card is not thereby front.
type Card struct {
	Label string
	Type  string
	Front bool
}

// Pressure is one read of land_debt and land_cap.
type Pressure struct {
	Debt         int
	Cap          int
	RatePerHour  int
	HorizonHours int
}

// Above reports land_debt > land_cap. Equal is not above.
func (p Pressure) Above() bool { return p.Debt > p.Cap }

// Flows reports whether the card may be dealt at this debt. At or under the
// cap every card flows. Above the cap, only ci, fix, rebase, and front cards
// flow. A label ci-<pr>-<sha8> is a ci card (#2756 10.2) even when Type was
// not copied onto the queue entry.
func Flows(debt, cap int, c Card) bool {
	if debt <= cap {
		return true
	}
	if c.Front {
		return true
	}
	switch c.Type {
	case TypeCI, TypeFix, TypeRebase:
		return true
	}
	return strings.HasPrefix(c.Label, "ci-")
}

// Unit states (#3139 2.4) and whether a unit in that state is land debt.
// A state outside this table is refused, never guessed.
var unitStateDebt = map[string]bool{
	"opened": true, "reading": true, "landable": true, "batched": true,
	"gating": true, "green": true, "landing": true,
	"landed": false, "dropped": false, "settled": false,
}

// IsDebtState reports whether a unit in state counts toward land_debt, and
// whether the state is one the unit contract names at all.
func IsDebtState(state string) (debt, known bool) {
	debt, known = unitStateDebt[state]
	return debt, known
}

// Measure reads land_debt and land_cap. Clients do not supply the counts.
// Two pipelined round trips: the unit index with the policy, then every
// indexed unit's state. An indexed unit with no state, or a state outside the
// contract, is an error naming the unit: a gap is not zero debt.
func Measure(ctx context.Context, st *store.Store, sprint string) (Pressure, error) {
	client, err := redisClient(st)
	if err != nil {
		return Pressure{}, err
	}
	if err := checkSprint(sprint); err != nil {
		return Pressure{}, err
	}
	pipe := client.Pipeline()
	members := pipe.SMembers(ctx, land.UnitsSetKey(sprint))
	policy := pipe.HGetAll(ctx, PolicyKey(sprint))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return Pressure{}, fmt.Errorf("backpressure: measure %s: %w", sprint, err)
	}
	units, err := members.Result()
	if err != nil && err != redis.Nil {
		return Pressure{}, fmt.Errorf("backpressure: units %s: %w", sprint, err)
	}
	fields, err := policy.Result()
	if err != nil && err != redis.Nil {
		return Pressure{}, fmt.Errorf("backpressure: policy %s: %w", sprint, err)
	}
	rate, horizon, err := rateHorizon(fields)
	if err != nil {
		return Pressure{}, err
	}
	cap, err := landCap(rate, horizon)
	if err != nil {
		return Pressure{}, err
	}
	debt, err := unitDebt(ctx, client, sprint, units)
	if err != nil {
		return Pressure{}, err
	}
	return Pressure{Debt: debt, Cap: cap, RatePerHour: rate, HorizonHours: horizon}, nil
}

// unitDebt reads every unit's state in one pipeline and counts the debt.
func unitDebt(ctx context.Context, client *redis.Client, sprint string, units []string) (int, error) {
	if len(units) == 0 {
		return 0, nil
	}
	sort.Strings(units)
	pipe := client.Pipeline()
	states := make([]*redis.StringCmd, len(units))
	for i, u := range units {
		states[i] = pipe.HGet(ctx, land.UnitKey(sprint, u), "state")
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, fmt.Errorf("backpressure: unit states %s: %w", sprint, err)
	}
	debt := 0
	for i, u := range units {
		state, err := states[i].Result()
		if err == redis.Nil || (err == nil && state == "") {
			return 0, fmt.Errorf("backpressure: land_debt %s: unit %s is indexed in %s with no state in %s",
				sprint, u, land.UnitsSetKey(sprint), land.UnitKey(sprint, u))
		}
		if err != nil {
			return 0, fmt.Errorf("backpressure: unit %s state: %w", u, err)
		}
		isDebt, known := IsDebtState(state)
		if !known {
			return 0, fmt.Errorf("backpressure: land_debt %s: unit %s state %q is not a unit state (#3139 2.4)", sprint, u, state)
		}
		if isDebt {
			debt++
		}
	}
	return debt, nil
}

func rateHorizon(fields map[string]string) (int, int, error) {
	rate, horizon := InterimLandRatePerHour, InterimLandHorizonHours
	if v, ok := fields[PolicyLandRate]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			return 0, 0, fmt.Errorf("backpressure: policy %s=%q: want a non-negative integer", PolicyLandRate, v)
		}
		rate = n
	}
	if v, ok := fields[PolicyLandHorizon]; ok {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return 0, 0, fmt.Errorf("backpressure: policy %s=%q: want a positive number of hours", PolicyLandHorizon, v)
		}
		horizon = n
	}
	return rate, horizon, nil
}

func landCap(rate, horizon int) (int, error) {
	if rate < 0 || horizon < 1 {
		return 0, fmt.Errorf("backpressure: land cap rate %d horizon %d", rate, horizon)
	}
	if rate > 0 && horizon > int(^uint(0)>>1)/rate {
		return 0, fmt.Errorf("backpressure: land cap %d/h × %dh overflows", rate, horizon)
	}
	return rate * horizon, nil
}

func redisClient(st *store.Store) (*redis.Client, error) {
	if st == nil || st.Client() == nil {
		return nil, fmt.Errorf("backpressure: nil store")
	}
	return st.Client(), nil
}

func checkSprint(sprint string) error {
	if !sprintNameRx.MatchString(sprint) {
		return fmt.Errorf("backpressure: sprint %q must match [a-z0-9-]{1,40}", sprint)
	}
	return nil
}

func checkType(workType string) error {
	if !tokenRx.MatchString(workType) {
		return fmt.Errorf("backpressure: type %q must match [a-z0-9][a-z0-9-]{0,39}", workType)
	}
	return nil
}
