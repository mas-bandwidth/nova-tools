package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: EVERY HAND SCRIPT THE FLEET RUNS IS IN THE STOPGAP REGISTER.
//
// The release rule of 2026-10-04 (nova-sprint v1.0.0, lens simplicity): no bash or zsh in anything that
// ships; every loop or helper is a Go verb. The coordinator's wake, the buds' card and read
// runners, the opencode runners, the launchd ping and beat loops and the dashboard server
// were each written as a stopgap. docs/STOPGAPS.md lists them, one section each: path,
// behaviours, the verb or card that retires it, and `STATUS: live|retired <date>`.
//
// A live row needs no citation. A retired row is only true when its replacement verb is in
// its tool's verb table (docs/CLI.md) and every behaviour cites `(test: TestX)` for a test
// that exists in the tree: a script is not retired until something else holds each thing it
// did. This card retires no row, so the file's retired rows are none today; the rule is
// held on fakes below, and the red cases of the register's own parser run on every build.

const stopgapsDoc = "docs/STOPGAPS.md"

// requiredStopgaps are the hand scripts the register must list, by section name.
var requiredStopgaps = []string{
	"coordinator-wake",
	"bud-card-runner",
	"bud-reader-runner",
	"flash-friend-runner",
	"security-friend-runner",
	"friend-ping",
	"friend-beat-loops",
}

var (
	stopgapStatusRe  = regexp.MustCompile(`^STATUS: (live|retired (\d{4}-\d{2}-\d{2}))$`)
	stopgapBehaveRe  = regexp.MustCompile(`^(\d+)\. (.+)$`)
	stopgapCitedRe   = regexp.MustCompile(`\(test: (Test\w+)\)\.?$`)
	stopgapTestDecl  = regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	stopgapVerbRowRe = regexp.MustCompile("^\\| `([a-z][a-z0-9-]*)[ `]")
)

type stopgap struct {
	Name        string
	Path        string
	Replacement []string
	Tool        string
	Status      string
	Retired     bool
	Behaviours  []string
}

// parseStopgaps reads the register into its rows and returns every format fault found.
func parseStopgaps(doc string) ([]stopgap, []string) {
	var rows []stopgap
	var faults []string
	var cur *stopgap
	inBehaviours := false
	flush := func() {
		if cur == nil {
			return
		}
		switch {
		case cur.Path == "":
			faults = append(faults, cur.Name+": no Path: line")
		case len(cur.Replacement) == 0:
			faults = append(faults, cur.Name+": no Replacement: line")
		case cur.Status == "":
			faults = append(faults, cur.Name+": no STATUS: line")
		case len(cur.Behaviours) == 0:
			faults = append(faults, cur.Name+": no numbered behaviours")
		}
		if cur.Retired && cur.Tool == "" {
			faults = append(faults, cur.Name+": a retired row needs a Tool: line")
		}
		if cur.Retired && len(cur.Replacement) == 1 && cur.Replacement[0] == "none yet" {
			faults = append(faults, cur.Name+": a retired row cannot say Replacement: none yet")
		}
		rows = append(rows, *cur)
	}
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimRight(line, " \t")
		if name, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			cur, inBehaviours = &stopgap{Name: strings.TrimSpace(name)}, false
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "Path: "):
			cur.Path = strings.TrimPrefix(line, "Path: ")
		case strings.HasPrefix(line, "Replacement: "):
			for _, v := range strings.Split(strings.TrimPrefix(line, "Replacement: "), ",") {
				cur.Replacement = append(cur.Replacement, strings.TrimSpace(v))
			}
		case strings.HasPrefix(line, "Tool: "):
			cur.Tool = strings.TrimPrefix(line, "Tool: ")
		case strings.HasPrefix(line, "STATUS:"):
			m := stopgapStatusRe.FindStringSubmatch(line)
			if m == nil {
				faults = append(faults, fmt.Sprintf("%s: STATUS line %q is not `STATUS: live|retired <YYYY-MM-DD>`", cur.Name, line))
				break
			}
			cur.Status, cur.Retired = m[1], m[2] != ""
		case line == "Behaviours:":
			inBehaviours = true
		case inBehaviours && stopgapBehaveRe.MatchString(line):
			m := stopgapBehaveRe.FindStringSubmatch(line)
			if want := fmt.Sprint(len(cur.Behaviours) + 1); m[1] != want {
				faults = append(faults, fmt.Sprintf("%s: behaviour numbered %s, want %s", cur.Name, m[1], want))
			}
			cur.Behaviours = append(cur.Behaviours, m[2])
		}
	}
	flush()
	return rows, faults
}

// retiredFaults is the rule for the retired rows: the replacement verbs are in the tool's
// verb table (hasVerb) and every behaviour cites a test that exists (hasTest).
func retiredFaults(rows []stopgap, hasVerb func(tool, verb string) bool, hasTest func(name string) bool) []string {
	var faults []string
	for _, r := range rows {
		if !r.Retired {
			continue
		}
		for _, v := range r.Replacement {
			if !hasVerb(r.Tool, v) {
				faults = append(faults, fmt.Sprintf("%s: replacement verb %q is in no verb table of %s", r.Name, v, r.Tool))
			}
		}
		for i, b := range r.Behaviours {
			m := stopgapCitedRe.FindStringSubmatch(b)
			switch {
			case m == nil:
				faults = append(faults, fmt.Sprintf("%s: behaviour %d cites no `(test: TestX)`", r.Name, i+1))
			case !hasTest(m[1]):
				faults = append(faults, fmt.Sprintf("%s: behaviour %d cites %s, which exists in no test file", r.Name, i+1, m[1]))
			}
		}
	}
	return faults
}

// verbTableHas reports whether docs/CLI.md's section `## <tool>` has a table row
// that starts with the verb in backticks.
func verbTableHas(cli, tool, verb string) bool {
	in := false
	for _, line := range strings.Split(cli, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			in = strings.TrimSpace(h) == tool
			continue
		}
		if m := stopgapVerbRowRe.FindStringSubmatch(line); in && m != nil && m[1] == verb {
			return true
		}
	}
	return false
}

// treeTestNames returns every Test function declared in a _test.go file of the tree.
func treeTestNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range stopgapTestDecl.FindAllStringSubmatch(string(raw), -1) {
			names[m[1]] = true
		}
		return nil
	}))
	return names
}

// TestEveryStopgapNamesItsBehavioursAndItsReplacement holds the shipped register: every
// required script has a well-formed row, no row is listed twice, and the retired rows (the
// later cards of the stream add them) name a replacement verb that exists and a test for
// every behaviour.
func TestEveryStopgapNamesItsBehavioursAndItsReplacement(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	rows, faults := parseStopgaps(readFile(t, filepath.Join(root, stopgapsDoc)))
	require.Empty(t, faults, "%s is malformed", stopgapsDoc)

	seen := map[string]bool{}
	for _, r := range rows {
		require.False(t, seen[r.Name], "%s lists %s twice", stopgapsDoc, r.Name)
		seen[r.Name] = true
	}
	for _, name := range requiredStopgaps {
		require.True(t, seen[name], "%s has no section `## %s`: every hand script the fleet runs is listed", stopgapsDoc, name)
	}

	cli := readFile(t, filepath.Join(root, "docs", "CLI.md"))
	tests := treeTestNames(t, root)
	faults = retiredFaults(rows,
		func(tool, verb string) bool { return verbTableHas(cli, tool, verb) },
		func(name string) bool { return tests[name] })
	require.Empty(t, faults, "a retired stopgap must name a verb in its tool's table and a test for every behaviour")
}

// TestStopgapRegisterRefusesAnIncompleteRetiredRow pins the rule on fakes: a retired row is
// red for each missing piece, and green once it has the verb and a cited, existing test.
func TestStopgapRegisterRefusesAnIncompleteRetiredRow(t *testing.T) {
	t.Parallel()
	cli := "## nova-sprint\n\n| Command | What it does |\n| --- | --- |\n| `where [--json]` | The table |\n"
	hasVerb := func(tool, verb string) bool { return verbTableHas(cli, tool, verb) }
	hasTest := func(name string) bool { return name == "TestWhereReadsTheTable" }
	row := func(replacement, behaviour string) string {
		return "## wake\n\nPath: /x/watch.sh\nReplacement: " + replacement + "\nTool: nova-sprint\nSTATUS: retired 2026-10-05\n\nBehaviours:\n1. " + behaviour + "\n"
	}
	check := func(doc string) []string {
		rows, faults := parseStopgaps(doc)
		return append(faults, retiredFaults(rows, hasVerb, hasTest)...)
	}

	require.Empty(t, check(row("where", "It reads the table (test: TestWhereReadsTheTable)")))
	require.Contains(t, strings.Join(check(row("where", "It reads the table")), "\n"), "cites no `(test: TestX)`")
	require.Contains(t, strings.Join(check(row("where", "It reads the table (test: TestGone)")), "\n"), "TestGone, which exists in no test file")
	require.Contains(t, strings.Join(check(row("wake-verb", "It reads the table (test: TestWhereReadsTheTable)")), "\n"), `replacement verb "wake-verb"`)
	require.Contains(t, strings.Join(check(row("none yet", "It reads the table (test: TestWhereReadsTheTable)")), "\n"), "cannot say Replacement: none yet")

	live := "## wake\n\nPath: /x/watch.sh\nReplacement: none yet\nSTATUS: live\n\nBehaviours:\n1. It reads the table\n"
	require.Empty(t, check(live), "a live row needs no citation")
	require.Contains(t, strings.Join(check(strings.Replace(live, "STATUS: live", "STATUS: retired soon", 1)), "\n"), "is not `STATUS: live|retired")
}
