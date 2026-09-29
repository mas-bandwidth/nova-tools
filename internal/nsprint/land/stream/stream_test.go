package stream

import (
	"fmt"
	"testing"
)

const (
	repo = "mas-bandwidth/nova-tools"
	strm = "landing: streams + lander"
)

func score(who, head string, n int) string {
	return fmt.Sprintf("SCORE who=%s head=%s score=%d/10 gates=ci:ok,base:ok,scope:ok", who, head, n)
}

func TestSlug(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"swarm: cards": "swarm-cards", strm: "landing-streams-lander", "  Redis  ": "redis"} {
		got, err := Slug(in)
		if err != nil || got != want {
			t.Errorf("Slug(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, _ := Slug("swarm: cards", "redis"); got != "swarm-cards+redis" {
		t.Errorf("two streams: %q", got)
	}
	for _, bad := range []string{"::", "streams"} {
		if _, err := Slug(bad); err == nil {
			t.Errorf("Slug(%q) accepted", bad)
		}
	}
}

func TestReadAtCountsOnlyAtHeadAndNeverJev(t *testing.T) {
	t.Parallel()

	head := "4760b3858aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct {
		name  string
		lines []string
		who   string
		score int
		held  string
	}{
		{"at head", []string{score("rowan", head[:8], 9)}, "rowan", 9, ""},
		{"old head", []string{score("rowan", "deadbeef", 10)}, "", -1, ""},
		{"jev never counts", []string{score("jev", head, 10)}, "", -1, ""},
		{"best of two", []string{score("emma", head, 8), score("rowan", head, 10)}, "rowan", 10, ""},
		{"hold at head", []string{score("rowan", head, 10), "HOLD who=emma head=" + head[:7] + " reason=x"}, "rowan", 10, "emma"},
		{"re-read releases own hold", []string{"DISPOSITION who=rowan head=" + head + " verdict=HOLD", score("rowan", head, 9)}, "rowan", 9, ""},
		{"approve with score", []string{"DISPOSITION who=emma head=" + head + " verdict=APPROVE score=8"}, "emma", 8, ""},
	}
	for _, tc := range cases {
		r := ReadAt(tc.lines, head)
		if r.Who != tc.who || r.Score != tc.score || r.Held != tc.held {
			t.Errorf("%s: got %+v, want who=%q score=%d held=%q", tc.name, r, tc.who, tc.score, tc.held)
		}
	}
}

func TestParseCloses(t *testing.T) {
	t.Parallel()

	for body, want := range map[string]string{
		"Closes #3779\nfixes: #12 and resolved #12": "3779 12",
		"DEPENDS-ON: #5; see #6":                    "-",
		"":                                          "-",
		"closed #7.":                                "7",
	} {
		if got := ParseCloses(body); got != want {
			t.Errorf("ParseCloses(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestUnionCloses(t *testing.T) {
	t.Parallel()

	for _, c := range [][3]string{
		{"103", "7", "103 7"}, {"-", "7", "7"}, {"7", "7 8", "7 8"}, {"-", "-", "-"}, {"-", "", "-"}, {"", "", ""}, {"", "-", ""}, {"", "7", "7"},
	} {
		if got := UnionCloses(c[0], c[1]); got != c[2] {
			t.Errorf("UnionCloses(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestReceiptLineWithCost(t *testing.T) {
	t.Parallel()

	// LandPRReport without cost
	prRepNoCost := LandPRReport{
		State:    "merged",
		Head:     "1111222233334444",
		CI:       "green",
		MergeSHA: "5555666677778888",
		Record:   "created",
		Card:     "gh-client",
		CardMove: "merging->landed",
	}
	wantPRNoCost := "LAND PR repo=o/r pr=#42 state=merged head=11112222 ci=green merge=55556666 failed=- record=created card=gh-client card_move=merging->landed rest_calls=2"
	if got := prRepNoCost.ReceiptLine("o/r", 42, 2); got != wantPRNoCost {
		t.Errorf("pr receipt without cost =\n%q\nwant\n%q", got, wantPRNoCost)
	}

	// LandPRReport with cost
	prRepCost := prRepNoCost
	prRepCost.CostKnown = true
	prRepCost.CostTotal = 240
	prRepCost.CostSpin = 30
	wantPRCost := wantPRNoCost + " total_s=240 spin_s=30"
	if got := prRepCost.ReceiptLine("o/r", 42, 2); got != wantPRCost {
		t.Errorf("pr receipt with cost =\n%q\nwant\n%q", got, wantPRCost)
	}

	// Stream Report without cost
	streamRepNoCost := Report{
		Slug:        "swarm-cards",
		Branch:      "stream/swarm-cards",
		State:       "open",
		ParkedMoved: 1,
		PR:          100,
		Reused:      false,
	}
	wantStreamNoCost := "LAND STREAM repo=o/r stream=swarm-cards branch=stream/swarm-cards head=aaaaaaaa base=main@bbbbbbbb members=#1,#2 parked=- moved=1 tests=0 pr=#100 reused=false state=open"
	if got := streamRepNoCost.ReceiptLine("o/r", "main", "aaaaaaaa1111", "bbbbbbbb2222", []string{"#1", "#2"}, nil); got != wantStreamNoCost {
		t.Errorf("stream receipt without cost =\n%q\nwant\n%q", got, wantStreamNoCost)
	}

	// Stream Report with cost
	streamRepCost := streamRepNoCost
	streamRepCost.CostKnown = true
	streamRepCost.CostTotal = 500
	streamRepCost.CostSpin = 120
	wantStreamCost := wantStreamNoCost + " total_s=500 spin_s=120"
	if got := streamRepCost.ReceiptLine("o/r", "main", "aaaaaaaa1111", "bbbbbbbb2222", []string{"#1", "#2"}, nil); got != wantStreamCost {
		t.Errorf("stream receipt with cost =\n%q\nwant\n%q", got, wantStreamCost)
	}
}
