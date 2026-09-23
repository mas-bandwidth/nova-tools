package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReplayCountsAGatewayShapedBundleAsGatewayNotACardFailure is #2634 part 3.
// The gateway fixture is the sprint's gateway-death shape: inception
// mercury-2.5, no tokens, zero spend, and the provider UnknownError with no
// card verdict. The card fixture published ABSTAIN. A no-token inception row
// with no provider error is not that shape. The report counts bundles.
// It does not print a rate.
func TestReplayCountsAGatewayShapedBundleAsGatewayNotACardFailure(t *testing.T) {
	root := filepath.Join("testdata", "replay2634")
	raw, err := os.ReadFile(filepath.Join(root, "gateway", "harness-output.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"name": "UnknownError"`,
		"Unexpected server error. Check server logs for details.",
		`"ref": "err_95331ad0"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("gateway fixture is not the issue's gateway-death shape, missing %q", want)
		}
	}

	rep, err := ReplayBundles(root)
	if err != nil {
		t.Fatal(err)
	}
	text := rep.Text()
	if strings.Contains(text, "%") {
		t.Fatalf("report invented a percentage:\n%s", text)
	}
	if rep.Gateway != 1 || rep.Card != 1 || rep.Other != 1 || len(rep.Rows) != 3 {
		t.Fatalf("report counted gateway=%d card=%d other=%d bundles=%d\n%s",
			rep.Gateway, rep.Card, rep.Other, len(rep.Rows), text)
	}
	got := map[string]ReplayClass{}
	for _, row := range rep.Rows {
		got[row.Path] = row.Class
	}
	if got["gateway"] != ReplayGateway {
		t.Fatalf("gateway-shaped bundle class %q\n%s", got["gateway"], text)
	}
	if got["card"] == ReplayGateway || got["card"] != ReplayCard {
		t.Fatalf("card failure class %q, want card\n%s", got["card"], text)
	}
	if got["other"] == ReplayGateway {
		t.Fatalf("a no-token attempt with no provider error counted as gateway\n%s", text)
	}
	if !strings.Contains(text, "REPLAY bundles=3 gateway=1 card=1 other=1\n") {
		t.Fatalf("report text:\n%s", text)
	}
}
