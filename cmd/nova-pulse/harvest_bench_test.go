package main

// The flag contract of `harvest --bench` (dogfood, 2026-09-18). A bench harvest folds what
// is on the bench: there is no pulse packet to name and no relaunch to feed, so --id,
// --sources and --templates are not its to supply -- a `cut --rows` produces none of the
// three, and the verb refused to start without them. What it does want is --clone, because
// the branch is pushed from here and never from the bench.
//
// Both cases are refusals, so neither reaches the ssh seam and no test here opens a
// connection.

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestHarvestBenchWantsACloneAndNotAPulseID(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"harvest", "--bench", "hulk", "--root", "/home/gaffer/rowan-swarm-root"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (no --clone)\nstderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--clone is required with --bench") {
		t.Errorf("the refusal does not name --clone:\n%s", errb.String())
	}
	for _, unwanted := range []string{"--id is required", "--sources", "--templates"} {
		if strings.Contains(errb.String(), unwanted) {
			t.Errorf("a bench harvest asked for %s, which a cut --rows never produces:\n%s", unwanted, errb.String())
		}
	}
}

// Without --bench the pulse id is still required: the local fold reads its packet by id.
func TestHarvestWithoutBenchStillWantsThePulseID(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"harvest", "--root", "/tmp/root"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--id is required") {
		t.Errorf("the local form no longer requires --id:\n%s", errb.String())
	}
}

// --since is a duration, and a value that is not one is a refusal naming the shape rather
// than a filter nobody asked for.
func TestHarvestBenchRefusesASinceThatIsNotADuration(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"harvest", "--bench", "hulk", "--root", "/r", "--clone", "/c", "--since", "six hours"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--since wants a duration") {
		t.Errorf("the refusal does not name the shape of --since:\n%s", errb.String())
	}
}
