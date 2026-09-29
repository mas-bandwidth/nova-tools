package ci

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// Model changes require new measured evidence, independent of checkout mtimes.
// The fingerprint includes every model, configuration, case declaration and the
// runner (internal/tlc's non-test files), so changing a dependency or the
// interpretation of a result invalidates the old records too. No JVM or network
// runs in this class test.
func TestTLCRecordsCoverCurrentModels(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	for _, problem := range tlcRecordProblems(root) {
		t.Error(problem)
	}
	// The runner that writes the records and this class test must compute the
	// same fingerprint of the same files: a record the runner wrote is stale only
	// because an input changed, never because the two disagree on which files an
	// input is. The runner reads its own files from the bytes it was built with.
	want, err := tlcInputFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := tlc.Fingerprint(root); err != nil || got != want {
		t.Errorf("internal/tlc computes fingerprint %s (%v), this class test computes %s: the runner and the class disagree on the inputs", got, err, want)
	}
	plan, err := readTLCTSV(filepath.Join(root, "tla", "CASES.tsv"), []string{"config", "module", "expected", "property", "deadlock", "group", "gate", "debt"})
	if err != nil {
		t.Fatal(err)
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
	for _, module := range []string{"MCMemberTable.tla", "MCEpochMemberTable.tla", "MCTableEdit.tla", "MCTableOrder.tla", "MCTableSession.tla", "MCTableFirstContact.tla", "MCRedisFn.tla", "MCFirstConn.tla", "MCFunctionalRun.tla"} {
		if !required[module] {
			t.Errorf("TLC required model %s disappeared from the gate", module)
		}
	}
}

var tlcRunHeader = []string{"config", "module", "input_sha256", "jar_sha256", "host", "started_utc", "generated", "distinct", "seconds", "exit", "result", "expected", "property", "budget", "mode"}

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

func tlcInputFingerprint(root string) (string, error) {
	paths := []string{filepath.Join(root, "tla", "CASES.tsv")}
	for _, pattern := range []string{"tla/*.tla", "tla/MC*.cfg", "internal/tlc/*.go"} {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return "", err
		}
		for _, m := range matches {
			// The runner is internal/tlc's own files; its tests are not part of
			// how a result is read.
			if !strings.HasSuffix(m, "_test.go") {
				paths = append(paths, m)
			}
		}
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		h.Write(raw)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func tlcRecordProblems(root string) []string {
	var problems []string
	bad := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	fingerprint, err := tlcInputFingerprint(root)
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
		if row[1] != p[1] || row[11] != p[2] || row[12] != p[3] {
			bad("TLC %s record disagrees with its declared module or outcome", name)
		}
		if row[2] != fingerprint {
			bad("TLC %s record is stale; model/configuration/runner inputs changed: run make tlc on a Linux bench", name)
		}
		jar, err := hex.DecodeString(row[3])
		if err != nil || len(jar) != sha256.Size || strings.TrimSpace(row[4]) == "" {
			bad("TLC %s is missing jar identity or bench provenance", name)
		}
		if _, err := time.Parse(time.RFC3339Nano, row[5]); err != nil {
			bad("TLC %s has no valid run timestamp", name)
		}
		generated, eg := strconv.ParseUint(row[6], 10, 64)
		distinct, ed := strconv.ParseUint(row[7], 10, 64)
		seconds, es := strconv.ParseFloat(row[8], 64)
		budget, eb := strconv.ParseFloat(row[13], 64)
		bounded := row[14] == "bounded"
		if eb != nil || math.IsNaN(budget) || math.IsInf(budget, 0) || budget <= 0 || budget > 3600 || bounded && budget > 110 || !bounded && row[14] != "manual" || p[6] == "required" && !bounded {
			bad("TLC %s invalid run budget or mode", name)
		}
		if es != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > budget+2 {
			bad("TLC %s invalid elapsed time or exceeded its recorded budget", name)
		}
		debtFailure := p[6] == "bench" && p[7] != "-" && row[10] == "FAIL"
		if !debtFailure && (eg != nil || ed != nil || distinct == 0 || generated < distinct || bounded && seconds > 110) {
			bad("TLC %s has invalid state counts or exceeded its 110-second budget", name)
		}
		if debtFailure {
			if _, err := strconv.Atoi(row[9]); err != nil {
				bad("TLC %s debt record has invalid exit", name)
			}
			continue // retained failed measurement, explicitly not a passing proof
		}
		code := map[string]string{"pass": "0", "invariant": "12", "action": "13", "temporal": "13"}[p[2]]
		if code == "" || row[9] != code || row[10] != "PASS" {
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

func TestTLCRecordFreshnessAndCoverageWitnesses(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"none", "model", "config", "runner", "new-case", "missing-record", "failed-record", "wrong-exit", "waived-required", "manual-required", "stale-debt", "declared-debt"} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			write := func(name, contents string) {
				t.Helper()
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("tla/MCSample.tla", "sample model\n")
			write("tla/MCSample.cfg", "sample configuration\n")
			write("internal/tlc/run.go", "sample runner\n")
			write("tla/CASES.tsv", "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\nMCSample.cfg\tMCSample.tla\tpass\t-\tcheck\tsample\trequired\t-\n")
			fingerprint, err := tlcInputFingerprint(root)
			if err != nil {
				t.Fatal(err)
			}
			row := []string{"MCSample.cfg", "MCSample.tla", fingerprint, strings.Repeat("a", 64), "fixture-bench", "2026-01-01T00:00:00Z", "10", "5", "1.25", "0", "PASS", "pass", "-", "110", "bounded"}
			switch mutation {
			case "model":
				write("tla/MCSample.tla", "changed model\n")
			case "config":
				write("tla/MCSample.cfg", "changed bounds\n")
			case "runner":
				write("internal/tlc/run.go", "changed interpretation\n")
			case "new-case":
				write("tla/MCAdded.cfg", "new configuration\n")
			case "failed-record":
				row[10] = "FAIL"
			case "wrong-exit":
				row[9] = "124"
			case "manual-required":
				row[14] = "manual"
			case "waived-required":
				write("tla/CASES.tsv", "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\nMCSample.cfg\tMCSample.tla\tpass\t-\tcheck\tsample\tbench\twaiver\n")
				row[2], _ = tlcInputFingerprint(root)
			case "declared-debt", "stale-debt":
				write("tla/MCCardMachine.tla", "card model\n")
				write("tla/CASES.tsv", "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\nMCSample.cfg\tMCCardMachine.tla\tpass\t-\tcheck\tcard\tbench\tlayer-2\n")
				row[1], row[6], row[7], row[8], row[9], row[10] = "MCCardMachine.tla", "-", "-", "110.2", "124", "FAIL"
				row[2], _ = tlcInputFingerprint(root)
				if mutation == "stale-debt" {
					write("tla/MCCardMachine.tla", "changed card\n")
				}
			}
			record := strings.Join(tlcRunHeader, "\t") + "\n"
			if mutation != "missing-record" {
				record += strings.Join(row, "\t") + "\n"
			}
			write("tla/RUNS.tsv", record)
			problems := tlcRecordProblems(root)
			if (mutation == "none" || mutation == "declared-debt") && len(problems) != 0 {
				t.Fatalf("valid fixture refused: %v", problems)
			}
			if mutation != "none" && mutation != "declared-debt" && len(problems) == 0 {
				t.Fatalf("%s did not invalidate the run evidence", mutation)
			}
		})
	}
}
