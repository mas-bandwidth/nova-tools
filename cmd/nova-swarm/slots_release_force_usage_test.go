package main

import (
	"strings"
	"testing"
)

// THE FLAG THE HELP DID NOT HAVE (#1902, Johnny's hold on PR #1943).
//
// `slots release` keeps a lease whose holder is still running, and `--force` is the only
// way past that. Both facts were in the code and in nobody's help: the usage banner still
// printed the old `slots release` line, so a person whose release exited 2 with `SLOTS
// KEPT` had no printed remedy and the one flag that answers it was invisible. A behaviour
// that the binary's own help does not name is one every bench re-derives by reading source.
//
// This holds the banner to the three things a reader needs: the flag, the keep, and that
// the keep is the default.
func TestUsageNamesForceAndTheKeepOnSlotsRelease(t *testing.T) {
	exit, stdout, stderr := runSwarm(t, "help")
	if exit != 0 {
		t.Fatalf("`nova-swarm help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	}
	var block []string
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "nova-swarm slots release") {
			block = append(block, line)
			continue
		}
		// The verb's own continuation lines are the indented ones that follow it.
		if len(block) > 0 {
			if strings.HasPrefix(strings.TrimRight(line, " "), "      ") && !strings.Contains(line, "nova-swarm ") {
				block = append(block, line)
				continue
			}
			break
		}
	}
	if len(block) == 0 {
		t.Fatalf("the usage banner has no `nova-swarm slots release` line at all:\n%s", stdout)
	}
	usage := strings.Join(block, "\n")
	for _, want := range []string{
		"--force",
		"KEPT",
		"live=",
		"exit 2",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("the usage banner's slots release entry does not name %q; a release that keeps a live holder and exits 2 has to say so where a person looks:\n%s", want, usage)
		}
	}
	// --force is optional and it is not the default: the line carries it in brackets, and
	// a banner that printed it as required would be teaching the oversubscribing form.
	if !strings.Contains(usage, "[--force]") {
		t.Errorf("the usage banner must show --force as the optional override, `[--force]`:\n%s", usage)
	}
}
