package pulse

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This is #1432, measured on the M2 Air: Spotlight indexed the clone, the build outputs and
// above all $TMPDIR, where every t.TempDir() git repository lands, so with no test running
// `ps` showed mds_stores at 105.6% CPU, mds at 34.5% and three mdworker_shared. The same
// package on the same commit on the same machine measured 68.1, 72.7, 73.8, 74.2 and 81.2
// seconds in one afternoon, a 19% spread wider than most changes anyone would want to
// detect. A darwin bench is not provisioned until indexing is off for the directories it
// works in. The probe is DATA a bench runs over ssh; this test holds the table, not mdutil.
func TestIssue1432TheDarwinStandardChecksSpotlightIsOff(t *testing.T) {
	find := func(list []StandardCheck, name string) (StandardCheck, bool) {
		for _, c := range list {
			if c.Name == name {
				return c, true
			}
		}
		return StandardCheck{}, false
	}

	// 1. The darwin standard carries the spotlight-off check.
	c, ok := find(FleetStandardChecks("darwin", "go1.26.5", "abc123", 25), "spotlight-off")
	if !ok {
		t.Fatalf("the darwin standard has no check named spotlight-off; the list is %v",
			FleetStandardChecks("darwin", "go1.26.5", "abc123", 25))
	}
	// 2. It demands exactly "off" and carries no toolchain root. A non-empty Root would put
	// this row into internal/ci's toolchain-root list, where it does not belong.
	if c.OS != "darwin" || c.Match != MatchEquals || c.Want != "off" {
		t.Errorf("spotlight-off = OS:%q Match:%q Want:%q, want darwin/equals/off", c.OS, c.Match, c.Want)
	}
	if c.Root != "" {
		t.Errorf("spotlight-off Root = %q, want empty; a non-empty Root would put this row "+
			"into internal/ci's toolchain-root list, where it does not belong", c.Root)
	}
	// 3. The probe asks mdutil about both directories the issue names.
	if strings.TrimSpace(c.Probe) == "" {
		t.Fatalf("spotlight-off has no probe")
	}
	for _, want := range []string{"mdutil", "HOME", "TMPDIR"} {
		if !strings.Contains(c.Probe, want) {
			t.Errorf("spotlight-off probe does not name %s: %s", want, c.Probe)
		}
	}
	// 4. A linux or windows bench must never DRIFT on Spotlight: that is a false red on
	// every bench in the fleet.
	for _, goos := range []string{"linux", "windows"} {
		if _, ok := find(FleetStandardChecks(goos, "go1.26.5", "abc123", 25), "spotlight-off"); ok {
			t.Errorf("%s standard carries spotlight-off; a %s bench DRIFTing on Spotlight is a false red", goos, goos)
		}
	}

	// 5. End to end through FleetStandard with a fake ssh: a bench whose mdutil says
	// indexing is enabled DRIFTs, one that says disabled is OK.
	run := func(t *testing.T, answer string) string {
		t.Helper()
		fake := newFleetVerbsFake(t)
		home := fleetStandardHome(t, "abc123")
		writeFleetVerbsExe(t, filepath.Join(fake.Bin, "mdutil"),
			"#!/bin/sh\necho '"+answer+"'\n")
		var out, errb bytes.Buffer
		FleetStandard(FleetStandardInput{
			Benches: fleetVerbsBenches(t, home), Name: "worker-1", SSH: fake.SSH, OS: "darwin",
			Go: "go1.26.5", Want: "abc123", MinFreeGB: 0,
			Timeout: 30 * time.Second, Stdout: &out, Stderr: &errb,
		})
		return out.String()
	}
	on := run(t, "Indexing enabled.")
	// The DRIFT line is the table's own format: wants() renders a MatchEquals demand as
	// equals:<want> (fleetstandard.go:230), which the existing fleet suite already pins
	// (fleetverbs_test.go:647). The card's shorthand "want=off" is that line's tail.
	if !strings.Contains(on, "STANDARD worker-1 spotlight-off DRIFT want=equals:off got=on") {
		t.Errorf("an indexer-on bench did not DRIFT on spotlight-off:\n%s", on)
	}
	off := run(t, "Indexing and searching disabled.")
	if !strings.Contains(off, "STANDARD worker-1 spotlight-off OK got=off") {
		t.Errorf("an indexer-off bench was not OK on spotlight-off:\n%s", off)
	}
}
