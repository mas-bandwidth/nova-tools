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
redis.call('HDEL', KEYS[1], 'unhooked', 'reason', 'misses', 'beat_at')
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
// lease is live, clears the challenge and the heartbeat's count of misses
// (tla/HookBeat.tla, Answer). It answers {proven} or {code}: -1 no challenge
// stands, -2 another nonce, -3 the record moved since, -4 a heartbeat
// challenge past its interval (its until).
// KEYS: record, challenge, then per push of Pushes its lease and proof.
// ARGV: sum, ttl ms, by (JSON), then the names of Pushes.
var answerLua = redis.NewScript(nowMS + `
local c = redis.call('HMGET', KEYS[2], 'epoch', 'sum', 'pushes')
if not c[1] then return {-1} end
if c[2] ~= ARGV[1] then return {-2} end
local u = redis.call('HGET', KEYS[2], 'until')
if u and tonumber(ms) > tonumber(u) then return {-4} end
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
redis.call('HSET', KEYS[1], 'misses', 0, 'answered_at', ms)
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
	ErrExpired     = errors.New("the heartbeat challenge is past its interval: it counts as missed; answer the next one, which the heartbeat server sends within the interval")
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
	case -4:
		return 0, ErrExpired
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
		out[i].Stale = servedOf(served.Val(), now.UTC()).Stale()
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

// beatLua is one heartbeat pass over every name, in one atomic step
// (tla/HookBeat.tla, Pass). The pass is taken once an interval: a pass less
// than nine tenths of an interval after the last one (another server's) does
// nothing and answers {0} (the once-key is the server row's at). Otherwise,
// for each name: a record that is not hooked, or hooked as a role with no bus
// push, is left as it is (code 0); a challenge still standing from the last
// pass is a miss; at the limit the name is unhooked as unhookLua does, with
// the reason, and the event logged once (code 2); else a fresh challenge is
// set, due within the interval, naming the pushes of the role a session
// receives (code 1). The server's row is stamped. Last, the pass reads the
// unhooked set, the coordinator (the first hooked name of that role) and the
// live names (hooked, of a challenged role), and what each audience was last
// told: the answer is {1, {code, misses, epoch} per name, the set, the
// coordinator, the live names, then per audience in Audiences the set and the
// coordinator it was told}. Nothing is marked told here: the server marks an
// audience told only after its message went out (Store.Told), so a send that
// fails is told again by the next pass.
// KEYS: events, serve, then per name its record and allKeys (8 keys; the challenge last).
// ARGV: every ms, limit, ttl ms, by, events cap, the count of roles, a role and
// the pushes its challenge names (comma) per role, then per name its name and
// its nonce's sum.
var beatLua = redis.NewScript(nowMS + `
local now = tonumber(ms)
local every, limit, ttl, by, cap = tonumber(ARGV[1]), tonumber(ARGV[2]), ARGV[3], ARGV[4], ARGV[5]
local prev = tonumber(redis.call('HGET', KEYS[2], 'at') or '0')
if prev > 0 and now - prev < every * 9 / 10 then return {0, {}, '', '', '', '', '', '', ''} end
redis.call('HSET', KEYS[2], 'at', ms, 'every', ARGV[1], 'by', by)
local nroles = tonumber(ARGV[6])
local bus = {}
for i = 1, nroles do bus[ARGV[5 + 2 * i]] = ARGV[6 + 2 * i] end
local out, gone, live, coord = {}, {}, {}, ''
local k, a = 3, 7 + 2 * nroles
while k <= #KEYS do
  local rec, chal, name, sum = KEYS[k], KEYS[k + 7], ARGV[a], ARGV[a + 1]
  local did, misses, e = 0, 0, 0
  local role = redis.call('HGET', rec, 'role')
  if redis.call('HGET', rec, 'state') == 'hooked' and bus[role] then
    local last = tonumber(redis.call('HGET', rec, 'beat_at') or '0')
    misses = tonumber(redis.call('HGET', rec, 'misses') or '0')
    if last > 0 and redis.call('EXISTS', chal) == 1 then misses = misses + 1 end
    if misses >= limit then
      e = redis.call('HINCRBY', rec, 'epoch', 1)
      local why = 'missed ' .. misses .. ' challenges'
      redis.call('HSET', rec, 'state', 'unhooked', 'unhooked', ms, 'reason', why, 'misses', misses)
      for i = k + 1, k + 7 do redis.call('DEL', KEYS[i]) end
      redis.call('XADD', KEYS[1], 'MAXLEN', '~', cap, '*', 'name', name, 'event', 'unhooked', 'reason', why, 'role', role, 'epoch', e, 'at', ms, 'by', by)
      did = 2
    else
      e = tonumber(redis.call('HGET', rec, 'epoch'))
      redis.call('HSET', rec, 'misses', misses, 'beat_at', ms)
      redis.call('DEL', chal)
      redis.call('HSET', chal, 'epoch', e, 'sum', sum, 'pushes', bus[role], 'until', string.format('%d', now + every))
      redis.call('PEXPIRE', chal, ttl)
      did = 1
    end
  end
  table.insert(out, {did, misses, e})
  local st = redis.call('HGET', rec, 'state')
  if st == 'unhooked' then table.insert(gone, name) end
  if st == 'hooked' and bus[role] then
    table.insert(live, name)
    if role == 'coordinator' and coord == '' then coord = name end
  end
  k, a = k + 8, a + 2
end
local set = table.concat(gone, ',')
local f = redis.call('HMGET', KEYS[2], 'told:coordinator', 'coord:coordinator', 'told:rest', 'coord:rest')
return {1, out, set, coord, table.concat(live, ','), f[1] or '', f[2] or '', f[3] or '', f[4] or ''}
`)

// What one heartbeat pass did to one name.
const (
	BeatLeft       = 0 // not hooked, no bus push, or challenged already this interval
	BeatChallenged = 1 // a fresh challenge is set: deliver its nonce
	BeatUnhooked   = 2 // missed the limit in a row: unhooked
)

// Beat is one name's outcome of a heartbeat pass.
type Beat struct {
	Name   string
	Did    int    // BeatLeft, BeatChallenged or BeatUnhooked
	Nonce  string // the challenge's nonce, when challenged: the store keeps only its sum
	Misses int
	Epoch  int64
}

// Reason is why an unhooked beat unhooked its name, as the record and the event say it.
func (b Beat) Reason() string { return fmt.Sprintf("missed %d challenges", b.Misses) }

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

// Told is what an audience was last told: the unhooked set and the coordinator.
type Told struct {
	Unhooked []string
	Coord    string
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

// toldLua marks an audience told p's set and coordinator, only while it still
// holds what the pass read (another server's mark since is kept).
// KEYS: serve. ARGV: audience, the set read, the coordinator read, the set, the coordinator.
var toldLua = redis.NewScript(`
local f = redis.call('HMGET', KEYS[1], 'told:' .. ARGV[1], 'coord:' .. ARGV[1])
if (f[1] or '') ~= ARGV[2] or (f[2] or '') ~= ARGV[3] then return 0 end
redis.call('HSET', KEYS[1], 'told:' .. ARGV[1], ARGV[4], 'coord:' .. ARGV[1], ARGV[5])
return 1
`)

// Told marks audience told what p found, after its message went out
// (tla/HookBeat.tla, Tell: delivered at least once, never twice).
func (s Store) Told(ctx context.Context, p Passed, audience string) error {
	ctx, cancel := s.call(ctx)
	defer cancel()
	w := p.Was[audience]
	return toldLua.Run(ctx, s.C, []string{ServeKey}, audience, strings.Join(w.Unhooked, ","), w.Coord, strings.Join(p.Unhooked, ","), p.Coord).Err()
}

// Challenged is the pushes a heartbeat challenge names for each role a session
// answers in: the bus push, and a friend's work push, which reaches the
// session through the same deliver path as its bus push (the friend daemon's
// adapter), so the session's answer proves both. The sprint push is the seat
// hook's own (nova-sprint hook), and a member's or reader's loop has no
// session: neither is challenged.
var Challenged = map[Role][]Push{Coordinator: {Bus}, Friend: {Bus, Work}, Comms: {Bus}}

// Pass is one heartbeat pass over names (sorted), in one round trip and one
// atomic step (beatLua): taken once an interval however many servers pass; in
// it each hooked name of a role in Challenged is challenged, a challenge left
// standing a whole interval is a miss, and at limit misses in a row the name
// is unhooked, once; and the set of unhooked names is told once per change
// (tla/HookBeat.tla).
func (s Store) Pass(ctx context.Context, names []string, every time.Duration, limit int, nonce func() string, by string) (Passed, error) {
	keys := []string{EventsKey, ServeKey}
	args := []any{every.Milliseconds(), limit, (2*every + AnswerBound).Milliseconds(), by, EventsMax, len(Challenged)}
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
		out.Beats[i] = Beat{Name: n, Nonce: nonce()}
		keys = append(append(keys, RecordKey(n)), allKeys(n)...)
		args = append(args, n, Sum(out.Beats[i].Nonce))
	}
	ctx, cancel := s.call(ctx)
	defer cancel()
	got, err := beatLua.Run(ctx, s.C, keys, args...).Slice()
	if err != nil {
		return Passed{}, err
	}
	if len(got) != 9 {
		return Passed{}, fmt.Errorf("the beat script answered %v, not a taken word, the names and seven sets", got)
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
		if !ok || len(v) != 3 {
			return Passed{}, fmt.Errorf("the beat script answered %v for %s, not a code, a count and an epoch", row, names[i])
		}
		b := &out.Beats[i]
		did, _ := v[0].(int64)    // ignored: a code that is no number reads as left alone
		misses, _ := v[1].(int64) // ignored: as above
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
		out.Was[a] = Told{Unhooked: list(str(5 + 2*i)), Coord: str(6 + 2*i)}
	}
	return out, nil
}

// Served is the heartbeat server's stamp as the store holds it: when it last
// passed and its interval, with the store's now; ok false when no server ever passed.
type Served struct {
	At, Now time.Time
	Every   time.Duration
	OK      bool
}

// Stale is whether the heartbeat server is gone: it passed once, and not for
// more than two of its intervals by the store's TIME at this read. No reader
// trusts a record's last written state then (tla/HookBeat.tla, the finding of
// the model run): every name reads down, "hook server stale".
func (sv Served) Stale() bool { return sv.OK && sv.Now.Sub(sv.At) > 2*sv.Every }

// StaleReason is the reason every name reads down while the server is stale.
const StaleReason = "hook server stale"

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
	return Served{At: msTime(m["at"]), Now: now, Every: time.Duration(every) * time.Millisecond, OK: true}
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

// Overdue marks j re-pushed, so it is never re-pushed again.
func (s Store) Overdue(ctx context.Context, j Judgment) error {
	raw, err := json.Marshal(stored{ID: j.ID, To: j.To, Subject: j.Subject, DueMS: j.Due.UnixMilli(), Overdue: true})
	if err != nil {
		return err
	}
	ctx, cancel := s.call(ctx)
	defer cancel()
	return s.C.HSet(ctx, JudgmentsKey, j.ID, raw).Err()
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
