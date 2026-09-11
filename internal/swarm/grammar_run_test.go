package swarm

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// THE OUTPUT GRAMMAR IS A CONTRACT (SPEC-SWARM.md:566), and a line the tool prints that the
// grammar does not name is a line a reader parses by field and finds missing.
//
// DELTA READ 2, finding 2: the recovery path prints `RUN RECLAIM ... end=violation` and
// `end=budget-unverifiable`; the grammar enumerated `done|killed|failed|budget|unknown|
// unlaunched` and neither word was in it. The end on that line is the supervisor's own
// `exit.json` word or the one rule 11 writes over it, so the enumeration is the set of ends
// a job can END with -- not a subset somebody typed once.
//
// This test reads the grammar out of the spec and checks every RUN line the tool can print
// against it: the field names, in order, and every literal value against the enumeration
// the grammar gives that field.
func TestEveryRunLineParsesAgainstTheGrammar(t *testing.T) {
	grammar := readGrammar(t)
	for _, format := range runFormats(t) {
		key, fields, values := grammarFields(format)
		want, ok := grammar[key]
		if !ok {
			t.Errorf("%q is printed and the grammar does not name it (SPEC-SWARM.md, Output grammar)", key)
			continue
		}
		if !fieldsMatch(fields, want.fields) {
			t.Errorf("%s prints fields %v; the grammar names %v", key, fields, want.fields)
		}
		for field, value := range values {
			enum, has := want.enums[field]
			if !has || len(enum) == 0 {
				continue
			}
			if !enum[value] {
				t.Errorf("%s prints %s=%s; the grammar admits %v", key, field, value, keysOf(enum))
			}
		}
	}
}

// AND THE ENDS THEMSELVES: `RUN RECLAIM` carries whatever word the job ended with, which is
// every end the supervisor writes to exit.json, the one rule 11 writes over a done job, the
// absence of any record at all, and the reservation that never launched.
func TestTheReclaimLineNamesEveryEndAJobCanHave(t *testing.T) {
	want := readGrammar(t)["RUN RECLAIM"].enums["end"]
	for _, end := range []string{
		EndDone, EndKilled, EndFailed, EndBudget, EndUnverifiable, EndViolation, EndUnknown, "unlaunched",
	} {
		if !want[end] {
			t.Errorf("a job can end %q and RUN RECLAIM prints it; the grammar admits %v", end, keysOf(want))
		}
	}
}

type grammarLine struct {
	fields []string
	enums  map[string]map[string]bool
}

var (
	fieldRE  = regexp.MustCompile(`([a-z_]+)=`)
	enumRE   = regexp.MustCompile(`([a-z_]+)=<([a-z0-9|:<>\-]+)>`)
	formatRE = regexp.MustCompile(`"(RUN [A-Z][A-Z-]*[^"]*)"`)
	litRE    = regexp.MustCompile(`([a-z_]+)=([a-z][a-z0-9\-]*)`)
)

// readGrammar reads the fenced Output grammar block out of the spec, which is the one place
// the contract is written.
func readGrammar(t *testing.T) map[string]grammarLine {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatalf("the grammar is the spec's: %s", err)
	}
	out := map[string]grammarLine{}
	inBlock, seen := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, "## Output grammar"):
			seen = true
			continue
		case !seen:
			continue
		case strings.HasPrefix(line, "```"):
			if inBlock {
				return out
			}
			inBlock = true
			continue
		case !inBlock || !strings.HasPrefix(line, "RUN "):
			continue
		}
		key, fields, _ := grammarFields(line)
		enums := map[string]map[string]bool{}
		for _, m := range enumRE.FindAllStringSubmatch(line, -1) {
			set := map[string]bool{}
			for _, word := range strings.Split(m[2], "|") {
				set[word] = true
			}
			enums[m[1]] = set
		}
		out[key] = grammarLine{fields: fields, enums: enums}
	}
	t.Fatal("the spec has no Output grammar block")
	return nil
}

// runFormats is every RUN line the tool can print: the format strings of the packages that
// print them, tests excluded -- a line only a test prints is not a line the tool prints.
func runFormats(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{filepath.Join("..", "..", "internal", "swarm"), filepath.Join("..", "..", "cmd", "nova-swarm")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range formatRE.FindAllStringSubmatch(string(raw), -1) {
				out = append(out, m[1])
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no RUN line was found in the sources; this test would pass over anything")
	}
	return out
}

// grammarFields reads one line -- printed or specified -- into its key, its field names in
// order, and the literal values it prints for them.
func grammarFields(line string) (string, []string, map[string]string) {
	line = strings.TrimSuffix(strings.TrimSpace(strings.ReplaceAll(line, `\n`, "")), `\n`)
	words := strings.Fields(line)
	key := "RUN"
	if len(words) > 1 {
		key += " " + strings.TrimSuffix(words[1], ":")
	}
	body := strings.TrimSpace(strings.TrimPrefix(line, key))
	var fields []string
	for _, m := range fieldRE.FindAllStringSubmatch(body, -1) {
		fields = append(fields, m[1])
	}
	values := map[string]string{}
	for _, m := range litRE.FindAllStringSubmatch(body, -1) {
		values[m[1]] = m[2]
	}
	return key, fields, values
}

// fieldsMatch: the printed fields are the grammar's, in order. The grammar may name a
// trailing OPTIONAL field the format does not carry in its own string (`RUN DONE`'s
// bracketed `log=`, which is appended or is empty), so a shorter printed list is a prefix.
func fieldsMatch(got, want []string) bool {
	if len(got) > len(want) {
		return false
	}
	for i, f := range got {
		if want[i] != f {
			return false
		}
	}
	return true
}

func keysOf(set map[string]bool) []string {
	var out []string
	for k := range set {
		out = append(out, k)
	}
	return out
}
