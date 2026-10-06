package docs

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkStopgaps is the register's retirement rule. A row that says retired
// passes. A row that names a script and names no nova verb and no card fails
// for that. Anything else fails unless it is a card. The tools argument is
// the same map the verb checker reads, or a fake of that map.
func checkStopgaps(tools map[string]novaTool, text string) []string {
	rows := coordinatorToolRows(text)
	if len(rows) == 0 {
		return []string{"no row under a | Tool | What it does | Replaced by | header"}
	}
	var problems []string
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.verbs), "retired") {
			continue
		}
		verb := stopgapNamesVerb(tools, r.verbs)
		card := strings.HasPrefix(strings.TrimSpace(r.verbs), "card:")
		where := "line " + strconv.Itoa(r.line) + " (" + r.tool + "): "
		switch {
		case stopgapNamesScript(r) && !verb && !card:
			problems = append(problems, where+"names a script with no replacing verb")
		case !verb && !card:
			problems = append(problems, where+"names no nova verb that replaces it")
		case !card:
			problems = append(problems, where+"is neither retired nor a card")
		}
	}
	return problems
}

// stopgapNamesScript is a row whose cells name a shell or Python script.
func stopgapNamesScript(r coordinatorToolRow) bool {
	text := strings.ToLower(r.tool + " " + r.what + " " + r.verbs)
	for _, s := range []string{".sh", ".py", "bash", "zsh", "python"} {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// stopgapNamesVerb is true when a code span is a nova command the tools map
// can check. A span that is not a nova command does not count.
func stopgapNamesVerb(tools map[string]novaTool, cell string) bool {
	for _, s := range codeSpanRe.FindAllStringSubmatch(cell, -1) {
		if checked, _ := checkCommand(tools, strings.Fields(s[1])); checked {
			return true
		}
	}
	return false
}

// fakeStopgapTools is an in-memory nova tool, so a witness does not need the
// binaries the page is checked against.
func fakeStopgapTools() map[string]novaTool {
	return map[string]novaTool{
		"nova-sprint": {
			verbs:    map[string]novaVerb{"dashboard": {flags: map[string]bool{"listen": true, "every": true}}},
			literals: map[string]bool{"listen": true, "every": true},
		},
	}
}

// TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt holds the register to
// its retirement rule: every row names a nova verb and is retired, or it
// names a card. The witnesses are an in-memory table and a fake tool map.
func TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt(t *testing.T) {
	t.Parallel()
	tools := fakeStopgapTools()
	head := "| Tool | What it does | Replaced by |\n|---|---|---|\n"

	t.Run("a script with no verb fails", func(t *testing.T) {
		t.Parallel()
		ps := checkStopgaps(tools, head+"| `server.py` | a python server | the page |\n")
		assert.Contains(t, strings.Join(ps, "\n"), "names a script with no replacing verb")
	})

	t.Run("a retired row passes", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, checkStopgaps(tools, head+"| `server.py` | a python server | retired |\n"))
	})

	t.Run("a card passes", func(t *testing.T) {
		t.Parallel()
		row := "| `a.sh` | a bash loop | card: x-1; needs `nova-sprint dashboard`; PATHS: a.go |\n"
		assert.Empty(t, checkStopgaps(tools, head+row))
	})

	t.Run("a verb that is not retired fails", func(t *testing.T) {
		t.Parallel()
		row := "| `unit` | a page | `nova-sprint dashboard --listen 127.0.0.1:1` |\n"
		ps := checkStopgaps(tools, head+row)
		assert.Contains(t, strings.Join(ps, "\n"), "is neither retired nor a card")
	})

	t.Run("no verb and no card fails", func(t *testing.T) {
		t.Parallel()
		ps := checkStopgaps(tools, head+"| `unit` | a loop | nothing here |\n")
		assert.Contains(t, strings.Join(ps, "\n"), "names no nova verb that replaces it")
	})

	t.Run("an in-memory twin of the register passes", func(t *testing.T) {
		t.Parallel()
		twin := head +
			"| `server.py` | a python server | retired `nova-sprint dashboard --listen 127.0.0.1:1 --every 1s` |\n" +
			"| `a.sh` | a bash loop | card: x-1; needs `nova-sprint dashboard --every 1s`; PATHS: a.go |\n"
		assert.Empty(t, checkStopgaps(tools, twin))
	})

	t.Run("the page", func(t *testing.T) {
		t.Parallel()
		raw, err := os.ReadFile(coordinatorToolsPath)
		require.NoError(t, err)
		page := readNovaTools(t)
		for _, p := range checkStopgaps(page, string(raw)) {
			t.Errorf("%s %s", coordinatorToolsPath, p)
		}
	})
}
