package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt holds the register to
// one rule: a row is retired, and then it names a nova verb this tree has, or
// it names a card. A script with neither fails. mirror-refresh stays a card:
// nova-swarm mirror is not dispatched from a file this card may edit.
func TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt(t *testing.T) {
	t.Parallel()
	tools := readNovaTools(t)
	raw, err := os.ReadFile(coordinatorToolsPath)
	require.NoError(t, err)
	text := string(raw)
	for _, p := range stopgapProblems(tools, text) {
		t.Errorf("%s %s", coordinatorToolsPath, p)
	}
	for _, row := range coordinatorToolRows(text) {
		switch {
		case strings.Contains(row.tool, "server.py"):
			assert.True(t, strings.HasPrefix(row.verbs, "retired:"), "server.py")
			assert.Contains(t, row.verbs, "nova-sprint dashboard")
		case strings.Contains(row.tool, "sprint-table-live"):
			assert.True(t, strings.HasPrefix(row.verbs, "retired:"), "sprint-table-live")
			assert.Contains(t, row.verbs, "nova-sprint where")
		case strings.Contains(row.tool, "/disk-guard"):
			assert.True(t, strings.HasPrefix(row.verbs, "retired:"), "disk-guard")
			assert.Contains(t, row.verbs, "nova-swarm disk-guard")
		case strings.Contains(row.tool, "nova-loop"):
			assert.True(t, strings.HasPrefix(row.verbs, "retired:"), "nova-loop")
			assert.Contains(t, row.verbs, "nova-loop run")
		case strings.Contains(row.tool, "mirror-refresh"):
			assert.True(t, strings.HasPrefix(row.verbs, "card:"), "mirror-refresh")
			assert.Contains(t, row.verbs, "mirror-refresh-verb")
		}
	}

	head := "| Tool | What it does | Replaced by |\n|---|---|---|\n"
	assert.Empty(t, stopgapProblems(tools, head+"| `c.sh` | x | retired: `nova-sprint where` |\n"), "a retired row with a real verb was refused")
	script := stopgapProblems(tools, head+"| `b.sh` | x | the shell prints a line |\n")
	assert.NotEmpty(t, script, "a script with no verb was accepted")
	assert.Contains(t, strings.Join(script, "\n"), "names no replacing verb")
	retired := stopgapProblems(tools, head+"| `a.sh` | x | retired: still the shell |\n")
	assert.NotEmpty(t, retired, "a retired row with no verb was accepted")
	assert.Contains(t, strings.Join(retired, "\n"), "a retired row names no nova verb")
	assert.Empty(t, stopgapProblems(tools, head+"| `d.sh` | x | card: some-id; needs `nova-sprint fly`; PATHS: cmd/x.go |\n"), "a card was refused")
}

func stopgapProblems(tools map[string]novaTool, text string) []string {
	var problems []string
	rows := coordinatorToolRows(text)
	if len(rows) == 0 {
		return []string{"no row under a | Tool | What it does | Replaced by | header"}
	}
	for _, r := range rows {
		retired := strings.HasPrefix(r.verbs, "retired:")
		card := strings.HasPrefix(r.verbs, "card:")
		if !retired && !card {
			problems = append(problems, "line "+itoa(r.line)+" ("+r.tool+"): a row is retired or names a card")
		}
		verb := rowHasCheckedVerb(tools, r)
		if retired && !verb {
			problems = append(problems, "line "+itoa(r.line)+" ("+r.tool+"): a retired row names no nova verb")
		}
		if isStopgapScript(r.tool) && !verb && !card {
			problems = append(problems, "line "+itoa(r.line)+" ("+r.tool+"): a script row names no replacing verb")
		}
	}
	return problems
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func isStopgapScript(tool string) bool {
	switch {
	case strings.Contains(tool, ".sh"),
		strings.Contains(tool, ".py"),
		strings.Contains(tool, "/bin/"),
		strings.Contains(tool, "disk-guard"),
		strings.Contains(tool, "mirror-refresh"),
		strings.Contains(tool, "nova-loop"),
		strings.Contains(tool, "com.nova.loop."):
		return true
	default:
		return false
	}
}

// rowHasCheckedVerb reports whether a cell has a code span the verb checker
// accepts. A card's needs span is the verb that does not exist yet, so it is
// skipped, the same way TestEveryCoordinatorToolMapsToARealNovaVerb skips it.
func rowHasCheckedVerb(tools map[string]novaTool, r coordinatorToolRow) bool {
	spans := codeSpanRe.FindAllStringSubmatch(r.verbs, -1)
	skip := ""
	if m := cardCellRe.FindStringSubmatch(r.verbs); m != nil {
		skip = m[2]
	}
	skipped := false
	for _, s := range spans {
		if skip != "" && !skipped && s[1] == skip {
			skipped = true
			continue
		}
		checked, problems := checkCommand(tools, strings.Fields(s[1]))
		if checked && len(problems) == 0 {
			return true
		}
	}
	return false
}
