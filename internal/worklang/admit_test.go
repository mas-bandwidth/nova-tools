package worklang

// The seam, read against the REAL set of 2026-09-18 rather than a fixture
// invented to suit it. What a unit says it consumes is what admission charges
// it, and the paths it says it writes are the paths that serialize it.

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/jobs"
)

// A5, A6: a unit's form becomes an admission request, and a lane enters the
// vector as a NAMED dimension. Two lanes are two dimensions of capacity 1; one
// opaque "lane" count could not tell docs from merge.
func TestUnitRequestCarriesTheLaneByName(t *testing.T) {
	ws := parseFixture(t, "pitstop-2026-09-18-units.lisp")
	u, ok := ws.Unit("certify:verb")
	if !ok {
		t.Fatal("certify:verb was not read")
	}
	req := u.Request()
	if req.ID != "certify:verb" {
		t.Errorf("request id = %q", req.ID)
	}
	if n := req.Vector[jobs.Lane("pulse")]; n != 1 {
		t.Errorf("lane:pulse = %d, want 1: a lane is a resource of capacity 1", n)
	}
	if _, wrong := req.Vector["lane"]; wrong {
		t.Error("the vector carries an unnamed `lane` dimension; two lanes would contend as one")
	}
	if len(req.Writes) != 5 {
		t.Errorf("writes = %d, want the 5 the unit names", len(req.Writes))
	}
}

// A7 on the real file's own paths: certify:verb and certify:launchd both write
// fleet/launchd/com.rowan.fleet-certify.plist. They are serialized by that file
// whatever their lanes say -- the check here puts them in DIFFERENT lanes, so
// nothing but the writes intersection can be holding the second one.
func TestRealUnitsSerializeOnAWriteTheyShare(t *testing.T) {
	ws := parseFixture(t, "pitstop-2026-09-18-units.lisp")
	first, ok := ws.Unit("certify:verb")
	if !ok {
		t.Fatal("certify:verb was not read")
	}
	second, ok := ws.Unit("certify:launchd")
	if !ok {
		t.Fatal("certify:launchd was not read")
	}
	a := jobs.New(nil)
	defer a.Close()

	one := first.Request()
	two := second.Request()
	// Put them in two lanes, so the lane rule cannot be what refuses the second.
	one.Vector = jobs.Vector{jobs.Lane("pulse"): 1}
	two.Vector = jobs.Vector{jobs.Lane("docs"): 1}

	verdicts := a.Admit([]jobs.Request{one, two})
	if !verdicts[0].Go {
		t.Fatalf("certify:verb was held: %v", verdicts[0].Refusal)
	}
	if verdicts[1].Go {
		t.Fatal("two units wrote one plist because their lanes differed")
	}
	if got := verdicts[1].On(); got != "writes:fleet/launchd/com.rowan.fleet-certify.plist" {
		t.Errorf("held on %q, want the shared path", got)
	}
	if got := verdicts[1].By(); got != "certify:verb" {
		t.Errorf("held by %q, want certify:verb", got)
	}
	if !strings.Contains(verdicts[1].Refusal.Error(), "even when the lanes differ") {
		t.Errorf("the refusal %q does not say why the lane did not help", verdicts[1].Refusal)
	}
}
