package decide_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// TestJevVerdictOnceInStream is the DONE-WHEN of card nx-e15-jev-ledger-to-stream:
// every Jev verdict appears once in the stream as kind=jev with PR, head and score,
// and the fold (internal/events, the attempts table) holds it as one row per write.
func TestJevVerdictOnceInStream(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stream := events.NewFakeStream()

	verdicts := []struct {
		repo  string
		pr    int
		head  string
		score int
	}{
		{"mas-bandwidth/nova-tools", 2563, "abc123def456", 7},
		{"mas-bandwidth/schema", 1585, "def789abc012", 9},
		{"mas-bandwidth/nova-tools", 2580, "0123456789ab", 5},
	}

	var ids []string
	for i, v := range verdicts {
		id, err := decide.WriteJevLedger(ctx, stream, decide.JevVerdict{Repo: v.repo, PR: v.pr, Head: v.head, Verdict: "PASS", Score: v.score, Scored: true})
		if err != nil {
			t.Fatalf("verdict #%d: %v", i, err)
		}
		ids = append(ids, id)
	}

	// Every call produces exactly one entry with a unique id.
	if len(ids) != 3 {
		t.Fatalf("got %d ids, want 3", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("ids[%d] == ids[%d] == %s; the stream mints a new id per call", i, i-1, ids[i])
		}
	}

	// Fold the stream: the fold's jev rows carry PR, head and score (=route).
	db, err := events.OpenDB(ctx, filepath.Join(t.TempDir(), "jev-fold.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	folder := &events.Folder{Reader: stream, DB: db, Group: "fold", Consumer: "test-1", Count: 10}
	if err := folder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := folder.Once(ctx); err != nil {
		t.Fatal(err)
	}

	count, err := db.CountKind(ctx, events.Jev)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("the fold holds %d jev rows, want 3", count)
	}

	// Dump the fold and verify every verdict is present with its fields.
	var dump bytes.Buffer
	if err := db.Dump(ctx, &dump); err != nil {
		t.Fatal(err)
	}
	body := dump.String()
	for _, v := range verdicts {
		label := v.repo + "#" + strconv.Itoa(v.pr)
		if !strings.Contains(body, label) {
			t.Errorf("the fold does not contain label %q (repo#pr, one row per pull request)", label)
		}
		if !strings.Contains(body, strconv.Itoa(v.pr)) {
			t.Errorf("the fold does not contain pr %d", v.pr)
		}
		if !strings.Contains(body, v.head) {
			t.Errorf("the fold does not contain head %q", v.head)
		}
		scoreRoute := fmt.Sprintf("score/%d", v.score)
		if !strings.Contains(body, scoreRoute) {
			t.Errorf("the fold does not contain score %d as route %q", v.score, scoreRoute)
		}
	}

	// A second write of the same verdict produces a new stream entry (different id),
	// and the fold holds both because each event has its own id.
	id, err := decide.WriteJevLedger(ctx, stream, decide.JevVerdict{Repo: "mas-bandwidth/nova-tools", PR: 2563, Head: "abc123def456", Verdict: "PASS", Score: 7, Scored: true})
	if err != nil {
		t.Fatal(err)
	}
	if id == ids[0] {
		t.Fatal("a second write of the same data got the same id; the stream mints a new one every time")
	}
	if _, err := folder.Once(ctx); err != nil {
		t.Fatal(err)
	}
	count, err = db.CountKind(ctx, events.Jev)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("the fold holds %d jev rows after a duplicate write, want 4 (different event id = different row)", count)
	}
}

// TestJevLedgerLabelIsOnePullRequest. The label is the id the fold's by_label and
// totals views group on; a repo alone there made every Jev verdict across a
// repository one pseudo-card (Emma, PR #2788). The label is repo#pr.
func TestJevLedgerLabelIsOnePullRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stream := events.NewFakeStream()
	for _, pr := range []int{11, 12} {
		if _, err := decide.WriteJevLedger(ctx, stream, decide.JevVerdict{Repo: "mas-bandwidth/schema", PR: pr, Head: "8d2213c7a6ea7ac0359e1020edaaa7914b8f8df3", Verdict: "UNSURE", Score: 5, Scored: true, Model: "jev-latest"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := stream.Range(ctx, "-", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d entries, want 2", len(got))
	}
	for i, want := range []string{"mas-bandwidth/schema#11", "mas-bandwidth/schema#12"} {
		f := got[i].Fields
		if f["label"] != want || f["event"] != "jev" || f["model"] != "jev-latest" || f["route"] != "score/5" {
			t.Errorf("entry %d = %v, want label %s event jev model jev-latest route score/5", i, f, want)
		}
	}
}

// TestJevLedgerUnscoredIsNotAZero. A Jev line printed score=- (no provider,
// --no-jev, or an unanswered ask) still goes to the stream, and its route says
// no score was given instead of a number nobody gave.
func TestJevLedgerUnscoredIsNotAZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stream := events.NewFakeStream()
	if _, err := decide.WriteJevLedger(ctx, stream, decide.JevVerdict{Repo: "a/b", PR: 1, Head: "abcdef1", Verdict: "UNSURE"}); err != nil {
		t.Fatal(err)
	}
	got, err := stream.Range(ctx, "-", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Fields["route"] != "score/-" || got[0].Fields["model"] != "jev" {
		t.Fatalf("entries = %v, want one with route score/- and model jev", got)
	}
}

// TestJevLedgerRefusesWhatIsNotAVerdict. Emma's adversarial probes on PR #2788:
// a score outside the friends' 1-10, an empty head, a pull request number that
// is not one, and a repo that is not owner/name are refused before the stream.
func TestJevLedgerRefusesWhatIsNotAVerdict(t *testing.T) {
	t.Parallel()
	ok := decide.JevVerdict{Repo: "a/b", PR: 1, Head: "abcdef1", Verdict: "PASS", Score: 8, Scored: true}
	for _, tc := range []struct {
		name string
		edit func(v *decide.JevVerdict)
		want string
	}{
		{"score zero", func(v *decide.JevVerdict) { v.Score = 0 }, "1-10"},
		{"score negative", func(v *decide.JevVerdict) { v.Score = -3 }, "1-10"},
		{"score eleven", func(v *decide.JevVerdict) { v.Score = 11 }, "1-10"},
		{"empty head", func(v *decide.JevVerdict) { v.Head = "" }, "head"},
		{"head not a sha", func(v *decide.JevVerdict) { v.Head = "main" }, "head"},
		{"no pr", func(v *decide.JevVerdict) { v.PR = 0 }, "pull request"},
		{"empty repo", func(v *decide.JevVerdict) { v.Repo = "" }, "owner/name"},
		{"repo without owner", func(v *decide.JevVerdict) { v.Repo = "nova-tools" }, "owner/name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := events.NewFakeStream()
			v := ok
			tc.edit(&v)
			_, err := decide.WriteJevLedger(context.Background(), stream, v)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a refusal naming %q", err, tc.want)
			}
			if stream.Len() != 0 {
				t.Fatalf("a refused verdict reached the stream (%d entries)", stream.Len())
			}
		})
	}
	if _, err := decide.WriteJevLedger(context.Background(), events.NewFakeStream(), ok); err != nil {
		t.Fatalf("the control verdict was refused: %v", err)
	}
}
