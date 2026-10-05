package sprint

import (
	"fmt"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// The sprint's policy numbers are settings (the owner, 2026-10-02: "i just want to set
// numbers as I see fit directly in nova-config"; docs/SPEC-SPRINT.md section 11, "The
// policy numbers"): one table, config.SprintPolicies (each number's name, kind, range, unit
// and meaning), and one reader, the snapshot's (PolicyDuration, PolicyCount), through which
// every use site reads its number each pass, never through its constant. The value is the
// one the store gave the snapshot (Snapshot.Policy) when it is in the number's range; else,
// for friend_idle, friend_stall_after and friend_stall_step, nova-sprint set's property;
// else the default, the constant PolicyDefaults names, today's value. The store does not
// give Snapshot.Policy yet: its read of nova-config's sprint:<name> keys is the next layer
// (internal/sprint/store/routes.go), so today every number but those three is its default.

// The policy numbers' names, config.SprintPolicies' fields.
const (
	PolicyDealAhead               = "deal_ahead"
	PolicyMaxRedeals              = "max_redeals"
	PolicyMaxReadReasks           = "max_read_reasks"
	PolicyReadLease               = "read_lease"
	PolicyReadersWindow           = "readers_window"
	PolicyFriendReadDeadline      = "friend_read_deadline"
	PolicyFriendIdle              = "friend_idle"
	PolicyFriendStallAfter        = "friend_stall_after"
	PolicyFriendStallStep         = "friend_stall_step"
	PolicyMemberDownAfter         = "member_down_after"
	PolicyLoopSilence             = "loop_silence"
	PolicyFriendObservedDownAfter = "friend_observed_down_after"
	PolicyOverloadTimeouts        = "overload_timeouts"
	PolicyOverloadWindow          = "overload_window"
	PolicyPromoteCards            = "promote_cards"
	PolicyPromoteAge              = "promote_age"
	PolicyRemindEvery             = "remind_every"
	PolicyBalancePollEvery        = "balance_poll_every"
	PolicyFlashGateBound          = "flash_gate_bound"
)

// PolicyDefaults is each policy number's default: its constant, an int or a
// time.Duration. The tick reads a number only through this table, never its constant.
var PolicyDefaults = map[string]any{
	PolicyDealAhead:               DealAhead,
	PolicyMaxRedeals:              MaxRedeals,
	PolicyMaxReadReasks:           MaxReadReasks,
	PolicyReadLease:               DefaultReadLease,
	PolicyReadersWindow:           ReadersWindow,
	PolicyFriendReadDeadline:      FriendReadDeadline,
	PolicyFriendIdle:              FriendIdleDefault,
	PolicyFriendStallAfter:        FriendStallAfterDefault,
	PolicyFriendStallStep:         FriendStallStepDefault,
	PolicyMemberDownAfter:         MemberDownAfter,
	PolicyLoopSilence:             LoopSilence,
	PolicyFriendObservedDownAfter: FriendObservedDownAfter,
	PolicyOverloadTimeouts:        OverloadTimeouts,
	PolicyOverloadWindow:          OverloadWindow,
	PolicyPromoteCards:            PromoteCards,
	PolicyPromoteAge:              PromoteAge,
	PolicyRemindEvery:             RemindEvery,
	PolicyBalancePollEvery:        BalancePollEvery,
	PolicyFlashGateBound:          FlashGateBound,
}

// PolicyCompiled is the policy numbers that are not settings yet, each with what blocks
// it: set --list prints them with source compiled.
var PolicyCompiled = []struct {
	Name, Value, Why string
}{
	{"critical_behind", strconv.Itoa(CriticalBehind), "read without a snapshot (IsCritical, ceilingTier and CardTiers, through the route and decide paths)"},
	{"rollback_window", DefaultRollbackWindow.String(), "server switch's own --window flag (default 15m), which opens no store"},
}

// Where a policy number's value came from, as set --list says it.
const (
	SourceConfig   = "nova-config" // the store's sprint:<name>, nova-config's applied value
	SourceSet      = "nova-sprint set"
	SourceDefault  = "default"
	SourceCompiled = "compiled"
)

// PolicyValues is the policy numbers as the store read them, by name: a value is taken
// only once its range is checked (policyValue).
type PolicyValues map[string]string

// policyValue is the number's value and its source: the store's when it gave one in
// range, else the sprint's set property for the three set takes too (friend_idle,
// friend_stall_after, friend_stall_step), else the default. A nil snapshot is the
// defaults'.
func (s *Snapshot) policyValue(name string) (any, string) {
	pol, ok := config.LookupPolicy(name)
	if !ok {
		panic("sprint: no policy number " + name)
	}
	parse := func(v string) (any, bool) {
		if pol.Type == config.PolicyCount {
			n, err := pol.Count(v)
			return n, err == nil
		}
		d, err := pol.Duration(v)
		return d, err == nil
	}
	if s != nil {
		if v, ok := s.Policy[name]; ok && v != "" {
			if x, ok := parse(v); ok {
				return x, SourceConfig
			}
		}
		if prop, ok := setProps[name]; ok && s.Work != nil {
			if v, ok := s.Work.Prop(prop); ok {
				if d, err := time.ParseDuration(v); err == nil && d > 0 {
					return d, SourceSet
				}
			}
		}
	}
	return PolicyDefaults[name], SourceDefault
}

// setProps is the policy numbers nova-sprint set writes too, by their work table property.
var setProps = map[string]string{
	PolicyFriendIdle:       PropFriendIdle,
	PolicyFriendStallAfter: PropFriendStallAfter,
	PolicyFriendStallStep:  PropFriendStallStep,
}

// PolicyDuration is the duration policy number's effective value.
func (s *Snapshot) PolicyDuration(name string) time.Duration {
	v, _ := s.policyValue(name)
	return v.(time.Duration)
}

// PolicyCount is the count policy number's effective value.
func (s *Snapshot) PolicyCount(name string) int {
	v, _ := s.policyValue(name)
	return v.(int)
}

// PolicySetting is one line of set --list: a policy number, its effective value, where
// that came from, its default, unit, range and meaning.
type PolicySetting struct {
	Name, Value, Source, Default, Unit, Range, Meaning string
}

// String is the setting as set --list prints it.
func (p PolicySetting) String() string {
	return fmt.Sprintf("%s=%s source=%s default=%s unit=%q range=%q", p.Name, p.Value, p.Source, p.Default, p.Unit, p.Range)
}

// PolicySettings is every policy number as the tick reads it now, in the table's order.
func (s *Snapshot) PolicySettings() []PolicySetting {
	var out []PolicySetting
	for _, pol := range config.SprintPolicies {
		v, src := s.policyValue(pol.Name)
		out = append(out, PolicySetting{Name: pol.Name, Value: policyText(v), Source: src, Default: policyText(PolicyDefaults[pol.Name]),
			Unit: pol.Unit, Range: pol.Range(), Meaning: pol.Meaning})
	}
	for _, c := range PolicyCompiled {
		out = append(out, PolicySetting{Name: c.Name, Value: c.Value, Source: SourceCompiled, Default: c.Value, Range: "not a setting yet", Meaning: c.Why})
	}
	return out
}

// policyText is a policy number's value as its setting is written.
func policyText(v any) string {
	if n, ok := v.(int); ok {
		return strconv.Itoa(n)
	}
	return fmt.Sprint(v)
}

// policyCap is the most a count policy number can be set to: a bound a loop over stored
// records walks to whatever the number is now (ProviderTakes).
func policyCap(name string) int {
	pol, _ := config.LookupPolicy(name)
	n, _ := strconv.Atoi(pol.Max)
	return n
}
