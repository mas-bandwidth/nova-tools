package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// mergequeue lists the merge queue of a branch (docs/SPEC-SPRINT.md section 11,
// promote): on the night of 2026-10-09 the seat ran gh api graphql against
// mergeQueue entries by hand to see why a pull request had not merged and again
// to see a group ejected. The verb is that read as one line per entry, in
// position order, with a summary. It reads the forge only (no store, no actor)
// and reuses promote's GraphQL client and query style (promote_forge.go).
func init() {
	verbClasses["mergequeue"] = classRead
	verbEffect["mergequeue"] = "inspection: reads the forge's merge queue of one branch and writes nothing"
}

// mergeQueueForge is the forge call the mergequeue verb makes, through the same
// gh client as promote (promote_forge.go): the entries of a branch's merge
// queue. ghForge implements it; a test gives a fake and asks no forge.
type mergeQueueForge interface {
	QueueEntries(ctx context.Context, base string) ([]mergeQueueEntry, error)
}

// mergeQueueCheckNode is one status context of the merge group's head commit: a
// CheckRun carries a name and a conclusion, any other kind is not read.
type mergeQueueCheckNode struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

// mergeQueueEntry is one entry of a branch's merge queue (promoteEntriesQuery).
// Card is the card the head branch names, when it is a card's (mergequeue_card);
// InQueue is how long it has waited, set when the verb renders it.
type mergeQueueEntry struct {
	Position int       `json:"position"`
	Number   string    `json:"pr"`
	Title    string    `json:"title"`
	Branch   string    `json:"branch,omitempty"`
	Card     string    `json:"card,omitempty"`
	State    string    `json:"state"`
	Head     string    `json:"head,omitempty"`
	Check    string    `json:"check,omitempty"`
	Enqueued time.Time `json:"enqueued"`
	InQueue  string    `json:"in_queue,omitempty"`
}

// mergeQueueView is the whole read: the branch, its entries in position order,
// their count and the entry queued longest.
type mergeQueueView struct {
	Base    string            `json:"base"`
	Entries []mergeQueueEntry `json:"entries"`
	Count   int               `json:"count"`
	Oldest  *mergeQueueEntry  `json:"oldest,omitempty"`
}

// cmdMergeQueue is `nova-sprint mergequeue`: one line per entry of the base's
// merge queue with its position, pull request, card, state, merge group and
// time in queue, then a summary with the count and the oldest entry. --base is
// the branch whose queue is read (default dev, the branch the sprint promotes
// into); --json prints the one value.
func (a *app) cmdMergeQueue(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("mergequeue")
	base := fs.String("base", basesTrunk, "the branch whose merge queue to list (default dev, the branch the sprint promotes into)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "mergequeue", argErr("takes no words ", err, pos...))
	}
	if strings.TrimSpace(*base) == "" {
		return refuse(stderr, "mergequeue", "--base wants the branch whose merge queue to list, e.g. "+basesTrunk)
	}
	v, err := a.listMergeQueue(context.Background(), *base)
	if err != nil {
		return a.readFailed("mergequeue", err, stderr)
	}
	if c.json {
		// ignored: strings, ints, times and slices always encode
		b, _ := json.Marshal(v)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, mergeQueueText(v))
	return 0
}

// mergeQueuer is the forge the verb asks: the app's merge-queue client when it
// can list entries (a test's fake), else gh through promote's client.
func (a *app) mergeQueuer() mergeQueueForge {
	if f, ok := a.mergeQueue.(mergeQueueForge); ok {
		return f
	}
	return ghForge{p: &promoter{dir: "", env: a.gitEnv}}
}

// listMergeQueue reads the base's merge queue and renders it: the entries in
// position order, each with the card its head branch names and its time in
// queue, and the oldest entry.
func (a *app) listMergeQueue(ctx context.Context, base string) (mergeQueueView, error) {
	entries, err := a.mergeQueuer().QueueEntries(ctx, base)
	if err != nil {
		return mergeQueueView{}, err
	}
	return mergeQueueViewOf(base, entries, a.now()), nil
}

// mergeQueueViewOf is the view over the forge's answer: position order, the card
// each head branch names, the time in queue at now, and the oldest entry.
func mergeQueueViewOf(base string, entries []mergeQueueEntry, now time.Time) mergeQueueView {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Position < entries[j].Position })
	v := mergeQueueView{Base: base, Entries: entries, Count: len(entries)}
	for i := range v.Entries {
		e := &v.Entries[i]
		e.Card = mergeQueueCard(e.Branch)
		e.InQueue = mergeQueueAge(now.Sub(e.Enqueued))
		if v.Oldest == nil || e.Enqueued.Before(v.Oldest.Enqueued) {
			v.Oldest = e
		}
	}
	return v
}

// mergeQueueText is the view in lines: a MERGEQUEUE ENTRY line an entry, then the
// MERGEQUEUE OK summary with the count, the base and the oldest entry.
func mergeQueueText(v mergeQueueView) string {
	var b strings.Builder
	for _, e := range v.Entries {
		fmt.Fprintf(&b, "MERGEQUEUE ENTRY pos=%d pr=%s state=%s card=%s head=%s check=%s in=%s title=%s\n",
			e.Position, oneline.Field(e.Number), oneline.Field(e.State), oneline.Field(dashed(e.Card)),
			oneline.Field(dashed(e.Head)), oneline.Field(dashed(e.Check)), oneline.Field(e.InQueue), oneline.Field(e.Title))
	}
	fmt.Fprintf(&b, "MERGEQUEUE OK entries=%d base=%s", v.Count, oneline.Field(v.Base))
	if v.Oldest != nil {
		fmt.Fprintf(&b, " oldest=%s in=%s", oneline.Field(v.Oldest.Number), oneline.Field(v.Oldest.InQueue))
	}
	b.WriteByte('\n')
	return b.String()
}

// mergeQueueState is a MergeQueueEntryState enum as the queue's words.
func mergeQueueState(state string) string {
	switch state {
	case "AWAITING_CHECKS":
		return "awaiting checks"
	case "LOCKED":
		return "locked"
	case "MERGEABLE":
		return "mergeable"
	case "QUEUED":
		return "queued"
	case "UNMERGEABLE":
		return "unmergeable"
	case "":
		return "-"
	default:
		return strings.ToLower(strings.ReplaceAll(state, "_", " "))
	}
}

// mergeQueueCheck is the merge group's head check run: a failing one when there
// is, else the first, as name:conclusion (the name alone with no conclusion).
func mergeQueueCheck(nodes []mergeQueueCheckNode) string {
	var pick *mergeQueueCheckNode
	for i := range nodes {
		n := &nodes[i]
		if n.Typename != "" && n.Typename != "CheckRun" {
			continue
		}
		if pick == nil {
			pick = n
		}
		if !mergeQueueCheckPassed(n.Conclusion) {
			pick = n
			break
		}
	}
	if pick == nil {
		return ""
	}
	if pick.Conclusion == "" {
		return pick.Name
	}
	return pick.Name + ":" + pick.Conclusion
}

// mergeQueueCheckPassed says a check run's conclusion is a green one.
func mergeQueueCheckPassed(conclusion string) bool {
	switch conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return true
	}
	return false
}

// mergeQueueCardPattern is a card's work branch (sprint.BranchOf:
// sprint/<prefix><workCard>.g<gen>.e<epoch>), the card the part before it.
var mergeQueueCardPattern = regexp.MustCompile(`^sprint/(.+)\.w\d+\.g\d+\.e\d+$`)

// mergeQueueCard is the card a head branch names, "" when the branch is no
// card's (a promotion branch, a stream branch).
func mergeQueueCard(branch string) string {
	m := mergeQueueCardPattern.FindStringSubmatch(strings.TrimSpace(branch))
	if m == nil {
		return ""
	}
	return m[1]
}

// mergeQueueAge is a wait as the line prints it, rounded to the second, never
// below zero.
func mergeQueueAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}
