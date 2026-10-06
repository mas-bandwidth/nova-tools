package docs

import (
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coordtools_register_test.go holds docs/COORDINATOR-TOOLS.md as the register
// of the coordinator's stopgaps: every row names the nova verb that replaces
// its script, and says where the row stands, retired (the verb is adopted and
// the script no longer runs) or carried by a card (the card that builds the
// verb, or the one that moves the coordinator onto it). A row naming a script
// and no verb is a stopgap nothing guarantees goes away. Whether the verb's
// words and flags are real is TestEveryCoordinatorToolMapsToARealNovaVerb's
// (coordinator_tools_test.go); this reads the rows that test reads.

// retiredRe is a row's retirement: `retired <date>`, the day its verb was adopted.
var retiredRe = regexp.MustCompile(`(?:^|[;.] )retired (\d{4}-\d{2}-\d{2})\b`)

// retiresWithRe is a row whose verb exists and whose adoption a card carries.
var retiresWithRe = regexp.MustCompile("(?:^|[;.] )retires with card: `?([a-z0-9][a-z0-9.-]*)`?")

// replacingVerb is the nova verb a code span runs, "" when it runs none:
// leading environment words are skipped, and a nova-secrets exec wrapper is
// read through to the command after its `--`, which is the replacement.
func replacingVerb(words []string) string {
	for len(words) > 0 && (words[0] == "env" || words[0] == "/usr/bin/env" || (strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-"))) {
		words = words[1:]
	}
	if len(words) < 2 {
		return ""
	}
	tool := path.Base(words[0])
	if !strings.HasPrefix(tool, "nova-") || strings.ContainsAny(words[1][:1], "-<[(|") {
		return ""
	}
	if tool == "nova-secrets" {
		for i, w := range words {
			if w == "--" {
				return replacingVerb(words[i+1:])
			}
		}
		return ""
	}
	return tool + " " + words[1]
}

// checkStopgapRegister is every problem of the register's rows.
func checkStopgapRegister(text string) []string {
	var problems []string
	rows := coordinatorToolRows(text)
	if len(rows) == 0 {
		return []string{"no row under a | Tool | What it does | Replaced by | header"}
	}
	for _, r := range rows {
		say := func(s string) { problems = append(problems, "line "+strconv.Itoa(r.line)+" ("+r.tool+"): "+s) }
		if r.verbs == "" {
			say("a row wants its replacing verb and where it stands")
			continue
		}
		var verbs []string
		building := cardCellRe.FindStringSubmatch(r.verbs)
		if building != nil {
			// a card that builds the verb: the verb it needs is the replacement
			if v := replacingVerb(strings.Fields(building[2])); v != "" {
				verbs = append(verbs, v)
			}
		} else {
			for _, s := range codeSpanRe.FindAllStringSubmatch(r.verbs, -1) {
				if v := replacingVerb(strings.Fields(s[1])); v != "" {
					verbs = append(verbs, v)
				}
			}
		}
		if len(verbs) == 0 {
			say("the row names a script with no nova verb that replaces it: name the verb line, or a card that needs `nova-<tool> <verb> ...`")
		}
		retired, adopting := retiredRe.MatchString(r.verbs), retiresWithRe.MatchString(r.verbs)
		switch {
		case building != nil && (retired || adopting):
			say("a row a card is still building is not retired and not being adopted yet")
		case retired && adopting:
			say("a row is retired or retires with a card, never both")
		case building == nil && !retired && !adopting:
			say("the row is neither retired (`retired <date>`) nor carried by a card (`card: <id>; needs ...`, or `retires with card: <id>`)")
		}
	}
	return problems
}

// TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt is the contract: every
// row of the register names a nova verb that replaces its script, and is
// retired or carried by a card. The witnesses are rows the check must refuse
// and rows it must pass, so a check that passes everything fails here.
func TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt(t *testing.T) {
	t.Parallel()

	t.Run("the register", func(t *testing.T) {
		raw, err := os.ReadFile(coordinatorToolsPath)
		require.NoError(t, err, "%s: %v", coordinatorToolsPath, err)
		require.NotEmpty(t, coordinatorToolRows(string(raw)), "%s holds no register row", coordinatorToolsPath)
		for _, p := range checkStopgapRegister(string(raw)) {
			t.Errorf("%s %s", coordinatorToolsPath, p)
		}
	})

	t.Run("the witnesses", func(t *testing.T) {
		head := "| Tool | What it does | Replaced by |\n|---|---|---|\n"
		for _, line := range []string{
			"| `<seat-bin>/x.sh` | a loop | `NOVA_SPRINT_SERVER=127.0.0.1:6390 nova-sprint where --watch --every 1s`; retired 2026-10-06 |",
			"| `<seat-bin>/x.sh` | a loop | `nova-sprint dashboard --listen 127.0.0.1:7390`; retires with card: coordinator-adopts-the-verbs |",
			"| `<seat-bin>/x.sh` | a loop | `nova-secrets exec --store <s> -- nova-sprint friend sync --root <d>`. retired 2026-10-06 |",
			"| `<seat-bin>/x.sh` | a loop | card: x-verb; needs `nova-swarm mirror --dir <d>`; PATHS: cmd/nova-swarm/mirror.go |",
		} {
			assert.Empty(t, checkStopgapRegister(head+line+"\n"), "the row is refused: %s", line)
		}
		for line, want := range map[string]string{
			"| `<seat-bin>/x.sh` | a loop | `x.sh --loop 60`; retired 2026-10-06 |":                                         "a script with no nova verb",
			"| `<seat-bin>/x.sh` | a loop | `nova-secrets exec --store <s> -- x.sh`; retires with card: y |":                   "a script with no nova verb",
			"| `<seat-bin>/x.sh` | a loop | the coordinator's own script |":                                                  "a script with no nova verb",
			"| `<seat-bin>/x.sh` | a loop | card: y; needs `x.sh --every 1m`; PATHS: a.go |":                                "a script with no nova verb",
			"| `<seat-bin>/x.sh` | a loop | `nova-sprint where --watch` |":                                                   "neither retired",
			"| `<seat-bin>/x.sh` | a loop | `nova-sprint where`; retired 2026-10-06; retires with card: y |":                 "never both",
			"| `<seat-bin>/x.sh` | a loop | card: y; needs `nova-swarm mirror`; PATHS: a.go; retired 2026-10-06 |":           "still building",
			"| `<seat-bin>/x.sh` | a loop |":                                                                                  "wants its replacing verb",
			"| `<seat-bin>/x.sh` | a loop | `nova-sprint where`; retired soon |":                                             "neither retired",
		} {
			ps := checkStopgapRegister(head + line + "\n")
			assert.NotEmpty(t, ps, "the row is not refused: %s", line)
			assert.Contains(t, strings.Join(ps, "\n"), want, "the row is refused for another reason: %s", line)
		}
		assert.NotEmpty(t, checkStopgapRegister("no table"))
	})
}
