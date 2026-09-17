package update

import (
	"strings"
	"testing"
)

// A dev build stamped as a Go pseudo-version vX.Y.Z-0.<stamp>-<sha> whose commit is on
// main after the release tag (vX.Y.(Z-1)) is AHEAD of that release, never DIFFERENT.
func TestDevBuildAheadOfReleaseReportsAhead(t *testing.T) {
	installed := printer(t, "v0.15.3-0.20260912135226-f7cdb9c")
	latest := printer(t, "v0.15.2")
	p := manifest(t, row("x", "tool", installed, "local:"+latest, "none"))
	c, out, errs := run(t, Environment{}, "check", "--file", p)
	if c != 1 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	need(t, out, "UPDATE AHEAD name=x kind=tool installed=0.15.3-0.20260912135226-f7cdb9c latest=0.15.2 ahead=f7cdb9c")
	if strings.Contains(out, "UPDATE DIFFERENT") {
		t.Fatalf("%q appeared in:\n%s", "UPDATE DIFFERENT", out)
	}
}
