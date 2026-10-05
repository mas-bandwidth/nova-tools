package release

// THE SPEND GATE, IN FRONT OF THE TAG (docs/SPEC-RELEASE.md, rule 14).
//
// The owner, 2026-10-05: "We should not make a release without verifying that we capture
// actual spend, not < 1/2 of it." and "We must be reliable, and accurate." On 2026-10-04
// openrouter's own account showed about $2,250 spent while the sprint's cost panel showed
// $836: runs with no result, reads and retries were not priced. The sprint now prices every
// paid call whatever its end and reconciles a day against the provider's count of it
// (internal/sprint, cost_reconcile.go); this gate makes a release prove it.
//
// For the release's window -- since the previous release's tag, or --spend-since and
// --spend-until -- `cut` reads each provider's own count of the window and the sprint store's
// records of the same provider and window, and refuses the tag when any gap passes five
// percent of the provider's figure (sprint.CostGapOver): a paid provider in dollars, a
// subscription friend (no dollars) in tokens against its harness's own usage receipts. Every
// provider is printed with both figures and the gap. A provider whose readout cannot be
// read, a provider the store recorded that has no readout at all, and a store that cannot be
// read are each a refusal naming it, never a pass: a gate that passes what it could not see
// is the gate being lucky.
//
// The window is whole UTC days, [the first day's midnight, the last day's midnight): the
// providers count completed days, and like is compared with like.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// SpendReadout is a provider's own count of a window: dollars for a paid provider, tokens
// for a subscription one.
type SpendReadout struct {
	USD    float64
	Tokens int64
}

// SpendProvider is one provider's own readout of its usage, behind which sits its endpoint
// (net/http, its key from nova-secrets exec in the environment) or, in a test, a fake.
type SpendProvider interface {
	// Name is the provider as the sprint's routes name it (openrouter, opencode, ...).
	Name() string
	// Subscription says the provider bills a flat plan: its readout is the harness's token
	// receipts and the gate compares tokens, never dollars.
	Subscription() bool
	// Spend is the provider's count of [from, to); an error is a readout that could not be
	// read, and the gate refuses on it.
	Spend(ctx context.Context, from, to time.Time) (SpendReadout, error)
}

// SpendStore is the sprint store's records of every provider over [from, to)
// (sprint.RecordedSpendBetween); an error is a store that could not be read.
type SpendStore interface {
	RecordedSpend(ctx context.Context, from, to time.Time) (map[string]sprint.WindowSpend, error)
}

// SnapshotSpend is a SpendStore over a load of the sprint's tables (the work table and the
// routes): the store's own records, summed by sprint.RecordedSpendBetween.
type SnapshotSpend func(ctx context.Context) (*sprint.Snapshot, error)

// RecordedSpend is the snapshot's records of the window.
func (f SnapshotSpend) RecordedSpend(ctx context.Context, from, to time.Time) (map[string]sprint.WindowSpend, error) {
	s, err := f(ctx)
	if err != nil {
		return nil, err
	}
	return sprint.RecordedSpendBetween(s, from, to), nil
}

// SpendGate is what the gate reads: each provider's readout and the store. A nil Store is a
// store with no reader, and the gate refuses on it.
type SpendGate struct {
	Providers []SpendProvider
	Store     SpendStore
	// StoreWhy is why Store is nil, said in the refusal.
	StoreWhy string
}

// SpendLine is one provider's comparison as the gate printed it.
type SpendLine struct {
	Provider   string
	Unit       string // "usd" or "tokens"
	Store      float64
	Reported   float64
	Gap        float64 // Reported less Store
	Share      float64 // |Gap| over Reported; 1 when Reported is 0 and Store is not
	Over       bool
	Unreadable string // why the provider's readout could not be read; "" when it was
}

// SpendVerdict is the gate's answer over a window.
type SpendVerdict struct {
	From, To time.Time
	Lines    []SpendLine
	// StoreErr is why the store's records could not be read; "" when they were.
	StoreErr string
}

// Refused says the release may not be cut: the store unread, a provider unreadable, or a gap
// past the bound.
func (v SpendVerdict) Refused() bool {
	if v.StoreErr != "" {
		return true
	}
	return slices.ContainsFunc(v.Lines, func(l SpendLine) bool { return l.Over || l.Unreadable != "" })
}

// SpendWindow is [since, until) rounded to whole UTC days: since's day from its midnight,
// until's day excluded.
func SpendWindow(since, until time.Time) (time.Time, time.Time) {
	day := func(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }
	return day(since), day(until)
}

// CheckSpend is the gate over [from, to): every provider the gate reads and every provider
// the store recorded in the window, each compared (see the file's head). It never errs: a
// read that fails is a line of the verdict.
func CheckSpend(ctx context.Context, g SpendGate, from, to time.Time) SpendVerdict {
	v := SpendVerdict{From: from, To: to}
	var recorded map[string]sprint.WindowSpend
	switch {
	case g.Store == nil:
		v.StoreErr = "the store has no reader in this release lane"
		if g.StoreWhy != "" {
			v.StoreErr = g.StoreWhy
		}
	default:
		got, err := g.Store.RecordedSpend(ctx, from, to)
		if err != nil {
			v.StoreErr = "the store's records could not be read: " + err.Error()
		}
		recorded = got
	}
	byName := map[string]SpendProvider{}
	for _, p := range g.Providers {
		byName[strings.ToLower(p.Name())] = p
	}
	names := slices.Sorted(maps.Keys(byName))
	for _, n := range slices.Sorted(maps.Keys(recorded)) {
		if _, ok := byName[n]; !ok {
			names = append(names, n)
		}
	}
	for _, n := range names {
		rec := recorded[n]
		p, ok := byName[n]
		if !ok {
			name := n
			if name == "" {
				name = "(none)"
			}
			v.Lines = append(v.Lines, SpendLine{Provider: name, Unit: "usd", Store: rec.USD,
				Unreadable: fmt.Sprintf("the store recorded %d runs of it and no readout of its own spend is known", rec.Records)})
			continue
		}
		l := SpendLine{Provider: n, Unit: "usd", Store: rec.USD}
		if p.Subscription() {
			l.Unit, l.Store = "tokens", float64(rec.Tokens)
		}
		got, err := p.Spend(ctx, from, to)
		if err != nil {
			l.Unreadable = err.Error()
			v.Lines = append(v.Lines, l)
			continue
		}
		l.Reported = got.USD
		if p.Subscription() {
			l.Reported = float64(got.Tokens)
		}
		l.Gap = l.Reported - l.Store
		switch {
		case l.Reported > 0:
			l.Share = math.Abs(l.Gap) / l.Reported
		case l.Store > 0:
			l.Share = 1
		}
		l.Over = l.Share > sprint.CostGapOver
		v.Lines = append(v.Lines, l)
	}
	return v
}

// figure is a line's amount in its unit.
func (l SpendLine) figure(x float64) string {
	if l.Unit == "tokens" {
		return fmt.Sprintf("%.0f", x)
	}
	return sprint.Dollars(x)
}

// Said is the line as the gate prints it.
func (l SpendLine) Said(token string) string {
	if l.Unreadable != "" {
		return fmt.Sprintf("RELEASE %s SPEND provider=%s unit=%s store=%s provider_says=unreadable verdict=refused why=%q",
			token, field(l.Provider), l.Unit, l.figure(l.Store), l.Unreadable)
	}
	verdict := "ok"
	if l.Over {
		verdict = "refused"
	}
	return fmt.Sprintf("RELEASE %s SPEND provider=%s unit=%s store=%s provider_says=%s gap=%s share=%.1f%% bound=%.0f%% verdict=%s",
		token, field(l.Provider), l.Unit, l.figure(l.Store), l.figure(l.Reported), l.figure(math.Abs(l.Gap)), l.Share*100, sprint.CostGapOver*100, verdict)
}

// errSpend says the spend gate refused and its lines are already printed.
var errSpend = errors.New("spend-gate")

// SpendRemedy is what the refusal tells a person to do.
const SpendRemedy = "price every paid call and reconcile (nova-sprint cost reconcile), or make the named readout readable, then cut again"

// SpendNote is the gate as `release help` and `cut -h` say it.
var SpendNote = "cut runs the spend gate before it tags: over the release's window (since the previous tag, or --spend-since/--spend-until; whole UTC days), " +
	"each paid provider's own count of its spend is set beside the sprint store's records of it, and a subscription friend's harness token receipts beside the recorded tokens; " +
	"a gap over 5% of the provider's figure refuses the tag, naming the provider, both figures and the gap. " +
	"A readout that cannot be read, a provider the store recorded with no readout, and a store that cannot be read each refuse, never pass. Keys come from nova-secrets exec and are never printed."

// spendCheck is the gate as `cut` runs it, after the previous tag is known: the window
// resolved, every line printed, and errSpend when it refused.
func spendCheck(ctx context.Context, token string, o options, deps Deps, forge Forge, previous string, errs io.Writer) error {
	if deps.Spend == nil {
		// Only a caller of Run that wired no gate reaches this; Main wires the production one.
		fmt.Fprintf(errs, "RELEASE %s NOTE spend-gate=unwired: this caller of the release verbs gave no spend gate\n", token)
		return nil
	}
	now := time.Now
	if deps.Now != nil {
		now = deps.Now
	}
	until := now()
	if o.spendUntil != "" {
		t, err := parseSpendTime(o.spendUntil)
		if err != nil {
			return refuse("give --spend-until as 2006-01-02 or RFC 3339", "--spend-until %q: %s", o.spendUntil, err)
		}
		until = t
	}
	var since time.Time
	switch {
	case o.spendSince != "":
		t, err := parseSpendTime(o.spendSince)
		if err != nil {
			return refuse("give --spend-since as 2006-01-02 or RFC 3339", "--spend-since %q: %s", o.spendSince, err)
		}
		since = t
	case previous == "":
		return refuse("name the window with --spend-since <day>", "there is no previous release tag to measure the spend window from")
	default:
		tt, ok := forge.(TagTimer)
		if !ok {
			return refuse("name the window with --spend-since <day>", "the forge cannot say when %s was cut", previous)
		}
		t, err := tt.TagTime(ctx, o.repo, previous)
		if err != nil {
			return refuse("name the window with --spend-since <day>", "cannot read when %s was cut: %s", previous, err)
		}
		since = t
	}
	from, to := SpendWindow(since, until)
	if to.Before(from) {
		return refuse("give a window whose start is before its end", "the spend window %s..%s ends before it starts", from.Format(time.DateOnly), to.Format(time.DateOnly))
	}
	progress(errs, "reading each provider's own spend and the store's records over %s..%s", from.Format(time.DateOnly), to.Format(time.DateOnly))
	v := CheckSpend(ctx, *deps.Spend, from, to)
	for _, l := range v.Lines {
		fmt.Fprintln(errs, l.Said(token))
	}
	window := from.Format(time.DateOnly) + ".." + to.Format(time.DateOnly)
	if v.StoreErr != "" {
		fmt.Fprintf(errs, "RELEASE %s SPEND store=unreadable verdict=refused why=%q\n", token, v.StoreErr)
	}
	if !v.Refused() {
		fmt.Fprintf(errs, "RELEASE %s NOTE spend-gate=ok window=%s providers=%d\n", token, window, len(v.Lines))
		return nil
	}
	over, unreadable := 0, 0
	var named []string
	for _, l := range v.Lines {
		switch {
		case l.Unreadable != "":
			unreadable++
			named = append(named, l.Provider)
		case l.Over:
			over++
			named = append(named, l.Provider)
		}
	}
	if v.StoreErr != "" {
		named = append(named, "store")
	}
	fmt.Fprintf(errs, "RELEASE %s REFUSED reason=spend-gate window=%s over=%d unreadable=%d store=%s named=%s remedy=%q\n",
		token, window, over, unreadable, map[bool]string{true: "unreadable", false: "read"}[v.StoreErr != ""], strings.Join(named, ","), SpendRemedy)
	return errSpend
}

// parseSpendTime reads a day (2006-01-02, its UTC midnight) or an RFC 3339 time.
func parseSpendTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// TagTimer is a forge that can say when a tag was cut: the time of the commit it points at.
type TagTimer interface {
	TagTime(ctx context.Context, repo, tag string) (time.Time, error)
}

// TagTime is when the tag's commit was committed.
func (g *GH) TagTime(ctx context.Context, repo, tag string) (time.Time, error) {
	out, err := g.api(ctx, "api", "repos/"+repo+"/commits/"+tag, "--jq", ".commit.committer.date")
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(out))
}

// ---------------------------------------------------------------------------
// The production readouts.
// ---------------------------------------------------------------------------

// ProductionSpend is the gate Main wires: openrouter's own activity, through the key
// nova-secrets exec puts in the environment. The store has no reader in nova-update yet
// (the sprint store is reached through nova-sprint's seat login), so the production gate
// refuses on the store until one is wired: a release is never cut on spend nobody checked.
func ProductionSpend(getenv func(string) string) *SpendGate {
	return &SpendGate{
		Providers: []SpendProvider{&OpenRouterActivity{Getenv: getenv}},
		StoreWhy:  "nova-update has no reader of the sprint store's cost records yet (nova-sprint reaches the store through its seat login): the spend gate cannot be satisfied until one is wired",
	}
}

// OpenRouterActivityURL is openrouter's account activity: GET with a provisioning key as a
// bearer token answers {"data": [{"date": "2006-01-02", "usage": <dollars>, ...}, ...]},
// one item per day and endpoint, over the last 30 completed UTC days.
const OpenRouterActivityURL = "https://openrouter.ai/api/v1/activity"

// OpenRouterActivityKeyEnv is the variable the provisioning key is in, as
// `nova-secrets exec --only OPENROUTER_PROVISIONING_KEY -- nova-update release cut ...`
// delivers it.
const OpenRouterActivityKeyEnv = "OPENROUTER_PROVISIONING_KEY"

// openRouterActivityDays is how far back the activity reaches.
const openRouterActivityDays = 30

// OpenRouterActivity is openrouter's own count of a window, summed from its activity.
type OpenRouterActivity struct {
	Getenv    func(string) string
	Transport http.RoundTripper
	Now       func() time.Time
}

// Name is openrouter.
func (*OpenRouterActivity) Name() string { return "openrouter" }

// Subscription is false: openrouter bills dollars.
func (*OpenRouterActivity) Subscription() bool { return false }

// Spend sums the activity's usage over the window's days; a window reaching past the
// activity's 30 days, no key, or an answer not of the shape cannot be read. The key is never
// in an error.
func (a *OpenRouterActivity) Spend(ctx context.Context, from, to time.Time) (SpendReadout, error) {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	if earliest := now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -openRouterActivityDays); from.Before(earliest) {
		return SpendReadout{}, fmt.Errorf("openrouter's activity reaches back %d days, to %s, and the window starts %s", openRouterActivityDays, earliest.Format(time.DateOnly), from.Format(time.DateOnly))
	}
	getenv := a.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	key := getenv(OpenRouterActivityKeyEnv)
	if key == "" {
		return SpendReadout{}, fmt.Errorf("%s is not in this environment: run under nova-secrets exec --only %s", OpenRouterActivityKeyEnv, OpenRouterActivityKeyEnv)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, OpenRouterActivityURL, nil)
	if err != nil {
		return SpendReadout{}, fmt.Errorf("the request could not be made: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	rt := a.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return SpendReadout{}, fmt.Errorf("GET %s failed: %w", OpenRouterActivityURL, err)
	}
	defer resp.Body.Close() // ignored: the answer is read to its end or bounded below; a close error loses nothing read
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return SpendReadout{}, fmt.Errorf("GET %s: the answer could not be read: %w", OpenRouterActivityURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return SpendReadout{}, fmt.Errorf("GET %s answered %d", OpenRouterActivityURL, resp.StatusCode)
	}
	var wire struct {
		Data []struct {
			Date  string   `json:"date"`
			Usage *float64 `json:"usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Data == nil {
		return SpendReadout{}, fmt.Errorf("GET %s answered no data list", OpenRouterActivityURL)
	}
	var sum float64
	for _, d := range wire.Data {
		day, err := time.Parse(time.DateOnly, strings.TrimSpace(d.Date)[:min(len(strings.TrimSpace(d.Date)), 10)])
		if err != nil || d.Usage == nil {
			return SpendReadout{}, fmt.Errorf("GET %s answered an item with no date or usage", OpenRouterActivityURL)
		}
		if !day.Before(from) && day.Before(to) {
			sum += *d.Usage
		}
	}
	return SpendReadout{USD: sum}, nil
}
