package main

import (
	"errors"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// Seat resolution (nova-tools#4330). `nova-sprint --seat <name> <verb>` (or
// NOVA_SPRINT_SEAT, then NOVA_SEAT) names the seat; the row for it in
// $XDG_CONFIG_HOME/nova-sprint/seats.tsv, written by the fleet play, names
// the seat's Redis address, ACL user, the key of the seat's file holding that
// user's password, and the store and age key that open the file. The secret is
// read through nova-secrets' library in this process (internal/seatcred) the
// first time a verb dials; the address becomes the default every verb's
// --redis falls back to (NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, NOVA_REDIS, the
// variables ns.sh set) and the address a store dial uses when its verb names
// none (seatcred.Addr), so no wrapper script stands between a session and its
// verbs.
//
// A seat with no row is the #4052 seat: the nova-secrets seat of that name in
// the default layout. If that does not open either, the refusal names the
// profile file and the row it wants.

// SprintSeatEnv names nova-sprint's seat when no --seat flag does; it wins
// over seatcred.SeatEnv (NOVA_SEAT), which every seat-aware tool reads.
const SprintSeatEnv = "NOVA_SPRINT_SEAT"

// seatAddrEnvs are the address variables verbs read their --redis default
// from; a seat row sets all three.
var seatAddrEnvs = []string{"NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR", "NOVA_REDIS"}

// selectSeat takes --seat out of args, selects the seat, and when the seat
// has a profile row points the verbs' address defaults at its Redis. A
// malformed profile is refused here, before any verb runs.
func selectSeat(args []string, getenv func(string) string, setenv func(k, v string) error) ([]string, error) {
	rest, err := seatcred.FromArgs(args, func(k string) string {
		if k == seatcred.SeatEnv {
			if v := getenv(SprintSeatEnv); v != "" {
				return v
			}
		}
		return getenv(k)
	})
	if err != nil {
		return nil, err
	}
	seat := seatcred.Selected()
	if seat == "" {
		return rest, nil
	}
	var p seatcred.Profile
	path, err := seatcred.ProfilePath("nova-sprint", getenv)
	if err != nil {
		err = fmt.Errorf("seat %s: %w: %v", seat, seatcred.ErrNoProfileRow, err)
	} else {
		p, err = seatcred.LoadProfile(path, seat, getenv("HOME"))
	}
	if errors.Is(err, seatcred.ErrNoProfileRow) {
		// The #4052 seat, named by the refusal when it does not open either.
		rowErr := err
		seatcred.SelectWith(seat, "", func(s string) (seatcred.Cred, error) {
			c, err := seatcred.Resolve(s, getenv)
			if err != nil {
				return c, fmt.Errorf("%v; and as a nova-secrets seat: %w", rowErr, err)
			}
			return c, nil
		})
		return rest, nil
	}
	if err != nil {
		return nil, err
	}
	for _, k := range seatAddrEnvs {
		if err := setenv(k, p.Addr); err != nil {
			return nil, fmt.Errorf("seat %s: cannot set %s: %v", seat, k, err)
		}
	}
	seatcred.SelectWith(seat, p.Addr, func(string) (seatcred.Cred, error) { return seatcred.ResolveProfile(p, getenv) })
	return rest, nil
}
