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
// the case's own row of the plan, and the runner (internal/tlc's non-test
// files), so changing a dependency or the interpretation of a result invalidates
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
	for _, need := range []string{"tla/MCEpochMemberFixedPoint.cfg", "tla/MCEpochMemberTable.tla", "tla/EpochMemberTable.tla", "tla/MemberTable.tla", "tla/TableMachine.tla", "tla/CASES.tsv#MCEpochMemberFixedPoint.cfg", "internal/tlc/inputs.go"} {
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

var tlcRunHeader = []string{"config", "module", "input_sha256", "input_files", "jar_sha256", "host", "started_utc", "generated", "distinct", "seconds", "exit", "result", "expected", "property", "budget", "mode"}

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
// the plan under tla/, and the runner's files from the checkout (the bytes of
// internal/tlc's non-test Go files; the tests are not part of how a result is
// read).
func tlcSource(root string) (tlc.Source, error) {
	plan, err := os.ReadFile(filepath.Join(root, "tla", "CASES.tsv"))
	if err != nil {
		return tlc.Source{}, err
	}
	matches, err := filepath.Glob(filepath.Join(root, "internal", "tlc", "*.go"))
	if err != nil {
		return tlc.Source{}, err
	}
	runner := map[string][]byte{}
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(m)
		if err != nil {
			return tlc.Source{}, err
		}
		runner["internal/tlc/"+filepath.Base(m)] = raw
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
		bench := row[1] == "MCCardMachine.tla" || row[1] == "MCLandWatch.tla" || row[1] == "MCTableMachine.tla" || row[1] == "MCFileLock.tla"
		if row[6] != "required" && row[6] != "bench" || row[6] == "bench" && !bench {
			bad("TLC %s cannot leave the required model gate", row[0])
		}
		if row[7] == "" || row[7] != "-" && (row[6] != "bench" || row[1] != "MCCardMachine.tla" && row[1] != "MCLandWatch.tla") {
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
		name := row[0]
		p, ok := wanted[name]
		if !ok || seen[name] {
			bad("TLC unexpected or duplicate run record %s", name)
			continue
		}
		seen[name] = true
		if row[1] != p[1] || row[12] != p[2] || row[13] != p[3] {
			bad("TLC %s record disagrees with its declared module or outcome", name)
		}
		if want, files, err := src.Fingerprint(name); err == nil {
			if row[2] != want || row[3] != strconv.Itoa(files) {
				bad("TLC %s record is stale: it was measured on inputs %s (%d files) and the case now reads %s (%d files): %s; run its group on a Linux bench (`tlacheck inputs --case %s` prints each file and its hash)",
					name, row[2], atoiOrZero(row[3]), want, files, tlcInputList(src, name), name)
			}
		}
		jar, err := hex.DecodeString(row[4])
		if err != nil || len(jar) != sha256.Size || strings.TrimSpace(row[5]) == "" {
			bad("TLC %s is missing jar identity or bench provenance", name)
		}
		if _, err := time.Parse(time.RFC3339Nano, row[6]); err != nil {
			bad("TLC %s has no valid run timestamp", name)
		}
		generated, eg := strconv.ParseUint(row[7], 10, 64)
		distinct, ed := strconv.ParseUint(row[8], 10, 64)
		seconds, es := strconv.ParseFloat(row[9], 64)
		budget, eb := strconv.ParseFloat(row[14], 64)
		bounded := row[15] == "bounded"
		if eb != nil || math.IsNaN(budget) || math.IsInf(budget, 0) || budget <= 0 || budget > 3600 || bounded && budget > 110 || !bounded && row[15] != "manual" || p[6] == "required" && !bounded {
			bad("TLC %s invalid run budget or mode", name)
		}
		if es != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > budget+2 {
			bad("TLC %s invalid elapsed time or exceeded its recorded budget", name)
		}
		debtFailure := p[6] == "bench" && p[7] != "-" && row[11] == "FAIL"
		if !debtFailure && (eg != nil || ed != nil || distinct == 0 || generated < distinct || bounded && seconds > 110) {
			bad("TLC %s has invalid state counts or exceeded its 110-second budget", name)
		}
		if debtFailure {
			if _, err := strconv.Atoi(row[10]); err != nil {
				bad("TLC %s debt record has invalid exit", name)
			}
			continue // retained failed measurement, explicitly not a passing proof
		}
		code := map[string]string{"pass": "0", "invariant": "12", "action": "13", "temporal": "13"}[p[2]]
		if code == "" || row[10] != code || row[11] != "PASS" {
			bad("TLC %s did not reach its declared result; timeout and generic failure are not evidence", name)
		}
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
	f.write("internal/tlc/run.go", "sample runner\n")
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
		row := []string{p[0], p[1], fp, strconv.Itoa(files), strings.Repeat("a", 64), "fixture-bench", "2026-01-01T00:00:00Z", "10", "5", "1.25", "0", "PASS", p[2], p[3], "110", "bounded"}
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
		{"the runner's tests edited", func(f tlcFixture) { f.write("internal/tlc/run_test.go", "edited\n") }, nil},
		{"a file that is not an input edited", func(f tlcFixture) { f.write("tla/README.md", "edited\n") }, nil},
		{"a module nobody extends added", func(f tlcFixture) { f.write("tla/Unused.tla", "---- MODULE Unused ----\n====\n") }, nil},
		{"a module named only in a comment added", func(f tlcFixture) {
			f.write("tla/Ignored.tla", "---- MODULE Ignored ----\n====\n")
		}, nil},
		{"a record with the wrong count of files", func(f tlcFixture) {
			f.seal(func(row []string) []string {
				if row[0] == "MCLone.cfg" {
					row[3] = "99"
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
		for _, want := range []string{"TLC " + config + " record is stale", "tla/Shared.tla", "tla/" + config, "tlacheck inputs --case " + config} {
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
				if row[0] == "MCB.cfg" {
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
				if row[0] == "MCC.cfg" {
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
	want := []string{"internal/tlc/run.go", "tla/CASES.tsv#MCA.cfg", "tla/MCA.cfg", "tla/MCA.tla", "tla/Shared.tla"}
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
				edit = func(row []string) []string { row[11] = "FAIL"; return row }
			case "wrong-exit":
				edit = func(row []string) []string { row[10] = "124"; return row }
			case "manual-required":
				edit = func(row []string) []string { row[15] = "manual"; return row }
			case "waived-required":
				f.write("tla/CASES.tsv", strings.Replace(f.read("tla/CASES.tsv"), "required\t-\n", "bench\twaiver\n", 1))
			case "declared-debt", "stale-debt":
				f.write("tla/MCCardMachine.tla", "card model\n")
				f.write("tla/CASES.tsv", strings.Replace(f.read("tla/CASES.tsv"), "MCA.cfg\tMCA.tla\tpass\t-\tcheck\talpha\trequired\t-", "MCA.cfg\tMCCardMachine.tla\tpass\t-\tcheck\tcard\tbench\tlayer-2", 1))
				edit = func(row []string) []string {
					if row[0] == "MCA.cfg" {
						row[7], row[8], row[9], row[10], row[11] = "-", "-", "110.2", "124", "FAIL"
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
