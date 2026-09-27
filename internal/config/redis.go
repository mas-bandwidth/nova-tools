package config

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// The Redis keys apply writes, per kind. A friend's keys are the ones the
// nova-sprint verbs wrote by hand until now, through the same Redis
// Functions (internal/nsprint/fn/lua: capacity.lua's ns_capacity_desired
// for slots and tiers, friend_roles.lua's ns_friend_roles for roles). Her
// logins and wake path are what she would just know: her own presence
// writes them, apply never touches friends:login or friend:<f>:wakepath. A
// machine's ceiling goes through ns_capacity_machine; its registry row has
// no writer in the library, so it is the hash machine:<m> and the set
// `machines`, both nova-config's own. The fleet row is two plain keys of
// nova-config's own, fleet:store and fleet:coordinator, each a machine's
// name; the sprint row is sprint:coordinator, a friend's name (each absent
// when the row names none).
//
// config:decl is the stamp: rev:<kind> is the Postgres revision last applied
// and at:<kind> the server time it was written (the shape of friends:decl in
// friend_declare.lua).
const (
	DeclKey     = "config:decl"
	FriendsKey  = "friends"
	MachinesKey = "machines"
	CapLogKey   = "cap:log"
)

// FleetKey is the plain key one fleet field is written to: fleet:store,
// fleet:coordinator.
func FleetKey(field string) string { return "fleet:" + field }

// SprintKey is the plain key one sprint field is written to:
// sprint:coordinator.
func SprintKey(field string) string { return "sprint:" + field }

// FriendBeatKey is a friend's own heartbeat (nova-friend): its host field
// is the machine she runs on now, what her desired slots are charged to.
func FriendBeatKey(f string) string { return "friend:" + f + ":beat" }

// BeatKey is the machine's own heartbeat, written by the bench that runs on
// it (nova-sprint bench beat): host, at (unix ms), load1, ncpu, cpu today;
// os, arch and memory_gb when the beat carries them. nova-config reads it,
// never writes it.
func BeatKey(m string) string { return "bench:" + m + ":beat" }

// MachineKey is the machine's registry hash.
func MachineKey(m string) string { return "machine:" + m }

// MachineCeilingKey is what ns_capacity_machine writes.
func MachineCeilingKey(m string) string { return "machine:" + m + ":ceiling" }

// RedisApplier is the Applier over a live store.
type RedisApplier struct {
	Client *redis.Client
	// Now is the stamp's clock (time.Now when nil).
	Now func() time.Time
}

func (a *RedisApplier) now() int64 {
	if a.Now != nil {
		return a.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// Prepare installs the nova_sprint function library when the store has none
// (fn.LoadMissing: never replaces a deployed one).
func (a *RedisApplier) Prepare(ctx context.Context) error {
	return fn.LoadMissing(ctx, a.Client)
}

func (a *RedisApplier) Read(ctx context.Context, kind string) (map[string]View, int64, error) {
	switch kind {
	case KindFriend:
		return a.readFriends(ctx)
	case KindMachine:
		return a.readMachines(ctx)
	case KindFleet:
		return a.readSingleton(ctx, KindFleet, FleetKey)
	case KindSprint:
		return a.readSingleton(ctx, KindSprint, SprintKey)
	}
	return nil, 0, fmt.Errorf("apply: no Redis reader for kind %q", kind)
}

func (a *RedisApplier) Write(ctx context.Context, kind string, row Row, prev View, actor, idem string) error {
	switch kind {
	case KindFriend:
		return a.writeFriend(ctx, row, prev, actor, idem)
	case KindMachine:
		return a.writeMachine(ctx, row, prev, actor, idem)
	case KindFleet:
		return a.writeSingleton(ctx, KindFleet, FleetKey, row)
	case KindSprint:
		return a.writeSingleton(ctx, KindSprint, SprintKey, row)
	}
	return fmt.Errorf("apply: no Redis writer for kind %q", kind)
}

func (a *RedisApplier) Remove(ctx context.Context, kind, name, actor, idem string) error {
	switch kind {
	case KindFriend:
		return a.removeFriend(ctx, name, actor, idem)
	case KindMachine:
		return a.removeMachine(ctx, name, actor, idem)
	case KindFleet, KindSprint:
		return fmt.Errorf("apply: the %s row is never removed", kind)
	}
	return fmt.Errorf("apply: no Redis remover for kind %q", kind)
}

// revField is the decl hash field of a kind's revision; atField its time.
func revField(kind string) string { return "rev:" + kind }
func atField(kind string) string  { return "at:" + kind }

// declRev reads the stamped revision of a kind, 0 when none.
func (a *RedisApplier) declRev(ctx context.Context, kind string) (int64, error) {
	v, err := a.Client.HGet(ctx, DeclKey, revField(kind)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("redis: read %s %s: %w", DeclKey, revField(kind), err)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("redis: %s %s is %q, not a revision", DeclKey, revField(kind), v)
	}
	return n, nil
}

// Stamp is the compare-and-set of friend_declare.lua in a WATCH/MULTI: the
// stamp is written only while it still reads prev.
func (a *RedisApplier) Stamp(ctx context.Context, kind string, prev, rev int64) error {
	err := a.Client.Watch(ctx, func(tx *redis.Tx) error {
		cur, err := tx.HGet(ctx, DeclKey, revField(kind)).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		var have int64
		if cur != "" {
			if have, err = strconv.ParseInt(cur, 10, 64); err != nil {
				return fmt.Errorf("%s %s is %q, not a revision", DeclKey, revField(kind), cur)
			}
		}
		if have != prev {
			return Conflict(kind, have, rev)
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.HSet(ctx, DeclKey, revField(kind), strconv.FormatInt(rev, 10), atField(kind), strconv.FormatInt(a.now(), 10))
			return nil
		})
		return err
	}, DeclKey)
	if errors.Is(err, redis.TxFailedErr) {
		have, rerr := a.declRev(ctx, kind)
		if rerr != nil {
			return rerr
		}
		return Conflict(kind, have, rev)
	}
	if err != nil && !Refused(err) {
		return fmt.Errorf("redis: stamp %s: %w", kind, err)
	}
	return err
}

// --- friends ---------------------------------------------------------------

func (a *RedisApplier) readFriends(ctx context.Context) (map[string]View, int64, error) {
	names, err := a.Client.SMembers(ctx, FriendsKey).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("redis: read %s: %w", FriendsKey, err)
	}
	sort.Strings(names)
	pipe := a.Client.Pipeline()
	desired := make([]*redis.SliceCmd, len(names))
	roles := make([]*redis.StringCmd, len(names))
	for i, f := range names {
		desired[i] = pipe.HMGet(ctx, "friend:"+f+":desired", "slots", "tiers")
		roles[i] = pipe.HGet(ctx, "friend:"+f+":roles", "roles")
	}
	rev := pipe.HGet(ctx, DeclKey, revField(KindFriend))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, fmt.Errorf("redis: read friends: %w", err)
	}
	views := make(map[string]View, len(names))
	for i, f := range names {
		d := desired[i].Val()
		views[f] = View{
			"slots": intText(str(d, 0)),
			"tiers": sortedList(str(d, 1)),
			"roles": sortedList(roles[i].Val()),
		}
	}
	return views, revValue(rev), nil
}

func str(vals []any, i int) string {
	if i >= len(vals) || vals[i] == nil {
		return ""
	}
	return fmt.Sprint(vals[i])
}

// intText canonicalises an int read from a hash: "" or a non-number is "0".
func intText(s string) string {
	n, err := strconv.Atoi(s)
	if err != nil {
		return "0"
	}
	return strconv.Itoa(n)
}

func sortedList(csv string) string {
	words, _ := splitList(csv)
	return strings.Join(words, ",")
}

func revValue(cmd *redis.StringCmd) int64 {
	n, _ := strconv.ParseInt(cmd.Val(), 10, 64)
	return n
}

// replyWords flattens a function reply into words.
func replyWords(reply any) []string {
	values, ok := reply.([]any)
	if !ok {
		return []string{fmt.Sprint(reply)}
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// charge is the machine a friend's desired slots count against: the host
// her beat reports (friends may run on any bench), else the fleet's
// coordinator machine as the default charge, else a refusal naming the
// fleet set that fixes it.
func (a *RedisApplier) charge(ctx context.Context, f string) (string, error) {
	host, err := a.Client.HGet(ctx, FriendBeatKey(f), "host").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("redis: read %s: %w", FriendBeatKey(f), err)
	}
	if host != "" {
		return host, nil
	}
	coordinator, err := a.Client.Get(ctx, FleetKey("coordinator")).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("redis: read %s: %w", FleetKey("coordinator"), err)
	}
	if coordinator == "" {
		return "", &RefusedError{Err: ErrCeiling, Detail: fmt.Sprintf("friend %s has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply", f)}
	}
	return coordinator, nil
}

// tiersArg is the twelfth argument of ns_capacity_desired: the list, or
// '-' to clear a stored one when the row has none (” would keep it).
func tiersArg(tiers string) string {
	if tiers == "" {
		return "-"
	}
	return tiers
}

func (a *RedisApplier) writeFriend(ctx context.Context, row Row, prev View, actor, idem string) error {
	f := row.Name
	// 1. slots and tiers, registering the friend (ns_capacity_desired),
	// charged to the machine her beat reports or the coordinator machine.
	machine, err := a.charge(ctx, f)
	if err != nil {
		return err
	}
	reply, err := a.Client.FCall(ctx, "ns_capacity_desired", nil, KindFriend, f, row.Fields["slots"], machine, actor, idem, "", "", "", "", "", tiersArg(row.Fields["tiers"])).Result()
	if err != nil {
		return fmt.Errorf("redis: friend %s slots: %w", f, err)
	}
	words := replyWords(reply)
	switch words[0] {
	case "SET", "SAME":
	case "CEILING":
		return &RefusedError{Err: ErrCeiling, Detail: fmt.Sprintf("CEILING %s: friend %s makes the sum %s over the machine ceiling %s; raise it with nova-config machine set %s --slots <n>, or lower a friend's slots",
			word(words, 1), f, word(words, 2), word(words, 3), word(words, 1))}
	case "NOCEILING":
		return &RefusedError{Err: ErrCeiling, Detail: fmt.Sprintf("machine %s has no ceiling in Redis; run: nova-config machine add %s ... (her beat names it) then apply", machine, machine)}
	case "NAME-IS-LOGIN":
		return &RefusedError{Err: ErrActor, Detail: fmt.Sprintf("%s is a login in Redis (friends:login), not a friend", f)}
	default:
		return fmt.Errorf("redis: friend %s slots: %s", f, strings.Join(words, " "))
	}
	// 2. roles (ns_friend_roles: the actor must be a coordinator, or nobody
	// is one yet and this row makes the first; the sprint row's coordinator
	// carries the role here, derived by Kind.Derive).
	if prev == nil && row.Fields["roles"] != "" || prev != nil && prev["roles"] != row.Fields["roles"] {
		reply, err := a.Client.FCall(ctx, "ns_friend_roles", nil, f, row.Fields["roles"], actor, idem).Result()
		if err != nil {
			if strings.Contains(err.Error(), "ACTOR") {
				return &RefusedError{Err: ErrActor, Detail: fmt.Sprintf("roles of %s: --as %s is not a registered friend; apply as a friend that holds the coordinator role", f, actor)}
			}
			return fmt.Errorf("redis: friend %s roles: %w", f, err)
		}
		words := replyWords(reply)
		switch words[0] {
		case "OK":
		case "REFUSED":
			return RedisRefusal("roles of %s: --as %s does not hold the coordinator role in Redis", f, actor)
		case "LASTCOORD":
			return RedisRefusal("roles of %s: it would leave no coordinator; run: nova-config sprint set --coordinator <friend>, then apply", f)
		case "BADROLE", "UNKNOWN":
			return RedisRefusal("roles of %s: %s %s", f, words[0], word(words, 1))
		default:
			return fmt.Errorf("redis: friend %s roles: %s", f, strings.Join(words, " "))
		}
	}
	return nil
}

func word(words []string, i int) string {
	if i < len(words) {
		return words[i]
	}
	return ""
}

// The refusals the runtime's functions answer with.
var (
	ErrCeiling = errors.New("ceiling")
	ErrActor   = errors.New("actor")
	ErrWorking = errors.New("working copies")
	ErrInUse   = errors.New("in use")
)

// RedisRefusal is a *RefusedError with a formatted detail.
func RedisRefusal(format string, args ...any) *RefusedError {
	return &RefusedError{Err: ErrActor, Detail: fmt.Sprintf(format, args...)}
}

func (a *RedisApplier) removeFriend(ctx context.Context, f, actor, idem string) error {
	// The friend's working set is a table set, named by the sprint epoch
	// (ws.ConsumerKeyAt, nova-tools#4238): a copy it still holds keeps the
	// friend in Redis.
	epoch, err := ws.Epoch(ctx, a.Client)
	if err != nil {
		return fmt.Errorf("redis: read the sprint epoch: %w", err)
	}
	working, err := a.Client.ZRange(ctx, ws.ConsumerKeyAt(epoch, KindFriend+":"+f, "working"), 0, -1).Result()
	if err != nil {
		return fmt.Errorf("redis: read friend %s working copies: %w", f, err)
	}
	if len(working) > 0 {
		sort.Strings(working)
		return &RefusedError{Err: ErrWorking, Detail: fmt.Sprintf("friend %s holds %d working copies (%s); let them finish or move them before removing the friend", f, len(working), strings.Join(working, ","))}
	}
	// Only what apply wrote goes: her presence's own keys (beat, logins,
	// wake path) are hers.
	pipe := a.Client.TxPipeline()
	pipe.SRem(ctx, FriendsKey, f)
	pipe.Del(ctx, "friend:"+f+":desired", "friend:"+f+":roles")
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: CapLogKey, MaxLen: 100000, Approx: true, Values: map[string]any{
		"kind": "config-remove", "subject": KindFriend + ":" + f, "actor": actor, "idem": idem, "at": strconv.FormatInt(a.now(), 10)}})
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: remove friend %s: %w", f, err)
	}
	return nil
}

// --- machines --------------------------------------------------------------

func (a *RedisApplier) readMachines(ctx context.Context) (map[string]View, int64, error) {
	names, err := a.Client.SMembers(ctx, MachinesKey).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("redis: read %s: %w", MachinesKey, err)
	}
	sort.Strings(names)
	pipe := a.Client.Pipeline()
	ceiling := make([]*redis.StringCmd, len(names))
	reg := make([]*redis.MapStringStringCmd, len(names))
	for i, m := range names {
		ceiling[i] = pipe.HGet(ctx, MachineCeilingKey(m), "slots")
		reg[i] = pipe.HGetAll(ctx, MachineKey(m))
	}
	rev := pipe.HGet(ctx, DeclKey, revField(KindMachine))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, fmt.Errorf("redis: read machines: %w", err)
	}
	k, _ := Lookup(KindMachine)
	views := make(map[string]View, len(names))
	for i, m := range names {
		// slots is read from the ceiling, the key the runtime guards on, so
		// a ceiling moved by hand is put back by the next apply.
		r := reg[i].Val()
		v := View{"slots": intText(ceiling[i].Val())}
		for _, f := range k.Fields {
			switch {
			case f.Name == "slots":
			case f.Type == TypeInt:
				v[f.Name] = intText(r[f.Name])
			default:
				v[f.Name] = r[f.Name]
			}
		}
		views[m] = v
	}
	return views, revValue(rev), nil
}

func (a *RedisApplier) writeMachine(ctx context.Context, row Row, prev View, actor, idem string) error {
	m := row.Name
	if prev == nil || prev["slots"] != row.Fields["slots"] {
		// cores and memory are never declared (they come live from the
		// beat), so the ceiling call carries none and the CI budget the
		// function derives from them is left as it is.
		reply, err := a.Client.FCall(ctx, "ns_capacity_machine", nil, m, row.Fields["slots"], "", "", actor, idem, "", "").Result()
		if err != nil {
			return fmt.Errorf("redis: machine %s ceiling: %w", m, err)
		}
		words := replyWords(reply)
		switch words[0] {
		case "SET":
		case "CEILING":
			return &RefusedError{Err: ErrCeiling, Detail: fmt.Sprintf("CEILING %s: its friends desire %s slots and the row says %s; lower their slots first, or set --slots to at least %s",
				m, word(words, 2), word(words, 3), word(words, 2))}
		default:
			return fmt.Errorf("redis: machine %s ceiling: %s", m, strings.Join(words, " "))
		}
	}
	k, _ := Lookup(KindMachine)
	rev, _ := strings.CutPrefix(idem, "config:"+KindMachine+":")
	fields := []any{"rev", rev, "at", strconv.FormatInt(a.now(), 10)}
	for _, f := range k.Fields {
		fields = append(fields, f.Name, row.Fields[f.Name])
	}
	pipe := a.Client.TxPipeline()
	pipe.SAdd(ctx, MachinesKey, m)
	pipe.HSet(ctx, MachineKey(m), fields...)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: machine %s registry: %w", m, err)
	}
	return nil
}

func (a *RedisApplier) removeMachine(ctx context.Context, m, actor, idem string) error {
	var users []string
	for _, kind := range []string{KindFriend, "bench"} {
		names, err := a.Client.SMembers(ctx, kind+"s").Result()
		if err != nil {
			return fmt.Errorf("redis: read %ss: %w", kind, err)
		}
		for _, n := range names {
			on, err := a.Client.HGet(ctx, kind+":"+n+":desired", "machine").Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return fmt.Errorf("redis: read %s %s: %w", kind, n, err)
			}
			if on == m {
				users = append(users, kind+":"+n)
			}
		}
	}
	if len(users) > 0 {
		sort.Strings(users)
		return &RefusedError{Err: ErrInUse, Detail: fmt.Sprintf("machine %s still carries %s in Redis; move or remove them first", m, strings.Join(users, ","))}
	}
	pipe := a.Client.TxPipeline()
	pipe.SRem(ctx, MachinesKey, m)
	pipe.Del(ctx, MachineKey(m), MachineCeilingKey(m), "machine:"+m+":budget")
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: CapLogKey, MaxLen: 100000, Approx: true, Values: map[string]any{
		"kind": "config-remove", "subject": KindMachine + ":" + m, "actor": actor, "idem": idem, "at": strconv.FormatInt(a.now(), 10)}})
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: remove machine %s: %w", m, err)
	}
	return nil
}

// --- the fleet and sprint rows ----------------------------------------------

// readSingleton is the one view of a singleton kind: the row always exists
// on both sides, so a field Redis lacks is "" and the plan is a SET of what
// differs, never an ADD or a REMOVE.
func (a *RedisApplier) readSingleton(ctx context.Context, kind string, key func(string) string) (map[string]View, int64, error) {
	k, _ := Lookup(kind)
	pipe := a.Client.Pipeline()
	vals := make([]*redis.StringCmd, len(k.Fields))
	for i, f := range k.Fields {
		vals[i] = pipe.Get(ctx, key(f.Name))
	}
	rev := pipe.HGet(ctx, DeclKey, revField(kind))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, fmt.Errorf("redis: read %s: %w", kind, err)
	}
	v := View{}
	for i, f := range k.Fields {
		v[f.Name] = vals[i].Val()
	}
	return map[string]View{kind: v}, revValue(rev), nil
}

// writeSingleton is a plain SET per field (nova-config's own keys; no
// function in the library reads or writes them), DEL when the row names
// nothing.
func (a *RedisApplier) writeSingleton(ctx context.Context, kind string, key func(string) string, row Row) error {
	k, _ := Lookup(kind)
	pipe := a.Client.TxPipeline()
	for _, f := range k.Fields {
		if v := row.Fields[f.Name]; v == "" {
			pipe.Del(ctx, key(f.Name))
		} else {
			pipe.Set(ctx, key(f.Name), v, 0)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: write %s: %w", kind, err)
	}
	return nil
}

// --- the live, measured facts ------------------------------------------------

// Beat is what a machine reported last, read from BeatKey: never stored in
// Postgres, never typed (Glenn 2026-09-27: measured facts coming live is
// "more robust"). Cores is the beat's ncpu; OS, Arch and MemoryGB are ""
// until the beat carries them; At is the beat's time as RFC 3339 UTC.
type Beat struct {
	OS, Arch, Cores, MemoryGB, At string
}

// BeatReader reads the beats of named machines; nil for one with no beat.
type BeatReader interface {
	Beats(ctx context.Context, names []string) (map[string]*Beat, error)
}

// Beats reads every named machine's beat in one pipeline.
func (a *RedisApplier) Beats(ctx context.Context, names []string) (map[string]*Beat, error) {
	pipe := a.Client.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(names))
	for i, m := range names {
		cmds[i] = pipe.HGetAll(ctx, BeatKey(m))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("redis: read beats: %w", err)
	}
	out := make(map[string]*Beat, len(names))
	for i, m := range names {
		h := cmds[i].Val()
		if len(h) == 0 {
			continue
		}
		b := &Beat{OS: h["os"], Arch: h["arch"], Cores: h["ncpu"], MemoryGB: h["memory_gb"]}
		if ms, err := strconv.ParseInt(h["at"], 10, 64); err == nil && ms > 0 {
			b.At = time.UnixMilli(ms).UTC().Format(time.RFC3339)
		}
		out[m] = b
	}
	return out, nil
}
