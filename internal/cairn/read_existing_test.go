package cairn

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestFlatReadMetadataOrderingAndNestedPrecedence(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	raw := "# Own heading\n\n## 2026-09-28T02:00:00Z — late\n\n  late prose  \n\n## 2026-09-28T01:00:00Z — early\n\nearly\n"
	if err := os.WriteFile(benchFile(store, "flat"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 1, 30, 0, 0, time.UTC)
	if err := Open(store, "nested", "src", now, PublishNever); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(store, "nested", "middle", "nested prose", "", now, PublishNever); err != nil {
		t.Fatal(err)
	}
	rows, total, err := Index(store, "", 0)
	if err != nil || total != 3 || len(rows) != 3 {
		t.Fatalf("index=%+v total=%d err=%v", rows, total, err)
	}
	if rows[0].ID != "early" || rows[1].ID != "middle" || rows[2].ID != "late" {
		t.Fatalf("order=%+v", rows)
	}
	rc, err := Receipt(store, "flat", "late")
	if err != nil || rc.Bytes != len("late prose") || rc.Policy != "unknown" || rc.Source != "" {
		t.Fatalf("flat metadata=%+v err=%v", rc, err)
	}
	if got := Coverage(store); got.Sessions != 2 || got.Entries != 3 {
		t.Fatalf("coverage=%+v", got)
	}
	// A flat duplicate of a nested session is not a second record and cannot
	// replace its entry metadata, even if its own contents are malformed.
	if err := os.WriteFile(benchFile(store, "nested"), []byte("## 2026-99-28T01:00:00Z — bad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, total, err = Index(store, "nested", 0)
	if err != nil || total != 1 || rows[0].Source != "src" {
		t.Fatalf("nested precedence=%+v total=%d err=%v", rows, total, err)
	}
	if got := Coverage(store); got.Sessions != 2 || got.Entries != 3 {
		t.Fatalf("duplicate coverage=%+v", got)
	}
	rc, err = Receipt(store, "nested", "middle")
	if err != nil || rc.Source != "src" || rc.Policy != PublishNever {
		t.Fatalf("nested receipt=%+v err=%v", rc, err)
	}
}

func TestFlatReadersRefuseCorruptAndAmbiguousHeadings(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"## 2026-99-28T01:00:00Z — e\n\nnote\n",
		"## 2026-09-28T01:00:00Z — ../e\n\nnote\n",
		"## 2026-09-28T01:00:00Z — e\n\na\n\n## 2026-09-28T02:00:00Z — e\n\nb\n",
	} {
		store := t.TempDir()
		if err := os.WriteFile(benchFile(store, "s"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Index(store, "", 0); err == nil {
			t.Errorf("index accepted %q", raw)
		}
		if _, err := Receipt(store, "s", "e"); err == nil {
			t.Errorf("receipt accepted %q", raw)
		}
	}
}

func TestFlatReadersKeepUnstructuredProseAndMissingEntriesDistinct(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	raw := "# My record\n\n## A manually dated note\n\nwords\n"
	if err := os.WriteFile(benchFile(store, "s"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	rows, total, err := Index(store, "s", 0)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("unstructured prose=%v %d %v", rows, total, err)
	}
	if _, err := Receipt(store, "s", "e"); err == nil || !strings.Contains(err.Error(), "no such entry") {
		t.Fatalf("missing entry=%v", err)
	}
}
