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
