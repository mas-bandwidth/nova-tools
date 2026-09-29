package tlc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestModuleReferencesReadsEveryFormAndNothingElse(t *testing.T) {
	t.Parallel()
	tests := []struct {
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
	}
	for _, tc := range tests {
		got := ModuleReferences([]byte(tc.text))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// inputsTree is a checkout tla/ with the shapes a case's inputs can take: a
// chain of extended modules, an instantiated module, a shared module reached
// twice, a standard module, and a module with a cycle in its references.
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
	var out []string
	for _, i := range in {
		out = append(out, i.Path)
	}
	return out
}

func testSource(t *testing.T, root string) Source {
	t.Helper()
	plan, err := os.ReadFile(filepath.Join(root, "tla", CasesFile))
	if err != nil {
		t.Fatal(err)
	}
	return Source{TLADir: filepath.Join(root, "tla"), Plan: plan, Runner: map[string][]byte{"internal/tlc/run.go": []byte("runner\n")}}
}

func TestInputsAreTheCasesConfigurationItsModulesAndItsRow(t *testing.T) {
	t.Parallel()
	src := testSource(t, inputsTree(t))
	got, err := src.Inputs("MCTop.cfg")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"internal/tlc/run.go", "tla/CASES.tsv#MCTop.cfg", "tla/Cyc.tla", "tla/Leaf.tla", "tla/MCTop.cfg",
		"tla/MCTop.tla", "tla/Mid.tla", "tla/Shared.tla",
	}
	if !reflect.DeepEqual(paths(got), want) {
		t.Fatalf("inputs %v, want %v", paths(got), want)
	}
	// Another case of the same module reads the same models and its own row.
	broken, err := src.Inputs("MCTopBroken.cfg")
	if err != nil {
		t.Fatal(err)
	}
	wantBroken := append([]string(nil), want...)
	wantBroken[1], wantBroken[4] = "tla/CASES.tsv#MCTopBroken.cfg", "tla/MCTopBroken.cfg"
	sort.Strings(wantBroken)
	if !reflect.DeepEqual(paths(broken), wantBroken) {
		t.Fatalf("inputs %v, want %v", paths(broken), wantBroken)
	}
	lone, err := src.Inputs("MCLone.cfg")
	if err != nil || !reflect.DeepEqual(paths(lone), []string{"internal/tlc/run.go", "tla/CASES.tsv#MCLone.cfg", "tla/MCLone.cfg", "tla/MCLone.tla"}) {
		t.Fatalf("inputs %v, %v", paths(lone), err)
	}
}

// The digest is over each input's path and the hash of its bytes, in path
// order. It is worked out here from that statement alone.
func TestDigestIsThePathsAndHashesInOrder(t *testing.T) {
	t.Parallel()
	src := testSource(t, inputsTree(t))
	in, err := src.Inputs("MCLone.cfg")
	if err != nil {
		t.Fatal(err)
	}
	hash := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	row := "config\tmodule\texpected\tproperty\tdeadlock\tgroup\tgate\tdebt\nMCLone.cfg\tMCLone.tla\tpass\t-\tcheck\tbeta\trequired\t-\n"
	text := "internal/tlc/run.go\x00" + hash("runner\n") + "\n" +
		"tla/CASES.tsv#MCLone.cfg\x00" + hash(row) + "\n" +
		"tla/MCLone.cfg\x00" + hash("SPECIFICATION Spec\n") + "\n" +
		"tla/MCLone.tla\x00" + hash("EXTENDS FiniteSets\n====\n") + "\n"
	if got := Digest(in); got != hash(text) {
		t.Fatalf("digest %s, by hand %s", got, hash(text))
	}
	// Order of the list does not change it, and nothing but path and bytes
	// does: the same tree in another directory has the same digest.
	reversed := []Input{in[3], in[2], in[1], in[0]}
	if Digest(reversed) != Digest(in) {
		t.Fatal("the digest depends on the order it is handed the inputs in")
	}
	other := testSource(t, inputsTree(t))
	if a, b := Digest(in), func() string { x, _ := other.Inputs("MCLone.cfg"); return Digest(x) }(); a != b {
		t.Fatalf("the same files in another directory: %s and %s", a, b)
	}
	if fp, n, err := src.Fingerprint("MCLone.cfg"); err != nil || fp != Digest(in) || n != 4 {
		t.Fatalf("fingerprint %s over %d files (%v)", fp, n, err)
	}
}

func TestInputsRefuseACaseTheyCannotResolve(t *testing.T) {
	t.Parallel()
	files := func(extra map[string]string) Source {
		base := map[string]string{
			"CASES.tsv": header +
				row("MCTop.cfg", "MCTop.tla", "pass", "-", "check", "alpha", "required", "-") +
				row("MCNoModule.cfg", "MCNoModule.tla", "pass", "-", "check", "alpha", "required", "-"),
			"MCTop.tla": "EXTENDS Naturals\n", "MCTop.cfg": "c\n", "MCNoModule.cfg": "c\n",
		}
		for k, v := range extra {
			base[k] = v
		}
		return testSource(t, tree(t, base))
	}
	tests := []struct {
		name   string
		src    Source
		config string
		want   string
	}{
		{"a case not in the plan", files(nil), "MCOther.cfg", "declares no case MCOther.cfg"},
		{"a module the plan names that is not a file", files(nil), "MCNoModule.cfg", "its module MCNoModule.tla cannot be read"},
		{"a configuration that is not a file", func() Source {
			s := files(nil)
			if err := os.Remove(filepath.Join(s.TLADir, "MCTop.cfg")); err != nil {
				t.Fatal(err)
			}
			return s
		}(), "MCTop.cfg", "its configuration cannot be read"},
		{"an extended module that is not a file and not standard", files(map[string]string{"MCTop.tla": "EXTENDS Naturals, Nowhere\n"}), "MCTop.cfg", "module MCTop.tla names Nowhere, which is neither tla/Nowhere.tla nor one of TLC's standard modules"},
		{"an instantiated module that is not a file and not standard", files(map[string]string{"MCTop.tla": "I == INSTANCE Nowhere\n"}), "MCTop.cfg", "names Nowhere"},
		{"a module two steps down that is not there", files(map[string]string{"MCTop.tla": "EXTENDS Mid\n", "Mid.tla": "EXTENDS Deep\n"}), "MCTop.cfg", "module Mid.tla names Deep"},
		{"a module path", files(map[string]string{"CASES.tsv": header + row("MCTop.cfg", "../MCTop.tla", "pass", "-", "check", "alpha", "required", "-")}), "MCTop.cfg", "not a file name in tla/"},
		{"a module that is not a tla file", files(map[string]string{"CASES.tsv": header + row("MCTop.cfg", "MCTop.txt", "pass", "-", "check", "alpha", "required", "-")}), "MCTop.cfg", "not a .tla file"},
		{"a case named twice", files(map[string]string{"CASES.tsv": header + row("MCTop.cfg", "MCTop.tla", "pass", "-", "check", "alpha", "required", "-") + row("MCTop.cfg", "MCTop.tla", "pass", "-", "check", "alpha", "required", "-")}), "MCTop.cfg", "names MCTop.cfg twice"},
	}
	for _, tc := range tests {
		if _, err := tc.src.Inputs(tc.config); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

// A module of the tree that has a standard module's name is the tree's.
func TestAFileUnderTlaShadowsAStandardModule(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"CASES.tsv": header + row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"),
		"MCA.tla":   "EXTENDS Naturals\n", "MCA.cfg": "c\n", "Naturals.tla": "own\n",
	})
	got, err := testSource(t, root).Inputs("MCA.cfg")
	if err != nil || !strings.Contains(strings.Join(paths(got), " "), "tla/Naturals.tla") {
		t.Fatalf("inputs %v, %v", paths(got), err)
	}
}

// The repository's own plan: every case resolves, every case reads the runner
// and its own row, and a case that shares no module with another reads none of
// the other's files.
func TestEveryCaseOfTheRepositoryResolvesToItsOwnInputs(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	cases, err := LoadCases(root)
	if err != nil {
		t.Fatal(err)
	}
	src, err := SourceAt(root)
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string][]string{}
	for _, c := range cases {
		in, err := src.Inputs(c.Config)
		if err != nil {
			t.Errorf("%s: %v", c.Config, err)
			continue
		}
		reads[c.Config] = paths(in)
		joined := " " + strings.Join(paths(in), " ") + " "
		for _, need := range []string{"tla/" + c.Config, "tla/" + c.Module, CasesRowPath(c.Config), "internal/tlc/run.go", "internal/tlc/outcome.go", "internal/tlc/suite.go"} {
			if !strings.Contains(joined, " "+need+" ") {
				t.Errorf("%s does not read %s", c.Config, need)
			}
		}
	}
	for config, read := range reads {
		for _, name := range BookkeepingFiles {
			if slicesContains(read, RunnerDir+"/"+name) {
				t.Errorf("%s reads %s, a bookkeeping file: extending it must stale nothing", config, name)
			}
		}
	}
	if strings.Contains(strings.Join(reads["MCFileLock.cfg"], " "), "MemberTable") || strings.Contains(strings.Join(reads["MCMemberTable.cfg"], " "), "FileLock") {
		t.Error("two models that share nothing read each other's files")
	}
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Only the result files are inputs: an edit to any bookkeeping file (the
// standard-module list is in one) stales no case.
func TestOnlyTheResultFilesOfTheRunnerAreInputs(t *testing.T) {
	t.Parallel()
	src := testSource(t, inputsTree(t))
	src.Runner = map[string][]byte{}
	for _, n := range ResultFiles {
		src.Runner[RunnerDir+"/"+n] = []byte(n + "\n")
	}
	before, _, err := src.Fingerprint("MCLone.cfg")
	if err != nil {
		t.Fatal(err)
	}
	built, err := RunnerFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(built) != len(ResultFiles) {
		t.Fatalf("the binary carries %d runner files, %d are result files", len(built), len(ResultFiles))
	}
	for name := range built {
		base := filepath.Base(name)
		if !slicesContains(ResultFiles, base) {
			t.Errorf("%s is embedded and is not a result file", name)
		}
	}
	for _, n := range ResultFiles {
		other := testSource(t, inputsTree(t))
		other.Runner = map[string][]byte{}
		for k, v := range src.Runner {
			other.Runner[k] = v
		}
		other.Runner[RunnerDir+"/"+n] = []byte("edited\n")
		if after, _, _ := other.Fingerprint("MCLone.cfg"); after == before {
			t.Errorf("editing the result file %s left the fingerprint unchanged", n)
		}
	}
}

// The refusal for a module that is neither a file nor standard names both ways
// out, and the standard list holds what the pinned jar bundles.
func TestUnknownModuleRefusalNamesTheNextAction(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"CASES.tsv": header + row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"),
		"MCA.tla":   "EXTENDS Naturals, Nowhere\n", "MCA.cfg": "c\n",
	})
	_, err := testSource(t, root).Inputs("MCA.cfg")
	if err == nil {
		t.Fatal("refused nothing")
	}
	for _, want := range []string{"neither tla/Nowhere.tla nor one of TLC's standard modules", "add the module file tla/Nowhere.tla", "add the name to standardModules in internal/tlc/inputs.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	for _, name := range []string{"Bags", "FiniteSets", "Integers", "Naturals", "Randomization", "RealTime", "Reals", "Sequences", "TLC", "Toolbox"} {
		if !standardModules[name] {
			t.Errorf("%s is bundled by the jar and is not in standardModules", name)
		}
	}
	if len(standardModules) != 10 {
		t.Errorf("standardModules holds %d names; the jar bundles ten", len(standardModules))
	}
}

// A module reached through a nested module's closing line is still followed to
// its file: the case reads what the outer module extends after the inner one.
func TestInputsFollowAModuleNamedAfterANestedModule(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"CASES.tsv": header + row("MCA.cfg", "MCA.tla", "pass", "-", "check", "alpha", "required", "-"),
		"MCA.tla":   "---- MODULE MCA ----\n---- MODULE Inner ----\nEXTENDS Naturals\n====\nEXTENDS C\n====\n",
		"C.tla":     "EXTENDS Naturals\n====\n", "MCA.cfg": "c\n",
	})
	got, err := testSource(t, root).Inputs("MCA.cfg")
	if err != nil || !slicesContains(paths(got), "tla/C.tla") {
		t.Fatalf("inputs %v, %v", paths(got), err)
	}
}

// A module's references are parsed once per distinct text, however many cases
// read it and however many times a case is fingerprinted, and an edited module
// is parsed again. The counts are per text, so the test holds with the others
// running beside it.
func TestAModulesReferencesAreParsedOncePerText(t *testing.T) {
	t.Parallel()
	root := inputsTree(t)
	// Texts no other test has parsed, so the cache holds none of them yet.
	mark := "\\* " + t.Name() + " " + root + "\n"
	texts := map[string][]byte{}
	for _, name := range []string{"MCTop", "Mid", "Leaf", "Shared", "Cyc", "MCLone"} {
		path := filepath.Join(root, "tla", name+".tla")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		texts[name] = append([]byte(mark+name+"\n"), raw...)
		if err := os.WriteFile(path, texts[name], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src := testSource(t, root)
	var first []string
	for round := 0; round < 3; round++ {
		for _, config := range []string{"MCTop.cfg", "MCTopBroken.cfg", "MCLone.cfg"} {
			in, err := src.Inputs(config)
			if err != nil {
				t.Fatal(err)
			}
			if config == "MCTop.cfg" && round == 0 {
				first = paths(in)
			}
		}
	}
	for name, text := range texts {
		if got := referencesParsedCount(text); got != 1 {
			t.Errorf("%s was parsed %d times by nine fingerprints, want once", name, got)
		}
	}
	// An edit that adds a reference is seen at once, and the edited text is
	// parsed once, as is the module it newly names.
	edited := []byte(mark + "EXTENDS Shared, Unread\n====\n")
	if err := os.WriteFile(filepath.Join(root, "tla", "Leaf.tla"), edited, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		in, err := src.Inputs("MCTop.cfg")
		if err != nil {
			t.Fatal(err)
		}
		if !slicesContains(paths(in), "tla/Unread.tla") || slicesContains(first, "tla/Unread.tla") {
			t.Fatalf("the edited module's new reference: before %v, after %v", first, paths(in))
		}
	}
	if got := referencesParsedCount(edited); got != 1 {
		t.Errorf("the edited text was parsed %d times, want once", got)
	}
	if got := referencesParsedCount(texts["Leaf"]); got != 1 {
		t.Errorf("the text before the edit was parsed %d times, want the once it was", got)
	}
}
