package card

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
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
	// Now stamps the end record's at field (the wrapper's clock, not Redis
	// TIME); nil means time.Now.
	Now func() time.Time
}

var _ WrapperLedger = (*RedisLedger)(nil)

// Card reads state, bench, attempt and identity from the card hash. It writes
// nothing, so a refusal before launched leaves the keyspace as it was.
func (l *RedisLedger) Card(ctx context.Context) (WrapperCard, error) {
	if l.Store == nil || l.Store.Client() == nil {
		return WrapperCard{}, errors.New("no store")
	}
	vals, err := l.Store.Client().HMGet(ctx, CardKey(l.Sprint, l.Label), "state", "bench", "attempt", "identity").Result()
	if err != nil {
		return WrapperCard{}, err
	}
	str := func(v any) string {
		s, _ := v.(string)
		return s
	}
	attempt, _ := strconv.Atoi(str(vals[2]))
	return WrapperCard{State: str(vals[0]), Bench: str(vals[1]), Attempt: attempt, Identity: str(vals[3])}, nil
}

// Launched is ns_card_launched: dealt to launched.
func (l *RedisLedger) Launched(ctx context.Context, branch, jobDir string) (int, error) {
	res, err := Launched(ctx, l.Store, LaunchRequest{Sprint: l.Sprint, Label: l.Label, Token: l.Token, Branch: branch, JobDir: jobDir})
	return res.Code, err
}

// Beat is ns_card_beat; the first one moves launched to running.
func (l *RedisLedger) Beat(ctx context.Context) (int, error) {
	res, err := Beat(ctx, l.Store, BeatRequest{Sprint: l.Sprint, Label: l.Label, Token: l.Token})
	return res.Code, err
}

// Result calls ns_card_result (#2506) to record the parsed RESULT envelope.
func (l *RedisLedger) Result(ctx context.Context, res typedrec.Result, resultsDir string) (int, error) {
	const verb = "card result"
	if l.Store == nil || l.Store.Client() == nil || !validSprintLabel(l.Sprint, l.Label) || l.Token == "" {
		return usage(verb, l.Label).Code, nil
	}
	c, err := l.Card(ctx)
	if err != nil {
		return WrapperExitRedis, err
	}
	attemptStr := strconv.Itoa(c.Attempt)
	if c.Attempt < 1 && res.Attempt > 0 {
		attemptStr = strconv.Itoa(res.Attempt)
	}

	validStr := "0"
	if res.Valid {
		validStr = "1"
	}

	args := []any{
		l.Sprint,
		l.Label,
		attemptStr,
		l.Token,
		res.Schema,
		res.Kind,
		validStr,
		res.Field,
		res.Defect,
		strconv.Itoa(res.Line),
		res.RawSHA256,
		string(res.RawBytes),
		resultsDir,
	}

	var claimKeys []string
	for k := range res.Claims {
		claimKeys = append(claimKeys, k)
	}
	sort.Strings(claimKeys)
	for _, k := range claimKeys {
		args = append(args, "c_"+strings.ToLower(k), res.Claims[k])
	}

	reply, err := fcall(ctx, l.Store, "ns_card_result", cardKeys(l.Sprint, l.Label), args...)
	if err != nil {
		if res, down := redisDown(verb, l.Label, err); down {
			return res.Code, nil
		}
		return 0, err
	}
	return reply.Code, nil
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
	rec := EndRecord{
		Identity: id, Outcome: end.Outcome, Reason: end.Reason, ExitCode: end.Exit,
		TokenSHA: TokenSHA(l.Token), PushedSHA: "-", At: now().UTC().Format(time.RFC3339),
	}
	if err := WriteEndRecord(end.ResultsDir, rec); err != nil {
		return WrapperExitCouldNot, err
	}
	res, err := End(ctx, l.Store, EndRequest{
		Sprint: l.Sprint, Label: l.Label, Token: l.Token,
		Outcome: end.Outcome, Reason: end.Reason, ResultsDir: end.ResultsDir,
	})
	return res.Code, err
}
