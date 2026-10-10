package release

// THE SPEND THE STORE RECORDED, PROVEN AGAINST THE PROVIDERS' OWN, IN FRONT OF THE TAG.
//
// The owner, 2026-10-05: "We should not make a release without verifying that we capture
// actual spend, not < 1/2 of it." and "We must be reliable, and accurate." On 2026-10-04
// openrouter's own account showed about $2,250 spent while the sprint's cost panel showed
// $836: runs with no result, reads and retries were not priced. The cost records are fixed
// by pricing every paid call whatever its outcome (internal/sprint, cost.go); this gate is
// how a release proves the fix holds.
//
// Over the release's window (since the previous tag, or --spend-since), each paid provider's
// own count of its spend is set beside the spend the sprint's store recorded of that
// provider over the same window, and each subscription friend's harness receipts (tokens,
// no dollars) beside the tokens the store recorded of that friend. A gap over SpendGapOver
// of the provider's own figure refuses the cut, naming the provider, both figures and the
// gap. A provider or a receipt that cannot be read is a refusal naming it, never a pass:
// a check that passes when it cannot look is no check.
//
// The way past is the other gates': --no-spend-gate --reason <why>, said on the line and
// written into the CHANGELOG section with every row the gate could not pass.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// SpendGapOver is the share of the provider's own figure a gap must pass to refuse.
const SpendGapOver = 0.05

// SpendWindow is the release's window: [From, To), From at the start of its UTC day (the
// providers count by the UTC day).
type SpendWindow struct {
	From, To time.Time
}

// String is the window as a line field says it.
func (w SpendWindow) String() string {
	return w.From.UTC().Format(time.RFC3339) + ".." + w.To.UTC().Format(time.RFC3339)
}

// SpendWindowFrom is the window from since to now, since taken back to the start of its UTC day.
func SpendWindowFrom(since, now time.Time) SpendWindow {
	return SpendWindow{From: since.UTC().Truncate(24 * time.Hour), To: now.UTC()}
}

// ProviderSpend is one paid provider's own count of what was spent with it over a window,
// in dollars. An error is a readout that could not be read, and refuses.
type ProviderSpend interface {
	Provider() string
	Spend(ctx context.Context, w SpendWindow) (float64, error)
}

// TokenReceipts is the subscription friends' harnesses' own counts of the tokens each used
// over a window, by friend. An error is receipts that could not be read, and refuses.
type TokenReceipts interface {
	Tokens(ctx context.Context, w SpendWindow) (map[string]int64, error)
}

// RecordedSpend is what the sprint's store recorded over a window: the paid providers it
// knows of, its dollars of each, and its tokens of each subscription friend.
type RecordedSpend interface {
	Providers(ctx context.Context, w SpendWindow) ([]string, error)
	Spend(ctx context.Context, provider string, w SpendWindow) (float64, error)
	Tokens(ctx context.Context, w SpendWindow) (map[string]int64, error)
}

// SpendSources are the gate's three readouts. A nil Receipts is no receipts readout: a
// subscription friend the store recorded is then a refusal naming it.
type SpendSources struct {
	Store     RecordedSpend
	Providers []ProviderSpend
	Receipts  TokenReceipts
}

// SpendRow is one comparison: a paid provider's dollars (Kind "provider") or a
// subscription friend's tokens (Kind "friend"). Unread says why the provider's own figure
// could not be read, "" when it was.
type SpendRow struct {
	Kind   string
	Name   string
	Store  float64
	Own    float64
	Gap    float64 // Own less Store
	Share  float64 // |Gap| over Own; 1 when Own is 0 and Store is not
	Unread string
}

// Refused says the row refuses the cut: unread, or its gap past SpendGapOver.
func (r SpendRow) Refused() bool { return r.Unread != "" || r.Share > SpendGapOver }

// Line is the row as the gate prints it.
func (r SpendRow) Line() string {
	verdict := "ok"
	if r.Refused() {
		verdict = "refuse"
	}
	if r.Unread != "" {
		return fmt.Sprintf("SPEND %s=%s unread verdict=%s: %s", r.Kind, oneline.Field(r.Name), verdict, oneline.Escape(r.Unread))
	}
	if r.Kind == "friend" {
		return fmt.Sprintf("SPEND friend=%s store_tokens=%d receipt_tokens=%d gap=%d share=%.1f%% verdict=%s",
			oneline.Field(r.Name), int64(r.Store), int64(r.Own), int64(math.Abs(r.Gap)), r.Share*100, verdict)
	}
	return fmt.Sprintf("SPEND provider=%s store=%s provider_usd=%s gap=%s share=%.1f%% verdict=%s",
		oneline.Field(r.Name), usd(r.Store), usd(r.Own), usd(math.Abs(r.Gap)), r.Share*100, verdict)
}

// usd is a dollar figure to the cent.
func usd(v float64) string { return fmt.Sprintf("$%.2f", v) }

// SpendVerdict is what the gate found over its window.
type SpendVerdict struct {
	Window SpendWindow
	Rows   []SpendRow
}

// Refused is the rows that refuse the cut.
func (v SpendVerdict) Refused() []SpendRow {
	var out []SpendRow
	for _, r := range v.Rows {
		if r.Refused() {
			out = append(out, r)
		}
	}
	return out
}

// share is |own - store| over own; 1 when own is 0 and store is not.
func share(store, own float64) float64 {
	switch {
	case own > 0:
		return math.Abs(own-store) / own
	case store > 0:
		return 1
	}
	return 0
}

// CheckSpend sets each paid provider's own spend over the window beside the store's, and
// each subscription friend's receipts beside the store's tokens of them. The providers
// checked are every one the store knows of and every one a readout is given for; one with
// no readout is unread. An error is the store unread, which refuses whole.
func CheckSpend(ctx context.Context, src SpendSources, w SpendWindow) (SpendVerdict, error) {
	v := SpendVerdict{Window: w}
	if src.Store == nil {
		return v, errors.New("no store to read the recorded spend from")
	}
	known, err := src.Store.Providers(ctx, w)
	if err != nil {
		return v, fmt.Errorf("cannot read the store's providers: %w", err)
	}
	readers := map[string]ProviderSpend{}
	names := map[string]bool{}
	for _, p := range known {
		names[strings.ToLower(p)] = true
	}
	for _, r := range src.Providers {
		readers[strings.ToLower(r.Provider())] = r
		names[strings.ToLower(r.Provider())] = true
	}
	for _, p := range slices.Sorted(maps.Keys(names)) {
		row := SpendRow{Kind: "provider", Name: p}
		if row.Store, err = src.Store.Spend(ctx, p, w); err != nil {
			return v, fmt.Errorf("cannot read the store's spend of %s: %w", p, err)
		}
		r, ok := readers[p]
		if !ok {
			row.Unread = "no readout of " + p + "'s own spend is known"
		} else if own, err := r.Spend(ctx, w); err != nil {
			row.Unread = err.Error()
		} else {
			row.Own, row.Gap, row.Share = own, own-row.Store, share(row.Store, own)
		}
		v.Rows = append(v.Rows, row)
	}
	recorded, err := src.Store.Tokens(ctx, w)
	if err != nil {
		return v, fmt.Errorf("cannot read the store's tokens: %w", err)
	}
	var receipts map[string]int64
	unread := ""
	switch {
	case src.Receipts == nil:
		unread = "no harness receipts were given (--spend-receipts <file>)"
	default:
		if receipts, err = src.Receipts.Tokens(ctx, w); err != nil {
			unread = err.Error()
		}
	}
	friends := maps.Clone(recorded)
	if friends == nil {
		friends = map[string]int64{}
	}
	for f := range receipts {
		friends[f] += 0
	}
	for _, f := range slices.Sorted(maps.Keys(friends)) {
		row := SpendRow{Kind: "friend", Name: f, Store: float64(recorded[f]), Unread: unread}
		if unread == "" {
			own := float64(receipts[f])
			row.Own, row.Gap, row.Share = own, own-row.Store, share(row.Store, own)
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

// SpendWaiveFlag is the way past the gate, spelled once.
const SpendWaiveFlag = "--no-spend-gate"

// SpendRemedy is what the refusal tells somebody to do.
const SpendRemedy = "find the spend the store did not record (nova-sprint cost reconcile; card <id> shows the records), give every provider a readout and the friends their receipts, or " +
	SpendWaiveFlag + " " + DogfoodReasonFlag + " <why>"

// SpendWaiverPrefix is how the CHANGELOG section names a waived spend gate.
const SpendWaiverPrefix = "Spend gate waived: "

// SpendNote is the gate said where a person meets it.
var SpendNote = "cut runs the spend gate once the previous tag is known: over the release's window (since the previous tag's commit, or --spend-since <RFC3339>, from the start of that UTC day to now), " +
	"each paid provider's own count of its spend is set beside what the sprint's store (--spend-store <addr>) recorded of it, and each subscription friend's harness receipts (--spend-receipts <file>) beside the tokens the store recorded of them. " +
	"A gap over 5% of the provider's own figure refuses, printing `SPEND provider=<p> store=<$> provider_usd=<$> gap=<$> share=<%>`; a provider whose readout cannot be read, or a friend with no receipt, refuses naming it, never passes. " +
	"The providers' keys come from the environment as nova-secrets exec delivers them and are never printed. " +
	"The way past is " + SpendWaiveFlag + " " + DogfoodReasonFlag + " <why>, and every row that did not pass is then written into the CHANGELOG section."

// errSpend says the gate refused and the refusal is already printed.
var errSpend = errors.New("spend-gate")

// spendResult is what the gate left for the cut's line and section.
type spendResult struct {
	State   string // ok, waived
	Section string
}

// spendCheck is the gate as cut runs it: the window from --spend-since or the previous
// tag's commit, the sources from deps.Spend or the flags, every row printed to errs, and a
// refusal (errSpend, already printed) unless every row passes or the gate is waived.
func spendCheck(ctx context.Context, o options, deps Deps, forge Forge, previous string, errs io.Writer) (spendResult, error) {
	var since time.Time
	switch {
	case o.spendSince != "":
		t, err := time.Parse(time.RFC3339, o.spendSince)
		if err != nil {
			return spendResult{}, refuse("give --spend-since as RFC3339, such as 2026-10-01T00:00:00Z", "--spend-since %q is no time: %s", o.spendSince, err)
		}
		since = t
	case previous != "":
		tt, ok := forge.(TagTimer)
		if !ok {
			return spendResult{}, refuse("give --spend-since <RFC3339>", "the forge cannot say when %s was made, so the spend window has no start", previous)
		}
		t, err := tt.TagTime(ctx, o.repo, previous)
		if err != nil {
			return spendResult{}, refuse("ask again when the forge answers, or give --spend-since <RFC3339>", "cannot read when %s was made: %s", previous, err)
		}
		since = t
	default:
		return spendResult{}, refuse("give --spend-since <RFC3339>", "there is no previous tag, so the spend window has no start")
	}
	w := SpendWindowFrom(since, deps.Now())
	src := deps.Spend
	if src == nil {
		s, err := ProductionSpend(ctx, o, w)
		if err != nil {
			if o.noSpend {
				return waiveUnread(o, w, err, errs)
			}
			fmt.Fprintf(errs, "CUT REFUSED reason=spend-gate window=%s unread: %s (%s)\n", w, oneline.Err(err), SpendRemedy)
			return spendResult{}, errSpend
		}
		src = &s
	}
	progress(errs, "setting the store's spend over %s beside each provider's own", w)
	v, err := CheckSpend(ctx, *src, w)
	if err != nil {
		if o.noSpend {
			return waiveUnread(o, w, err, errs)
		}
		fmt.Fprintf(errs, "CUT REFUSED reason=spend-gate window=%s unread: %s (%s)\n", w, oneline.Err(err), SpendRemedy)
		return spendResult{}, errSpend
	}
	for _, r := range v.Rows {
		fmt.Fprintln(errs, r.Line())
	}
	bad := v.Refused()
	if len(bad) == 0 {
		return spendResult{State: "ok"}, nil
	}
	if o.noSpend {
		if strings.TrimSpace(o.reason) == "" {
			return spendResult{}, refuse("say why with "+DogfoodReasonFlag+" <why>", "%s needs %s", SpendWaiveFlag, DogfoodReasonFlag)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s%s (window %s)\n", SpendWaiverPrefix, o.reason, w)
		for _, r := range bad {
			fmt.Fprintf(&b, "- %s\n", r.Line())
		}
		fmt.Fprintf(errs, "SPEND GATE WAIVED refused=%d reason=%s\n", len(bad), oneline.Field(o.reason))
		return spendResult{State: "waived", Section: b.String() + "\n"}, nil
	}
	var providers, friends []string
	for _, r := range bad {
		if r.Kind == "friend" {
			friends = append(friends, r.Name)
		} else {
			providers = append(providers, r.Name)
		}
	}
	fmt.Fprintf(errs, "CUT REFUSED reason=spend-gate window=%s refused=%d providers=%s friends=%s (%s)\n", w, len(bad),
		field(strings.Join(providers, ",")), field(strings.Join(friends, ",")), SpendRemedy)
	return spendResult{}, errSpend
}

// waiveUnread is the waiver of a gate whose store could not be read.
func waiveUnread(o options, w SpendWindow, err error, errs io.Writer) (spendResult, error) {
	if strings.TrimSpace(o.reason) == "" {
		return spendResult{}, refuse("say why with "+DogfoodReasonFlag+" <why>", "%s needs %s", SpendWaiveFlag, DogfoodReasonFlag)
	}
	fmt.Fprintf(errs, "SPEND GATE WAIVED unread reason=%s: %s\n", oneline.Field(o.reason), oneline.Err(err))
	return spendResult{State: "waived", Section: fmt.Sprintf("%s%s (window %s)\n- unread: %s\n\n", SpendWaiverPrefix, o.reason, w, oneline.Err(err))}, nil
}

// TagTimer is a forge that can say when a tag's commit was made: the start of the spend
// window. GH is one.
type TagTimer interface {
	TagTime(ctx context.Context, repo, tag string) (time.Time, error)
}
