package decide

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The routing defect of 2026-09-18, with the day's own units as the fixture.
//
// Twenty real units of work/pitstop-2026-09-18-units.lisp went through live
// Jev. Thirteen were decided by the provider; the other SEVEN were decided
// mechanically by the security-kind rule and every one of them came back
// rung=johnny. Johnny is reserved: he takes a read and the STOP a read can
// call, not the work, and six of those seven units are owned in the set by
// rowan-child (the seventh, wall:toolchain-roots, is held for his security READ
// -- which is exactly the distinction the rule was missing).
//
// The seven units are in testdata/pitstop-2026-09-18, copied from the evidence
// the run was driven with, and the expectation below is the whole fix: the work
// goes to the rung the evidence supports, and johnny is attached as the READER.

// securityUnit reads one of the day's units back off disk.
func securityUnit(t *testing.T, name string) Unit {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "pitstop-2026-09-18", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := ParseUnit(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return u
}

func TestTheSevenSecurityUnitsGoToTheirRungAndAreReadByJohnny(t *testing.T) {
	reg := testRegistry(t)
	for _, tc := range []struct {
		file string
		unit string
		rung string
		ask  string
		why  string
	}{
		// fleet-chore is mechanical and starts at the bottom; one package over
		// one lane raises nothing, so the work is Flash's and the touch is read.
		{"certify_fix-loop", "certify:fix-loop", "flash", AskCard, "sudo"},
		{"fleet_services", "fleet:services", "flash", AskCard, "secrets, network"},
		{"release_next", "release:next", "flash", AskCard, "deploy-keys, network"},
		{"threadripper_standard", "threadripper:standard", "flash", AskCard, "sudo"},
		// fix-with-red-test and new-verb start at the child rungs, and the lane
		// owner is nobody on this ladder, so the first mind at that height wins.
		{"decide_validate-stepup", "decide:validate-stepup", "opus", AskChild, "network"},
		{"sandbox_lock-reap", "sandbox:lock-reap", "opus", AskChild, "sandbox"},
		// kind guard starts at the friends' rung. Johnny is there and is
		// RESERVED, so the rung is the next eligible mind at that height.
		{"wall_toolchain-roots", "wall:toolchain-roots", "emma", AskBus, "kind guard, guard, sandbox"},
	} {
		u := securityUnit(t, tc.file)
		if u.ID != tc.unit {
			t.Errorf("%s: the fixture holds unit %s, want %s", tc.file, u.ID, tc.unit)
		}
		if !u.Security() {
			t.Fatalf("%s: the fixture is not security work, so it is the wrong fixture", tc.file)
		}
		res := mustRoute(t, reg, u, DefaultFloor)
		if res.Rung.Name != tc.rung {
			t.Errorf("%s: rung = %s, want %s -- the WORK goes to the rung the evidence supports (%s)", tc.unit, res.Rung.Name, tc.rung, res.Reason)
		}
		if res.Rung.Name == "johnny" {
			t.Errorf("%s: johnny is reserved for reads and the STOP, never for the work", tc.unit)
		}
		if res.Rung.Ask != tc.ask {
			t.Errorf("%s: ask = %s, want %s", tc.unit, res.Rung.Ask, tc.ask)
		}
		if res.ReadField() != "johnny" {
			t.Errorf("%s: read = %s, want johnny -- a security READ is attached to every security unit", tc.unit, res.ReadField())
		}
		if !strings.Contains(res.Reason, tc.why) {
			t.Errorf("%s: the reason must name what makes it security work (%s): %s", tc.unit, tc.why, res.Reason)
		}
		// Every touch named ONCE. "secrets, secrets, network" reads like two
		// findings where the evidence holds one.
		if strings.Contains(res.Reason, tc.why+", "+strings.Split(tc.why, ", ")[0]) {
			t.Errorf("%s: the reason repeats a touch: %s", tc.unit, res.Reason)
		}
	}
}

// The seven are decided by the MACHINERY and never by the provider: security is
// a kind, not a height, and it is answered here. A decider that is asked at all
// fails this test.
func TestTheSevenSecurityUnitsNeverReachTheProvider(t *testing.T) {
	reg := testRegistry(t)
	for _, name := range []string{
		"certify_fix-loop", "fleet_services", "release_next", "threadripper_standard",
		"decide_validate-stepup", "sandbox_lock-reap", "wall_toolchain-roots",
	} {
		u := securityUnit(t, name)
		fake := &countingDecider{}
		res, err := RouteJev(context.Background(), fake, reg, u, DefaultFloor)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fake.calls != 0 {
			t.Errorf("%s: the provider was asked %d times about security work", name, fake.calls)
		}
		if res.ReadField() != "johnny" {
			t.Errorf("%s: read = %s, want johnny", name, res.ReadField())
		}
		if res.Usage.Calls != 0 {
			t.Errorf("%s: a decision that made no call must claim none: calls=%d", name, res.Usage.Calls)
		}
	}
}

// The touch tokens are deduplicated, in the order the enumeration names them.
// --secrets beside touches: ["secrets"] is one fact said twice.
func TestSecurityReasonNamesEachTouchOnce(t *testing.T) {
	reg := testRegistry(t)
	for name, tc := range map[string]struct {
		unit Unit
		want string
	}{
		"secrets twice": {
			Unit{ID: "s1", Kind: KindFleetChore, Files: 1, Secrets: true, Touches: []string{TouchSecrets, TouchNetwork}},
			"secrets, network",
		},
		"guard three ways": {
			Unit{ID: "s2", Kind: KindGuard, Files: 1, Guard: true, Touches: []string{TouchGuard, TouchSandbox}},
			"kind guard, guard, sandbox",
		},
		"one touch": {
			Unit{ID: "s3", Kind: KindFleetChore, Files: 1, Touches: []string{TouchSudo}},
			"sudo",
		},
	} {
		if got := tc.unit.securityWhy(); got != tc.want {
			t.Errorf("%s: securityWhy = %q, want %q", name, got, tc.want)
		}
		res := mustRoute(t, reg, tc.unit, DefaultFloor)
		if !strings.Contains(res.Reason, tc.want) {
			t.Errorf("%s: the reason must carry the deduplicated touches: %s", name, res.Reason)
		}
	}
}

// A security unit with nobody to read it is a refusal, not a dispatch. That
// property is the one the old rule had and the new rule keeps: the read is not
// optional because the rung is no longer johnny's.
func TestSecurityWithNoReaderIsRefused(t *testing.T) {
	for name, data := range map[string]string{
		"nobody designated": `{"minds":[
		  {"name":"flash","lineage":"deepseek","height":0,"kinds":[],"availability":"available","ask":"card"},
		  {"name":"emma","lineage":"emma","height":3,"kinds":[],"availability":"available","ask":"bus"}
		]}`,
		"reader asleep": `{"minds":[
		  {"name":"flash","lineage":"deepseek","height":0,"kinds":[],"availability":"available","ask":"card"},
		  {"name":"guardian","lineage":"johnny","height":3,"kinds":["guard"],"availability":"asleep","ask":"bus"}
		]}`,
	} {
		reg, err := ParseRegistry([]byte(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		u := Unit{ID: "s", Kind: KindFleetChore, Files: 1, Touches: []string{TouchSecrets}}
		res, err := RouteRules(reg, u, DefaultFloor)
		if err == nil {
			t.Errorf("%s: security work with no reader must be refused, got rung %s", name, res.Rung.Name)
		}
		if res.Refusal == "" {
			t.Errorf("%s: a refused decision is still a row, and it says why", name)
		}
	}
}

// A fresh take designated to a RESERVED mind is the same shape and gets the
// same answer: the read is attached and the work goes up the ladder.
func TestAFreshTakeOnAReservedMindIsAReadToo(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "f1", Kind: KindDesign, Files: 3, Packages: 1, FreshTake: true}
	res := mustRoute(t, reg, u, DefaultFloor)
	if res.Rung.Name == "johnny" {
		t.Fatalf("a reserved mind takes no work: %s", res.Reason)
	}
	if res.ReadField() != "johnny" {
		t.Errorf("read = %s, want johnny: a designation on a reserved mind is a READ", res.ReadField())
	}
	if !strings.Contains(res.Reason, "reserved") {
		t.Errorf("the reason must say why the designation became a read: %s", res.Reason)
	}
}

// One read, once, however many designations name the same mind.
func TestOneMindNamedTwiceIsOneRead(t *testing.T) {
	reg := testRegistry(t)
	u := Unit{ID: "f2", Kind: KindGuard, Files: 3, Packages: 1, FreshTake: true, Touches: []string{TouchSecrets}}
	res := mustRoute(t, reg, u, DefaultFloor)
	if res.ReadField() != "johnny" {
		t.Errorf("read = %s, want one johnny", res.ReadField())
	}
}

// The line carries the read on every decision, as a dash where there is none:
// every field on every line, so no reader has to infer an absence.
func TestTheLineAlwaysCarriesTheRead(t *testing.T) {
	reg := testRegistry(t)
	plain := mustRoute(t, reg, Unit{ID: "p", Kind: KindRebase, Files: 2, Packages: 1}, DefaultFloor)
	if !strings.Contains(plain.Line(), " read=- ") {
		t.Errorf("a decision with no read carries the dash: %s", plain.Line())
	}
	sec := mustRoute(t, reg, securityUnit(t, "fleet_services"), DefaultFloor)
	if !strings.Contains(sec.Line(), " read=johnny ") {
		t.Errorf("a security decision carries its reader: %s", sec.Line())
	}
}

// countingDecider answers nothing and counts being asked. A security unit that
// reaches it is a security decision the provider was allowed to make.
type countingDecider struct{ calls int }

func (d *countingDecider) Decide(_ context.Context, state string, qs map[string]Question) (map[string]Answer, Usage, error) {
	d.calls++
	return map[string]Answer{RungQuestion: {Type: "choice", Choice: "rung-1", Confidence: 0.99}}, Usage{}, nil
}
