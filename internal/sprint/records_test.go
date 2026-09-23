package sprint

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ghWith returns a GH whose subprocess is replaced by the canned answers below, keyed by the
// first two arguments (`pr view`, `issue view`). No test in this repo names a real host, and
// no test here runs gh.
func ghWith(friends []string, answers map[string]string) *GH {
	g := NewGH("gh", "", friends, time.Second)
	g.run = func(ctx context.Context, args ...string) ([]byte, error) {
		key := strings.Join(args[:2], " ")
		for i, a := range args {
			if a == "--json" && i+1 < len(args) {
				key += " " + args[i+1]
				break
			}
		}
		body, ok := answers[key]
		if !ok {
			return nil, errNotFound(key)
		}
		return []byte(body), nil
	}
	return g
}

type errNotFound string

func (e errNotFound) Error() string { return "no canned answer for " + string(e) }

const headSHA = "af1f1dfaf1f1dfaf1f1dfaf1f1dfaf1f1dfaf1f1"

// TestAMergedPullRequestClosesTheTask: the primary record is the merge, and the evidence on
// the task is the merge commit, not a sentence.
func TestAMergedPullRequestClosesTheTask(t *testing.T) {
	g := ghWith(nil, map[string]string{
		"pr view state,mergedAt,mergeCommit": `{"state":"MERGED","mergedAt":"2026-09-22T16:20:00Z","mergeCommit":{"oid":"affffdf1d0000000000000000000000000000000"}}`,
	})
	rec, err := g.Look(context.Background(), Task{ID: "t", Kind: KindFix, Ref: "mas-bandwidth/nova-tools#2598"})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Closed || !strings.Contains(rec.Evidence, "merged affffdf1d") {
		t.Fatalf("the record is %+v; wants closed with the merge commit as evidence", rec)
	}
}

// TestAnOpenPullRequestSaysNothing: no evidence is not negative evidence, and it is certainly
// not a close.
func TestAnOpenPullRequestSaysNothing(t *testing.T) {
	g := ghWith(nil, map[string]string{
		"pr view state,mergedAt,mergeCommit": `{"state":"OPEN"}`,
	})
	rec, err := g.Look(context.Background(), Task{ID: "t", Kind: KindFix, Ref: "o/n#1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Closed {
		t.Fatalf("an open pull request closed a task")
	}
}

// TestATypedLineAtHeadClosesARead, and one at an older head does NOT: a carried line is
// evidence about a commit that is no longer there.
func TestATypedLineAtHeadClosesARead(t *testing.T) {
	at := `{"headRefOid":"` + headSHA + `","comments":[{"author":{"login":"johnny"},"body":"DISPOSITION who=johnny head=` + headSHA + ` verdict=APPROVE","createdAt":"2026-09-22T16:12:00Z"}]}`
	carried := `{"headRefOid":"` + headSHA + `","comments":[{"author":{"login":"johnny"},"body":"DISPOSITION who=johnny head=0000000000000000000000000000000000000000 verdict=APPROVE","createdAt":"2026-09-22T10:00:00Z"}]}`
	task := Task{ID: "read", Kind: KindRead, Ref: "mas-bandwidth/nova-tools#2598"}

	g := ghWith([]string{"johnny", "stella"}, map[string]string{"pr view headRefOid,comments": at})
	rec, err := g.Look(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Closed || !strings.Contains(rec.Evidence, "APPROVE by johnny") {
		t.Fatalf("the record is %+v; a typed line at head closes the read", rec)
	}

	g = ghWith([]string{"johnny"}, map[string]string{"pr view headRefOid,comments": carried})
	rec, err = g.Look(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Closed {
		t.Fatalf("a line carried from an older head closed a read")
	}
}

// TestALineByAStrangerIsNotAFriendRead: a cold read never substitutes for a friend's.
func TestALineByAStrangerIsNotAFriendRead(t *testing.T) {
	body := `{"headRefOid":"` + headSHA + `","comments":[{"author":{"login":"passer-by"},"body":"DISPOSITION who=passer-by head=` + headSHA + ` verdict=APPROVE","createdAt":"2026-09-22T16:12:00Z"}]}`
	g := ghWith([]string{"johnny", "stella"}, map[string]string{"pr view headRefOid,comments": body})
	rec, err := g.Look(context.Background(), Task{ID: "read", Kind: KindRead, Ref: "o/n#1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Closed {
		t.Fatalf("a line by a login outside the friends list closed a read")
	}
}

// TestFlipMeasuresTheActualFromTheLease: the estimator's input comes from the record's time,
// not from when the flip happened to run.
func TestFlipMeasuresTheActualFromTheLease(t *testing.T) {
	leased := time.Date(2026, 9, 22, 15, 0, 0, 0, time.UTC)
	g := ghWith(nil, map[string]string{
		"pr view state,mergedAt,mergeCommit": `{"state":"MERGED","mergedAt":"2026-09-22T16:15:00Z","mergeCommit":{"oid":"abc1234567890000000000000000000000000000"}}`,
	})
	task := Task{ID: "t", Kind: KindFix, Ref: "o/n#1", State: StateOpen, LeasedAt: leased}
	changed, problems := Flip(context.Background(), []Task{task}, g, nil, leased.Add(8*time.Hour))
	if len(problems) != 0 {
		t.Fatalf("Flip had problems: %v", problems)
	}
	if len(changed) != 1 || changed[0].Actual != 75 {
		t.Fatalf("the actual is %d minutes; wants 75, measured from the lease to the merge", changed[0].Actual)
	}
}

// TestFlipTakesACardFromTheStream: the ev:cards half, behind its interface, with the fake
// standing in until #2587 lands.
func TestFlipTakesACardFromTheStream(t *testing.T) {
	cards := &FakeCards{Landings: map[string]Record{
		"cell-2026-09-22-a@1": {Closed: true, Evidence: "landed 0cda23d5", At: time.Date(2026, 9, 22, 17, 0, 0, 0, time.UTC)},
	}}
	tasks := []Task{
		{ID: "landed", Kind: KindCard, Ref: "cell-2026-09-22-a@1", State: StateOpen},
		{ID: "still-out", Kind: KindCard, Ref: "cell-2026-09-22-b@1", State: StateOpen},
	}
	changed, problems := Flip(context.Background(), tasks, nil, cards, time.Now().UTC())
	if len(problems) != 0 {
		t.Fatalf("Flip had problems: %v", problems)
	}
	if len(changed) != 1 || changed[0].ID != "landed" || changed[0].Evidence != "landed 0cda23d5" {
		t.Fatalf("Flip changed %+v; wants the landed card alone, with the landed sha as evidence", changed)
	}
}

// TestParseRefSaysWhatItWants: a ref nobody can look up is a refusal with the shape in it.
func TestParseRefSaysWhatItWants(t *testing.T) {
	for _, bad := range []string{"nova-tools", "o/n#", "#12", "o/n#zero", "n#12"} {
		if _, err := ParseRef(bad); err == nil {
			t.Fatalf("ParseRef took %q", bad)
		} else if !strings.Contains(err.Error(), "wants") {
			t.Fatalf("the refusal for %q is %q; it must say what the ref wants", bad, err)
		}
	}
	got, err := ParseRef("mas-bandwidth/nova-tools#2593@" + headSHA)
	if err != nil {
		t.Fatal(err)
	}
	if got.Repo != "mas-bandwidth/nova-tools" || got.Number != 2593 || got.Head != headSHA {
		t.Fatalf("ParseRef returned %+v", got)
	}
}

// TestTheFakeIsStrictLikeTheRealStream (AGENTS.md): an empty label is refused there, so it is
// refused here.
func TestTheFakeIsStrictLikeTheRealStream(t *testing.T) {
	if _, err := (&FakeCards{}).Landed(context.Background(), "  "); err == nil {
		t.Fatalf("the fake took an empty label; the real stream would not")
	}
}
