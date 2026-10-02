package ci

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// Model changes require new measured evidence, independent of checkout mtimes.
// A record's fingerprint covers what its own case reads: the configuration, the
// module the plan names for it and the modules that one extends or instantiates,
// the case's own row of the plan, and the runner's result files
// (tlc.ResultFiles), so changing a dependency or the interpretation of a result invalidates
// the records that read it and no other. No JVM or network runs in this class
// test.
func TestTLCRecordsCoverCurrentModels(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	for _, problem := range tlcRecordProblems(root) {
		t.Error(problem)
	}
	// The runner that writes the records and this class test must compute the
	// same fingerprint of the same files: a record the runner wrote is stale only
	// because an input changed, never because the two disagree on which files an
	// input is. The runner reads its own files from the bytes it was built with;
	// this test reads them from the checkout.
	plan, err := readTLCTSV(filepath.Join(root, "tla", "CASES.tsv"), []string{"config", "module", "expected", "property", "deadlock", "group", "gate", "debt"})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := tlcSource(root)
	if err != nil {
		t.Fatal(err)
	}
	built, err := tlc.SourceAt(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range plan {
		want, wantFiles, err := disk.Fingerprint(row[0])
		if err != nil {
			t.Errorf("TLC %s: %v", row[0], err)
			continue
		}
		if got, files, err := built.Fingerprint(row[0]); err != nil || got != want || files != wantFiles {
			t.Errorf("TLC %s: internal/tlc computes fingerprint %s over %d files (%v), this class test computes %s over %d: the runner and the class disagree on the inputs", row[0], got, files, err, want, wantFiles)
		}
	}
	// A case reads its own models and the ones they extend, and no others: the
	// point of a per-case fingerprint, held on the real tree.
	inputs, err := disk.Inputs("MCEpochMemberFixedPoint.cfg")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, in := range inputs {
		paths = append(paths, in.Path)
	}
	for _, need := range []string{"tla/MCEpochMemberFixedPoint.cfg", "tla/MCEpochMemberTable.tla", "tla/EpochMemberTable.tla", "tla/MemberTable.tla", "tla/TableMachine.tla", "tla/CASES.tsv#MCEpochMemberFixedPoint.cfg", "internal/tlc/run.go", "internal/tlc/outcome.go", "internal/tlc/suite.go"} {
		if !slices.Contains(paths, need) {
			t.Errorf("MCEpochMemberFixedPoint.cfg does not read %s; it reads %v", need, paths)
		}
	}
	for _, p := range paths {
		if strings.HasPrefix(p, "tla/") && !slices.Contains([]string{"tla/MCEpochMemberFixedPoint.cfg", "tla/MCEpochMemberTable.tla", "tla/EpochMemberTable.tla", "tla/MemberTable.tla", "tla/TableMachine.tla", "tla/CASES.tsv#MCEpochMemberFixedPoint.cfg"}, p) {
			t.Errorf("MCEpochMemberFixedPoint.cfg reads %s, which is not one of its inputs", p)
		}
	}
	required := map[string]bool{}
	for _, row := range plan {
		if row[6] == "required" {
			required[row[1]] = true
		}
		if row[7] != "-" {
			t.Logf("TLC DEBT config=%s reason=%s; failed or missing evidence is not a pass", row[0], row[7])
		}
	}
	for _, module := range []string{"MCMemberTable.tla", "MCEpochMemberTable.tla", "MCTableEdit.tla", "MCTableOrder.tla", "MCTableSession.tla", "MCTableFirstContact.tla", "MCRedisFn.tla", "MCFirstConn.tla"} {
		if !required[module] {
			t.Errorf("TLC required model %s disappeared from the gate", module)
		}
	}
}

// tc is the index of each column of tla/RUNS.tsv by its name.
var tc = func() map[string]int {
	m := map[string]int{}
	for i, name := range tlcRunHeader {
		m[name] = i
	}
	return m
}()

var tlcRunHeader = []string{"config", "module", "input_sha256", "input_files", "jar_sha256", "java_version", "host", "cpus", "started_utc", "workers", "generated", "distinct", "seconds", "exit", "result", "expected", "property", "budget", "mode"}

func readTLCTSV(path string, header []string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = len(header)
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 1 || strings.Join(rows[0], "\t") != strings.Join(header, "\t") {
		return nil, fmt.Errorf("%s: incorrect TSV header", path)
	}
	return rows[1:], nil
}

// tlcSource is where the inputs of a case are read from in root: the models and
// the plan under tla/, and the runner's result files from the checkout (the
// bytes of tlc.ResultFiles; the bookkeeping files and the tests are not part of
// how a result is read).
func tlcSource(root string) (tlc.Source, error) {
	plan, err := os.ReadFile(filepath.Join(root, "tla", "CASES.tsv"))
	if err != nil {
		return tlc.Source{}, err
	}
	runner := map[string][]byte{}
	for _, name := range tlc.ResultFiles {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tlc.RunnerDir), name))
		if err != nil {
			return tlc.Source{}, err
		}
		runner[tlc.RunnerDir+"/"+name] = raw
	}
	return tlc.Source{TLADir: filepath.Join(root, "tla"), Plan: plan, Runner: runner}, nil
}

func tlcRecordProblems(root string) []string {
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	src, err := tlcSource(root)
	if err != nil {
		return []string{fmt.Sprintf("TLC inputs: %v", err)}
	}
	plan, err := readTLCTSV(filepath.Join(root, "tla", "CASES.tsv"), []string{"config", "module", "expected", "property", "deadlock", "group", "gate", "debt"})
	if err != nil {
		return []string{fmt.Sprintf("TLC cases: %v", err)}
	}
	configs, err := filepath.Glob(filepath.Join(root, "tla", "MC*.cfg"))
	if err != nil || len(configs) == 0 {
		return []string{fmt.Sprintf("TLC configuration inventory: %v", err)}
	}
	wanted := map[string][]string{}
	for _, row := range plan {
		if _, ok := wanted[row[0]]; ok {
			bad("TLC duplicate case %s", row[0])
		}
		wanted[row[0]] = row
		if filepath.Base(row[1]) != row[1] || !strings.HasPrefix(row[1], "MC") || !strings.HasSuffix(row[1], ".tla") {
			bad("TLC %s invalid module %s", row[0], row[1])
		} else if info, err := os.Stat(filepath.Join(root, "tla", row[1])); err != nil || !info.Mode().IsRegular() {
			bad("TLC %s missing module %s", row[0], row[1])
		}
		if _, _, err := src.Fingerprint(row[0]); err != nil {
			bad("TLC %s: what the case reads cannot be resolved: %v", row[0], err)
		}
		if row[4] != "check" && row[4] != "ignore-terminal" {
			bad("TLC %s invalid deadlock policy", row[0])
		}
		if row[5] == "" || strings.ContainsAny(row[5], " /\\\t\n") {
			bad("TLC %s invalid group", row[0])
		}
		// These models are outside layer one's required table gate. New modules
		// and cases are required by default; changing a gate to debt cannot hide one.
		bench := row[1] == "MCCardMachine.tla" || row[1] == "MCTableMachine.tla" || row[1] == "MCFileLock.tla"
		if row[6] != "required" && row[6] != "bench" || row[6] == "bench" && !bench {
			bad("TLC %s cannot leave the required model gate", row[0])
		}
		if row[7] == "" || row[7] != "-" && (row[6] != "bench" || row[1] != "MCCardMachine.tla") {
			bad("TLC %s invalid debt declaration", row[0])
		}
	}
	actual := map[string]bool{}
	for _, path := range configs {
		name := filepath.Base(path)
		actual[name] = true
		if _, ok := wanted[name]; !ok {
			bad("TLC %s has no declared case; add it and run it on a bench", name)
		}
	}
	for name := range wanted {
		if !actual[name] {
			bad("TLC declared case %s has no configuration", name)
		}
	}
	records, err := readTLCTSV(filepath.Join(root, "tla", "RUNS.tsv"), tlcRunHeader)
	if err != nil {
		bad("TLC run records: %v; run make tlc on a Linux bench and commit the complete records", err)
		return problems
	}
	seen := map[string]bool{}
	for _, row := range records {
		name := row[tc["config"]]
		p, ok := wanted[name]
		if !ok || seen[name] {
			bad("TLC unexpected or duplicate run record %s", name)
			continue
		}
		seen[name] = true
		if row[tc["module"]] != p[1] || row[tc["expected"]] != p[2] || row[tc["property"]] != p[3] {
			bad("TLC %s record disagrees with its declared module or outcome", name)
		}
		if want, files, err := src.Fingerprint(name); err == nil {
			if row[tc["input_sha256"]] != want || row[tc["input_files"]] != strconv.Itoa(files) {
				bad("TLC %s record is stale: it was measured on inputs %s (%d files) and the case now reads %s (%d files): %s. The case is in group %s: run `tlacheck groups --stale` for the groups to run again, run each on a Linux bench into a clean directory, then `tlacheck merge --keep tla/RUNS.tsv` (tla/README.md gives the commands; `tlacheck inputs --case %s` prints each file and its hash)",
					name, row[tc["input_sha256"]], atoiOrZero(row[tc["input_files"]]), want, files, tlcInputList(src, name), p[5], name)
			}
		}
		if w := row[tc["workers"]]; w != "1" && w != "2" {
			bad("TLC %s records %q workers; a run uses one or two", name, w)
		}
		if v := row[tc["java_version"]]; v == "" || strings.ContainsAny(v, " \t") {
			bad("TLC %s records no java version", name)
		}
		if h := row[tc["host"]]; !tlc.ValidPlatform(h) {
			bad("TLC %s records host %q: the column holds the platform label the tool computes (<goos>-<goarch>, one of %s), never a machine name", name, h, strings.Join(tlc.Platforms, ", "))
		}
		if n, err := strconv.Atoi(row[tc["cpus"]]); err != nil || n < 1 || n > 4096 {
			bad("TLC %s records %q logical CPUs", name, row[tc["cpus"]])
		}
		jar, err := hex.DecodeString(row[tc["jar_sha256"]])
		if err != nil || len(jar) != sha256.Size {
			bad("TLC %s is missing jar identity or bench provenance", name)
		}
		if _, err := time.Parse(time.RFC3339Nano, row[tc["started_utc"]]); err != nil {
			bad("TLC %s has no valid run timestamp", name)
		}
		generated, eg := strconv.ParseUint(row[tc["generated"]], 10, 64)
		distinct, ed := strconv.ParseUint(row[tc["distinct"]], 10, 64)
		seconds, es := strconv.ParseFloat(row[tc["seconds"]], 64)
		budget, eb := strconv.ParseFloat(row[tc["budget"]], 64)
		bounded := row[tc["mode"]] == "bounded"
		if eb != nil || math.IsNaN(budget) || math.IsInf(budget, 0) || budget <= 0 || budget > 3600 || bounded && budget > 110 || !bounded && row[tc["mode"]] != "manual" || p[6] == "required" && !bounded {
			bad("TLC %s invalid run budget or mode", name)
		}
		if es != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > budget+2 {
			bad("TLC %s invalid elapsed time or exceeded its recorded budget", name)
		}
		debtFailure := p[6] == "bench" && p[7] != "-" && row[tc["result"]] == "FAIL"
		if !debtFailure && (eg != nil || ed != nil || distinct == 0 || generated < distinct || bounded && seconds > 110) {
			bad("TLC %s has invalid state counts or exceeded its 110-second budget", name)
		}
		if debtFailure {
			if _, err := strconv.Atoi(row[tc["exit"]]); err != nil {
				bad("TLC %s debt record has invalid exit", name)
			}
			continue // retained failed measurement, explicitly not a passing proof
		}
		code := map[string]string{"pass": "0", "invariant": "12", "action": "13", "temporal": "13"}[p[2]]
		if code == "" || row[tc["exit"]] != code || row[tc["result"]] != "PASS" {
			bad("TLC %s did not reach its declared result; timeout and generic failure are not evidence", name)
		}
	}
	jars := map[string]int{}
	for _, row := range records {
		jars[row[tc["jar_sha256"]]]++
	}
	if len(jars) > 1 {
		var parts []string
		for jar, n := range jars {
			parts = append(parts, fmt.Sprintf("%.12s (%d records)", jar, n))
		}
		sort.Strings(parts)
		bad("TLC run records hold %d jars (%s); one jar measures the whole file: run again the groups recorded under the other jars with one jar (`tlacheck groups --stale` names none, since the jar is not an input; run those groups, then `tlacheck merge --keep`), or run every group with one jar", len(jars), strings.Join(parts, ", "))
	}
	for name := range wanted {
		if !seen[name] && wanted[name][7] == "-" {
			bad("TLC %s has no run record; run every declared case", name)
		}
	}
	return problems
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// tlcInputList names the files a case reads, for a refusal that cannot know
// which of them changed: the record holds the fingerprint and not a file list.
func tlcInputList(src tlc.Source, config string) string {
	inputs, err := src.Inputs(config)
	if err != nil {
		return err.Error()
	}
	var names []string
	for _, in := range inputs {
		names = append(names, in.Path)
	}
	return "the case reads " + strings.Join(names, " ")
}

const tlcFixturePlanHeader = "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"

// tlcFixture is a small checkout: MCA and MCB extend one shared module and
// MCLone shares nothing with either. Its records are made from the files as
// they are, so a fixture starts current and a test changes one thing.
type tlcFixture struct {
	t    *testing.T
	root string
}

func newTLCFixture(t *testing.T) tlcFixture {
	t.Helper()
	f := tlcFixture{t: t, root: t.TempDir()}
	f.write("tla/Shared.tla", "---- MODULE Shared ----\nEXTENDS Naturals\nVARIABLE x\n====\n")
	f.write("tla/MCA.tla", "---- MODULE MCA ----\nEXTENDS Shared, FiniteSets\n====\n")
	f.write("tla/MCB.tla", "---- MODULE MCB ----\n(* EXTENDS Ignored *)\nEXTENDS Shared\n====\n")
	f.write("tla/MCLone.tla", "---- MODULE MCLone ----\nEXTENDS Sequences\n====\n")
	for _, name := range []string{"MCA", "MCB", "MCLone"} {
		f.write("tla/"+name+".cfg", "SPECIFICATION Spec\n")
	}
	for _, name := range tlc.ResultFiles {
		f.write("internal/tlc/"+name, "sample "+name+"\n")
	}
	for _, name := range tlc.BookkeepingFiles {
		f.write("internal/tlc/"+name, "bookkeeping "+name+"\n")
	}
	f.write("internal/tlc/run_test.go", "the runner's tests are not an input\n")
	f.write("tla/README.md", "not an input\n")
	f.write("tla/CASES.tsv", tlcFixturePlanHeader+
		"MCA.cfg\tMCA.tla\tpass\t-\tcheck\talpha\trequired\t-\n"+
		"MCB.cfg\tMCB.tla\tpass\t-\tcheck\tbeta\trequired\t-\n"+
		"MCLone.cfg\tMCLone.tla\tpass\t-\tcheck\tgamma\trequired\t-\n")
	f.seal(nil)
	return f
}

func (f tlcFixture) write(name, contents string) {
	f.t.Helper()
	path := filepath.Join(f.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f tlcFixture) read(name string) string {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(raw)
}

// seal writes tla/RUNS.tsv: one record per case of the plan, on the fingerprint
// the case reads now. edit may change a record (its fields by column) or return
// nil to leave the case out.
func (f tlcFixture) seal(edit func(row []string) []string) {
	f.t.Helper()
	plan, err := readTLCTSV(filepath.Join(f.root, "tla", "CASES.tsv"), []string{"config", "module", "expected", "property", "deadlock", "group", "gate", "debt"})
	if err != nil {
		f.t.Fatal(err)
	}
	src, err := tlcSource(f.root)
	if err != nil {
		f.t.Fatal(err)
	}
	out := strings.Join(tlcRunHeader, "\t") + "\n"
	for _, p := range plan {
		fp, files, err := src.Fingerprint(p[0])
		if err != nil {
			f.t.Fatal(err)
		}
		row := make([]string, len(tlcRunHeader))
		for name, v := range map[string]string{"config": p[0], "module": p[1], "input_sha256": fp, "input_files": strconv.Itoa(files), "jar_sha256": strings.Repeat("a", 64), "java_version": "21.0.12.1", "workers": "2", "host": "linux-amd64", "cpus": "8", "started_utc": "2026-01-01T00:00:00Z", "generated": "10", "distinct": "5", "seconds": "1.25", "exit": "0", "result": "PASS", "expected": p[2], "property": p[3], "budget": "110", "mode": "bounded"} {
			row[tc[name]] = v
		}
		if edit != nil {
			if row = edit(row); row == nil {
				continue
			}
		}
		out += strings.Join(row, "\t") + "\n"
	}
	f.write("tla/RUNS.tsv", out)
}

// stale is the sorted list of cases whose records the class test calls stale,
// and the other problems it names.
func (f tlcFixture) stale() (stale, other []string) {
	f.t.Helper()
	re := regexp.MustCompile(`^TLC (\S+) record is stale`)
	for _, problem := range tlcRecordProblems(f.root) {
		if m := re.FindStringSubmatch(problem); m != nil {
			stale = append(stale, m[1])
		} else {
			other = append(other, problem)
		}
	}
	sort.Strings(stale)
	return stale, other
}

func (f tlcFixture) expectStale(want ...string) {
	f.t.Helper()
	stale, other := f.stale()
	if want == nil {
		want = []string{}
	}
	if stale == nil {
		stale = []string{}
	}
	if len(other) != 0 || strings.Join(stale, ",") != strings.Join(want, ",") {
		f.t.Fatalf("stale %v and other problems %v; want stale %v and no other", stale, other, want)
	}
}

// A record is current until one of the files its case reads changes, and then
// only that case's record is stale.
func TestTLCPerCaseFingerprintStalesOnlyTheCasesThatReadWhatChanged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		edit  func(f tlcFixture)
		stale []string
	}{
		{"nothing changed", func(f tlcFixture) {}, nil},
		{"a model edited after its record", func(f tlcFixture) { f.write("tla/MCA.tla", f.read("tla/MCA.tla")+"\\* edited\n") }, []string{"MCA.cfg"}},
		{"an unrelated model edited", func(f tlcFixture) { f.write("tla/MCLone.tla", f.read("tla/MCLone.tla")+"\\* edited\n") }, []string{"MCLone.cfg"}},
		{"a shared module edited", func(f tlcFixture) { f.write("tla/Shared.tla", f.read("tla/Shared.tla")+"\\* edited\n") }, []string{"MCA.cfg", "MCB.cfg"}},
		{"a configuration edited", func(f tlcFixture) { f.write("tla/MCB.cfg", "SPECIFICATION Other\n") }, []string{"MCB.cfg"}},
		{"one row of the plan edited", func(f tlcFixture) {
			f.write("tla/CASES.tsv", strings.Replace(f.read("tla/CASES.tsv"), "check\tbeta", "ignore-terminal\tbeta", 1))
		}, []string{"MCB.cfg"}},
		{"the runner edited", func(f tlcFixture) { f.write("internal/tlc/run.go", "changed interpretation\n") }, []string{"MCA.cfg", "MCB.cfg", "MCLone.cfg"}},
		{"a bookkeeping file of the runner edited", func(f tlcFixture) { f.write("internal/tlc/inputs.go", "the list of standard modules grew\n") }, nil},
		{"the runner's tests edited", func(f tlcFixture) { f.write("internal/tlc/run_test.go", "edited\n") }, nil},
		{"a file that is not an input edited", func(f tlcFixture) { f.write("tla/README.md", "edited\n") }, nil},
		{"a module nobody extends added", func(f tlcFixture) { f.write("tla/Unused.tla", "---- MODULE Unused ----\n====\n") }, nil},
		{"a module named only in a comment added", func(f tlcFixture) {
			f.write("tla/Ignored.tla", "---- MODULE Ignored ----\n====\n")
		}, nil},
		{"a record with the wrong count of files", func(f tlcFixture) {
			f.seal(func(row []string) []string {
				if row[tc["config"]] == "MCLone.cfg" {
					row[tc["input_files"]] = "99"
				}
				return row
			})
		}, []string{"MCLone.cfg"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newTLCFixture(t)
			tc.edit(f)
			f.expectStale(tc.stale...)
		})
	}
}

// The refusal names the case, and the files the case reads, so a change to any
// of them is found from it; the record holds the fingerprint, not a file list.
func TestTLCStaleRecordNamesTheCaseAndItsInputFiles(t *testing.T) {
	t.Parallel()
	f := newTLCFixture(t)
	f.write("tla/Shared.tla", f.read("tla/Shared.tla")+"\\* edited\n")
	problems := tlcRecordProblems(f.root)
	if len(problems) != 2 {
		t.Fatalf("problems %v", problems)
	}
	for i, config := range []string{"MCA.cfg", "MCB.cfg"} {
		group := map[string]string{"MCA.cfg": "alpha", "MCB.cfg": "beta"}[config]
		for _, want := range []string{"TLC " + config + " record is stale", "tla/Shared.tla", "tla/" + config, "tlacheck inputs --case " + config, "group " + group + ":", "`tlacheck groups --stale`", "`tlacheck merge --keep tla/RUNS.tsv`", "clean directory"} {
			if !strings.Contains(problems[i], want) {
				t.Errorf("%q does not name %q", problems[i], want)
			}
		}
	}
	if strings.Contains(strings.Join(problems, "\n"), "MCLone") {
		t.Errorf("the case that does not read the module is named: %v", problems)
	}
}

func TestTLCRecordsAndCasesMustMatchOneToOne(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(f tlcFixture)
		want string
	}{
		{"a configuration with no declared case", func(f tlcFixture) { f.write("tla/MCNew.cfg", "SPECIFICATION Spec\n") }, "MCNew.cfg has no declared case"},
		{"a declared case with no record", func(f tlcFixture) {
			f.seal(func(row []string) []string {
				if row[tc["config"]] == "MCB.cfg" {
					return nil
				}
				return row
			})
		}, "MCB.cfg has no run record"},
		{"a new case with a configuration, a plan row and no record", func(f tlcFixture) {
			f.write("tla/MCC.cfg", "SPECIFICATION Spec\n")
			f.write("tla/MCC.tla", "---- MODULE MCC ----\n====\n")
			f.write("tla/CASES.tsv", f.read("tla/CASES.tsv")+"MCC.cfg\tMCC.tla\tpass\t-\tcheck\tdelta\trequired\t-\n")
			f.seal(func(row []string) []string {
				if row[tc["config"]] == "MCC.cfg" {
					return nil
				}
				return row
			})
		}, "MCC.cfg has no run record"},
		{"a record with no configuration", func(f tlcFixture) {
			if err := os.Remove(filepath.Join(f.root, "tla", "MCLone.cfg")); err != nil {
				f.t.Fatal(err)
			}
		}, "declared case MCLone.cfg has no configuration"},
		{"a record with no case and no configuration", func(f tlcFixture) {
			plan := strings.Replace(f.read("tla/CASES.tsv"), "MCLone.cfg\tMCLone.tla\tpass\t-\tcheck\tgamma\trequired\t-\n", "", 1)
			if err := os.Remove(filepath.Join(f.root, "tla", "MCLone.cfg")); err != nil {
				f.t.Fatal(err)
			}
			f.write("tla/CASES.tsv", plan)
		}, "unexpected or duplicate run record MCLone.cfg"},
		{"a module the plan names that is not there", func(f tlcFixture) {
			if err := os.Remove(filepath.Join(f.root, "tla", "MCLone.tla")); err != nil {
				f.t.Fatal(err)
			}
		}, "MCLone.cfg missing module MCLone.tla"},
		{"a module that extends a module that is not there", func(f tlcFixture) {
			f.write("tla/MCLone.tla", "---- MODULE MCLone ----\nEXTENDS Sequences, Nowhere\n====\n")
		}, "neither tla/Nowhere.tla nor one of TLC's standard modules"},
		{"a module that instantiates a module that is not there", func(f tlcFixture) {
			f.write("tla/MCLone.tla", "---- MODULE MCLone ----\nI(n) == INSTANCE Nowhere WITH x <- n\n====\n")
		}, "neither tla/Nowhere.tla nor one of TLC's standard modules"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newTLCFixture(t)
			tc.edit(f)
			found := false
			for _, problem := range tlcRecordProblems(f.root) {
				found = found || strings.Contains(problem, tc.want)
			}
			if !found {
				t.Fatalf("no problem names %q: %v", tc.want, tlcRecordProblems(f.root))
			}
		})
	}
}

// The reversed control of the per-case fingerprint. Every file the case reads
// is changed on its own, and each change makes the case stale: nothing the case
// reads is left out of the fingerprint. The list of what it reads is written
// here by hand and the fingerprint is worked out here from it, so neither
// drifts with internal/tlc.
func TestTLCEveryFileACaseReadsStalesIt(t *testing.T) {
	t.Parallel()
	f := newTLCFixture(t)
	src, err := tlcSource(f.root)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := src.Inputs("MCA.cfg")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, in := range inputs {
		got = append(got, in.Path)
	}
	want := []string{"internal/tlc/outcome.go", "internal/tlc/plan.go", "internal/tlc/run.go", "internal/tlc/suite.go", "tla/CASES.tsv#MCA.cfg", "tla/MCA.cfg", "tla/MCA.tla", "tla/Shared.tla"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("MCA.cfg reads %v, want %v", got, want)
	}
	var hand strings.Builder
	for _, p := range want {
		var raw string
		switch {
		case strings.HasPrefix(p, "tla/CASES.tsv#"):
			raw = strings.TrimSuffix(tlcFixturePlanHeader, "\n") + "\nMCA.cfg\tMCA.tla\tpass\t-\tcheck\talpha\trequired\t-\n"
		default:
			raw = f.read(p)
		}
		sum := sha256.Sum256([]byte(raw))
		hand.WriteString(p + "\x00" + hex.EncodeToString(sum[:]) + "\n")
	}
	handSum := sha256.Sum256([]byte(hand.String()))
	if fp, n, _ := src.Fingerprint("MCA.cfg"); fp != hex.EncodeToString(handSum[:]) || n != len(want) {
		t.Fatalf("fingerprint %s over %d files, worked out by hand %s over %d", fp, n, hex.EncodeToString(handSum[:]), len(want))
	}
	for _, path := range want {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			f := newTLCFixture(t)
			if strings.HasPrefix(path, "tla/CASES.tsv#") {
				f.write("tla/CASES.tsv", strings.Replace(f.read("tla/CASES.tsv"), "check\talpha", "ignore-terminal\talpha", 1))
			} else {
				f.write(path, f.read(path)+"\n\\* one more line\n")
			}
			stale, _ := f.stale()
			if !slices.Contains(stale, "MCA.cfg") {
				t.Fatalf("changing %s left MCA.cfg's record current", path)
			}
			// And nothing else: MCLone.cfg reads the runner and no other file
			// here, so only the runner stales it too.
			if slices.Contains(stale, "MCLone.cfg") != strings.HasPrefix(path, "internal/tlc/") {
				t.Fatalf("changing %s left MCLone.cfg's record wrong: stale %v", path, stale)
			}
		})
	}
}

// The host column holds a platform label the tool computes, from a closed list;
// a machine's name in the record file is refused, and the CPU count is a count.
func TestTLCRecordHostIsAPlatformLabel(t *testing.T) {
	t.Parallel()
	for name, tc2 := range map[string]struct {
		column, value string
		ok            bool
	}{
		"a platform":           {"host", "linux-arm64", true},
		"a machine name":       {"host", "build-host-7.example", false},
		"a short host name":    {"host", "bench", false},
		"an operating system":  {"host", "linux", false},
		"another OS":           {"host", "darwin-arm64", false},
		"an unlisted arch":     {"host", "linux-sparc", false},
		"a label with a space": {"host", "linux-amd64 ", false},
		"no cpus":              {"cpus", "0", false},
		"cpus that are text":   {"cpus", "many", false},
		"cpus":                 {"cpus", "64", true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newTLCFixture(t)
			f.seal(func(row []string) []string { row[tc[tc2.column]] = tc2.value; return row })
			problems := tlcRecordProblems(f.root)
			if tc2.ok != (len(problems) == 0) {
				t.Fatalf("%s=%q: problems %v", tc2.column, tc2.value, problems)
			}
		})
	}
}

// One jar measures the whole record file: a record on another jar is refused
// by the class test whatever its inputs say.
func TestTLCRecordFileHoldsOneJar(t *testing.T) {
	t.Parallel()
	f := newTLCFixture(t)
	if stale, other := f.stale(); len(stale)+len(other) != 0 {
		t.Fatalf("fixture refused: %v %v", stale, other)
	}
	f.seal(func(row []string) []string {
		if row[tc["config"]] == "MCB.cfg" {
			row[tc["jar_sha256"]] = strings.Repeat("b", 64)
		}
		return row
	})
	stale, other := f.stale()
	if len(stale) != 0 || len(other) != 1 || !strings.Contains(other[0], "hold 2 jars") || !strings.Contains(other[0], "aaaaaaaaaaaa (2 records)") || !strings.Contains(other[0], "bbbbbbbbbbbb (1 records)") {
		t.Fatalf("stale %v, other %v", stale, other)
	}
}

// The gates that are not about freshness: what the class test refuses about a
// record's result, budget and mode, and about a case's gate and debt.
func TestTLCRecordFreshnessAndCoverageWitnesses(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"none", "failed-record", "wrong-exit", "waived-required", "manual-required", "stale-debt", "declared-debt"} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()
			f := newTLCFixture(t)
			edit := func(row []string) []string { return row }
			switch mutation {
			case "failed-record":
				edit = func(row []string) []string { row[tc["result"]] = "FAIL"; return row }
			case "wrong-exit":
				edit = func(row []string) []string { row[tc["exit"]] = "124"; return row }
			case "manual-required":
				edit = func(row []string) []string { row[tc["mode"]] = "manual"; return row }
			case "waived-required":
				f.write("tla/CASES.tsv", strings.Replace(f.read("tla/CASES.tsv"), "required\t-\n", "bench\twaiver\n", 1))
			case "declared-debt", "stale-debt":
				f.write("tla/MCCardMachine.tla", "card model\n")
				f.write("tla/CASES.tsv", strings.Replace(f.read("tla/CASES.tsv"), "MCA.cfg\tMCA.tla\tpass\t-\tcheck\talpha\trequired\t-", "MCA.cfg\tMCCardMachine.tla\tpass\t-\tcheck\tcard\tbench\tlayer-2", 1))
				edit = func(row []string) []string {
					if row[tc["config"]] == "MCA.cfg" {
						row[tc["generated"]], row[tc["distinct"]], row[tc["seconds"]], row[tc["exit"]], row[tc["result"]] = "-", "-", "110.2", "124", "FAIL"
					}
					return row
				}
			}
			f.seal(edit)
			if mutation == "stale-debt" {
				f.write("tla/MCCardMachine.tla", "changed card\n")
			}
			problems := tlcRecordProblems(f.root)
			if (mutation == "none" || mutation == "declared-debt") && len(problems) != 0 {
				t.Fatalf("valid fixture refused: %v", problems)
			}
			if mutation != "none" && mutation != "declared-debt" && len(problems) == 0 {
				t.Fatalf("%s did not invalidate the run evidence", mutation)
			}
		})
	}
}
