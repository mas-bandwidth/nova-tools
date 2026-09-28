// Package benchrole is the bench registry's role column (nova-tools #3634;
// Glenn 2026-09-24 6:15 PM: "Studio is for friends. Fleet is for CI and
// swarms."). A registered bench (the `benches` set) carries `role` on its
// registry record bench:<b>:desired: `fleet` runs swarm cards and CI, and
// `friends` hosts friends and the coordinator only, so no swarm card is dealt
// to it and it never claims a CI request. `capacity bench --role` is the one
// writer (ns_capacity_desired); an absent role is a bench registered before
// the column existed and reads as fleet.
//
// Every gate reads the one field in the same Redis Function call that acts on
// it: ns_card_deal answers NONE role=friends, ns_card_push refuses a BENCH:
// pin to a friends bench, ns_ci_claim refuses REFUSED role=friends, and
// ns_capacity_desired refuses a CI legs declaration on a friends bench. The
// Go side prints each refusal as the one REFUSED line of Error.
package benchrole

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

const (
	// Fleet is a bench that runs swarm cards and CI.
	Fleet = "fleet"
	// Friends is a bench that hosts friends only: no swarm card, no CI.
	Friends = "friends"
	// Field is the role column on the registry record.
	Field = "role"
	// Registry is the set of registered benches.
	Registry = "benches"
)

// Key is a bench's registry record, the hash the role column lives on.
func Key(bench string) string { return "bench:" + bench + ":desired" }

// Parse reads one role value. An empty value is a bench registered before the
// column existed and reads as fleet; anything but friends or fleet is an
// error naming the two.
func Parse(raw string) (string, error) {
	switch strings.TrimSpace(raw) {
	case "", Fleet:
		return Fleet, nil
	case Friends:
		return Friends, nil
	default:
		return "", fmt.Errorf("role %q: want friends or fleet", raw)
	}
}

// ParseFlag reads a --role value: empty keeps the stored role (""), else it
// must be friends or fleet.
func ParseFlag(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw != Fleet && raw != Friends {
		return "", fmt.Errorf("--role %q: want friends or fleet", raw)
	}
	return raw, nil
}

// Error is the refusal every gate prints: one line naming the bench, its role
// and what was refused, exit 1.
type Error struct {
	Bench string
	Role  string
	What  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("REFUSED bench=%s role=%s: %s; a friends bench hosts friends only (nova-tools#3634), set capacity bench --role fleet to change it",
		e.Bench, e.Role, e.What)
}

// ExitCode is the refusal's exit status.
func (e *Error) ExitCode() int { return 1 }

// Refused is the Error for a bench whose role gate said no.
func Refused(bench, role, what string) *Error { return &Error{Bench: bench, Role: role, What: what} }

// Row is one registered bench as `bench ls` prints it.
type Row struct {
	Bench   string
	Role    string // parsed; the raw value when it does not parse
	Valid   bool   // false when the stored role is neither friends nor fleet
	Machine string
	Slots   string
	Legs    string
	Paused  string
}

// Line is the row's one line.
func (r Row) Line() string {
	return fmt.Sprintf("BENCH %s role=%s machine=%s slots=%s legs=%s paused=%s",
		r.Bench, r.Role, dash(r.Machine), dash(r.Slots), dash(r.Legs), dash(r.Paused))
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// List reads every registered bench's registry record: one SMEMBERS, then one
// pipeline of HMGETs, sorted by name. No SCAN.
func List(ctx context.Context, c *redis.Client) ([]Row, error) {
	if c == nil {
		return nil, errors.New("bench ls: nil redis client")
	}
	names, err := c.SMembers(ctx, Registry).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("bench ls: %w", err)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	cmds := make([]*redis.SliceCmd, len(names))
	for i, b := range names {
		cmds[i] = pipe.HMGet(ctx, Key(b), Field, "machine", "slots", "legs", "paused")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("bench ls: %w", err)
	}
	rows := make([]Row, len(names))
	for i, b := range names {
		v := cmds[i].Val()
		raw := field(v, 0)
		role, perr := Parse(raw)
		row := Row{Bench: b, Role: role, Valid: perr == nil,
			Machine: field(v, 1), Slots: field(v, 2), Legs: field(v, 3), Paused: field(v, 4)}
		if perr != nil {
			row.Role = raw
		}
		rows[i] = row
	}
	return rows, nil
}

func field(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}
