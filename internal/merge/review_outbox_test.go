package merge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPolicyOutboxIsConfinedAndManifested(t *testing.T) {
	lane := t.TempDir()
	r := NewRecords(lane, "records", "origin", NewGit(lane, time.Second, Exec{}), time.Second)
	s := Submission{At: "2026-09-14T01:02:03Z", Rand: "abcdef"}
	file := PolicyFile("951", "emma", "0123456789abcdef0123456789abcdef01234567", s)
	body := []byte(`{"file":"` + file + `"}`)
	if err := r.writeOutbox(s, []Item{{Part: "policy", Path: file, Body: body}}); err != nil {
		t.Fatal(err)
	}
	items, taken, err := r.outbox()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Part != "policy" || items[0].Path != file {
		t.Fatalf("items=%+v", items)
	}
	if len(taken) != 2 {
		t.Fatalf("want part and manifest, got %q", taken)
	}
	if _, err := os.Stat(filepath.Join(lane, OutboxDir, s.ID()+"-parts.json")); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyCannotEscapeReviews(t *testing.T) {
	for _, file := range []string{"reads/951/policy.json", "gates/951/policy.json", "../reviews/951/policy.json", "/tmp/policy.json"} {
		if err := validPart("policy", file); err == nil {
			t.Errorf("policy destination %q was accepted", file)
		}
	}
}
