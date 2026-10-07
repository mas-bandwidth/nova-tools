package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/nova-tools/internal/sprint"
	"github.com/nova-tools/internal/sprint/store"
)

// view cards (docs/SPEC-SPRINT.md section 11, "view cards"; the owner, 2026-10-05: "We need to
// get away from these one shot shell scripts"): the work table's primaries listed, or counted
// with --by, by column, tier (read as the dealer reads it), stream and holder. A read: it
// writes nothing, and the server serves it as GET /api/view/cards.

func init() { verbClasses["view cards"] = classRead }

// cardsView is view cards' document, schema 1: Cards when listing, Counts when --by is given.
type cardsView struct {
	View   string           `json:"view"`
	Schema int              `json:"schema"`
	At     time.Time        `json:"at"`
	Epoch  uint64           `json:"epoch"`
	Col    string           `json:"col,omitempty"`
	Stream string           `json:"stream,omitempty"`
	Holder string           `json:"holder,omitempty"`
	By     string           `json:"by,omitempty"`
	Total  int              `json:"total"`
	Counts map[string]int   `json:"counts,omitempty"`
	Cards  []sprint.CardRow `json:"cards,omitempty"`
}

func (a *app) cmdViewCards(args []string, stdout, stderr io.Writer) int {
	const name = "view cards"
	fs, c := a.verbSetup(name)
	col := fs.String("col", "", "only the cards in this column (waiting, ready, working, review, merging, landed)")
	stream := fs.String("stream", "", "only the cards of this stream")
	holder := fs.String("holder", "", "only the cards this member or friend is working")
	by := fs.String("by", "", "count the cards by tier, stream, col or holder instead of listing them")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if *col != "" && !slices.Contains(sprint.States, *col) {
		return refuse(stderr, name, fmt.Sprintf("--col is one of %s", strings.Join(sprint.States, ", ")))
	}
	if _, ok := sprint.CardsBy(nil, *by); *by != "" && !ok {
		return refuse(stderr, name, "--by is one of tier, stream, col, holder")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	v, err := a.cardsView(context.Background(), st, sprint.CardsFilter{Col: *col, Stream: *stream, Holder: *holder}, *by)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	if c.json {
		viewJSON(stdout, v)
		return 0
	}
	fmt.Fprint(stdout, cardsText(v))
	return 0
}

func (a *app) cardsView(ctx context.Context, st *store.Store, f sprint.CardsFilter, by string) (cardsView, error) {
	v := cardsView{View: "cards", Schema: viewSchema, At: a.now().UTC().Truncate(time.Second), Col: f.Col, Stream: f.Stream, Holder: f.Holder, By: by}
	st, err := st.Pinned(ctx)
	if err != nil {
		return v, err
	}
	v.Epoch = st.PinnedEpoch()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Fleet}, nil)
	if err != nil {
		return v, err
	}
	rows := sprint.CardRows(s, f)
	v.Total = len(rows)
	if by == "" {
		v.Cards = rows
		return v, nil
	}
	v.Counts, _ = sprint.CardsBy(rows, by)
	return v, nil
}

func cardsText(v cardsView) string {
	var b strings.Builder
	if v.By == "" {
		for _, r := range v.Cards {
			fmt.Fprintf(&b, "%s col=%s tier=%s stream=%s holder=%s\n", r.ID, r.Col, r.Tier, r.Stream, cmpDash(r.Holder))
		}
		fmt.Fprintf(&b, "total=%d\n", v.Total)
		return b.String()
	}
	for _, k := range sprint.SortedKeys(v.Counts) {
		fmt.Fprintf(&b, "%s=%d\n", k, v.Counts[k])
	}
	fmt.Fprintf(&b, "total=%d\n", v.Total)
	return b.String()
}

func cmpDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
