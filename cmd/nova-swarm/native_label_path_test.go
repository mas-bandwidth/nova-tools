package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ISSUE #1923. `native` checks that the SLOT is under the root and then joins the
// card's LABEL into <slot>/jobs/<label> and <slot>/tmp/<label> with nothing asked of
// it. Those two directories are MkdirAll'd, the first is leased (a publish and an
// os.Remove of `.lease` inside it), and both are handed to the wall as --write. A
// label of `../../../OUTSIDE` therefore names a directory outside the swarm root to
// make, to delete inside, and to give the card write access to.
//
// The bound this test crosses: a label is a NAME, and every path this run derives
// from it stays strictly below the slot, which stays below the root. The honest
// label in the same table is here so a fix that refuses every label fails too.
func TestNativeRefusesALabelThatWalksOutOfTheSwarmRoot(t *testing.T) {
	bin := nativeHarness(t)
	for _, label := range []string{
		"../../../OUTSIDE",
		"..",
		"a/b",
		"-rf",
	} {
		t.Run(label, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "swarm-root")
			slot := filepath.Join(root, "1")
			if err := os.MkdirAll(slot, 0o755); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(base, "OUTSIDE")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("not the card's\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: "fake/fake-model", label: label,
				card:    []byte("a card\n"),
				slotDir: slot, root: root, deadline: time.Minute, noWall: true,
			}, &errOut)

			if code != 2 {
				t.Errorf("a label that is a path exits 2, got %d:\n%s", code, errOut.String())
			}
			if !strings.Contains(errOut.String(), "NATIVE REFUSED") {
				t.Errorf("the refusal is one REFUSED line, got:\n%s", errOut.String())
			}
			if !strings.Contains(errOut.String(), "label") {
				t.Errorf("the refusal does not name the label:\n%s", errOut.String())
			}
			// Nothing was made, leased or removed outside the root.
			if _, err := os.Lstat(filepath.Join(outside, ".lease")); err == nil {
				t.Errorf("a lease was published outside the swarm root at %s", outside)
			}
			if _, err := os.Lstat(filepath.Join(outside, "keep")); err != nil {
				t.Errorf("the bytes outside the swarm root did not survive: %v", err)
			}
			entries, err := os.ReadDir(base)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Name() != "swarm-root" && e.Name() != "OUTSIDE" {
					t.Errorf("the run made %s beside the swarm root", filepath.Join(base, e.Name()))
				}
			}
		})
	}
}

// The same admission with an honest label still makes the job directory, so the
// refusal above is about the label being a path and not about labels.
func TestNativeStillAcceptsAnOrdinaryLabel(t *testing.T) {
	bin := nativeHarness(t)
	base := t.TempDir()
	root := filepath.Join(base, "swarm-root")
	slot := filepath.Join(root, "1")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "card-1.a_b",
		card:    []byte("a card\n"),
		slotDir: slot, root: root, deadline: time.Minute, noWall: true,
	}, &errOut)
	if code == 2 && strings.Contains(errOut.String(), "not a job name") {
		t.Fatalf("an ordinary label was refused as a path:\n%s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(slot, "jobs", "card-1.a_b")); err != nil {
		t.Fatalf("the honest job directory was not made: %v", err)
	}
}
