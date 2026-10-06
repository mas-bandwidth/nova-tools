package friend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// DefaultTokenCap is a card's token cap when the friend row says none
// (docs/SPEC-FRIEND.md, friend-token-cap-b.w1): a harness that re-sends its
// whole context each call makes a long card snowball, so a one-shot lane
// stops at six million tokens. A row's 0 is no cap.
const DefaultTokenCap int64 = 6_000_000

// TokenCapEvery is how often a running card's usage is read.
const TokenCapEvery = 10 * time.Second

// Tokens is a card's usage as its harness recorded it: input, cached input
// (cache reads and writes), output and reasoning.
type Tokens struct {
	Input, CachedInput, Output, Reasoning int64
}

// Sum is every token counted toward the cap.
func (t Tokens) Sum() int64 { return t.Input + t.CachedInput + t.Output + t.Reasoning }

// Since is t less an earlier reading, never below zero.
func (t Tokens) Since(before Tokens) Tokens {
	return Tokens{max(t.Input-before.Input, 0), max(t.CachedInput-before.CachedInput, 0), max(t.Output-before.Output, 0), max(t.Reasoning-before.Reasoning, 0)}
}

func (t Tokens) String() string {
	return fmt.Sprintf("input=%d cached_input=%d output=%d reasoning=%d total=%d", t.Input, t.CachedInput, t.Output, t.Reasoning, t.Sum())
}

// CardUsage is where a lane reads a running card's tokens: opencode's
// session record, claude -p's stream-json usage events, or a test's fake.
type CardUsage interface {
	Tokens(ctx context.Context) (Tokens, error)
}

// TokenCapped is a card's run stopped at its token cap.
type TokenCapped struct {
	Cap    int64
	Tokens Tokens
}

func (e TokenCapped) Error() string {
	return fmt.Sprintf("token cap %d reached at %d tokens", e.Cap, e.Tokens.Sum())
}

// TokenGuard is a one-shot lane's token cap: the friend row's setting and the
// clock its usage is read on.
type TokenGuard struct {
	// Cap is the friend row's token_cap as the daemon last read it; nil is
	// DefaultTokenCap, and 0 is no cap.
	Cap func() int64
	// After is the clock a running card's usage is read on; time.After when nil.
	After func(time.Duration) <-chan time.Time
}

func (g TokenGuard) limit() int64 {
	if g.Cap == nil {
		return DefaultTokenCap
	}
	return g.Cap()
}

func (g TokenGuard) after(d time.Duration) <-chan time.Time {
	if g.After == nil {
		return time.After(d)
	}
	return g.After(d)
}

// Watch runs run, the lane's own child, under a context of its own, and reads
// usage every TokenCapEvery while it runs; when the card's tokens reach the
// cap it cancels that context (its child alone is stopped) and answers the
// cap and the usage, after run has returned. A usage that cannot be read is
// read again at the next tick. Nil: the run ended under the cap, or there is none.
func (g TokenGuard) Watch(ctx context.Context, usage CardUsage, run func(context.Context)) *TokenCapped {
	limit := g.limit()
	if limit <= 0 {
		run(ctx)
		return nil
	}
	child, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(child)
	}()
	var capped *TokenCapped
	for {
		var tick <-chan time.Time
		if capped == nil {
			tick = g.after(TokenCapEvery)
		}
		select {
		case <-done:
			return capped
		case <-tick:
			if t, err := usage.Tokens(child); err == nil && t.Sum() >= limit {
				capped = &TokenCapped{Cap: limit, Tokens: t}
				stop()
			}
		}
	}
}

// HoldAtCap writes the card's REPORT.md in outbox as `Verdict: HOLD` with the
// cap's reason and the usage so far, unless the run wrote one already: the
// lane's end finishes the card from it and the lane takes the next.
func HoldAtCap(outbox, harness string, c TokenCapped) error {
	path := filepath.Join(outbox, "REPORT.md")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(outbox, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("Verdict: HOLD\nHead: none\n\n%s: the friend's one-shot lane stopped its own %s run at the friend row's token cap; usage so far: %s. Whatever was pushed on the card's branch is a draft only.\n", c.Error(), harness, c.Tokens)
	return atomicfile.WriteFile(path, []byte(body), 0o644)
}

// ParseTokenCap reads the friend row's token_cap off her beat's answer
// (row_token_cap=<n>); ok is false when the answer carries none or it is not
// a count.
func ParseTokenCap(answer string) (n int64, ok bool) {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_token_cap="); found {
			if c, err := strconv.ParseInt(v, 10, 64); err == nil && c >= 0 {
				n, ok = c, true
			}
		}
	}
	return n, ok
}

// streamUsage is claude -p's stream-json read as it prints: each assistant
// message's usage, the last event of each message id kept (a message's
// blocks repeat its usage), summed.
type streamUsage struct {
	mu      sync.Mutex
	partial []byte
	byID    map[string]Tokens
}

// claudeUsageEvent is the part of a stream-json assistant line a cap reads.
type claudeUsageEvent struct {
	Type    string `json:"type"`
	Message struct {
		ID    string `json:"id"`
		Usage struct {
			Input       int64 `json:"input_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
			Output      int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Write takes the run's output as it prints, a line at a time.
func (s *streamUsage) Write(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = append(s.partial, p...)
	for {
		at := bytes.IndexByte(s.partial, '\n')
		if at < 0 {
			return
		}
		line := s.partial[:at]
		s.partial = s.partial[at+1:]
		var e claudeUsageEvent
		if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || e.Message.ID == "" {
			continue
		}
		if s.byID == nil {
			s.byID = map[string]Tokens{}
		}
		u := e.Message.Usage
		s.byID[e.Message.ID] = Tokens{Input: u.Input, CachedInput: u.CacheCreate + u.CacheRead, Output: u.Output}
	}
}

func (s *streamUsage) Tokens(context.Context) (Tokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var t Tokens
	for _, m := range s.byID {
		t.Input, t.CachedInput, t.Output = t.Input+m.Input, t.CachedInput+m.CachedInput, t.Output+m.Output
	}
	return t, nil
}

// teeOutput is ctx with w handed each write the command prints, beside the
// tail ctx already carries.
func teeOutput(ctx context.Context, w func([]byte)) context.Context {
	prev, _ := ctx.Value(tailKey{}).(func([]byte))
	return WithOutputTail(ctx, func(p []byte) {
		w(p)
		if prev != nil {
			prev(p)
		}
	})
}

// openCodeTokens is the part of `opencode export <session>` a cap reads.
type openCodeTokens struct {
	Messages []struct {
		Info struct {
			Role   string `json:"role"`
			Tokens struct {
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

// SessionTokens is a session's usage from its export: the sum of its
// assistant messages' tokens, the JSON from the first '{'.
func SessionTokens(export string) (Tokens, error) {
	at := strings.IndexByte(export, '{')
	if at < 0 {
		return Tokens{}, fmt.Errorf("opencode export: no JSON object in %q", oneLine(export, 120))
	}
	var e openCodeTokens
	if err := json.NewDecoder(strings.NewReader(export[at:])).Decode(&e); err != nil {
		return Tokens{}, fmt.Errorf("opencode export: %v", err)
	}
	var t Tokens
	for _, m := range e.Messages {
		if m.Info.Role != "assistant" {
			continue
		}
		k := m.Info.Tokens
		t.Input += k.Input
		t.CachedInput += k.Cache.Read + k.Cache.Write
		t.Output += k.Output
		t.Reasoning += k.Reasoning
	}
	return t, nil
}

// cardOutbox is the outbox a lane turn's text names (CardText's second
// step); empty when it names none.
var cardOutboxLine = regexp.MustCompile(`(?m)^2\. Write (.+)/REPORT\.md and `)

func cardOutbox(text string) string {
	if m := cardOutboxLine.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}
