package main

// The whole stream landing as one verb (nova-tools#3598): `nova-sprint land`
// builds the stream branch, opens one stream PR, requests our own CI, waits
// for the ci word a bench writes, and merges on green. Throwaway redis-server
// (the ci request is a library call), a local bare remote, a fake forge whose
// merge really merges the stream branch into the bare dev, and a bench turn
// (`ci run`) in place of the wait's tick. No host is touched.

import (
	"strings"
	"testing"
)

func TestLandRunRefusesUsage(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"land", "--repo", lsRepo},
		{"land", "--stream", "s", "--repo", "nova-tools"},
		{"land", "--repo", lsRepo, "--stream", "s", "extra"},
		{"land", "--repo", lsRepo, "--stream", "s", "--tick", "0s", "--redis", "127.0.0.1:1"},
	} {
		if code, _, errOut := runSprint(args...); code != 2 || strings.Count(errOut, "\n") != 1 {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}
