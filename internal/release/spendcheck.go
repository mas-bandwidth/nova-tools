package release

// THE SPEND, IN FRONT OF THE TAG.
//
// The owner, 2026-10-05: "We should not make a release without verifying that we capture
// actual spend, not < 1/2 of it." On 2026-10-04 OpenRouter's own account showed about $2,250
// and the sprint's cost panel $836: runs with no result, reads and retries went unpriced, and
// nothing in the release asked. So `cut` asks, over the release's window, of every paid
// provider: what the store recorded for it, and what the provider itself reports it was
// charged. A gap past SpendGapOver of the provider's figure refuses the release, naming the
// provider and both figures. The subscription friends carry no dollars; their tokens, as
// recorded, are held to the harness's own usage receipts the same way.
//
// A readout that cannot be read is a refusal naming the provider, never a pass: a gate that
// goes green when its witness is silent is the gate that let $1,400 go unseen.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// SpendGapOver is the share of the provider's own figure a gap must pass to refuse.
const SpendGapOver = 0.05

// Units a figure is in.
const (
	SpendUSD    = "usd"
	SpendTokens = "tokens"
)

// SpendWaiveFlag is the way past the gate, spelled once; it shares --reason with the others.
const SpendWaiveFlag = "--no-spend-gate"

// SpendRemedy is what the refusal tells somebody to do about it.
const SpendRemedy = "price every paid call the store missed (nova-sprint cost reconcile shows the day-by-day gap) and cut again, or " + SpendWaiveFlag + " " + DogfoodReasonFlag + " <why>"

// SpendNote is the gate said where a person will meet it: on `release help`.
var SpendNote = "cut runs the spend gate after the tags are read: over the release's window (--spend-since, else since the previous release tag) " +
	"each paid provider's OWN reported spend is read and held to what the sprint's store recorded for it, and the release is refused when any gap passes " +
	"five percent, naming the provider, both figures and the gap; the subscription friends' recorded tokens are held to their harness receipts the same way. " +
	"A provider whose readout cannot be read refuses and is named: silence is never a pass. A run with no readouts wired is NOT a run that passed: it prints `spend-gate=skipped`. " +
	"The way past is to fix the recording, or " + SpendWaiveFlag + " " + DogfoodReasonFlag + " <why>, printed on the line and written into the CHANGELOG section."

// SpendWaiverPrefix is how the CHANGELOG section names a waived gate.
const SpendWaiverPrefix = "Spend gate waived: "

// SpendWindow is the span a release's spend is read over: From inclusive, To exclusive.
type SpendWindow struct {
	From, To time.Time
}

func (w SpendWindow) String() string {
	return w.From.UTC().Format(time.RFC3339) + ".." + w.To.UTC().Format(time.RFC3339)
}

// ProviderUsage is one provider's OWN readout of what a window cost (or, for a subscription
// friend, how many tokens its harness receipted). One per provider; production ones read the
// provider's usage endpoint with the key from nova-secrets, a test's is a fake.
type ProviderUsage interface {
	// Name is the provider, as the store names it.
	Name() string
	// Unit is SpendUSD for a paid provider, SpendTokens for a subscription friend.
	Unit() string
	// Reported is the provider's own figure over the window.
	Reported(ctx context.Context, w SpendWindow) (float64, error)
}

// SpendStore is the sprint's record of spend: what it recorded for a provider over a window,
// in the unit asked (dollars or tokens).
type SpendStore interface {
	Recorded(ctx context.Context, provider, unit string, w SpendWindow) (float64, error)
}

// SpendCheck is the seam: the readouts, the store, and how a window is found when the verb
// was not given one. A nil Deps.Spend is a run with nothing wired.
type SpendCheck struct {
	Providers []ProviderUsage
	Store     SpendStore
	// Window finds the window from the previous release tag ("" for none) and now; nil means
	// --spend-since is required.
	Window func(ctx context.Context, previous string, now time.Time) (SpendWindow, error)
}

// SpendFigure is one provider's two figures over the window and what the gate made of them.
type SpendFigure struct {
	Provider string
	Unit     string
	Recorded float64
	Reported float64
	// Err is why a figure could not be read; set, the provider refuses.
	Err error
}

// Gap is the recorded figure less the provider's.
func (f SpendFigure) Gap() float64 { return f.Recorded - f.Reported }

// Share is the gap's size over the provider's figure; 1 when the provider reports nothing and
// the store recorded something, 0 when both are nothing.
func (f SpendFigure) Share() float64 {
	switch {
	case f.Reported > 0:
		return math.Abs(f.Gap()) / f.Reported
	case f.Recorded > 0:
		return 1
	}
	return 0
}

// Refused is whether the gate says no to this provider.
func (f SpendFigure) Refused() bool { return f.Err != nil || f.Share() > SpendGapOver }

func (f SpendFigure) amount(v float64) string {
	if f.Unit == SpendTokens {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// Line is the figure as the release prints it: provider, both figures, the gap.
func (f SpendFigure) Line() string {
	if f.Err != nil {
		return fmt.Sprintf("provider=%s unit=%s unreadable=%s", field(f.Provider), f.Unit, field(f.Err.Error()))
	}
	return fmt.Sprintf("provider=%s unit=%s store=%s provider-reported=%s gap=%s share=%.1f%%",
		field(f.Provider), f.Unit, field(f.amount(f.Recorded)), field(f.amount(f.Reported)), field(f.amount(math.Abs(f.Gap()))), f.Share()*100)
}

// ReadSpend reads every provider's two figures over the window. Every provider is read, so a
// refusal names all of them and not the first.
func ReadSpend(ctx context.Context, c SpendCheck, w SpendWindow) []SpendFigure {
	var out []SpendFigure
	for _, p := range c.Providers {
		f := SpendFigure{Provider: p.Name(), Unit: p.Unit()}
		var err error
		if f.Reported, err = p.Reported(ctx, w); err != nil {
			f.Err = fmt.Errorf("the provider's own readout: %w", err)
		} else if c.Store == nil {
			f.Err = errors.New("no store to read the recorded spend from")
		} else if f.Recorded, err = c.Store.Recorded(ctx, f.Provider, f.Unit, w); err != nil {
			f.Err = fmt.Errorf("the store's record: %w", err)
		}
		out = append(out, f)
	}
	return out
}

// errSpend says the gate said no and the refusal has already been printed.
var errSpend = errors.New("spend-gate")

// spendCheck is the gate as `cut` runs it: the token the receipt line carries (ok, waived or
// skipped) and an error, errSpend for the one whose lines are already written.
func spendCheck(o options, deps Deps, previous string, out, errs io.Writer) (string, error) {
	if o.noSpend {
		if strings.TrimSpace(o.reason) == "" {
			return "", refuse("say why: "+SpendWaiveFlag+" "+DogfoodReasonFlag+" <why>",
				"%s skips the check that the store captured the actual spend and no reason was given", SpendWaiveFlag)
		}
		fmt.Fprintf(out, "RELEASE CUT SPEND WAIVED reason=%s\n", field(o.reason))
		return "waived", nil
	}
	c := deps.Spend
	if c == nil || len(c.Providers) == 0 {
		fmt.Fprintf(errs, "RELEASE CUT NOTE spend-gate=skipped remedy=%q\n",
			"wire the provider readouts so the recorded spend is checked against each provider's own before the release")
		return "skipped", nil
	}
	now := deps.Now()
	w := SpendWindow{To: now}
	switch {
	case o.spendSince != "":
		from, err := parseSpendTime(o.spendSince)
		if err != nil {
			return "", refuse("pass --spend-since as 2006-01-02 or RFC 3339", "--spend-since %q is no time", o.spendSince)
		}
		w.From = from
	case c.Window != nil:
		var err error
		if w, err = c.Window(context.Background(), previous, now); err != nil {
			return "", refuse("pass --spend-since", "the release's window is unknown: %s", err)
		}
	default:
		return "", refuse("pass --spend-since <time>", "the release's spend window is unknown: no --spend-since and nothing to find the previous release's time")
	}
	figs := ReadSpend(context.Background(), *c, w)
	bad := 0
	for _, f := range figs {
		if f.Refused() {
			bad++
			fmt.Fprintf(errs, "SPEND GATE REFUSED %s window=%s\n", f.Line(), field(w.String()))
		} else {
			fmt.Fprintf(errs, "RELEASE CUT NOTE spend-gate %s window=%s\n", f.Line(), field(w.String()))
		}
	}
	if bad > 0 {
		fmt.Fprintf(errs, "RELEASE CUT REFUSED reason=spend-gate providers=%d refused=%d over=%.0f%% remedy=%q\n", len(figs), bad, SpendGapOver*100, SpendRemedy)
		return "", errSpend
	}
	return "ok", nil
}

func parseSpendTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse(time.DateOnly, s)
}

// spendWaiver is the waiver as the CHANGELOG carries it, "" when the gate was not waived.
func spendWaiver(state, reason string) string {
	if state != "waived" {
		return ""
	}
	return SpendWaiverPrefix + strings.TrimSpace(reason) + "\n\n"
}
