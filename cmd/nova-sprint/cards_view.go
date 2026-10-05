package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// cardsView is view cards' document, schema 1.
type cardsView struct {
	View   string               `json:"view"`
	Schema int                  `json:"schema"`
	Epoch  uint64               `json:"epoch"`
	At     time.Time            `json:"at"`
	Total  int                  `json:"total"`
	By     string               `json:"by,omitempty"`
	Counts map[string]int       `json:"counts,omitempty"`
	Cards  []sprint.CardSummary `json:"cards,omitempty"`
}

func init() {
	verbClasses["view cards"] = classRead
}

// cmdViewCards implements `nova-sprint view cards`:
// `nova-sprint view cards [--col <c>] [--stream <s>] [--holder <member>] [--by tier|stream|col|holder] [--json]`
// that counts (with --by) or lists the primaries, the tier read the way the dealer reads it.
func (a *app) cmdViewCards(args []string, stdout, stderr io.Writer) int {
	const name = "view cards"
	fs, c := a.verbSetup(name)
	col := fs.String("col", "", "filter primaries by column (waiting, ready, working, review, merging, landed)")
	stream := fs.String("stream", "", "filter primaries by stream")
	holder := fs.String("holder", "", "filter primaries by holder (fleet member or friend)")
	by := fs.String("by", "", "count primaries aggregated by tier, stream, col or holder")

	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}

	if *col != "" {
		if !slices.Contains(sprint.States, sprint.State(*col)) {
			return refuse(stderr, name, "--col wants waiting, ready, working, review, merging or landed, found "+oneline.Escape(*col))
		}
	}
	if *by != "" {
		switch *by {
		case "tier", "stream", "col", "holder":
		default:
			return refuse(stderr, name, "--by wants tier, stream, col or holder, found "+oneline.Escape(*by))
		}
	}
	if *holder != "" {
		trimmed := strings.TrimPrefix(*holder, "friend.")
		if !sprint.ValidID(*holder) && !sprint.ValidID(trimmed) {
			return refuse(stderr, name, "--holder <member> names one fleet member or friend (letters, digits, _ and -)")
		}
	}

	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	ctx := context.Background()
	st, err = st.Pinned(ctx)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Fleet}, nil)
	if err != nil {
		return a.readFailed(name, err, stderr)
	}

	now := a.now()
	filter := sprint.CardsViewFilter{
		Col:    *col,
		Stream: *stream,
		Holder: *holder,
	}
	res := sprint.CollectCardsView(s, filter, *by)

	v := cardsView{
		View:   "cards",
		Schema: viewSchema,
		Epoch:  st.PinnedEpoch(),
		At:     now.UTC().Truncate(time.Second),
		Total:  res.Total,
		By:     *by,
	}
	if *by != "" {
		v.Counts = res.Counts
	} else {
		v.Cards = res.Cards
	}

	if c.json {
		viewJSON(stdout, v)
		return 0
	}

	if *by != "" {
		fmt.Fprintf(stdout, "VIEW cards by=%s total=%d\n", *by, v.Total)
		keys := slices.Sorted(maps.Keys(v.Counts))
		for _, k := range keys {
			fmt.Fprintf(stdout, "%s %d\n", k, v.Counts[k])
		}
		return 0
	}

	fmt.Fprintf(stdout, "VIEW cards total=%d\n", v.Total)
	for _, card := range v.Cards {
		h := cmp.Or(card.Holder, "-")
		fmt.Fprintf(stdout, "CARD %s stream=%s col=%s tier=%s holder=%s\n", card.ID, card.Stream, card.Col, card.Tier, h)
	}
	return 0
}
