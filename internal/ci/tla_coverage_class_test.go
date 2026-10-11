package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// tlaCoverageModuleProblems holds every module in tla/ to a row: each
// tla/<X>.tla whose name does not begin with MC (the model-checker modules) must
// be named by exactly one row's module column. tlaCoverageProblems walks the rows
// and never sees a module no row names; this walks the modules.
func tlaCoverageModuleProblems(root string, rows []tlaCoverageRow) []string {
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	namedBy := map[string][]string{}
	for _, r := range rows {
		for _, m := range r.modules() {
			namedBy[m] = append(namedBy[m], r.machine)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "tla"))
	if err != nil {
		return []string{fmt.Sprintf("tla/ cannot be read: %v", err)}
	}
	var modules []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tla") {
			continue
		}
		m := strings.TrimSuffix(e.Name(), ".tla")
		if strings.HasPrefix(m, "MC") {
			continue
		}
		modules = append(modules, m)
	}
	sort.Strings(modules)
	for _, m := range modules {
		switch named := namedBy[m]; {
		case len(named) == 0:
			bad("tla/%s.tla is named by no row of tla/COVERAGE.tsv; add one (status partial, stale or missing names the card that owes it)", m)
		case len(named) > 1:
			bad("tla/%s.tla is named by %d rows (%s); a module has exactly one row", m, len(named), strings.Join(named, ", "))
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
	for _, problem := range tlaCoverageModuleProblems(root, rows) {
		assert.Fail(t, problem)
	}
	for _, r := range rows {
		t.Run(r.machine, func(t *testing.T) { requireModelCurrent(t, r.machine) })
	}
}

// writeTLACoverageFixture lays down a tla/ directory under a fresh t.TempDir()
// with one empty module per name and a COVERAGE.tsv whose rows are given
// without the header. It returns the root readTLACoverage reads.
func writeTLACoverageFixture(t *testing.T, rows []string, modules ...string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tla"), 0o755))
	for _, m := range modules {
		require.NoError(t, os.WriteFile(filepath.Join(root, "tla", m+".tla"), []byte("---- MODULE "+m+" ----\n====\n"), 0o644))
	}
	content := strings.Join(append([]string{strings.Join(tlaCoverageHeader, "\t")}, rows...), "\n") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "COVERAGE.tsv"), []byte(content), 0o644))
	return root
}

// TestTLACoverageNamesEveryModule proves a module no row names is caught: the
// real tla/ holds a module for every machine, so each tla/<X>.tla whose name is
// not an MC module must be named by exactly one row's module column.
func TestTLACoverageNamesEveryModule(t *testing.T) {
	t.Parallel()
	t.Run("a module named by no row is reported", func(t *testing.T) {
		t.Parallel()
		root := writeTLACoverageFixture(t, []string{"a\t-\tA\tpartial\tsome-card\tnote"}, "A", "B", "MCA")
		rows, err := readTLACoverage(root)
		require.NoError(t, err)
		got := strings.Join(tlaCoverageModuleProblems(root, rows), "\n")
		assert.Contains(t, got, "tla/B.tla is named by no row of tla/COVERAGE.tsv; add one (status partial, stale or missing names the card that owes it)")
		assert.NotContains(t, got, "MCA.tla")
	})
	t.Run("a module named by two rows reports both rows", func(t *testing.T) {
		t.Parallel()
		root := writeTLACoverageFixture(t, []string{
			"one\t-\tA\tpartial\tsome-card\tnote",
			"two\t-\tA\tpartial\tsome-card\tnote",
		}, "A")
		rows, err := readTLACoverage(root)
		require.NoError(t, err)
		got := strings.Join(tlaCoverageModuleProblems(root, rows), "\n")
		assert.Contains(t, got, "tla/A.tla")
		assert.Contains(t, got, "one")
		assert.Contains(t, got, "two")
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
