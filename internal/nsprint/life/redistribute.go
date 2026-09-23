package life

// Redistribution of an out-of-credits or down friend's work (nova-tools
// #3047, mechanism 1 of #3033). Glenn's ruling of 2026-09-23 replaces the
// #2756 5.2 sentence that work assigned to a down friend stays with it until
// `assign` moves it: out-of-credits is a friend state, and the sprint tick
// moves that friend's open tasks by kind and closes its leases in the same
// tick; a friend whose presence expired with work gets the same one tick
// later. The whole tick is one Redis Function call
// (internal/nsprint/fn/lua/redistribute.lua): no model and no coordinator is
// called, and every move writes its receipt in the same call.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Function names registered by internal/nsprint/fn/lua/redistribute.lua.
const (
	FunctionFriendState  = "ns_friend_state"
	FunctionRedistribute = "ns_friend_redistribute"
)

// Friend states kept in friend:<f>:state and printed on the friend row.
const (
	// StateOutOfCredits is written by the friend's keeper when the harness
	// reports a credit or usage limit; until is the reported reset.
	StateOutOfCredits = "out-of-credits"
	// StateDown is written by the tick when friend:<f>:beat has expired while
	// work is still assigned to or leased by the friend.
	StateDown = "down"
)

// Roster names who may receive moved work. It is config (the reviewers file's
// may-hold column, the builders, the coordinator), never state. Only UP,
// unpaused friends with no state of their own and free width receive work.
type Roster struct {
	// MayHold are the readers who may receive a read, a re-read or a release
	// task; the PR's author and the friend being emptied are never chosen.
	MayHold []string
	// Builders receive build (work), fix and harvest tasks.
	Builders []string
	// Coordinator receives a build or fix only when no builder has free width.
	Coordinator string
}

// SetState records a friend state from the friend's keeper. state is
// StateOutOfCredits (until may be zero when the harness does not say when the
// window resets) or "clear" to remove any state.
func SetState(ctx context.Context, st *store.Store, friend, state string, until time.Time, reason, actor, idem string) error {
	if st == nil || friend == "" {
		return fmt.Errorf("friend state: store and friend are required")
	}
	if state != StateOutOfCredits && state != "clear" {
		return fmt.Errorf("friend state %s: state must be %s or clear, got %q", friend, StateOutOfCredits, state)
	}
	untilMS := ""
	if !until.IsZero() {
		untilMS = strconv.FormatInt(until.UnixMilli(), 10)
	}
	reply, err := st.Client().FCall(ctx, FunctionFriendState, nil,
		friend, state, untilMS, reason, actor, idem).Result()
	if err != nil {
		return fmt.Errorf("friend state %s: %w", friend, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return fmt.Errorf("friend state %s: unexpected reply %T", friend, reply)
	}
	if status := fmt.Sprint(values[0]); status != "OK" {
		return fmt.Errorf("friend state %s: %s", friend, status)
	}
	return nil
}

// Move is what one tick did for one friend in a state.
type Move struct {
	Friend string
	State  string
	// Moved counts tasks placed on another friend's queue, leases included.
	Moved int
	// Leases counts leases closed with evidence.
	Leases int
	// Released counts reads that only this friend could answer (a carried
	// HOLD), each turned into the PR's one release task.
	Released int
	// Unrouted counts tasks placed on `ready` because no eligible friend had
	// free width; the router deals them on its next pass.
	Unrouted int
	// Pending is the first tick after a presence expiry: state=down is
	// written and nothing moves until the next tick.
	Pending bool
}

// Line is the tick's one line per friend.
func (m Move) Line() string {
	return fmt.Sprintf("REDISTRIBUTE friend=%s state=%s moved=%d leases=%d released=%d unrouted=%d pending=%t",
		m.Friend, m.State, m.Moved, m.Leases, m.Released, m.Unrouted, m.Pending)
}

// Redistribute runs one tick over every registered friend. It is idempotent:
// a friend with nothing left to move reports zeros.
func Redistribute(ctx context.Context, st *store.Store, roster Roster, actor, idem string) ([]Move, error) {
	if st == nil {
		return nil, fmt.Errorf("redistribute: nil store")
	}
	for _, list := range [][]string{roster.MayHold, roster.Builders, {roster.Coordinator}} {
		for _, name := range list {
			if strings.Contains(name, ",") {
				return nil, fmt.Errorf("redistribute: friend name %q contains a comma", name)
			}
		}
	}
	reply, err := st.Client().FCall(ctx, FunctionRedistribute, nil,
		strings.Join(roster.MayHold, ","), strings.Join(roster.Builders, ","),
		roster.Coordinator, actor, idem).Result()
	if err != nil {
		return nil, fmt.Errorf("redistribute: %w", err)
	}
	values, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("redistribute: unexpected reply %T", reply)
	}
	const fields = 8
	if len(values)%fields != 0 {
		return nil, fmt.Errorf("redistribute: reply of %d values is not whole records", len(values))
	}
	moves := make([]Move, 0, len(values)/fields)
	for i := 0; i < len(values); i += fields {
		rec := make([]string, fields)
		for k := range rec {
			rec[k] = fmt.Sprint(values[i+k])
		}
		if rec[0] != "friend" {
			return nil, fmt.Errorf("redistribute: unknown record %q", rec[0])
		}
		m := Move{Friend: rec[1], State: rec[2], Pending: rec[7] == "1"}
		for k, dst := range []*int{&m.Moved, &m.Leases, &m.Released, &m.Unrouted} {
			n, err := strconv.Atoi(rec[3+k])
			if err != nil {
				return nil, fmt.Errorf("redistribute: %s field %d: %w", m.Friend, 3+k, err)
			}
			*dst = n
		}
		moves = append(moves, m)
	}
	return moves, nil
}
