package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// H7: through the command: init names the coordinator, where shows it,
// release is refused for another actor and without a reason, and card of a
// sentinel lists what it needs and who needs it.
func TestReleaseIsTheCoordinators(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	if out := ta.ok("where"); !strings.Contains(out, "coordinator: lead\n") {
		t.Fatalf("where: %s", out)
	}
	ta.ok("add --stream s1 --count 1")
	out := ta.ok("add --stream s1 --sentinel stop")
	if !strings.Contains(out, "MOVED sentinel stop -> waiting stream=s1") {
		t.Fatalf("add --sentinel: %s", out)
	}
	ta.ok("add --stream s2 b --needs stop")
	if out := ta.ok("card stop"); !strings.Contains(out, "NEEDS s1-1 ready\n") || !strings.Contains(out, "NEEDED-BY b\n") {
		t.Fatalf("card stop: %s", out)
	}
	ta.ok("start --limit 1")
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.ok("ask")
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a")
	ta.ok("read --as reader-b --ok s1-1.r1.reader-b")
	ta.ok("accept s1-1 --actor lead")
	ta.ok("merge --stream s1")
	g := ta.group(sprint.NSentinelReached, "s1")
	if code, _, errs := ta.do("release stop --reason 'looked' --actor someone"); code != 1 || !strings.Contains(errs, "release is the coordinator's alone: lead, not someone") {
		t.Fatalf("another actor: %d %s", code, errs)
	}
	if code, _, errs := ta.do("release stop --actor lead"); code != 1 || !strings.Contains(errs, "release wants --reason") {
		t.Fatalf("no reason: %d %s", code, errs)
	}
	out = ta.ok("release stop --reason 'the layer is read and green' --actor lead --answers " + g.ID)
	if !strings.Contains(out, "sentinel stop waiting -> landed (released by lead); 1 cards are now ready") || !strings.Contains(out, "b waiting -> ready") {
		t.Fatalf("release: %s", out)
	}
	ta.clean()
}
