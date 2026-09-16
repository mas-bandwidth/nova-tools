package update

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The verbs block is the one place help and the dispatcher meet; every verb the
// switch dispatches is listed beside the others, including snapshot's new
// --bin/--out shape and diff, and the #622 --file count.
func TestHelpListsEveryVerbTheSwitchDispatches(t *testing.T) {
	var o bytes.Buffer
	help("nova-version", &o)
	for _, verb := range []string{"snapshot", "diff", "report", "send"} {
		if !strings.Contains(o.String(), "nova-version "+verb+" ") {
			t.Fatalf("help does not list %s:\n%s", verb, o.String())
		}
	}
}

// #622 RED TEST: snapshot scopes to the adopted manifest. It takes --file <manifest>
// and reports how many of the adopted tools answer (known=16), never how many nova-*
// executables happen to sit on a bin dir or PATH. An installed column holding a recorded
// version is known without a process, so the count is the manifest's own.
func TestSnapshotReportsAdoptedManifestKnown(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "adopted.tsv")
	var b strings.Builder
	b.WriteString(Header + "\n")
	for i := 1; i <= 16; i++ {
		fmt.Fprintf(&b, "nova-tool-%02d\ttool\t0.%d.0\t-\t-\trowan\n", i, i)
	}
	if err := os.WriteFile(file, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	code := Run("nova-version", []string{"snapshot", "--file", file}, "", &o, &e, Environment{})
	if code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	need(t, o.String(), "SNAPSHOT OK checked=16 known=16 unknown=0 file="+field(file))
}

// The count is the adopted manifest's own: every recorded version is known, the same
// way report reads it, and an adopted tool whose installed argv resolves nowhere is
// unknown -- SNAPSHOT FAIL, exit 1, exactly report's verdict shape.
func TestSnapshotCountsUnknownAdoptedTool(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "adopted.tsv")
	rows := Header + "\n" +
		"nova-tool-01\ttool\t0.1.0\t-\t-\trowan\n" +
		"nova-absent\ttool\t" + noSuchTool() + "\t-\t-\trowan\n"
	if err := os.WriteFile(file, []byte(rows), 0644); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	code := Run("nova-version", []string{"snapshot", "--file", file}, "", &o, &e, Environment{})
	if code != 1 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	need(t, e.String(), "SNAPSHOT FAIL checked=2 known=1 unknown=1 file="+field(file))
}

func noSuchTool() string { return "nova-definitely-absent-xyz" }

// A snapshot without --file refuses like every other verb that names a file: the
// shape sentence is what --file is, and a missing one is never guessed.
func TestSnapshotRefusesWithoutFile(t *testing.T) {
	var o, e bytes.Buffer
	code := Run("nova-version", []string{"snapshot"}, "", &o, &e, Environment{})
	if code != 2 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	need(t, e.String(), "SNAPSHOT REFUSED: missing --file; refusing to guess (run: nova-version help)")
}
