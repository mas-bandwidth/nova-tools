package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SWARM GATE #3501: inside card sandbox ~/.local/bin is on PATH but its files cannot be inspected.
// A probe card running `nova-check links` and `nova-check version` by name (no absolute path)
// exits 0 inside `nova-swarm native` on Studio bench, while direct file reads under ~/.local/bin
// are denied.
func TestNativeProbeCardRunsNovaCheckByName(t *testing.T) {
	wallOnly(t)

	checkBin, err := exec.LookPath("nova-check")
	if err != nil {
		t.Skip("nova-check is not installed on PATH")
	}

	if err := buildShared(); err != nil {
		t.Fatalf("building shared test binaries: %v", err)
	}

	root, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "probe-check")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a harness in slotDir that executes `nova-check` by name in bash
	harnessScript := filepath.Join(slot, "test-harness.sh")
	harnessContent := `#!/bin/bash
shift # run
while [ $# -gt 0 ]; do
	case "$1" in
		--) shift; break;;
		*) shift;;
	esac
done
card="$1"
cd "$NOVA_SWARM_JOB" || exit 1

# 1. Run nova-check links --dir . by name
bash -c 'nova-check links --dir . > links.out 2>&1'
links_rc=$?

# 2. Run nova-check version by name
bash -c 'nova-check version > version.out 2>&1'
version_rc=$?

# 3. File inspection of binary under ~/.local/bin must be denied (Operation not permitted)
cat "` + checkBin + `" > cat.out 2>&1
cat_rc=$?

printf "%d %d %d\n" $links_rc $version_rc $cat_rc > probe.rc
if [ $links_rc -ne 0 ] || [ $version_rc -ne 0 ]; then
	exit 127
fi
if [ $cat_rc -eq 0 ]; then
	echo "cat succeeded but must be denied" >&2
	exit 1
fi
exit 0
`
	if err := os.WriteFile(harnessScript, []byte(harnessContent), 0o755); err != nil {
		t.Fatal(err)
	}

	cardPath := filepath.Join(root, "probe-card.md")
	cardText := `RESULT: dogfood-check-probe sha=ce8631be62c6
KIND: probe
REPO: mas-bandwidth/nova-tools
BASE: dev
base-sha: ce8631be62c652826c504f6536a018ab33f27a9d
PATHS: RESULT.md
TEST: none
DEPENDS-ON: none
WHO: any
DONE-WHEN: RESULT.md exists
`
	if err := os.WriteFile(cardPath, []byte(cardText), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run cmdNative with the real sandbox binary compiled from cmd/nova-sandbox
	args := []string{
		"native",
		"--tokens", "unmetered",
		"--slots-store", nativeStore(t),
		"--owner", "fake-1",
		"--harness", harnessScript,
		"--model", "fake/model",
		"--label", "probe-check",
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "30s",
		"--sandbox", builtSandbox,
	}

	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		// Read probe.rc if it exists
		rcData, _ := os.ReadFile(filepath.Join(jobDir, "probe.rc"))
		linksOut, _ := os.ReadFile(filepath.Join(jobDir, "links.out"))
		versionOut, _ := os.ReadFile(filepath.Join(jobDir, "version.out"))
		catOut, _ := os.ReadFile(filepath.Join(jobDir, "cat.out"))
		t.Fatalf("native run failed rc=%d: probe.rc=%s\nlinks.out:\n%s\nversion.out:\n%s\ncat.out:\n%s\nstderr:\n%s\nstdout:\n%s",
			rc, string(rcData), string(linksOut), string(versionOut), string(catOut), stderr.String(), stdout.String())
	}

	probeRcBytes, err := os.ReadFile(filepath.Join(jobDir, "probe.rc"))
	if err != nil {
		t.Fatalf("could not read probe.rc: %v", err)
	}
	fields := strings.Fields(string(probeRcBytes))
	if len(fields) != 3 {
		t.Fatalf("probe.rc malformed: %q", string(probeRcBytes))
	}
	if fields[0] != "0" {
		t.Errorf("nova-check links by name exited %s, want 0", fields[0])
	}
	if fields[1] != "0" {
		t.Errorf("nova-check version by name exited %s, want 0", fields[1])
	}
	if fields[2] == "0" {
		t.Errorf("cat %s inside sandbox exited 0, want non-zero (files cannot be inspected)", checkBin)
	}
}
