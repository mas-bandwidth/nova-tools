package update

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// The verbs block is the one place help and the dispatcher meet; every verb the
// switch dispatches is listed beside the others, including snapshot's new
// --bin/--out shape and diff.
func TestHelpListsEveryVerbTheSwitchDispatches(t *testing.T) {
	var o bytes.Buffer
	help("nova-version", &o)
	for _, verb := range []string{"snapshot", "diff", "report", "send"} {
		if !strings.Contains(o.String(), "nova-version "+verb+" ") {
			t.Fatalf("help does not list %s:\n%s", verb, o.String())
		}
	}
}

// #622: snapshot scopes its count to the ADOPTED manifest, not every nova-*
// executable in a bin directory (or on PATH). With --file <manifest> it reads
// each adopted entry the way report does and prints how many answer -- the
// adopted sixteen -- so the count is the manifest's, never a directory scan's.
func TestSnapshotFileCountsTheAdoptedManifest(t *testing.T) {
	var rows []string
	for i := 1; i <= 16; i++ {
		rows = append(rows, row(fmt.Sprintf("nova-tool-%02d", i), "tool", fmt.Sprintf("0.%d.0", i), "-", "-"))
	}
	file := manifest(t, rows...)
	var o, e bytes.Buffer
	code := Run("nova-version", []string{"snapshot", "--file", file}, "", &o, &e, Environment{})
	if code != 0 {
		t.Fatalf("snapshot --file did not count the adopted manifest: exit %d\nstdout: %s\nstderr: %s", code, o.String(), e.String())
	}
	need(t, o.String(), "SNAPSHOT OK checked=16 known=16 unknown=0 file="+field(file))
}
