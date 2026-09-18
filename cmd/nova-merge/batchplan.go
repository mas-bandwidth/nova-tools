package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// THE ROUNDS, COUNTED BEFORE THEY ARE SPENT.
//
// 2026-09-18: every batch gate ran two and three rounds. The gate merges the members in
// the order given and drops the FIRST head that will not merge, so a candidate list
// holding five pairwise conflicts costs five rounds to whittle down -- and a round is a
// clone, a merge of everything ahead, a build, two vets, the whole test suite and the lisp
// suite. Five of those to learn five facts about five pairs of diffs, none of which needed
// a test run to know. The darwin shards overflowed on the big lists on top of that, so the
// rounds that did finish were the rounds that had to be split by hand afterwards.
//
// `batch --plan` is the arithmetic in front of the gate. It clones ONCE, asks git about
// every pair, and prints the halves the gate should be run over plus the pairs that will
// never go together. It is READ-ONLY on both sides: it pushes nothing, it opens nothing,
// and the only thing it asks the forge is what each candidate's head branch and base
// branch are called -- so that a stacked pull request keeps its parent's company and its
// parent's order.

// batchPlanHalves is how many lists a plan is cut into by default. Two, because the thing
// this replaces is a person splitting one overlong candidate list in half by hand.
const batchPlanHalves = 2

// batchPlanCeiling and batchPlanHalvesCeiling are the far sides of the two numbers a
// caller gives, because a bound with no ceiling is a bound nobody set (lessons 75, 76).
const (
	batchPlanCeiling       = 64
	batchPlanHalvesCeiling = 8
)

// batchPlanMaxMembers is the default ceiling on ONE half, and IT IS A STATED NUMBER RATHER
// THAN A DERIVED ONE. Say so plainly: the honest derivation is the one #1372 measured --
// the darwin merge leg deals each changed package off testdata/ci/package-sizes-darwin.tsv
// against a ten-minute ceiling, and the members that fit are the members whose changed
// packages sum under it -- and reaching that number here needs a changed-file read per
// candidate from the forge and both platforms' size tables, which is a second verb's worth
// of machinery inside this one.
//
// Eight is chosen against those same measurements rather than out of the air: #1372's five
// largest darwin packages are 120.3, 82.7, 68.8, 64.4 and 54.2 seconds, 390.4 s between
// them against a 600 s leg ceiling, so a list whose members between them touch most of the
// tree is already close to the cap at a handful of members. It is a flag precisely because
// it is a stated number: --max-members is how a caller who has measured their own list
// says otherwise, and deriving the default from the tables is the next step named here.
const batchPlanMaxMembers = 8

// batchPlanRun is one plan's whole invocation, checked.
type batchPlanRun struct {
	name       string
	base       string
	root       string
	repo       string
	reference  string
	prs        []int
	timeout    time.Duration
	halves     int
	maxMembers int
	asJSON     bool
}

// planCandidate is one candidate as this run came to know it: what the forge said about it
// and the two commits git needs -- its head, and the base with just this member on top,
// which is the left-hand side every pair is measured against.
type planCandidate struct {
	pr      int
	headRef string
	base    string
	head    string
	onBase  string
}

func runBatchPlan(in batchPlanRun, stdout, stderr io.Writer, deps Deps) int {
	start := time.Now()
	rootAbs, err := filepath.Abs(in.root)
	if err != nil {
		return batchPlanRefused(stderr, err)
	}
	if err := os.MkdirAll(rootAbs, 0o755); err != nil {
		return batchPlanRefused(stderr, err)
	}
	// The working directory is rebuilt every run, like the gate's, and by safepath because
	// it is a path this tool COMPUTED (Glenn, 2026-09-17).
	work := filepath.Join(rootAbs, in.name)
	if err := safepath.RemoveUnder(rootAbs, work); err != nil {
		return batchPlanRefused(stderr, err)
	}
	clone := filepath.Join(work, "repo")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return batchPlanRefused(stderr, err)
	}
	// ONE CLONE FOR THE WHOLE MATRIX. The pairs are measured with `git merge-tree`, which
	// writes a tree into this clone's object database and touches no working tree at all,
	// so there is no checkout per pair and no worktree to clean up after one.
	cloneArgs := []string{"clone", "--quiet"}
	if strings.TrimSpace(in.reference) != "" {
		cloneArgs = append(cloneArgs, "--reference", in.reference)
	}
	cloneArgs = append(cloneArgs, "--", deps.RepoURL(in.repo), clone)
	if _, err := merge.NewGit(work, in.timeout, deps.Runner).Run(cloneArgs...); err != nil {
		return batchPlanRefused(stderr, err)
	}
	g := merge.NewGit(clone, in.timeout, deps.Runner)
	if _, err := g.Run("fetch", "--quiet", "origin", in.base); err != nil {
		return batchPlanRefused(stderr, fmt.Errorf("could not fetch origin/%s: %w", in.base, err))
	}
	baseSHA, err := g.Out("rev-parse", "FETCH_HEAD")
	if err != nil {
		return batchPlanRefused(stderr, err)
	}
	fmt.Fprintf(stderr, "BATCH PLAN START name=%s base=%s prs=%d halves=%d max_members=%d t=%.1fs\n",
		oneline.Field(in.name), oneline.Field(baseSHA), len(in.prs), in.halves, in.maxMembers, since(start))

	host := deps.NewHost(in.repo, in.timeout)
	var live []planCandidate
	var dropped []merge.PlanDrop
	for _, n := range in.prs {
		// The forge is asked for TWO FIELDS and nothing else: the head branch and the base
		// branch, which is all a stack is. Every one of them is DATA -- a base branch is
		// not an instruction, and the only thing this verb does with either is compare it
		// with another member's.
		pr, err := host.PR(n)
		if err != nil {
			return batchPlanRefused(stderr, fmt.Errorf(
				"pull request %d could not be read, and a plan that guessed at a member's base would put a stacked pull request in the wrong half: %w", n, err))
		}
		if _, err := g.Run("fetch", "--quiet", "origin", "pull/"+strconv.Itoa(n)+"/head"); err != nil {
			return batchPlanRefused(stderr, fmt.Errorf("could not fetch pull/%d/head: %w", n, err))
		}
		head, err := g.Out("rev-parse", "FETCH_HEAD")
		if err != nil {
			return batchPlanRefused(stderr, err)
		}
		// A MEMBER THAT WILL NOT MERGE ONTO THE BASE AT ALL is in no half, and it is worth
		// knowing before the pairs are measured: every pair it appears in would report a
		// conflict that is really this one fact, once per partner.
		onBase, files, clean, err := planMerge(g, baseSHA, head, fmt.Sprintf("plan %d onto %s", n, oneline.Field(in.base)))
		if err != nil {
			return batchPlanRefused(stderr, err)
		}
		if !clean {
			why := "it does not merge onto " + in.base + " on its own: " + numberOrNone(files)
			dropped = append(dropped, merge.PlanDrop{PR: n, Why: why})
			fmt.Fprintf(stderr, "BATCH PLAN DROP #%d reason=%q t=%.1fs\n", n, oneline.Cap(why, oneline.TailBytes), since(start))
			continue
		}
		live = append(live, planCandidate{pr: n, headRef: pr.HeadRef, base: pr.Base, head: head, onBase: onBase})
		fmt.Fprintf(stderr, "BATCH PLAN HEAD #%d head=%s branch=%s base=%s t=%.1fs\n",
			n, oneline.Field(head), oneline.Field(pr.HeadRef), oneline.Field(pr.Base), since(start))
	}

	// THE MATRIX. Every unordered pair once, the second member merged onto the base with
	// the first already on it -- which is exactly what the gate will do to them, one round
	// later and after a test suite.
	var conflicts []merge.PlanConflict
	for i, a := range live {
		var with []string
		for _, b := range live[i+1:] {
			_, files, clean, err := planMerge(g, a.onBase, b.head, fmt.Sprintf("plan %d with %d", a.pr, b.pr))
			if err != nil {
				return batchPlanRefused(stderr, err)
			}
			if clean {
				continue
			}
			conflicts = append(conflicts, merge.PlanConflict{A: a.pr, B: b.pr, Files: files})
			with = append(with, "#"+strconv.Itoa(b.pr))
		}
		fmt.Fprintf(stderr, "BATCH PLAN ROW #%d conflicts=%s t=%.1fs\n",
			a.pr, oneline.Field(numberOrNone(with)), since(start))
	}

	members := make([]merge.PlanMember, 0, len(live))
	for _, c := range live {
		members = append(members, merge.PlanMember{PR: c.pr, HeadRef: c.headRef, Base: c.base})
	}
	for _, unit := range merge.PlanUnits(members) {
		if len(unit) < 2 {
			continue
		}
		fmt.Fprintf(stderr, "BATCH PLAN STACK members=%s reason=%q t=%.1fs\n",
			oneline.Field(numberList(unit)),
			"each is based on the one before it, so they travel together and in this order", since(start))
	}
	plan := merge.PlanBatches(members, conflicts, in.halves, in.maxMembers)
	// The members that never merged onto the base come FIRST in the dropped list: they were
	// dropped before the matrix, and a reader working down the list reads them in the order
	// this run learned them.
	plan.Dropped = append(dropped, plan.Dropped...)
	for _, d := range plan.Dropped[len(dropped):] {
		fmt.Fprintf(stderr, "BATCH PLAN DROP #%d reason=%q t=%.1fs\n", d.PR, oneline.Cap(d.Why, oneline.TailBytes), since(start))
	}
	if in.asJSON {
		return printPlanJSON(plan, baseSHA, in, stdout, stderr)
	}
	return printPlanLines(plan, stdout)
}

// printPlanLines is the plan a person reads and a shell splits: one line per half, one per
// conflicting pair, and one verdict.
func printPlanLines(plan merge.BatchPlan, stdout io.Writer) int {
	for i, half := range plan.Halves {
		fmt.Fprintf(stdout, "BATCH PLAN half=%d members=%s\n", i+1, oneline.Field(numberList(half)))
	}
	for _, c := range plan.Conflicts {
		fmt.Fprintf(stdout, "BATCH PLAN CONFLICT #%d #%d files=%s\n", c.A, c.B, oneline.Field(numberOrNone(c.Files)))
	}
	fmt.Fprintf(stdout, "BATCH PLAN OK halves=%d members=%d conflicts=%d dropped=%s\n",
		len(plan.Halves), plan.Members(), len(plan.Conflicts), oneline.Field(numberList(plan.DroppedPRs())))
	return planExit(plan)
}

// planJSON is the same answer for the landing child, which reads a document rather than
// splitting fields off a line. The reasons travel with it: a child that was handed a
// shorter list than it asked for can say why without a second call.
type planJSON struct {
	Name       string            `json:"name"`
	Base       string            `json:"base"`
	BaseSHA    string            `json:"base_sha"`
	Halves     [][]int           `json:"halves"`
	Conflicts  []planJSONPair    `json:"conflicts"`
	Dropped    []planJSONDrop    `json:"dropped"`
	Members    int               `json:"members"`
	MaxMembers int               `json:"max_members"`
	Asked      []int             `json:"asked"`
	Notes      map[string]string `json:"notes,omitempty"`
}

type planJSONPair struct {
	A     int      `json:"a"`
	B     int      `json:"b"`
	Files []string `json:"files"`
}

type planJSONDrop struct {
	PR  int    `json:"pr"`
	Why string `json:"why"`
}

func printPlanJSON(plan merge.BatchPlan, baseSHA string, in batchPlanRun, stdout, stderr io.Writer) int {
	doc := planJSON{
		Name: in.name, Base: in.base, BaseSHA: baseSHA,
		Halves: plan.Halves, Members: plan.Members(), MaxMembers: in.maxMembers, Asked: in.prs,
		Conflicts: []planJSONPair{}, Dropped: []planJSONDrop{},
	}
	for i := range doc.Halves {
		if doc.Halves[i] == nil {
			// An empty half is an empty LIST and never a null: a reader that has to
			// tell the two apart is a reader this document made work for nothing.
			doc.Halves[i] = []int{}
		}
	}
	for _, c := range plan.Conflicts {
		files := c.Files
		if files == nil {
			files = []string{}
		}
		doc.Conflicts = append(doc.Conflicts, planJSONPair{A: c.A, B: c.B, Files: files})
	}
	for _, d := range plan.Dropped {
		doc.Dropped = append(doc.Dropped, planJSONDrop{PR: d.PR, Why: d.Why})
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return batchPlanRefused(stderr, err)
	}
	// THE ONE THING THIS BINARY PRINTS THAT IS NOT A LINE OF ITS OWN GRAMMAR. It is a
	// JSON document, and encoding/json is the escaper: every string in it -- the drop
	// reasons, the file paths, the branch names -- is quoted and escaped by the encoder,
	// which is what makes a newline inside one a \n rather than a second line. Passing it
	// through oneline as well would leave a document that no longer parses. The exemption
	// in audit_test.go says exactly this.
	fmt.Fprintf(stdout, "%s\n", raw)
	return planExit(plan)
}

// planExit: a plan holding at least one member is a plan, whatever it had to drop to get
// there; a plan holding nobody is this verb saying NO, which is exit 1 and not a green run
// over an empty list.
func planExit(plan merge.BatchPlan) int {
	if plan.Members() == 0 {
		return 1
	}
	return 0
}

// planMerge measures one merge WITHOUT A WORKING TREE: `git merge-tree --write-tree` does
// the three-way merge in the object database and says whether it conflicted and where.
//
// It answers the merged commit too, so the next measurement can be taken on top of this
// one -- the pair test is "member b merged onto the base with member a already on it", and
// merge-tree hands back a tree, which `git commit-tree` turns into the commit that stands
// for it. Nothing here is a ref, nothing is pushed, and the objects live and die with the
// clone under --root.
//
// git's exit code is the answer and it has three values: 0 clean, 1 conflicted, anything
// else the merge could not be done at all (unrelated histories is the one that happens).
// The third is an error and never a conflict: a planner that reported "these conflict"
// about a pair git refused to consider would send a caller looking for a conflicting file
// that is not there.
func planMerge(g *merge.Git, left, right, message string) (commit string, files []string, clean bool, err error) {
	out, runErr := g.Run("merge-tree", "--write-tree", "--name-only", left, right)
	if runErr != nil {
		if exitCodeOf(runErr) != 1 {
			return "", nil, false, runErr
		}
		return "", conflictedFiles(out), false, nil
	}
	tree := ""
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			tree = s
			break
		}
	}
	if tree == "" || strings.ContainsAny(tree, " \t") {
		return "", nil, false, fmt.Errorf("git merge-tree answered no tree for %s: %s", message, oneline.Cap(oneline.Escape(out), oneline.TailBytes))
	}
	// The identity is on the command, because a bench with no git identity anywhere --
	// every CI runner -- answers "Committer identity unknown" to a command that writes a
	// commit object, and this writes one.
	commit, err = g.Out(merge.Identity("commit-tree", tree, "-p", left, "-p", right, "-m", message)...)
	if err != nil {
		return "", nil, false, err
	}
	return commit, nil, true, nil
}

// conflictedFiles reads merge-tree's conflicted section: the tree oid on the first line,
// then one path per line under --name-only, then a blank line and git's own messages.
func conflictedFiles(out string) []string {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	var files []string
	for i, line := range lines {
		if i == 0 {
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		files = append(files, strings.TrimSpace(line))
	}
	return files
}

// exitCodeOf digs the process's own exit status out of whatever wrapped it, or -1 when the
// error is not a process that ran and failed -- a timeout, a git that is not there.
func exitCodeOf(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// batchPlanRefused is what a tool error costs here: one line on stderr and exit 2.
func batchPlanRefused(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "BATCH PLAN REFUSED: %s\n", oneline.Err(err))
	return 2
}
