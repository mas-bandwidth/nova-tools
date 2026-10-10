package sprint

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stopgaps (docs/STOPGAPS.md; card the-stopgaps-retire2): every hand script
// of the coordinator's names the verb that replaces it and the proof the verb
// does the job, and the seat check prints "STOPGAP <name> still running" for
// each one alive until it is removed.

// stopgapNames is every stopgap the card names (its START), so the table can
// lose none of them.
var stopgapNames = []string{
	"runner.zsh", "deliver.py", "deliver-loop.sh", "finish-loop.py", "zhi-beat.sh",
	"note-when-delivered.sh", "twin-widen.py", "twin-behind.py", "graft-audit.py", "seat-model.py",
}

const stopgapHeader = "| Stopgap | What it did | Card | Verb | Test | Landed | Real run |"

var (
	testNameRE = regexp.MustCompile(`^Test[A-Z][A-Za-z0-9_]*$`)
	commitRE   = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	runRE      = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2} [^ ]+:`)
)

// parseStopgapTable reads the register's table out of docs/STOPGAPS.md: one
// Stopgap a row, Landed "no" read as "" and Real run "owed" as "".
func parseStopgapTable(md string) ([]Stopgap, []string) {
	var rows []Stopgap
	var refusals []string
	in := false
	for _, l := range strings.Split(md, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == stopgapHeader:
			in = true
			continue
		case !in:
			continue
		case strings.HasPrefix(l, "|---"):
			continue
		case !strings.HasPrefix(l, "|"):
			in = false
			continue
		}
		cells := strings.Split(strings.Trim(l, "|"), "|")
		if len(cells) != 7 {
			refusals = append(refusals, "row has "+itoa(len(cells))+" cells, not 7: "+l)
			continue
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		s := Stopgap{Name: cells[0], Did: cells[1], Card: cells[2], Verb: cells[3], Test: cells[4], Landed: cells[5], Run: cells[6]}
		if s.Landed == "no" {
			s.Landed = ""
		}
		if s.Run == "owed" {
			s.Run = ""
		}
		rows = append(rows, s)
		if cells[5] == "" || cells[6] == "" {
			refusals = append(refusals, s.Name+": Landed and Real run say no and owed when there is none, never nothing")
		}
	}
	return rows, refusals
}

// checkStopgaps refuses every row whose verb or proof is missing: no name,
// card, verb or test; a test that is not a test's name; a Landed that is not a
// commit; a landed row whose test is not in the tree; a real run on a row not
// landed; a name twice; and a stopgap of the card's missing.
func checkStopgaps(rows []Stopgap, testInTree func(string) bool) []string {
	var refusals []string
	seen := map[string]bool{}
	for _, s := range rows {
		name := s.Name
		if name == "" {
			name = "<no name>"
			refusals = append(refusals, "a row names no stopgap")
		}
		if seen[s.Name] {
			refusals = append(refusals, name+": named twice")
		}
		seen[s.Name] = true
		if s.Card == "" {
			refusals = append(refusals, name+": no card replaces it")
		}
		if s.Verb == "" {
			refusals = append(refusals, name+": no verb replaces it")
		}
		switch {
		case s.Test == "":
			refusals = append(refusals, name+": no test proves its verb")
		case !testNameRE.MatchString(s.Test):
			refusals = append(refusals, name+": "+s.Test+" is not a test's name")
		case s.Landed != "" && !testInTree(s.Test):
			refusals = append(refusals, name+": landed, and its test "+s.Test+" is not in the tree")
		}
		if s.Landed != "" && !commitRE.MatchString(s.Landed) {
			refusals = append(refusals, name+": Landed "+s.Landed+" is neither a commit nor no")
		}
		if s.Run != "" {
			if s.Landed == "" {
				refusals = append(refusals, name+": a real run of a verb that has not landed")
			} else if !runRE.MatchString(s.Run) {
				refusals = append(refusals, name+": Real run "+s.Run+" does not start with a date, a host and a colon")
			}
		}
	}
	for _, n := range stopgapNames {
		if !seen[n] {
			refusals = append(refusals, n+": a stopgap of the card's with no row")
		}
	}
	return refusals
}

// testsInTree is every Test function declared in the repository's _test.go files.
func testsInTree(t *testing.T, root string) map[string]bool {
	t.Helper()
	decl := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	tests := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			tests[m[1]] = true
		}
		return nil
	})
	require.NoError(t, err)
	return tests
}

func TestEveryStopgapNamesItsVerbAndProof(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	md, err := os.ReadFile(filepath.Join(root, "docs", "STOPGAPS.md"))
	require.NoError(t, err)
	rows, refusals := parseStopgapTable(string(md))
	require.NotEmpty(t, rows, "docs/STOPGAPS.md carries no table under %q", stopgapHeader)
	tests := testsInTree(t, root)
	refusals = append(refusals, checkStopgaps(rows, func(n string) bool { return tests[n] })...)
	assert.Empty(t, refusals, "docs/STOPGAPS.md")
	assert.Empty(t, checkStopgaps(Stopgaps, func(n string) bool { return tests[n] }), "sprint.Stopgaps")
	assert.Equal(t, Stopgaps, rows, "docs/STOPGAPS.md and sprint.Stopgaps are one register, row for row")
}

// The reversed witnesses: the check refuses each missing piece by name.
func TestTheStopgapTableRefusesARowWithNoVerbOrProof(t *testing.T) {
	t.Parallel()
	inTree := func(n string) bool { return n == "TestTheVerbWorks" }
	whole := func() []Stopgap {
		var rows []Stopgap
		for _, n := range stopgapNames {
			rows = append(rows, Stopgap{Name: n, Card: "c", Verb: "nova-sprint v", Test: "TestTheVerbWorks"})
		}
		return rows
	}
	require.Empty(t, checkStopgaps(whole(), inTree))

	for _, c := range []struct {
		name   string
		break_ func(*Stopgap)
		want   string
	}{
		{"no verb", func(s *Stopgap) { s.Verb = "" }, "runner.zsh: no verb replaces it"},
		{"no card", func(s *Stopgap) { s.Card = "" }, "runner.zsh: no card replaces it"},
		{"no test", func(s *Stopgap) { s.Test = "" }, "runner.zsh: no test proves its verb"},
		{"not a test", func(s *Stopgap) { s.Test = "it works" }, "runner.zsh: it works is not a test's name"},
		{"landed test not in tree", func(s *Stopgap) { s.Landed, s.Test = "abc1234", "TestGone" }, "runner.zsh: landed, and its test TestGone is not in the tree"},
		{"landed not a commit", func(s *Stopgap) { s.Landed = "yes" }, "runner.zsh: Landed yes is neither a commit nor no"},
		{"run of an unlanded verb", func(s *Stopgap) { s.Run = "bench 2026-10-06" }, "runner.zsh: a real run of a verb that has not landed"},
		{"a run that is no date, host and colon", func(s *Stopgap) { s.Landed, s.Run = "abc1234", "yesterday on the bench" }, "runner.zsh: Real run yesterday on the bench does not start with a date, a host and a colon"},
	} {
		rows := whole()
		c.break_(&rows[0])
		assert.Contains(t, checkStopgaps(rows, inTree), c.want, c.name)
	}

	missing := whole()[1:]
	assert.Contains(t, checkStopgaps(missing, inTree), "runner.zsh: a stopgap of the card's with no row")

	md := stopgapHeader + "\n|---|---|---|---|---|---|---|\n| runner.zsh | runs cards | c | | TestTheVerbWorks | no | owed |\n| x.sh | | c | v | TestTheVerbWorks | | |\n"
	rows, refusals := parseStopgapTable(md)
	require.Len(t, rows, 2)
	assert.Contains(t, checkStopgaps(rows, inTree), "runner.zsh: no verb replaces it")
	assert.Contains(t, refusals, "x.sh: Landed and Real run say no and owed when there is none, never nothing")
}

func TestStopgapScriptIsTheProgramOrTheInterpretersScript(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args string
		want string
	}{
		{"/bin/zsh /Users/x/buds/rowan-space/runner.zsh", "runner.zsh"},
		{"zsh -f ./runner.zsh --once", "runner.zsh"},
		{"-zsh", ""},
		{"/usr/bin/python3 -u /tmp/scratch/deliver.py", "deliver.py"},
		{"/Library/Frameworks/Python.framework/Versions/3.12/Resources/Python.app/Contents/MacOS/Python finish-loop.py", "finish-loop.py"},
		{"/tmp/scratch/deliver-loop.sh", "deliver-loop.sh"},
		{"bash -c while true; do ./deliver.py; done", ""},
		{"python3 -m http.server", ""},
		{"tail -f /tmp/scratch/zhi-beat.sh", "tail"},
		{"grep finish-loop.py", "grep"},
		{"zsh", ""},
	} {
		assert.Equal(t, c.want, StopgapScript(strings.Fields(c.args)), c.args)
	}
}

func TestProcsFromPSReadsThePidAndArgv(t *testing.T) {
	t.Parallel()
	ps := ProcsFromPS("  101 /bin/zsh /x/runner.zsh\n\n  bad line\n 202 python3 /s/deliver.py --every 20\n303\n")
	assert.Equal(t, []Proc{
		{PID: 101, Args: []string{"/bin/zsh", "/x/runner.zsh"}},
		{PID: 202, Args: []string{"python3", "/s/deliver.py", "--every", "20"}},
	}, ps)
}

func TestTheSeatCheckPrintsEveryStopgapStillRunning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	ps := ProcsFromPS(" 11 /bin/zsh /b/rowan-space/runner.zsh\n 12 /bin/zsh /b/rowan-personal/runner.zsh\n 13 python3 /s/seat-model.py\n 14 bash -c ./finish-loop.py\n 15 vim twin-widen.py\n")

	m := upMeasures()
	m.Stopgaps = StopgapsM{Measured: true, Running: StopgapsAlive(Stopgaps, ps)}
	r := JudgeSeatCheck(m, now)
	require.Len(t, r.Stopgaps, 2, "runner.zsh and seat-model.py are alive; a -c text and an editor are not the scripts")
	text := r.Text()
	assert.Contains(t, text, `STOPGAP runner.zsh still running pids=11,12 state=landed card=claude-oneshot-lanes verb=`)
	assert.Contains(t, text, `STOPGAP seat-model.py still running pids=13 state=owed card=view-seat-is-the-coordinators-model verb="nova-sprint view seat [--json]"`)
	assert.NotContains(t, text, "finish-loop.py")
	assert.NotContains(t, text, "twin-widen.py")
	assert.Equal(t, 0, r.ExitCode, "a stopgap whose verb is not yet seen doing the job is a note")
	assert.Equal(t, "MACHINERY OK n="+itoa(len(r.Lines)+2), r.Summary())
	lines := strings.Split(strings.TrimSpace(text), "\n")
	assert.True(t, strings.HasPrefix(lines[len(lines)-1], "MACHINERY OK"), "the summary is the last line")
	assert.True(t, strings.HasPrefix(lines[len(lines)-2], "STOPGAP "), "the stopgaps print after the checks")

	// Retired, and still running: DOWN with the kill, exit 1, until it is removed.
	register := append([]Stopgap(nil), Stopgaps...)
	register[0].Landed, register[0].Run = "e67fdb9da", "a real run"
	ls := JudgeStopgaps(register, StopgapsM{Measured: true, Running: StopgapsAlive(register, ps)})
	require.Len(t, ls, 2)
	assert.False(t, ls[0].Up)
	assert.Equal(t, `STOPGAP runner.zsh still running pids=11,12 state=retired card=claude-oneshot-lanes verb="nova-friend run with a claude one-shot lane (the row's CLAUDE_CONFIG_DIR)" remedy="kill 11 12"`, ls[0].String())
	assert.True(t, ls[1].Up)

	// Removed: no line.
	m.Stopgaps = StopgapsM{Measured: true, Running: StopgapsAlive(Stopgaps, nil)}
	r = JudgeSeatCheck(m, now)
	assert.Empty(t, r.Stopgaps)
	assert.NotContains(t, r.Text(), StopgapToken)

	// Not measured (the server's own check): no line, exit 0.
	m.Stopgaps = StopgapsM{}
	r = JudgeSeatCheck(m, now)
	assert.Empty(t, r.Stopgaps)
	assert.NotContains(t, r.Text(), "stopgaps")
	assert.Equal(t, 0, r.ExitCode, "a check that read no process table is not DOWN")

	// A ps that failed: DOWN, exit 1, never MACHINERY OK.
	m.Stopgaps = StopgapsM{Err: "ps: exit 1"}
	r = JudgeSeatCheck(m, now)
	assert.Equal(t, 1, r.ExitCode, "a failed process scan is DOWN")
	assert.Contains(t, r.Text(), `MACHINERY stopgaps DOWN why="process scan failed: ps: exit 1" remedy="ps -axww -o pid=,args="`)
}

func TestARetiredStopgapStillRunningIsDownInTheReport(t *testing.T) {
	t.Parallel()
	retired := Stopgap{Name: "x.sh", Card: "c", Verb: "v", Test: "TestV", Landed: "abc1234", Run: "r"}
	assert.Equal(t, "retired", retired.State())
	assert.Equal(t, "landed", Stopgap{Landed: "abc1234"}.State())
	assert.Equal(t, "owed", Stopgap{}.State())

	ls := JudgeStopgaps([]Stopgap{retired}, StopgapsM{Measured: true, Running: []StopgapM{{Name: "x.sh", PIDs: []int{7}}, {Name: "not-a-row.sh", PIDs: []int{8}}}})
	require.Len(t, ls, 1)
	r := SeatCheckReport{Lines: []SeatCheckLine{{Thing: "server", Up: true}}, Stopgaps: ls, Down: 1}
	assert.Equal(t, "MACHINERY DOWN n=1 of=2", r.Summary())
	assert.Contains(t, r.JSON(), `"stopgaps":[{"stopgap":{"name":"x.sh"`)
}
