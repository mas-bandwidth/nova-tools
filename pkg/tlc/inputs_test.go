package tlc

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleReferencesReadsEveryFormAndNothingElse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"one module", "---- MODULE A ----\nEXTENDS B\n====\n", []string{"B"}},
		{"a list", "EXTENDS Naturals, FiniteSets,Sequences\n", []string{"Naturals", "FiniteSets", "Sequences"}},
		{"a list over lines", "EXTENDS Naturals,\n         Foo,\n         Bar\nVARIABLE x\n", []string{"Naturals", "Foo", "Bar"}},
		{"instance", "INSTANCE M\n", []string{"M"}},
		{"instance with", "INSTANCE M WITH x <- 1, y <- 2\n", []string{"M"}},
		{"named instance", "Inner == INSTANCE M\n", []string{"M"}},
		{"parameterised instance", "Inner(n) == INSTANCE M WITH x <- n\n", []string{"M"}},
		{"local instance", "LOCAL INSTANCE M\n", []string{"M"}},
		{"in order, once each", "EXTENDS A\nX == INSTANCE B\nY == INSTANCE A\nEXTENDS C\n", []string{"A", "B", "C"}},
		{"a line comment", "\\* EXTENDS Hidden\nEXTENDS Shown\n", []string{"Shown"}},
		{"a block comment", "(* EXTENDS Hidden *)\nEXTENDS Shown\n", []string{"Shown"}},
		{"a nested comment", "(* outer (* INSTANCE Hidden *) INSTANCE AlsoHidden *)\nINSTANCE Shown\n", []string{"Shown"}},
		{"a comment across lines", "(*\nINSTANCE Hidden\n*) INSTANCE Shown\n", []string{"Shown"}},
		{"a string", "Msg == \"EXTENDS Hidden and INSTANCE Hidden\"\nEXTENDS Shown\n", []string{"Shown"}},
		{"a string with an escaped quote", "Msg == \"a \\\" INSTANCE Hidden\"\nINSTANCE Shown\n", []string{"Shown"}},
		{"after the end of the module", "EXTENDS A\n====\nEXTENDS Hidden\nINSTANCE Hidden\n", []string{"A"}},
		{"a longer word is not the keyword", "MYEXTENDS B\nNOINSTANCE C\n_INSTANCE D\n", nil},
		{"a nested module's end does not end the outer module", "---- MODULE Outer ----\nEXTENDS A\n---- MODULE Inner ----\nEXTENDS B\n====\nEXTENDS C\nINSTANCE D\n====\nEXTENDS Hidden\n", []string{"A", "B", "C", "D"}},
		{"the reader's example", "---- MODULE Outer ----\n---- MODULE Inner ----\n====\nEXTENDS C\n====\n", []string{"C"}},
		{"a module inside a module inside a module", "---- MODULE A ----\n---- MODULE B ----\n---- MODULE C ----\nEXTENDS X\n====\nEXTENDS Y\n====\nEXTENDS Z\n====\nEXTENDS Hidden\n", []string{"X", "Y", "Z"}},
		{"a module the same text declares is not a file", "---- MODULE Outer ----\n---- MODULE Inner ----\n====\nEXTENDS Inner, Real\nX == INSTANCE Inner\n====\n", []string{"Real"}},
		{"text before the first header is not read", "EXTENDS Hidden\n---- MODULE A ----\nEXTENDS Shown\n====\n", []string{"Shown"}},
		{"a header inside a comment opens nothing", "---- MODULE A ----\n(* ---- MODULE Fake ---- *)\nEXTENDS Shown\n====\nEXTENDS Hidden\n", []string{"Shown"}},
		{"nothing", "---- MODULE A ----\nVARIABLE x\n====\n", nil},
		{"an inner header split over two lines", "---- MODULE Outer ----\nEXTENDS A\n---- MODULE Inner\n----\nEXTENDS B\n====\nI == INSTANCE C\n====\n", []string{"A", "B", "C"}},
		{"an outer header split over two lines", "---- MODULE Outer\n----\nEXTENDS A\n====\nEXTENDS Hidden\n", []string{"A"}},
		{"a header with no closing dashes and text after the name", "---- MODULE Outer ----\n---- MODULE Inner EXTENDS B\n====\nEXTENDS C\n====\n", []string{"B", "C"}},
		{"a reference on the header's own line", "---- MODULE Outer ---- EXTENDS A\n====\n", []string{"A"}},
		{"a reference after the closing line's dashes", "---- MODULE Outer ----\n---- MODULE Inner ----\n==== INSTANCE C\n====\n", []string{"C"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ModuleReferences([]byte(tc.text))
			assert.Equal(t, tc.want, got, "%s: %v, want %v", tc.name, got, tc.want)
		})
	}
}

func inputsTree(t *testing.T) string {
	t.Helper()
	return tree(t, map[string]string{
		"CASES.tsv": header +
			row("MCTop.cfg", "MCTop.tla", "pass", "-", "check", "alpha", "required", "-") +
			row("MCTopBroken.cfg", "MCTop.tla", "invariant", "Safe", "check", "alpha", "required", "-") +
			row("MCLone.cfg", "MCLone.tla", "pass", "-", "check", "beta", "required", "-"),
		"MCTop.tla":       "EXTENDS Mid, Naturals\nInner == INSTANCE Leaf\n====\n",
		"Mid.tla":         "EXTENDS Shared, Sequences\n====\n",
		"Leaf.tla":        "EXTENDS Shared\n====\n",
		"Shared.tla":      "EXTENDS Naturals, Cyc\n====\n",
		"Cyc.tla":         "EXTENDS Shared\n====\n",
		"MCLone.tla":      "EXTENDS FiniteSets\n====\n",
		"Unread.tla":      "EXTENDS Shared\n====\n",
		"MCTop.cfg":       "SPECIFICATION Spec\n",
		"MCTopBroken.cfg": "SPECIFICATION Spec\n",
		"MCLone.cfg":      "SPECIFICATION Spec\n",
		"README.md":       "not an input\n",
	})
}

func paths(in []Input) []string {
	out := make([]string, len(in))
	for i, x := range in {
		out[i] = x.Path
	}
	return out
}

func testSource(t *testing.T, root string) Source {
	t.Helper()
	plan, err := os.ReadFile(filepath.Join(root, "tla", CasesFile))
	require.NoError(t, err)
	return Source{TLADir: filepath.Join(root, "tla"), Plan: plan, Runner: map[string][]byte{"pkg/tlc/run.go": []byte("runner\n")}}
}

// inputPaths is the sentence a resolved case repeats.
func inputPaths(t *testing.T, src Source, config string) []string {
	t.Helper()
	got, err := src.Inputs(config)
	require.NoError(t, err, "inputs %v, %v", paths(got), err)
	return paths(got)
}

func TestInputsAreTheCasesConfigurationItsModulesAndItsRow(t *testing.T) {
	t.Parallel()
	src := testSource(t, inputsTree(t))
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"MCTop.cfg", []string{
			"pkg/tlc/run.go", "tla/CASES.tsv#MCTop.cfg", "tla/Cyc.tla", "tla/Leaf.tla", "tla/MCTop.cfg",
			"tla/MCTop.tla", "tla/Mid.tla", "tla/Shared.tla",
		}},
		{"MCTopBroken.cfg", []string{
			"pkg/tlc/run.go", "tla/CASES.tsv#MCTopBroken.cfg", "tla/Cyc.tla", "tla/Leaf.tla",
			"tla/MCTop.tla", "tla/MCTopBroken.cfg", "tla/Mid.tla", "tla/Shared.tla",
		}},
		{"MCLone.cfg", []string{"pkg/tlc/run.go", "tla/CASES.tsv#MCLone.cfg", "tla/MCLone.cfg", "tla/MCLone.tla"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := inputPaths(t, src, tc.name)
			assert.Equal(t, tc.want, got, "inputs %v, want %v", got, tc.want)
		})
	}
}

func TestDigestIsThePathsAndHashesInOrder(t *testing.T) {
	t.Parallel()
	src := testSource(t, inputsTree(t))
	in, err := src.Inputs("MCLone.cfg")
	require.NoError(t, err)
	hash := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	rowText := "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\nMCLone.cfg\tMCLone.tla\tpass\t-\tcheck\tbeta\trequired\t-\n"
	text := "pkg/tlc/run.go\x00" + hash("runner\n") + "\n" +
		"tla/CASES.tsv#MCLone.cfg\x00" + hash(rowText) + "\n" +
		"tla/MCLone.cfg\x00" + hash("SPECIFICATION Spec\n") + "\n" +
		"tla/MCLone.tla\x00" + hash("EXTENDS FiniteSets\n====\n") + "\n"
	got := Digest(in)
	require.Equal(t, hash(text), got, "digest %s, by hand %s", got, hash(text))
	reversed := []Input{in[3], in[2], in[1], in[0]}
	require.Equal(t, Digest(in), Digest(reversed), "the digest depends on the order it is handed the inputs in")
	other := testSource(t, inputsTree(t))
	again, err := other.Inputs("MCLone.cfg")
	require.NoError(t, err)
	require.Equal(t, got, Digest(again), "the same files in another directory: %s and %s", got, Digest(again))
	fp, n, err := src.Fingerprint("MCLone.cfg")
	require.NoError(t, err, "fingerprint %s over %d files (%v)", fp, n, err)
	require.Equal(t, Digest(in), fp, "fingerprint %s over %d files (%v)", fp, n, err)
	require.Equal(t, 4, n, "fingerprint %s over %d files (%v)", fp, n, err)
}

func TestInputsRefuseACaseTheyCannotResolve(t *testing.T) {
	t.Parallel()
	base := func(t *testing.T, extra map[string]string) Source {
		t.Helper()
		files := map[string]string{
			"CASES.tsv": header +
				row("MCTop.cfg", "MCTop.tla", "pass", "-", "check", "alpha", "required", "-") +
				row("MCNoModule.cfg", "MCNoModule.tla", "pass", "-", "check", "alpha", "required", "-"),
			"MCTop.tla": "EXTENDS Naturals\n", "MCTop.cfg": "c\n", "MCNoModule.cfg": "c\n",
		}
		maps.Copy(files, extra)
		return testSource(t, tree(t, files))
	}
	for _, tc := range []struct {
		name, config, want string
		src                func(t *testing.T) Source
	}{
		{"a case not in the plan", "MCOther.cfg", "declares no case MCOther.cfg", func(t *testing.T) Source { return base(t, nil) }},
		{"a module the plan names that is not a file", "MCNoModule.cfg", "its module MCNoModule.tla cannot be read", func(t *testing.T) Source { return base(t, nil) }},
		{"a configuration that is not a file", "MCTop.cfg", "its configuration cannot be read", func(t *testing.T) Source {
			t.Helper()
			s := base(t, nil)
			require.NoError(t, os.Remove(filepath.Join(s.TLADir, "MCTop.cfg")))
			return s
		}},
		{"an extended module that is not a file and not standard", "MCTop.cfg", "module MCTop.tla names Nowhere, which is neither tla/Nowhere.tla nor one of TLC's standard modules", func(t *testing.T) Source {
			return base(t, map[string]string{"MCTop.tla": "EXTENDS Naturals, Nowhere\n"})
		}},
		{"an instantiated module that is not a file and not standard", "MCTop.cfg", "names Nowhere", func(t *testing.T) Source {
			return base(t, map[string]string{"MCTop.tla": "I == INSTANCE Nowhere\n"})
		}},
		{"a module two steps down that is not there", "MCTop.cfg", "module Mid.tla names Deep", func(t *testing.T) Source {
			return base(t, map[string]string{"MCTop.tla": "EXTENDS Mid\n", "Mid.tla": "EXTENDS Deep\n"})
		}},
		{"a module path", "MCTop.cfg", "not a file name in tla/", func(t *testing.T) Source {
			return base(t, map[string]string{"CASES.tsv": header + row("MCTop.cfg", "../MCTop.tla", "pass", "-", "check", "alpha", "required", "-")})
		}},
		{"a module that is not a tla file", "MCTop.cfg", "not a .tla file", func(t *testing.T) Source {
			return base(t, map[string]string{"CASES.tsv": header + row("MCTop.cfg", "MCTop.txt", "pass", "-", "check", "alpha", "required", "-")})
		}},
		{"a case named twice", "MCTop.cfg", "names MCTop.cfg twice", func(t *testing.T) Source {
			return base(t, map[string]string{"CASES.tsv": header + row("MCTop.cfg", "MCTop.tla", "pass", "-", "check", "alpha", "required", "-") + row("MCTop.cfg", "MCTop.tla", "pass", "-", "check", "alpha", "required", "-")})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.src(t).Inputs(tc.config)
			assert.ErrorContains(t, err, tc.want, "%s: %v, want %q", tc.name, err, tc.want)
		})
	}
}

func TestAFileUnderTlaShadowsAStandardModule(t *testing.T) {
	t.Parallel()
	got := inputPaths(t, testSource(t, tree(t, map[string]string{
		"CASES.tsv": header + row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"),
		"MCA.tla":   "EXTENDS Naturals\n", "MCA.cfg": "c\n", "Naturals.tla": "own\n",
	})), "MCA.cfg")
	require.Contains(t, got, "tla/Naturals.tla", "inputs %v, %v", got, got)
}

func TestEveryCaseOfTheRepositoryResolvesToItsOwnInputs(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	cases, err := LoadCases(root)
	require.NoError(t, err)
	src, err := SourceAt(root)
	require.NoError(t, err)
	reads := map[string][]string{}
	for _, c := range cases {
		in, err := src.Inputs(c.Config)
		if !assert.NoError(t, err, "%s: %v", c.Config, err) {
			continue
		}
		reads[c.Config] = paths(in)
		for _, need := range []string{"tla/" + c.Config, "tla/" + c.Module, CasesRowPath(c.Config), "pkg/tlc/run.go", "pkg/tlc/outcome.go", "pkg/tlc/suite.go"} {
			assert.Contains(t, reads[c.Config], need, "%s does not read %s", c.Config, need)
		}
	}
	for config, read := range reads {
		for _, name := range BookkeepingFiles {
			assert.False(t, slices.Contains(read, RunnerDir+"/"+name), "%s reads %s, a bookkeeping file: extending it must stale nothing", config, name)
		}
	}
	joined := func(config string) string { return strings.Join(reads[config], " ") }
	assert.NotContains(t, joined("MCFirstConn.cfg"), "MemberTable", "two models that share nothing read each other's files")
	assert.NotContains(t, joined("MCMemberTable.cfg"), "FirstConn", "two models that share nothing read each other's files")
}

func TestOnlyTheResultFilesOfTheRunnerAreInputs(t *testing.T) {
	t.Parallel()
	src := testSource(t, inputsTree(t))
	src.Runner = map[string][]byte{}
	for _, n := range ResultFiles {
		src.Runner[RunnerDir+"/"+n] = []byte(n + "\n")
	}
	before, _, err := src.Fingerprint("MCLone.cfg")
	require.NoError(t, err)
	built, err := RunnerFiles()
	require.NoError(t, err)
	require.Len(t, built, len(ResultFiles), "the binary carries %d runner files, %d are result files", len(built), len(ResultFiles))
	for name := range built {
		assert.True(t, slices.Contains(ResultFiles, filepath.Base(name)), "%s is embedded and is not a result file", name)
	}
	for _, n := range ResultFiles {
		other := testSource(t, inputsTree(t))
		other.Runner = maps.Clone(src.Runner)
		other.Runner[RunnerDir+"/"+n] = []byte("edited\n")
		after, _, err := other.Fingerprint("MCLone.cfg")
		require.NoError(t, err)
		assert.NotEqual(t, before, after, "editing the result file %s left the fingerprint unchanged", n)
	}
}

func TestUnknownModuleRefusalNamesTheNextAction(t *testing.T) {
	t.Parallel()
	_, err := testSource(t, tree(t, map[string]string{
		"CASES.tsv": header + row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"),
		"MCA.tla":   "EXTENDS Naturals, Nowhere\n", "MCA.cfg": "c\n",
	})).Inputs("MCA.cfg")
	require.Error(t, err, "refused nothing")
	for _, want := range []string{"neither tla/Nowhere.tla nor one of TLC's standard modules", "add the module file tla/Nowhere.tla", "add the name to standardModules in pkg/tlc/inputs.go"} {
		assert.ErrorContains(t, err, want, "%q lacks %q", err, want)
	}
	for _, name := range []string{"Bags", "FiniteSets", "Integers", "Naturals", "Randomization", "RealTime", "Reals", "Sequences", "TLC", "Toolbox"} {
		assert.True(t, standardModules[name], "%s is bundled by the jar and is not in standardModules", name)
	}
	assert.Len(t, standardModules, 10, "standardModules holds %d names; the jar bundles ten", len(standardModules))
}

func TestInputsFollowAModuleNamedAfterANestedModule(t *testing.T) {
	t.Parallel()
	got := inputPaths(t, testSource(t, tree(t, map[string]string{
		"CASES.tsv": header + row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"),
		"MCA.tla":   "---- MODULE MCA ----\n---- MODULE Inner ----\nEXTENDS Naturals\n====\nEXTENDS C\n====\n",
		"C.tla":     "EXTENDS Naturals\n====\n", "MCA.cfg": "c\n",
	})), "MCA.cfg")
	require.Contains(t, got, "tla/C.tla", "inputs %v, %v", got, got)
}

// referencesParsedCount is how many times the text has been parsed by
// cachedModuleReferences: 0 when it never was.
func referencesParsedCount(text []byte) int64 {
	if hit, ok := referencesCache.Load(sha256.Sum256(text)); ok {
		return hit.(*referencesEntry).parsed.Load()
	}
	return 0
}

func TestAModulesReferencesAreParsedOncePerText(t *testing.T) {
	t.Parallel()
	root := inputsTree(t)
	mark := "\\* " + t.Name() + " " + root + "\n"
	texts := map[string][]byte{}
	for _, name := range []string{"MCTop", "Mid", "Leaf", "Shared", "Cyc", "MCLone"} {
		path := filepath.Join(root, "tla", name+".tla")
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		texts[name] = append([]byte(mark+name+"\n"), raw...)
		require.NoError(t, os.WriteFile(path, texts[name], 0o644))
	}
	src := testSource(t, root)
	var first []string
	for round := 0; round < 3; round++ {
		for _, config := range []string{"MCTop.cfg", "MCTopBroken.cfg", "MCLone.cfg"} {
			in, err := src.Inputs(config)
			require.NoError(t, err)
			if config == "MCTop.cfg" && round == 0 {
				first = paths(in)
			}
		}
	}
	for name, text := range texts {
		got := referencesParsedCount(text)
		assert.Equal(t, int64(1), got, "%s was parsed %d times by nine fingerprints, want once", name, got)
	}
	edited := []byte(mark + "EXTENDS Shared, Unread\n====\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tla", "Leaf.tla"), edited, 0o644))
	for range 2 {
		in, err := src.Inputs("MCTop.cfg")
		require.NoError(t, err)
		got := paths(in)
		require.Contains(t, got, "tla/Unread.tla", "the edited module's new reference: before %v, after %v", first, got)
		require.NotContains(t, first, "tla/Unread.tla", "the edited module's new reference: before %v, after %v", first, got)
	}
	got := referencesParsedCount(edited)
	assert.Equal(t, int64(1), got, "the edited text was parsed %d times, want once", got)
	got = referencesParsedCount(texts["Leaf"])
	assert.Equal(t, int64(1), got, "the text before the edit was parsed %d times, want the once it was", got)
}
