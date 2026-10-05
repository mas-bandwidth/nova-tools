package docs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coordrules_test.go holds the coordinator's numbered rules
// (docs/SPRINT-COORDINATOR.md, "10. The rules") to the tools they name. A rule
// says what does it: verb lines in code spans, a setting being a verb's flag
// (`nova-sprint set --friend-idle <duration>`), or the word judgment when the
// seat decides with no verb. A stranger who adopts the sprint runs those lines,
// so a verb or a flag the tool does not carry is a rule that cannot be followed.
// The verbs are read from each tool's source as coordinator_tools_test.go reads
// them, plus nova-config's usage and nova-update's verbs block.

// coordRulesPath is the runbook, relative to this package.
const coordRulesPath = "../../docs/SPRINT-COORDINATOR.md"

// coordRulesHeader is the header of the rules table.
const coordRulesHeader = "| # | Rule | Why | Does it | Card | Pass |"

// coordRulesMin is the fewest rules the table may hold: the rules distilled
// from the coordinator's practice on 2026-10-05 number more than this.
const coordRulesMin = 90

// coordRuleRow is one row of the rules table.
type coordRuleRow struct {
	line                           int
	n, rule, why, does, card, pass string
}

// coordRuleRows reads the rows under the rules table's header; ok false when
// the header is not there.
func coordRuleRows(text string) (rows []coordRuleRow, ok bool) {
	in := false
	for i, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == coordRulesHeader {
			in, ok = true, true
			continue
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			in = false
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		if strings.HasPrefix(cells[0], "---") {
			continue
		}
		r := coordRuleRow{line: i + 1}
		if len(cells) == 6 {
			r.n, r.rule, r.why, r.does, r.card, r.pass = cells[0], cells[1], cells[2], cells[3], cells[4], cells[5]
		} else {
			r.n = "?" + strconv.Itoa(len(cells))
		}
		rows = append(rows, r)
	}
	return rows, ok
}

// cardIDRe is the shape of a card's id.
var cardIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

// checkCoordRules is every problem of the rules table.
func checkCoordRules(tools map[string]novaTool, text string) []string {
	rows, ok := coordRuleRows(text)
	if !ok {
		return []string{"no rules table: the header " + coordRulesHeader + " is not there"}
	}
	var problems []string
	say := func(r coordRuleRow, s string) {
		problems = append(problems, "line "+strconv.Itoa(r.line)+" (rule "+r.n+"): "+s)
	}
	for i, r := range rows {
		if strings.HasPrefix(r.n, "?") {
			say(r, "a rule wants six cells: number, rule, why, does it, card, pass")
			continue
		}
		if r.n != strconv.Itoa(i+1) {
			say(r, "rules are numbered 1, 2, 3 in order; this is row "+strconv.Itoa(i+1))
		}
		if r.rule == "" || r.why == "" {
			say(r, "a rule says the rule and why")
		}
		if r.card != "none" && !cardIDRe.MatchString(strings.Trim(r.card, "`")) {
			say(r, "the card cell is a card id or none")
		}
		if r.pass != "yes" && r.pass != "no" {
			say(r, "the pass cell is yes (handover prints it) or no")
		}
		spans := codeSpanRe.FindAllStringSubmatch(r.does, -1)
		if len(spans) == 0 {
			if r.does != "judgment" {
				say(r, "the does-it cell names a verb line in a code span, or says judgment")
			}
			continue
		}
		checked := false
		for _, s := range spans {
			ok, ps := checkCommand(tools, strings.Fields(s[1]))
			checked = checked || ok
			for _, p := range ps {
				say(r, p)
			}
		}
		if !checked {
			say(r, "the does-it cell names no nova verb line")
		}
	}
	return problems
}

// usageBlockTool reads a usage block held in a constant of a package
// directory: one synopsis line per verb, `  <tool> <verb words> <flags>`, the
// verb the words before the first flag, placeholder or run of two spaces, and
// a word of alternatives (`set|show|history`) one verb per alternative.
func usageBlockTool(t *testing.T, tool, dir, constName string) novaTool {
	t.Helper()
	files := parseDir(t, dir)
	nt := novaTool{verbs: map[string]novaVerb{}, common: map[string]bool{}, literals: sourceLiterals(files)}
	var usage string
	for _, f := range files {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				val := spec.(*ast.ValueSpec)
				for i, name := range val.Names {
					if name.Name == constName && i < len(val.Values) {
						usage, _ = stringLit(val.Values[i])
					}
				}
			}
		}
	}
	require.NotEmpty(t, usage, "%s declares no constant %s; %s's verb table has moved", dir, constName, tool)
	for _, line := range strings.Split(usage, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), tool+" ")
		if !ok {
			continue
		}
		rest, _, _ = strings.Cut(rest, "  ")
		fields := strings.Fields(rest)
		var words []string
		for _, w := range fields {
			if strings.ContainsAny(w[:1], "-<[(|") {
				break
			}
			words = append(words, w)
		}
		if len(words) == 0 {
			continue
		}
		flags := synopsisFlags(strings.Join(fields[len(words):], " "))
		head := strings.Join(words[:len(words)-1], " ")
		for _, alt := range strings.Split(words[len(words)-1], "|") {
			name := strings.TrimSpace(head + " " + alt)
			v, ok := nt.verbs[name]
			if !ok {
				v = novaVerb{flags: map[string]bool{}}
				nt.verbs[name] = v
			}
			for f := range flags {
				v.flags[f] = true
			}
		}
	}
	require.NotEmpty(t, nt.verbs, "%s: no synopsis line of %s was read", dir, tool)
	return nt
}

// parseDir parses the non-test Go files of a directory relative to the repository root.
func parseDir(t *testing.T, dir string) []*ast.File {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("../..", dir, "*.go"))
	require.NoError(t, err)
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err, "%s: %v", name, err)
		files = append(files, f)
	}
	require.NotEmpty(t, files, "%s holds no Go source", dir)
	return files
}

// coordRuleTools is every tool a rule may name.
func coordRuleTools(t *testing.T) map[string]novaTool {
	t.Helper()
	tools := readNovaTools(t)
	tools["nova-config"] = usageBlockTool(t, "nova-config", "cmd/nova-config", "usageTop")
	tools["nova-update"] = usageBlockTool(t, "nova-update", "internal/update", "updateVerbs")
	require.Contains(t, tools["nova-config"].verbs, "sprint set", "nova-config's usage was not read")
	require.Contains(t, tools["nova-update"].verbs, "release", "nova-update's verbs block was not read")
	return tools
}

// TestEveryCoordinatorRuleNamesVerbsThatExist is the contract: every numbered
// rule of the runbook names what does it, as verb lines whose verbs are in the
// tool's verb table and whose flags are in that verb's synopsis and FlagSet, or
// as judgment. The witnesses run the same check on rules that must be refused,
// so a check that passes everything fails here.
func TestEveryCoordinatorRuleNamesVerbsThatExist(t *testing.T) {
	t.Parallel()
	tools := coordRuleTools(t)

	t.Run("the runbook", func(t *testing.T) {
		raw, err := os.ReadFile(coordRulesPath)
		require.NoError(t, err, "%s: %v", coordRulesPath, err)
		rows, _ := coordRuleRows(string(raw))
		require.GreaterOrEqual(t, len(rows), coordRulesMin, "%s holds fewer rules than the coordinator follows", coordRulesPath)
		for _, p := range checkCoordRules(tools, string(raw)) {
			t.Errorf("%s %s", coordRulesPath, p)
		}
	})

	t.Run("the witnesses", func(t *testing.T) {
		head := coordRulesHeader + "\n|---|---|---|---|---|---|\n"
		good := head +
			"| 1 | r | w | `nova-sprint set --friend-idle <duration>` | none | yes |\n" +
			"| 2 | r | w | `nova-sprint friend level`, `nova-friend ping --as <c> --to <f>` | friend-deal-idle-lanes-first | no |\n" +
			"| 3 | r | w | judgment | none | no |\n" +
			"| 4 | r | w | `nova-config sprint set`, `nova-update release adopt`, `nova-update adoption --file <path>` | none | no |\n"
		assert.Empty(t, checkCoordRules(tools, good), "rules that name real verbs and flags are refused")

		for line, want := range map[string]string{
			"| 1 | r | w | `nova-sprint fly` | none | yes |":            `nova-sprint has no verb "fly"`,
			"| 1 | r | w | `nova-sprint set --fly <d>` | none | yes |":  "nova-sprint set has no flag --fly",
			"| 1 | r | w | `nova-config sprint fly` | none | yes |":     `nova-config has no verb "sprint"`,
			"| 1 | r | w | `nova-update adoption --fly` | none | yes |": "nova-update adoption has no flag --fly",
			"| 1 | r | w | `nova-teleport go` | none | yes |":           "nova-teleport is a nova tool this test reads no verb table of",
			"| 1 | r | w | `ns.sh where` | none | yes |":                "names no nova verb line",
			"| 1 | r | w | the coordinator does it | none | yes |":      "or says judgment",
			"| 1 | r | w | judgment | Not A Card | yes |":               "the card cell is a card id or none",
			"| 1 | r | w | judgment | none | maybe |":                   "the pass cell is yes",
			"| 2 | r | w | judgment | none | no |":                      "numbered 1, 2, 3 in order",
			"| 1 |  | w | judgment | none | no |":                       "says the rule and why",
			"| 1 | r | w | judgment |":                                  "wants six cells",
		} {
			ps := checkCoordRules(tools, head+line+"\n")
			assert.NotEmpty(t, ps, "the rule is not refused: %s", line)
			assert.Contains(t, strings.Join(ps, "\n"), want, "the rule is refused for another reason: %s", line)
		}
		assert.Contains(t, strings.Join(checkCoordRules(tools, "no table"), "\n"), "no rules table")
	})
}
