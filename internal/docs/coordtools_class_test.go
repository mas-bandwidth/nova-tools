package docs

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coordtools_class_test.go is the class rule over docs/COORDINATOR-TOOLS.md, the register of
// stopgaps: a row whose tool is a script needs a verb that replaces it (a verb line the
// tool's own table carries, or a card that writes it), a retired row keeps its verb line,
// and the rows that had no card to wait for are written here, so they may not wait on one.
// coordinator_tools_test.go checks each verb line against the tools' verb tables; this file
// checks what each row stands for.

// verbOwedNoCard names the rows whose verb is written, not waited for: the dashboard server,
// disk-guard, mirror-refresh, the bash nova-loop wrapper and the live table. A card cell on
// one of them fails.
var verbOwedNoCard = []string{"server.py", "/disk-guard`", "mirror-refresh`", "/nova-loop`", "sprint-table-live"}

// isScript reads a tool cell: a script file, a wrapper in a bin directory, or a loop unit.
func isScript(tool string) bool {
	for _, m := range []string{".sh", ".py", "/bin/", "com.nova.loop.", "~/.local/bin/"} {
		if strings.Contains(tool, m) {
			return true
		}
	}
	return false
}

// checkStopgaps is every problem of the rows the class rule judges.
func checkStopgaps(tools map[string]novaTool, text string) []string {
	var problems []string
	say := func(r coordinatorToolRow, s string) {
		problems = append(problems, "line "+strconv.Itoa(r.line)+" ("+r.tool+"): "+s)
	}
	for _, r := range coordinatorToolRows(text) {
		if r.what == "" || r.verbs == "" {
			continue // coordinator_tools_test.go refuses a row of fewer than three cells
		}
		isCard := strings.HasPrefix(r.verbs, "card:")
		retired := strings.HasPrefix(r.verbs, "retired:")
		hasVerb := false
		for _, s := range codeSpanRe.FindAllStringSubmatch(r.verbs, -1) {
			if ok, _ := checkCommand(tools, strings.Fields(s[1])); ok && !isCard {
				hasVerb = true
			}
		}
		if isCard && !cardCellRe.MatchString(r.verbs) {
			say(r, "a card row names the card, the verb it needs and its PATHS")
		}
		if isScript(r.tool) && !isCard && !hasVerb {
			say(r, "a script with no verb that replaces it and no card")
		}
		if retired && !hasVerb {
			say(r, "a retired row keeps the verb line that replaced the tool")
		}
		for _, m := range verbOwedNoCard {
			if strings.Contains(r.tool, m) && isCard {
				say(r, "this tool has no card to wait for: its verb is written, name the verb line")
			}
		}
	}
	return problems
}

// TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt holds the register: the page's rows
// pass, and the witnesses run the same check on rows that must be refused.
func TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt(t *testing.T) {
	t.Parallel()
	tools := readNovaTools(t)

	t.Run("the register", func(t *testing.T) {
		raw, err := os.ReadFile(coordinatorToolsPath)
		require.NoError(t, err)
		for _, p := range checkStopgaps(tools, string(raw)) {
			t.Errorf("%s %s", coordinatorToolsPath, p)
		}
		for _, want := range []string{"nova-worker mirror", "--stop-floor", "nova-sprint dashboard", "nova-config loop run"} {
			assert.Contains(t, string(raw), want, "the register no longer names %s", want)
		}
	})

	t.Run("the witnesses", func(t *testing.T) {
		head := "| Tool | What it does | Replaced by |\n|---|---|---|\n"
		for line, want := range map[string]string{
			"| `~/bin/thing.sh` | x | see the wiki |":    "a script with no verb",
			"| `<seat-bin>/ns.sh` | x | `ns.sh where` |": "a script with no verb",
			"| `com.nova.loop.a` | x | retired: gone |":  "a script with no verb",
			"| `x` | x | retired: gone |":                "a retired row keeps the verb line",
			"| `~/nova-bench/dashboard/server.py` | x | card: dash; needs `nova-sprint dashboard serve`; PATHS: a.go |": "its verb is written",
			"| `~/.local/bin/mirror-refresh` | x | card: m; needs `nova-worker mirror`; PATHS: a.go |":                   "its verb is written",
			"| `~/.local/bin/nova-loop` | x | card: l; needs `nova-config loop run <name>`; PATHS: a.go |":              "its verb is written",
			"| `a.sh` | x | card: only a name |": "names the card, the verb it needs",
		} {
			ps := checkStopgaps(tools, head+line+"\n")
			assert.Contains(t, strings.Join(ps, "\n"), want, "the row is not refused for this: %s", line)
		}
		for _, line := range []string{
			"| `a.sh` | x | `nova-sprint where --watch` |",
			"| `~/.local/bin/nova-loop` | x | `nova-config loop run <name> --run-dir <d> -- <cmd>` |",
			"| `a.sh` | x | retired: `nova-sprint where --watch` |",
			"| `a.sh` | x | card: c-1; needs `nova-sprint fly`; PATHS: a.go |",
			"| `x` | x | prose with no script |",
		} {
			assert.Empty(t, checkStopgaps(tools, head+line+"\n"), "the row is refused: %s", line)
		}
	})
}
