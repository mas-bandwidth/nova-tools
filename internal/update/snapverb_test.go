package update

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The snapshot verb reports the manifest --file names, not the executables a
// directory scan would find: a person's adopted manifest is the 16 tools they
// chose, while the directory holds 32 nova-* executables (#622).
func TestSnapshotFileReportsAdoptedManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adopted.tsv")
	var b strings.Builder
	b.WriteString(Header + "\n")
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&b, "tool-%d\ttool\t%d.%d.0\t-\t-\trowan\n", i, i, i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	if code := Run("nova-version", []string{"snapshot", "--file", path}, "", &o, &e, Environment{}); code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, o.String(), e.String())
	}
	need(t, o.String(), "SNAPSHOT OK known=16 file="+field(path))
}

// The verbs block is the one place help and the dispatcher meet; snapshot was
// dispatched by the switch but not listed beside report, so `nova-version help`
// withheld a verb its own main handled (#622).
func TestHelpListsEveryVerbTheSwitchDispatches(t *testing.T) {
	var o bytes.Buffer
	help("nova-version", &o)
	for _, verb := range []string{"snapshot", "report", "send"} {
		if !strings.Contains(o.String(), "nova-version "+verb+" ") {
			t.Fatalf("help does not list %s:\n%s", verb, o.String())
		}
	}
}

func TestSnapshotRefusesAMissingFile(t *testing.T) {
	var o, e bytes.Buffer
	if code := Run("nova-version", []string{"snapshot"}, "", &o, &e, Environment{}); code != 2 {
		t.Fatalf("exit %d, want 2; out=%q err=%q", code, o.String(), e.String())
	}
	need(t, e.String(), "missing --file")
}
