package merge

import (
	"encoding/json"
	"strings"
	"testing"
)

// nova-tools #2550: a HOLD at a SUPERSEDED head was carried forever.
//
// Measured 2026-09-22 02:25Z-13:50Z on schema fixed-table-form: 57 consecutive ticks of
// `nova-merge batch` took 16 cell pull requests and the gate dropped every one of them:
//
//	BATCH DROP #1551 reason="head 5adf9cb212a8 carries an unreleased HOLD" who=emma
//	hold=comment:5767845547 source=comment-rule held_at=67712d4eb59a carried=yes
//	at=2026-09-21T21:35:02Z conf=-
//
// held_at 67712d4eb59a is a head the branch has moved past; the current head 5adf9cb212a8
// carries four typed `DISPOSITION who=Emma head=5adf9cb2... verdict=APPROVE score=10/10`
// comments from the same friend. members=none, so the integration branch was empty and
// GitHub refused the pull request; 0 of 16 landed in 11.5 h though read debt was 0.
//
// The rule this table holds (issue #2550):
//
//	A HOLD whose held_at is not the current head is RELEASED by a LATER TYPED verdict
//	from the SAME friend at the CURRENT head. APPROVE releases it; a new HOLD at the
//	current head replaces it and holds. A hold is carried only while no typed verdict
//	from its author exists at the current head.
//
// It is deliberately narrow. A hold AT the current head is untouched: a comment still
// never releases one (SPEC-DECIDE reading 3, TestACommentNeverReleasesAnything), and
// rows "hold at head released by nothing" and "abstain is not a verdict" hold that line.

// The 12-character prefixes are the real ones from gate-cells-1338.log; the tails are
// padding, because the log prints `merge.Short` and never the whole object name.
const (
	headA2550 = "67712d4eb59a" + "0f1e2d3c4b5a69788796a5b4c3d2" // the superseded head
	headB2550 = "5adf9cb212a8" + "1b3c5d7e9f02468ace13579bdf02" // the current head
)

const reviewers2550 = "who\tlogins\tmay-hold\n" +
	"emma\temma-claude\tyes\n" +
	"johnny\tjohnny-grok\tyes\n" +
	"rowan\trowan-claude\tyes\n"

type fixtureComment struct {
	ID    int64
	Login string
	Body  string
	At    string
}

// commentsJSON renders the fixture the way the forge answers it, so the table runs the
// whole pipe -- decode, ParseComment, UnliftedHolds -- and not just the fold.
func commentsJSON(t *testing.T, cs []fixtureComment) string {
	t.Helper()
	type user struct {
		Login string `json:"login"`
	}
	type wire struct {
		ID        int64  `json:"id"`
		User      user   `json:"user"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]wire, 0, len(cs))
	for _, c := range cs {
		out = append(out, wire{ID: c.ID, User: user{Login: c.Login}, Body: c.Body, CreatedAt: c.At})
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal fixture comments: %v", err)
	}
	return string(b)
}

func hold2550(head string) string {
	return "DISPOSITION who=emma head=" + head + " verdict=HOLD\nnotes.txt is untracked in the cell."
}

func approve2550(head string) string {
	return "DISPOSITION who=emma head=" + head + " verdict=APPROVE score=10/10"
}

func TestACarriedHoldIsReleasedByTheSameFriendsVerdictAtHead(t *testing.T) {
	t.Parallel()

	type want struct {
		held    bool   // the gate drops the pull request
		holdID  string // the hold the DROP line names, when held
		heldAt  string // held_at on the DROP line, when held
		carried bool   // carried= on the DROP line, when held
	}

	cases := []struct {
		name     string
		comments []fixtureComment
		want     want
	}{
		{
			// Row 1 of the issue. This is the 16 cell pull requests' shape.
			name: "hold at A, typed APPROVE at B which is current: taken",
			comments: []fixtureComment{
				{ID: 101, Login: "emma-claude", Body: hold2550(headA2550), At: "2026-09-21T21:35:02Z"},
				{ID: 102, Login: "emma-claude", Body: approve2550(headB2550), At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: false},
		},
		{
			// Row 2 of the issue: nothing has been said at the current head, so the
			// hold is still the last word its author left and it is carried.
			name: "hold at A, nothing at B: dropped, carried",
			comments: []fixtureComment{
				{ID: 201, Login: "emma-claude", Body: hold2550(headA2550), At: "2026-09-21T21:35:02Z"},
			},
			want: want{held: true, holdID: "comment:201", heldAt: headA2550, carried: true},
		},
		{
			// Row 3 of the issue: a new HOLD at the current head REPLACES the carried
			// one. The pull request is still dropped, but the DROP line must name the
			// hold at head with carried=no -- pointing a reader at a head the branch
			// moved past is what cost 11.5 h.
			name: "hold at A, hold at B: dropped at head, not carried",
			comments: []fixtureComment{
				{ID: 301, Login: "emma-claude", Body: hold2550(headA2550), At: "2026-09-21T21:35:02Z"},
				{ID: 302, Login: "emma-claude", Body: hold2550(headB2550), At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: true, holdID: "comment:302", heldAt: headB2550, carried: false},
		},
		{
			// Row 4, the negative control the fix must not trample: the APPROVE is at
			// the SUPERSEDED head too. There is no read at the current head, so the
			// hold stands and stays carried. Release keys on the head, never on
			// "somebody approved at some point".
			name: "hold at A, APPROVE at A only, head moved to B: dropped, no read at head",
			comments: []fixtureComment{
				{ID: 401, Login: "emma-claude", Body: hold2550(headA2550), At: "2026-09-21T21:35:02Z"},
				{ID: 402, Login: "emma-claude", Body: approve2550(headA2550), At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: true, holdID: "comment:401", heldAt: headA2550, carried: true},
		},
		{
			// The live #1551 shape, comment ids and stamps as the log and the issue
			// carry them: one hold at the superseded head, four typed APPROVE 10/10 at
			// the current head from the same friend.
			name: "live #1551: hold at 67712d4eb59a, four APPROVE at 5adf9cb212a8: taken",
			comments: []fixtureComment{
				{ID: 5767845547, Login: "emma-claude", Body: hold2550(headA2550), At: "2026-09-21T21:35:02Z"},
				{ID: 5768100001, Login: "emma-claude", Body: approve2550(headB2550), At: "2026-09-22T01:48:00Z"},
				{ID: 5768100002, Login: "emma-claude", Body: approve2550(headB2550), At: "2026-09-22T02:45:00Z"},
				{ID: 5768100003, Login: "emma-claude", Body: approve2550(headB2550), At: "2026-09-22T03:34:00Z"},
				{ID: 5768100004, Login: "emma-claude", Body: approve2550(headB2550), At: "2026-09-22T04:10:00Z"},
			},
			want: want{held: false},
		},
		{
			// nova-tools #2615 follow-up: the same-friend match is case-insensitive.
			// Measured 2026-09-22 20:14Z, lane land-1615: BATCH DROP #2587 held
			// who=johnny though the pull request carried a typed `who=johnny` APPROVE
			// at head, because the HOLD comment had been typed `who=Johnny`.
			name: "hold typed who=Johnny at A, APPROVE typed who=johnny at B: taken",
			comments: []fixtureComment{
				{ID: 1101, Login: "johnny-grok", Body: "DISPOSITION who=Johnny head=" + headA2550 + " verdict=HOLD score=4/10",
					At: "2026-09-21T21:35:02Z"},
				{ID: 1102, Login: "johnny-grok", Body: "DISPOSITION who=johnny head=" + headB2550 + " verdict=APPROVE score=9/10",
					At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: false},
		},
		{
			// The mirror: HOLD typed lowercase, APPROVE typed capitalised.
			name: "hold typed who=johnny at A, APPROVE typed who=Johnny at B: taken",
			comments: []fixtureComment{
				{ID: 1201, Login: "johnny-grok", Body: "DISPOSITION who=johnny head=" + headA2550 + " verdict=HOLD score=4/10",
					At: "2026-09-21T21:35:02Z"},
				{ID: 1202, Login: "johnny-grok", Body: "DISPOSITION who=Johnny head=" + headB2550 + " verdict=APPROVE score=9/10",
					At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: false},
		},
		{
			// The second ask on the issue: an APPROVE's head= is matched against the
			// whole 40-character head by the same prefix rule HOLD's head= gets, so the
			// abbreviation a friend actually types releases.
			name: "APPROVE at B typed as a 12-character prefix: taken",
			comments: []fixtureComment{
				{ID: 601, Login: "emma-claude", Body: hold2550(headA2550), At: "2026-09-21T21:35:02Z"},
				{ID: 602, Login: "emma-claude", Body: approve2550(headB2550[:12]), At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: false},
		},
		{
			// The second ask, other half: a DISPOSITION that is not the WHOLE line is
			// not a verdict. A release smuggled into a sentence inside a long body
			// releases nothing.
			name: "APPROVE smuggled mid-line in a multi-line body: dropped, carried",
			comments: []fixtureComment{
				{ID: 701, Login: "emma-claude", Body: hold2550(headA2550), At: "2026-09-21T21:35:02Z"},
				{ID: 702, Login: "emma-claude", Body: "Ran the cells again.\nI would write DISPOSITION who=emma head=" +
					headB2550 + " verdict=APPROVE if the rebase were clean.\nIt is not.", At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: true, holdID: "comment:701", heldAt: headA2550, carried: true},
		},
		{
			// Only the holder releases: a second friend's APPROVE at head is not the
			// author of the carried hold and lifts nothing.
			name: "hold at A by emma, APPROVE at B by johnny: dropped, carried",
			comments: []fixtureComment{
				{ID: 801, Login: "emma-claude", Body: hold2550(headA2550), At: "2026-09-21T21:35:02Z"},
				{ID: 802, Login: "johnny-grok", Body: "DISPOSITION who=johnny head=" + headB2550 +
					" verdict=APPROVE score=9/10", At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: true, holdID: "comment:801", heldAt: headA2550, carried: true},
		},
		{
			// Amended by the coordinator's 2026-09-22 4:55 PM decision: a hold AT the
			// current head IS released by the same friend's later typed APPROVE at that
			// same head -- SPEC-DECIDE reading 3's older "a comment releases nothing at
			// head" no longer governs a friend superseding their own word (it still
			// governs the needs_read approval gate, read.go, and a DIFFERENT friend's
			// comment; see the row below and whocase_test.go).
			name: "hold at B and APPROVE at B, both current, same friend: taken",
			comments: []fixtureComment{
				{ID: 901, Login: "emma-claude", Body: hold2550(headB2550), At: "2026-09-22T01:00:00Z"},
				{ID: 902, Login: "emma-claude", Body: approve2550(headB2550), At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: false},
		},
		{
			// The negative control on the same decision: at head, same as carried, the
			// rule is same-friend, never "somebody approved". A different friend's
			// typed APPROVE at the SAME head as the hold releases nothing.
			name: "hold at B by emma, APPROVE at B by johnny, both current: dropped",
			comments: []fixtureComment{
				{ID: 951, Login: "emma-claude", Body: hold2550(headB2550), At: "2026-09-22T01:00:00Z"},
				{ID: 952, Login: "johnny-grok", Body: "DISPOSITION who=johnny head=" + headB2550 +
					" verdict=APPROVE score=9/10", At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: true, holdID: "comment:951", heldAt: headB2550, carried: false},
		},
		{
			// An untyped hold-shaped line binds to the current head, is who=unknown,
			// and the same friend's later typed APPROVE at that head does not name it:
			// it fails closed, exactly as it did before #2550.
			name: "untyped bold HOLD at head, typed APPROVE at head: dropped",
			comments: []fixtureComment{
				{ID: 1001, Login: "emma-claude", Body: "**HOLD** the schema leg is red.", At: "2026-09-22T01:00:00Z"},
				{ID: 1002, Login: "emma-claude", Body: approve2550(headB2550), At: "2026-09-22T01:48:00Z"},
			},
			want: want{held: true, holdID: "comment:1001", heldAt: headB2550, carried: false},
		},
	}

	rs := mustReviewers2550(t)
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vs, err := ParseForgeVerdicts(commentsJSON(t, tc.comments), "[]", 1551, rs, "rowan", headB2550, false)
			if err != nil {
				t.Fatalf("the fixture did not decode: %v", err)
			}
			holds := UnliftedHolds(vs, headB2550, "rowan", rs)

			if !tc.want.held {
				if len(holds) != 0 {
					t.Fatalf("the gate dropped a pull request whose current head %s carries a released hold; this is the #2550 break, held by %+v",
						Short(headB2550), holds)
				}
				return
			}
			if len(holds) == 0 {
				t.Fatalf("the gate took a held pull request: want a hold at %s, got none", Short(tc.want.heldAt))
			}
			got := holds[0]
			if got.ID != tc.want.holdID {
				t.Errorf("the DROP line would name hold=%s, want %s (all: %+v)", got.ID, tc.want.holdID, holds)
			}
			if !headMatch(got.Head, tc.want.heldAt) {
				t.Errorf("the DROP line would say held_at=%s, want %s", Short(got.Head), Short(tc.want.heldAt))
			}
			if got.Carried != tc.want.carried {
				t.Errorf("the DROP line would say carried=%v, want %v (held_at=%s head=%s)",
					got.Carried, tc.want.carried, Short(got.Head), Short(headB2550))
			}
		})
	}
}

func mustReviewers2550(t *testing.T) *ReviewerSet {
	t.Helper()
	rs, err := ParseReviewers(strings.NewReader(reviewers2550))
	if err != nil {
		t.Fatalf("reviewer fixture: %v", err)
	}
	return rs
}
