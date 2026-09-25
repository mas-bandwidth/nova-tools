package merge

import (
	"strings"
	"testing"
	"time"
)

// nova-tools #3032: a carried hold by an absent holder must not block a pull
// request another may-hold friend approved at head. Emma ran out of credits
// overnight and Johnny was down; eleven PRs Stella had approved at head stayed
// in the gate because a typed HOLD by Emma or Johnny at an OLDER head is
// carried to the current head, and only the holder's own typed line at that
// head releases it. When the holder has been absent longer than absent_after
// (default 60 min), a carried hold is released by any other non-author
// may-hold reader's typed APPROVE at the current head; a hold AT the current
// head is never released this way (the holder judged this exact head).

const (
	headA3032 = "67712d4eb59a" + "0f1e2d3c4b5a69788796a5b4c3d2" // the superseded head
	headB3032 = "5adf9cb212a8" + "1b3c5d7e9f02468ace13579bdf02" // the current head
)

const reviewers3032 = "who\tlogins\tmay-hold\n" +
	"emma\temma-claude\tyes\n" +
	"stella\tstella-astra\tyes\n" +
	"johnny\tjohnny-grok\tyes\n" +
	"rowan\trowan-claude\tyes\n" +
	"bot\tci-bot\tno\n"

func mustReviewers3032(t *testing.T) *ReviewerSet {
	t.Helper()
	rs, err := ParseReviewers(strings.NewReader(reviewers3032))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	return rs
}

func TestAnAbsentHoldersCarriedHoldIsReleasedByAnotherMayHoldReadAtHead(t *testing.T) {
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	rs := mustReviewers3032(t)

	emmaHold := func(head string) Verdict {
		return Verdict{
			ID: "comment:201", Who: "emma", Word: "hold", Head: head,
			At:     now.Add(-3 * time.Hour).Format(time.RFC3339),
			Source: "comment-rule", Kind: "line", Conf: "-",
		}
	}

	readApprove := func(who string) Verdict {
		return Verdict{
			ID: "record:" + who, Who: who, Word: "approve", Head: headB3032,
			At:     now.Add(-1 * time.Hour).Format(time.RFC3339),
			Source: "record", Kind: "line", Conf: "-",
		}
	}

	absent := func(since time.Duration) map[string]FriendState {
		return map[string]FriendState{"emma": {State: "down", Since: now.Add(since), Known: true}}
	}

	cases := []struct {
		name       string
		vs         []Verdict
		presence   map[string]FriendState
		wantHeld   bool
		wantReader string
	}{
		{
			name:       "carried hold by a holder absent 90m is released by stella's approve at head",
			vs:         []Verdict{emmaHold(headA3032), readApprove("stella")},
			presence:   absent(-90 * time.Minute),
			wantHeld:   false,
			wantReader: "stella",
		},
		{
			name:       "out-of-credits holder absent 90m is released",
			vs:         []Verdict{emmaHold(headA3032), readApprove("stella")},
			presence:   map[string]FriendState{"emma": {State: "out-of-credits", Since: now.Add(-90 * time.Minute), Known: true}},
			wantHeld:   false,
			wantReader: "stella",
		},
		{
			name:     "a hold at the current head is never released this way",
			vs:       []Verdict{emmaHold(headB3032), readApprove("stella")},
			presence: absent(-90 * time.Minute),
			wantHeld: true,
		},
		{
			name:     "a holder absent 30m has not been gone long enough",
			vs:       []Verdict{emmaHold(headA3032), readApprove("stella")},
			presence: absent(-30 * time.Minute),
			wantHeld: true,
		},
		{
			name:     "the author's own approve releases nothing",
			vs:       []Verdict{emmaHold(headA3032), readApprove("rowan")},
			presence: absent(-90 * time.Minute),
			wantHeld: true,
		},
		{
			name:     "a reader without may-hold releases nothing",
			vs:       []Verdict{emmaHold(headA3032), readApprove("bot")},
			presence: absent(-90 * time.Minute),
			wantHeld: true,
		},
		{
			name:     "no presence record releases nothing",
			vs:       []Verdict{emmaHold(headA3032), readApprove("stella")},
			presence: nil,
			wantHeld: true,
		},
		{
			name:     "the holder's own approve still releases it (existing rule, unaffected)",
			vs:       []Verdict{emmaHold(headA3032), readApprove("emma")},
			presence: nil,
			wantHeld: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			holds, released := UnliftedHoldsWithPresence(tc.vs, headB3032, "rowan", rs, tc.presence, now, DefaultAbsentAfter)
			if tc.wantHeld {
				if len(holds) == 0 {
					t.Fatalf("the gate took a held pull request: want a hold, got none (released=%+v)", released)
				}
				if len(released) != 0 {
					t.Fatalf("a held pull request reported a release: %+v", released)
				}
				return
			}
			if len(holds) != 0 {
				t.Fatalf("the gate dropped a pull request whose carried hold was released: %+v", holds)
			}
			if tc.wantReader == "" {
				if len(released) != 0 {
					t.Fatalf("want no absence-rule release, got %+v", released)
				}
				return
			}
			if len(released) == 0 {
				t.Fatalf("want a release, got none")
			}
			if released[0].Reader != tc.wantReader {
				t.Errorf("released by reader=%s, want %s (all: %+v)", released[0].Reader, tc.wantReader, released)
			}
			if released[0].Holder != "emma" {
				t.Errorf("released holder=%s, want emma", released[0].Holder)
			}
			if !released[0].Since.Equal(now.Add(-90 * time.Minute)) {
				t.Errorf("released since=%s, want %s", released[0].Since, now.Add(-90*time.Minute))
			}
		})
	}
}

func TestParsePresenceReadsDownAndOutOfCreditsMarks(t *testing.T) {
	tsv := "who\tlogins\tmay-hold\tstatus\tsince\n" +
		"emma\temma-claude\tyes\tdown\t2026-09-23T08:30:00Z\n" +
		"johnny\tjohnny-grok\tyes\tout-of-credits\t1758612600\n" +
		"stella\tstella-astra\tyes\n" +
		"rowan\trowan-claude\tyes\taway\t2026-09-23T08:30:00Z\n"

	presence, err := ParsePresence(strings.NewReader(tsv))
	if err != nil {
		t.Fatalf("ParsePresence: %v", err)
	}

	wantSince := time.Date(2026, 9, 23, 8, 30, 0, 0, time.UTC)
	if st, ok := presence["emma"]; !ok || !st.Known || st.State != "down" || !st.Since.Equal(wantSince) {
		t.Errorf("emma presence = %+v (ok=%v), want down since %s", st, ok, wantSince)
	}
	if st, ok := presence["johnny"]; !ok || !st.Known || st.State != "out-of-credits" || st.Since.IsZero() {
		t.Errorf("johnny presence = %+v (ok=%v), want out-of-credits with a since", st, ok)
	}
	if _, ok := presence["stella"]; ok {
		t.Errorf("stella has no mark and must not appear in the presence map")
	}
	if st, ok := presence["rowan"]; !ok || st.State != "away" {
		t.Errorf("rowan presence = %+v (ok=%v), want away", st, ok)
	}
}
