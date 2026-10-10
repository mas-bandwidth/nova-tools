package friend

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// A one-shot lane's card is capped by its tokens (docs/SPEC-FRIEND.md,
// friend-token-cap-bb.w2). A harness that re-sends its whole context each call
// makes a long card snowball: the card's tokens grow with the square of its
// turns. Each lane counts the card's tokens as it runs, from the harness's own
// usage record (a Claude run's stream-json usage, an OpenCode session's export),
// summed over input, cache read, cache write, output and reasoning; when the sum
// reaches the friend row's cap (DefaultTokenCap where the row names none, 0 none)
// the lane stops its own child (the turn's process group, through its context)
// and writes the card's REPORT.md as a hold naming `token cap <cap> reached at
// <n> tokens` and the usage so far, so the card's end is a finish and the
// friend's next card proceeds (lanes.go: a REPORT.md is a done card). The loop's
// capStep is a separate watch of an opencode session's sqlite totals; this is
// the count every one-shot lane has, including a claude lane, which has no sqlite.

// DefaultTokenCap is a card's token cap where the friend row names none.
const DefaultTokenCap int64 = 6_000_000

// TokenPoll is how often a lane reads a running card's usage where the harness
// prints none as it runs (an OpenCode turn: its session's export).
const TokenPoll = 15 * time.Second

// TokenCapOf reads the row's token cap off her beat's answer (row_token_cap=<n>,
// beside row_mode and row_width; 0 is no cap); ok is false when the answer carries
// none, or one that is no whole number at or above zero.
func TokenCapOf(answer string) (int64, bool) {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_token_cap="); found {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return 0, false
			}
			return n, true
		}
	}
	return 0, false
}

// tokenCap is the cap a lane reads before each card: cap's answer, DefaultTokenCap
// when cap is nil.
func tokenCap(row func() int64) int64 {
	if row == nil {
		return DefaultTokenCap
	}
	return row()
}

// Tokens is a card's tokens as the harness's own usage record counts them.
type Tokens struct {
	Input, CacheRead, CacheWrite, Output, Reasoning int64
}

// Sum is every token the card was charged for: what the cap counts.
func (t Tokens) Sum() int64 { return t.Input + t.CacheRead + t.CacheWrite + t.Output + t.Reasoning }

func (t Tokens) add(u Tokens) Tokens {
	return Tokens{t.Input + u.Input, t.CacheRead + u.CacheRead, t.CacheWrite + u.CacheWrite, t.Output + u.Output, t.Reasoning + u.Reasoning}
}

// since is t less base, each count at least zero: a session's growth since a read.
func (t Tokens) since(base Tokens) Tokens {
	return Tokens{max(t.Input-base.Input, 0), max(t.CacheRead-base.CacheRead, 0), max(t.CacheWrite-base.CacheWrite, 0), max(t.Output-base.Output, 0), max(t.Reasoning-base.Reasoning, 0)}
}

func (t Tokens) String() string {
	return fmt.Sprintf("input=%d cache_read=%d cache_write=%d output=%d reasoning=%d", t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning)
}

// CardUsage is a running card's tokens so far, read from its harness's own record.
// A test hands a fake; a lane hands the harness's stream or export.
type CardUsage interface {
	Tokens(ctx context.Context) (Tokens, error)
}

// TokenCapped is a card's turn the lane stopped at its token cap.
type TokenCapped struct {
	Cap   int64
	At    Tokens // the card's usage when the lane stopped it
	Card  string
	Wrote bool // the lane wrote the card's REPORT.md (false: hers stood, or none could be written)
}

func (e TokenCapped) Error() string {
	return fmt.Sprintf("token cap %d reached at %d tokens", e.Cap, e.At.Sum())
}

// oneShotCapReport is the REPORT.md of a card a one-shot lane stopped at its
// token cap: a hold, the cap words, and the usage so far. The name is not
// TokenCapReport: that writer is the loop's (lane_parity.go) and stays as it is.
func oneShotCapReport(friend string, e TokenCapped) string {
	return fmt.Sprintf("Verdict: HOLD\n\nnova-friend's one-shot lane of %s stopped card %s: %s; the usage so far: %s. The lane stopped its own run at the friend row's per-card cap; whatever was pushed on the card's branch is a draft only.\n",
		cmp.Or(friend, "the friend"), e.Card, e.Error(), e.At)
}

// writeCapReport writes e's report into outbox unless a REPORT.md is there already (her
// own report stands, as a lane's end leaves it), and answers e with Wrote set.
func writeCapReport(friend, outbox string, e TokenCapped) (TokenCapped, error) {
	path := filepath.Join(outbox, "REPORT.md")
	if exists(path) {
		return e, nil
	}
	if err := os.MkdirAll(outbox, 0o755); err != nil {
		return e, err
	}
	if err := atomicfile.WriteFile(path, []byte(oneShotCapReport(friend, e)), 0o644); err != nil {
		return e, err
	}
	e.Wrote = true
	return e, nil
}

// tokenWatch is one card's run under its cap: check reads the usage and, at the cap,
// stops the run (stop cancels the run's own context, which signals its own process
// group and no other) once.
type tokenWatch struct {
	cap   int64
	prior Tokens // the card's earlier runs' usage
	usage CardUsage
	stop  context.CancelFunc

	mu     sync.Mutex
	last   Tokens
	capped bool
}

// check reads the run's usage; a read that fails leaves the run going, and after
// the stop the usage stands as it was read at the cap.
func (w *tokenWatch) check(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.capped {
		return
	}
	t, err := w.usage.Tokens(ctx)
	// ignored: a usage read that fails stops nothing; the next read counts, and the turn's price says a record that was not read
	if err != nil {
		return
	}
	w.last = t
	if w.cap > 0 && w.prior.add(t).Sum() >= w.cap {
		w.capped = true
		w.stop()
	}
}

// done is whether the watch stopped the run, and the card's usage at its last read.
func (w *tokenWatch) done() (bool, Tokens) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.capped, w.prior.add(w.last)
}

// claudeStream is a Claude run's usage as its stream-json prints it: each assistant
// message's usage, the last line for a message id standing for it (a message prints a
// line per content block, each with the message's usage so far), summed over messages.
type claudeStream struct {
	mu      sync.Mutex
	partial []byte
	order   []string
	usage   map[string]Tokens
}

// claudeUsageLine is the part of a stream-json assistant line the count reads.
// reasoning_tokens is absent on a Claude transcript today; a line that carries it
// counts it, and one that does not contributes zero.
type claudeUsageLine struct {
	Type    string `json:"type"`
	Message struct {
		ID    string `json:"id"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			Output     int64 `json:"output_tokens"`
			Reasoning  int64 `json:"reasoning_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// add reads the whole lines in p (and what an earlier write left of a line).
func (s *claudeStream) add(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = append(s.partial, p...)
	for {
		at := strings.IndexByte(string(s.partial), '\n')
		if at < 0 {
			return
		}
		line := s.partial[:at]
		s.partial = s.partial[at+1:]
		var l claudeUsageLine
		if json.Unmarshal(line, &l) != nil || l.Type != "assistant" || l.Message.Usage == nil {
			continue
		}
		if s.usage == nil {
			s.usage = map[string]Tokens{}
		}
		id := l.Message.ID
		if id == "" {
			id = fmt.Sprintf("(line %d)", len(s.order))
		}
		if _, seen := s.usage[id]; !seen {
			s.order = append(s.order, id)
		}
		u := l.Message.Usage
		s.usage[id] = Tokens{Input: u.Input, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Output: u.Output, Reasoning: u.Reasoning}
	}
}

func (s *claudeStream) Tokens(context.Context) (Tokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sum := Tokens{}
	for _, id := range s.order {
		sum = sum.add(s.usage[id])
	}
	return sum, nil
}

// watchClaude is ctx for one Claude run of a card under cap: a context of its own whose
// cancel stops the run, every write the run prints read for its usage (and still handed
// to the daemon's tail, WithOutputTail) and checked against the cap.
func watchClaude(ctx context.Context, limit int64, prior Tokens) (context.Context, *tokenWatch, context.CancelFunc) {
	run, cancel := context.WithCancel(ctx)
	s := &claudeStream{}
	w := &tokenWatch{cap: limit, prior: prior, usage: s, stop: cancel}
	prev, _ := ctx.Value(tailKey{}).(func([]byte))
	run = WithOutputTail(run, func(p []byte) {
		if prev != nil {
			prev(p)
		}
		s.add(p)
		w.check(ctx)
	})
	return run, w, cancel
}

// cardRuns is each card's usage over its finished runs, so a card's cap counts every
// run the lane gave it. Keyed by the card's outbox.
type cardRuns struct {
	mu   sync.Mutex
	runs map[string]Tokens
}

func (c *cardRuns) prior(outbox string) Tokens {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runs[outbox]
}

func (c *cardRuns) set(outbox string, t Tokens) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runs == nil {
		c.runs = map[string]Tokens{}
	}
	c.runs[outbox] = t
}

// openCodeTokens is the part of `opencode export <session>` the token count reads: each
// assistant message's tokens.
type openCodeTokens struct {
	Messages []struct {
		Info struct {
			Role     string `json:"role"`
			Model    string `json:"modelID"`
			Provider string `json:"providerID"`
			Tokens   *struct {
				Input     int64 `json:"input"`
				Output    int64 `json:"output"`
				Reasoning int64 `json:"reasoning"`
				Cache     struct {
					Read  int64 `json:"read"`
					Write int64 `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
		} `json:"info"`
	} `json:"messages"`
}

// errNoTokenShape is an export whose assistant messages carry no tokens the count reads.
var errNoTokenShape = errors.New("opencode export: no assistant message carries tokens")

// SessionTokens is a session's tokens from its export: its assistant messages' tokens
// summed. An export with no message carrying them answers errNoTokenShape, and the cap
// is not applied; its price (SessionCost) stands as it is.
func SessionTokens(export string) (Tokens, error) {
	sum, _, _, err := parseOpenCodeSession(export)
	return sum, err
}

// parseOpenCodeSession is the single export parser for both the running token cap
// and the finish. Its model is the last assistant step's provider and model.
// sawAssistant distinguishes an empty new session from a malformed usage record.
func parseOpenCodeSession(export string) (Tokens, string, bool, error) {
	at := strings.IndexByte(export, '{')
	if at < 0 {
		return Tokens{}, "", false, fmt.Errorf("opencode export: no JSON object in %q", oneLine(export, 120))
	}
	var e openCodeTokens
	if err := json.NewDecoder(strings.NewReader(export[at:])).Decode(&e); err != nil {
		return Tokens{}, "", false, fmt.Errorf("opencode export: %v", err)
	}
	sum, model, found, sawAssistant := Tokens{}, "", false, false
	for _, m := range e.Messages {
		if m.Info.Role != "assistant" {
			continue
		}
		sawAssistant = true
		if t := m.Info.Tokens; t != nil {
			sum, found = sum.add(Tokens{Input: t.Input, CacheRead: t.Cache.Read, CacheWrite: t.Cache.Write, Output: t.Output, Reasoning: t.Reasoning}), true
			if m.Info.Model != "" {
				model = m.Info.Model
				if m.Info.Provider != "" && !strings.HasPrefix(model, m.Info.Provider+"/") {
					model = m.Info.Provider + "/" + model
				}
			}
		}
	}
	if !found {
		return Tokens{}, "", sawAssistant, errNoTokenShape
	}
	return sum, model, sawAssistant, nil
}

// openCodeUsage is a lane turn's usage: its session's tokens since the read before the
// turn began.
type openCodeUsage struct {
	p       *OpenCodePriced
	session string
	base    Tokens
}

func (u openCodeUsage) Tokens(ctx context.Context) (Tokens, error) {
	t, err := u.p.sessionTokens(ctx, u.session)
	return t.since(u.base), err
}

// cardOutboxLine is CardText's line naming the card's outbox, and cardIDLine its line
// naming the card: an OpenCode turn is handed its card as text.
var (
	cardOutboxLine = regexp.MustCompile(`(?m)^2\. Write (.+)/REPORT\.md and `)
	cardIDLine     = regexp.MustCompile(`(?m)^nova-friend: lane \d+ of \d+: one card this turn, (.+)\. Do exactly`)
)

// turnCard is the card a lane turn's text hands (CardText): its id and outbox; ok is
// false for a turn that hands none.
func turnCard(text string) (id, outbox string, ok bool) {
	o := cardOutboxLine.FindStringSubmatch(text)
	if o == nil {
		return "", "", false
	}
	if m := cardIDLine.FindStringSubmatch(text); m != nil {
		id = m[1]
	}
	return id, o[1], true
}

// tick is the poll's clock: Tick's when set, else a real ticker.
func (p *OpenCodePriced) tick(d time.Duration) (<-chan time.Time, func()) {
	if p.Tick != nil {
		return p.Tick(d)
	}
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// sessionTokens is a session's tokens from its export.
func (p *OpenCodePriced) sessionTokens(ctx context.Context, id string) (Tokens, error) {
	out, exit, err := p.Run(ctx, p.Dir, p.program(), []string{"export", id}, "")
	if err == nil && exit != 0 {
		err = fmt.Errorf("exited %d: %s", exit, oneLine(out, 200))
	}
	if err != nil {
		return Tokens{}, err
	}
	return SessionTokens(out)
}

// deliverCapped is a card's turn under its cap: the session's tokens read before it,
// then on each tick of TokenPoll while it runs; at the cap the turn's context is
// cancelled (its own process group signalled) and the card's REPORT.md written as a
// hold. A session whose export carries no tokens runs the turn uncapped, said on the
// record.
func (p *OpenCodePriced) deliverCapped(ctx context.Context, id, text string) (LaneTurn, error) {
	card, outbox, isCard := turnCard(text)
	limit := tokenCap(p.TokenCap)
	if !isCard || limit <= 0 {
		return p.OpenCode.DeliverTo(ctx, id, text)
	}
	base, err := p.sessionTokens(ctx, id)
	if err != nil {
		if p.Out != nil {
			fmt.Fprintf(p.Out, "opencode: session=%s card=%s token_cap=%d not applied: its usage was not read: %v\n", id, card, limit, err)
		}
		return p.OpenCode.DeliverTo(ctx, id, text)
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &tokenWatch{cap: limit, prior: p.cards.prior(outbox), usage: openCodeUsage{p: p, session: id, base: base}, stop: cancel}
	ticks, stop := p.tick(TokenPoll)
	defer stop()
	ended := make(chan struct{})
	polled := make(chan struct{})
	go func() {
		defer close(polled)
		for {
			select {
			case <-ended:
				return
			case <-run.Done():
				return
			default:
			}
			select {
			case <-ended:
				return
			case <-run.Done():
				return
			case <-ticks:
				w.check(ctx)
			}
		}
	}()
	lt, err := p.OpenCode.DeliverTo(run, id, text)
	close(ended)
	<-polled
	w.check(ctx) // the turn's last usage, for the card's next run
	capped, at := w.done()
	p.cards.set(outbox, at)
	if !capped {
		return lt, err
	}
	e, werr := writeCapReport(p.Friend, outbox, TokenCapped{Cap: limit, At: at, Card: card})
	if p.Out != nil {
		fmt.Fprintf(p.Out, "opencode: session=%s card=%s %s; its run is stopped, usage %s%s\n", id, card, e.Error(), at, reportWords(e, werr))
	}
	return lt, e
}

// reportWords is what the record says of a capped card's report.
func reportWords(e TokenCapped, err error) string {
	switch {
	case err != nil:
		return "; its REPORT.md was not written: " + err.Error()
	case e.Wrote:
		return "; REPORT.md written as HOLD"
	default:
		return "; her own REPORT.md stands"
	}
}
