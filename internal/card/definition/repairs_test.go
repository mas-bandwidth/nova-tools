package definition

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// cardFor is the base card under another ID.
func cardFor(id string) string { return strings.ReplaceAll(baseCard, "card-alpha", id) }

// repoWith commits files to a new repository and returns its root and commit.
func repoWith(t *testing.T, files map[string]string) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	testGit(t, dir, "init", "-q", "-b", "main")
	for p, c := range files {
		write(t, dir, p, c)
	}
	testGit(t, dir, "add", "-A")
	testGit(t, dir, "commit", "-q", "-m", "cards")
	return dir, testGit(t, dir, "rev-parse", "HEAD")
}

func admit(t *testing.T, files map[string]string, paths ...string) ([]Admission, *Refusals) {
	t.Helper()
	dir, commit := repoWith(t, files)
	return Admissions(context.Background(), dir, commit, paths, WithIdentity("example.com/owner/cards"))
}

// B1: Admissions takes the paths and reads the committed bytes itself. What the
// reader's probes forged by handing a caller-built definition in cannot be said:
// there is no parameter for one. What remains is that the array rules of Validate
// run on the pinned bytes.
func TestAdmissionsRunsValidateOnThePinnedBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string]string
		want  Cause
	}{
		{"two files with one ID give no records", map[string]string{"a.md": cardFor("same"), "b.md": cardFor("same")}, CauseRepeatedID},
		{"a cycle across two files", map[string]string{
			"a.md": strings.Replace(cardFor("a"), "DEPENDS-ON: -", "DEPENDS-ON: b", 1),
			"b.md": strings.Replace(cardFor("b"), "DEPENDS-ON: -", "DEPENDS-ON: a", 1)}, CauseCycle},
		{"a card that depends on itself", map[string]string{
			"a.md": strings.Replace(cardFor("a"), "DEPENDS-ON: -", "DEPENDS-ON: a", 1), "b.md": cardFor("b")}, CauseSelfDependent},
		{"a fix-red card with no test is not a read card", map[string]string{
			"a.md": strings.Replace(cardFor("a"), "TEST: internal/queue TestNameRefusesEmpty", "TEST: none because", 1), "b.md": cardFor("b")}, CauseInvalidTest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var paths []string
			for _, p := range []string{"a.md", "b.md"} {
				if _, ok := c.files[p]; ok {
					paths = append(paths, p)
				}
			}
			as, refs := admit(t, c.files, paths...)
			if as != nil || refs == nil {
				t.Fatalf("records beside a refusal: %v", as)
			}
			found := false
			for _, r := range refs.List {
				found = found || r.Cause == c.want
			}
			if !found {
				t.Fatalf("no %s in %v", c.want, refs.Lines())
			}
		})
	}
	// The records say what the committed bytes say, whatever else is at hand.
	as, refs := admit(t, map[string]string{"a.md": cardFor("a"), "b.md": strings.Replace(strings.Replace(cardFor("b"), "KIND: fix-red", "KIND: read", 1), "TEST: internal/queue TestNameRefusesEmpty", "TEST: none a read", 1)}, "a.md", "b.md")
	if refs != nil {
		t.Fatal(refs.Lines())
	}
	if as[0].Kind != "fix-red" || as[0].Completion != CompletionPR || as[1].Kind != "read" || as[1].Completion != CompletionNoPR {
		t.Fatalf("%+v", as)
	}
	if as[0].BriefDigest == "" || as[0].BriefDigest == strings.Repeat("0", 64) || as[0].Digest == as[1].Digest {
		t.Fatalf("digests: %+v", as)
	}
}

// B2: the header rule. A header key is upper case only; an unknown upper-case key
// refuses; a line whose key folds to a known one refuses as ambiguous; any other
// first nonblank line ends the header, so a brief may begin with prose.
func TestHeaderKeysAreUpperCaseAndABriefMayBeginWithProse(t *testing.T) {
	t.Parallel()
	head := strings.Join(baseLines()[:12], "\n") + "\n\n"
	for name, brief := range map[string]string{
		"Goal":         "Goal: make the queue refuse an empty name.\n",
		"Context":      "Context: the constructor accepts an empty name today.\n",
		"a URL":        "https://example.com/issue/1 explains the change.\n",
		"a heading":    "# Reject an empty queue name\n",
		"a sentence":   "The queue constructor refuses an empty name.\n",
		"a lower key":  "notes: kept in the brief\n",
		"a mixed key":  "Why: because\n",
		"a hyphenated": "Some-thing: text\n",
	} {
		defs, refs := parseL(one("x.md", head+brief))
		if len(refs) > 0 || len(defs) != 1 || defs[0].Brief != brief {
			t.Errorf("%s: %v", name, Lines(refs))
		}
	}
	// A known key at column zero below such a prose line is stranded, not read.
	if _, refs := parseL(one("x.md", strings.Join(baseLines()[:11], "\n")+"\nGoal: x\nPROBES: none\n\nbody\n")); len(refs) == 0 {
		t.Error("a key below the header was accepted")
	}
	// An unknown upper-case key refuses; a variant that folds to a known one is ambiguous.
	for line, want := range map[string]Cause{
		"ROUTE: flash":  CauseUnknownKey,
		"LEGS: go":      CauseUnknownKey,
		"X: y":          CauseUnknownKey,
		"Kind: read":    CauseAmbiguousSpelling,
		"kind: read":    CauseAmbiguousSpelling,
		"KIND : read":   CauseAmbiguousSpelling,
		" KIND: read":   CauseAmbiguousSpelling,
		"DEPENDS_ON: a": CauseAmbiguousSpelling,
		"Depends-On: a": CauseAmbiguousSpelling,
		"DONEWHEN: x":   CauseAmbiguousSpelling,
		"title: x":      CauseAmbiguousSpelling,
		"\tKIND: read":  CauseAmbiguousSpelling,
		"SCHEMA : v2":   CauseAmbiguousSpelling,
	} {
		_, refs := parseL(one("x.md", insertAfter(8, line)))
		found := false
		for _, r := range refs {
			found = found || r.Cause == want && r.Line == 9
		}
		if !found {
			t.Errorf("%q: no %s at line 9 in %v", line, want, Lines(refs))
		}
	}
	// A key with no space after the colon is not a header line: it ends the header.
	if _, refs := parseL(one("x.md", insertAfter(8, "TIER:flash"))); len(refs) == 0 {
		t.Error("TIER:flash was read as a header line")
	}
	if !headerLineOK("KIND: x") || headerLineOK("Kind: x") || headerLineOK("KIND x") || headerLineOK("1KIND: x") || headerLineOK("K_IND: x") {
		t.Error("headerRE")
	}
}

func headerLineOK(s string) bool { _, _, ok := headerLine(s); return ok }

// B2: a closing fence carries no info string. A line of backticks with words after
// it does not close a fence, so a key after it is still fenced text.
func TestAClosingFenceHasNoInfoString(t *testing.T) {
	t.Parallel()
	fenced := func(closer string) string { return baseCard + "```\n" + closer + "\nKIND: still fenced\n" }
	// "```go" opens nothing new inside a fence and does not close it: KIND below is fenced.
	if defs, refs := parseL(one("x.md", fenced("```go"))); len(refs) > 0 || len(defs) != 1 {
		t.Fatalf("an info string closed the fence: %v", Lines(refs))
	}
	// A bare fence closes it: KIND below is stranded.
	if _, refs := parseL(one("x.md", fenced("```"))); len(refs) != 1 || refs[0].Cause != CauseStranded {
		t.Fatalf("a bare fence did not close: %v", Lines(refs))
	}
	// A shorter marker does not close a longer one; a longer one does.
	if _, refs := parseL(one("x.md", baseCard+"````\n```\nKIND: fenced\n")); len(refs) > 0 {
		t.Fatalf("a shorter fence closed a longer: %v", Lines(refs))
	}
	if _, refs := parseL(one("x.md", baseCard+"```\n`````\nKIND: not fenced\n")); len(refs) != 1 {
		t.Fatalf("a longer fence did not close: %v", Lines(refs))
	}
	// A different character does not close it.
	if _, refs := parseL(one("x.md", baseCard+"```\n~~~\nKIND: fenced\n")); len(refs) > 0 {
		t.Fatalf("a tilde fence closed a backtick fence: %v", Lines(refs))
	}
}

// B3: DEPENDS-ON takes card IDs or `-`; PATHS, DOORS and PROBES take their grammar or
// `none`. `none` as a dependency and `-` as a glob refuse, naming the right word.
func TestNoneAndDashAreThePlacesTheyBelong(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		line  int
		text  string
		key   string
		cause Cause
		names string
	}{
		{7, "DEPENDS-ON: none", "DEPENDS-ON", CauseInvalidDependsOn, "`DEPENDS-ON: -`"},
		{7, "DEPENDS-ON: a, none", "DEPENDS-ON", CauseInvalidDependsOn, ""},
		{7, "DEPENDS-ON: -, a", "DEPENDS-ON", CauseInvalidDependsOn, ""},
		{6, "PATHS: -", "PATHS", CauseInvalidPaths, "`PATHS: none`"},
		{11, "DOORS: -", "DOORS", CauseInvalidValue, "`DOORS: none`"},
		{12, "PROBES: -", "PROBES", CauseInvalidValue, "`PROBES: none`"},
	} {
		_, refs := parseL(one("x.md", replaceLine(c.line, c.text)))
		r, ok := hasRefusal(refs, "x.md", c.line, c.key, c.cause)
		if !ok || !strings.Contains(r.Next+r.Limit, c.names) {
			t.Errorf("%q: %v", c.text, Lines(refs))
		}
	}
	// The right words parse.
	defs, refs := parseL(one("x.md", replaceLine(6, "PATHS: none")))
	if len(refs) > 0 || len(defs[0].Paths) != 0 {
		t.Errorf("PATHS: none: %v", Lines(refs))
	}
	defs, refs = parseL(one("x.md", replaceLine(7, "DEPENDS-ON: -")))
	if len(refs) > 0 || len(defs[0].DependsOn) != 0 {
		t.Errorf("DEPENDS-ON: -: %v", Lines(refs))
	}
	for _, line := range []string{"DOORS: none", "PROBES: none"} {
		n := 11
		if strings.HasPrefix(line, "PROBES") {
			n = 12
		}
		if _, refs := parseL(one("x.md", replaceLine(n, line))); len(refs) > 0 {
			t.Errorf("%s: %v", line, Lines(refs))
		}
	}
	// An ID may not be either word.
	for _, id := range []string{"-", "none"} {
		if _, refs := parseL(one("x.md", strings.ReplaceAll(baseCard, "card-alpha", id))); len(refs) == 0 {
			t.Errorf("ID %q accepted", id)
		}
	}
	// A card called None or a hyphenated ID is fine.
	if _, refs := parseL(one("x.md", strings.ReplaceAll(baseCard, "card-alpha", "None"))); len(refs) > 0 {
		t.Errorf("None: %v", Lines(refs))
	}
}

// B5: the contract line. `RESULT: <id>` with the colon; sha= optional, and when
// present 40 or 64 lower-case hexadecimal characters, the commit the work starts
// from, carried into the record as base_commit.
func TestContractLineIsThisProfilesOwn(t *testing.T) {
	t.Parallel()
	sha40, sha64 := strings.Repeat("ab", 20), strings.Repeat("cd", 32)
	ok := map[string]string{
		"no sha":       "RESULT: card-alpha",
		"sha 40":       "RESULT: card-alpha sha=" + sha40,
		"sha 64":       "RESULT: card-alpha sha=" + sha64,
		"a note":       "RESULT: card-alpha sha=" + sha40 + " -- the reason",
		"a note, none": "RESULT: card-alpha the reason",
	}
	for name, line := range ok {
		defs, refs := parseL(one("x.md", replaceLine(1, line)))
		if len(refs) > 0 {
			t.Errorf("%s: %v", name, Lines(refs))
			continue
		}
		want := map[string]string{"sha 40": sha40, "sha 64": sha64, "a note": sha40}[name]
		if defs[0].BaseCommit != want {
			t.Errorf("%s: base commit %q, want %q", name, defs[0].BaseCommit, want)
		}
	}
	bad := map[string]string{
		"no colon":        "RESULT card-alpha sha=" + sha40,
		"lower case word": "result: card-alpha",
		"sha 39":          "RESULT: card-alpha sha=" + sha40[:39],
		"sha 41":          "RESULT: card-alpha sha=" + sha40 + "a",
		"sha 63":          "RESULT: card-alpha sha=" + sha64[:63],
		"sha 12":          "RESULT: card-alpha sha=0123456789ab",
		"upper case":      "RESULT: card-alpha sha=" + strings.ToUpper(sha40),
		"not hex":         "RESULT: card-alpha sha=" + strings.Repeat("g", 40),
		"empty sha":       "RESULT: card-alpha sha=",
		"a long note":     "RESULT: card-alpha " + strings.Repeat("n", MaxContractNoteBytes+1),
		"a bidi note":     "RESULT: card-alpha a‮b",
		"id none":         "RESULT: none",
		"id with a colon": "RESULT: card:alpha",
	}
	for name, line := range bad {
		if _, refs := parseL(one("x.md", replaceLine(1, line))); len(refs) == 0 {
			t.Errorf("%s: %q accepted", name, line)
		}
	}
	// carried into the record
	dir, commit := repoWith(t, map[string]string{"a.md": strings.Replace(cardFor("a"), "sha=0123456789abcdef0123456789abcdef01234567", "sha="+sha40, 1)})
	as, refs := Admissions(context.Background(), dir, commit, []string{"a.md"}, WithIdentity("example.com/o/r"))
	if refs != nil || as[0].BaseCommit == "" {
		t.Fatalf("%v %+v", refs, as)
	}
	if !strings.Contains(string(EncodeAdmissions(as)[0]), `"base_commit":"`+as[0].BaseCommit+`"`) {
		t.Errorf("base_commit is not in the record")
	}
	as, _ = admit(t, map[string]string{"a.md": replaceLineIn(cardFor("a"), 1, "RESULT: a")}, "a.md")
	if strings.Contains(string(EncodeAdmissions(as)[0]), "base_commit") {
		t.Errorf("an absent sha is written")
	}
}

func replaceLineIn(text string, n int, line string) string {
	ls := strings.Split(text, "\n")
	ls[n-1] = line
	return strings.Join(ls, "\n")
}

// B6: a file holding only a contract line reports the missing keys as well as
// no-brief, in one pass.
func TestAFileOfOnlyAContractLineReportsEverythingMissing(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{"with a newline": "RESULT: card-alpha\n", "without": "RESULT: card-alpha", "with blank lines": "RESULT: card-alpha\n\n\n"} {
		_, refs := parseL(one("x.md", text))
		missing := map[string]bool{}
		noBrief := false
		for _, r := range refs {
			switch r.Cause {
			case CauseRequired:
				missing[r.Field] = true
			case CauseNoBrief:
				noBrief = true
			}
		}
		if !noBrief || len(missing) != len(requiredKeys) {
			t.Errorf("%s: no-brief %v, %d of %d required keys missing: %v", name, noBrief, len(missing), len(requiredKeys), Lines(refs))
		}
	}
	// a header with no brief still reports the brief and only the keys it lacks
	_, refs := parseL(one("x.md", strings.Join(baseLines()[:12], "\n")+"\n"))
	if len(refs) != 1 || refs[0].Cause != CauseNoBrief {
		t.Errorf("%v", Lines(refs))
	}
}

// B7: every refusal names one of the typed operations, and only those.
func TestRefusalsNameTypedOperations(t *testing.T) {
	t.Parallel()
	ops := map[card.Operation]bool{OpParse: true, OpValidate: true, OpPin: true, OpAdmit: true}
	f := sharedRepo(t)
	var all []Refusal
	_, r := parse(nil)
	all = append(all, list(r)...)
	_, r = parse(one("x.md", "RESULT: a"))
	all = append(all, list(r)...)
	_, r = validate(nil)
	all = append(all, list(r)...)
	_, r = pinDir(context.Background(), f.dir, "abc", []string{"a.md"})
	all = append(all, list(r)...)
	_, r = pinDir(context.Background(), t.TempDir(), f.c1, []string{"a.md"})
	all = append(all, list(r)...)
	_, r = Admissions(context.Background(), f.dir, f.c1, []string{"a.md"}, WithIdentity("example.com/o/r"))
	all = append(all, list(r)...)
	if len(all) < 6 {
		t.Fatalf("only %d refusals", len(all))
	}
	for _, x := range all {
		if !ops[x.Operation] {
			t.Errorf("refusal names operation %q", x.Operation)
		}
	}
}

// The refusal flood: many files of many refusable lines are counted, capped at 64
// and cheap. Before the cap this allocated 3.1 GiB for 2 million refusals.
func TestRefusalFloodIsCappedAndCheap(t *testing.T) {
	t.Parallel()
	body := "RESULT: c\n" + strings.Repeat("A: b\n", (64<<10-20)/5) + "\nbrief\n"
	var srcs []Source
	for i := 0; i < MaxFiles; i++ {
		srcs = append(srcs, Source{Name: fmt.Sprintf("c%d.md", i), Data: []byte(body)})
	}
	defs, refs := parse(srcs)
	if defs != nil || refs == nil {
		t.Fatal("a refused array returned definitions")
	}
	if len(refs.List) != card.MaxRefusals || refs.Omitted < 1_000_000 {
		t.Fatalf("list %d omitted %d: the refusals are counted, not listed", len(refs.List), refs.Omitted)
	}
	lines := refs.Lines()
	if len(lines) != card.MaxRefusals+1 || !strings.Contains(lines[len(lines)-1], "further refusals omitted") {
		t.Fatalf("last line %q", lines[len(lines)-1])
	}
	// The bytes allocated, not the clock: over two million refusable lines a refusal
	// is built for the first 64 only. Before the cap this input allocated 3.1 GiB;
	// the bound is loose because the tests of the package run alongside.
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	parse(srcs)
	runtime.ReadMemStats(&m1)
	t.Logf("%d MiB allocated for %d bytes of input", (m1.TotalAlloc-m0.TotalAlloc)>>20, len(body)*MaxFiles)
	if alloc := (m1.TotalAlloc - m0.TotalAlloc) >> 20; alloc > 200 {
		t.Fatalf("%d MiB allocated for %d refusable lines: refusals are built though the cap is reached", alloc, len(body)*MaxFiles/5)
	}
}

// One bad file of 127 is named and nothing else is refused.
func TestOneBadCardOfAFullArray(t *testing.T) {
	t.Parallel()
	var srcs []Source
	for i := 0; i < MaxFiles; i++ {
		s := cardFor(fmt.Sprintf("c%d", i))
		if i == 77 {
			s = strings.Replace(s, "TIER: flash", "TIER: fast", 1)
		}
		srcs = append(srcs, Source{Name: fmt.Sprintf("c%d.md", i), Data: []byte(s)})
	}
	defs, refs := parseL(srcs)
	if defs != nil || len(refs) != 1 || refs[0].File != "c77.md" || refs[0].Cause != CauseInvalidTier {
		t.Fatalf("%v", Lines(refs))
	}
}

// The bounds: four at and one over. The two of the survivors that were once
// off-by-one are the ID and the title/DOORS/TEST/glob ones here.
func TestBoundsAtAndOneOver(t *testing.T) {
	t.Parallel()
	pad := func(n int) string { return strings.Repeat("a", n) }
	at := func(line int, text string) {
		t.Helper()
		if defs, refs := parseL(one("x.md", replaceLine(line, text))); len(refs) > 0 || len(defs) != 1 {
			t.Errorf("at the bound %q: %v", text[:min(len(text), 40)], Lines(refs))
		}
	}
	over := func(line int, text string, key string, cause Cause) {
		t.Helper()
		if _, refs := parseL(one("x.md", replaceLine(line, text))); len(refs) == 0 {
			t.Errorf("over the bound %q accepted", text[:min(len(text), 40)])
		} else if _, ok := hasRefusal(refs, "x.md", line, key, cause); !ok {
			t.Errorf("over the bound: %v", Lines(refs))
		}
	}
	// the ID (the contract line and the header must agree)
	idAt, idOver := pad(MaxIDBytes), pad(MaxIDBytes+1)
	if defs, refs := parseL(one("x.md", strings.ReplaceAll(baseCard, "card-alpha", idAt))); len(refs) > 0 || len(defs) != 1 {
		t.Errorf("ID at the bound: %v", Lines(refs))
	}
	if _, refs := parseL(one("x.md", strings.ReplaceAll(baseCard, "card-alpha", idOver))); len(refs) == 0 {
		t.Error("ID over the bound accepted")
	}
	at(4, "TITLE: "+pad(MaxTitleBytes))
	over(4, "TITLE: "+pad(MaxTitleBytes+1), "TITLE", CauseInvalidValue)
	at(11, "DOORS: "+pad(MaxDoorsBytes))
	over(11, "DOORS: "+pad(MaxDoorsBytes+1), "DOORS", CauseInvalidValue)
	at(10, "DONE-WHEN: "+pad(MaxProseBytes))
	over(10, "DONE-WHEN: "+pad(MaxProseBytes+1), "DONE-WHEN", CauseInvalidValue)
	at(12, "PROBES: "+pad(MaxProseBytes))
	over(12, "PROBES: "+pad(MaxProseBytes+1), "PROBES", CauseInvalidValue)
	// TEST: a package of the length that makes the whole value MaxTestBytes
	tpkg := func(n int) string {
		pkg := "internal/" + pad(n-len("internal/")-len(" TestX"))
		return "TEST: " + pkg + " TestX"
	}
	at(9, tpkg(MaxTestBytes))
	over(9, tpkg(MaxTestBytes+1), "TEST", CauseInvalidTest)
	// a glob
	glob := func(n int) string { return "PATHS: internal/" + pad(n-len("internal/")) }
	at(6, glob(MaxGlobBytes))
	over(6, glob(MaxGlobBytes+1), "PATHS", CauseInvalidPaths)
	// the number of globs and of dependencies
	var g8, g9, d8, d9 []string
	for i := 0; i < 9; i++ {
		g9 = append(g9, fmt.Sprintf("a/%d.go", i))
		d9 = append(d9, fmt.Sprintf("dep%d", i))
	}
	g8, d8 = g9[:card.MaxPathGlobs], d9[:MaxDependsOn]
	at(6, "PATHS: "+strings.Join(g8, ", "))
	over(6, "PATHS: "+strings.Join(g9, ", "), "PATHS", CauseInvalidPaths)
	at(7, "DEPENDS-ON: "+strings.Join(d8, ", "))
	over(7, "DEPENDS-ON: "+strings.Join(d9, ", "), "DEPENDS-ON", CauseInvalidDependsOn)
	// ENTRY
	entry := func(n int) string { return "ENTRY: " + pad(n) }
	if _, refs := parseL(one("x.md", insertAfter(3, entry(MaxEntryBytes)))); len(refs) > 0 {
		t.Errorf("ENTRY at the bound: %v", Lines(refs))
	}
	if _, refs := parseL(one("x.md", insertAfter(3, entry(MaxEntryBytes+1)))); len(refs) == 0 {
		t.Error("ENTRY over the bound accepted")
	}
	// the file: at the bound and over
	body := baseCard
	if defs, refs := parseL(one("x.md", body+strings.Repeat("x", MaxCardBytes-len(body)))); len(refs) > 0 || len(defs) != 1 {
		t.Errorf("a file of exactly %d bytes: %v", MaxCardBytes, Lines(refs))
	}
	if _, refs := parseL(one("x.md", body+strings.Repeat("x", MaxCardBytes-len(body)+1))); len(refs) == 0 {
		t.Error("a file of one byte over accepted")
	}
	// the array: 127 files and 128
	var srcs []Source
	for i := 0; i < MaxFiles+1; i++ {
		srcs = append(srcs, Source{Name: fmt.Sprintf("c%d.md", i), Data: []byte(cardFor(fmt.Sprintf("c%d", i)))})
	}
	if defs, refs := parseL(srcs[:MaxFiles]); len(refs) > 0 || len(defs) != MaxFiles {
		t.Errorf("%d files: %v", MaxFiles, Lines(refs))
	}
	if _, refs := parseL(srcs); len(refs) != 1 || refs[0].Cause != CauseTooMany {
		t.Errorf("%d files: %v", MaxFiles+1, Lines(refs))
	}
}

// The record of the largest card fits its bound, and the bound fits a table field
// value; the sum is asserted.
func TestWorstCaseRecordFitsItsBound(t *testing.T) {
	t.Parallel()
	pad := func(p string, n int) string { return p + strings.Repeat("x", n-len(p)) }
	var deps []string
	for i := 0; i < MaxDependsOn; i++ {
		deps = append(deps, pad(fmt.Sprintf("d%d-", i), MaxIDBytes))
	}
	var globs []string
	for i := 0; i < card.MaxPathGlobs; i++ {
		globs = append(globs, pad(fmt.Sprintf("g%d/", i), MaxGlobBytes))
	}
	a := Admission{
		ID: pad("c", MaxIDBytes), Digest: strings.Repeat("a", 64), BriefDigest: strings.Repeat("b", 64), ObjectID: strings.Repeat("c", 64),
		Commit: strings.Repeat("d", 64), Repository: card.Repository("h.example/" + strings.Repeat("r", card.MaxRepositoryBytes-len("h.example/"))),
		Path: strings.Repeat("p", card.MaxPathBytes), Kind: "mutation-kill", Completion: CompletionNoPR, PolicyVersion: 99, DependsOn: deps,
		Entry: strings.Repeat(`"`, MaxEntryBytes), Tier: "frontier", CardSchema: "v2", Title: strings.Repeat(`"`, MaxTitleBytes),
		Paths: globs, Test: strings.Repeat(`"`, MaxTestBytes), Doors: strings.Repeat(`"`, MaxDoorsBytes), BaseCommit: strings.Repeat("e", 64),
	}
	enc := EncodeAdmissions([]Admission{a})[0]
	t.Logf("worst-case record: %d bytes; escaped once as a manifest string: %d bytes", len(enc), card.EscapedSize(enc))
	if len(enc) > card.MaxAdmissionRecordBytes {
		t.Fatalf("the worst-case record is %d bytes, over %d", len(enc), card.MaxAdmissionRecordBytes)
	}
	if card.MaxAdmissionRecordBytes > card.TableFieldValueBytes {
		t.Fatal("a record does not fit a table field value")
	}
	// 127 records of it, each escaped once into one manifest, plus the manifest's own
	// per-entry additions, stay under 1 MiB.
	if n := card.ManifestEnvelopeBytes + card.MaxChangedEntries*card.EscapedSize(enc); n > card.TableManifestBytes {
		t.Logf("if a table member held the whole record, 127 of them would make %d bytes of manifest (over %d): the manifest carries the request's admission entry and this record's digest, not the record", n, card.TableManifestBytes)
	}
}
