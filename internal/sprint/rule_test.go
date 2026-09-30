package sprint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The rule registry (the upper design, version 2.1, 1.4.2, 8.0 and IT05).

// testRule is a rule that reads nothing and plans nothing.
func testRule(name string, priority int) Rule {
	return Rule{
		Name:     name,
		Priority: priority,
		Read:     func([]AgendaKey, ReadBounds, int) (ReadPlan, []AgendaKey) { return ReadPlan{}, nil },
		Plan:     func(*Snapshot, []AgendaKey, Now) RulePlan { return RulePlan{} },
	}
}

func names(rs []Rule) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func TestRuleTablePriorityOrder(t *testing.T) {
	t.Parallel()
	var rs ruleSet
	// Registered in no order at all, with two of one priority.
	for _, r := range []Rule{testRule("c", 30), testRule("a", 10), testRule("e", 20), testRule("d", 20), testRule("b", 5)} {
		rs.register(r)
	}
	if got, want := names(rs.table()), []string{"b", "a", "d", "e", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rule table %v, want %v: priority first, then name", got, want)
	}

	// The table is a copy: changing it changes nothing.
	tab := rs.table()
	tab[0].Name = "changed"
	tab[1] = Rule{}
	if got := names(rs.table()); got[0] != "b" || got[1] != "a" {
		t.Fatalf("the registry changed through its table: %v", got)
	}

	// An empty registry has an empty table.
	if got := (&ruleSet{}).table(); len(got) != 0 {
		t.Fatalf("an empty registry: %v", got)
	}

	// The caps and functions come back as registered.
	var one ruleSet
	r := testRule("late", 11)
	r.MaxSteps = 1
	one.register(r)
	got := one.table()[0]
	if got.Name != "late" || got.Priority != 11 || got.MaxSteps != 1 || got.Read == nil || got.Plan == nil {
		t.Fatalf("registered rule: %+v", got)
	}
}

func TestRegisterRuleRefusesWhatCannotRun(t *testing.T) {
	t.Parallel()
	noRead, noPlan := testRule("x", 1), testRule("y", 1)
	noRead.Read, noPlan.Plan = nil, nil
	negative := testRule("z", 1)
	negative.MaxSteps = -1
	var rs ruleSet
	rs.register(testRule("ok", 1))
	for name, tt := range map[string]struct {
		rule Rule
		want string
	}{
		"no name":             {testRule("", 1), "no name"},
		"no Read":             {noRead, "no Read"},
		"no Plan":             {noPlan, "no Plan"},
		"a negative cap":      {negative, "step cap of -1"},
		"a name twice":        {testRule("ok", 2), "registered twice"},
		"a name twice, again": {testRule("ok", 1), "registered twice"},
	} {
		msg := mustPanic(t, func() { rs.register(tt.rule) })
		if !strings.Contains(msg, tt.want) {
			t.Errorf("%s: panicked with %q, want %q", name, msg, tt.want)
		}
	}
	if got := names(rs.table()); !reflect.DeepEqual(got, []string{"ok"}) {
		t.Fatalf("a refused rule was registered: %v", got)
	}
	// The tick's registry takes the same, and RegisterRule's refusals are its own.
	if msg := mustPanic(t, func() { RegisterRule(testRule("", 1)) }); !strings.Contains(msg, "RegisterRule") {
		t.Fatalf("RegisterRule: %q", msg)
	}
	for _, r := range RuleTable() {
		if r.Name == "" || r.Read == nil || r.Plan == nil {
			t.Fatalf("the tick's table holds a rule that cannot run: %+v", r)
		}
	}
}

func TestRuleRegistryIsSafeToRegisterFromManyGoroutines(t *testing.T) {
	t.Parallel()
	var rs ruleSet
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rs.register(testRule(fmt.Sprintf("r%02d", i), 100-i))
			_ = rs.table()
		}()
	}
	wg.Wait()
	tab := rs.table()
	if len(tab) != 32 || tab[0].Name != "r31" || tab[31].Name != "r00" {
		t.Fatalf("%d rules, first %s, last %s", len(tab), tab[0].Name, tab[len(tab)-1].Name)
	}
}

func TestRulePrioritiesAreTheOrderOfTheDesign(t *testing.T) {
	t.Parallel()
	// 1.4.2: R1, R2; R4; R3; R5; R6; R7; R8; R9; R10; R11; R12, R13, R14, R18;
	// R15; R19; then R16 on its own queue.
	want := []string{"seen", "down", "needs", "resolve", "cross", "deal", "level", "ask", "accept", "rework", "late",
		"overdue", "hold", "remind", "behind", "done", "pullback", "held"}
	var got []string
	last := 0
	for _, r := range RulePriorities {
		if r.Priority <= last {
			t.Errorf("%s has priority %d after %d", r.Rule, r.Priority, last)
		}
		last = r.Priority
		got = append(got, r.Rule)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("priorities:\n got %v\nwant %v", got, want)
	}
	for i, name := range want {
		if p, ok := PriorityOf(name); !ok || p != i+1 {
			t.Errorf("PriorityOf(%s) = %d, %v", name, p, ok)
		}
	}
	if p, ok := PriorityOf("stopped"); ok || p != 0 {
		t.Errorf("R17 has no agenda key and no row: %d, %v", p, ok)
	}

	// Registered in any order, the rules run in the design's.
	var rs ruleSet
	for i := len(want) - 1; i >= 0; i-- {
		p, _ := PriorityOf(want[i])
		rs.register(testRule(want[i], p))
	}
	if got := names(rs.table()); !reflect.DeepEqual(got, want) {
		t.Fatalf("rule table %v", got)
	}
}

// ingestRuleWords are the words of the keys ingest.go makes: every string
// constant of its `rule...` block, read from the source so that a word added
// there is found here and not forgotten.
func ingestRuleWords(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "ingest.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	words := map[string]string{}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !strings.HasPrefix(name.Name, "rule") || !ok || lit.Kind != token.STRING {
					continue
				}
				words[name.Name] = strings.Trim(lit.Value, `"`)
			}
		}
	}
	return words
}

func TestEveryKeyIngestMakesIsServedByARuleWithAPriority(t *testing.T) {
	t.Parallel()
	words := ingestRuleWords(t)
	if len(words) < 14 {
		t.Fatalf("read %d rule words from ingest.go: %v", len(words), words)
	}
	for name, word := range words {
		rule := ServingRule(word + ":x")
		if p, ok := PriorityOf(rule); !ok || p == 0 {
			t.Errorf("%s (%q): the key is served by %q, which has no priority", name, word, rule)
		}
		if rule != ServingRule(word+"@7") || rule != ServingRule(word) {
			t.Errorf("%s (%q): the key's shape changes its rule", name, word)
		}
	}
	// The keys whose word is another rule's: askwait is R8's, made is R4's, idle is R11's.
	for key, want := range map[string]string{
		"askwait": "ask", "made:n1": "needs", "made@7": "needs", "idle:s1": "late",
		"ask:p1": "ask", "ask@48213": "ask", "deal": "deal", "held@9": "held", "resolve:s1": "resolve",
	} {
		if got := ServingRule(key); got != want {
			t.Errorf("ServingRule(%q) = %q, want %q", key, got, want)
		}
	}
	if got := ServingRule("nothing:at:all"); got != "nothing" {
		t.Errorf("a word that is no rule's is its own: %q", got)
	}
	if _, ok := PriorityOf(ServingRule("nothing:at:all")); ok {
		t.Error("a word that is no rule's has a priority")
	}
}
