package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tla/COVERAGE.tsv says, for every state machine, where its code is and which
// TLA+ module holds it. The file is measured by reading the code and the module,
// never by what a README says. A row is current, partial, stale or missing.
// Every row that is not current names the card that owes the model, and the
// machine's own test reports a skip with that card, never a pass. Later cards
// call requireModelCurrent from a one-line test, TestTLAModelIsCurrent<Machine>,
// so a card that brings a model in is held to the verdict by the row it flips
// to current. No JVM runs here: a record is current by the fingerprint the
// runner wrote, as TestTLCRecordsCoverCurrentModels reads it.

var tlaCoverageHeader = []string{"machine", "code", "module", "status", "owed_by", "note"}

var tlaCoverageStatuses = []string{"current", "partial", "stale", "missing"}

var tlaCoverageCardID = regexp.MustCompile(`^[a-z0-9][a-z0-9._~-]*$`)

type tlaCoverageRow struct {
	machine, code, module, status, owedBy, note string
}

// modules are the modules the row names, in order; none for a row marked "-".
func (r tlaCoverageRow) modules() []string {
	if r.module == "-" {
		return nil
	}
	return strings.Split(r.module, ",")
}

func readTLACoverage(root string) ([]tlaCoverageRow, error) {
	rows, err := readTLCTSV(filepath.Join(root, "tla", "COVERAGE.tsv"), tlaCoverageHeader)
	if err != nil {
		return nil, fmt.Errorf("%w; tla/COVERAGE.tsv holds one row per state machine (columns %s)", err, strings.Join(tlaCoverageHeader, ", "))
	}
	var out []tlaCoverageRow
	for _, r := range rows {
		out = append(out, tlaCoverageRow{r[0], r[1], r[2], r[3], r[4], r[5]})
	}
	return out, nil
}

// tlaCoverageProblems is the shape of the file: what a row must say whatever
// its status. The verdict on a current row is requireModelCurrent's.
func tlaCoverageProblems(root string, rows []tlaCoverageRow) []string {
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	seen := map[string]bool{}
	for _, r := range rows {
		if r.machine == "" || seen[r.machine] {
			bad("COVERAGE row %q: the machine is empty or listed twice", r.machine)
		}
		seen[r.machine] = true
		if r.note == "" || r.note == "-" {
			bad("COVERAGE %s: the note says nothing; say what the code does that the module does or does not hold", r.machine)
		}
		known := false
		for _, s := range tlaCoverageStatuses {
			known = known || s == r.status
		}
		if !known {
			bad("COVERAGE %s: status %q is not one of %s", r.machine, r.status, strings.Join(tlaCoverageStatuses, ", "))
		}
		if r.code != "-" {
			for _, f := range strings.Split(r.code, ",") {
				if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil || !info.Mode().IsRegular() {
					bad("COVERAGE %s: code file %q does not exist", r.machine, f)
				}
			}
		}
		if r.status == "missing" != (r.module == "-") {
			bad("COVERAGE %s: a row marked missing names no module (-), and every other status names the module that holds it", r.machine)
		}
		for _, m := range r.modules() {
			if info, err := os.Stat(filepath.Join(root, "tla", m+".tla")); err != nil || !info.Mode().IsRegular() {
				bad("COVERAGE %s: module %s has no tla/%s.tla", r.machine, m, m)
			}
		}
		switch {
		case r.status == "current" && r.owedBy != "-":
			bad("COVERAGE %s: a current row is owed nothing; owed_by is - (a card that extends it goes in the note)", r.machine)
		case r.status != "current" && !tlaCoverageCardID.MatchString(r.owedBy):
			bad("COVERAGE %s: %s but owed_by is %q; name the card that owes the model", r.machine, r.status, r.owedBy)
		}
	}
	return problems
}

// tlaModuleCoverageProblems is what the file must name: every tla/<X>.tla whose
// name does not start with MC belongs to exactly one row's module column. A
// module no row names, or two or more rows name, is a problem. The MC modules
// are the TLC configurations beside a module and are named by no row.
func tlaModuleCoverageProblems(root string, rows []tlaCoverageRow) []string {
	entries, err := os.ReadDir(filepath.Join(root, "tla"))
	if err != nil {
		return []string{fmt.Sprintf("tla: %v", err)}
	}
	owners := map[string][]string{}
	for _, r := range rows {
		for _, m := range r.modules() {
			owners[m] = append(owners[m], r.machine)
		}
	}
	var problems []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".tla") {
			continue
		}
		module := strings.TrimSuffix(name, ".tla")
		if strings.HasPrefix(module, "MC") {
			continue
		}
		switch {
		case len(owners[module]) == 0:
			problems = append(problems, fmt.Sprintf("tla/%s.tla is named by no row of tla/COVERAGE.tsv; add one (status partial, stale or missing names the card that owes it)", module))
		case len(owners[module]) > 1:
			problems = append(problems, fmt.Sprintf("tla/%s.tla is named by %d rows of tla/COVERAGE.tsv (%s); name it in exactly one", module, len(owners[module]), strings.Join(owners[module], ", ")))
		}
	}
	return problems
}

// requireModelCurrent holds a machine to the verdict. A current row passes only
// if every module it names has, in tla/CASES.tsv, at least one expected-fail
// (a reversed witness: the plan's expected column is not pass, so TLC is
// expected to find the named violation; the configuration is the Broken one,
// whether or not its file name contains Broken) and every case of the module
// has a RUNS.tsv record whose fingerprint is what the case reads now and whose
// result is the declared one. A row marked missing, partial or stale is a skip
// naming the card that owes it, never a pass.
func requireModelCurrent(t *testing.T, machine string) {
	t.Helper()
	root := repoRoot(t)
	rows, err := readTLACoverage(root)
	require.NoError(t, err)
	var row *tlaCoverageRow
	for i := range rows {
		if rows[i].machine == machine {
			row = &rows[i]
		}
	}
	require.NotNilf(t, row, "tla/COVERAGE.tsv has no row for machine %q; add one before a test names it", machine)
	if row.status != "current" {
		require.Regexpf(t, tlaCoverageCardID, row.owedBy, "%s is %s and names no card that owes it", machine, row.status)
		t.Skipf("%s is %s: its model is owed by %s", machine, row.status, row.owedBy)
	}
	for _, problem := range tlaModelProblems(t, root, row.modules()) {
		assert.Failf(t, "model not current", "%s: %s", machine, problem)
	}
}

func tlaModelProblems(t *testing.T, root string, modules []string) []string {
	t.Helper()
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	plan, err := readTLCTSV(filepath.Join(root, "tla", "CASES.tsv"), []string{"config", "module", "expected", "property", "deadlock", "group", "gate", "debt"})
	require.NoError(t, err)
	records, err := readTLCTSV(filepath.Join(root, "tla", "RUNS.tsv"), tlcRunHeader)
	require.NoError(t, err)
	src, err := tlcSource(root)
	require.NoError(t, err)
	recorded := map[string][]string{}
	for _, r := range records {
		recorded[r[tc["config"]]] = r
	}
	if len(modules) == 0 {
		return []string{"the row names no module"}
	}
	for _, module := range modules {
		cases, witnesses := 0, 0
		for _, c := range plan {
			if c[1] != "MC"+module+".tla" {
				continue
			}
			cases++
			// expected is pass, or the violation TLC must find (invariant,
			// action, temporal). Anything but pass is an expected-fail witness.
			if c[2] != "pass" {
				witnesses++
			}
			rec, ok := recorded[c[0]]
			if !ok {
				if c[7] == "-" {
					bad("%s: case %s has no run record", module, c[0])
				}
				continue
			}
			want, _, err := src.Fingerprint(c[0])
			if err != nil || rec[tc["input_sha256"]] != want {
				bad("%s: the record of %s is not current for what the case reads", module, c[0])
			}
			if rec[tc["result"]] != "PASS" {
				bad("%s: the record of %s is %s, not PASS", module, c[0], rec[tc["result"]])
			}
		}
		if cases == 0 {
			bad("%s: tla/CASES.tsv declares no case for it", module)
		} else if witnesses == 0 {
			bad("%s: no case is expected to fail; a model with no reversed witness (Broken configuration) proves nothing", module)
		}
	}
	return problems
}

func TestEveryStateMachineHasACurrentModel(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	rows, err := readTLACoverage(root)
	require.NoError(t, err)
	require.NotEmpty(t, rows, "tla/COVERAGE.tsv has no rows")
	for _, problem := range tlaCoverageProblems(root, rows) {
		assert.Fail(t, problem)
	}
	for _, problem := range tlaModuleCoverageProblems(root, rows) {
		assert.Fail(t, problem)
	}
	for _, r := range rows {
		t.Run(r.machine, func(t *testing.T) { requireModelCurrent(t, r.machine) })
	}
}

// writeTLACoverageFixture lays down a root with the named modules under tla/
// and the given tla/COVERAGE.tsv body.
func writeTLACoverageFixture(t *testing.T, coverage string, modules ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "tla")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for _, m := range modules {
		require.NoError(t, os.WriteFile(filepath.Join(dir, m+".tla"), []byte("---- MODULE "+m+" ----\n====\n"), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "COVERAGE.tsv"), []byte(coverage), 0o644))
	return root
}

// coverageTSV is a tla/COVERAGE.tsv body of the header and the given row lines.
func coverageTSV(lines ...string) string {
	return strings.Join(append([]string{strings.Join(tlaCoverageHeader, "\t")}, lines...), "\n") + "\n"
}

// coverageRowLine is one row of tla/COVERAGE.tsv from its cells.
func coverageRowLine(cells ...string) string {
	return strings.Join(cells, "\t")
}

func TestTLACoverageNamesEveryModule(t *testing.T) {
	t.Parallel()
	const unnamed = "tla/B.tla is named by no row of tla/COVERAGE.tsv; add one (status partial, stale or missing names the card that owes it)"

	t.Run("a module no row names is reported and an MC module is not", func(t *testing.T) {
		t.Parallel()
		row := coverageRowLine("a", "-", "A", "partial", "some-card", "n")
		root := writeTLACoverageFixture(t, coverageTSV(row), "A", "B", "MCA")
		rows, err := readTLACoverage(root)
		require.NoError(t, err)
		assert.Equal(t, []string{unnamed}, tlaModuleCoverageProblems(root, rows))
	})

	t.Run("a module named by two rows names both rows", func(t *testing.T) {
		t.Parallel()
		first := coverageRowLine("a1", "-", "A", "partial", "some-card", "n")
		second := coverageRowLine("a2", "-", "A", "partial", "some-card", "n")
		root := writeTLACoverageFixture(t, coverageTSV(first, second), "A", "MCA")
		rows, err := readTLACoverage(root)
		require.NoError(t, err)
		problems := tlaModuleCoverageProblems(root, rows)
		require.Len(t, problems, 1)
		assert.Contains(t, problems[0], "tla/A.tla")
		assert.Contains(t, problems[0], "a1")
		assert.Contains(t, problems[0], "a2")
	})
}

func TestTLACoverageRefusesAnOwedRowWithNoCard(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	good := tlaCoverageRow{"m", "-", "-", "missing", "some-card", "n"}
	assert.Empty(t, tlaCoverageProblems(root, []tlaCoverageRow{good}))
	for _, bad := range []tlaCoverageRow{
		{"m", "-", "-", "missing", "-", "n"},
		{"m", "-", "-", "missing", "", "n"},
		{"m", "-", "Land", "stale", "-", "n"},
		{"m", "-", "Land", "current", "some-card", "n"},
		{"m", "-", "-", "current", "-", "n"},
		{"m", "no/such/file.go", "-", "missing", "some-card", "n"},
		{"m", "-", "NoSuchModule", "partial", "some-card", "n"},
		{"m", "-", "-", "done", "some-card", "n"},
	} {
		assert.NotEmptyf(t, tlaCoverageProblems(root, []tlaCoverageRow{bad}), "%+v", bad)
	}
}
