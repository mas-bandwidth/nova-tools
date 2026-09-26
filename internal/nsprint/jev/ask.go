package jev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/redis/go-redis/v9"
)

// Asker is TypeSafe Jev as the ledger uses it: one typed call (decide.Client
// is one; tests fake it, and never call the real Jev).
type Asker interface {
	Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error)
}

// PricePerMTok is Jev's price in dollars per million tokens
// (jev-is-typesafe-ai-jev: ~$42 per billion input tokens), applied to every
// token the provider reports.
const PricePerMTok = 0.042

// Answer is Jev's answer on one row.
type Answer struct {
	Type, Subject, Answer, Version, InputSHA string
	Conf                                     float64
	Tokens                                   int
	TokensKnown                              bool
	MS                                       int64
}

// Cost is the answer's dollars, "-" when the provider reported no usage (no
// evidence is not zero).
func (a Answer) Cost() string {
	if !a.TokensKnown {
		return "-"
	}
	return strconv.FormatFloat(float64(a.Tokens)*PricePerMTok/1e6, 'f', 8, 64)
}

// rowPrompt is the prompt a row is asked with: its own (a hook's), else the
// built-in one for its type.
func rowPrompt(row map[string]string) (Prompt, error) {
	if v := row["q_version"]; v != "" {
		p := Prompt{Version: v, Instructions: row["q_instructions"]}
		if err := json.Unmarshal([]byte(row["q_options"]), &p.Options); err != nil || len(p.Options) < 2 {
			return Prompt{}, fmt.Errorf("the row's own prompt %s has no options", v)
		}
		return p, nil
	}
	p, ok := PromptFor(row["type"])
	if !ok {
		return Prompt{}, fmt.Errorf("type %s has no prompt", row["type"])
	}
	return p, nil
}

// AskRow asks Jev one row's question: one typed call, the state as the row
// holds it. ms is the call's, on clock (time.Now; a test's own).
func AskRow(ctx context.Context, a Asker, row map[string]string, clock func() time.Time) (Answer, error) {
	t, subject, state := row["type"], row["subject"], row["state"]
	if t == "" || subject == "" || strings.TrimSpace(state) == "" {
		return Answer{}, errors.New("the row has no type, subject or state")
	}
	p, err := rowPrompt(row)
	if err != nil {
		return Answer{}, err
	}
	start := clock()
	ans, usage, err := a.Decide(ctx, state, map[string]decide.Question{t: {Instructions: p.Instructions, Choice: p.Options}})
	ms := clock().Sub(start).Milliseconds()
	if err != nil {
		return Answer{}, err
	}
	got, ok := ans[t]
	if !ok || !p.Has(got.Choice) {
		return Answer{}, fmt.Errorf("Jev answered %q, not one of %s's options", got.Choice, t)
	}
	return Answer{Type: t, Subject: subject, Answer: got.Choice, Version: p.Version, InputSHA: row["input_sha"],
		Conf: got.Confidence, Tokens: usage.InputTokens + usage.OutputTokens, TokensKnown: usage.Known(), MS: ms}, nil
}

// answerCmds queues Jev's answer onto its row and the log.
func answerCmds(ctx context.Context, p redis.Pipeliner, a Answer, at int64) {
	tokens := "-"
	if a.TokensKnown {
		tokens = strconv.Itoa(a.Tokens)
	}
	conf := strconv.FormatFloat(a.Conf, 'f', 4, 64)
	p.HSet(ctx, RowKey(a.Type, a.Subject), "jev", a.Answer, "jev_conf", conf, "prompt_version", a.Version,
		"tokens", tokens, "cost", a.Cost(), "ms", strconv.FormatInt(a.MS, 10), "jev_at", strconv.FormatInt(at, 10))
	p.XAdd(ctx, &redis.XAddArgs{Stream: KeyDecisions, MaxLen: LogMax, Approx: true, Values: []any{
		"event", "jev", "type", a.Type, "subject", a.Subject, "answer", a.Answer, "conf", conf,
		"prompt_version", a.Version, "input_sha", a.InputSHA, "cost", a.Cost(), "ms", strconv.FormatInt(a.MS, 10),
		"at", strconv.FormatInt(at, 10)}})
}

// Asked is one pending row's result: its answer, or why it was not asked
// (Err) and whether it went back to pending (Again).
type Asked struct {
	Member string
	Answer Answer
	Err    error
	Again  bool
}

// writeTimeout bounds the writes an ask makes after its context is done:
// answers already paid for are written, and claimed rows go back.
const writeTimeout = 10 * time.Second

// AskPending claims up to n rows off jev:pending (SMOVE to jev:asking, so a
// second ask never takes the same row and a claimed row is never only in
// memory), asks Jev each (one typed call per row), and in one MULTI writes
// the answers, returns the rows the provider failed on to jev:pending and
// SREMs the rest from jev:asking. The writes run on the caller's context
// without its cancel (a cancelled ask still writes what it paid for and
// puts back what it claimed); when even that fails the error names the
// rows left in jev:asking, to be printed. Every result is returned.
func AskPending(ctx context.Context, c redis.Cmdable, a Asker, n int64) ([]Asked, error) {
	cand, err := c.SRandMemberN(ctx, KeyPending, n).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read %s: %w", KeyPending, err)
	}
	if len(cand) == 0 {
		return nil, nil
	}
	moved := make([]*redis.BoolCmd, len(cand))
	if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, m := range cand {
			moved[i] = p.SMove(ctx, KeyPending, KeyAsking, m)
		}
		return nil
	}); err != nil {
		return nil, putBack(ctx, c, cand, fmt.Errorf("claim from %s: %w", KeyPending, err))
	}
	var members []string
	for i, m := range cand {
		if moved[i].Val() {
			members = append(members, m)
		}
	}
	if len(members) == 0 {
		return nil, nil
	}
	rows := map[string]map[string]string{}
	keys := make([]string, len(members))
	for i, m := range members {
		t, s, _ := strings.Cut(m, " ")
		keys[i] = RowKey(t, s)
	}
	if err := hgetAll(ctx, c, keys, func(k string) string { return k }, rows); err != nil {
		return nil, putBack(ctx, c, members, err)
	}
	out := make([]Asked, len(members))
	var again []string
	for i, m := range members {
		out[i].Member = m
		row := rows[keys[i]]
		if row == nil {
			out[i].Err = errors.New("no row " + keys[i])
			continue
		}
		ans, err := AskRow(ctx, a, row, time.Now)
		if err != nil {
			out[i].Err = err
			// a provider's failure (an error, an answer off the options, a
			// cancelled call) is asked again; a row that cannot be asked (no
			// prompt, no state) is not
			if _, perr := rowPrompt(row); perr == nil && strings.TrimSpace(row["state"]) != "" {
				out[i].Again = true
				again = append(again, m)
			}
			continue
		}
		out[i].Answer = ans
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	at := time.Now().UnixMilli()
	if _, err := c.TxPipelined(wctx, func(p redis.Pipeliner) error {
		for _, r := range out {
			if r.Err == nil {
				answerCmds(wctx, p, r.Answer, at)
			}
		}
		p.SRem(wctx, KeyAsking, toAny(members)...)
		if len(again) > 0 {
			p.SAdd(wctx, KeyPending, toAny(again)...)
		}
		return nil
	}); err != nil {
		// nothing was written: every claimed row goes back, to be asked again
		return out, putBack(ctx, c, members, fmt.Errorf("write the answers: %w", err))
	}
	return out, nil
}

// putBack returns claimed rows from jev:asking to jev:pending on the
// caller's context without its cancel, and says so in the error; when even
// that fails, the error names the rows left in jev:asking.
func putBack(ctx context.Context, c redis.Cmdable, members []string, cause error) error {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	if _, err := c.TxPipelined(wctx, func(p redis.Pipeliner) error {
		p.SRem(wctx, KeyAsking, toAny(members)...)
		p.SAdd(wctx, KeyPending, toAny(members)...)
		return nil
	}); err != nil {
		return fmt.Errorf("%w; and the %d claimed rows could not go back (%v): they stay in %s: %s",
			cause, len(members), err, KeyAsking, strings.Join(members, ","))
	}
	return fmt.Errorf("%w; the %d claimed rows are back on %s", cause, len(members), KeyPending)
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
