package update

import (
	"bytes"
	"strings"
	"testing"
)

// The verbs block is the one place help and the dispatcher meet; every verb the
// switch dispatches is listed beside the others, including snapshot's new
// --bin/--out shape and diff.
func TestHelpListsEveryVerbTheSwitchDispatches(t *testing.T) {
	var o bytes.Buffer
	help("nova-version", &o)
	for _, verb := range []string{"snapshot", "diff", "report", "send"} {
		if !strings.Contains(o.String(), "nova-version "+verb+" ") {
			t.Fatalf("help does not list %s:\n%s", verb, o.String())
		}
	}
}
