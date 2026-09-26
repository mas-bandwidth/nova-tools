package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

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

// seatAddrEnvs are THE address variables, in THE order, every verb reads its
// Redis address from when it is given no --redis (redisDefaultFrom, the one
// resolver); a seat row sets all three. Before nova-tools#4352 A each verb
// named its own list (redisDefault(), redisDefault(),
// redisOr(v, "NOVA_REDIS_ADDR")), so `friend show`, `width` and `census`
// refused "redis address is required" with NOVA_SPRINT_REDIS set while
// every other verb read it (the coordinator, 2026-09-26 13:27-13:45 EDT).
var seatAddrEnvs = []string{"NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR", "NOVA_REDIS"}

// selectSeat takes --seat out of args, selects the seat in sel (the process's
// seatcred.Process() from main), and when the seat
// has a profile row points the verbs' address defaults at its Redis. A
// malformed profile is refused here, before any verb runs.
func selectSeat(sel *seatcred.Selection, args []string, getenv func(string) string, setenv func(k, v string) error) ([]string, error) {
	rest, err := sel.FromArgs(args, func(k string) string {
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
	seat := sel.Selected()
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
		sel.SelectWith(seat, "", func(s string) (seatcred.Cred, error) {
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
	sel.SelectProfile(p, func(string) (seatcred.Cred, error) { return seatcred.ResolveProfile(p, getenv) })
	return rest, nil
}

// redisDefault is every verb's --redis default (nova-tools#4330, #4352 A):
// the one resolver. It reads extra first (a verb's own variable, only card
// run's NOVA_CARD_REDIS), then seatAddrEnvs in their order, else the
// selected seat's Redis address from its seats.tsv row, else "". A verb given
// no --redis under --seat therefore dials the seat's Redis, and with no seat
// it dials the environment's, the same on every verb. The class test
// internal/ci/seatredis_class_test.go holds every --redis flag to it.
func redisDefault(extra ...string) string {
	return redisDefaultFrom(seatcred.Process(), os.Getenv, extra...)
}

func redisDefaultFrom(sel *seatcred.Selection, getenv func(string) string, extra ...string) string {
	for _, k := range append(append([]string{}, extra...), seatAddrEnvs...) {
		if v := getenv(k); v != "" {
			return v
		}
	}
	return sel.Addr()
}

// redisOr is v, else redisDefault(extra...): the same resolver for a verb
// that parses its flags by hand.
func redisOr(v string, extra ...string) string {
	if v != "" {
		return v
	}
	return redisDefault(extra...)
}

// GitHub token through the seat (nova-tools#4330). A seats.tsv row's seventh
// column names the key of the seat's file that holds its GitHub token, so the
// GitHub verbs (land, ci, read, file, pr) read it in this process and no
// session exports GH_TOKEN. A seat whose row has no seventh column (or no row)
// keeps the old behaviour -- GH_TOKEN, then GITHUB_TOKEN -- and says so once
// per process on stderr.

var (
	seatGitHubNoteOut  io.Writer = os.Stderr
	seatGitHubNoteOnce sync.Once
)

// seatGitHubToken is the selected seat's GitHub token. ok is false when no
// seat is selected or its row names no token column (then note, once, says
// the session's GH_TOKEN is read instead); err is a refusal naming the seat's
// file and the remedy, never a value.
func seatGitHubToken(sel *seatcred.Selection, note io.Writer, once *sync.Once) (tok string, ok bool, err error) {
	seat := sel.Selected()
	if seat == "" {
		return "", false, nil
	}
	if sel.GitHubEnv() == "" {
		once.Do(func() {
			fmt.Fprintf(note, "nova-sprint: seat %s: its seats.tsv row names no GitHub token env (the seventh column), so GitHub verbs read GH_TOKEN from the session as before\n", seat)
		})
		return "", false, nil
	}
	c, _, err := sel.Active()
	if err != nil {
		return "", true, err
	}
	if c.GitHubErr != nil {
		return "", true, c.GitHubErr
	}
	if err := c.GitHub.Use(func(v string) error { tok = strings.TrimSpace(v); return nil }); err != nil {
		return "", true, fmt.Errorf("seat %s: %v", seat, err)
	}
	return tok, true, nil
}

// envGitHubToken is the seat's GitHub token when its row names one, else
// GH_TOKEN, then GITHUB_TOKEN, from the environment; "" when none is set.
func envGitHubToken() (string, error) {
	tok, ok, err := seatGitHubToken(seatcred.Process(), seatGitHubNoteOut, &seatGitHubNoteOnce)
	if ok || err != nil {
		return tok, err
	}
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, nil
		}
	}
	return "", nil
}
