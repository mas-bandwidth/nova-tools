package hook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store is the record over one Redis client: the bus Redis. Every verb is one
// round trip, a script or a pipeline (docs/SPEC-HOOK.md, the verbs).
type Store struct {
	C redis.Cmdable
	// Timeout, when set, bounds each round trip on its own, inside the
	// caller's context: a long-running holder's beat makes several, and one
	// slow call never spends the next one's time.
	Timeout time.Duration
}

// call is ctx bounded by one round trip's Timeout, when the store has one.
func (s Store) call(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.Timeout)
}

// allKeys is every push's lease and proof key of name, then its challenge: the
// keys arm and unhook clear.
func allKeys(name string) []string {
	var keys []string
	for _, p := range Pushes {
		keys = append(keys, LiveKey(name, p), ProofKey(name, p))
	}
	return append(keys, ChallengeKey(name), DeliveryKey(name))
}

// nowMS is the store's TIME in ms, as a Lua expression's lines: every stamp the
// record holds is the store's clock, never a holder's.
const nowMS = `local t = redis.call('TIME')
local ms = t[1] .. string.format('%03d', math.floor(tonumber(t[2]) / 1000))
`

// The modes of an arm (armLua's ARGV[4]).
const (
	armHolder = "0" // a holder's start: join the role's record, arm only where there is none
	armFresh  = "1" // always a new epoch
	armWord   = "2" // the identity's own word (nova-hook hook): join the role's record, else a new epoch
)

// armLua hooks name in as a role (tla/Hook.tla, Arm and HookAs). A record
// hooked as the same role is joined at its epoch (code 1), unless fresh. A
// holder's arm leaves a record hooked as another role as it is (code 2) and
// an unhooked record unhooked (code 3), so an unhook stands across a holder's
// restart; it arms only where there is no record at all. The identity's word
// arms over either, and fresh always arms. To arm, the epoch moves on, every
// lease, proof and challenge is cleared, and the record says hooked (code 0).
// KEYS: record, names, then allKeys. ARGV: name, role, by, mode.
var armLua = redis.NewScript(nowMS + `
local st = redis.call('HGET', KEYS[1], 'state')
local role = redis.call('HGET', KEYS[1], 'role')
if st and ARGV[4] ~= '` + armFresh + `' then
  local e = tonumber(redis.call('HGET', KEYS[1], 'epoch'))
  if st == 'hooked' and role == ARGV[2] then return {e, 1} end
  if ARGV[4] == '` + armHolder + `' then
    if st ~= 'hooked' then return {e, 3} end
    return {e, 2}
  end
end
local e = redis.call('HINCRBY', KEYS[1], 'epoch', 1)
redis.call('HSET', KEYS[1], 'role', ARGV[2], 'state', 'hooked', 'armed', ms, 'by', ARGV[3])
redis.call('HDEL', KEYS[1], 'unhooked', 'reason', 'misses', 'beat_at', 'recent', 'pending_until', 'pending_missed', 'pending_sent', 'quiet_after', 'bus_sent_at', 'renewal_failed')
for i = 3, #KEYS do redis.call('DEL', KEYS[i]) end
redis.call('SADD', KEYS[2], ARGV[1])
return {e, 0}
`)

// held is the test every write of a holder makes, as a Lua condition: the
// record is hooked at the holder's epoch (ARGV[1]) as the holder's role
// (ARGV[2]). The role matters after a record is lost and armed again from
// nothing (tla/Hook.tla, Flush): its epoch counts from 1 again, and a holder
// of another role that joined an old epoch of the same number writes nothing.
const held = `redis.call('HGET', KEYS[1], 'state') == 'hooked' and redis.call('HGET', KEYS[1], 'epoch') == ARGV[1] and redis.call('HGET', KEYS[1], 'role') == ARGV[2]`

// stampLua writes a lease or a proof under the record's epoch and role
// (tla/Hook.tla, Renew and ProveDaemon): refused (0) unless held, and a proof
// (need set) refused (-1) unless the push's lease is live.
// KEYS: record, the key to write, the lease that must stand. ARGV: epoch, role, ttl ms, by (JSON).
var stampLua = redis.NewScript(nowMS + `
if not (` + held + `) then return 0 end
if KEYS[3] ~= KEYS[2] and redis.call('EXISTS', KEYS[3]) == 0 then return -1 end
redis.call('SET', KEYS[2], '{"epoch":' .. ARGV[1] .. ',"at":' .. ms .. ',"by":' .. ARGV[4] .. '}', 'PX', ARGV[3])
return 1
`)

// dropLua clears a proof under the record's epoch and role (tla/Hook.tla,
// Unprove): a daemon whose session stopped answering. KEYS: record, proof.
// ARGV: epoch, role.
var dropLua = redis.NewScript(`
if not (` + held + `) then return 0 end
redis.call('DEL', KEYS[2])
return 1
`)

// challengeLua bootstraps a challenge (tla/HookBeat.tla, Bootstrap): an
// existing challenge or beat_at in this epoch belongs to its issuer. The
// store keeps only the nonce's sum; only a reader of its delivery can answer.
// KEYS: record, challenge. ARGV: epoch, role, sum, pushes (comma), ttl ms.
var challengeLua = redis.NewScript(`
if not (` + held + `) then return 0 end
if redis.call('EXISTS', KEYS[2]) == 1 or tonumber(redis.call('HGET', KEYS[1], 'beat_at') or '0') > 0 then return -1 end
redis.call('HSET', KEYS[2], 'epoch', ARGV[1], 'sum', ARGV[3], 'pushes', ARGV[4])
redis.call('PEXPIRE', KEYS[2], ARGV[5])
return 1
`)

// answerLua answers name's challenge (tla/Hook.tla, Answer; tla/HookBeat.tla,
// Answer and AnswerIssued). A heartbeat challenge is answered with the sum of
// any nonce the server issued to the name in its epoch within the down-after
// window and not yet spent (the record's recent, newest first: a harness may
// hand the model a challenge after a newer one was issued); else the standing
// challenge (a bootstrap's, or one no recent holds) with its own nonce. The
// answer proves every push the challenge named whose lease is live, spends
// that challenge (the standing one too when it is the one answered), and
// clears the heartbeat's count of misses with answered_at now. It answers
// {proven, back, issued ms} (back 0 for the newest, k for k challenges
// before it; issued 0 when no recent holds it) or {code, window ms}: -1 no
// challenge stands, -2 another nonce, -3 the record moved since, -4 a standing
// heartbeat challenge past its interval that no recent holds; the window is
// the server row's down_after, 0 when it has none.
// KEYS: record, challenge, then per push of Pushes its lease and proof, then serve.
// ARGV: sum, ttl ms, by (JSON), then the names of Pushes.
var answerLua = redis.NewScript(nowMS + `
local now = tonumber(ms)
local window = tonumber(redis.call('HGET', KEYS[#KEYS], 'down_after') or '0')
local st, ep = redis.call('HGET', KEYS[1], 'state'), redis.call('HGET', KEYS[1], 'epoch')
local c = redis.call('HMGET', KEYS[2], 'epoch', 'sum', 'pushes')
local hit, back, at, pushes, keep = false, 0, '0', '', {}
for x in string.gmatch(redis.call('HGET', KEYS[1], 'recent') or '', '%S+') do
  local e, s, a, d, p = string.match(x, '^(%d+):(%x+):(%d+):(%d+):(%S+)$')
  if not hit and s == ARGV[1] and e == ep and now <= tonumber(d) then
    hit, at, pushes = true, a, p
  else
    if not hit then back = back + 1 end
    table.insert(keep, x)
  end
end
if hit then
  if st ~= 'hooked' then return {-3, window} end
  if c[2] == ARGV[1] or at == redis.call('HGET', KEYS[1], 'beat_at') then
    redis.call('DEL', KEYS[2], KEYS[#KEYS - 1])
    redis.call('HDEL', KEYS[1], 'pending_until', 'pending_missed', 'pending_sent')
  end
  redis.call('HSET', KEYS[1], 'recent', table.concat(keep, ' '))
else
  if not c[1] then return {-1, window} end
  if c[2] ~= ARGV[1] then return {-2, window} end
  local u = redis.call('HGET', KEYS[2], 'until')
  if u and now > tonumber(u) then return {-4, window} end
  if st ~= 'hooked' or ep ~= c[1] then return {-3, window} end
  back, pushes = 0, c[3]
  redis.call('DEL', KEYS[2], KEYS[#KEYS - 1])
  redis.call('HDEL', KEYS[1], 'pending_until', 'pending_missed', 'pending_sent')
end
local n = 0
for p in string.gmatch(pushes, '[^,]+') do
  for i = 4, #ARGV do
    if ARGV[i] == p and redis.call('EXISTS', KEYS[2 * (i - 3) + 1]) == 1 then
      redis.call('SET', KEYS[2 * (i - 3) + 2], '{"epoch":' .. ep .. ',"at":' .. ms .. ',"by":' .. ARGV[3] .. '}', 'PX', ARGV[2])
      n = n + 1
    end
  end
end
redis.call('HSET', KEYS[1], 'misses', 0, 'answered_at', ms)
redis.call('HDEL', KEYS[1], 'renewal_failed')
return {n, back, tonumber(at)}
`)

// unhookLua unhooks name (tla/Hook.tla, Unhook): the epoch moves on, so no
// holder's later write counts, and every lease, proof and challenge is cleared.
// It answers 0 for no record, 1 for one that was hooked, 2 for one unhooked already.
// KEYS: record, then allKeys.
var unhookLua = redis.NewScript(nowMS + `
local st = redis.call('HGET', KEYS[1], 'state')
if not st then return 0 end
redis.call('HINCRBY', KEYS[1], 'epoch', 1)
redis.call('HSET', KEYS[1], 'state', 'unhooked', 'unhooked', ms)
redis.call('HDEL', KEYS[1], 'recent', 'renewal_failed')
for i = 2, #KEYS do redis.call('DEL', KEYS[i]) end
if st == 'hooked' then return 1 end
return 2
`)

// byJSON is who as the JSON string a stamp carries.
func byJSON(who string) string {
	raw, err := json.Marshal(who)
	if err != nil {
		return `""` // ignored: a Go string always marshals; an empty holder is the fallback
	}
	return string(raw)
}

// checkName says why name is no name, "" when it is one: the bus's names,
// lowercase letters, digits and hyphens, at most 64 bytes.
func checkName(name string) string {
	if name == "" || len(name) > 64 {
		return fmt.Sprintf("the name %q wants 1 to 64 bytes of lowercase letters, digits and hyphens", name)
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return fmt.Sprintf("the name %q is not lowercase letters, digits and hyphens", name)
		}
	}
	return ""
}

// ErrOtherRole is Arm's answer for a record hooked as another role: a holder
// never takes a record over; only a fresh arm (nova-hook hook, asked by the
// identity itself) does.
var ErrOtherRole = errors.New("the record is hooked as another role")

// Arm hooks name in as role. Not fresh (a holder's start), it joins a record
// already hooked as that role at its epoch (joined true) and arms only where
// there is no record at all; a record hooked as another role is ErrOtherRole
// and an unhooked one ErrNotHooked, each left as it is, so neither a holder's
// start nor its restart ever undoes the identity's word. Fresh (nova-hook
// hook, the identity's own word) always starts a new epoch. by is who armed
// it, as the record shows it.
func (s Store) Arm(ctx context.Context, name string, role Role, by string, fresh bool) (epoch int64, joined bool, err error) {
	if fresh {
		return s.arm(ctx, name, role, by, armFresh)
	}
	return s.arm(ctx, name, role, by, armHolder)
}

// HookAs is the identity's own word, nova-hook hook: it joins a record hooked
// as role at its epoch (joined true); otherwise (no record, an unhooked one,
// one hooked as another role, which it takes over) it starts a new epoch.
func (s Store) HookAs(ctx context.Context, name string, role Role, by string) (epoch int64, joined bool, err error) {
	return s.arm(ctx, name, role, by, armWord)
}

func (s Store) arm(ctx context.Context, name string, role Role, by, mode string) (epoch int64, joined bool, err error) {
	if p := checkName(name); p != "" {
		return 0, false, errors.New(p)
	}
	if _, err := ParseRole(string(role)); err != nil {
		return 0, false, err
	}
	keys := append([]string{RecordKey(name), NamesKey}, allKeys(name)...)
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := armLua.Run(ctx, s.C, keys, name, string(role), by, mode).Int64Slice()
	if err != nil {
		return 0, false, err
	}
	if len(got) != 2 {
		return 0, false, fmt.Errorf("the arm script answered %v, not an epoch and a join word", got)
	}
	switch got[1] {
	case 2:
		return got[0], false, ErrOtherRole
	case 3:
		return got[0], false, ErrNotHooked
	}
	return got[0], got[1] == 1, nil
}

// Renew renews the lease of name's push p under epoch and role for LeaseTTL;
// ErrNotHooked when the record is not hooked at that epoch as that role.
func (s Store) Renew(ctx context.Context, name string, role Role, p Push, epoch int64, by string) error {
	return s.stamp(ctx, name, role, LiveKey(name, p), LiveKey(name, p), epoch, LeaseTTL, by)
}

// Prove writes the proof of name's push p under epoch and role for ProofTTL:
// a daemon holder's own proof of its round trip. ErrNotHooked when the record
// is not hooked at that epoch as that role; an error naming the lease when p
// is not live.
func (s Store) Prove(ctx context.Context, name string, role Role, p Push, epoch int64, how string) error {
	return s.stamp(ctx, name, role, ProofKey(name, p), LiveKey(name, p), epoch, ProofTTL, how)
}

func (s Store) stamp(ctx context.Context, name string, role Role, key, lease string, epoch int64, ttl time.Duration, by string) error {
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := stampLua.Run(ctx, s.C, []string{RecordKey(name), key, lease}, epoch, string(role), ttl.Milliseconds(), byJSON(by)).Int64()
	switch {
	case err != nil:
		return err
	case got == 0:
		return ErrNotHooked
	case got < 0:
		return fmt.Errorf("%s holds no live lease, so it cannot be proven; its holder renews it first", lease)
	}
	return nil
}

// Unprove clears the proof of name's push p under epoch and role: its
// holder's session stopped answering, so the push is armed and no longer proven.
func (s Store) Unprove(ctx context.Context, name string, role Role, p Push, epoch int64) error {
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := dropLua.Run(ctx, s.C, []string{RecordKey(name), ProofKey(name, p)}, epoch, string(role)).Int64()
	if err == nil && got == 0 {
		return ErrNotHooked
	}
	return err
}

// Sum is the challenge's form of a nonce: what the store keeps in its place.
func Sum(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}

// Challenge bootstraps a challenge for pushes under epoch and role
// (tla/HookBeat.tla, Bootstrap). It returns false without writing if a
// challenge stands or the heartbeat owns this epoch, including after Answer.
// Only a created challenge's nonce is delivered; a skipped client waits for
// the heartbeat's next delivery. A bootstrap lasts AnswerBound.
func (s Store) Challenge(ctx context.Context, name string, role Role, epoch int64, pushes []Push, nonce string) (created bool, err error) {
	var ps []string
	for _, p := range pushes {
		ps = append(ps, string(p))
	}
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := challengeLua.Run(ctx, s.C, []string{RecordKey(name), ChallengeKey(name)}, epoch, string(role), Sum(nonce), strings.Join(ps, ","), AnswerBound.Milliseconds()).Int64()
	if err == nil && got == 0 {
		return false, ErrNotHooked
	}
	return got == 1 && err == nil, err
}

// The refusals of Answer, by the script's code.
var (
	ErrNoChallenge = errors.New("no challenge stands for this name: its hook has not asked one, or it lapsed past its bound; read the hook's stream for the next HOOK CHALLENGE")
	ErrWrongNonce  = errors.New("the nonce is not the one the challenge asked, nor one the heartbeat issued to this name in this epoch within its down-after window and not yet answered; copy it from the newest HOOK CHALLENGE line of the hook's stream")
	ErrExpired     = errors.New("the heartbeat challenge is past its interval and its window: it counts as missed; answer the next one, which the heartbeat server sends within the interval")
)

// RecentMax bounds the challenges a name's record keeps for its proof window
// (the record's recent): a pass keeps the ones the down-after window can hold
// at one pass per nine tenths of an interval, never more than this.
const RecentMax = 64

// Proof is what an answer proved and which challenge it matched.
type Proof struct {
	Proven int // the pushes proven
	// Back is which challenge the nonce matched: 0 the newest the heartbeat
	// issued (or the standing challenge), k the one k challenges before it.
	Back int
	// Issued is when the heartbeat issued the matched challenge, by the
	// store's TIME; zero for a challenge the record's recent does not hold
	// (a bootstrap's).
	Issued time.Time
}

// Answer answers name's challenge with nonce: every push it named whose lease
// is live is proven for ProofTTL. It answers how many were proven.
func (s Store) Answer(ctx context.Context, name, nonce, by string) (int, error) {
	p, err := s.AnswerProof(ctx, name, nonce, by)
	return p.Proven, err
}

// AnswerProof answers name's challenge with nonce (answerLua): the newest
// challenge or any the heartbeat issued to name in its epoch within the
// down-after window and not yet spent (tla/HookBeat.tla, AnswerIssued and
// ProofNamesIssuedInWindow). The answer counts at the time of the proof.
// A refusal of another nonce names the server's window.
func (s Store) AnswerProof(ctx context.Context, name, nonce, by string) (Proof, error) {
	keys := []string{RecordKey(name), ChallengeKey(name)}
	args := []any{Sum(nonce), ProofTTL.Milliseconds(), byJSON(by)}
	for _, p := range Pushes {
		keys = append(keys, LiveKey(name, p), ProofKey(name, p))
		args = append(args, string(p))
	}
	keys = append(keys, DeliveryKey(name), ServeKey)
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := answerLua.Run(ctx, s.C, keys, args...).Int64Slice()
	if err != nil {
		return Proof{}, err
	}
	if len(got) == 2 && got[0] < 0 {
		window := "none: no heartbeat server row"
		if got[1] > 0 {
			window = (time.Duration(got[1]) * time.Millisecond).String()
		}
		switch got[0] {
		case -1:
			return Proof{}, ErrNoChallenge
		case -2:
			return Proof{}, fmt.Errorf("%w (the window: %s)", ErrWrongNonce, window)
		case -3:
			return Proof{}, ErrNotHooked
		case -4:
			return Proof{}, fmt.Errorf("%w (the window: %s)", ErrExpired, window)
		}
	}
	if len(got) != 3 || got[0] < 0 {
		return Proof{}, fmt.Errorf("the answer script answered %v, not a count, a match and an issue time", got)
	}
	return Proof{Proven: int(got[0]), Back: int(got[1]), Issued: msTime(strconv.FormatInt(got[2], 10))}, nil
}

// Unhook unhooks name: was is StateNone when there was no record,
// StateUnhooked when it was unhooked already, else "hooked".
func (s Store) Unhook(ctx context.Context, name string) (was string, err error) {
	if p := checkName(name); p != "" {
		return "", errors.New(p)
	}
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := unhookLua.Run(ctx, s.C, append([]string{RecordKey(name)}, allKeys(name)...)).Int64()
	if err != nil {
		return "", err
	}
	return map[int64]string{0: StateNone, 1: "hooked", 2: StateUnhooked}[got], nil
}

// Read is each name's status at the store's now, in one round trip (a
// pipeline: the record, each push's lease and proof with their TTLs, the TIME).
func (s Store) Read(ctx context.Context, names ...string) ([]Status, time.Time, error) {
	type reads struct {
		rec         *redis.MapStringStringCmd
		live, proof []*redis.StringCmd
		liveLeft    []*redis.DurationCmd
		proofLeft   []*redis.DurationCmd
	}
	all := make([]reads, len(names))
	var clock *redis.TimeCmd
	var served *redis.MapStringStringCmd
	ctx, cancel := s.call(ctx)
	defer cancel()
	_, err := s.C.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, n := range names {
			r := &all[i]
			r.rec = p.HGetAll(ctx, RecordKey(n))
			for _, push := range Pushes {
				r.live = append(r.live, p.Get(ctx, LiveKey(n, push)))
				r.liveLeft = append(r.liveLeft, p.PTTL(ctx, LiveKey(n, push)))
				r.proof = append(r.proof, p.Get(ctx, ProofKey(n, push)))
				r.proofLeft = append(r.proofLeft, p.PTTL(ctx, ProofKey(n, push)))
			}
		}
		clock, served = p.Time(ctx), p.HGetAll(ctx, ServeKey)
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, time.Time{}, err
	}
	now, err := clock.Result()
	if err != nil {
		return nil, time.Time{}, err
	}
	out := make([]Status, len(names))
	for i, n := range names {
		r := all[i]
		rec, err := r.rec.Result()
		if err != nil {
			return nil, time.Time{}, err
		}
		var prs []PushRead
		for j := range Pushes {
			prs = append(prs, PushRead{Live: r.live[j].Val(), Proof: r.proof[j].Val(), LiveLeft: max(r.liveLeft[j].Val(), 0), ProofLeft: max(r.proofLeft[j].Val(), 0)})
		}
		out[i] = Judge(n, rec, prs)
		out[i].Down = servedOf(served.Val(), now.UTC()).Down(out[i])
	}
	return out, now.UTC(), nil
}

// Names is every name that ever hooked in, sorted.
func (s Store) Names(ctx context.Context) ([]string, error) {
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := s.C.SMembers(ctx, NamesKey).Result()
	if err != nil {
		return nil, err
	}
	slices.Sort(got)
	return got, nil
}

// Gate reads w.Name's record and says whether w may act: nil, a *Refusal with
// the breadcrumb, or an error of the store. Fail closed: a caller acts only on
// nil; a store that does not answer is a refusal of its own, never a pass.
func (s Store) Gate(ctx context.Context, w Want) error {
	if p := checkName(w.Name); p != "" {
		return errors.New(p)
	}
	st, _, err := s.Read(ctx, w.Name)
	if err != nil {
		return fmt.Errorf("the hook record of %s could not be read, so nothing runs (fail closed): %w", w.Name, err)
	}
	if r := st[0].Gate(w); r != nil {
		return r
	}
	return nil
}

// beatLua performs one atomic server pass (tla/HookBeat.tla, Pass). The
// server row prevents two passes in one interval. Each hooked bus role gets
// a challenge only after its selected quiet window or when proof renewal is
// due. A valid pending nonce is retained; its outbox is retried until the bus
// confirms delivery. Expiry invalidates its recent entry and latches failed
// renewal until silence or a fresh model proof permits another challenge.
// Only a delivered expired challenge plus activity older than down-after
// unhooks a name and logs the transition. Activity never creates a proof.
// KEYS: events, serve, then each name's record, six push keys, challenge and
// delivery outbox. ARGV: every, down-after, key TTL, by, event cap, reason
// label, role count and role/push pairs, then name, sum, busy flag and nonce.
var beatLua = redis.NewScript(nowMS + `
local now = tonumber(ms)
local every, after, ttl, by, cap, label = tonumber(ARGV[1]), tonumber(ARGV[2]), ARGV[3], ARGV[4], ARGV[5], ARGV[6]
local dry = ARGV[#ARGV] == '1'
local prev = tonumber(redis.call('HGET', KEYS[2], 'at') or '0')
if prev > 0 and now - prev < every * 9 / 10 then return {0, {}, '', '', '', '', '', '', '', '', ''} end
if not dry then redis.call('HSET', KEYS[2], 'at', ms, 'every', ARGV[1], 'down_after', ARGV[2], 'by', by) end
local nroles = tonumber(ARGV[7])
local keep = math.min(math.floor(after * 10 / (9 * every)) + 1, ` + strconv.Itoa(RecentMax) + `)
local bus = {}
for i = 1, nroles do bus[ARGV[6 + 2 * i]] = ARGV[7 + 2 * i] end
local out, gone, live, coord = {}, {}, {}, ''
local k, a = 3, 8 + 2 * nroles
while k <= #KEYS do
  local rec, chal, delivery, name, sum = KEYS[k], KEYS[k + 7], KEYS[k + 8], ARGV[a], ARGV[a + 1]
  local deliveredNonce = ''
  local did, misses, e = 0, 0, 0
  local role = redis.call('HGET', rec, 'role')
  if redis.call('HGET', rec, 'state') == 'hooked' and bus[role] then
    local last = tonumber(redis.call('HGET', rec, 'beat_at') or '0')
    misses = tonumber(redis.call('HGET', rec, 'misses') or '0')
    local pending = tonumber(redis.call('HGET', rec, 'pending_until') or '0')
    -- A pre-upgrade outstanding challenge keeps its own issued deadline.
    if pending == 0 and last > 0 then pending = tonumber(redis.call('HGET', chal, 'until') or '0') end
    -- A dry run (the plan) computes all of this and writes none of it.
    local once = redis.call('HGET', delivery, 'once')
    local sent = redis.call('HGET', rec, 'pending_sent') == '1'
    if not sent and once and redis.call('EXISTS', once) == 1 then
      sent = true
      if not dry then redis.call('HSET', rec, 'pending_sent', '1') end
    end
    local miss = pending > 0 and now > pending and sent
    if miss and redis.call('HGET', rec, 'pending_missed') ~= '1' then
      misses = misses + 1
      if not dry then redis.call('HSET', rec, 'misses', misses, 'pending_missed', '1') end
    end
    local answer = tonumber(redis.call('HGET', rec, 'answered_at') or '0')
    local armed = tonumber(redis.call('HGET', rec, 'armed') or '0')
    local heard = math.max(answer, armed, tonumber(redis.call('HGET', rec, 'bus_sent_at') or '0'))
    local quiet = ` + strconv.FormatInt(QuietAfter.Milliseconds(), 10) + `
    if role == 'coordinator' then quiet = ` + strconv.FormatInt(CoordinatorEvery.Milliseconds(), 10) + `
    elseif role == 'friend' and ARGV[a + 2] == '1' then quiet = ` + strconv.FormatInt(BusyQuietAfter.Milliseconds(), 10) + ` end
    if not dry then redis.call('HSET', rec, 'quiet_after', quiet) end
    local proofDue = now - math.max(answer, armed) >= ` + strconv.FormatInt((ProofTTL-AnswerBound).Milliseconds(), 10) + ` - 2 * every and redis.call('HGET', rec, 'renewal_failed') ~= '1'
    if miss and now - heard > after then
      did = 2
      if dry then
        e = tonumber(redis.call('HGET', rec, 'epoch') or '0') + 1
      else
        e = redis.call('HINCRBY', rec, 'epoch', 1)
        local why = 'no answer in ' .. label .. ' (' .. misses .. ' challenges missed)'
        redis.call('HSET', rec, 'state', 'unhooked', 'unhooked', ms, 'reason', why, 'misses', misses)
        redis.call('HDEL', rec, 'recent', 'pending_until', 'pending_missed', 'pending_sent', 'renewal_failed')
        for i = k + 1, k + 8 do redis.call('DEL', KEYS[i]) end
        redis.call('XADD', KEYS[1], 'MAXLEN', '~', cap, '*', 'name', name, 'event', 'unhooked', 'reason', why, 'role', role, 'epoch', e, 'at', ms, 'by', by)
      end
    else
      -- An expired challenge cannot be answered alongside its replacement.
      -- Activity may keep a name hooked, but never extends this nonce or proof.
      if pending > 0 and now > pending then
        if not dry then
          redis.call('DEL', chal, delivery)
          redis.call('HDEL', rec, 'recent', 'pending_until', 'pending_missed', 'pending_sent')
          redis.call('HSET', rec, 'renewal_failed', '1')
        end
        proofDue = false
        pending = 0
      end
      if pending > 0 and not sent then
        deliveredNonce = redis.call('HGET', delivery, 'nonce') or ''
        if deliveredNonce ~= '' then
          did = 1
          e = tonumber(redis.call('HGET', rec, 'epoch'))
        end
      elseif pending == 0 and redis.call('EXISTS', chal) == 0 and (now - heard > quiet or proofDue) then
        e = tonumber(redis.call('HGET', rec, 'epoch'))
        local recent = {string.format('%d:%s:%s:%d:%s', e, sum, ms, now + after, bus[role])}
        for x in string.gmatch(redis.call('HGET', rec, 'recent') or '', '%S+') do
          if #recent >= keep then break end
          table.insert(recent, x)
        end
        local deadline = now + ` + strconv.FormatInt(AnswerBound.Milliseconds(), 10) + `
        deliveredNonce = ARGV[a + 3]
        if not dry then
          redis.call('HSET', rec, 'misses', misses, 'beat_at', ms, 'pending_until', string.format('%d', deadline), 'recent', table.concat(recent, ' '))
          redis.call('HDEL', rec, 'pending_missed', 'pending_sent', 'renewal_failed')
          redis.call('DEL', chal)
          redis.call('HSET', chal, 'epoch', e, 'sum', sum, 'pushes', bus[role], 'until', string.format('%d', deadline))
          redis.call('PEXPIRE', chal, ttl)
          redis.call('HSET', delivery, 'nonce', deliveredNonce, 'epoch', e, 'once', delivery .. ':' .. e .. ':' .. sum)
        end
        did = 1
      end
    end
  end
  table.insert(out, {did, misses, e, deliveredNonce})
  local st = redis.call('HGET', rec, 'state')
  if did == 2 then st = 'unhooked' end -- what the unhook above wrote, or a dry run would
  if st == 'unhooked' then table.insert(gone, name) end
  if st == 'hooked' and bus[role] then
    table.insert(live, name)
    if role == 'coordinator' and coord == '' then coord = name end
  end
  k, a = k + 9, a + 4
end
local set = table.concat(gone, ',')
local f = redis.call('HMGET', KEYS[2], 'told:coordinator', 'coord:coordinator', 'gen:coordinator', 'told:rest', 'coord:rest', 'gen:rest')
return {1, out, set, coord, table.concat(live, ','), f[1] or '', f[2] or '', f[3] or '0', f[4] or '', f[5] or '', f[6] or '0'}
`)

// deliveredLua marks only the pending payload that actually reached the bus;
// a failed or unknown call retries the same once-key (tla/HookBeat.tla, Deliver).
var deliveredLua = redis.NewScript(`
if redis.call('HGET', KEYS[2], 'nonce') ~= ARGV[1] or redis.call('HGET', KEYS[2], 'epoch') ~= ARGV[2] then return 0 end
redis.call('HSET', KEYS[1], 'pending_sent', '1')
redis.call('DEL', KEYS[2])
return 1
`)

// Delivered confirms the bus accepted a challenge, without extending its deadline.
func (s Store) Delivered(ctx context.Context, beat Beat) error {
	ctx, cancel := s.call(ctx)
	defer cancel()
	return deliveredLua.Run(ctx, s.C, []string{RecordKey(beat.Name), DeliveryKey(beat.Name)}, beat.Nonce, beat.Epoch).Err()
}

// What one heartbeat pass did to one name.
const (
	BeatLeft       = 0 // not hooked, no bus push, or challenged already this interval
	BeatChallenged = 1 // a fresh challenge is set: deliver its nonce
	BeatUnhooked   = 2 // a miss with no answer for down-after: unhooked
)

// Beat is one name's outcome of a heartbeat pass.
type Beat struct {
	Name   string
	Did    int    // BeatLeft, BeatChallenged or BeatUnhooked
	Nonce  string // pending payload; the private outbox retains it until delivery
	Misses int
	Epoch  int64
	// DownAfter is the pass's down-after: the silence that unhooks.
	DownAfter time.Duration
}

// Reason is why an unhooked beat unhooked its name, as the record and the event say it.
func (b Beat) Reason() string { return DownReason(b.DownAfter, b.Misses) }

// DownReason is the reason a heartbeat unhook records: no answer for
// downAfter, and the challenges missed in a row by then.
func DownReason(downAfter time.Duration, misses int) string {
	return fmt.Sprintf("no answer in %s (%d challenges missed)", downAfter, misses)
}

// The audiences a pass tells, each marked told on its own (Store.Told): the
// coordinator, who gets the judgment, and the rest of the live names.
const (
	ToCoordinator = "coordinator"
	ToRest        = "rest"
)

// Audiences is every audience, in the order the pass reply carries them.
var Audiences = []string{ToCoordinator, ToRest}

// Passed is what one heartbeat pass did and found.
type Passed struct {
	Taken    bool     // false: another pass (another server's) was taken inside nine tenths of the interval; nothing was done
	Beats    []Beat   // one per name, in the order asked
	Unhooked []string // every name unhooked now, sorted
	Coord    string   // the coordinator now: the first hooked name of that role, "" for none
	Live     []string // every hooked name of a challenged role: who hears a broadcast
	Was      map[string]Told
}

// Told is what an audience was last told: the unhooked set and the
// coordinator, and the generation of that telling.
type Told struct {
	Unhooked []string
	Coord    string
	Gen      int64
}

// ToldMark is one field of the server row (ServeKey) an audience's message
// writes in its own atomic step.
type ToldMark struct{ Field, Value string }

// Telling is what an audience's message for p carries so that the message and
// its told mark are one write (tla/HookBeat.tla, NeverToldTwice and
// EveryTransitionToldAtLeastOnce): the once-key of this telling, the
// audience's generation, which a later send under it finds and writes
// nothing; and the marks: what it was told, and the next generation.
func (p Passed) Telling(audience string) (once string, marks []ToldMark) {
	w := p.Was[audience]
	return fmt.Sprintf("%stold:%s:%d", prefix, audience, w.Gen), []ToldMark{
		{"told:" + audience, strings.Join(p.Unhooked, ",")},
		{"coord:" + audience, p.Coord},
		{"gen:" + audience, strconv.FormatInt(w.Gen+1, 10)},
	}
}

// Diff is what an audience has not been told: the names down and back since,
// and the coordinator it was told.
func (p Passed) Diff(audience string) (down, back []string, was string) {
	t := p.Was[audience]
	for _, n := range p.Unhooked {
		if !slices.Contains(t.Unhooked, n) {
			down = append(down, n)
		}
	}
	for _, n := range t.Unhooked {
		if !slices.Contains(p.Unhooked, n) {
			back = append(back, n)
		}
	}
	return down, back, t.Coord
}

// Changed is whether an audience has anything to be told.
func (p Passed) Changed(audience string) bool {
	down, back, was := p.Diff(audience)
	return len(down)+len(back) > 0 || was != p.Coord
}

// toldLua marks an audience told with no message, for an audience with no
// one to tell: only while its generation is still the one the pass read.
// KEYS: serve. ARGV: audience, the generation read, then field, value pairs.
var toldLua = redis.NewScript(`
if (redis.call('HGET', KEYS[1], 'gen:' .. ARGV[1]) or '0') ~= ARGV[2] then return 0 end
for i = 3, #ARGV, 2 do redis.call('HSET', KEYS[1], ARGV[i], ARGV[i + 1]) end
return 1
`)

// Told marks an audience told what p found when there is no one to send to.
func (s Store) Told(ctx context.Context, p Passed, audience string) error {
	_, marks := p.Telling(audience)
	args := []any{audience, strconv.FormatInt(p.Was[audience].Gen, 10)}
	for _, m := range marks {
		args = append(args, m.Field, m.Value)
	}
	ctx, cancel := s.call(ctx)
	defer cancel()
	return toldLua.Run(ctx, s.C, []string{ServeKey}, args...).Err()
}

// Sent says whether a once-key's send went: the key stands.
func (s Store) Sent(ctx context.Context, once string) (bool, error) {
	ctx, cancel := s.call(ctx)
	defer cancel()
	n, err := s.C.Exists(ctx, once).Result()
	return n == 1, err
}

// The friend status the coordinator is pushed (tla/HookBeat.tla, PushSend):
// for each name the state last pushed and the count of pushes, one field of
// the server row, written in the same atomic step as the push's message.
const pushedField = "pushed:"

// StateDown is a proven record that reads down at this read (Served.Down: no
// answer within the window, or no server), the state nova-hook friends shows
// as down; every other state is the record's own.
const StateDown = "down"

// PushState is the state of st a friend-status push names: the record's state,
// or down for a proven one the heartbeat's evidence does not stand behind.
func PushState(st Status) string {
	if st.Down != "" && st.State == StateProven {
		return StateDown
	}
	return st.State
}

// Pushed is what was last pushed for one name: its state and how many pushes went.
type Pushed struct {
	State string
	Gen   int64
}

// PushOnce is the once-key of a name's gen-th push, naming its epoch and the
// state it pushes (tla/HookBeat.tla, NeverPushedTwice): the count makes a
// flap back to a state in the same epoch a push of its own.
func PushOnce(name string, epoch int64, state string, gen int64) string {
	return fmt.Sprintf("%spush:%s:%d:%s:%d", prefix, name, epoch, state, gen)
}

// PushMark is the server-row mark a push of state, the gen-th, writes in the
// same atomic step as its message.
func PushMark(name, state string, gen int64) ToldMark {
	return ToldMark{pushedField + name, state + ":" + strconv.FormatInt(gen+1, 10)}
}

// Pushed reads what was last pushed for each name, and whether any push or
// baseline was ever written (false: the first pass of a fresh row).
func (s Store) Pushed(ctx context.Context) (map[string]Pushed, bool, error) {
	ctx, cancel := s.call(ctx)
	defer cancel()
	row, err := s.C.HGetAll(ctx, ServeKey).Result()
	if err != nil {
		return nil, false, err
	}
	out := map[string]Pushed{}
	for f, v := range row {
		name, ok := strings.CutPrefix(f, pushedField)
		if !ok {
			continue
		}
		st, gen, _ := strings.Cut(v, ":")     // ignored: a mark with no count reads as the first
		n, _ := strconv.ParseInt(gen, 10, 64) // ignored: a mark with no count is the first
		out[name] = Pushed{State: st, Gen: n}
	}
	return out, len(out) > 0, nil
}

// Baseline writes the states of the first pass as pushed, with no message: a
// server row with no pushed mark would else push every name at once. A mark
// already there stays (HSETNX).
func (s Store) Baseline(ctx context.Context, states map[string]string) error {
	ctx, cancel := s.call(ctx)
	defer cancel()
	_, err := s.C.Pipelined(ctx, func(p redis.Pipeliner) error {
		for n, st := range states {
			p.HSetNX(ctx, ServeKey, pushedField+n, st+":0")
		}
		return nil
	})
	return err
}

// Challenged is the pushes a heartbeat challenge names for each role a session
// answers in: the bus push. The sprint push is the seat hook's own
// (nova-sprint run), and a member's or reader's loop has no session: neither
// is challenged.
var Challenged = map[Role][]Push{Coordinator: {Bus}, Friend: {Bus}, Comms: {Bus}}

// Pass applies the silence/proof-renewal rule in one store round trip
// (tla/HookBeat.tla, Pass). busy names come from the authoritative sprint
// ready/working rows; omitted names use the idle policy. Beats with a nonce
// require delivery, including retry of a prepared but undelivered nonce.
func (s Store) Pass(ctx context.Context, names []string, every, downAfter time.Duration, nonce func() string, by string, busy ...string) (Passed, error) {
	return s.pass(ctx, names, every, downAfter, nonce, by, false, busy)
}

// PlanPass is what Pass would do now, from the same script, writing nothing
// (nova-hook serve --dry-run): the same Passed, with no nonce on any beat, as
// no challenge is set. A plan of a pass inside another's interval is not taken.
func (s Store) PlanPass(ctx context.Context, names []string, every, downAfter time.Duration, by string, busy ...string) (Passed, error) {
	p, err := s.pass(ctx, names, every, downAfter, func() string { return "" }, by, true, busy)
	for i := range p.Beats {
		p.Beats[i].Nonce = ""
	}
	return p, err
}

// pass is Pass, or with dry its plan: the script's last argument says which.
func (s Store) pass(ctx context.Context, names []string, every, downAfter time.Duration, nonce func() string, by string, dry bool, busy []string) (Passed, error) {
	keys := []string{EventsKey, ServeKey}
	args := []any{every.Milliseconds(), downAfter.Milliseconds(), (2*every + AnswerBound).Milliseconds(), by, EventsMax, downAfter.String(), len(Challenged)}
	for _, r := range Roles {
		if ps, ok := Challenged[r]; ok {
			var w []string
			for _, p := range ps {
				w = append(w, string(p))
			}
			args = append(args, string(r), strings.Join(w, ","))
		}
	}
	out := Passed{Beats: make([]Beat, len(names))}
	for i, n := range names {
		out.Beats[i] = Beat{Name: n, Nonce: nonce(), DownAfter: downAfter}
		keys = append(append(keys, RecordKey(n)), allKeys(n)...)
		active := "0"
		if slices.Contains(busy, n) {
			active = "1"
		}
		args = append(args, n, Sum(out.Beats[i].Nonce), active, out.Beats[i].Nonce)
	}
	args = append(args, map[bool]string{false: "0", true: "1"}[dry]) // ARGV[#ARGV]: a dry run writes nothing
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := beatLua.Run(ctx, s.C, keys, args...).Slice()
	if err != nil {
		return Passed{}, err
	}
	if len(got) != 11 {
		return Passed{}, fmt.Errorf("the beat script answered %v, not a taken word, the names and nine values", got)
	}
	if t, _ := got[0].(int64); t != 1 { // ignored: a word that is no number reads as not taken
		return Passed{}, nil
	}
	rows, _ := got[1].([]any) // ignored: checked by the count below
	if len(rows) != len(names) {
		return Passed{}, fmt.Errorf("the beat script answered %d names, not %d", len(rows), len(names))
	}
	for i, row := range rows {
		v, ok := row.([]any)
		if !ok || len(v) != 4 {
			return Passed{}, fmt.Errorf("the beat script answered %v for %s, not a code, a count and an epoch", row, names[i])
		}
		b := &out.Beats[i]
		out.Beats[i].Nonce, _ = v[3].(string) // ignored: no delivery payload means no delivery
		did, _ := v[0].(int64)                // ignored: a code that is no number reads as left alone
		misses, _ := v[1].(int64)             // ignored: as above
		b.Did, b.Misses = int(did), int(misses)
		b.Epoch, _ = v[2].(int64) // ignored: as above
		if b.Did != BeatChallenged {
			b.Nonce = ""
		}
	}
	out.Taken = true
	str := func(i int) string { v, _ := got[i].(string); return v } // ignored: no string is an empty set
	list := func(v string) []string {
		if v == "" {
			return nil
		}
		return strings.Split(v, ",")
	}
	out.Unhooked, out.Coord, out.Live = list(str(2)), str(3), list(str(4))
	out.Was = map[string]Told{}
	for i, a := range Audiences {
		gen, _ := strconv.ParseInt(str(7+3*i), 10, 64) // ignored: no generation is the first
		out.Was[a] = Told{Unhooked: list(str(5 + 3*i)), Coord: str(6 + 3*i), Gen: gen}
	}
	return out, nil
}

// Served is the heartbeat server's stamp as the store holds it: when it last
// passed and its interval, with the store's now; ok false when no server ever passed.
type Served struct {
	At, Now time.Time
	Every   time.Duration
	// DownAfter is the server's down-after (nova-hook serve --down-after):
	// the window a name's last answer must fall in to read up. A row from a
	// server that writes none reads as two intervals, that server's own rule.
	DownAfter time.Duration
	OK        bool
}

// Why a hooked record reads down although its state says otherwise: up needs
// evidence, a heartbeat server passing and the name's own answer.
const (
	NeverRanReason = "hook server never ran"
	StaleReason    = "hook server stale"
	NoAnswerReason = "no answer to a heartbeat challenge within the window"
)

// Stale is whether the heartbeat server gives no evidence at this read: it
// never passed, or not for more than two of its intervals by the store's TIME.
func (sv Served) Stale() bool { return !sv.OK || sv.Now.Sub(sv.At) > 2*sv.Every }

// Down is the shared reader rule (tla/HookBeat.tla, ActivityUp): a missing
// or stale server is down. A challenged role needs an initial nonce answer;
// afterward a delivered expired challenge with old activity reads down.
// The independent reader backstop also bounds silence when delivery is lost;
// quiet eligibility alone never changes reader status.
// The separate proof lease gate remains mandatory.
func (sv Served) Down(st Status) string {
	switch {
	case !sv.OK:
		return NeverRanReason
	case sv.Stale():
		return StaleReason
	}
	if _, ok := Challenged[st.Role]; ok {
		if st.AnswerAt.IsZero() {
			return NoAnswerReason
		}
		if sv.Now.Sub(st.LastActivity()) > st.QuietWindow()+AnswerBound+sv.Window()+2*sv.Every {
			return NoAnswerReason
		}
		if st.PendingSent && !st.PendingUntil.IsZero() {
			if sv.Now.After(st.PendingUntil) && sv.Now.Sub(st.LastActivity()) > sv.Window() {
				return NoAnswerReason
			}
		}
	}
	return ""
}

// Window is how old a name's last answer may be and the name read up: the
// server's down-after, or two intervals for a row that carries none.
func (sv Served) Window() time.Duration {
	if sv.DownAfter > 0 {
		return sv.DownAfter
	}
	return 2 * sv.Every
}

// Serve reads the heartbeat server's stamp and the store's TIME, in one round trip.
func (s Store) Serve(ctx context.Context) (Served, error) {
	ctx, cancel := s.call(ctx)
	defer cancel()
	var rec *redis.MapStringStringCmd
	var clock *redis.TimeCmd
	if _, err := s.C.Pipelined(ctx, func(p redis.Pipeliner) error {
		rec, clock = p.HGetAll(ctx, ServeKey), p.Time(ctx)
		return nil
	}); err != nil {
		return Served{}, err
	}
	return servedOf(rec.Val(), clock.Val().UTC()), nil
}

// servedOf is the server row m as read at the store's now.
func servedOf(m map[string]string, now time.Time) Served {
	every, err := strconv.ParseInt(m["every"], 10, 64)
	if err != nil || every <= 0 {
		return Served{Now: now} // ignored: no stamp, or one with no interval, is no server
	}
	after, err := strconv.ParseInt(m["down_after"], 10, 64)
	if err != nil || after <= 0 {
		after = 0 // ignored: a row with no down-after is an older server's: Window reads two intervals
	}
	return Served{At: msTime(m["at"]), Now: now, Every: time.Duration(every) * time.Millisecond, DownAfter: time.Duration(after) * time.Millisecond, OK: true}
}

// JudgmentsKey holds the heartbeat's open judgments: a hash of message id to
// the judgment (JSON), each open until one of its recipients answers it (the
// bus's answers record, nova-bus send --re <id>), re-pushed as OVERDUE at most
// once past its due (tla/HookBeat.tla, Judge).
const JudgmentsKey = prefix + "judgments"

// Judgment is one open judgment: the bus message that raised it.
type Judgment struct {
	ID      string    `json:"id"`
	To      []string  `json:"to"`
	Subject string    `json:"subject"`
	Due     time.Time `json:"due"`
	Overdue bool      `json:"overdue"` // re-pushed once already: never again
}

// openLua stores a judgment with its due at the store's TIME plus ARGV[2] ms,
// and logs it. KEYS: judgments, events. ARGV: id, within ms, to (comma), subject, events cap.
var openLua = redis.NewScript(nowMS + `
local due = string.format('%d', tonumber(ms) + tonumber(ARGV[2]))
local to = {}
for n in string.gmatch(ARGV[3], '[^,]+') do table.insert(to, n) end
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode({id = ARGV[1], to = to, subject = ARGV[4], due_ms = tonumber(due), overdue = false}))
redis.call('XADD', KEYS[2], 'MAXLEN', '~', ARGV[5], '*', 'event', 'judgment', 'id', ARGV[1], 'to', ARGV[3], 'subject', ARGV[4], 'at', ms)
return 1
`)

// stored is a judgment as the hash holds it.
type stored struct {
	ID      string   `json:"id"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	DueMS   int64    `json:"due_ms"`
	Overdue bool     `json:"overdue"`
}

// OpenJudgment records j (its ID the message's) as open, due within of the store's now.
func (s Store) OpenJudgment(ctx context.Context, j Judgment, within time.Duration) error {
	ctx, cancel := s.call(ctx)
	defer cancel()
	return openLua.Run(ctx, s.C, []string{JudgmentsKey, EventsKey}, j.ID, within.Milliseconds(), strings.Join(j.To, ","), j.Subject, EventsMax).Err()
}

// Judgments is every open judgment and the store's now, in one round trip.
func (s Store) Judgments(ctx context.Context) ([]Judgment, time.Time, error) {
	ctx, cancel := s.call(ctx)
	defer cancel()
	var all *redis.MapStringStringCmd
	var clock *redis.TimeCmd
	if _, err := s.C.Pipelined(ctx, func(p redis.Pipeliner) error {
		all, clock = p.HGetAll(ctx, JudgmentsKey), p.Time(ctx)
		return nil
	}); err != nil {
		return nil, time.Time{}, err
	}
	var out []Judgment
	for _, raw := range all.Val() {
		var v stored
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue // ignored: a value that is no judgment is nothing to answer
		}
		out = append(out, Judgment{ID: v.ID, To: v.To, Subject: v.Subject, Due: time.UnixMilli(v.DueMS).UTC(), Overdue: v.Overdue})
	}
	slices.SortFunc(out, func(a, b Judgment) int { return strings.Compare(a.ID, b.ID) })
	return out, clock.Val().UTC(), nil
}

// OverdueMark is j's re-push's once-key and the mark that records it
// re-pushed (a field of JudgmentsKey), written with the re-push in one step,
// so it is re-pushed at most once.
func (j Judgment) OverdueMark() (once, field, value string, err error) {
	raw, err := json.Marshal(stored{ID: j.ID, To: j.To, Subject: j.Subject, DueMS: j.Due.UnixMilli(), Overdue: true})
	return prefix + "overdue:" + j.ID, j.ID, string(raw), err
}

// Answered says whether any of j's recipients replied to it: the bus's
// answers record (bus2:answers:<name>, field the id replied to), in one round trip.
func (s Store) Answered(ctx context.Context, j Judgment) (bool, error) {
	ctx, cancel := s.call(ctx)
	defer cancel()
	cmds := make([]*redis.BoolCmd, len(j.To))
	if _, err := s.C.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, n := range j.To {
			cmds[i] = p.HExists(ctx, "bus2:answers:"+n, j.ID)
		}
		return nil
	}); err != nil {
		return false, err
	}
	for _, c := range cmds {
		if c.Val() {
			return true, nil
		}
	}
	return false, nil
}

// Close ends an answered judgment, and logs it.
func (s Store) Close(ctx context.Context, j Judgment) error {
	ctx, cancel := s.call(ctx)
	defer cancel()
	_, err := s.C.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HDel(ctx, JudgmentsKey, j.ID)
		p.XAdd(ctx, &redis.XAddArgs{Stream: EventsKey, MaxLen: EventsMax, Approx: true, Values: map[string]any{"event": "answered", "id": j.ID, "subject": j.Subject}})
		return nil
	})
	return err
}

// Lost is a client's report of an episode it could not reach the server: from
// and until by its own clock, logged once on hook:events when it reaches the server again.
func (s Store) Lost(ctx context.Context, name string, from, until time.Time) error {
	ctx, cancel := s.call(ctx)
	defer cancel()
	return s.C.XAdd(ctx, &redis.XAddArgs{Stream: EventsKey, MaxLen: EventsMax, Approx: true, Values: map[string]any{
		"event": "not-connected", "name": name, "from": from.Format(time.RFC3339), "until": until.Format(time.RFC3339)}}).Err()
}
