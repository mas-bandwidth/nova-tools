package main

import (
	"context"
	"flag"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// reconcilesView is each provider's latest cost reconciliation (sprint.LatestReconciles) off
// the fleet table's properties as the view's shapes read them: no card is read. where
// --json carries it as `reconciles` and the text frame as its COST RECONCILE line, so the
// gap the release's spend gate refuses on is seen first (docs/SPEC-SPRINT.md, "What a card
// cost", the reconciliation).
func reconcilesView(shapes []ntable.Table) []sprint.ReconcileRow {
	fleet := sprint.NewTable(sprint.Fleet)
	if i := slices.Index(sprint.ViewOrder, sprint.Fleet); i >= 0 && i < len(shapes) {
		fleet.SetProps(shapes[i].Props)
	}
	return sprint.LatestReconciles(fleet)
}

// THE STORE'S SIDE OF THE RELEASE'S SPEND GATE (docs/SPEC-RELEASE.md, "The store's spend
// matches the providers' own"): `where --json --spend-since <t> --spend-until <t>` carries
// `spend_window`, the store's records of each provider over [since, until)
// (sprint.RecordedSpendBetween: every take and read of every card whatever its end, at its
// charged figure, and its tokens). nova-update's release cut reads it through this verb, so
// the gate reaches the store by the seat's own login and never by a second opener.

// spendWindowView is the store's records of a window, per provider (lower case).
type spendWindowView struct {
	From      time.Time                     `json:"from"`
	To        time.Time                     `json:"to"`
	Providers map[string]sprint.WindowSpend `json:"providers"`
}

// spendWindowFlags are where's --spend-since and --spend-until.
type spendWindowFlags struct{ since, until string }

func (f *spendWindowFlags) add(fs *flag.FlagSet) {
	fs.StringVar(&f.since, "spend-since", "", "with --json and --spend-until: also the store's recorded spend of each provider from this time (RFC 3339 or 2006-01-02, UTC), as `spend_window`: what the release's spend gate reads")
	fs.StringVar(&f.until, "spend-until", "", "with --json and --spend-since: the spend window ends before this time (RFC 3339 or 2006-01-02, UTC)")
}

func (f spendWindowFlags) set() bool { return f.since != "" || f.until != "" }

// check is why the flags are refused; "" when they are not.
func (f spendWindowFlags) check(json bool) string {
	if !f.set() {
		return ""
	}
	if !json {
		return "--spend-since and --spend-until are a field of the JSON view: give --json with them"
	}
	if f.since == "" || f.until == "" {
		return "--spend-since and --spend-until are given together"
	}
	from, err1 := spendTime(f.since)
	to, err2 := spendTime(f.until)
	switch {
	case err1 != nil:
		return "--spend-since " + f.since + ": " + err1.Error()
	case err2 != nil:
		return "--spend-until " + f.until + ": " + err2.Error()
	case to.Before(from):
		return "the spend window ends before it starts"
	}
	return ""
}

// spendTime reads a day (its UTC midnight) or an RFC 3339 time.
func spendTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// spendWindowOf is the store's records of the window: one read of the work table and the
// routes.
func spendWindowOf(ctx context.Context, st *store.Store, f spendWindowFlags) (*spendWindowView, error) {
	from, _ := spendTime(f.since) // checked at the flags
	to, _ := spendTime(f.until)
	s, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return nil, err
	}
	routes, _, err := st.Routes(ctx)
	if err != nil {
		return nil, err
	}
	s.Routes = routes
	return &spendWindowView{From: from.UTC(), To: to.UTC(), Providers: sprint.RecordedSpendBetween(s, from, to)}, nil
}
