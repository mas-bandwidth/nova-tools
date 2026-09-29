package bus

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestPlanArchive(t *testing.T) {
	root := writeBus(t, nil)
	c, _ := LoadConfig(root)

	// Note 1: 2026-09-01 (older)
	writeNoteWithDate(t, root, "from-ada", "ada-111111111111", "2026-09-01T12:00:00Z", "Older Ada note")
	// Note 2: 2026-09-05 (older)
	writeNoteWithDate(t, root, "from-bo", "bo-222222222222", "2026-09-05T12:00:00Z", "Older Bo note")
	// Note 3: 2026-09-10 (newer)
	writeNoteWithDate(t, root, "from-bo", "bo-333333333333", "2026-09-10T12:00:00Z", "Newer Bo note")

	tab, err := ReadBus(root, c)
	if err != nil {
		t.Fatalf("ReadBus: %v", err)
	}

	before, _ := time.Parse(time.RFC3339, "2026-09-08T00:00:00Z")
	plan, err := PlanArchive(tab, before, filepath.Join(root, "archive"))
	if err != nil {
		t.Fatalf("PlanArchive: %v", err)
	}

	if plan.ArchivedCount != 2 {
		t.Errorf("ArchivedCount = %d, want 2", plan.ArchivedCount)
	}
	if plan.KeptCount != 1 {
		t.Errorf("KeptCount = %d, want 1", plan.KeptCount)
	}
	if len(plan.TouchedLanes) != 2 {
		t.Errorf("TouchedLanes = %v, want 2 lanes", plan.TouchedLanes)
	}
}

func TestArchiveExecutionDirectory(t *testing.T) {
	root := writeBus(t, nil)
	c, _ := LoadConfig(root)

	// Write 2 older notes and 1 newer note
	p1 := writeNoteWithDate(t, root, "from-ada", "ada-111111111111", "2026-09-01T12:00:00Z", "Older Ada note")
	p2 := writeNoteWithDate(t, root, "from-bo", "bo-222222222222", "2026-09-05T12:00:00Z", "Older Bo note")
	p3 := writeNoteWithDate(t, root, "from-bo", "bo-333333333333", "2026-09-10T12:00:00Z", "Newer Bo note")

	// Set up lane INDEX files
	RebuildLaneIndex(root, c, mustReadBus(t, root, c), "from-ada")
	RebuildLaneIndex(root, c, mustReadBus(t, root, c), "from-bo")

	// Record a receipt in Ada's lane for bo-222222222222
	now := time.Now().UTC()
	tab := mustReadBus(t, root, c)
	ada, _ := c.Lookup("Ada")
	rplan, err := PlanReceipts(tab, ada, []string{"bo-222222222222"}, now)
	if err != nil {
		t.Fatalf("PlanReceipts: %v", err)
	}
	if err := rplan.Append(root); err != nil {
		t.Fatalf("rplan.Append: %v", err)
	}

	// Set up an OPEN list in Ada's lane carrying bo-222222222222
	adaOpen := []OpenEntry{
		{
			ID:      "bo-222222222222",
			Kind:    OpenNote,
			Heard:   true,
			From:    "Bo",
			Addr:    "to",
			Date:    "2026-09-05T12:00:00Z",
			Path:    p2,
			Subject: "Older Bo note",
		},
		{
			ID:      "bo-333333333333",
			Kind:    OpenNote,
			Heard:   false,
			From:    "Bo",
			Addr:    "to",
			Date:    "2026-09-10T12:00:00Z",
			Path:    p3,
			Subject: "Newer Bo note",
		},
	}
	if err := WriteOpen(root, "from-ada", adaOpen); err != nil {
		t.Fatalf("WriteOpen: %v", err)
	}
	cursorCommit := "0123456789abcdef0123456789abcdef01234567"
	if err := WriteCursor(root, "from-ada", cursorCommit, 2, "", now); err != nil {
		t.Fatalf("WriteCursor: %v", err)
	}

	tab = mustReadBus(t, root, c)
	before, _ := time.Parse(time.RFC3339, "2026-09-08T00:00:00Z")
	target := filepath.Join(root, "archive")
	plan, err := PlanArchive(tab, before, target)
	if err != nil {
		t.Fatalf("PlanArchive: %v", err)
	}

	res, err := plan.Execute(root)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if res.Archived != 2 || res.Kept != 1 {
		t.Errorf("Archived = %d, Kept = %d, want 2 and 1", res.Archived, res.Kept)
	}

	// Verify files moved out of active lanes
	if _, err := os.Stat(filepath.Join(root, p1)); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed, but it exists", p1)
	}
	if _, err := os.Stat(filepath.Join(root, p2)); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed, but it exists", p2)
	}
	// Verify p3 is kept
	if _, err := os.Stat(filepath.Join(root, p3)); err != nil {
		t.Errorf("expected %s to remain, err: %v", p3, err)
	}

	// Verify archived files exist in archive/
	if _, err := os.Stat(filepath.Join(target, p1)); err != nil {
		t.Errorf("expected %s in archive, err: %v", p1, err)
	}
	if _, err := os.Stat(filepath.Join(target, p2)); err != nil {
		t.Errorf("expected %s in archive, err: %v", p2, err)
	}

	// Verify archive/INDEX exists and has 2 entries
	archIndexEntries, err := ReadLaneIndex(root, "archive")
	if err != nil {
		t.Fatalf("ReadLaneIndex archive: %v", err)
	}
	if len(archIndexEntries) != 2 {
		t.Errorf("archive/INDEX entries = %d, want 2", len(archIndexEntries))
	}

	// Verify lane INDEX has only kept notes
	boIndexEntries, err := ReadLaneIndex(root, "from-bo")
	if err != nil {
		t.Fatalf("ReadLaneIndex from-bo: %v", err)
	}
	if len(boIndexEntries) != 1 || boIndexEntries[0].ID != "bo-333333333333" {
		t.Errorf("from-bo/INDEX entries = %v, want only bo-333333333333", boIndexEntries)
	}

	// Verify Ada's OPEN list updated path for archived note
	adaOpenUpdated, err := ReadOpen(root, "from-ada")
	if err != nil {
		t.Fatalf("ReadOpen from-ada: %v", err)
	}
	if len(adaOpenUpdated) != 2 {
		t.Fatalf("ada open entries = %d, want 2", len(adaOpenUpdated))
	}
	if !strings.HasPrefix(adaOpenUpdated[0].Path, "archive/") {
		t.Errorf("adaOpenUpdated[0].Path = %q, want prefix 'archive/'", adaOpenUpdated[0].Path)
	}
	if adaOpenUpdated[1].Path != p3 {
		t.Errorf("adaOpenUpdated[1].Path = %q, want %q", adaOpenUpdated[1].Path, p3)
	}

	// Verify cursor commit is preserved
	cursor, err := ReadCursor(root, "from-ada")
	if err != nil {
		t.Fatalf("ReadCursor: %v", err)
	}
	if cursor.Commit != cursorCommit {
		t.Errorf("Cursor commit = %q, want %q", cursor.Commit, cursorCommit)
	}

	// Verify ReadBus and Resolve: receipt for archived note resolves cleanly
	tabAfter := mustReadBus(t, root, c)
	if n, ok := tabAfter.Resolve("bo-222222222222"); !ok || n == nil {
		t.Errorf("Resolve(bo-222222222222) failed to resolve archived note")
	}

	// Run bus check: must pass with 0 problems
	problems := tabAfter.Check()
	for _, p := range problems {
		t.Errorf("unexpected check problem: %s: %s", p.Where, p.Reason)
	}
}

func TestArchiveExecutionTarball(t *testing.T) {
	root := writeBus(t, nil)
	c, _ := LoadConfig(root)

	p1 := writeNoteWithDate(t, root, "from-ada", "ada-111111111111", "2026-09-01T12:00:00Z", "Older Ada note")
	p2 := writeNoteWithDate(t, root, "from-bo", "bo-222222222222", "2026-09-05T12:00:00Z", "Older Bo note")
	p3 := writeNoteWithDate(t, root, "from-bo", "bo-333333333333", "2026-09-10T12:00:00Z", "Newer Bo note")

	RebuildLaneIndex(root, c, mustReadBus(t, root, c), "from-ada")
	RebuildLaneIndex(root, c, mustReadBus(t, root, c), "from-bo")

	tab := mustReadBus(t, root, c)
	before, _ := time.Parse(time.RFC3339, "2026-09-08T00:00:00Z")
	tarPath := filepath.Join(root, "archive.tar.gz")
	plan, err := PlanArchive(tab, before, tarPath)
	if err != nil {
		t.Fatalf("PlanArchive: %v", err)
	}
	if !plan.IsTarball {
		t.Fatal("expected IsTarball = true")
	}

	res, err := plan.Execute(root)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Archived != 2 || res.Kept != 1 {
		t.Errorf("Archived = %d, Kept = %d, want 2 and 1", res.Archived, res.Kept)
	}

	// Verify original files removed
	if _, err := os.Stat(filepath.Join(root, p1)); !os.IsNotExist(err) {
		t.Errorf("expected %s removed", p1)
	}
	if _, err := os.Stat(filepath.Join(root, p2)); !os.IsNotExist(err) {
		t.Errorf("expected %s removed", p2)
	}
	if _, err := os.Stat(filepath.Join(root, p3)); err != nil {
		t.Errorf("expected %s kept", p3)
	}

	// Verify tarball contents
	tf, err := os.Open(tarPath)
	if err != nil {
		t.Fatalf("open tarball: %v", err)
	}
	defer tf.Close()
	gz, err := gzip.NewReader(tf)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	var foundFiles []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		foundFiles = append(foundFiles, hdr.Name)
	}
	sort.Strings(foundFiles)
	wantFiles := []string{p1, p2}
	sort.Strings(wantFiles)
	if strings.Join(foundFiles, ",") != strings.Join(wantFiles, ",") {
		t.Errorf("tarball files = %v, want %v", foundFiles, wantFiles)
	}

	// Verify archive/INDEX resolves archived notes
	tabAfter := mustReadBus(t, root, c)
	if n, ok := tabAfter.Resolve("ada-111111111111"); !ok || n == nil {
		t.Errorf("Resolve(ada-111111111111) failed")
	}
}

func TestPlanArchiveNoOldNotes(t *testing.T) {
	root := writeBus(t, nil)
	c, _ := LoadConfig(root)
	writeNoteWithDate(t, root, "from-bo", "bo-333333333333", "2026-09-10T12:00:00Z", "Newer note")

	tab := mustReadBus(t, root, c)
	before, _ := time.Parse(time.RFC3339, "2026-09-01T00:00:00Z")
	plan, err := PlanArchive(tab, before, filepath.Join(root, "archive"))
	if err != nil {
		t.Fatalf("PlanArchive: %v", err)
	}
	if plan.ArchivedCount != 0 {
		t.Errorf("ArchivedCount = %d, want 0", plan.ArchivedCount)
	}
	if plan.KeptCount != 1 {
		t.Errorf("KeptCount = %d, want 1", plan.KeptCount)
	}
	res, err := plan.Execute(root)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Archived != 0 {
		t.Errorf("res.Archived = %d, want 0", res.Archived)
	}
}

func TestPlanArchiveUnparsableDateKept(t *testing.T) {
	root := writeBus(t, nil)
	c, _ := LoadConfig(root)
	// Write a note whose date cannot be parsed
	unparsablePath := "from-bo/undated-note.md"
	content := "From: Bo\nTo: Ada\nDate: not a date\nId: bo-undated1234\nSubject: Undated\n\nContent.\n"
	write(t, root, unparsablePath, content)

	tab := mustReadBus(t, root, c)
	before, _ := time.Parse(time.RFC3339, "2026-09-15T00:00:00Z")
	plan, err := PlanArchive(tab, before, filepath.Join(root, "archive"))
	if err != nil {
		t.Fatalf("PlanArchive: %v", err)
	}
	// Note with unparsable date cannot claim to predate anything, so it must be kept.
	if plan.ArchivedCount != 0 {
		t.Errorf("ArchivedCount = %d, want 0", plan.ArchivedCount)
	}
	if plan.KeptCount != 1 {
		t.Errorf("KeptCount = %d, want 1", plan.KeptCount)
	}
}

func TestArchiveMultipleRunsAccumulate(t *testing.T) {
	root := writeBus(t, nil)
	c, _ := LoadConfig(root)

	writeNoteWithDate(t, root, "from-ada", "ada-111111111111", "2026-08-01T12:00:00Z", "August note")
	writeNoteWithDate(t, root, "from-bo", "bo-222222222222", "2026-09-01T12:00:00Z", "September note")
	writeNoteWithDate(t, root, "from-bo", "bo-333333333333", "2026-10-01T12:00:00Z", "October note")

	RebuildLaneIndex(root, c, mustReadBus(t, root, c), "from-ada")
	RebuildLaneIndex(root, c, mustReadBus(t, root, c), "from-bo")

	target := filepath.Join(root, "archive")

	// Run 1: Archive before 2026-08-15 (archives August note)
	tab1 := mustReadBus(t, root, c)
	before1, _ := time.Parse(time.RFC3339, "2026-08-15T00:00:00Z")
	plan1, err := PlanArchive(tab1, before1, target)
	if err != nil {
		t.Fatalf("PlanArchive 1: %v", err)
	}
	if plan1.ArchivedCount != 1 {
		t.Fatalf("plan1 ArchivedCount = %d, want 1", plan1.ArchivedCount)
	}
	if _, err := plan1.Execute(root); err != nil {
		t.Fatalf("Execute 1: %v", err)
	}

	// Run 2: Archive before 2026-09-15 (archives September note)
	tab2 := mustReadBus(t, root, c)
	before2, _ := time.Parse(time.RFC3339, "2026-09-15T00:00:00Z")
	plan2, err := PlanArchive(tab2, before2, target)
	if err != nil {
		t.Fatalf("PlanArchive 2: %v", err)
	}
	if plan2.ArchivedCount != 1 {
		t.Fatalf("plan2 ArchivedCount = %d, want 1", plan2.ArchivedCount)
	}
	if _, err := plan2.Execute(root); err != nil {
		t.Fatalf("Execute 2: %v", err)
	}

	// Verify archive/INDEX has both entries
	archIndexEntries, err := ReadLaneIndex(root, "archive")
	if err != nil {
		t.Fatalf("ReadLaneIndex archive: %v", err)
	}
	if len(archIndexEntries) != 2 {
		t.Fatalf("archive/INDEX entries = %d, want 2", len(archIndexEntries))
	}

	// Verify both resolve in ReadBus
	tab3 := mustReadBus(t, root, c)
	if n, ok := tab3.Resolve("ada-111111111111"); !ok || n == nil {
		t.Errorf("Resolve(ada-111111111111) failed")
	}
	if n, ok := tab3.Resolve("bo-222222222222"); !ok || n == nil {
		t.Errorf("Resolve(bo-222222222222) failed")
	}
	if n, ok := tab3.Resolve("bo-333333333333"); !ok || n == nil {
		t.Errorf("Resolve(bo-333333333333) failed")
	}
}

func writeNoteWithDate(t *testing.T, root, lane, id, date, subject string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, lane), 0755); err != nil {
		t.Fatal(err)
	}
	parsedDate, err := time.Parse(time.RFC3339, date)
	if err != nil {
		t.Fatal(err)
	}
	dateHeader := parsedDate.Format(DateLayout)
	filename := fmt.Sprintf("%s-%s.md", parsedDate.Format(FileTimeLayout), id)
	relPath := lane + "/" + filename
	content := fmt.Sprintf("From: %s\nTo: Ada\nDate: %s\nId: %s\nSubject: %s\n\nContent.\n",
		strings.TrimPrefix(lane, "from-"), dateHeader, id, subject)
	if err := os.WriteFile(filepath.Join(root, relPath), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return relPath
}

func mustReadBus(t *testing.T, root string, c *Config) *Bus {
	t.Helper()
	tab, err := ReadBus(root, c)
	if err != nil {
		t.Fatalf("ReadBus: %v", err)
	}
	return tab
}
