package tlc

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const header = "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\n"

func row(config, module, expected, property, deadlock, group, gate, debt string) string {
	return strings.Join([]string{config, module, expected, property, deadlock, group, gate, debt}, "\t") + "\n"
}

func refused(t *testing.T, err error, name, phrase string) {
	t.Helper()
	if phrase == "" {
		assert.Error(t, err, "%s was accepted", name)
		return
	}
	assert.ErrorContains(t, err, phrase, "%s: %v, want %q", name, err, phrase)
}

func TestParseCasesReadsAValidPlan(t *testing.T) {
	t.Parallel()
	plan := header +
		row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-") +
		row("MCABroken.cfg", "MCA.tla", "invariant", "Safe|Other", "ignore-terminal", "alpha", "required", "-") +
		row("MCB.cfg", "MCB.tla", "temporal", "Ends", "check", "beta", "bench", "slow")
	cases, err := ParseCases(strings.NewReader(plan))
	require.NoError(t, err)
	want := []Case{
		{"MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"},
		{"MCABroken.cfg", "MCA.tla", "invariant", "Safe|Other", "ignore-terminal", "alpha", "required", "-"},
		{"MCB.cfg", "MCB.tla", "temporal", "Ends", "check", "beta", "bench", "slow"},
	}
	require.Equal(t, want, cases, "cases = %v, want %v", cases, want)
	got := RequiredGroups(cases)
	require.Equal(t, []string{"alpha"}, got, "required groups = %v, want [alpha]", got)
}

func TestParseCasesRefusesEachMalformedRow(t *testing.T) {
	t.Parallel()
	good := []string{"MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"}
	line := func(field int, value string) string {
		fields := append([]string(nil), good...)
		fields[field] = value
		return header + strings.Join(fields, "\t") + "\n"
	}
	for _, tc := range []struct {
		name, text, reason string
	}{
		{"module shape", line(1, "A.tla"), "invalid instance module"},
		{"module extension", line(1, "MCA.txt"), "invalid instance module"},
		{"expected", line(2, "maybe"), "invalid expected outcome"},
		{"deadlock", line(4, "never"), "invalid deadlock policy"},
		{"a pass with a property", line(3, "Safe"), "expected property required only for a counterexample"},
		{"gate", line(6, "optional"), "invalid gate/group"},
		{"group shape", line(5, "Alpha"), "invalid gate/group"},
		{"a required case waived as debt", line(7, "later"), "required case cannot be waived as debt"},
		{"a counterexample case with property -", line(2, "invariant"), ""},
		{"empty", "", ""},
		{"a missing column", "config\tmodule\n", ""},
		{"a short row", header + "MCA.cfg\tMCA.tla\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCases(strings.NewReader(tc.text))
			refused(t, err, tc.name, tc.reason)
		})
	}
}

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	rooted := make(map[string]string, len(files))
	for name, body := range files {
		rooted["tla/"+name] = body
	}
	return testkit.Tree(t, t.TempDir(), rooted)
}

func TestLoadCasesHoldsThePlanToTheFiles(t *testing.T) {
	t.Parallel()
	plan := header + row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-")
	base := map[string]string{"CASES.tsv": plan, "MCA.tla": "m\n", "MCA.cfg": "c\n"}
	_, err := LoadCases(tree(t, base))
	require.NoError(t, err, "a matching tree was refused")
	for _, tc := range []struct {
		name string
		edit func(map[string]string)
	}{
		{"a configuration nobody declared", func(m map[string]string) { m["MCB.cfg"] = "c\n" }},
		{"a declared configuration that is missing", func(m map[string]string) {
			m["CASES.tsv"] += row("MCC.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-")
		}},
		{"a case declared twice", func(m map[string]string) {
			m["CASES.tsv"] += row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-")
		}},
		{"a missing module", func(m map[string]string) { delete(m, "MCA.tla") }},
		{"no plan", func(m map[string]string) { delete(m, "CASES.tsv") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := maps.Clone(base)
			tc.edit(m)
			_, err := LoadCases(tree(t, m))
			refused(t, err, tc.name, "")
		})
	}
}

func TestSelectPicksAGroupOrAShard(t *testing.T) {
	t.Parallel()
	cases := []Case{{Config: "a", Group: "x"}, {Config: "b", Group: "y"}, {Config: "c", Group: "x"}, {Config: "d", Group: "y"}}
	names := func(cs []Case, err error) string {
		if err != nil {
			return "error: " + err.Error()
		}
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.Config
		}
		return strings.Join(out, ",")
	}
	for _, tc := range []struct {
		name          string
		group         string
		shards, shard int
		want          string
	}{
		{"all", "", 1, 0, "a,b,c,d"},
		{"group x", "x", 1, 0, "a,c"},
		{"shard 0 of 2", "", 2, 0, "a,c"},
		{"shard 1 of 2", "", 2, 1, "b,d"},
		{"shard 3 of 4", "", 4, 3, "d"},
		{"unknown group", "z", 1, 0, "error: unknown group: z"},
		{"group x shard 0 of 2", "x", 2, 0, "a"},
		{"group x shard 1 of 2", "x", 2, 1, "c"},
		{"too many shards for the group", "x", 3, 0, "error: shard count exceeds the 2 cases of group x"},
		{"a zero shard count", "", 0, 0, "error: use a positive shard count and a zero-based shard below it"},
		{"a shard past the count", "", 2, 2, "error: use a positive shard count and a zero-based shard below it"},
		{"more shards than cases", "", 5, 0, "error: shard count exceeds the number of declared cases"},
	} {
		got := names(Select(cases, tc.group, tc.shards, tc.shard))
		assert.Equal(t, tc.want, got, "%s: Select(group=%q shards=%d shard=%d) = %s, want %s", tc.name, tc.group, tc.shards, tc.shard, got, tc.want)
	}
}

func TestTheRepositoryPlanLoads(t *testing.T) {
	t.Parallel()
	cases, err := LoadCases(filepath.Join("..", ".."))
	require.NoError(t, err, "tla/CASES.tsv is refused")
	require.NotEmpty(t, cases, "the plan declares no case or no required group")
	require.NotEmpty(t, RequiredGroups(cases), "the plan declares no case or no required group")
}
