package friendread

import (
	"os"
	"strings"
	"testing"
)

// TestFriendDoneCountsKindReadEventsOnTheFixtureStream is the counter.
// The fixture is the friend-control day as cards:done entries. emma's three
// typed reads count as done=3 whether they were HOLD or APPROVE; johnny's
// score-7 APPROVE counts as done=1; stella has no read. A card ok, a prose
// mention of HOLD, another year, a redelivery, a NOTE, a missing who, a short
// head, a decide entry whose kind is the word read, a landing, and a stamp
// whose prefix is today but whose UTC date is yesterday do not add a read.
// The line names done and does not name ok, fail or calibration. The verdict
// is still on the read.
func TestFriendDoneCountsKindReadEventsOnTheFixtureStream(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/cards-done.stream")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Count(entries, "2026-09-22", []string{"emma", "johnny", "stella"})
	if err != nil {
		t.Fatal(err)
	}
	wantFriends := []Friend{
		{Who: "emma", Done: 3},
		{Who: "johnny", Done: 1},
		{Who: "stella", Done: 0},
	}
	if len(got.Friends) != len(wantFriends) {
		t.Fatalf("friends: got %d (%+v), want %d", len(got.Friends), got.Friends, len(wantFriends))
	}
	for i, want := range wantFriends {
		gotF := got.Friends[i]
		if gotF != want {
			t.Errorf("friend %d: got %+v, want %+v", i, gotF, want)
		}
		line := gotF.Line()
		if line != want.Line() {
			t.Errorf("line: got %q, want %q", line, want.Line())
		}
		for _, banned := range []string{"ok=", "fail=", "calibrat", "ok%", "DISPOSITION"} {
			if strings.Contains(line, banned) {
				t.Errorf("line %q carries %q; this fold does not score a friend or scan a comment", line, banned)
			}
		}
	}
	wantReads := []struct {
		id, who, verdict, score string
	}{
		{"1-0", "emma", "HOLD", "6"},
		{"2-0", "emma", "APPROVE", "9"},
		{"3-0", "johnny", "APPROVE", "7"},
		{"4-0", "emma", "APPROVE", "10/10"},
	}
	if len(got.Reads) != len(wantReads) {
		t.Fatalf("reads: got %d (%+v), want %d", len(got.Reads), got.Reads, len(wantReads))
	}
	for i, want := range wantReads {
		r := got.Reads[i]
		if r.ID != want.id || r.Who != want.who || r.Verdict != want.verdict || r.Score != want.score {
			t.Errorf("read %d: got id=%s who=%s verdict=%s score=%s, want id=%s who=%s verdict=%s score=%s",
				i, r.ID, r.Who, r.Verdict, r.Score, want.id, want.who, want.verdict, want.score)
		}
		if r.At.UTC().Format("2006-01-02") != "2026-09-22" {
			t.Errorf("read %d at %s is not on the counted UTC day", i, r.At)
		}
	}
}

func TestADispositionCommentIsNotAStreamEntry(t *testing.T) {
	t.Parallel()

	_, err := Parse("DISPOSITION who=emma head=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa verdict=APPROVE score=9\n")
	if err == nil {
		t.Fatal("a bare DISPOSITION comment was parsed as a stream entry")
	}
	_, err = Parse("1-0 DISPOSITION who=emma head=aaaaaaaa verdict=APPROVE\n")
	if err == nil {
		t.Fatal("a DISPOSITION token beside a stream id was accepted as a field")
	}
}

func TestCountRefusesADayThatIsNotADate(t *testing.T) {
	t.Parallel()

	_, err := Count(nil, "today", nil)
	if err == nil {
		t.Fatal("a day named today was accepted; the fold wants YYYY-MM-DD and does not read the clock")
	}
}
