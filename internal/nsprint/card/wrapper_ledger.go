package card

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// RedisLedger is the wrapper's ledger over the sprint's Redis functions:
// ns_card_launched, ns_card_beat and ns_card_end (card_run.lua, #2928). It
// holds the attempt's token and hands it only to those calls; the end record
// it writes carries token_sha, never the token.
type RedisLedger struct {
	Store  *store.Store
	Sprint string
	Label  string
	Token  string
	// LaunchDeadline is enforced atomically by ns_card_launched.
	LaunchDeadline time.Time
	// Now stamps the end record's at field (the wrapper's clock, not Redis
	// TIME); nil means time.Now.
	Now func() time.Time
}

var _ WrapperLedger = (*RedisLedger)(nil)

// CardConfigKey is the card configuration hash; its wall_max_min is the EST
// of a card that carries none (#3653).
const CardConfigKey = "cfg:card"

// Card reads state, bench, attempt, identity and est from the card hash, and
// cfg:card wall_max_min, in one round trip. It writes nothing, so a refusal
// before launched leaves the keyspace as it was. An unreadable cfg:card (no
// key, or a bench user not allowed to read it) is an absent wall_max_min.
func (l *RedisLedger) Card(ctx context.Context) (WrapperCard, error) {
	if l.Store == nil || l.Store.Client() == nil {
		return WrapperCard{}, errors.New("no store")
	}
	pipe := l.Store.Client().Pipeline()
	cardCmd := pipe.HMGet(ctx, CardKey(l.Sprint, l.Label), "state", "bench", "attempt", "identity", "est")
	cfgCmd := pipe.HGet(ctx, CardConfigKey, "wall_max_min")
	_, _ = pipe.Exec(ctx)
	vals, err := cardCmd.Result()
	if err != nil {
		return WrapperCard{}, err
	}
	str := func(v any) string {
		s, _ := v.(string)
		return s
	}
	attempt, _ := strconv.Atoi(str(vals[2]))
	return WrapperCard{
		State: str(vals[0]), Bench: str(vals[1]), Attempt: attempt, Identity: str(vals[3]),
		EstMin: positiveFloat(str(vals[4])), WallMaxMin: positiveFloat(cfgCmd.Val()),
	}, nil
}

// positiveFloat is s as a positive number, or 0.
func positiveFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0
	}
	return f
}

// Launched is ns_card_launched: dealt to launched, recording wall_max_s.
func (l *RedisLedger) Launched(ctx context.Context, branch, jobDir string, wallMax time.Duration) (int, error) {
	res, err := Launched(ctx, l.Store, LaunchRequest{Sprint: l.Sprint, Label: l.Label, Token: l.Token, Branch: branch, JobDir: jobDir, Deadline: l.LaunchDeadline, WallMax: wallMax})
	return res.Code, err
}

// Beat is ns_card_beat; the first one moves launched to running.
func (l *RedisLedger) Beat(ctx context.Context) (int, error) {
	res, err := Beat(ctx, l.Store, BeatRequest{Sprint: l.Sprint, Label: l.Label, Token: l.Token})
	return res.Code, err
}

// End writes end.record into the results directory, then calls ns_card_end.
// A fenced end returns 3 and leaves the record for the reconciler (#2756 3.2).
func (l *RedisLedger) End(ctx context.Context, end WrapperEnd) (int, error) {
	c, err := l.Card(ctx)
	if err != nil {
		return WrapperExitRedis, err
	}
	id, err := ParseIdentity(c.Identity)
	if err != nil {
		return WrapperExitCouldNot, err
	}
	now := l.Now
	if now == nil {
		now = time.Now
	}
	pushed := end.PushedSHA
	if pushed == "" {
		pushed = NoCommit
	}
	rec := EndRecord{
		Identity: id, Outcome: end.Outcome, Reason: end.Reason, ExitCode: end.Exit,
		TokenSHA: TokenSHA(l.Token), PushedSHA: pushed, At: now().UTC().Format(time.RFC3339),
	}
	if err := WriteEndRecord(end.ResultsDir, rec); err != nil {
		return WrapperExitCouldNot, err
	}
	res, err := End(ctx, l.Store, EndRequest{
		Sprint: l.Sprint, Label: l.Label, Token: l.Token,
		Outcome: end.Outcome, Reason: end.Reason, ResultsDir: end.ResultsDir,
		Why: end.Why,
	})
	return res.Code, err
}
