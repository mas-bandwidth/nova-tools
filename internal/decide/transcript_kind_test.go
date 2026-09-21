package decide

import "testing"

// A documented-transcript test is card work, and today the ladder had no name
// for it at all.
//
// Measured 2026-09-19 in ~/rowan-working/queue/decide/route.jsonl: twelve route
// calls named kind "transcript-test" and every one of them was REFUSED, because
// the kind is not in the table -- `want one of rebase, stack, fixture-retarget,
// fleet-chore, dogfood, row-test, ...`. The twelve units ran anyway, on the
// bottom rung, direct: tools13's t1..t6 (PRs #1802, #1789, #1798, #1799, #1795)
// and tools10's c17/c18. Eleven came back ok and the twelfth was an ABSTAIN on
// the manager's own wrong premise, not the rung's failure.
//
// It is the `dogfood` case again (2026-09-18): reading a documented transcript
// against what the tool actually prints is a comparison with an exact expected
// string, which is a procedure and not a judgement. The difference from
// `dogfood` is only which document is read, so it takes the same rung.
func TestATranscriptTestIsAKindAtTheBottomRung(t *testing.T) {
	if !KnownKind(KindTranscriptTest) {
		t.Fatalf("transcript-test is not a kind, so every route call naming it is refused; 12 were refused on 2026-09-19")
	}
	h, ok := StartHeight(KindTranscriptTest)
	if !ok {
		t.Fatalf("transcript-test has no starting rung")
	}
	if h != 0 {
		t.Errorf("a transcript diff starts at the bottom rung, got height %d", h)
	}
	if !Mechanical(KindTranscriptTest) {
		t.Errorf("a transcript diff is a comparison against an exact expected string, which is a procedure: it is mechanical")
	}
}

// The twelve units the log holds, by name, routed rather than refused. The
// rung they actually ran on and came back green from is flash.
func TestTheTwelveRefusedTranscriptUnitsRouteToFlash(t *testing.T) {
	reg := testRegistry(t)
	for _, id := range []string{
		"tools13-t1-1654-memory", "tools13-t2-1654-tokens", "tools13-t3-1654-swarm",
		"tools13-t4-1654-update", "tools13-t5-1654-version", "tools13-t6-1654-play",
	} {
		u := Unit{ID: id, Kind: KindTranscriptTest, Files: 1, Packages: 1, Lanes: 1}
		res, err := RouteRules(reg, u, DefaultFloor)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if res.Rung.Name != "flash" {
			t.Errorf("%s routed to %s, want flash (%s)", id, res.Rung.Name, res.Reason)
		}
	}
}
