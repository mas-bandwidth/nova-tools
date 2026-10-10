package docs

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// coordrules_test.go holds the coordinator's numbered rules, section 10 of
// docs/SPRINT-COORDINATOR.md, to the tools they name. A coordinator who adopts
// nova-sprint runs the seat from those rules alone, so a rule whose verb, flag
// or setting is not in the tool is a rule that cannot be followed. Each rule is
//
//	R<n>. **<the rule, one sentence, wrapped as it needs>**
//	    Why: <the failure it prevents>
//	    Done by: `<verb line>`, `<verb line>`; or judgment, the seat's own call
//	    Card: <id> (optional: the card that makes it mechanical)
//
// numbered from 1 with no gap. Every code span of Done by is checked as
// TestEveryCoordinatorToolMapsToARealNovaVerb checks a verb line, and a
// nova-config line against the kinds and fields of pkg/config: a setting
// is `nova-config <kind> set ... --<field> <value>`.

// coordinatorRunbookPath is the runbook, relative to this package.
const coordinatorRunbookPath = "../../docs/SPRINT-COORDINATOR.md"

// rulesSection is the heading of the runbook's numbered rules.
const rulesSection = "## 10. The rules"

// ruleHeadRe reads a rule's first line: its number and the start of its
// sentence, which runs on over the unindented lines after it to the closing **.
var ruleHeadRe = regexp.MustCompile(`^R([0-9]+)\. \*\*(.+)$`)

// judgmentDoneBy is the Done by of a rule no verb carries out: the seat's own
// call. It is said, never left blank.
const judgmentDoneBy = "judgment"

// coordRule is one numbered rule of the runbook.
type coordRule struct {
	line, n                 int
	rule, why, doneBy, card string
}

// ruleLikeRe is a line that starts as a rule does; one ruleHeadRe refuses is
// a rule written in another shape, and so unread.
var ruleLikeRe = regexp.MustCompile(`^R[0-9]`)

// coordRules reads the rules of the section. A field runs on over the indented
// lines after it until the next field, a blank line or the next rule.
func coordRules(text string) (rules []coordRule, problems []string) {
	in := false
	var cur *coordRule
	var last *string
	open := false // the sentence of cur has not reached its closing **
	for i, line := range strings.Split(text, "\n") {
		at := "line " + strconv.Itoa(i+1) + ": "
		if open && (strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ")) {
			problems = append(problems, at+"R"+strconv.Itoa(cur.n)+"'s sentence is not closed by **")
			open = false
		}
		if strings.HasPrefix(line, "## ") {
			in, cur, last = line == rulesSection, nil, nil
			continue
		}
		if !in {
			continue
		}
		if open {
			cur.rule += " " + strings.TrimSpace(line)
			if strings.HasSuffix(cur.rule, "**") {
				cur.rule, open = strings.TrimSuffix(cur.rule, "**"), false
			}
			continue
		}
		if m := ruleHeadRe.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			rules = append(rules, coordRule{line: i + 1, n: n, rule: m[2]})
			cur, last = &rules[len(rules)-1], nil
			if strings.HasSuffix(cur.rule, "**") {
				cur.rule = strings.TrimSuffix(cur.rule, "**")
			} else {
				open = true
			}
			continue
		}
		body, indented := strings.CutPrefix(line, "    ")
		switch {
		case strings.TrimSpace(line) == "":
			cur, last = nil, nil
		case cur != nil && indented:
			field := false
			for _, f := range []struct {
				key string
				to  *string
			}{{"Why: ", &cur.why}, {"Done by: ", &cur.doneBy}, {"Card: ", &cur.card}} {
				if v, ok := strings.CutPrefix(body, f.key); ok {
					if *f.to != "" {
						problems = append(problems, at+"R"+strconv.Itoa(cur.n)+" has a second "+strings.TrimSpace(f.key))
					}
					*f.to, last, field = strings.TrimSpace(v), f.to, true
					break
				}
			}
			if !field && last != nil {
				*last += " " + strings.TrimSpace(body)
			}
		case ruleLikeRe.MatchString(line):
			problems = append(problems, at+"a rule's first line is R<n>. **<the rule>**")
		}
	}
	if open {
		problems = append(problems, "R"+strconv.Itoa(cur.n)+"'s sentence is not closed by **")
	}
	return rules, problems
}

// configTool is nova-config's grammar read from pkg/config: each kind's
// verbs and fields, the verbs of the store, and the flags every verb takes.
type configTool struct {
	kinds  map[string]*config.Kind
	common map[string]bool
	top    map[string]map[string]bool
}

func readConfigTool() configTool {
	ct := configTool{kinds: map[string]*config.Kind{},
		common: map[string]bool{"as": true, "dry-run": true, "json": true, "pg": true, "file": true, "redis": true, "seat": true},
		top: map[string]map[string]bool{
			"migrate": {"print": true}, "status": {}, "apply": {"kind": true}, "inventory": {"list": true, "host": true, "fixture": true, "timeout": true},
			"kinds": {}, "logout": {}, "login": {"store": true, "key": true, "secret": true, "dsn": true, "friend": true, "sops": true, "check": true},
		}}
	for _, k := range config.Kinds {
		ct.kinds[k.Name] = k
	}
	return ct
}

// check is the problems of one nova-config line, its words after the tool.
func (ct configTool) check(words []string) []string {
	if len(words) == 0 {
		return []string{"nova-config names no verb"}
	}
	flagsOK := func(allowed func(string) bool, what string) (ps []string) {
		for _, w := range words {
			if w == "--" {
				break
			}
			f, ok := strings.CutPrefix(w, "--")
			if !ok {
				continue
			}
			f, _, _ = strings.Cut(f, "=")
			if !ct.common[f] && !allowed(f) {
				ps = append(ps, what+" has no flag --"+f)
			}
		}
		return ps
	}
	if extra, ok := ct.top[words[0]]; ok {
		return flagsOK(func(f string) bool { return extra[f] }, "nova-config "+words[0])
	}
	k, ok := ct.kinds[words[0]]
	if !ok {
		return []string{"nova-config has no verb or kind " + strconv.Quote(words[0])}
	}
	if len(words) < 2 {
		return []string{"nova-config " + k.Name + " names no verb"}
	}
	verbs := map[string]bool{"set": true, "show": true, "history": true}
	if !k.Singleton {
		verbs["add"], verbs["remove"], verbs["list"] = true, true, true
	}
	if k.Name == config.KindMachine {
		verbs["width"], verbs["self"] = true, true
	}
	if !verbs[words[1]] {
		return []string{"nova-config " + k.Name + " has no verb " + strconv.Quote(words[1])}
	}
	fields := map[string]bool{}
	for _, f := range k.Fields {
		fields[f.Name] = true
	}
	return flagsOK(func(f string) bool { return fields[f] || (k.Name == config.KindMachine && f == "check") }, "nova-config "+k.Name+" "+words[1])
}

// checkCoordRules is every problem of the runbook's rules.
func checkCoordRules(tools map[string]novaTool, ct configTool, text string) []string {
	rules, problems := coordRules(text)
	if len(rules) == 0 {
		return append(problems, "no rule under "+strconv.Quote(rulesSection))
	}
	say := func(r coordRule, s string) {
		problems = append(problems, "line "+strconv.Itoa(r.line)+" (R"+strconv.Itoa(r.n)+"): "+s)
	}
	seen := map[string]int{}
	for i, r := range rules {
		if r.n != i+1 {
			say(r, "rules are numbered from 1 with no gap: want R"+strconv.Itoa(i+1))
		}
		if prev, ok := seen[r.rule]; ok {
			say(r, "the same rule as R"+strconv.Itoa(prev))
		}
		seen[r.rule] = r.n
		if r.why == "" {
			say(r, "a rule wants a Why: line, the failure it prevents")
		}
		if r.doneBy == "" {
			say(r, "a rule wants a Done by: line, the verb or setting that does it, or "+judgmentDoneBy)
			continue
		}
		if strings.HasPrefix(r.doneBy, judgmentDoneBy) {
			continue
		}
		checked := false
		for _, s := range codeSpanRe.FindAllStringSubmatch(r.doneBy, -1) {
			words := strings.Fields(s[1])
			if len(words) > 0 && words[0] == "nova-config" {
				checked = true
				for _, p := range ct.check(words[1:]) {
					say(r, p)
				}
				continue
			}
			ok, ps := checkCommand(tools, words)
			checked = checked || ok
			for _, p := range ps {
				say(r, p)
			}
		}
		if !checked {
			say(r, "Done by names no nova verb line or setting; a rule no verb carries out says "+judgmentDoneBy)
		}
	}
	return problems
}

// TestEveryCoordinatorRuleNamesVerbsThatExist is the contract: every numbered
// rule of docs/SPRINT-COORDINATOR.md section 10 says why, and names verbs,
// flags and settings the tools carry, or says judgment. The witnesses run the
// same check on rules that must be refused, so a check that passes everything
// fails here.
func TestEveryCoordinatorRuleNamesVerbsThatExist(t *testing.T) {
	t.Parallel()
	tools := readNovaTools(t)
	ct := readConfigTool()

	t.Run("the runbook", func(t *testing.T) {
		raw, err := os.ReadFile(coordinatorRunbookPath)
		require.NoError(t, err, "%s: %v", coordinatorRunbookPath, err)
		rules, _ := coordRules(string(raw))
		require.GreaterOrEqual(t, len(rules), 90, "%s carries fewer rules than the coordinator follows", coordinatorRunbookPath)
		for _, p := range checkCoordRules(tools, ct, string(raw)) {
			t.Errorf("%s %s", coordinatorRunbookPath, p)
		}
	})

	t.Run("the witnesses", func(t *testing.T) {
		rule := func(n int, done string) string {
			return "R" + strconv.Itoa(n) + ". **Rule " + strconv.Itoa(n) + ".**\n    Why: a failure.\n    Done by: " + done + "\n\n"
		}
		head := "# x\n\n" + rulesSection + "\n\n"
		good := head + rule(1, "`nova-sprint fleet level`, `nova-sprint set --friend-finish 30m`.") +
			rule(2, "`nova-config sprint set --decide_judgment_bar 0.8 --as <c>`; `nova-config apply --kind sprint --as <c>`.") +
			rule(3, "`nova-friend ping --as <c> --to <f>`; `nova-config route set <r> --enabled false --note '<why>' --as <c>`.") +
			rule(4, "judgment: the seat decides.") +
			"R5. **A rule whose sentence\nwraps.**\n    Why: a failure\n    that wraps.\n    Done by: `nova-sprint\n    where`\n\n" + "## 11. After\n\nR9. **Not a rule here.**\n"
		assert.Empty(t, checkCoordRules(tools, ct, good), "rules that name real verbs, flags and settings are refused")
		got, _ := coordRules(good)
		require.Len(t, got, 5, "a rule outside the section is read, or one inside is not")
		assert.Equal(t, "A rule whose sentence wraps.", got[4].rule)
		assert.Equal(t, "a failure that wraps.", got[4].why)
		assert.Equal(t, "`nova-sprint where`", got[4].doneBy)

		for done, want := range map[string]string{
			"`nova-sprint fly`":                         `nova-sprint has no verb "fly"`,
			"`nova-sprint set --fly 1`":                 "nova-sprint set has no flag --fly",
			"`nova-config sprint set --fly 1 --as <c>`": "nova-config sprint set has no flag --fly",
			"`nova-config sprint add x --as <c>`":       `nova-config sprint has no verb "add"`,
			"`nova-config planet set x --as <c>`":       `nova-config has no verb or kind "planet"`,
			"`nova-config apply --fly`":                 "nova-config apply has no flag --fly",
			"`nova-teleport go`":                        "nova-teleport is a nova tool this test reads no verb table of",
			"`ns.sh where`":                             "names no nova verb line or setting",
			"":                                          "wants a Done by: line",
		} {
			text := head + "R1. **Rule.**\n    Why: a failure.\n"
			if done != "" {
				text += "    Done by: " + done + "\n"
			}
			ps := checkCoordRules(tools, ct, text)
			assert.Contains(t, strings.Join(ps, "\n"), want, "the rule is not refused, or refused for another reason: %q", done)
		}
		for text, want := range map[string]string{
			head + "R1. **Rule.**\n    Done by: judgment\n":                                            "wants a Why: line",
			head + rule(1, "judgment") + rule(3, "judgment"):                                           "no gap: want R2",
			head + rule(1, "judgment") + strings.Replace(rule(2, "judgment"), "Rule 2.", "Rule 1.", 1): "the same rule as R1",
			head + "R1. Rule without bold.\n":                                                          "a rule's first line is",
			head + "R1. **Rule never closed.\n    Why: a.\n    Done by: judgment\n":                    "not closed by **",
			head + "R1. **Rule.**\n    Why: a.\n    Why: b.\n    Done by: judgment\n":                  "a second Why:",
			"# x\n\nno section\n": "no rule under",
		} {
			ps := checkCoordRules(tools, ct, text)
			assert.Contains(t, strings.Join(ps, "\n"), want, "the runbook is not refused, or refused for another reason:\n%s", text)
		}
	})
}
