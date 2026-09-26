//go:build functional

package main

import (
	"strings"
	"testing"
)

// The wake remedy is run through the binary against a throwaway store, in
// the order the line prints it.
func TestFGBWakeRemedyDeclaresTheFriend(t *testing.T) {
	t.Parallel()
	addr := rowanStore(t)
	code, out, errOut := runSprint("friend", "wake", "ghost", "--redis", addr, "--reason", "audit")
	if code != 1 || !strings.HasPrefix(out, "WAKE QUEUED friend=ghost reason=audit undeclared=1") ||
		!strings.Contains(out, "nova-sprint capacity machine --as <actor> --redis "+addr+" <machine> <slots>") ||
		!strings.Contains(out, "nova-sprint capacity friend --as <actor> --machine <machine> --redis "+addr+" ghost <slots>") {
		t.Fatalf("undeclared wake: exit %d out %q err %q", code, out, errOut)
	}
	// The printed remedy, placeholders filled, in its order.
	for _, remedy := range [][]string{
		{"capacity", "machine", "--as", "rowan", "--redis", addr, "studio", "4"},
		{"capacity", "friend", "--as", "rowan", "--machine", "studio", "--redis", addr, "ghost", "1"},
	} {
		if code, out, errOut := runSprint(remedy...); code != 0 {
			t.Fatalf("the wake remedy nova-sprint %s refused: exit %d out %q err %q", strings.Join(remedy, " "), code, out, errOut)
		}
	}
	code, out, errOut = runSprint("friend", "wake", "ghost", "--redis", addr, "--reason", "audit")
	if code != 0 || out != "WAKE QUEUED friend=ghost reason=audit\n" || errOut != "" {
		t.Fatalf("declared wake: exit %d out %q err %q", code, out, errOut)
	}
	if code, out, _ := runSprint("friend", "wake-health", "--redis", addr); code == 2 && strings.Contains(out, "not defined") {
		t.Fatalf("the wake remedy's second verb does not parse: %q", out)
	}
}
