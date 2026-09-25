package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// pipelineItem is one item of one stage of the pipeline, joined to the items of every
// other stage by its uid -- the one key the whole view turns on (nova-tools#2111:
// "generic global uids so every stage joins on one key"). From is where the item
// came from, what produced it; To is where it goes.
type pipelineItem struct {
	UID   string   `json:"uid"`
	Label string   `json:"label"`
	From  []string `json:"from"`
	To    []string `json:"to"`
}

// pipelineStage is one column of the linked view: the stage's own name and the items
// that populate it right now.
type pipelineStage struct {
	Stage string         `json:"stage"`
	Items []pipelineItem `json:"items"`
}

// pipelineFile is the record --file names: the stages of the pipeline in the record's
// own order, each with its population.
type pipelineFile struct {
	Stages []pipelineStage `json:"stages"`
}

// cmdVisualize prints the pipeline as linked views (nova-tools#2111): one block per
// stage, in the record's own pipeline order, each block showing its population right
// now -- a count and one line per item -- and every item line pointing at its
// neighbours, where it came from and where it goes. --select narrows every stage to
// the one item's ancestors and descendants, each marked as such, so the item's whole
// chain reads across the view: the one linked view where the funnel, the read table,
// the sprint table, the adoption table and the waiting table were five hand-built
// projections of this one data set.
//
// The record is data, never a program: a JSON file of stages, items and uid links.
// A link naming an item no stage holds is a refusal rather than a silent dangling
// label, because a label where a join belongs is the failure the issue names. There
// is no default record and no discovery: --file comes from the caller or the verb
// refuses to guess.
func cmdVisualize(args []string, stdout, stderr io.Writer) int {
	f := newFlags("visualize")
	file := f.fs.String("file", "", "")
	selectUID := f.fs.String("select", "", "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*file, "file", "the pipeline record, as JSON: every stage with its items and the uid links between them")
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already prints every item", *max))
	}
	if f.refused(stderr) {
		return 2
	}

	raw, err := os.ReadFile(*file)
	if err != nil {
		return refuse(stderr, " visualize", oneline.Err(err))
	}
	var p pipelineFile
	if err := json.Unmarshal(raw, &p); err != nil {
		return refuse(stderr, " visualize", oneline.Err(err))
	}
	idx, err := p.check()
	if err != nil {
		return refuse(stderr, " visualize", oneline.Err(err))
	}

	// marks is the selection's highlight: uid -> self, ancestor or descendant. It
	// is empty when nothing is selected, and then every item shows.
	marks := map[string]string{}
	if *selectUID != "" {
		if _, ok := idx.held[*selectUID]; !ok {
			return refuse(stderr, " visualize", fmt.Sprintf("the selection %s names no item the record holds; a selection is a uid of the pipeline, not a free label", oneline.Field(*selectUID)))
		}
		marks[*selectUID] = "self"
		for uid := range idx.reach(*selectUID, false) {
			marks[uid] = "ancestor"
		}
		for uid := range idx.reach(*selectUID, true) {
			if marks[uid] == "" {
				marks[uid] = "descendant"
			}
		}
	}

	for _, s := range p.Stages {
		rows := 0
		for _, it := range s.Items {
			if _, linked := marks[it.UID]; linked || *selectUID == "" {
				rows++
			}
		}
		fmt.Fprintf(stdout, "VIEW stage=%s rows=%d\n", oneline.Quote(s.Stage), rows)
		l := bounded.Capped(stdout, *max, "ITEM", "items", "widen --max or pass --max 0")
		for _, it := range s.Items {
			mark := marks[it.UID]
			if *selectUID != "" && mark == "" {
				continue
			}
			l.Line(itemLine(s.Stage, it, mark))
		}
		l.More()
		if err := l.Err(); err != nil {
			return refuse(stderr, " visualize", oneline.Err(err))
		}
	}
	return 0
}

// itemLine renders one item of one stage as the single line the view shows: the
// stage and the uid, the links that point at its neighbours, the selection's mark
// when one is set, and the item's own free text last -- the shape every listing in
// this repo keeps, keyed fields first and the readable tail after them.
func itemLine(stage string, it pipelineItem, mark string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ITEM stage=%s uid=%s", oneline.Quote(stage), oneline.Field(it.UID))
	if len(it.From) > 0 {
		fmt.Fprintf(&b, " from=%s", oneline.Field(strings.Join(it.From, ",")))
	}
	if len(it.To) > 0 {
		fmt.Fprintf(&b, " to=%s", oneline.Field(strings.Join(it.To, ",")))
	}
	if mark != "" {
		fmt.Fprintf(&b, " link=%s", oneline.Field(mark))
	}
	if it.Label != "" {
		fmt.Fprintf(&b, " label=%s", oneline.Escape(it.Label))
	}
	return b.String()
}

// pipelineIndex is the record's one join, built once by check: which stage holds
// each uid, and the links as edges both ways, so a --select walks ancestors and
// descendants from the same index instead of rescanning the record per direction.
type pipelineIndex struct {
	held map[string]string
	down map[string]map[string]bool // uid -> where it goes
	up   map[string]map[string]bool // uid -> where it came from
}

// check refuses the breaks in the one join before anything is shown: a stage with
// no name, an item with no uid, a uid two items share, and a link naming an item no
// stage holds. A view that rendered a dangling label would be the 2026-09-20
// failure the issue names, so the record joins or the verb refuses. On success it
// returns the index the view reads: an edge is an item's own To and every From that
// names it, so a record may spell an edge once and the walk still sees it both ways.
func (p pipelineFile) check() (pipelineIndex, error) {
	idx := pipelineIndex{held: map[string]string{}, down: map[string]map[string]bool{}, up: map[string]map[string]bool{}}
	for _, s := range p.Stages {
		if s.Stage == "" {
			return idx, fmt.Errorf("a stage of the record has no name; a column of the view is named or it is not a column")
		}
		for _, it := range s.Items {
			if it.UID == "" {
				return idx, fmt.Errorf("an item of stage %s has no uid; the uid is the one key every stage joins on", oneline.Quote(s.Stage))
			}
			if other, ok := idx.held[it.UID]; ok {
				return idx, fmt.Errorf("the uid %s is held by stage %s and stage %s alike; two items with one key is a broken join",
					oneline.Field(it.UID), oneline.Field(other), oneline.Field(s.Stage))
			}
			idx.held[it.UID] = s.Stage
		}
	}
	for _, s := range p.Stages {
		for _, it := range s.Items {
			for _, ref := range it.To {
				if _, ok := idx.held[ref]; !ok {
					return idx, fmt.Errorf("the item %s goes to %s, which no stage holds; a link is a join, and a label where a join belongs is the failure the issue names",
						oneline.Field(it.UID), oneline.Field(ref))
				}
				idx.link(it.UID, ref)
			}
			for _, ref := range it.From {
				if _, ok := idx.held[ref]; !ok {
					return idx, fmt.Errorf("the item %s came from %s, which no stage holds; a link is a join, and a label where a join belongs is the failure the issue names",
						oneline.Field(it.UID), oneline.Field(ref))
				}
				idx.link(ref, it.UID)
			}
		}
	}
	return idx, nil
}

// link records one edge, from -> to, in both directions of the index.
func (idx pipelineIndex) link(from, to string) {
	if idx.down[from] == nil {
		idx.down[from] = map[string]bool{}
	}
	idx.down[from][to] = true
	if idx.up[to] == nil {
		idx.up[to] = map[string]bool{}
	}
	idx.up[to][from] = true
}

// reach walks the links away from the uid, transitively, and returns the uids on the
// other side: where the item goes (forward) or where it came from. The result is a
// set: the view prints in the record's order, so nothing here needs an order of its own.
func (idx pipelineIndex) reach(uid string, forward bool) map[string]bool {
	edges := idx.up
	if forward {
		edges = idx.down
	}
	seen := map[string]bool{uid: true}
	queue := []string{uid}
	out := map[string]bool{}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for v := range edges[u] {
			if seen[v] {
				continue
			}
			seen[v] = true
			out[v] = true
			queue = append(queue, v)
		}
	}
	return out
}
