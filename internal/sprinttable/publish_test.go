package sprinttable

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const previousTable = "SPRINT TABLE\n\nalpha | 1\n"

func TestSecondStartDoesNotClearThePreviousTable(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "SPRINT-TABLE.txt")
	if err := os.WriteFile(out, []byte(previousTable), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Publish(out, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Kept || got.Wrote {
		t.Fatalf("second start wrote the table: %+v", got)
	}
	if got.Reason != ReasonRefreshNotReady {
		t.Fatalf("reason %q, want %s", got.Reason, ReasonRefreshNotReady)
	}
	assertFile(t, out, previousTable)
	if !bytes.Equal(got.Body, []byte(previousTable)) {
		t.Fatalf("showed %q, want the previous table", got.Body)
	}

	// A finished-but-empty render is still a blank screen. Keep the last one.
	got, err = Publish(out, []byte(" \n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Kept || got.Reason != ReasonEmptyRender {
		t.Fatalf("empty render was published: %+v", got)
	}
	assertFile(t, out, previousTable)

	next := "SPRINT TABLE\n\nbeta | 2\n"
	got, err = Publish(out, []byte(next), true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Wrote || got.Kept {
		t.Fatalf("a ready render did not publish: %+v", got)
	}
	assertFile(t, out, next)
	if leftover := temps(t, dir); len(leftover) != 0 {
		t.Fatalf("temp files left beside the table: %v", leftover)
	}
}

func TestUnreadyStartDoesNotCreateAnEmptyTable(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "SPRINT-TABLE.txt")
	got, err := Publish(out, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Kept || len(got.Body) != 0 {
		t.Fatalf("invented a table: %+v", got)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("created %s (%v); a start with nothing ready must not blank a new file into place", out, err)
	}
}

func TestFixturePublishesByteIdentical(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "SPRINT-TABLE.txt")
	const fixture = "SPRINT TABLE\n\nalpha | 1\n"
	got, err := Publish(out, []byte(fixture), true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Body, []byte(fixture)) {
		t.Fatalf("body %q, want the fixture bytes", got.Body)
	}
	assertFile(t, out, fixture)
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s is %q, want %q", path, got, want)
	}
	if len(got) == 0 {
		t.Fatal("table is empty")
	}
}

func temps(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Name() != "SPRINT-TABLE.txt" {
			out = append(out, e.Name())
		}
	}
	return out
}
