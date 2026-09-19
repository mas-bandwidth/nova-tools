package merge

import "testing"

// #1572. The shapes in this table are REAL COMMENT FIRST LINES off this repository's own
// pull requests, copied as data. The parser is judged against what readers actually
// write, not against what would be convenient to parse.
func TestParseVerdictLineReadsTheShapesThisRepositoryCarries(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		body       string
		word, head string
		ok         bool
	}{
		{"Stella HOLD at exact `7333349f2117a4ae886f4fff93470878a8d5c7f3`, beyond the inherited hold", "hold", "7333349f2117a4ae886f4fff93470878a8d5c7f3", true},
		{"Stella: **HOLD** on exact `1a11652dbbe3494e9472c54f00065e2865d7be70`, with two findings credited", "hold", "1a11652dbbe3494e9472c54f00065e2865d7be70", true},
		{"I APPROVE PR #1587 at exact head `009816689e3e90be33aded28d581aee2342b6466`.", "approve", "009816689e3e90be33aded28d581aee2342b6466", true},
		{"Stella: **scoped APPROVE** at `881658ee8267d315025de9db9a7a07d7862b2a8d`.", "approve", "881658ee8267d315025de9db9a7a07d7862b2a8d", true},
		{"# PR1430 coordinator cold read — HOLD", "hold", "", true},
		{"HOLD. #1546 / SPEC-SWARM: a launch without a lease is refused by the launcher.", "hold", "", true},
		{"Stand-in re-read for Stella at 0098166: APPROVE", "approve", "0098166", true},
		// A line carrying both folds HOLD, as rule 18 folds red last.
		{"APPROVE on the docs half, HOLD on the retry bound at `6adbbd1d89869455e920a43ccf7378daf4375add`", "hold", "6adbbd1d89869455e920a43ccf7378daf4375add", true},
		// PROSE IS NOT A VERDICT. Upper case is the whole signal: a sentence about a hold
		// spells it in lower case, and the first line is all that is read.
		{"the retry deadline hold is answered; new head pushed", "", "", false},
		{"Repaired on the same branch at `818fa98a956eed51cb64c2fca14dba686c8dfe53`.", "", "", false},
		{"Thanks — reading now.\n\nHOLD at `818fa98a956eed51cb64c2fca14dba686c8dfe53`", "", "", false},
		{"", "", "", false},
	} {
		word, head, ok := ParseVerdictLine(c.body)
		if ok != c.ok || word != c.word || head != c.head {
			t.Errorf("ParseVerdictLine(%q) = (%q, %q, %t), want (%q, %q, %t)", c.body, word, head, ok, c.word, c.head, c.ok)
		}
	}
}

// THE FOLD IS THE WHOLE DECISION, and each row here is one of the three words the issue
// argued over: whose hold counts, what lifts one, and where the clock starts.
func TestUnliftedHoldsIsLiftedOnlyByThatReadersApproveAtThisHead(t *testing.T) {
	t.Parallel()
	const head = "6adbbd1d89869455e920a43ccf7378daf4375add"
	const older = "479d05120b0bb6f6e1a6c0b0b1a5e8f2b3c4d5e6"
	hold := func(who, at, h string) Verdict {
		return Verdict{Who: who, Word: "hold", Head: h, At: at, Source: "comment"}
	}
	approve := func(who, at, h string) Verdict {
		return Verdict{Who: who, Word: "approve", Head: h, At: at, Source: "comment"}
	}
	readers := []string{"gafferongames", "johnny"}
	for _, c := range []struct {
		name string
		vs   []Verdict
		want int
	}{
		{"the morning of 2026-09-19: one hold, nothing after it", []Verdict{hold("gafferongames", "02:34:25Z", head)}, 1},
		{"an approve at this head, after it, from the same reader", []Verdict{hold("gafferongames", "02:34:25Z", head), approve("gafferongames", "03:11:13Z", head)}, 0},
		{"an approve at an OLDER head lifts nothing", []Verdict{hold("gafferongames", "02:34:25Z", head), approve("gafferongames", "03:11:13Z", older)}, 1},
		{"an approve naming NO head lifts nothing", []Verdict{hold("gafferongames", "02:34:25Z", head), approve("gafferongames", "03:11:13Z", "")}, 1},
		{"an approve BEFORE the hold lifts nothing", []Verdict{approve("gafferongames", "01:00:00Z", head), hold("gafferongames", "02:34:25Z", head)}, 1},
		{"NOBODY LIFTS ANOTHER READER'S HOLD", []Verdict{hold("gafferongames", "02:34:25Z", head), approve("johnny", "03:11:13Z", head)}, 1},
		{"a hold at a STALE head still holds: a hold never expires", []Verdict{hold("gafferongames", "02:34:25Z", older)}, 1},
		{"one unnamed login's hold counts for nothing", []Verdict{hold("some-lane-bot", "02:34:25Z", head)}, 0},
		{"two readers, two holds, two lines", []Verdict{hold("gafferongames", "02:34:25Z", head), hold("johnny", "02:40:00Z", head)}, 2},
		// One stamp to the second: the hold folds last, so it is the one that decides.
		{"a tie between a hold and an approve does not merge", []Verdict{approve("gafferongames", "02:34:25Z", head), hold("gafferongames", "02:34:25Z", head)}, 1},
		{"a case-folded login is the same reader", []Verdict{hold("GafferOnGames", "02:34:25Z", head)}, 1},
	} {
		if got := UnliftedHolds(c.vs, head, readers); len(got) != c.want {
			t.Errorf("%s: %d unlifted holds, want %d (%v)", c.name, len(got), c.want, got)
		}
	}
	// AN EMPTY READER SET COUNTS NOBODY, which is why the verbs refuse to run without one.
	if got := UnliftedHolds([]Verdict{hold("gafferongames", "02:34:25Z", head)}, head, nil); len(got) != 0 {
		t.Errorf("an empty reader set counted %d holds; the refusal to run without one is the verb's", len(got))
	}
}

// A REVIEW'S STATE IS ITS VERDICT, and it carries the sha the forge recorded it against
// rather than one typed into a sentence.
func TestDecodeVerdictsReadsCommentsAndReviews(t *testing.T) {
	t.Parallel()
	comments := `[{"user":{"login":"gafferongames"},"body":"HOLD at ` + "`6adbbd1d89869455e920a43ccf7378daf4375add`" + ` for the retry bound","created_at":"2026-09-19T02:34:25Z"},
	{"user":{"login":"rowan-claude"},"body":"**HOLD answered. New exact head ...**","created_at":"2026-09-19T02:45:16Z"}]`
	reviews := `[{"user":{"login":"johnny"},"body":"","state":"CHANGES_REQUESTED","submitted_at":"2026-09-19T02:50:00Z","commit_id":"6ADBBD1D89869455E920A43CCF7378DAF4375ADD"},
	{"user":{"login":"stella"},"body":"looks fine","state":"COMMENTED","submitted_at":"2026-09-19T02:55:00Z","commit_id":"6adbbd1d89869455e920a43ccf7378daf4375add"}]`
	vs, err := decodeVerdicts(comments, reviews, 1551)
	if err != nil {
		t.Fatal(err)
	}
	// Three: the hold comment, the lane's own report of a hold (which the NAMED READER
	// SET is what keeps out, not the parser), and the CHANGES_REQUESTED review. The
	// COMMENTED review's body carries no verdict word and is not one.
	if len(vs) != 3 {
		t.Fatalf("decoded %d verdicts, want 3: %v", len(vs), vs)
	}
	if vs[2].Word != "hold" || vs[2].Who != "johnny" || vs[2].Head != "6adbbd1d89869455e920a43ccf7378daf4375add" {
		t.Errorf("a CHANGES_REQUESTED review is a hold at its own commit_id, got %+v", vs[2])
	}
	// Several JSON arrays one after another is what `gh api --paginate` returns.
	paged, err := decodeVerdicts(comments+comments, `[]`, 1551)
	if err != nil {
		t.Fatal(err)
	}
	if len(paged) != 4 {
		t.Fatalf("a paginated stream decoded %d verdicts, want 4", len(paged))
	}
}
