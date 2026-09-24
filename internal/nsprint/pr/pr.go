// Package pr reads the reap fields of one #3139 unit record, s:<S>:u:<unit>
// (nova-tools#3091 rev 5). The name is kept because #3156 imports pr.Inputs;
// the package reads a unit hash, never a retired PR key.
//
// Seven fields feed #3156's reap rules. land.lua writes every one of them,
// each from exactly one function:
//
//	head          ns_unit_head (#3139; this package only reads it)
//	paths         ns_unit_head, write-once, canonical JSON from CanonPaths
//	card_type     ns_unit_head, write-once, from the card hash (TYPE: line)
//	cut_at        ns_unit_head, write-once, from the card hash (Redis TIME at ns_card_push)
//	last_read_at  present-empty at create; ns_read on a counted read, monotonic
//	approve_head  present-empty at create; ns_read on a counted APPROVE
//	merged_at     present-empty at create; ns_land, once, with state=landed
//
// A present-empty last_read_at, approve_head or merged_at is a valid "not
// yet". An absent field is MISSING: the unit is never reap-eligible.
package pr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrMissing is wrapped by every MISSING error: an absent reap field, an
// unresolved PR, or a unit hash that does not exist.
var ErrMissing = errors.New("MISSING")

// ErrRefused is wrapped by every CanonPaths refusal.
var ErrRefused = errors.New("REFUSED paths")

// ReapFields are the seven unit fields #3156's rules read, in the order
// Inputs checks them.
var ReapFields = []string{"head", "paths", "card_type", "cut_at", "last_read_at", "approve_head", "merged_at"}

type missingError struct{ what string }

func (e *missingError) Error() string { return "MISSING " + e.what }
func (e *missingError) Unwrap() error { return ErrMissing }

func missing(what string) error { return &missingError{what: what} }

type refusedError struct{ reason, entry string }

func (e *refusedError) Error() string { return "REFUSED paths " + e.reason + " " + e.entry }
func (e *refusedError) Unwrap() error { return ErrRefused }

func refuse(reason, entry string) error {
	if entry == "" {
		entry = `""`
	}
	return &refusedError{reason: reason, entry: entry}
}

// CanonPaths canonicalizes a card's PATHS line. The line is a JSON array of
// strings, or whitespace- or comma-separated tokens; a path holding a space
// or a comma can only be written in the JSON form. Each entry uses /
// separators and is path.Clean'ed (the leading ./ and a trailing / go). An
// entry that is absolute, empty or has a .. component is refused with
// "REFUSED paths <absolute|empty|dotdot|json> <entry>". The result is sorted
// and deduplicated.
func CanonPaths(line string) ([]string, error) {
	line = strings.TrimSpace(line)
	var raw []string
	if strings.HasPrefix(line, "[") {
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, refuse("json", line)
		}
	} else {
		raw = strings.FieldsFunc(line, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
	}
	if len(raw) == 0 {
		return nil, refuse("empty", line)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		e := strings.ReplaceAll(strings.TrimSpace(entry), `\`, "/")
		switch {
		case e == "":
			return nil, refuse("empty", entry)
		case strings.HasPrefix(e, "/"):
			return nil, refuse("absolute", entry)
		}
		for _, part := range strings.Split(e, "/") {
			if part == ".." {
				return nil, refuse("dotdot", entry)
			}
		}
		c := strings.TrimPrefix(path.Clean(e), "./")
		if c == "." || c == "" {
			return nil, refuse("empty", entry)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out, nil
}

// EncodePaths is the stored form of a canonical path list: compact JSON,
// e.g. ["a b/c","internal/nsprint/pr"].
func EncodePaths(paths []string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(paths) // a []string always encodes
	return strings.TrimSuffix(b.String(), "\n")
}

// CanonJSON is CanonPaths followed by EncodePaths: the value ns_unit_head stores.
func CanonJSON(line string) (string, error) {
	paths, err := CanonPaths(line)
	if err != nil {
		return "", err
	}
	return EncodePaths(paths), nil
}

// Overlap reports whether two canonical path lists share a path, comparing
// whole components: a/b overlaps a/b and a/b/c, never a/bc.
func Overlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y || strings.HasPrefix(y, x+"/") || strings.HasPrefix(x, y+"/") {
				return true
			}
		}
	}
	return false
}

// Input is one unit record's reap fields, parsed by Inputs (the spec's
// "Inputs" type; Go cannot give a function and a type the same name). Read,
// Approved and Merged are false for a present-empty sentinel. An Input that
// did not come from a successful Inputs call is invalid, and every predicate
// returns the value that keeps the unit open (fail closed).
type Input struct {
	Repo        string
	Head        string
	Paths       []string
	CardType    string
	CutAt       int64
	LastReadAt  int64
	Read        bool
	ApproveHead string
	Approved    bool
	MergedAt    int64
	Merged      bool

	err error
	ok  bool
}

// Err is the error Inputs returned for this record, or MISSING inputs for a
// zero Input; nil for a valid one.
func (in Input) Err() error {
	if in.ok {
		return nil
	}
	if in.err != nil {
		return in.err
	}
	return missing("inputs")
}

// Inputs parses rec, the HGETALL of s:<S>:u:<unit>: the seven reap fields
// plus repo. An absent field, a present-empty head, paths, card_type or
// cut_at, and a non-empty last_read_at, cut_at or merged_at that is not a
// whole number are MISSING <field> (wrapping ErrMissing).
func Inputs(rec map[string]string) (Input, error) {
	fail := func(field string) (Input, error) {
		err := missing(field)
		return Input{err: err}, err
	}
	var in Input
	var ok bool
	if in.Repo, ok = rec["repo"]; !ok || in.Repo == "" {
		return fail("repo")
	}
	if in.Head, ok = rec["head"]; !ok || in.Head == "" {
		return fail("head")
	}
	rawPaths, ok := rec["paths"]
	if !ok {
		return fail("paths")
	}
	if err := json.Unmarshal([]byte(rawPaths), &in.Paths); err != nil || len(in.Paths) == 0 {
		return fail("paths")
	}
	if in.CardType, ok = rec["card_type"]; !ok || in.CardType == "" {
		return fail("card_type")
	}
	cut, ok := rec["cut_at"]
	if !ok {
		return fail("cut_at")
	}
	n, err := strconv.ParseInt(cut, 10, 64)
	if err != nil {
		return fail("cut_at")
	}
	in.CutAt = n
	if in.LastReadAt, in.Read, ok = sentinelInt(rec, "last_read_at"); !ok {
		return fail("last_read_at")
	}
	if in.ApproveHead, ok = rec["approve_head"]; !ok {
		return fail("approve_head")
	}
	in.Approved = in.ApproveHead != ""
	if in.MergedAt, in.Merged, ok = sentinelInt(rec, "merged_at"); !ok {
		return fail("merged_at")
	}
	in.ok = true
	return in, nil
}

// sentinelInt reads a field whose "not yet" is present-empty. ok is false for
// an absent field or a non-empty value that is not a whole number.
func sentinelInt(rec map[string]string, field string) (v int64, set, ok bool) {
	raw, present := rec[field]
	if !present {
		return 0, false, false
	}
	if raw == "" {
		return 0, false, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, false
	}
	return n, true, true
}

// ApprovedAtHead is X1: an APPROVE is stored at the unit's current head. An
// invalid Input answers true, which keeps the unit open.
func ApprovedAtHead(in Input) bool {
	if !in.ok {
		return true
	}
	return in.Approved && in.ApproveHead == in.Head
}

// StaleFor is E1's age: now - last_read_at, or now - cut_at when no counted
// read has been stored. Never negative. An invalid Input answers 0.
func StaleFor(in Input, now time.Time) time.Duration {
	if !in.ok {
		return 0
	}
	ref := in.CutAt
	if in.Read {
		ref = in.LastReadAt
	}
	d := now.Unix() - ref
	if d < 0 {
		return 0
	}
	return time.Duration(d) * time.Second
}

// Superseder is E2's witness: the index of the first landed record in the
// same repo whose paths overlap in's and whose merged_at is after in's
// cut_at, or -1. Invalid records, unmerged ones and an in that is itself
// merged never supersede.
func Superseder(in Input, landed []Input) int {
	if !in.ok || in.Merged {
		return -1
	}
	for i, l := range landed {
		if l.ok && l.Merged && l.Repo == in.Repo && l.MergedAt > in.CutAt && Overlap(l.Paths, in.Paths) {
			return i
		}
	}
	return -1
}

// SupersededBy is E2: some landed record supersedes in (see Superseder).
func SupersededBy(in Input, landed []Input) bool { return Superseder(in, landed) >= 0 }

// Behind is E3's count: landed records in the same repo merged after in's
// cut_at. #3156 compares it with BehindN (behind when Behind > BehindN). An
// invalid Input answers 0.
func Behind(in Input, landed []Input) int {
	if !in.ok || in.Merged {
		return 0
	}
	n := 0
	for _, l := range landed {
		if l.ok && l.Merged && l.Repo == in.Repo && l.MergedAt > in.CutAt {
			n++
		}
	}
	return n
}

// Gated is E3's type test: in's card_type is one of types. An invalid Input
// answers false.
func Gated(in Input, types []string) bool {
	if !in.ok {
		return false
	}
	for _, t := range types {
		if t == in.CardType {
			return true
		}
	}
	return false
}

func unitKey(sprint, unit string) string { return "s:" + sprint + ":u:" + unit }

// PRUnitKey is s:<S>:prunit:<repo>:<n>, which ns_unit_head writes when a
// unit names its PR.
func PRUnitKey(sprint, repo string, n int) string {
	return fmt.Sprintf("s:%s:prunit:%s:%d", sprint, repo, n)
}

// Resolve is the production PR-to-unit resolver: GET s:<S>:prunit:<repo>:<n>,
// then HGETALL s:<S>:u:<unit>. Two round trips, no SCAN. No unit names the
// PR: MISSING s:<S>:prunit:<repo>:<n>; the unit hash is gone: MISSING
// s:<S>:u:<unit>. Both wrap ErrMissing.
func Resolve(ctx context.Context, c redis.Cmdable, sprint, repo string, n int) (string, map[string]string, error) {
	key := PRUnitKey(sprint, repo, n)
	unit, err := c.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) || (err == nil && unit == "") {
		return "", nil, missing(key)
	}
	if err != nil {
		return "", nil, err
	}
	rec, err := c.HGetAll(ctx, unitKey(sprint, unit)).Result()
	if err != nil {
		return "", nil, err
	}
	if len(rec) == 0 {
		return unit, nil, missing(unitKey(sprint, unit))
	}
	return unit, rec, nil
}

// LoadUnits is #3156's input set: SMEMBERS s:<S>:units, then one pipeline of
// HGETALL s:<S>:u:<unit>. A member whose hash is gone comes back as an empty
// record, which Inputs reports as MISSING.
func LoadUnits(ctx context.Context, c redis.Cmdable, sprint string) (map[string]map[string]string, error) {
	units, err := c.SMembers(ctx, "s:"+sprint+":units").Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(units))
	if len(units) == 0 {
		return out, nil
	}
	cmds := make([]*redis.MapStringStringCmd, len(units))
	pipe := c.Pipeline()
	for i, u := range units {
		cmds[i] = pipe.HGetAll(ctx, unitKey(sprint, u))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for i, u := range units {
		out[u] = cmds[i].Val()
	}
	return out, nil
}
