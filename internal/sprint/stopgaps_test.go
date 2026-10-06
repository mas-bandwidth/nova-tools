package sprint

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The stopgaps retire (docs/STOPGAPS.md): each of the coordinator's stopgaps
// names the verb that replaces it and the proof the verb does the job, and the
// seat check prints a STOPGAP line for each one still running.

// stopgapRow is one row of the table in docs/STOPGAPS.md.
type stopgapRow struct {
	Name, Card, Verb, Test, Run, Status string
}

const stopgapsDoc = "../../docs/STOPGAPS.md"

// readStopgapRows reads the table's rows, the header and its rule left out.
func readStopgapRows(t *testing.T) []stopgapRow {
	t.Helper()
	b, err := os.ReadFile(stopgapsDoc)
	require.NoError(t, err)
	var rows []stopgapRow
	header := true
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		if header || strings.HasPrefix(line, "|---") {
			header = false
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		require.Len(t, cells, 6, "a row of docs/STOPGAPS.md has six cells: %s", line)
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, stopgapRow{cells[0], cells[1], cells[2], cells[3], cells[4], cells[5]})
	}
	return rows
}

func missing(cell string) bool { return cell == "" || cell == "-" }

// testFound says whether `<package> <TestName>` names a test in that package
// of the tree at root.
func testFound(root, test string) bool {
	pkg, name, ok := strings.Cut(test, " ")
	if !ok || !strings.HasPrefix(name, "Test") {
		return false
	}
	files, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pkg), "*_test.go"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil && strings.Contains(string(b), "func "+name+"(") {
			return true
		}
	}
	return false
}

// refuseStopgapRow is the table's rule: a row names its card and verb; a
// retired row names a test found in the tree and one real run; an owed row
// says what is owed.
func refuseStopgapRow(root string, r stopgapRow) error {
	switch {
	case missing(r.Name):
		return errors.New("a row names no stopgap")
	case missing(r.Card) || missing(r.Verb):
		return errors.New(r.Name + ": no verb (card and command) replaces it")
	case r.Status == "retired":
		switch {
		case missing(r.Test):
			return errors.New(r.Name + ": retired with no test")
		case !testFound(root, r.Test):
			return errors.New(r.Name + ": retired on a test not found in the tree: " + r.Test)
		case missing(r.Run):
			return errors.New(r.Name + ": retired with no real run")
		}
		return nil
	case strings.HasPrefix(r.Status, "owed: ") && len(strings.TrimSpace(strings.TrimPrefix(r.Status, "owed: "))) > 0:
		return nil
	}
	return errors.New(r.Name + `: the status is neither "retired" nor "owed: <what>": ` + r.Status)
}

func TestEveryStopgapNamesItsVerbAndProof(t *testing.T) {
	t.Parallel()

	t.Run("the table is sprint.Stopgaps and every row keeps the rule", func(t *testing.T) {
		t.Parallel()
		rows := readStopgapRows(t)
		require.Len(t, rows, len(Stopgaps), "docs/STOPGAPS.md has one row per sprint.Stopgaps entry")
		for i, r := range rows {
			s := Stopgaps[i]
			assert.Equal(t, s.Name, r.Name, "row %d", i)
			assert.Equal(t, s.Card, r.Card, "row %d (%s)", i, r.Name)
			assert.Equal(t, s.Verb, r.Verb, "row %d (%s)", i, r.Name)
			assert.Equal(t, s.Retired, r.Status == "retired", "row %d (%s): sprint.Stopgaps Retired and the table's Status agree", i, r.Name)
			assert.NoError(t, refuseStopgapRow("../..", r))
		}
	})

	t.Run("a row whose verb or proof is missing is refused", func(t *testing.T) {
		t.Parallel()
		proven := stopgapRow{
			Name: "x.sh", Card: "c", Verb: "nova-sprint x",
			Test: "./internal/sprint TestEveryStopgapNamesItsVerbAndProof", Run: "nova-sprint x, 2026-10-06: X OK", Status: "retired",
		}
		require.NoError(t, refuseStopgapRow("../..", proven))
		for name, mutate := range map[string]func(*stopgapRow){
			"no card":                func(r *stopgapRow) { r.Card = "-" },
			"no verb":                func(r *stopgapRow) { r.Verb = "" },
			"retired with no test":   func(r *stopgapRow) { r.Test = "-" },
			"retired on a lost test": func(r *stopgapRow) { r.Test = "./internal/sprint TestNoSuchTestAnywhere" },
			"retired in no package":  func(r *stopgapRow) { r.Test = "./internal/nowhere TestEveryStopgapNamesItsVerbAndProof" },
			"retired with no run":    func(r *stopgapRow) { r.Run = "-" },
			"owed with no reason":    func(r *stopgapRow) { r.Status = "owed: " },
			"no status":              func(r *stopgapRow) { r.Status = "" },
		} {
			r := proven
			mutate(&r)
			assert.Error(t, refuseStopgapRow("../..", r), name)
		}
		owed := proven
		owed.Test, owed.Run, owed.Status = "-", "-", "owed: the card has not landed"
		assert.NoError(t, refuseStopgapRow("../..", owed))
	})
}

func TestTheSeatCheckPrintsAStopgapStillRunning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	procs := []ProcM{
		{PID: 101, Args: "/bin/zsh /srv/buds/a/runner.zsh"},
		{PID: 102, Args: "zsh -f /srv/buds/b/runner.zsh"},
		{PID: 103, Args: "/usr/bin/env python3 -u /home/coord/stopgaps/deliver.py --loop"},
		{PID: 104, Args: "/home/coord/stopgaps/seat-model.py"},
		{PID: 105, Args: "vim /home/coord/stopgaps/twin-widen.py"},
		{PID: 106, Args: "grep deliver.py"},
		{PID: 107, Args: "nova-friend run --as a --harness claude"},
	}

	t.Run("one line each, in the table's order, every pid", func(t *testing.T) {
		t.Parallel()
		ls := StopgapsRunning(procs)
		require.Len(t, ls, 3)
		assert.Equal(t, "runner.zsh", ls[0].Name)
		assert.Equal(t, []int{101, 102}, ls[0].PIDs)
		assert.Equal(t, "deliver.py", ls[1].Name)
		assert.Equal(t, "seat-model.py", ls[2].Name)
		assert.True(t, strings.HasPrefix(ls[0].String(), "STOPGAP runner.zsh still running pid=101,102 card=claude-oneshot-lanes "), ls[0].String())
	})

	t.Run("an owed stopgap is a note and a retired one is DOWN", func(t *testing.T) {
		t.Parallel()
		m := upMeasures()
		m.Procs = ProcsM{Measured: true, Procs: procs}
		r := JudgeSeatCheck(m, now)
		require.Len(t, r.Stopgaps, 3)
		for _, l := range r.Stopgaps {
			if !l.Retired {
				assert.Contains(t, r.Text(), l.String())
				assert.Contains(t, l.String(), "note=")
			}
		}
		retiredRunning := 0
		for _, l := range r.Stopgaps {
			if l.Retired {
				retiredRunning++
			}
		}
		assert.Equal(t, retiredRunning, r.Down, "only a retired stopgap still running is DOWN")

		retired := StopgapLine{Name: "deliver.py", PIDs: []int{103}, Card: "c", Verb: "v", Retired: true}
		assert.Equal(t, `STOPGAP deliver.py still running pid=103 card=c verb="v" remedy="kill 103; remove deliver.py and what starts it"`, retired.String())
	})

	t.Run("a stopgap still running counts against the exit once it is retired", func(t *testing.T) {
		t.Parallel()
		m := upMeasures()
		m.Procs = ProcsM{Measured: true, Procs: []ProcM{{PID: 9, Args: "zsh runner.zsh"}}}
		table := append([]Stopgap(nil), Stopgaps...)
		for i := range table {
			table[i].Retired = table[i].Name == "runner.zsh"
		}
		r := judgeSeatCheck(m, now, table)
		require.Len(t, r.Stopgaps, 1)
		assert.True(t, r.Stopgaps[0].Retired)
		assert.Contains(t, r.Text(), `STOPGAP runner.zsh still running pid=9 card=claude-oneshot-lanes verb="nova-friend run --harness claude (one-shot lanes)" remedy="kill 9; remove runner.zsh and what starts it"`)
		assert.Equal(t, 1, r.ExitCode)
		assert.Equal(t, 1, r.Down)
		assert.Contains(t, r.Summary(), "DOWN n=1 of=12")
	})

	t.Run("no line when the table was not read or the check ran in the server", func(t *testing.T) {
		t.Parallel()
		m := upMeasures()
		m.Procs = ProcsM{Procs: procs}
		assert.Empty(t, JudgeSeatCheck(m, now).Stopgaps)
		m.Procs.Measured = true
		m.Server.Self = true
		assert.Empty(t, JudgeSeatCheck(m, now).Stopgaps)
	})
}
