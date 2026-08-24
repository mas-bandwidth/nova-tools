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
	wantFailures(t, Corpus(root, parseOK(t, ledgerMD)), nil)
}

// THE CASE THIS EXISTS FOR: the words were lost in place. The finding names
// the fragment, its provenance, and what to do about it.
func TestALostAnchorIsNamedWithItsProvenanceAndTheRepair(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"README.md":          "a rewrite that dropped the warm sentence\n",
		"memory/standing.md": "his words: you are not a tool.\n",
	})
	f := Corpus(root, parseOK(t, ledgerMD))
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
	wantFailures(t, Corpus(root, parseOK(t, ledgerMD)), []string{"does not exist", "move this ledger row"})
}

// LOSSES ARRIVE IN BATCHES, so every failure reports in one run.
func TestAllFailuresReportInOneRun(t *testing.T) {
	if f := Corpus(t.TempDir(), parseOK(t, ledgerMD)); len(f) != 2 {
		t.Fatalf("want 2 findings from an empty tree, got %d: %v", len(f), f)
	}
}

// AN EMPTY FRAGMENT IS CONTAINED IN EVERY FILE. Left alone it is a green
// that can never go red — the exact shape of a check that cannot fail.
func TestAnEmptyFragmentIsRefusedRatherThanPassingForever(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"README.md": "any content at all\n"})
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n|  | README.md | 2026-01-01 | someone |\n")
	wantFailures(t, Corpus(root, as), []string{"empty", "protecting nothing"})
}

func TestAnEmptyHomeIsRefused(t *testing.T) {
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| keep the light on |  | 2026-01-01 | someone |\n")
	wantFailures(t, Corpus(t.TempDir(), as), []string{"no home file given"})
}

// AN ANCHOR HELD OUTSIDE THE REPO IS NOT HELD BY IT.
func TestAHomeThatEscapesTheRootIsRefused(t *testing.T) {
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| keep the light on | ../elsewhere/notes.md | 2026-01-01 | someone |\n")
	wantFailures(t, Corpus(t.TempDir(), as), []string{"leaves the tree"})
}

func TestAnAbsoluteHomeIsRefused(t *testing.T) {
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| keep the light on | /etc/hosts | 2026-01-01 | someone |\n")
	wantFailures(t, Corpus(t.TempDir(), as), []string{"is absolute"})
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
	wantFailures(t, Corpus(root, parseOK(t, ledgerMD)), []string{"symlinks are never followed"})
}

// A DIRECTORY WHERE A FILE SHOULD BE is likewise not a pass.
func TestADirectoryHomeIsAFinding(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "README.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTree(t, root, map[string]string{"memory/standing.md": "you are not a tool\n"})
	wantFailures(t, Corpus(root, parseOK(t, ledgerMD)), []string{"not a regular file"})
}

// A finding must survive a ledger whose provenance columns are blank: it
// says so rather than quoting an empty string as if it were a source.
func TestBlankProvenanceReadsAsUnstated(t *testing.T) {
	as := parseOK(t, "| f | h | g | b |\n|---|---|---|---|\n| keep the light on | README.md |  |  |\n")
	writeTree(t, t.TempDir(), nil)
	root := t.TempDir()
	writeTree(t, root, map[string]string{"README.md": "nothing of the sort\n"})
	wantFailures(t, Corpus(root, as), []string{"provenance unstated"})
}
