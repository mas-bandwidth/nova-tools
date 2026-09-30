package sprint

import (
	"fmt"
	"sort"
	"sync"
)

// The rule registry, the last part of IT05 (the upper design, version 2.1,
// sections 1.4.2 and 8.0): each rules file registers its rules in init, and the
// tick reads them in priority order. A Rule (plan_types.go) is a pure pair:
// Read says what its keys need read, and Plan turns the answer into a
// RulePlan.

// RulePriorities are the rules in the order of 1.4.2, which is also the order
// of its round robin: R1, R2 (presence); R4 (needs); R3 (resolve); R5 (cross);
// R6 (deal); R7 (level); R8 (ask); R9 (accept); R10 (rework); R11 (late); R12,
// R13, R14, R18 (overdue, hold, remind, behind); R15 (done); R19 (pull back);
// then R16 (held), on its own queue. R17 (stopped) has no agenda key and no
// row. A rules file takes its rule's priority from here, so that a change to
// the order is a changed row.
var RulePriorities = []struct {
	Rule     string
	Priority int
}{
	{"seen", 1},      // R1
	{"down", 2},      // R2
	{"needs", 3},     // R4
	{"resolve", 4},   // R3
	{"cross", 5},     // R5
	{"deal", 6},      // R6
	{"level", 7},     // R7
	{"ask", 8},       // R8
	{"accept", 9},    // R9
	{"rework", 10},   // R10
	{"late", 11},     // R11
	{"overdue", 12},  // R12
	{"hold", 13},     // R13
	{"remind", 14},   // R14
	{"behind", 15},   // R18
	{"done", 16},     // R15
	{"pullback", 17}, // R19
	{"held", 18},     // R16
}

// PriorityOf is the priority of a rule by RulePriorities.
func PriorityOf(name string) (int, bool) {
	for _, r := range RulePriorities {
		if r.Rule == name {
			return r.Priority, true
		}
	}
	return 0, false
}

// ruleSet is a registry of rules. The tick's is defaultRules; a test makes its
// own, so that the rules it registers are not the tick's.
type ruleSet struct {
	mu    sync.Mutex
	rules map[string]Rule
}

// register adds a rule. A rule that cannot run (no name, no Read or Plan, a
// negative cap) or one already registered by that name is a fault of the
// program, found when it starts, so register panics.
func (rs *ruleSet) register(r Rule) {
	switch {
	case r.Name == "":
		panic("sprint: RegisterRule: a rule with no name")
	case r.Read == nil || r.Plan == nil:
		panic(fmt.Sprintf("sprint: RegisterRule: rule %q has no %s", r.Name, missingFunc(r)))
	case r.MaxSteps < 0:
		panic(fmt.Sprintf("sprint: RegisterRule: rule %q has a step cap of %d", r.Name, r.MaxSteps))
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, dup := rs.rules[r.Name]; dup {
		panic(fmt.Sprintf("sprint: RegisterRule: rule %q is registered twice", r.Name))
	}
	if rs.rules == nil {
		rs.rules = map[string]Rule{}
	}
	rs.rules[r.Name] = r
}

// missingFunc names the function a rule lacks.
func missingFunc(r Rule) string {
	if r.Read == nil {
		return "Read"
	}
	return "Plan"
}

// table is the rules in priority order, then by name.
func (rs *ruleSet) table() []Rule {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]Rule, 0, len(rs.rules))
	for _, r := range rs.rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// defaultRules are the rules the tick runs.
var defaultRules = &ruleSet{}

// RegisterRule adds a rule to the tick's; each rules file calls it from init.
// It panics on a rule with no name, no Read or Plan, or a negative MaxSteps,
// and on a name registered twice.
func RegisterRule(r Rule) { defaultRules.register(r) }

// RuleTable is the tick's rules in priority order (a copy: changing it changes
// nothing).
func RuleTable() []Rule { return defaultRules.table() }
