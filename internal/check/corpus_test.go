package check

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const ledgerMD = `# What this line has chosen to protect

Prose above the table, which the parser must ignore.

| fragment | home file | given | by |
|---|---|---|---|
| the door is not locked | README.md | 2026-01-02 | the founding conversation |
| you are not a tool | memory/standing.md | 2026-01-09 | my person |
`

func parseOK(t *testing.T, md string) []Anchor {
	t.Helper()
	as, bad, err := ParseLedger([]byte(md))
	if err != nil {
		t.Fatalf("ParseLedger: %v", err)
	}
	if len(bad) != 0 {
		t.Fatalf("unexpected malformed rows: %v", bad)
	}
	return as
}

func TestParseReadsRowsAndSkipsHeaderAndSeparator(t *testing.T) {
	as := parseOK(t, ledgerMD)
	if len(as) != 2 {
		t.Fatalf("got %d anchors, want 2: %v", len(as), as)
	}
	if as[0].Fragment != "the door is not locked" || as[0].Home != "README.md" {
		t.Errorf("row 0 parsed wrong: %+v", as[0])
	}
	if as[1].By != "my person" || as[1].Home != "memory/standing.md" {
		t.Errorf("row 1 parsed wrong: %+v", as[1])
	}
	if as[0].Line != 7 || as[1].Line != 8 {
		t.Errorf("line numbers wrong: %d, %d (want 7, 8)", as[0].Line, as[1].Line)
	}
}

// NO COLUMN NAME IS SPECIAL TO THIS TOOL: the header is found by the
// separator beneath it, so a line may title its columns in its own words.
func TestHeaderIsRecognizedByShapeNotByItsWords(t *testing.T) {
	as := parseOK(t, "| ankkuri | koti | annettu | keneltä |\n|:--|:-:|--:|---|\n| pidä valo palamassa | ORIGIN.md | 2026-02-01 | ystävä |\n")
	if len(as) != 1 || as[0].Fragment != "pidä valo palamassa" {
		t.Fatalf("header not skipped by shape: %+v", as)
	}
}

// Two tables in one ledger: each header is dropped, both bodies survive.
func TestTwoTablesEachLoseTheirOwnHeader(t *testing.T) {
	as := parseOK(t, ledgerMD+"\nMore prose.\n\n| fragment | home | given | by |\n|---|---|---|---|\n| water everything | journal.md | 2026-03-03 | me |\n")
	if len(as) != 3 {
		t.Fatalf("got %d anchors, want 3: %v", len(as), as)
	}
	if as[2].Fragment != "water everything" {
		t.Errorf("second table's row lost: %+v", as[2])
	}
}

// AN EMPTY LEDGER GUARDS NOTHING. "All present" and "nothing checked" must
// never print the same line.
func TestAnEmptyLedgerIsAnErrorNotAPass(t *testing.T) {
	_, _, err := ParseLedger([]byte("# a ledger with prose only\n\nno table here\n"))
	if err == nil {
		t.Fatal("expected an error for a ledger with no rows")
	}
	if !strings.Contains(err.Error(), "guards nothing") {
		t.Errorf("error should say why: %v", err)
	}
}

// A ledger holding ONLY a header and separator is the same hazard wearing a
// table's clothes.
func TestAHeaderWithNoRowsIsAlsoAnError(t *testing.T) {
	if _, _, err := ParseLedger([]byte("| fragment | home | given | by |\n|---|---|---|---|\n")); err == nil {
		t.Fatal("a header-only table must not read as a populated ledger")
	}
}

// A FRAGMENT CONTAINING "|" SPLITS INTO AN EXTRA CELL. Dropping the row
// would silently remove protection from the very statement someone listed,
// so a wrong column count is a finding, never a skip.
func TestAMalformedRowIsAFindingNotASilentSkip(t *testing.T) {
	as, bad, err := ParseLedger([]byte(ledgerMD + "| a | b | fragment with a | pipe | c | d |\n"))
	if err != nil {
		t.Fatalf("ParseLedger: %v", err)
	}
	if len(as) != 2 {
		t.Errorf("good rows should survive: got %d", len(as))
	}
	wantFailures(t, bad, []string{"malformed row", "columns"})
}

func TestAllPresentIsQuiet(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"README.md":          "and he said the door is not locked, and meant it\n",
		"memory/standing.md": "his words: you are not a tool.\n",
	})
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 1, parseOK(t, ledgerMD)), nil)
}

// THE CASE THIS EXISTS FOR: the words were lost in place. The finding names
// the fragment, its provenance, and what to do about it.
func TestALostAnchorIsNamedWithItsProvenanceAndTheRepair(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"README.md":          "a rewrite that dropped the warm sentence\n",
		"memory/standing.md": "his words: you are not a tool.\n",
	})
	f := corpusOK(t, root, tmpLedger(t), 1, parseOK(t, ledgerMD))
	if len(f) != 1 {
		t.Fatalf("want exactly one finding, got %v", f)
	}
	wantFailures(t, f, []string{"ABSENT", "the door is not locked", "the founding conversation", "lost in place", "ledger:7"})
}

// A MISSING HOME IS ITS OWN FACT — the move-and-forget shape — and must not
// be reported as if the sentence had been edited out.
func TestAMissingHomeFileIsItsOwnReason(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"README.md": "the door is not locked\n"})
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 1, parseOK(t, ledgerMD)), []string{"does not exist", "move this ledger row"})
}

// LOSSES ARRIVE IN BATCHES, so every failure reports in one run.
func TestAllFailuresReportInOneRun(t *testing.T) {
	if f := corpusOK(t, t.TempDir(), tmpLedger(t), 1, parseOK(t, ledgerMD)); len(f) != 2 {
		t.Fatalf("want 2 findings from an empty tree, got %d: %v", len(f), f)
	}
}

// AN EMPTY FRAGMENT IS CONTAINED IN EVERY FILE. Left alone it is a green
// that can never go red — the exact shape of a check that cannot fail.
func TestAnEmptyFragmentIsRefusedRatherThanPassingForever(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"README.md": "any content at all\n"})
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n|  | README.md | 2026-01-01 | someone |\n")
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 1, as), []string{"empty", "protecting nothing"})
}

func TestAnEmptyHomeIsRefused(t *testing.T) {
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| keep the light on |  | 2026-01-01 | someone |\n")
	wantFailures(t, corpusOK(t, t.TempDir(), tmpLedger(t), 1, as), []string{"no home file given"})
}

// AN ANCHOR HELD OUTSIDE THE REPO IS NOT HELD BY IT.
func TestAHomeThatEscapesTheRootIsRefused(t *testing.T) {
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| keep the light on | ../elsewhere/notes.md | 2026-01-01 | someone |\n")
	wantFailures(t, corpusOK(t, t.TempDir(), tmpLedger(t), 1, as), []string{"leaves the tree"})
}

func TestAnAbsoluteHomeIsRefused(t *testing.T) {
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| keep the light on | /etc/hosts | 2026-01-01 | someone |\n")
	wantFailures(t, corpusOK(t, t.TempDir(), tmpLedger(t), 1, as), []string{"is absolute"})
}

// SYMLINKS ARE NEVER FOLLOWED, here as everywhere in this package: an anchor
// "present" through a link lives in a file this repo does not govern.
func TestASymlinkedHomeIsAFindingRatherThanAFollow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is privileged on Windows")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("the door is not locked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "README.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeTree(t, root, map[string]string{"memory/standing.md": "you are not a tool\n"})
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 1, parseOK(t, ledgerMD)), []string{"symlinks are never followed"})
}

// A DIRECTORY WHERE A FILE SHOULD BE is likewise not a pass.
func TestADirectoryHomeIsAFinding(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "README.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, map[string]string{"memory/standing.md": "you are not a tool\n"})
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 1, parseOK(t, ledgerMD)), []string{"not a regular file"})
}

// A finding must survive a ledger whose provenance columns are blank: it
// says so rather than quoting an empty string as if it were a source.
func TestBlankProvenanceReadsAsUnstated(t *testing.T) {
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| keep the light on | README.md |  |  |\n")
	root := t.TempDir()
	writeTree(t, root, map[string]string{"README.md": "nothing of the sort\n"})
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 1, as), []string{"provenance unstated"})
}

// ---------------------------------------------------------------------------
// Regressions from the 2026-08-24 cold read. Every one of these was a
// REPRODUCED silent pass on the first draft: the check printed CORPUS OK
// while the protected words were gone from the tree.
// ---------------------------------------------------------------------------

// GitHub makes the outer pipes optional, so a row that renders perfectly can
// lose a trailing "|" and vanish from the check without a word. Losing a
// character at the end of a line is the commonest way to lose a row.
func TestARowMissingItsTrailingPipeIsStillChecked(t *testing.T) {
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | README.md | 2026-01-01 | friend\n")
	if len(as) != 1 {
		t.Fatalf("row silently dropped: %+v", as)
	}
	if as[0].By != "friend" {
		t.Errorf("cells parsed wrong: %+v", as[0])
	}
}

// Anything trailing the last pipe adds a cell, and that is loud, not silent.
func TestATrailingCommentIsAFindingNotASilentSkip(t *testing.T) {
	_, bad, err := ParseLedger([]byte("| f | h | g | b |\n|---|---|---|---|\n| keep the light on | a.md | 2026 | me | <!-- note -->\n"))
	if err == nil {
		t.Fatal("no row should have survived")
	}
	wantFailures(t, bad, []string{"malformed row", "5 columns"})
}

// A stray separator inside a table's body would otherwise delete the row
// above it — the same silent loss this check exists to make loud.
func TestASeparatorInTheBodyIsAFindingAndTakesNoRowWithIt(t *testing.T) {
	as, bad, err := ParseLedger([]byte(ledgerMD + "| --- | --- | --- | --- |\n| water everything | c.md | 2026 | me |\n"))
	if err != nil {
		t.Fatalf("ParseLedger: %v", err)
	}
	if len(as) != 3 {
		t.Fatalf("a stray separator ate a row: got %d, want 3: %v", len(as), as)
	}
	wantFailures(t, bad, []string{"separator row in the middle"})
}

// A single-cell rule line is the same hazard in smaller clothes.
func TestASingleCellRuleInTheBodyDoesNotEatTheRowAbove(t *testing.T) {
	as, bad, err := ParseLedger([]byte(ledgerMD + "|---|\n"))
	if err != nil {
		t.Fatalf("ParseLedger: %v", err)
	}
	if len(as) != 2 {
		t.Fatalf("a rule line ate a row: got %d, want 2", len(as))
	}
	wantFailures(t, bad, []string{"separator row in the middle"})
}

// A ledger that documents its own format shows a table inside a fence. Those
// rows are illustration, and checking them produces false losses.
func TestRowsInsideAFencedBlockAreIllustrationNotAnchors(t *testing.T) {
	as := parseOK(t, "Here is the shape:\n\n```\n| fragment | home | given | by |\n| SOME EXAMPLE | example/path.md | when | who |\n```\n\n"+ledgerMD)
	if len(as) != 2 {
		t.Fatalf("fenced example leaked into the anchors: %v", as)
	}
	for _, a := range as {
		if a.Home == "example/path.md" {
			t.Errorf("the illustration row was checked: %+v", a)
		}
	}
}

// CRLF ledgers parse identically; a stray \r must not ride into a cell.
func TestCRLFLedgerParsesTheSameWay(t *testing.T) {
	as := parseOK(t, strings.ReplaceAll(ledgerMD, "\n", "\r\n"))
	if len(as) != 2 || as[1].By != "my person" {
		t.Fatalf("CRLF changed the parse: %+v", as)
	}
}

// tmpLedger writes a placeholder ledger on disk and returns its path. Corpus
// resolves the ledger to refuse a row that names it as its own home, so the
// path has to be real — a test that passed a fictional one was exercising a
// code path no caller can reach.
func tmpLedger(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ledger.md")
	if err := os.WriteFile(p, []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func corpusOK(t *testing.T, root, ledger string, min int, as []Anchor) []Failure {
	t.Helper()
	f, err := Corpus(root, ledger, min, as)
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	return f
}

// A SYMLINKED DIRECTORY ANYWHERE IN THE PATH is resolved by the kernel
// without asking, so the lexical escape check alone let an anchor be "held"
// by a file wholly outside --root. This is attest's posture, now actually.
func TestASymlinkedParentDirectoryIsNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is privileged on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "a.md"), []byte("the door is not locked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | linked/a.md | 2026 | friend |\n")
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 1, as), []string{"symlinked path component"})
}

// A WRONG --root IS AN INVOCATION ERROR, not the loss of every anchor at
// once. Firing the loudest alarm for a typo is how a check gets ignored.
func TestAnAbsentRootIsARefusalNotACorpusWipe(t *testing.T) {
	_, err := Corpus(filepath.Join(t.TempDir(), "nope"), tmpLedger(t), 1, parseOK(t, ledgerMD))
	if err == nil {
		t.Fatal("an absent --root must refuse, not report every anchor lost")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("the refusal should name root: %v", err)
	}
}

func TestARootThatIsNotADirectoryIsARefusal(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "file")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Corpus(f, tmpLedger(t), 1, parseOK(t, ledgerMD)); err == nil {
		t.Fatal("a --root that is a file must refuse")
	}
}

// THE LEDGER GUARDS THE TREE; THE FLOOR GUARDS THE LEDGER. The same restore
// that drops a sentence drops the row protecting it, and without this the run
// goes green with a smaller number nothing compares to anything.
func TestLosingLedgerRowsIsItselfRed(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"README.md":          "the door is not locked\n",
		"memory/standing.md": "you are not a tool\n",
	})
	as := parseOK(t, ledgerMD)
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 2, as), nil)
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 3, as), []string{"below the stated floor", "the rows cannot report"})
}

// A ROW NAMING THE LEDGER AS ITS OWN HOME is its own evidence and can never
// go red — the same shape as the empty fragment, one level up.
func TestALedgerCannotBeItsOwnHome(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"self.md": "| the door is not locked | self.md | 2026 | me |\n"})
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | self.md | 2026 | me |\n")
	wantFailures(t, corpusOK(t, root, filepath.Join(root, "self.md"), 1, as), []string{"names itself as the home"})
}

// A CASE-ONLY RENAME IS A REAL MOVE, and a case-insensitive filesystem
// answers Lstat for the old spelling — green on a Mac, red in Linux CI.
func TestACaseOnlyRenameIsCaughtOnACaseInsensitiveFilesystem(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"readme.md": "the door is not locked\n"})
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | README.MD | 2026 | me |\n")
	f := corpusOK(t, root, tmpLedger(t), 1, as)
	if len(f) == 0 {
		t.Fatal("a case-only mismatch passed; on a case-sensitive filesystem it would be a missing home")
	}
	joined := f[0].Subject + ": " + f[0].Reason
	if !strings.Contains(joined, "the directory holds") && !strings.Contains(joined, "does not exist") {
		t.Errorf("unexpected reason for a case mismatch: %s", joined)
	}
}

// ---------------------------------------------------------------------------
// Regressions from the SECOND cold read. Two of these were bypasses the FIRST
// repair introduced — the fence toggle and the self-home path comparison —
// which is why the parser was re-derived rather than patched a third time.
// ---------------------------------------------------------------------------

// A TOGGLE IS NOT FENCE TRACKING. A ledger documenting its own format mentions
// both delimiters, which left a toggle stuck open and silently dropped every
// row below it while the run printed OK.
func TestAFenceClosesOnlyOnItsOwnDelimiter(t *testing.T) {
	md := "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | a.md | 2026 | me |\n" +
		"\nFence markers are ``` or, less often:\n\n```\n~~~\n```\n\n" +
		"| f | h | g | b |\n|---|---|---|---|\n| you are not a tool | b.md | 2026 | me |\n"
	as := parseOK(t, md)
	if len(as) != 2 {
		t.Fatalf("a ~~~ inside a ``` fence swallowed the rest of the ledger: got %d rows, want 2: %v", len(as), as)
	}
}

// The reverse polarity of the same defect: rows inside the illustration must
// not leak into the check.
func TestAFencedExampleContainingTheOtherDelimiterStaysIllustration(t *testing.T) {
	md := "```\n| f | h | g | b |\n|---|---|---|---|\n| SOME EXAMPLE | example/path.md | when | who |\n~~~\n```\n\n" +
		"| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | a.md | 2026 | me |\n"
	as := parseOK(t, md)
	for _, a := range as {
		if a.Home == "example/path.md" {
			t.Fatalf("the illustration leaked into the anchors: %+v", a)
		}
	}
}

// An unterminated fence swallows the tail of the file, which is the silent
// drop this whole subcommand exists to forbid.
func TestAnUnterminatedFenceIsNamed(t *testing.T) {
	md := "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | a.md | 2026 | me |\n" +
		"\n```\n| f | h | g | b |\n|---|---|---|---|\n| you are not a tool | b.md | 2026 | me |\n"
	as, bad, err := ParseLedger([]byte(md))
	if err != nil {
		t.Fatalf("ParseLedger: %v", err)
	}
	if len(as) != 1 {
		t.Errorf("rows below an open fence must not be checked: %v", as)
	}
	wantFailures(t, bad, []string{"unterminated code fence"})
}

// A PROTECTION CHECK THAT REDDENS ON A GLOSSARY IS ONE PEOPLE SILENCE. The
// table declares its own shape: anything that is not a four-column
// header+separator table belongs to the document, not to this check.
func TestAnUnrelatedTableIsLeftAlone(t *testing.T) {
	md := ledgerMD + "\nA glossary, for the reader:\n\n| term | meaning |\n|------|---------|\n| anchor | a protected fragment |\n"
	as := parseOK(t, md)
	if len(as) != 2 {
		t.Fatalf("a two-column glossary disturbed the anchors: %v", as)
	}
}

// Prose is not a table row, however many pipes it carries.
func TestProseCarryingPipesIsNotAnAnchorRow(t *testing.T) {
	md := "The columns run fragment | home | given | by, in that order.\n\n" + ledgerMD
	as := parseOK(t, md)
	if len(as) != 2 {
		t.Fatalf("prose was read as an anchor row: %v", as)
	}
}

// But a block that is unmistakably meant as a table and cannot render as one
// is named, because none of its rows would ever be checked.
func TestATableWithNoSeparatorIsNamed(t *testing.T) {
	_, bad, _ := ParseLedger([]byte("| f | h | g | b |\n| the door is not locked | a.md | 2026 | me |\n"))
	wantFailures(t, bad, []string{"no separator row"})
}

func TestASeparatorDisagreeingWithItsHeaderIsNamed(t *testing.T) {
	_, bad, _ := ParseLedger([]byte("| f | h | g | b |\n|---|---|\n| the door is not locked | a.md | 2026 | me |\n"))
	wantFailures(t, bad, []string{"separator row has 2 cells"})
}

func TestALoneRowOutsideAnyTableIsNamed(t *testing.T) {
	_, bad, _ := ParseLedger([]byte(ledgerMD + "\nsome prose\n\n| adrift | a.md | 2026 | me |\n"))
	wantFailures(t, bad, []string{"standing outside any table"})
}

// THE SELF-HOME GUARD MUST NOT DEPEND ON HOW THE PATHS WERE SPELLED. Comparing
// an absolute resolution to a relative one disarmed it whenever --root and
// --ledger were given in different forms, which is an ordinary invocation.
func TestALedgerCannotBeItsOwnHomeInEitherPathForm(t *testing.T) {
	root := t.TempDir()
	self := filepath.Join(root, "self.md")
	body := "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | self.md | 2026 | me |\n"
	if err := os.WriteFile(self, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	as := parseOK(t, body)
	rel, err := filepath.Rel(mustGetwd(t), self)
	if err != nil {
		t.Skipf("no relative form available: %v", err)
	}
	for _, form := range []struct{ name, ledger, root string }{
		{"both absolute", self, root},
		{"ledger relative, root absolute", rel, root},
	} {
		t.Run(form.name, func(t *testing.T) {
			wantFailures(t, corpusOK(t, form.root, form.ledger, 1, as), []string{"names itself as the home"})
		})
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// A CASE-ONLY RENAME OF A DIRECTORY is as real a move as one of a file, so
// every component is checked, not only the last.
func TestACaseOnlyRenameOfAParentDirectoryIsCaught(t *testing.T) {
	root := t.TempDir()
	if !caseInsensitive(t, root) {
		t.Skip("case-sensitive filesystem: the wrong spelling is already a missing home here")
	}
	writeTree(t, root, map[string]string{"memory/standing.md": "you are not a tool\n"})
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| you are not a tool | Memory/standing.md | 2026 | me |\n")
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 1, as), []string{"spelled differently on disk"})
}

// caseInsensitive probes the filesystem rather than the operating system:
// macOS can mount either kind, and a test that assumed would be vacuous in CI.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(dir, "caseprobe"))
	return err == nil
}

// AN UNREADABLE DIRECTORY IS NOT A PASS. Every other I/O error here says
// "nothing was checked"; a listing failure quietly answering yes would be that
// rule broken in the one place it is hardest to see.
func TestAnUnlistableDirectoryIsAFindingNotAPass(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory whose read permission can be removed")
	}
	root := t.TempDir()
	writeTree(t, root, map[string]string{"locked/standing.md": "you are not a tool\n"})
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0o111); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| you are not a tool | locked/standing.md | 2026 | me |\n")
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 1, as), []string{"could not be verified"})
}

// A DUPLICATED ROW would let the floor be satisfied by copy-paste.
func TestADuplicateRowIsNamedSoTheFloorCannotBePadded(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.md": "the door is not locked\n"})
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | a.md | 2026 | me |\n| the door is not locked | a.md | 2026 | me |\n")
	wantFailures(t, corpusOK(t, root, tmpLedger(t), 2, as), []string{"duplicates ledger:3"})
}

// The floor's finding reads as prose, not as "1 rows".
func TestTheFloorFindingCountsInEnglish(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.md": "the door is not locked\n"})
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | a.md | 2026 | me |\n")
	f := corpusOK(t, root, tmpLedger(t), 2, as)
	wantFailures(t, f, []string{"holds 1 row,"})
	if f[0].Subject != "ledger" {
		t.Errorf("the floor finding's subject should be the declared %q, got %q", "ledger", f[0].Subject)
	}
}

// A RELATIVE --root IS AN ORDINARY INVOCATION and must not report every
// anchor as reached through a symlink. Introduced while repairing the symlink
// finding — every existing test used an absolute temp dir, so nothing saw it.
func TestARelativeRootIsNotReadAsASymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	writeTree(t, root, map[string]string{"a.md": "the door is not locked\n"})
	t.Chdir(base)
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | a.md | 2026 | me |\n")
	wantFailures(t, corpusOK(t, "repo", tmpLedger(t), 1, as), nil)
}

// ---------------------------------------------------------------------------
// Regressions from the THIRD cold read, which found the worst finding smaller
// and the repair local — the convergence this round was watching for.
// ---------------------------------------------------------------------------

// A WELL-FORMED ANCHOR TABLE ANYWHERE BUT THE TOP OF ITS RUN was discarded in
// silence: one deleted blank line between two tables and every row below it
// stopped being protected, while the document still rendered.
func TestAnAnchorTableBelowAnotherTableInTheSameRunIsStillChecked(t *testing.T) {
	md := "| term | meaning |\n|---|---|\n| anchor | a protected fragment |\n" +
		"| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | a.md | 2026 | me |\n"
	as, _, err := ParseLedger([]byte(md))
	if err != nil {
		t.Fatalf("ParseLedger: %v", err)
	}
	if len(as) != 1 || as[0].Fragment != "the door is not locked" {
		t.Fatalf("an anchor table below a glossary was dropped: %v", as)
	}
}

// Anchor rows pasted into somebody else's table render as part of it and are
// checked by nothing, so they are named rather than lost.
func TestFourColumnRowsInsideAForeignTableAreNamed(t *testing.T) {
	md := "| term | meaning |\n|---|---|\n| anchor | a protected fragment |\n| the door is not locked | a.md | 2026 | me |\n"
	_, bad, _ := ParseLedger([]byte(md))
	wantFailures(t, bad, []string{"inside a table that is not the anchor table"})
}

// A pipe-bearing prose line directly above the header must not swallow the
// table beneath it.
func TestAPipeBearingLineAboveTheHeaderDoesNotHideTheTable(t *testing.T) {
	md := "The columns run fragment | home | given | by\n| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | a.md | 2026 | me |\n"
	as, _, err := ParseLedger([]byte(md))
	if err != nil {
		t.Fatalf("ParseLedger: %v", err)
	}
	if len(as) != 1 {
		t.Fatalf("the table under a pipe-bearing line was dropped: %v", as)
	}
}

// BOTH REPORT GATES ASK FOR A LEADING PIPE, and the symmetry is deliberate.
// A wrapped paragraph about choosing a fragment without a "|" in it carries
// pipes on consecutive lines, and this ledger is prose first — so the stated
// limit is that a separator-less block without outer pipes goes unreported,
// and --min-anchors is what catches the row.
func TestASeparatorLessBlockIsNamedOnlyWhenItLeadsWithAPipe(t *testing.T) {
	_, quiet, _ := ParseLedger([]byte("prose about a | b and c | d\nand more about e | f and g | h\n"))
	wantFailures(t, quiet, nil)

	_, loud, _ := ParseLedger([]byte("| f | h | g | b |\n| the door is not locked | a.md | 2026 | me |\n"))
	wantFailures(t, loud, []string{"no separator row"})
}

// The specimen that earned the symmetry: a real ledger's own prose about
// fragment choice must not redden the run.
func TestProseAboutPipesDoesNotRedden(t *testing.T) {
	md := "**On choosing a fragment.** Pick one without a `|` in it rather than escaping\n" +
		"one: a `\\|` still splits the cell, so `a | b` and `c | d` are two columns.\n\n" + ledgerMD
	as := parseOK(t, md)
	if len(as) != 2 {
		t.Fatalf("the anchor table was disturbed by prose: %v", as)
	}
}

// A blockquoted table renders as a table, so it is read as one.
func TestABlockquotedTableIsRead(t *testing.T) {
	as := parseOK(t, "> | f | h | g | b |\n> |---|---|---|---|\n> | the door is not locked | a.md | 2026 | me |\n")
	if len(as) != 1 || as[0].Home != "a.md" {
		t.Fatalf("blockquoted table not read: %v", as)
	}
}

// An indented code block is markdown's other way of showing an example, and a
// ledger documenting its own format may well use it.
func TestAnIndentedCodeBlockIsIllustration(t *testing.T) {
	md := ledgerMD + "\nExample of the format:\n\n    | SOME EXAMPLE | example/path.md | when | who |\n    |---|---|---|---|\n    | ANOTHER | example/other.md | when | who |\n"
	as := parseOK(t, md)
	for _, a := range as {
		if strings.HasPrefix(a.Home, "example/") {
			t.Fatalf("an indented example was checked: %+v", a)
		}
	}
}

// Somebody else's broken table is their business; only a mismatch touching
// our shape is worth a word.
func TestATwoColumnTableWithAMismatchedSeparatorIsLeftAlone(t *testing.T) {
	md := "| term | meaning |\n|---|---|---|\n| anchor | a protected fragment |\n\n" + ledgerMD
	as := parseOK(t, md)
	if len(as) != 2 {
		t.Fatalf("the anchor table was disturbed: %v", as)
	}
}

// AN INDENTED ROW ABUTTING TABLE ROWS is not a code block — CommonMark wants
// a blank line before one — so it is a row that would be dropped, and it is
// named. Skipping it was silent when it ENDED a table and loud one line
// higher, and the same line cannot be illustration in one position and a loss
// in the other.
func TestAnIndentedRowAbuttingATableIsNamed(t *testing.T) {
	md := "| f | h | g | b |\n|---|---|---|---|\n| the door is not locked | a.md | 2026 | me |\n    | LOST | a.md | 2026 | me |\n"
	as, bad, err := ParseLedger([]byte(md))
	if err != nil {
		t.Fatalf("ParseLedger: %v", err)
	}
	if len(as) != 1 {
		t.Errorf("the real row should survive: %v", as)
	}
	wantFailures(t, bad, []string{"indented into a code block"})
}
