package tlc

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const header = "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"

func row(config, module, expected, property, deadlock, group, gate, debt string) string {
	return strings.Join([]string{config, module, expected, property, deadlock, group, gate, debt}, "\t") + "\n"
}

func TestParseCasesReadsAValidPlan(t *testing.T) {
	t.Parallel()
	plan := header +
		row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-") +
		row("MCABroken.cfg", "MCA.tla", "invariant", "Safe|Other", "ignore-terminal", "alpha", "required", "-") +
		row("MCB.cfg", "MCB.tla", "temporal", "Ends", "check", "beta", "bench", "slow")
	cases, err := ParseCases(strings.NewReader(plan))
	if err != nil {
		t.Fatal(err)
	}
	want := []Case{
		{"MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"},
		{"MCABroken.cfg", "MCA.tla", "invariant", "Safe|Other", "ignore-terminal", "alpha", "required", "-"},
		{"MCB.cfg", "MCB.tla", "temporal", "Ends", "check", "beta", "bench", "slow"},
	}
	if !reflect.DeepEqual(cases, want) {
		t.Fatalf("cases = %v, want %v", cases, want)
	}
	if got := RequiredGroups(cases); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Fatalf("required groups = %v, want [alpha]", got)
	}
}

func TestParseCasesRefusesEachMalformedRow(t *testing.T) {
	t.Parallel()
	good := []string{"MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"}
	tests := []struct {
		name   string
		field  int
		value  string
		reason string
	}{
		{"module shape", 1, "A.tla", "invalid instance module"},
		{"module extension", 1, "MCA.txt", "invalid instance module"},
		{"expected", 2, "maybe", "invalid expected outcome"},
		{"deadlock", 4, "never", "invalid deadlock policy"},
		{"a pass with a property", 3, "Safe", "expected property required only for a counterexample"},
		{"gate", 6, "optional", "invalid gate/group"},
		{"group shape", 5, "Alpha", "invalid gate/group"},
		{"a required case waived as debt", 7, "later", "required case cannot be waived as debt"},
	}
	for _, tc := range tests {
		fields := append([]string(nil), good...)
		fields[tc.field] = tc.value
		_, err := ParseCases(strings.NewReader(header + strings.Join(fields, "\t") + "\n"))
		if err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: error = %v, want %q", tc.name, err, tc.reason)
		}
	}
	noProperty := append([]string(nil), good...)
	noProperty[2] = "invariant"
	if _, err := ParseCases(strings.NewReader(header + strings.Join(noProperty, "\t") + "\n")); err == nil {
		t.Error("a counterexample case with property - was accepted")
	}
	for name, text := range map[string]string{
		"empty":            "",
		"a missing column": "config\tmodule\n",
		"a short row":      header + "MCA.cfg\tMCA.tla\n",
	} {
		if _, err := ParseCases(strings.NewReader(text)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// tree writes a checkout's tla/ with the named files.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, "tla", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadCasesHoldsThePlanToTheFiles(t *testing.T) {
	t.Parallel()
	plan := header + row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-")
	base := map[string]string{"CASES.tsv": plan, "MCA.tla": "m\n", "MCA.cfg": "c\n"}
	if _, err := LoadCases(tree(t, base)); err != nil {
		t.Fatalf("a matching tree was refused: %v", err)
	}
	with := func(edit func(map[string]string)) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		edit(m)
		return m
	}
	for name, files := range map[string]map[string]string{
		"a configuration nobody declared": with(func(m map[string]string) { m["MCB.cfg"] = "c\n" }),
		"a declared configuration that is missing": with(func(m map[string]string) {
			m["CASES.tsv"] += row("MCC.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-")
		}),
		"a case declared twice": with(func(m map[string]string) {
			m["CASES.tsv"] += row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-")
		}),
		"a missing module": with(func(m map[string]string) { delete(m, "MCA.tla") }),
		"no plan":          with(func(m map[string]string) { delete(m, "CASES.tsv") }),
	} {
		if _, err := LoadCases(tree(t, files)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestSelectPicksAGroupOrAShard(t *testing.T) {
	t.Parallel()
	cases := []Case{{Config: "a", Group: "x"}, {Config: "b", Group: "y"}, {Config: "c", Group: "x"}, {Config: "d", Group: "y"}}
	names := func(cs []Case, err error) string {
		if err != nil {
			return "error: " + err.Error()
		}
		var out []string
		for _, c := range cs {
			out = append(out, c.Config)
		}
		return strings.Join(out, ",")
	}
	tests := []struct {
		group         string
		shards, shard int
		want          string
	}{
		{"", 1, 0, "a,b,c,d"},
		{"x", 1, 0, "a,c"},
		{"", 2, 0, "a,c"},
		{"", 2, 1, "b,d"},
		{"", 4, 3, "d"},
		{"z", 1, 0, "error: unknown group: z"},
		{"x", 2, 0, "error: --group and shard selection cannot be combined"},
		{"", 0, 0, "error: use a positive shard count and a zero-based shard below it"},
		{"", 2, 2, "error: use a positive shard count and a zero-based shard below it"},
		{"", 5, 0, "error: shard count exceeds the number of declared cases"},
	}
	for _, tc := range tests {
		if got := names(Select(cases, tc.group, tc.shards, tc.shard)); got != tc.want {
			t.Errorf("Select(group=%q shards=%d shard=%d) = %s, want %s", tc.group, tc.shards, tc.shard, got, tc.want)
		}
	}
}

// The repository's own plan is held to its files by the runner's refusals; the
// tests here run the same load over the real tla/.
func TestTheRepositoryPlanLoads(t *testing.T) {
	t.Parallel()
	cases, err := LoadCases(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("tla/CASES.tsv is refused: %v", err)
	}
	if len(cases) == 0 || len(RequiredGroups(cases)) == 0 {
		t.Fatal("the plan declares no case or no required group")
	}
}

func TestLoadCasesRefusesAConfigurationThatCannotRun(t *testing.T) {
	t.Parallel()
	plan := header + row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-")
	module := "---- MODULE MCA ----\nEXTENDS A, Naturals\n====\n"
	base := "---- MODULE A ----\nCONSTANTS Streams, \\* the rows\n          F(_), Broken\nVARIABLE x\n====\n"
	good := "\\* a comment naming Nothing = 1\nSPECIFICATION Spec\nCONSTANTS\n Streams = {a}\n F <- MCF\n Broken = \"none\"\nINVARIANT TypeOK\n"
	bad := "SPECIFICATION Spec\nCONSTANTS\n Streams = {a}\nINVARIANT Broken\n"
	files := map[string]string{"CASES.tsv": plan, "MCA.tla": module, "A.tla": base, "MCA.cfg": good}
	if _, err := LoadCases(tree(t, files)); err != nil {
		t.Fatalf("a configuration assigning every constant was refused: %v", err)
	}
	files["MCA.cfg"] = bad
	_, err := LoadCases(tree(t, files))
	if err == nil || !strings.Contains(err.Error(), "MCA.cfg leaves Broken, F unassigned") {
		t.Fatalf("a configuration leaving constants unassigned: got %v", err)
	}
}
