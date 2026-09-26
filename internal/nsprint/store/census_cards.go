package store

import (
	"errors"
	"fmt"
	"regexp"
)

// Card census, `nova-sprint census --sprint <S> [--keys <states>]` (nova-tools
// #3037), read the sprint's card index sets s:<S>:idx:card:<state> and
// printed a TSV row per card and a CENSUS cards=<n> line. That family (the
// records s:<S>:card:<label>, the indexes, the roster sprint:<S>:cards) is
// the card store the one task store (task:<id> in the ws index, nova-tools
// #3778) replaced for sprint tasks: on 2026-09-26 13:32 EDT `census --sprint
// quack-0926` printed cards=0 while the ws index held 126 cards of the
// sprint, and after #4411 refused only an empty family, one SADD
// s:<S>:idx:card:landed (which card push and 02_card_move.lua still write)
// made it print cards=1 while `sprint status` said 1/8.
//
// The family is not a count of the sprint's work, so census --sprint is
// refused whatever it holds (#4411): one line, exit 1, no Redis read, and
// the remedy is the verb that counts the sprint, `nova-sprint ws counts`
// (the one count, ws.Counts). A malformed request is still refused first,
// as usage. The library's ns_census_read (census.lua) is no longer called.

// ErrRetiredFamily is the refusal of every census --sprint: the sprint's
// cards are task:<id> records in the ws index, and the s:<S>:card family is
// not their count.
var ErrRetiredFamily = errors.New(`census reads a retired key family; remedy="nova-sprint ws counts"`)

var censusTokenRE = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// CardCensusRequest names one card census.
type CardCensusRequest struct {
	Sprint string
	// States are the index states named with --keys.
	States []string
}

// Check refuses a malformed request (usage) before the census refusal.
func (req CardCensusRequest) Check() error {
	if !censusTokenRE.MatchString(req.Sprint) {
		return fmt.Errorf("--sprint %q: want a sprint name matching [a-z0-9-]{1,40}", req.Sprint)
	}
	seen := map[string]bool{}
	for _, s := range req.States {
		if !censusTokenRE.MatchString(s) {
			return fmt.Errorf("--keys %q: want card index states, for example queued,dealt,launched", s)
		}
		if seen[s] {
			return fmt.Errorf("--keys names %s twice", s)
		}
		seen[s] = true
	}
	return nil
}

// RefuseCardCensus is the whole card census: the request's usage refusal,
// else ErrRetiredFamily. It reads nothing.
func RefuseCardCensus(req CardCensusRequest) error {
	if err := req.Check(); err != nil {
		return err
	}
	return ErrRetiredFamily
}
