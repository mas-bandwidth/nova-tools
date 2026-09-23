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

// Card tiers the deal pass asks about. Backpressure never holds the priority
// tier (ci cards and front cards are priority).
const (
	TierPriority = "priority"
	TierBulk     = "bulk"
)

// Where a verdict came from.
const (
	SourceHash    = "hash"    // a fresh hash with state ON or OFF
	SourceMissing = "missing" // no hash: the declared policy applied
	SourceStale   = "stale"   // at older than StaleAfter: the declared policy applied
	SourceInvalid = "invalid" // state or at unreadable: the declared policy applied
)

var (
	// ErrPolicyUndeclared: s:<S>:policy has no backpressure_missing of open or
	// closed. The dealer must not guess; preflight 7.9 is red.
	ErrPolicyUndeclared = errors.New("backpressure: policy undeclared")
	// ErrTwoBackpressureKeys: a key other than s:<S>:backpressure carries
	// backpressure state (the two-files defect of 2026-09-22).
	ErrTwoBackpressureKeys = errors.New("backpressure: two backpressure keys")
)

// BackpressureKey is the one backpressure hash of sprint S.
func BackpressureKey(sprint string) string { return "s:" + sprint + ":backpressure" }

// PolicyKey is the policy hash of sprint S (config loaded by sprint open).
func PolicyKey(sprint string) string { return "s:" + sprint + ":policy" }

// Verdict is what the deal pass acts on and what the table prints.
type Verdict struct {
	// Source is SourceHash, SourceMissing, SourceStale or SourceInvalid.
	Source string
	// Policy is the declared backpressure_missing policy.
	Policy string
	// State is ON or OFF from a fresh hash; empty when the policy applied.
	State string
	// Debt and Cap are from the hash when present (-1 when unreadable).
	Debt, Cap int
	// Age is the hash age; zero when missing.
	Age time.Duration
	// Blocked is true when bulk cards are held this pass.
	Blocked bool
}

// Allows reports whether a card of the given tier may be dealt this pass.
func (v Verdict) Allows(tier string) bool {
	if tier == TierPriority {
		return true
	}
	return !v.Blocked
}

// Line is the table and status line for this sprint's backpressure.
func (v Verdict) Line() string {
	switch v.Source {
	case SourceHash:
		return fmt.Sprintf("backpressure: %s debt=%d cap=%d age=%ds", v.State, v.Debt, v.Cap, int(v.Age/time.Second))
	case SourceStale:
		return fmt.Sprintf("backpressure: stale %ds, policy %s", int(v.Age/time.Second), v.Policy)
	case SourceInvalid:
		return "backpressure: invalid hash, policy " + v.Policy
	default:
		return "backpressure: missing, policy " + v.Policy
	}
}

// DealerLine is the red table line printed whenever the dealer skips bulk
// (#2725); empty when bulk flows.
func (v Verdict) DealerLine() string {
	if !v.Blocked {
		return ""
	}
	if v.Source == SourceHash {
		return fmt.Sprintf("dealer: blocked debt=%d cap=%d", v.Debt, v.Cap)
	}
	return "dealer: blocked " + strings.Replace(v.Line(), "backpressure: ", "backpressure ", 1)
}

// EvaluateBackpressure is the pure rule over the hash fields (nil or empty when
// the key is missing), the declared policy and Redis server time in ms.
func EvaluateBackpressure(hash map[string]string, policy string, nowMs int64) (Verdict, error) {
	if policy != PolicyOpen && policy != PolicyClosed {
		if policy == "" {
			return Verdict{}, ErrPolicyUndeclared
		}
		return Verdict{}, fmt.Errorf("%w: %s=%q", ErrPolicyUndeclared, PolicyField, policy)
	}
	fallback := func(source string, age time.Duration) Verdict {
		return Verdict{Source: source, Policy: policy, Debt: -1, Cap: -1, Age: age, Blocked: policy == PolicyClosed}
	}
	if len(hash) == 0 {
		return fallback(SourceMissing, 0), nil
	}
	state := hash["state"]
	atMs, err := strconv.ParseInt(hash["at"], 10, 64)
	if (state != "ON" && state != "OFF") || err != nil || atMs <= 0 {
		return fallback(SourceInvalid, 0), nil
	}
	age := time.Duration(nowMs-atMs) * time.Millisecond
	if age < 0 {
		age = 0
	}
	if age > StaleAfter {
		return fallback(SourceStale, age), nil
	}
	v := Verdict{Source: SourceHash, Policy: policy, State: state, Debt: atoiOr(hash["debt"], -1), Cap: atoiOr(hash["cap"], -1), Age: age}
	v.Blocked = state == "ON"
	return v, nil
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// ReadBackpressure reads the hash, the declared policy and Redis TIME in one
// pipeline (one round trip) and evaluates them. Clients never supply a time.
func ReadBackpressure(ctx context.Context, st *store.Store, sprint string) (Verdict, error) {
	if st == nil || st.Client() == nil {
		return Verdict{}, fmt.Errorf("backpressure: nil store")
	}
	if sprint == "" {
		return Verdict{}, fmt.Errorf("backpressure: sprint is required")
	}
	pipe := st.Client().Pipeline()
	timeCmd := pipe.Time(ctx)
	hashCmd := pipe.HGetAll(ctx, BackpressureKey(sprint))
	policyCmd := pipe.HGet(ctx, PolicyKey(sprint), PolicyField)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return Verdict{}, fmt.Errorf("backpressure: read %s: %w", sprint, err)
	}
	now, err := timeCmd.Result()
	if err != nil {
		return Verdict{}, fmt.Errorf("backpressure: TIME: %w", err)
	}
	hash, err := hashCmd.Result()
	if err != nil {
		return Verdict{}, fmt.Errorf("backpressure: HGETALL %s: %w", BackpressureKey(sprint), err)
	}
	policy, err := policyCmd.Result()
	if err != nil && err != redis.Nil {
		return Verdict{}, fmt.Errorf("backpressure: HGET %s %s: %w", PolicyKey(sprint), PolicyField, err)
	}
	return EvaluateBackpressure(hash, policy, now.UnixMilli())
}

// CheckOneBackpressureKey lists every Redis key naming backpressure and
// refuses any second source of truth for sprint S. Allowed: this sprint's
// s:<S>:backpressure (zero or one), other sprints' own s:<T>:backpressure, and
// the backpressure process beat proc:backpressure. Anything else, such as the
// v1 global `backpressure` key, is ErrTwoBackpressureKeys naming the keys. It
// returns this sprint's keys (empty or exactly one). It uses SCAN, so it is a
// preflight check, never part of the table snapshot.
func CheckOneBackpressureKey(ctx context.Context, st *store.Store, sprint string) ([]string, error) {
	if st == nil || st.Client() == nil {
		return nil, fmt.Errorf("backpressure: nil store")
	}
	mine := BackpressureKey(sprint)
	var own, extra []string
	iter := st.Client().Scan(ctx, 0, "*backpressure*", 1000).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		switch {
		case key == mine:
			own = append(own, key)
		case key == "proc:backpressure":
		case isSprintBackpressureKey(key):
		default:
			extra = append(extra, key)
		}
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("backpressure: scan: %w", err)
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		quoted := make([]string, len(extra))
		for i, k := range extra {
			quoted[i] = strconv.Quote(k)
		}
		return own, fmt.Errorf("%w: %s beside %q", ErrTwoBackpressureKeys, strings.Join(quoted, ", "), mine)
	}
	return own, nil
}

// isSprintBackpressureKey matches s:<name>:backpressure for a valid sprint
// name ([a-z0-9-]{1,40}, #2756 section 2.1 rule 10).
func isSprintBackpressureKey(key string) bool {
	name, ok := strings.CutPrefix(key, "s:")
	if !ok {
		return false
	}
	name, ok = strings.CutSuffix(name, ":backpressure")
	if !ok || len(name) < 1 || len(name) > 40 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
