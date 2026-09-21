package merge

import (
	"strings"
	"testing"
)

func TestParseReviewers(t *testing.T) {
	tsv := `who	logins	may-hold
rowan	rowan-claude,claude	yes
stella	stella-astra,astra	yes
johnny	johnny-grok	yes
bot	ci-bot	no
`
	rs, err := ParseReviewers(strings.NewReader(tsv))
	if err != nil {
		t.Fatalf("ParseReviewers failed: %v", err)
	}

	if !rs.IsScanned("claude") {
		t.Errorf("expected claude to be scanned")
	}
	if !rs.IsScanned("rowan-claude") {
		t.Errorf("expected rowan-claude to be scanned")
	}
	if !rs.IsScanned("ci-bot") {
		t.Errorf("expected ci-bot to be scanned")
	}
	if rs.IsScanned("stranger") {
		t.Errorf("stranger should not be scanned")
	}

	if !rs.MayHold("rowan") {
		t.Errorf("expected rowan to have may-hold")
	}
	if !rs.MayHold("stella") {
		t.Errorf("expected stella to have may-hold")
	}
	if rs.MayHold("bot") {
		t.Errorf("expected bot to NOT have may-hold")
	}
	if rs.MayHold("unknown") {
		t.Errorf("unknown should not have may-hold")
	}

	if !rs.LoginMayHold("claude") {
		t.Errorf("expected login claude to have may-hold")
	}
	if rs.LoginMayHold("ci-bot") {
		t.Errorf("expected ci-bot to NOT have may-hold")
	}

	// ResolveWho
	// Typed name maps to login
	if who, ok := rs.ResolveWho("claude", "rowan"); !ok || who != "rowan" {
		t.Errorf("expected rowan/true, got %s/%v", who, ok)
	}
	// Typed name does not map to login -> unknown / false
	if who, ok := rs.ResolveWho("claude", "stella"); ok || who != "unknown" {
		t.Errorf("expected unknown/false for stella on claude, got %s/%v", who, ok)
	}
	// Untyped (empty typedWho) -> unknown / false
	if who, ok := rs.ResolveWho("claude", ""); ok || who != "unknown" {
		t.Errorf("expected unknown/false for untyped, got %s/%v", who, ok)
	}
}
