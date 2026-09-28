package landed

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// batchAnswer is a GraphQL answer naming each PR under its alias; a nil entry is
// the forge saying nothing for that number.
func batchAnswer(prs map[int]string) string {
	var parts []string
	for n, body := range prs {
		parts = append(parts, `"p`+strconv.Itoa(n)+`":`+body)
	}
	return `{"data":{"repository":{` + strings.Join(parts, ",") + `}}}`
}

func mergedGQL(n int) string {
	return `{"state":"MERGED","mergedAt":"2026-09-22T10:00:00Z","closedAt":"2026-09-22T10:00:00Z","headRefOid":"abc` +
		strconv.Itoa(n) + `","mergeCommit":{"oid":"def` + strconv.Itoa(n) + `"}}`
}

const openGQL = `{"state":"OPEN","mergedAt":null,"closedAt":null,"headRefOid":"aaaaaaa","mergeCommit":null}`

func calls(f *forge, prefix string) int {
	total := 0
	for key, n := range f.calls {
		if strings.HasPrefix(key, prefix) {
			total += n
		}
	}
	return total
}

func key(args []string) string { return strings.Join(args, " ") }

// #3460: 150 PRs in one repo and one in another are read in three calls -- two
// of BatchSize and under for o/r, one for p/q -- and every verdict after that
// comes from the run's cache: no REST read of any PR.
func TestPrefetchReadsEachRepoInBatches(t *testing.T) {
	t.Parallel()

	first, second := map[int]string{}, map[int]string{}
	var subjects []string
	var loN, hiN []int
	for n := 1; n <= 150; n++ {
		body := mergedGQL(n)
		if n%2 == 0 {
			body = openGQL
		}
		if n <= BatchSize {
			first[n] = body
			loN = append(loN, n)
		} else {
			second[n] = body
			hiN = append(hiN, n)
		}
		subjects = append(subjects, "pr:o/r#"+strconv.Itoa(n))
	}
	subjects = append(subjects, "pr:o/r#1@abc1", "pr:p/q#3", "commit:abcdef1", "pr:o/r")
	f := &forge{answers: map[string]string{
		key(BatchArgs("o/r", loN)):      batchAnswer(first),
		key(BatchArgs("o/r", hiN)):      batchAnswer(second),
		key(BatchArgs("p/q", []int{3})): batchAnswer(map[int]string{3: mergedGQL(3)}),
	}}
	e := New(f.run, "o/r", "dev", "")
	ctx := context.Background()
	e.Prefetch(ctx, subjects)
	if got := calls(f, "api graphql"); got != 3 {
		t.Fatalf("151 PRs over two repos took %d batch calls, want 3: %v", got, f.calls)
	}
	for n := 1; n <= 150; n++ {
		want := "yes"
		if n%2 == 0 {
			want = "no"
		}
		if v := e.MergedAt(ctx, "pr:o/r#"+strconv.Itoa(n)); v.Word() != want {
			t.Errorf("MergedAt(#%d) = %s why=%s, want %s", n, v.Word(), v.Why, want)
		}
	}
	for subject, want := range map[string]string{"pr:o/r#1@abc1": "yes", "pr:o/r#1@def1": "yes", "pr:o/r#1@ffff": "no", "pr:p/q#3": "yes"} {
		if v := e.MergedAt(ctx, subject); v.Word() != want {
			t.Errorf("MergedAt(%s) = %s why=%s, want %s", subject, v.Word(), v.Why, want)
		}
	}
	if got := calls(f, "api repos/"); got != 0 {
		t.Errorf("a batched PR was read again by REST (%d calls): %v", got, f.calls)
	}
	// A second Prefetch over the same subjects asks nothing: all are cached.
	e.Prefetch(ctx, subjects)
	if got := calls(f, "api graphql"); got != 3 {
		t.Errorf("a cached PR was batched again: %d calls", got)
	}
}

// The batch answers the fields pull's REST read does: a closed PR carries its
// head and close stamp for the merge rule, a merged one its merge commit.
func TestPrefetchSpellsThePullAsRESTDoes(t *testing.T) {
	t.Parallel()

	f := &forge{answers: map[string]string{
		key(BatchArgs("o/r", []int{1, 2, 3})): batchAnswer(map[int]string{
			1: mergedGQL(1),
			2: `{"state":"CLOSED","mergedAt":null,"closedAt":"2026-09-22T11:00:00Z","headRefOid":"abc123","mergeCommit":null}`,
			3: openGQL,
		}),
	}}
	e := New(f.run, "o/r", "dev", "")
	e.Prefetch(context.Background(), []string{"pr:o/r#3", "pr:o/r#2", "pr:o/r#1"})
	for n, want := range map[int]pull{
		1: {State: "closed", MergedAt: "2026-09-22T10:00:00Z", ClosedAt: "2026-09-22T10:00:00Z", MergeCommitSHA: "def1"},
		2: {State: "closed", ClosedAt: "2026-09-22T11:00:00Z"},
		3: {State: "open"},
	} {
		want.Head.SHA = map[int]string{1: "abc1", 2: "abc123", 3: "aaaaaaa"}[n]
		got, ok := e.prs["o/r#"+strconv.Itoa(n)]
		if !ok || got.err != nil || got.pr != want {
			t.Errorf("pull #%d = %+v (cached %v), want %+v", n, got, ok, want)
		}
	}
}

// A PR the batch did not answer is read by REST, once, as before: a batch that
// failed says nothing, and no evidence is not negative evidence.
func TestPrefetchLeavesTheUnansweredToREST(t *testing.T) {
	t.Parallel()

	f := &forge{answers: map[string]string{
		// #2 is null in the answer; x/y's batch fails outright
		key(BatchArgs("o/r", []int{1, 2})): `{"data":{"repository":{"p1":` + mergedGQL(1) + `,"p2":null}}}`,
		"api repos/o/r/pulls/2":            `{"state":"open","merged_at":null,"head":{"sha":"abc"}}`,
		"api repos/x/y/pulls/5":            `{"state":"closed","merged_at":"2026-09-22T00:00:00Z","head":{"sha":"def"}}`,
	}}
	e := New(f.run, "o/r", "dev", "")
	ctx := context.Background()
	subjects := []string{"pr:o/r#1", "pr:o/r#2", "pr:x/y#5", "pr:x/y#6"}
	e.Prefetch(ctx, subjects)
	for subject, want := range map[string]string{"pr:o/r#1": "yes", "pr:o/r#2": "no", "pr:x/y#5": "yes", "pr:x/y#6": "unknown"} {
		if v := e.MergedAt(ctx, subject); v.Word() != want {
			t.Errorf("MergedAt(%s) = %s why=%s, want %s", subject, v.Word(), v.Why, want)
		}
	}
	for k, want := range map[string]int{
		"api repos/o/r/pulls/1": 0, "api repos/o/r/pulls/2": 1,
		"api repos/x/y/pulls/5": 1, "api repos/x/y/pulls/6": 1,
		key(BatchArgs("x/y", []int{5, 6})): 1,
	} {
		if f.calls[k] != want {
			t.Errorf("gh %s asked %d times, want %d", k, f.calls[k], want)
		}
	}
}

// The repo reaches the query only as GraphQL variables: the query text names no
// owner or repo, whatever a subject spells.
func TestBatchArgsKeepTheRepoOutOfTheQuery(t *testing.T) {
	t.Parallel()

	args := BatchArgs("zed/qux", []int{7, 12})
	if args[0] != "api" || args[1] != "graphql" {
		t.Fatalf("args = %q", args)
	}
	q := args[3]
	if strings.Contains(q, "zed") || strings.Contains(q, "qux") {
		t.Errorf("the query carries the repo: %s", q)
	}
	for _, want := range []string{"p7: pullRequest(number: 7)", "p12: pullRequest(number: 12)"} {
		if !strings.Contains(q, want) {
			t.Errorf("the query lacks %q: %s", want, q)
		}
	}
	if args[5] != "owner=zed" || args[7] != "name=qux" {
		t.Errorf("variables = %q", args[4:])
	}
}
