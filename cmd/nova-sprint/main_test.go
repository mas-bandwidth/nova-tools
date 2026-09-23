package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runSprint(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestBareCommandNamesTheDoor(t *testing.T) {
	code, stdout, stderr := runSprint()
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout %q; a refusal belongs on stderr", stdout)
	}
	if !strings.Contains(stderr, "run: nova-sprint help") {
		t.Fatalf("stderr %q, want the help door", stderr)
	}
	if strings.Count(stderr, "\n") != 1 {
		t.Fatalf("a bare command printed more than one line:\n%s", stderr)
	}
}

func TestTableRefusalNamesEveryMissingPiece(t *testing.T) {
	code, _, stderr := runSprint("table")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"--once", "--fixture", "--refresh pending", "--out", "run: nova-sprint help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestEmptyFixtureRefusesToBlank(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runSprint("table", "--once", "--fixture", empty, "--out", filepath.Join(dir, "out.txt"))
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr %s", code, stderr)
	}
	if !strings.Contains(stderr, "empty") {
		t.Fatalf("stderr %q, want it to say the fixture is empty", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); !os.IsNotExist(err) {
		t.Fatal("an empty fixture published a table")
	}
}

func TestSecondStartDoesNotClearThePreviousTable(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join("testdata", "table.txt")
	want, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes.TrimSpace(want)) == 0 {
		t.Fatal("fixture is empty")
	}
	out := filepath.Join(dir, "sprint-table.txt")

	code, published, stderr := runSprint("table", "--once", "--fixture", fixture, "--out", out)
	if code != 0 {
		t.Fatalf("publish exit %d; stderr %s", code, stderr)
	}
	if !strings.Contains(published, "TABLE PUBLISHED") {
		t.Fatalf("stdout %q, want TABLE PUBLISHED", published)
	}

	code, kept, stderr := runSprint("table", "--once", "--refresh", "pending", "--out", out)
	if code != 0 {
		t.Fatalf("second start exit %d; stderr %s", code, stderr)
	}
	if !strings.Contains(kept, "TABLE KEPT") || !strings.Contains(kept, "reason=refresh-not-ready") {
		t.Fatalf("stdout %q, want TABLE KEPT reason=refresh-not-ready", kept)
	}
	if !strings.Contains(kept, "SPRINT TABLE") {
		t.Fatal("second start did not show the previous table")
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("second start changed the published table (%d bytes -> %d)", len(want), len(got))
	}
	if len(got) == 0 {
		t.Fatal("published table is empty")
	}
}

func TestFixtureDryRenderIsByteIdentical(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "table.txt"))
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runSprint("table", "--once", "--fixture", filepath.Join("testdata", "table.txt"))
	if code != 0 {
		t.Fatalf("exit %d; stderr %s", code, stderr)
	}
	if stdout != string(want) {
		t.Fatalf("dry render is not the fixture\n got %q\nwant %q", stdout, want)
	}
}

func TestRefreshOwnSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		code, _, stderr := runSprint("refresh", "--", "true")
		if code != 2 || !strings.Contains(stderr, "setsid") {
			t.Fatalf("windows refresh exit %d stderr %q, want a setsid refusal", code, stderr)
		}
		return
	}
	code, stdout, stderr := runSprint("refresh", "--", "/usr/bin/true")
	if code != 0 {
		t.Fatalf("exit %d; stderr %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "REFRESH SESSION pid=") {
		t.Fatalf("stdout %q, want REFRESH SESSION pid=", stdout)
	}
}

func TestRefreshRefusesAMissingCommand(t *testing.T) {
	code, _, stderr := runSprint("refresh")
	if code != 2 || !strings.Contains(stderr, "--") {
		t.Fatalf("exit %d stderr %q, want a refusal that names --", code, stderr)
	}
}
