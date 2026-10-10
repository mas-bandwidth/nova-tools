package friend

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// A lane's finish carries the tokens it used, read from the harness's own session
// record, never from the model's report (docs/SPEC-FRIEND.md, usage capture per
// adapter; the owner, 2026-10-04: "we MUST track the complete cost", and 2026-10-07:
// "maximum throughput and lowest $$$ cost per-unit of work"). On 2026-10-07 the
// OpenCode friend's finishes carried `tokens input=0 ... output=0` and were priced
// "$0.00 (intro rate, route flash-mercury)" while real work ran on the branch:
// the sqlite database the daemon read was fresh after its reinstall, and the route's
// intro-rate price applied to the zeros the record did not hold. This file reads a
// session's usage from opencode's own session export (input, output, cache read, cache write and reasoning
// tokens per step, summed, with the provider and model), and prices a finish so that
// a usage that could not be read, or a route found with no counted token, is unpriced
// — never a dollar figure — and said to the seat.

var errEmptySession = errors.New("opencode session record has no assistant steps")

// usageBaseline is the session's measured total when a card starts. A newly
// opened session has a known zero total; any other failed read is unknown.
func usageBaseline(u LaneTokens, err error) (LaneTokens, bool) {
	if err == nil {
		return u, true
	}
	if errors.Is(err, errEmptySession) {
		return LaneTokens{}, true
	}
	return LaneTokens{}, false
}

// SumOpenCodeRecord sums a session's record: its assistant steps' tokens by class
// (input, cache read, cache write, output, reasoning), and the provider and model the
// steps ran. A record that holds no assistant step carrying tokens answers an error,
// so a finish whose usage cannot be read is said (usage unknown), never priced as
// free. The JSON may be preceded by a line of its own, as `opencode export` prints one.
func SumOpenCodeRecord(data []byte) (LaneTokens, error) {
	t, model, sawAssistant, err := parseOpenCodeSession(string(data))
	if err != nil {
		if errors.Is(err, errNoTokenShape) && !sawAssistant {
			return LaneTokens{}, errEmptySession
		}
		return LaneTokens{}, fmt.Errorf("opencode session record: %w", err)
	}
	return LaneTokens{Tokens: cardcost.Tokens{Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite,
		Output: t.Output, Reasoning: t.Reasoning, Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}, Model: model}, nil
}

// SessionUsage reads the record through OpenCode's export command, the same
// source used by the lane's token-cap poll; the model's report is not a source.
func (p *OpenCodePriced) SessionUsage(session string) (LaneTokens, error) {
	if p == nil || p.OpenCode == nil || p.Run == nil {
		return LaneTokens{}, fmt.Errorf("opencode session record: export runner is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, exit, err := p.Run(ctx, p.Dir, p.program(), []string{"export", session}, "")
	if err == nil && exit != 0 {
		err = fmt.Errorf("exited %d: %s", exit, oneLine(out, 200))
	}
	if err != nil {
		return LaneTokens{}, fmt.Errorf("opencode session record %s: %w", session, err)
	}
	return SumOpenCodeRecord([]byte(out))
}

// UsageUnknownNote is the one judgment the seat gets when a finish's usage could not
// be read: the subject and body, naming the card and why, so the hole is said once,
// never priced as free and never silent.
func UsageUnknownNote(friend, card, why string) (subject, body string) {
	subject = fmt.Sprintf("friend %s: usage unknown for card %s", friend, card)
	body = fmt.Sprintf("%s: the finish of card %s could not read the tokens its lane spent: %s. Its cost is unpriced, never $0.00; opencode's session export is the source, and when it is unavailable the run cannot be priced.\n",
		subject, card, oneLine(why, 300))
	return subject, body
}

// whyZero is the unpriced word of a route found with no counted token: the intro-rate
// price applies only to tokens that were counted, so a finish with none is refused by
// the ledger, never priced $0.00.
const whyZero = "zero tokens: the intro-rate price is applied only to counted tokens"

// FinishCost prices a card's tokens at the route row, as CostOf does, except a route
// found with no counted token is unpriced, never $0.00 (the intro-rate rule).
func FinishCost(t cardcost.Tokens, rp RoutePrice, model string) string {
	if rp.Found && t.Total() == 0 {
		return "unpriced (" + whyZero + ")"
	}
	return CostOf(t, rp, model)
}

// CostLineOf is a finish's cost lines, as CostLine builds them: the Cost: line a
// REPORT.md carries under its Head: line, and the tokens: and cost: lines a RESULT.md
// does. unknown is why the usage could not be read ("" when it could): the finish is
// then unpriced, never a dollar figure; a route found with no counted token is
// unpriced too. The priced line is CostLine's.
func CostLineOf(t LaneTokens, rp RoutePrice, model, unknown string) (report, tokens, cost string) {
	switch {
	case unknown != "":
		cost = "unpriced (usage unknown: " + oneLine(unknown, 200) + ")"
	case rp.Found && t.Tokens.Total() == 0:
		cost = "unpriced (" + whyZero + ")"
	default:
		return CostLine(t, rp, model)
	}
	if unknown == "" && rp.Found && t.Tokens.Total() == 0 {
		t.Tokens = cardcost.None()
	}
	tokens = fmt.Sprintf("input=%s cache_read=%s cache_write=%s output=%s reasoning=%s model=%s harness=opencode",
		count(t.Input), count(t.CacheRead), count(t.CacheWrite), count(t.Output), count(t.Reasoning), model)
	route := "-"
	if rp.Found {
		route = rp.Name
	}
	return fmt.Sprintf("Cost: %s tokens %s price_route=%s", cost, tokens, route), "tokens: " + tokens, "cost: " + cost
}

// PublishFinishCost writes the card's finish cost into its outbox, as PublishCost
// does, with a finish whose usage is unknown or that counted no token unpriced, never
// a dollar figure.
func PublishFinishCost(outbox string, t LaneTokens, rp RoutePrice, model, unknown string) error {
	report, tokens, cost := CostLineOf(t, rp, model, unknown)
	if raw, err := os.ReadFile(filepath.Join(outbox, "REPORT.md")); err == nil {
		out := WithCost(withoutFinishFields(string(raw)), report)
		if err := atomicfile.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte(out), 0o644); err != nil {
			return err
		}
	}
	if raw, err := os.ReadFile(filepath.Join(outbox, "RESULT.md")); err == nil {
		out := strings.TrimRight(withoutFinishFields(string(raw)), "\n") + "\n" + tokens + "\n" + cost + "\n"
		return atomicfile.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte(out), 0o644)
	}
	return nil
}

// withoutFinishFields removes model-authored accounting before the adapter writes
// the measured finish. Friend sync reads the first such field in each file.
func withoutFinishFields(body string) string {
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(strings.TrimLeft(line, "#*-_ \t")), ":")
		if ok && (strings.EqualFold(strings.TrimSpace(key), "cost") || strings.EqualFold(strings.TrimSpace(key), "tokens")) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n") + "\n"
}

// UnpricedRun is one finished run the cost ledger could not price.
type UnpricedRun struct {
	Friend string
	Route  string // the route that should have priced it; "" when there was none
}

// ReconcileUnpriced is what `nova-sprint cost reconcile` lists: the finished runs the
// ledger could not price, one line per friend and route with the count, in friend then
// route order, so the seat sees the hole on one line.
func ReconcileUnpriced(runs []UnpricedRun) []string {
	counts := map[string]int{}
	for _, r := range runs {
		counts[r.Friend+"\x00"+r.Route]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		friend, route, _ := strings.Cut(k, "\x00")
		out = append(out, fmt.Sprintf("friend=%s route=%s unpriced=%d", friend, cmp.Or(route, "-"), counts[k]))
	}
	return out
}
