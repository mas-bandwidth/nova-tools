package sprint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stopgaps (docs/STOPGAPS.md; card the-stopgaps-retire): every hand script
// the coordinator ran names the card and verb that replace it and the proof the
// verb does the job, and the seat check prints STOPGAP <name> still running for
// each one alive on the seat's machine.

// stopgapRow is one row of the table in docs/STOPGAPS.md.
type stopgapRow struct {
	line                                     int
	name, did, card, verb, test, landed, run string
}

// stopgapRows reads the table: every line after the header that starts with
// "| " and has seven cells.
func stopgapRows(t *testing.T, doc string) []stopgapRow {
	t.Helper()
	var rows []stopgapRow
	inTable := false
	for i, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, "| Stopgap |") {
			inTable = true
			continue
		}
		if !inTable || strings.HasPrefix(l, "|---") {
			continue
		}
		if !strings.HasPrefix(l, "|") {
			if len(rows) > 0 {
				break
			}
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(l), "|"), "|")
		require.Len(t, cells, 7, "docs/STOPGAPS.md:%d: a row has seven cells: %s", i+1, l)
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		rows = append(rows, stopgapRow{i + 1, cells[0], cells[1], cells[2], cells[3], cells[4], cells[5], cells[6]})
	}
	return rows
}

// stopgapRowRefusal is why a row is refused, "" when it stands: a row names
// its card, its verb and its test; a landed row's test is in the tree (tests
// holds every Test function the tree defines); a row with a real run has
// landed. owed is the real-run cell of a row with no run recorded.
func stopgapRowRefusal(r stopgapRow, tests map[string]bool) string {
	owed := r.run == "" || strings.HasPrefix(r.run, "owed")
	switch {
	case r.name == "":
		return "the row names no stopgap"
	case r.card == "":
		return r.name + " names no card that replaces it"
	case r.verb == "":
		return r.name + " names no verb that replaces it"
	case r.test == "":
		return r.name + " names no test that proves its verb"
	case r.landed == "":
		return r.name + " does not say whether its card landed (a commit, or no)"
	case r.landed != "no" && !tests[r.test]:
		return r.name + "'s card landed but its test " + r.test + " is not in the tree"
	case r.landed == "no" && !owed:
		return r.name + " records a real run of a verb that has not landed"
	}
	return ""
}

// treeTests is every func Test... the tree's _test.go files define.
func treeTests(t *testing.T) map[string]bool {
	t.Helper()
	def := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	tests := map[string]bool{}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" || n == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range def.FindAllStringSubmatch(string(b), -1) {
			tests[m[1]] = true
		}
		return nil
	})
	require.NoError(t, err)
	return tests
}

func TestEveryStopgapNamesItsVerbAndProof(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "STOPGAPS.md"))
	require.NoError(t, err)
	rows := stopgapRows(t, string(b))
	tests := treeTests(t)

	t.Run("every row stands", func(t *testing.T) {
		require.NotEmpty(t, rows, "docs/STOPGAPS.md has no table")
		for _, r := range rows {
			assert.Empty(t, stopgapRowRefusal(r, tests), "docs/STOPGAPS.md:%d", r.line)
			assert.NotEmpty(t, r.did, "docs/STOPGAPS.md:%d: %s says nothing of what it did", r.line, r.name)
		}
	})

	t.Run("the table is sprint.Stopgaps row for row", func(t *testing.T) {
		require.Len(t, rows, len(Stopgaps), "docs/STOPGAPS.md and sprint.Stopgaps name the same stopgaps")
		for i, s := range Stopgaps {
			r := rows[i]
			assert.Equal(t, s.Name, r.name, "docs/STOPGAPS.md:%d", r.line)
			assert.Equal(t, s.Card, r.card, "docs/STOPGAPS.md:%d %s", r.line, s.Name)
			assert.Equal(t, s.Verb, r.verb, "docs/STOPGAPS.md:%d %s", r.line, s.Name)
			assert.Equal(t, s.Test, r.test, "docs/STOPGAPS.md:%d %s", r.line, s.Name)
			assert.Equal(t, s.Landed, r.landed != "no", "docs/STOPGAPS.md:%d %s: landed", r.line, s.Name)
			if strings.HasPrefix(r.run, "owed") {
				assert.Empty(t, s.Run, "docs/STOPGAPS.md:%d %s: the table says the run is owed", r.line, s.Name)
			} else {
				assert.Equal(t, s.Run, r.run, "docs/STOPGAPS.md:%d %s: real run", r.line, s.Name)
			}
		}
	})

	t.Run("a row with no verb or no proof is refused", func(t *testing.T) {
		good := stopgapRow{name: "x.sh", did: "a job", card: "c", verb: "nova-sprint v", test: "TestEveryStopgapNamesItsVerbAndProof", landed: "abc123", run: "owed"}
		require.Empty(t, stopgapRowRefusal(good, tests))
		for _, c := range []struct {
			what string
			edit func(*stopgapRow)
			want string
		}{
			{"no card", func(r *stopgapRow) { r.card = "" }, "names no card"},
			{"no verb", func(r *stopgapRow) { r.verb = "" }, "names no verb"},
			{"no test", func(r *stopgapRow) { r.test = "" }, "names no test"},
			{"landed, test not in the tree", func(r *stopgapRow) { r.test = "TestNoSuchTestAnywhere" }, "is not in the tree"},
			{"a real run of a verb not landed", func(r *stopgapRow) { r.landed, r.run = "no", "2026-10-06 studio: did it" }, "has not landed"},
		} {
			r := good
			c.edit(&r)
			assert.Contains(t, stopgapRowRefusal(r, tests), c.want, c.what)
		}
	})

	t.Run("a retired stopgap has a real run", func(t *testing.T) {
		for _, s := range Stopgaps {
			assert.Equal(t, s.Landed && s.Run != "", s.Retired(), s.Name)
		}
		assert.False(t, Stopgap{Landed: true}.Retired())
		assert.False(t, Stopgap{Run: "seen"}.Retired())
		assert.True(t, Stopgap{Landed: true, Run: "seen"}.Retired())
	})
}

// The script a process runs, read off its argv as ps prints it on the Studio
// (2026-10-06 07:58).
func TestStopgapScriptIsTheProgramOrTheInterpretersScript(t *testing.T) {
	t.Parallel()
	ps := ProcsFromPS(strings.Join([]string{
		" 8658 /bin/zsh ./runner.zsh",
		" 6196 /bin/zsh -c source /Users/x/.claude/shell-snapshots/s.sh && zsh /tmp/s/deliver-loop.sh",
		" 6198 /bin/zsh /tmp/s/deliver-loop.sh",
		"60730 /bin/zsh /tmp/s/zhi-beat.sh",
		" 1657 /opt/homebrew/Cellar/python@3.14/3.14.8/Frameworks/Python.framework/Versions/3.14/Resources/Python.app/Contents/MacOS/Python /tmp/s/finish-loop.py",
		"74612 find / -name finish-loop.py",
		"   12 python3 -u /tmp/s/twin-widen.py card stream heavy a.go",
		"   13 python3 -c import seat-model.py",
		"   14 tail -n +1 -f /tmp/s/seat-model.py",
		"   15 /tmp/s/note-when-delivered.sh job note",
		"21548 /bin/zsh ./runner.zsh",
		"",
		"garbage",
	}, "\n"))
	require.Len(t, ps, 11)
	assert.Equal(t, []StopgapM{
		{Name: "runner.zsh", PIDs: []int{8658, 21548}},
		{Name: "deliver-loop.sh", PIDs: []int{6198}},
		{Name: "note-when-delivered.sh", PIDs: []int{15}},
		{Name: "finish-loop.py", PIDs: []int{1657}},
		{Name: "zhi-beat.sh", PIDs: []int{60730}},
		{Name: "twin-widen.py", PIDs: []int{12}},
	}, StopgapsRunning(ps))
	assert.Empty(t, StopgapsRunning(nil))
}

func TestTheSeatCheckPrintsEveryStopgapStillRunning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 7, 58, 0, 0, time.UTC)

	t.Run("not measured prints no line", func(t *testing.T) {
		r := JudgeSeatCheck(upMeasures(), now)
		assert.Empty(t, r.Stopgaps)
		assert.NotContains(t, r.Text(), StopgapToken)
	})

	t.Run("an owed stopgap is a note, never DOWN", func(t *testing.T) {
		m := upMeasures()
		m.Stopgaps = StopgapsM{Measured: true, Running: []StopgapM{{Name: "runner.zsh", PIDs: []int{8658, 21548}}, {Name: "deliver-loop.sh", PIDs: []int{6198}}}}
		r := JudgeSeatCheck(m, now)
		assert.Equal(t, 0, r.ExitCode, r.Text())
		require.Len(t, r.Stopgaps, 2)
		text := r.Text()
		assert.Contains(t, text, "\nSTOPGAP runner.zsh still running pids=8658,21548 state=landed card=claude-oneshot-lanes verb=\"nova-friend run --harness claude (one-shot lanes, the row's config dir)\"\n")
		assert.Contains(t, text, "\nSTOPGAP deliver-loop.sh still running pids=6198 state=owed card=deliver-is-the-daemons-duty-in-order verb=")
		assert.NotContains(t, text, "remedy=\"kill")
		lines := strings.Split(strings.TrimSpace(text), "\n")
		assert.Equal(t, fmt.Sprintf("MACHINERY OK n=%d", len(r.Lines)+2), lines[len(lines)-1])
	})

	t.Run("a retired stopgap still running is DOWN with the kill", func(t *testing.T) {
		row := Stopgap{Name: "zhi-beat.sh", Card: "c", Verb: "v", Landed: true, Run: "seen"}
		lines := judgeStopgapsOf([]Stopgap{row}, StopgapsM{Measured: true, Running: []StopgapM{{Name: "zhi-beat.sh", PIDs: []int{60730, 7}}}})
		require.Len(t, lines, 1)
		assert.False(t, lines[0].Up)
		assert.Equal(t, `STOPGAP zhi-beat.sh still running pids=60730,7 state=retired card=c verb="v" remedy="kill 60730 7"`, lines[0].String())
	})

	t.Run("a measured table with none running prints none", func(t *testing.T) {
		m := upMeasures()
		m.Stopgaps = StopgapsM{Measured: true}
		r := JudgeSeatCheck(m, now)
		assert.Empty(t, r.Stopgaps)
		assert.Equal(t, 0, r.ExitCode)
	})
}
