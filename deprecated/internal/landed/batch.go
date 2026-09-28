package landed

// Prefetch reads every PR a run will ask about before any is asked (#3460). The
// per-PR REST read cost about 0.77 s each, in series: 26 :merged criteria took 20
// s. One GraphQL call per repo (BatchSize PRs to a call) answers them all, so the
// run's forge time grows with the number of repos, not the number of criteria.
//
// The batch only fills the run's PR cache that pull reads; the verdict rules are
// unchanged. A PR the batch could not answer -- the call failed, the forge said
// nothing for that number, or the answer is not the shape expected -- is left out
// of the cache, and pull asks for it by REST as before: no evidence is not
// negative evidence, and each question is still settled once per run.

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// BatchSize is the most PRs one GraphQL call names; a repo with more takes
// ceil(n/BatchSize) calls.
const BatchSize = 100

// batchFields are the fields pull's REST read yields, under their GraphQL names.
const batchFields = "state mergedAt closedAt headRefOid mergeCommit { oid }"

// BatchArgs is the gh argument line of one batch read: the PRs of repo numbered
// numbers, in the order given, each under the alias p<n>. The repo travels as
// GraphQL variables, never inside the query text.
func BatchArgs(repo string, numbers []int) []string {
	owner, name, _ := strings.Cut(repo, "/")
	var q strings.Builder
	q.WriteString("query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) {")
	for _, n := range numbers {
		num := strconv.Itoa(n)
		q.WriteString(" p" + num + ": pullRequest(number: " + num + ") { " + batchFields + " }")
	}
	q.WriteString(" } }")
	return []string{"api", "graphql", "-f", "query=" + q.String(), "-f", "owner=" + owner, "-f", "name=" + name}
}

// Prefetch asks, once per repo, for every PR the subjects name that the run has
// not read yet. Subjects that are not pr: subjects, or do not parse, are skipped:
// Landed and MergedAt answer those as they always did.
func (e *Evaluator) Prefetch(ctx context.Context, subjects []string) {
	want := map[string]map[int]bool{}
	for _, s := range subjects {
		repo, n, _, err := ParsePR(s)
		if err != nil {
			continue
		}
		if _, ok := e.prs[repo+"#"+strconv.Itoa(n)]; ok {
			continue
		}
		if want[repo] == nil {
			want[repo] = map[int]bool{}
		}
		want[repo][n] = true
	}
	repos := make([]string, 0, len(want))
	for repo := range want {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		numbers := make([]int, 0, len(want[repo]))
		for n := range want[repo] {
			numbers = append(numbers, n)
		}
		sort.Ints(numbers)
		for len(numbers) > 0 {
			k := min(len(numbers), BatchSize)
			e.batch(ctx, repo, numbers[:k])
			numbers = numbers[k:]
		}
	}
}

// batchPull is one PR as the batch read spells it.
type batchPull struct {
	State       string  `json:"state"`
	MergedAt    *string `json:"mergedAt"`
	ClosedAt    *string `json:"closedAt"`
	HeadRefOid  string  `json:"headRefOid"`
	MergeCommit *struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
}

// batch reads one call's PRs and caches each one the answer carries in full.
func (e *Evaluator) batch(ctx context.Context, repo string, numbers []int) {
	out, err := e.Run(ctx, BatchArgs(repo, numbers)...)
	if err != nil {
		return
	}
	var body struct {
		Data struct {
			Repository map[string]*batchPull `json:"repository"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &body) != nil {
		return
	}
	for _, n := range numbers {
		bp := body.Data.Repository["p"+strconv.Itoa(n)]
		if bp == nil {
			continue
		}
		var p pull
		switch bp.State {
		case "OPEN":
			p.State = "open"
		case "CLOSED", "MERGED":
			p.State = "closed"
		default:
			continue
		}
		if bp.State == "MERGED" && (bp.MergedAt == nil || *bp.MergedAt == "") {
			continue
		}
		if bp.MergedAt != nil {
			p.MergedAt = *bp.MergedAt
		}
		if bp.ClosedAt != nil {
			p.ClosedAt = *bp.ClosedAt
		}
		if bp.MergeCommit != nil {
			p.MergeCommitSHA = bp.MergeCommit.Oid
		}
		p.Head.SHA = bp.HeadRefOid
		e.prs[repo+"#"+strconv.Itoa(n)] = prResult{pr: p}
	}
}
