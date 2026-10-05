package config

import (
	"fmt"
	"strconv"
	"time"
)

// The sprint's policy numbers (the owner, 2026-10-02: "i just want to set numbers as I
// see fit directly in nova-config"; docs/SPEC-CONFIG.md, "The sprint's policy numbers"):
// each a field of the sprint row, empty (the default) being the sprint's compiled default,
// today's value. Apply writes each to SprintKey(name), which the sprint's routes read
// takes into every tick, so a set and an apply change the tick's next pass with no
// rebuild. internal/sprint holds each default as its constant (PolicyDefaults), and
// TestEveryPolicyNumberIsASettingWithItsDefault holds the two equal.

// The two kinds of a policy number.
const (
	PolicyDuration = "duration" // a Go duration (90s, 20m, 2h), between Min and Max
	PolicyCount    = "count"    // a whole number, between Min and Max
)

// Policy is one policy number: its sprint row field, its kind, its default as the
// field's help says it, its range, its unit and what it decides.
type Policy struct {
	Name, Type, Default, Min, Max, Unit, Meaning string
}

// SprintPolicies is every policy number of the sprint, in the order set --list prints them.
var SprintPolicies = []Policy{
	{"deal_ahead", PolicyCount, "2", "1", "10", "times a member's width", "the cards the deal holds on a member or a friend, ready and working: this times its width"},
	{"max_redeals", PolicyCount, "3", "0", "20", "redeals", "the redeals of one work card before its next deal escalates it or raises the redeal-bound judgment"},
	{"max_read_reasks", PolicyCount, "2", "0", "20", "re-asks", "how often a returned read is asked again in place before it is retired"},
	{"read_lease", PolicyDuration, "10m0s", "1m", "6h", "duration", "the lease of a read in progress, renewed by the reader's beat"},
	{"readers_window", PolicyDuration, "10m0s", "1m", "24h", "duration", "how long a read may wait asked and not begun before the readers are behind"},
	{"friend_read_deadline", PolicyDuration, "2h0m0s", "10m", "48h", "duration", "the deadline of a read card dealt to a friend"},
	{"friend_idle", PolicyDuration, "20m0s", "1m", "24h", "duration", "how long a friend holding cards may show no file write before it is an alarm"},
	{"friend_stall_after", PolicyDuration, "20m0s", "1m", "24h", "duration", "how long a friend holding cards may show neither file write nor card progress before the stall ladder begins"},
	{"friend_stall_step", PolicyDuration, "5m0s", "1m", "6h", "duration", "the time between rungs of the friend stall ladder"},
	{"member_down_after", PolicyDuration, "45s", "15s", "1h", "duration", "how long a fleet member may go without a beat before seat check calls it DOWN"},
	{"loop_silence", PolicyDuration, "15s", "5s", "1h", "duration", "how long the run loop may go unseen before seat check calls it DOWN"},
	{"overload_timeouts", PolicyCount, "3", "1", "100", "timeouts", "the timeouts within overload_window that make a member overloaded"},
	{"overload_window", PolicyDuration, "15m0s", "1m", "24h", "duration", "the window the overload count is taken over"},
	{"promote_cards", PolicyCount, "25", "1", "1000", "cards", "the cards landed since the last promotion that make dev behind"},
	{"promote_age", PolicyDuration, "30m0s", "1m", "24h", "duration", "how long the oldest card landed since the last promotion may wait before dev is behind"},
	{"remind_every", PolicyDuration, "5m0s", "1m", "24h", "duration", "the running time between two reminders of one person's goal"},
	{"balance_poll_every", PolicyDuration, "10m0s", "1m", "24h", "duration", "how often nova-sprint balance reads each provider's balance"},
	{"flash_gate_bound", PolicyDuration, "15m0s", "1m", "6h", "duration", "the gate wall over which nova-sprint add starts a card on pro"},
	{"friend_observed_down_after", PolicyDuration, "10s", "5s", "1h", "duration", "how long the coordinator's observation of a friend stands before she is down without a newer proof"},
	{"rollback_window", PolicyDuration, "15m0s", "1m", "24h", "duration", "how long nova-sprint server switch --rollback watches for a failed land before the switch is permanent, when no --window is given"},
	{"critical_behind", PolicyCount, "10", "1", "1000", "cards behind", "the cards waiting on a primary that make it critical: it starts on pro and the inbox marks its judgments CRITICAL"},
}

// LookupPolicy is the policy number of the name.
func LookupPolicy(name string) (Policy, bool) {
	for _, p := range SprintPolicies {
		if p.Name == name {
			return p, true
		}
	}
	return Policy{}, false
}

// Range is the values the number takes, as a refusal names them.
func (p Policy) Range() string {
	if p.Type == PolicyCount {
		return fmt.Sprintf("a whole number from %s to %s", p.Min, p.Max)
	}
	return fmt.Sprintf("a duration from %s to %s", p.Min, p.Max)
}

// Help is the sprint row field's help line.
func (p Policy) Help() string {
	return fmt.Sprintf("%s; %s (%s); empty (the default) is the sprint's default, %s", p.Meaning, p.Range(), p.Unit, p.Default)
}

// Duration is the value of a duration policy number, refused outside its range.
func (p Policy) Duration(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	lo, _ := time.ParseDuration(p.Min)
	hi, _ := time.ParseDuration(p.Max)
	if p.Type != PolicyDuration || err != nil || d < lo || d > hi {
		return 0, p.refused(v)
	}
	return d, nil
}

// Count is the value of a count policy number, refused outside its range.
func (p Policy) Count(v string) (int, error) {
	n, err := strconv.Atoi(v)
	lo, _ := strconv.Atoi(p.Min)
	hi, _ := strconv.Atoi(p.Max)
	if p.Type != PolicyCount || err != nil || n < lo || n > hi {
		return 0, p.refused(v)
	}
	return n, nil
}

// Check is nil for a value the number takes, empty (its default) included, and else the
// refusal naming the number and its range.
func (p Policy) Check(v string) error {
	if v == "" {
		return nil
	}
	var err error
	if p.Type == PolicyCount {
		_, err = p.Count(v)
	} else {
		_, err = p.Duration(v)
	}
	return err
}

func (p Policy) refused(v string) error {
	return fmt.Errorf("%s wants %s (%s), or empty for its default %s; found %q", p.Name, p.Range(), p.Unit, p.Default, v)
}
