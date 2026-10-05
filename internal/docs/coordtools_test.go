package docs

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coordtools_test.go holds docs/COORDINATOR-TOOLS.md as the register of the
// coordinator's stopgaps: every script, wrapper and loop one coordinator runs that is
// not a nova verb has a row, and every row says how it goes away. Its Status cell is
// `retired` (the coordinator runs the nova verb in its place) or `card: <id>` (the card
// that builds the verb, or moves the coordinator onto it); its Replaced-by cell names
// the nova verb line that replaces it, or proposes the card with the verb it needs. A
// row that names a script and no replacing verb is a stopgap nothing removes, and it
// fails here. coordinator_tools_test.go checks the verbs and flags each line names
// against the tools' own source; this test checks that each row has one and an end.

// stopgapCardRe is a Status cell that names the card retiring its row.
var stopgapCardRe = regexp.MustCompile("^card: `?([a-z0-9][a-z0-9.-]*)`?$")

// stopgapRetired is the Status of a row whose verb the coordinator runs.
const stopgapRetired = "retired"

// namesAVerb is whether a cell names at least one nova verb line that the tools carry,
// with every flag it uses.
func namesAVerb(tools map[string]novaTool, cell string) bool {
	for _, s := range codeSpanRe.FindAllStringSubmatch(cell, -1) {
		if checked, ps := checkCommand(tools, strings.Fields(s[1])); checked && len(ps) == 0 {
			return true
		}
	}
	return false
}

// checkStopgapRegister is every problem of the register's rows: a row with no Status, a
// Status that is neither retired nor a card, a row with no replacing verb (a real one,
// or the one a proposed card needs), a retired row whose replacement is still a card,
// and a card row whose Status names another card than the one its replacement proposes.
func checkStopgapRegister(tools map[string]novaTool, text string) []string {
	var problems []string
	say := func(r coordinatorToolRow, s string) {
		problems = append(problems, "line "+strconv.Itoa(r.line)+" ("+r.tool+"): "+s)
	}
	rows := coordinatorToolRows(text)
	if len(rows) == 0 {
		return []string{"no row under a | Tool | What it does | Replaced by | Status | header"}
	}
	for _, r := range rows {
		proposed := cardCellRe.FindStringSubmatch(r.verbs)
		switch {
		case proposed != nil:
			// the verb a card needs is its row's replacement until the card lands
		case strings.HasPrefix(r.verbs, "card:"):
			say(r, "a card cell is `card: <id>; needs `<verb line>`; PATHS: <path>,...`")
		case !namesAVerb(tools, r.verbs):
			say(r, "names no nova verb that replaces it: a stopgap stays until a verb does its work; name the verb line, or propose its card")
		}
		status := r.status
		card := stopgapCardRe.FindStringSubmatch(status)
		switch {
		case status == "":
			say(r, "no Status: a row is `retired` or names the card that retires it, `card: <id>`")
		case status == stopgapRetired:
			if proposed != nil {
				say(r, "retired, and its replacement is still the card "+proposed[1]+": a row is retired when the coordinator runs a verb that exists")
			}
		case card == nil:
			say(r, "Status "+strconv.Quote(status)+" is neither `retired` nor `card: <id>`")
		case proposed != nil && card[1] != proposed[1]:
			say(r, "Status names the card "+card[1]+" and the replacement proposes "+proposed[1]+": the card that builds the verb is the one that retires the row")
		}
	}
	return problems
}

// TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt is the register's contract:
// every row of docs/COORDINATOR-TOOLS.md names the nova verb that replaces its tool and
// is either retired or names the card that retires it. The witnesses run the same check
// on rows that must be refused and on rows that must pass.
func TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt(t *testing.T) {
	t.Parallel()
	tools := readNovaTools(t)

	t.Run("the register", func(t *testing.T) {
		raw, err := os.ReadFile(coordinatorToolsPath)
		require.NoError(t, err, "%s: %v", coordinatorToolsPath, err)
		rows := coordinatorToolRows(string(raw))
		require.GreaterOrEqual(t, len(rows), 10, "%s registers fewer stopgaps than the coordinator runs", coordinatorToolsPath)
		for _, p := range checkStopgapRegister(tools, string(raw)) {
			t.Errorf("%s %s", coordinatorToolsPath, p)
		}
	})

	t.Run("the witnesses", func(t *testing.T) {
		head := "| Tool | What it does | Replaced by | Status |\n|---|---|---|---|\n"
		pass := map[string]string{
			"a retired row":          "| `a.sh` | x | `nova-sprint where --watch --every 1s` | retired |",
			"a row its card retires": "| `a.sh` | x | `nova-sprint where` | card: adopt-a |",
			"a row a card builds":    "| `a.sh` | x | card: a-every; needs `nova-sprint where --every-other <d>`; PATHS: a.go | card: a-every |",
		}
		for why, row := range pass {
			assert.Empty(t, checkStopgapRegister(tools, head+row+"\n"), "%s is refused", why)
		}
		refused := map[string]string{
			"| `a.sh` | x | a.sh does it | card: a-1 |":                                    "names no nova verb that replaces it",
			"| `a.sh` | x | `a.sh --loop` | retired |":                                     "names no nova verb that replaces it",
			"| `a.sh` | x | `nova-sprint fly` | retired |":                                 "names no nova verb that replaces it",
			"| `a.sh` | x | card: a-1; needs `nova-sprint fly` | card: a-1 |":              "a card cell is",
			"| `a.sh` | x | `nova-sprint where` |":                                         "no Status",
			"| `a.sh` | x | `nova-sprint where` | soon |":                                  `Status "soon" is neither`,
			"| `a.sh` | x | card: a-1; needs `nova-sprint fly`; PATHS: a.go | retired |":   "retired, and its replacement is still the card a-1",
			"| `a.sh` | x | card: a-1; needs `nova-sprint fly`; PATHS: a.go | card: b-2 |": "names the card b-2 and the replacement proposes a-1",
			"| `a.sh` | x | `nova-sprint where` | card: |":                                 "is neither",
		}
		for row, want := range refused {
			ps := checkStopgapRegister(tools, head+row+"\n")
			assert.NotEmpty(t, ps, "the row is not refused: %s", row)
			assert.Contains(t, strings.Join(ps, "\n"), want, "the row is refused for another reason: %s", row)
		}
		assert.NotEmpty(t, checkStopgapRegister(tools, "no table"))
	})
}
