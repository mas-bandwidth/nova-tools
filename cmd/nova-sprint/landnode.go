package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

func init() {
	// verbs.go replaces the verb slice in its own init; the class map is not
	// replaced. land-node stays a worker verb (coordinator.go, classWorker).
	verbClasses["land-node"] = classWorker
}

// cmdLandNode is nova-sprint land-node (docs/SPEC-SPRINT.md, the subsection
// ### merge-tree-node under section 7; tla/LandTwoLevel.tla NodeStage,
// NodeBlame, GreenLen, StagedGreen). The member named by --as merges that
// stream's queued cards onto the current base in rank order, gates the
// result, and bisects a red --check down to the one card that turns the tip
// red. The longest green prefix is pushed, never forced, to land/<stream>.
// Each queued card gets one verdict in the store. Nothing is written to a
// landed record: the model's lands are unchanged, and landing on the base is
// the root's.
//
// Merge, the per-card checks and the gate rerun are the lander's (build,
// runCheck, gateRerun in land.go and landgate.go). They are called, not
// copied. land.go on this base has no bisectRed; that function is
// landbatch.go on the merge-tree-proof branch, which this card does not commit.
// A red --check is cut by landNodeBisect, GreenLen's halving, probed with
// runCheck and gateRerun. build also tree-gates each card as it merges
// (gateCard). The model gates the batch once and then bisects. On a tree
// with no module, gateCard does not run. Where a card fails the tree gate or
// does not merge, build stops at that card, which is the same cut GreenLen
// gives for the first bad prefix.
func (a *app) cmdLandNode(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("land-node")
	stream := fs.String("stream", "", "the stream whose queued cards are landed onto land/<stream>")
	as := fs.String("as", "", "the member that runs this land; the actor recorded with the verdicts")
	repoDir := fs.String("repo-dir", "", "the clone to merge in, its origin the remote land/<stream> is pushed to")
	baseFlag := fs.String("base", "", "the base branch of a card whose brief names no BASE: line")
	check := fs.String("check", "", "a command run by sh -c in the clone on the merged tip, and on each bisect probe (bounded to 30m); a red result is bisected")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "land-node", err.Error())
	}
	var bad []string
	if len(pos) > 0 {
		bad = append(bad, "takes no words; the stream is --stream <s> and the member is --as <member>")
	}
	if *stream == "" || !sprint.ValidID(*stream) {
		bad = append(bad, "--stream wants one stream name")
	}
	if *as == "" || !sprint.ValidID(*as) {
		bad = append(bad, "--as wants one member name")
	}
	if *repoDir == "" {
		bad = append(bad, "--repo-dir wants the member's clone; land-node does not clone into the land root")
	}
	if strings.HasPrefix(*baseFlag, "-") {
		bad = append(bad, "--base wants a branch name, not "+*baseFlag)
	}
	if len(bad) > 0 {
		return refuse(stderr, "land-node", strings.Join(bad, "; "))
	}
	abs, err := filepath.Abs(*repoDir)
	if err != nil {
		return refuse(stderr, "land-node", "--repo-dir: "+oneline.Err(err))
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return refuse(stderr, "land-node", "--repo-dir wants an existing clone; "+abs+" is not a directory")
	}
	if root, under := a.landNodeInLandRoot(abs); under {
		return landNodeStop(stderr, "the clone "+abs+" is under the land root "+root+"; land-node runs on the member that takes the job, never in the lander's own clone; nothing was fetched or pushed")
	}
	// The actor is the member, whatever --actor or NOVA_SPRINT_ACTOR say
	// (coordinator.go, orActor). Checked before the store is opened.
	c.orActor(*as)
	ctx := context.Background()
	a.serial.Lock()
	st, err := a.store(*c)
	a.serial.Unlock()
	if err != nil {
		return a.readFailed("land-node", err, stderr)
	}
	a.serial.Lock()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	a.serial.Unlock()
	if err != nil {
		return a.readFailed("land-node", err, stderr)
	}
	if ctl := s.StreamCtl(*stream); ctl == nil {
		return landNodeStop(stderr, "no such stream; nothing was fetched or pushed; run: nova-sprint where")
	} else if ctl.F("state") == sprint.StreamStopped {
		return landNodeStop(stderr, "stopped ("+ctl.F("cause")+"); nothing was fetched or pushed; run: nova-sprint resume --stream "+*stream)
	}
	queue := landQueue(s, *stream)
	if len(queue) == 0 {
		return landNodeStop(stderr, "nothing queued to merge in stream "+*stream+"; nothing was fetched or pushed; run: nova-sprint queue --stream "+*stream)
	}
	l := &lander{
		a: a, c: *c, st: st, repoDir: abs, base: *baseFlag, check: *check,
		twin: a.twinOpen(c.redis), epoch: st.PinnedEpoch(), diffs: map[string]string{}, scope: map[string][]string{},
		// gateRerun is the lander's. land-node does not call landGate: that
		// opens a record under the land root, which this verb does not use.
		// A check whose output is not go-test failures returns unchanged.
		gateNote: "land-node keeps no land root and makes no gate record; a red check stays red",
	}
	cards := landNodeCards(s, l.base, queue)
	for i := 1; i < len(cards); i++ {
		if cards[i].repo != cards[0].repo || cards[i].base != cards[0].base {
			return landNodeStop(stderr, "the queue of "+*stream+" names more than one repository or base; land-node lands one base in one clone; nothing was fetched or pushed")
		}
	}
	if why, _ := l.placeWhy(*stream, cards); why != "" {
		return landNodeStop(stderr, why)
	}
	dir, why := l.clone(ctx, cards[0].repo)
	if why != "" {
		return landNodeStop(stderr, why)
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return landNodeStop(stderr, "the clone "+dir+" is not clean ("+firstLine(out, err)+"); nothing was fetched or pushed")
	}
	times := &landTimes{}
	merged, failed, why := l.build(ctx, dir, *stream, cards, times)
	if why != "" {
		// A fetch, a cut, or a base that fails its tree gate blames no card.
		// The base-red judgment is the root's (treeGateBase); this verb
		// writes nothing and pushes nothing.
		return landNodeStop(stderr, why+"; no card is blamed and nothing was pushed or reported")
	}
	for i, id := range merged {
		if i >= len(cards) || cards[i].id != id {
			return landNodeStop(stderr, "the merge order is not the queue order; nothing was pushed or reported")
		}
	}
	baseSha, err := l.git(ctx, dir, "rev-parse", "--verify", "refs/remotes/origin/"+cards[0].base)
	if err != nil {
		return landNodeStop(stderr, "the base has no tip after the fetch: "+firstLine("", err)+"; nothing was pushed or reported")
	}
	listed, err := l.git(ctx, dir, "rev-list", "--first-parent", "--reverse", baseSha+"..HEAD")
	if err != nil {
		return landNodeStop(stderr, "the batch commits could not be listed: "+firstLine("", err)+"; nothing was pushed or reported")
	}
	var commits []string
	if strings.TrimSpace(listed) != "" {
		commits = strings.Split(strings.TrimSpace(listed), "\n")
	}
	if len(commits) != len(merged) {
		return landNodeStop(stderr, fmt.Sprintf("the batch has %d commits and %d merged cards; nothing was pushed or reported", len(commits), len(merged)))
	}
	tips := append([]string{baseSha}, commits...)
	green := len(merged)
	blame := -1
	blameWhy := ""
	blameConflict := false
	if len(merged) > 0 && l.check != "" {
		tip := tips[len(merged)]
		w, out := l.runCheck(ctx, dir)
		if w != "" {
			w = l.gateRerun(ctx, dir, *stream, cards[0].base, tip, cards[:len(merged)], w, out)
		}
		if w != "" {
			lo, line, berr := l.landNodeBisect(ctx, dir, *stream, cards[0].base, tips, cards[:len(merged)])
			if berr != nil {
				return landNodeStop(stderr, oneline.Err(berr)+"; nothing was pushed or reported")
			}
			green, blame, blameWhy = lo, lo, line
		}
	}
	if blame < 0 && failed.id != "" {
		at := -1
		for i := range cards {
			if cards[i].id == failed.id {
				at = i
				break
			}
		}
		if at != len(merged) {
			return landNodeStop(stderr, "the card that stopped the merge is not the next card of the queue; nothing was pushed or reported")
		}
		blame = at
		blameWhy = failed.why
		blameConflict = len(failed.paths) > 0
		if blameConflict {
			blameWhy = strings.Join(failed.paths, ", ")
		}
	}
	ref := "land/" + *stream
	head := ""
	if green > 0 {
		head = tips[green]
		if _, err := l.git(ctx, dir, "push", "--porcelain", "origin", head+":refs/heads/"+ref); err != nil {
			return landNodeStop(stderr, "the push of "+ref+" was not accepted ("+firstLine("", err)+"); nothing was reported")
		}
		got, err := l.git(ctx, dir, "ls-remote", "origin", "refs/heads/"+ref)
		if err != nil || !strings.HasPrefix(got, head) {
			return landNodePushed(stderr, ref+" was pushed and its tip could not be read back ("+firstLine(got, err)+"); the verdicts were not written")
		}
	}
	verdicts := landNodeVerdicts(cards, green, blame, blameWhy, blameConflict)
	step := store.Step{
		Verb:  "land-node",
		Load:  []string{sprint.Merge},
		Plan:  landNodePlan(*stream, head, ref, *as, verdicts),
		Named: true,
		Actor: *as,
	}
	a.serial.Lock()
	res, err := st.Run(ctx, step)
	a.serial.Unlock()
	if err != nil || len(res.Refused) > 0 {
		msg := stepWhy(res, err)
		if head != "" {
			return landNodePushed(stderr, ref+" is at "+head+" and the verdicts were not written ("+msg+")")
		}
		return landNodeStop(stderr, "the verdicts were not written ("+msg+"); nothing was pushed")
	}
	landNodePrint(stdout, c.json, *stream, ref, head, *as, verdicts)
	return 0
}

// landNodeBisect is GreenLen (tla/LandTwoLevel.tla): lo is the longest
// prefix that is green, and the next prefix is red. Prefix 0 is the base,
// which build's treeGateBase already passed; --check is not run on the base
// alone, as the lander's own batch check is not. The search is the halving
// in tla/LandBisect.tla (lo known green, hi known red, mid = lo+(hi-lo)/2).
// Each probe is the lander's runCheck and, when that is red, gateRerun.
// This is not a copy of bisectRed: that function is not on this base.
func (l *lander) landNodeBisect(ctx context.Context, dir, stream, base string, tips []string, cards []landCard) (int, string, error) {
	n := len(tips) - 1
	if n < 1 || n != len(cards) {
		return 0, "", fmt.Errorf("land-node bisect was given %d tips and %d cards", len(tips), len(cards))
	}
	lo, hi := 0, n
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if _, err := l.git(ctx, dir, "reset", "-q", "--hard", tips[mid]); err != nil {
			return 0, "", fmt.Errorf("the bisect could not check out prefix %d: %s", mid, firstLine("", err))
		}
		w, out := l.runCheck(ctx, dir)
		if w != "" {
			w = l.gateRerun(ctx, dir, stream, base, tips[mid], cards[:mid], w, out)
		}
		if w != "" {
			hi = mid
		} else {
			lo = mid
		}
	}
	if hi != lo+1 || hi > n {
		return 0, "", fmt.Errorf("the bisect ended at %d..%d, not a single card", lo, hi)
	}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", tips[hi]); err != nil {
		return 0, "", fmt.Errorf("the bisect could not check out the first red prefix: %s", firstLine("", err))
	}
	w, out := l.runCheck(ctx, dir)
	if w != "" {
		w = l.gateRerun(ctx, dir, stream, base, tips[hi], cards[:hi], w, out)
	}
	if w == "" {
		return 0, "", fmt.Errorf("prefix %d was green after the bisect called it the first red prefix", hi)
	}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", tips[lo]); err != nil {
		return 0, "", fmt.Errorf("the bisect could not return to the green prefix: %s", firstLine("", err))
	}
	return lo, landNodeLine(w), nil
}

// landNodeCards is the queue as land's stream() reads it: work order, the
// brief's REPO and BASE (swarm.ReadCardBase, swarm.CardPaths), and --base
// where the brief names none.
func landNodeCards(s *sprint.Snapshot, base string, queue []*sprint.Card) []landCard {
	var cards []landCard
	for _, c := range queue {
		lc := landCard{id: c.ID, base: base}
		if pr := s.Work.Placed(c.ID); pr != nil {
			lc.head, lc.attempt, lc.primary = pr.F("head"), pr.F("attempt"), pr
			raw := []byte(pr.F("brief"))
			cb := swarm.ReadCardBase(raw)
			lc.repo, lc.paths, lc.brief = cb.Repo, swarm.CardPaths(raw), pr.F("brief")
			if cb.Ref != "" {
				lc.base = cb.Ref
			}
		}
		cards = append(cards, lc)
	}
	return cards
}

// landNodeVerdict is one queued card's report. kind is the judgment
// NodeBlame opens (tla/LandTwoLevel.tla, JudgeKinds): card-red or
// sibling-clash. Empty kind is not a judgment.
type landNodeVerdict struct {
	ID       string `json:"id"`
	Verdict  string `json:"verdict"`
	Why      string `json:"why,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Conflict bool   `json:"-"`
}

// landNodeVerdicts is one verdict per queued card, in queue order. The green
// prefix is landed-in-node (NodeStage). The one card at blame is red, or
// conflict when the merge left a path (NodeBlame: sibling-clash if the
// prefix clashes, otherwise card-red). Cards after it stay ready.
func landNodeVerdicts(cards []landCard, green, blame int, why string, conflict bool) []landNodeVerdict {
	why = landNodeLine(why)
	out := make([]landNodeVerdict, len(cards))
	for i, c := range cards {
		v := landNodeVerdict{ID: c.id, Verdict: "ready"}
		switch {
		case i < green:
			v.Verdict = "landed-in-node"
		case i == blame:
			v.Why = why
			if conflict {
				v.Verdict = "conflict"
				v.Kind = "sibling-clash"
			} else {
				v.Verdict = "red"
				v.Kind = "card-red"
			}
		}
		out[i] = v
	}
	return out
}

// landNodePlan writes the verdicts in place on the merge cards and, when a
// prefix was pushed, the land/<stream> head on the stream's control card.
// It does not move a card and it does not call the merge step, so no landed
// record is written (tla/LandTwoLevel.tla, lands unchanged). The blamed card
// opens one judgment of the model's kind. Answering that type is not this
// verb: sprint.Decisions does not list card-red or sibling-clash.
func landNodePlan(stream, head, ref, who string, verdicts []landNodeVerdict) func(*sprint.Snapshot) sprint.Plan {
	return func(s *sprint.Snapshot) sprint.Plan {
		var p sprint.Plan
		var units []sprint.Unit
		for _, v := range verdicts {
			c := s.Merge.Placed(v.ID)
			if c == nil || c.Row != stream || c.Col != sprint.Queued {
				p.Refused = append(p.Refused, sprint.Refusal{Key: v.ID, Why: "not queued in stream " + stream})
				continue
			}
			set := map[string]string{"node_verdict": v.Verdict}
			if v.Why != "" {
				set["node_why"] = v.Why
			}
			u := sprint.Unit{
				Key: v.ID, Stream: stream, Moved: v.ID + " " + v.Verdict,
				Changes: []sprint.Change{{Table: sprint.Merge, Entry: ntable.BatchMemberEntry{
					ID: v.ID,
					Expect: &ntable.MemberExpect{
						Revision: strconv.FormatUint(c.Rev, 10),
						Place:    &ntable.PlaceExpect{Row: c.Row, Col: c.Col},
					},
					Set: set,
				}}},
			}
			if v.Kind != "" {
				u.Notes = []sprint.Note{{
					Kind: sprint.Judgment, Type: v.Kind, Stream: stream, Card: v.ID,
					Primaries: []string{v.ID}, Count: 1, What: v.Why, Who: who, At: s.Now,
					Decisions: []string{"refuse", "rework"},
				}}
			}
			units = append(units, u)
		}
		if head != "" {
			ctl := s.StreamCtl(stream)
			if ctl == nil {
				p.Refused = append(p.Refused, sprint.Refusal{Key: sprint.CtlID(stream), Why: "the stream has no control card"})
			} else {
				units = append(units, sprint.Unit{
					Key: ctl.ID, Stream: stream, Moved: ref + " " + head,
					Changes: []sprint.Change{{Table: sprint.Merge, Entry: ntable.BatchMemberEntry{
						ID: ctl.ID,
						Expect: &ntable.MemberExpect{
							Revision: strconv.FormatUint(ctl.Rev, 10),
							Place:    &ntable.PlaceExpect{Row: ctl.Row, Col: ctl.Col},
						},
						Set: map[string]string{"node_head": head, "node_ref": ref},
					}}},
				})
			}
		}
		p.Units = units
		return p
	}
}

func landNodePrint(w io.Writer, asJSON bool, stream, ref, head, by string, verdicts []landNodeVerdict) {
	if asJSON {
		b, _ := json.Marshal(map[string]any{
			"verb": "land-node", "status": "ok", "exit": 0,
			"stream": stream, "ref": ref, "head": head, "by": by, "cards": verdicts,
		})
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintf(w, "LAND-NODE stream=%s ref=%s head=%s by=%s\n", oneline.Field(stream), oneline.Field(ref), oneline.Field(head), oneline.Field(by))
	for _, v := range verdicts {
		if v.Why == "" {
			fmt.Fprintf(w, "LAND-NODE card=%s verdict=%s\n", oneline.Field(v.ID), oneline.Field(v.Verdict))
			continue
		}
		fmt.Fprintf(w, "LAND-NODE card=%s verdict=%s why=%s\n", oneline.Field(v.ID), oneline.Field(v.Verdict), oneline.Escape(v.Why))
	}
}

// landNodeLine is a verdict's why on one line, capped so a card field stays
// a line. Empty stays empty.
func landNodeLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return ""
	}
	return oneline.Cap(s, 300)
}

// landNodeStop is a refusal before a push, or with nothing reported. Exit 1,
// so an example that has not reached git is not a usage refusal.
func landNodeStop(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "%s land-node: %s\n", prog, oneline.Escape(what))
	return 1
}

// landNodePushed is a push that landed and a report that did not. Exit 2.
// The ref is left where the push put it; it is not deleted and not forced.
func landNodePushed(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "%s land-node: %s; see nova-sprint help land-node\n", prog, oneline.Escape(what))
	return 2
}

// landNodeInLandRoot says dir is the land root or inside it: the directory
// land keeps clones in, or the process's land root. land-node never clones
// there and never merges a clone that lives there.
func (a *app) landNodeInLandRoot(dir string) (string, bool) {
	var roots []string
	if a.landRoot != nil {
		if r, err := a.landRoot(); err == nil && r != "" {
			roots = append(roots, r)
		}
	}
	if r, err := defaultLandRoot(); err == nil && r != "" {
		roots = append(roots, r)
	}
	for _, root := range roots {
		if landNodeUnder(dir, root) {
			return root, true
		}
	}
	return "", false
}

func landNodeUnder(dir, root string) bool {
	dir, err1 := filepath.Abs(dir)
	root, err2 := filepath.Abs(root)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	return rel == "." || (rel != "" && !strings.HasPrefix(rel, ".."))
}
