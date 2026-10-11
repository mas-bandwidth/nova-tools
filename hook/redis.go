package hook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	return append(keys, ChallengeKey(name))
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
redis.call('HDEL', KEYS[1], 'unhooked')
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

// challengeLua sets name's challenge (tla/Hook.tla, Challenge): the nonce's
// sum, never the nonce, so only a reader of the hook's stream can answer.
// KEYS: record, challenge. ARGV: epoch, role, sum, pushes (comma), ttl ms.
var challengeLua = redis.NewScript(`
if not (` + held + `) then return 0 end
redis.call('DEL', KEYS[2])
redis.call('HSET', KEYS[2], 'epoch', ARGV[1], 'sum', ARGV[3], 'pushes', ARGV[4])
redis.call('PEXPIRE', KEYS[2], ARGV[5])
return 1
`)

// answerLua answers name's challenge (tla/Hook.tla, Answer): with the nonce's
// sum, under the record's epoch, it proves every push the challenge named whose
// lease is live, and clears the challenge. It answers {proven} or {code}: -1 no
// challenge stands, -2 another nonce, -3 the record moved since.
// KEYS: record, challenge, then per push of Pushes its lease and proof.
// ARGV: sum, ttl ms, by (JSON), then the names of Pushes.
var answerLua = redis.NewScript(nowMS + `
local c = redis.call('HMGET', KEYS[2], 'epoch', 'sum', 'pushes')
if not c[1] then return {-1} end
if c[2] ~= ARGV[1] then return {-2} end
if redis.call('HGET', KEYS[1], 'state') ~= 'hooked' or redis.call('HGET', KEYS[1], 'epoch') ~= c[1] then return {-3} end
local n = 0
for p in string.gmatch(c[3], '[^,]+') do
  for i = 4, #ARGV do
    if ARGV[i] == p and redis.call('EXISTS', KEYS[2 * (i - 3) + 1]) == 1 then
      redis.call('SET', KEYS[2 * (i - 3) + 2], '{"epoch":' .. c[1] .. ',"at":' .. ms .. ',"by":' .. ARGV[3] .. '}', 'PX', ARGV[2])
      n = n + 1
    end
  end
end
redis.call('DEL', KEYS[2])
return {n}
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

// Challenge sets name's challenge for pushes under epoch and role,
// outstanding for AnswerBound: the hook prints the nonce on its stream, and
// only a session that read it answers it (Answer).
func (s Store) Challenge(ctx context.Context, name string, role Role, epoch int64, pushes []Push, nonce string) error {
	var ps []string
	for _, p := range pushes {
		ps = append(ps, string(p))
	}
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := challengeLua.Run(ctx, s.C, []string{RecordKey(name), ChallengeKey(name)}, epoch, string(role), Sum(nonce), strings.Join(ps, ","), AnswerBound.Milliseconds()).Int64()
	if err == nil && got == 0 {
		return ErrNotHooked
	}
	return err
}

// The refusals of Answer, by the script's code.
var (
	ErrNoChallenge = errors.New("no challenge stands for this name: its hook has not asked one, or it lapsed past its bound; read the hook's stream for the next HOOK CHALLENGE")
	ErrWrongNonce  = errors.New("the nonce is not the one the challenge asked; copy it from the newest HOOK CHALLENGE line of the hook's stream")
)

// Answer answers name's challenge with nonce: every push it named whose lease
// is live is proven for ProofTTL. It answers how many were proven.
func (s Store) Answer(ctx context.Context, name, nonce, by string) (int, error) {
	keys := []string{RecordKey(name), ChallengeKey(name)}
	args := []any{Sum(nonce), ProofTTL.Milliseconds(), byJSON(by)}
	for _, p := range Pushes {
		keys = append(keys, LiveKey(name, p), ProofKey(name, p))
		args = append(args, string(p))
	}
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := answerLua.Run(ctx, s.C, keys, args...).Int64Slice()
	if err != nil {
		return 0, err
	}
	if len(got) != 1 {
		return 0, fmt.Errorf("the answer script answered %v, not one count", got)
	}
	switch got[0] {
	case -1:
		return 0, ErrNoChallenge
	case -2:
		return 0, ErrWrongNonce
	case -3:
		return 0, ErrNotHooked
	}
	return int(got[0]), nil
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
		clock = p.Time(ctx)
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
