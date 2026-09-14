package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPatchHeaderUsesNameStatusForUnquotedSpacesAndQuotedControls(t *testing.T) {
	space := patchName{Old: "a b/x b/y.txt", New: "a b/x b/y.txt"}
	if _, got, err := parseDiffHeader("diff --git a/a b/x b/y.txt b/a b/x b/y.txt", space); err != nil || got != space.New {
		t.Fatalf("unquoted ambiguous header path=%q err=%v", got, err)
	}
	quoted := patchName{Old: "old\tname", New: "new\nname"}
	if old, got, err := parseDiffHeader("diff --git \"a/old\\tname\" \"b/new\\nname\"", quoted); err != nil || old != quoted.Old || got != quoted.New {
		t.Fatalf("quoted header old=%q new=%q err=%v", old, got, err)
	}
	if _, _, err := parseDiffHeader("diff --git a/a b/x b/y.txt b/a b/x b/y.txt", patchName{Old: "other", New: "other"}); err == nil {
		t.Fatal("ambiguous header accepted a different name-status pair")
	}
}

func TestPatchReaderStreamsLargePayloadWithoutRetainingIt(t *testing.T) {
	giant := strings.Repeat("x", 2*1024*1024)
	patch := "diff --git a/a b/x b/y.txt b/a b/x b/y.txt\n" +
		"--- a/a b/x b/y.txt\n+++ b/a b/x b/y.txt\n@@ -0,0 +1 @@\n+" + giant + "\n" +
		"diff --git a/tiny.txt b/tiny.txt\n--- a/tiny.txt\n+++ b/tiny.txt\n@@ -0,0 +1 @@\n+tiny\n"
	p := newPatchReader(nil, 0, nil, nil, []patchName{
		{Old: "a b/x b/y.txt", New: "a b/x b/y.txt"},
		{Old: "tiny.txt", New: "tiny.txt"},
	})
	// Feed deliberately uneven chunks: the line is much larger than a scanner
	// token, but the inventory remains file/hunk metadata only.
	for rest := []byte(patch); len(rest) > 0; {
		n := 7919
		if n > len(rest) {
			n = len(rest)
		}
		if got, err := p.Write(rest[:n]); err != nil || got != n {
			t.Fatalf("Write(%d) got=%d err=%v", n, got, err)
		}
		rest = rest[n:]
	}
	if err := p.finish(); err != nil {
		t.Fatal(err)
	}
	if len(p.files) != 2 || p.files[0].Added != 1 || p.files[1].Added != 1 || p.files[0].Hunks != 1 || p.payload.Len() != 0 {
		t.Fatalf("inventory lost count or retained payload: %#v payload=%d", p.files, p.payload.Len())
	}
	if p.files[0].FullText != "" || p.files[0].Bytes <= int64(len(giant)) {
		t.Fatalf("large source was retained or mismeasured: bytes=%d", p.files[0].Bytes)
	}
}

func TestPatchReaderSelectedLargeLineRetainsOnlyBudgetedOutput(t *testing.T) {
	line := strings.Repeat("x", patchControlPrefix+1)
	patch := "diff --git a/a.txt b/a.txt\n@@ -0,0 +1 @@\n+" + line + "\n"
	p := newPatchReader([]bool{true}, len(patch), nil, nil, []patchName{{Old: "a.txt", New: "a.txt"}})
	if _, err := p.Write([]byte(patch)); err != nil {
		t.Fatal(err)
	}
	if err := p.finish(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.payload.Bytes(), []byte(patch)) {
		t.Fatalf("selected payload changed at large-line boundary: %d bytes", p.payload.Len())
	}
}

func TestPatchReaderDoesNotChargeFollowingUnselectedHeaderAtExactBudget(t *testing.T) {
	first := "diff --git a/a.txt b/a.txt\n@@ -0,0 +1 @@\n+one\n"
	second := "diff --git a/b.txt b/b.txt\n@@ -0,0 +1 @@\n+two\n"
	p := newPatchReader([]bool{true, false}, len(first), nil, nil, []patchName{{Old: "a.txt", New: "a.txt"}, {Old: "b.txt", New: "b.txt"}})
	if _, err := p.Write([]byte(first + second)); err != nil {
		t.Fatalf("following unselected header consumed selected budget: %v", err)
	}
	if err := p.finish(); err != nil {
		t.Fatal(err)
	}
	if got := p.payload.String(); got != first {
		t.Fatalf("selected payload=%q, want first file only %q", got, first)
	}
}

func TestPatchReaderCountsHunkPayloadPrefixesAndDoesNotAdvanceNoNewlineMarker(t *testing.T) {
	spec, err := parseScopedSpec("docs/SPEC.md", "## Rules\n1. rule\n", "Rules")
	if err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/docs/SPEC.md b/docs/SPEC.md\n" +
		"--- a/docs/SPEC.md\n+++ b/docs/SPEC.md\n" +
		"@@ -1,2 +1,3 @@\n---removed\n+++added\n line one\n\\ No newline at end of file\n+// rule 1\n"
	p := newPatchReader(nil, 0, []scopedSpec{spec}, nil, []patchName{{Old: "docs/SPEC.md", New: "docs/SPEC.md"}})
	if _, err := p.Write([]byte(patch)); err != nil {
		t.Fatal(err)
	}
	if err := p.finish(); err != nil {
		t.Fatal(err)
	}
	f := p.files[0]
	if f.Added != 2 || f.Deleted != 1 {
		t.Fatalf("hunk payload prefix was treated as metadata: +%d -%d", f.Added, f.Deleted)
	}
	if len(f.ChangedHead) != 1 || f.ChangedHead[0].Number != 1 {
		t.Fatalf("no-newline marker advanced coordinates and lost changed rule: %#v", f.ChangedHead)
	}
}

func TestPatchReaderFindsScopedCitationPastBoundedPrefix(t *testing.T) {
	spec, err := parseScopedSpec("docs/SPEC.md", "## Rules\n1. rule\n", "Rules")
	if err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/a.txt b/a.txt\n@@ -0,0 +1 @@\n+" + strings.Repeat("x", patchControlPrefix+1) + " rule 1\n"
	p := newPatchReader(nil, 0, []scopedSpec{spec}, nil, []patchName{{Old: "a.txt", New: "a.txt"}})
	if _, err := p.Write([]byte(patch)); err != nil {
		t.Fatal(err)
	}
	if err := p.finish(); err != nil {
		t.Fatalf("oversized cited line was not streamed: %v", err)
	}
	if got := p.files[0].Cited; len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("oversized cited line lost Rule 2 target: %#v", got)
	}
}

func TestPatchNameStatusAndReaderCoverRenameAndBinaryWithoutFileMarkers(t *testing.T) {
	var names patchNameReader
	if _, err := names.Write([]byte("R100\x00old b/x\x00new b/y\x00M\x00image.bin\x00")); err != nil {
		t.Fatal(err)
	}
	if err := names.finish(); err != nil || len(names.names) != 2 || names.names[0] != (patchName{Old: "old b/x", New: "new b/y"}) {
		t.Fatalf("rename name-status=%#v err=%v", names.names, err)
	}
	patch := "diff --git a/old b/x b/new b/y\n" +
		"similarity index 100%\nrename from old b/x\nrename to new b/y\n" +
		"diff --git a/image.bin b/image.bin\nBinary files a/image.bin and b/image.bin differ\n"
	p := newPatchReader(nil, 0, nil, nil, names.names)
	if _, err := p.Write([]byte(patch)); err != nil {
		t.Fatal(err)
	}
	if err := p.finish(); err != nil {
		t.Fatal(err)
	}
	if len(p.files) != 2 || p.files[0].Path != "new b/y" || p.files[0].Hunks != 0 || p.files[1].Path != "image.bin" || p.files[1].Added != 0 || p.files[1].Deleted != 0 {
		t.Fatalf("rename/binary metadata was guessed or lost: %#v", p.files)
	}
}

func TestPatchNameReaderBoundsUnterminatedPath(t *testing.T) {
	var names patchNameReader
	if _, err := names.Write([]byte("M\x00" + strings.Repeat("x", patchNameTokenCap+1))); err == nil {
		t.Fatal("unterminated oversized -z path was retained")
	}
}

func TestPatchReaderGroupsTwoTypeChangeHeadersAsOneWholeFile(t *testing.T) {
	patch := "diff --git a/link.txt b/link.txt\n" +
		"deleted file mode 100644\nindex e69de29..0000000\n" +
		"diff --git a/link.txt b/link.txt\n" +
		"new file mode 120000\nindex 0000000..1de5659\n"
	p := newPatchReader(nil, 0, nil, nil, []patchName{{Old: "link.txt", New: "link.txt", TypeChange: true}})
	if _, err := p.Write([]byte(patch)); err != nil {
		t.Fatal(err)
	}
	if err := p.finish(); err != nil {
		t.Fatal(err)
	}
	if len(p.files) != 1 || p.files[0].Bytes != int64(len(patch)) || p.files[0].Path != "link.txt" {
		t.Fatalf("type change was not one whole-file group: %#v", p.files)
	}
}

func TestTwoPinnedPatchReadsDetectChangedSource(t *testing.T) {
	count := strings.ReplaceAll(t.TempDir()+"/count", "'", "'\\''")
	fake := packetFakeCommand(t, `
for arg in "$@"; do
  if [ "$arg" = "--name-status" ]; then printf 'M\000a.txt\000'; exit 0; fi
done
n=0
if [ -f '`+count+`' ]; then n=$(cat '`+count+`'); fi
n=$((n + 1)); printf '%s' "$n" > '`+count+`'
if [ "$n" = 1 ]; then printf 'diff --git a/a.txt b/a.txt\n@@ -0,0 +1 @@\n+one\n'; else printf 'diff --git a/a.txt b/a.txt\n@@ -0,0 +1 @@\n+two\n'; fi`)
	withPacketSourceBinaries(t, fake, packetGHBinary)
	first, _, err := readPatch(time.Second, t.TempDir(), "a..b", nil, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := readPatch(time.Second, t.TempDir(), "a..b", nil, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if samePatch(first, second) {
		t.Fatal("changed second raw patch had the same inventory hash")
	}
}
