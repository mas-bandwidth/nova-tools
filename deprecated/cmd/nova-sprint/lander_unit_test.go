package main

import (
	"strconv"
	"strings"
	"testing"
)

// TestWhyReadsUnitRecordsOnly (nova-tools#3611): `why <repo>#<n>` answers
// from the unit record s:<S>:prunit names. A retired s:<S>:pr:<repo>:<n>
// beside it (another head, another word) changes nothing, and a PR known only
// by a retired record is "no unit record", exit 1.
func TestWhyReadsUnitRecordsOnly(t *testing.T) {
	t.Parallel()

	mr := control35(t)
	const deadHead = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	mr.HSet("s:"+c35Sprint+":pr:nova-tools:3200", "head", deadHead, "mergeable", "CONFLICTING", "state", "landed")
	mr.HSet("s:"+c35Sprint+":pr:nova-tools:3201", "head", deadHead, "mergeable", "MERGEABLE", "state", "reading")

	out := why(t, mr)
	mustLine(t, out, "unit "+c35Unit+" nova-tools#3200")
	mustLine(t, out, "ci OK@4139b79f")
	mustLine(t, out, "reads 1/1 (stella 9 @4139b79f)")
	if strings.Contains(out, "deadbeef") || strings.Contains(out, "CONFLICTING") {
		t.Fatalf("why printed the retired record:\n%s", out)
	}

	code, stdout, stderr := runSprint("why", "nova-tools#3201", "--redis", mr.Addr(), "--sprint", c35Sprint, "--now", strconv.FormatInt(c35Now, 10))
	want := "why nova-tools#3201: no unit record: MISSING s:" + c35Sprint + ":prunit:nova-tools:3201"
	if code != 1 || !strings.Contains(stdout, want) {
		t.Fatalf("exit %d stdout %q stderr %q, want 1 and %q", code, stdout, stderr, want)
	}
}
