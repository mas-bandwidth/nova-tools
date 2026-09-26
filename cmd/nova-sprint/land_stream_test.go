package main

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

const lsRepo = "mas-bandwidth/nova-tools"

func TestLandVerbsRefuseUsage(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"land", "stream"},
		{"land", "stream", "--repo", "nova-tools", "--stream", "s", "--redis", "127.0.0.1:1"},
		{"land", "merge", "--repo", lsRepo},
		{"land", "status", "--repo", "x"},
		{"pr"},
		{"pr", "record", "--repo", lsRepo, "--n", "1", "--ci", "blue", "--redis", "127.0.0.1:1"},
		{"pr", "record", "--repo", lsRepo, "--n", "1", "--head", "xyz", "--redis", "127.0.0.1:1"},
		{"pr", "lines", "--repo", lsRepo, "--redis", "127.0.0.1:1"}, // no --n (without --add it lists, #4352 A)
	} {
		if code, _, errOut := runSprint(args...); code != 2 || strings.Count(errOut, "\n") != 1 {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
	// A new record without head/base/stream is refused by the script.
	mr := miniredis.RunT(t)
	if code, _, errOut := runSprint("pr", "record", "--redis", mr.Addr(), "--repo", lsRepo, "--n", "5", "--ci", "green"); code != 2 || !strings.Contains(errOut, "REFUSED no record pr:nova-tools:5") {
		t.Fatalf("new record without head: %d %s", code, errOut)
	}
}
