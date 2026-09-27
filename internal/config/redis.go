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
// Functions (internal/nsprint/fn/lua: capacity.lua's ns_capacity_desired,
// friend_roles.lua's ns_friend_roles, friend.lua's ns_friend_wakepath) and
// the friends:login hash presence.lua reads. A machine's ceiling goes through
// ns_capacity_machine; its registry row has no writer in the library, so it
// is the hash machine:<m> and the set `machines`, both nova-config's own.
//
// config:decl is the stamp: rev:<kind> is the Postgres revision last applied
// and at:<kind> the server time it was written (the shape of friends:decl in
// friend_declare.lua).
const (
	DeclKey        = "config:decl"
	FriendsKey     = "friends"
	MachinesKey    = "machines"
	LoginsKey      = "friends:login"
	CapLogKey      = "cap:log"
	FriendConfigFn = "config"
)

// FriendConfigKey is the hash holding a friend's fields no runtime key holds
// (harness, note), with the revision and time they were applied.
func FriendConfigKey(f string) string { return "friend:" + f + ":" + FriendConfigFn }

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
	}
	return nil, 0, fmt.Errorf("apply: no Redis reader for kind %q", kind)
}

func (a *RedisApplier) Write(ctx context.Context, kind string, row Row, prev View, actor, idem string) error {
	switch kind {
	case KindFriend:
		return a.writeFriend(ctx, row, prev, actor, idem)
	case KindMachine:
		return a.writeMachine(ctx, row, prev, actor, idem)
	}
	return fmt.Errorf("apply: no Redis writer for kind %q", kind)
}

func (a *RedisApplier) Remove(ctx context.Context, kind, name, actor, idem string) error {
	switch kind {
	case KindFriend:
		return a.removeFriend(ctx, name, actor, idem)
	case KindMachine:
		return a.removeMachine(ctx, name, actor, idem)
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
	wake := make([]*redis.SliceCmd, len(names))
	conf := make([]*redis.SliceCmd, len(names))
	for i, f := range names {
		desired[i] = pipe.HMGet(ctx, "friend:"+f+":desired", "slots", "machine")
		roles[i] = pipe.HGet(ctx, "friend:"+f+":roles", "roles")
		wake[i] = pipe.HMGet(ctx, "friend:"+f+":wakepath", "kind", "unit", "host", "notify")
		conf[i] = pipe.HMGet(ctx, FriendConfigKey(f), "harness", "note")
	}
	logins := pipe.HGetAll(ctx, LoginsKey)
	rev := pipe.HGet(ctx, DeclKey, revField(KindFriend))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, fmt.Errorf("redis: read friends: %w", err)
	}
	byFriend := map[string][]string{}
	for alias, f := range logins.Val() {
		byFriend[f] = append(byFriend[f], alias)
	}
	views := make(map[string]View, len(names))
	for i, f := range names {
		d := desired[i].Val()
		w := wake[i].Val()
		c := conf[i].Val()
		aliases := byFriend[f]
		sort.Strings(aliases)
		views[f] = View{
			"machine": str(d, 1),
			"slots":   intText(str(d, 0)),
			"harness": str(c, 0),
			"wake":    wakeText(str(w, 0), str(w, 1), str(w, 2), str(w, 3)),
			"roles":   sortedList(roles[i].Val()),
			"logins":  strings.Join(aliases, ","),
			"note":    str(c, 1),
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

// wakeText is the wake field's one spelling from the wakepath hash.
func wakeText(kind, unit, host, notify string) string {
	switch kind {
	case "unit":
		return "unit:" + unit + "@" + host
	case "human":
		return "human:" + notify
	}
	return ""
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

func (a *RedisApplier) writeFriend(ctx context.Context, row Row, prev View, actor, idem string) error {
	f := row.Name
	// 1. slots and machine, registering the friend (ns_capacity_desired).
	reply, err := a.Client.FCall(ctx, "ns_capacity_desired", nil, KindFriend, f, row.Fields["slots"], row.Fields["machine"], actor, idem).Result()
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
		return &RefusedError{Err: ErrCeiling, Detail: fmt.Sprintf("machine %s has no ceiling in Redis; run: nova-config apply --kind machine first", row.Fields["machine"])}
	case "NAME-IS-LOGIN":
		return &RefusedError{Err: ErrLoginTaken, Detail: fmt.Sprintf("%s is a login in Redis (friends:login), not a friend", f)}
	default:
		return fmt.Errorf("redis: friend %s slots: %s", f, strings.Join(words, " "))
	}
	// 2. roles (ns_friend_roles: the actor must be a coordinator, or nobody
	// is one yet and this row makes the first).
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
			return RedisRefusal("roles of %s: it is the last coordinator; give another friend the coordinator role first", f)
		case "BADROLE", "UNKNOWN":
			return RedisRefusal("roles of %s: %s %s", f, words[0], word(words, 1))
		default:
			return fmt.Errorf("redis: friend %s roles: %s", f, strings.Join(words, " "))
		}
	}
	// 3. the wake path (ns_friend_wakepath; clear when the row has none).
	if prev == nil && row.Fields["wake"] != "" || prev != nil && prev["wake"] != row.Fields["wake"] {
		kind, unit, host, notify := wakeParts(row.Fields["wake"])
		reply, err := a.Client.FCall(ctx, "ns_friend_wakepath", nil, f, kind, unit, host, notify, actor, idem).Result()
		if err != nil {
			return fmt.Errorf("redis: friend %s wake: %w", f, err)
		}
		if words := replyWords(reply); words[0] != "OK" {
			return fmt.Errorf("redis: friend %s wake: %s", f, strings.Join(words, " "))
		}
	}
	// 4. logins (friends:login alias -> friend), 5. harness and note.
	want := Words(row.Fields["logins"])
	var have []string
	if prev != nil {
		have = Words(prev["logins"])
	}
	pipe := a.Client.TxPipeline()
	for _, alias := range want {
		pipe.HSet(ctx, LoginsKey, alias, f)
	}
	for _, alias := range have {
		if !hasWord(row.Fields["logins"], alias) {
			pipe.HDel(ctx, LoginsKey, alias)
		}
	}
	rev, _ := strings.CutPrefix(idem, "config:"+KindFriend+":")
	pipe.HSet(ctx, FriendConfigKey(f), "harness", row.Fields["harness"], "note", row.Fields["note"], "rev", rev, "at", strconv.FormatInt(a.now(), 10))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: friend %s logins and config: %w", f, err)
	}
	return nil
}

// wakeParts is the wake field's parts for ns_friend_wakepath: kind unit,
// human or clear.
func wakeParts(wake string) (kind, unit, host, notify string) {
	if rest, ok := strings.CutPrefix(wake, "unit:"); ok {
		unit, host, _ = strings.Cut(rest, "@")
		return "unit", unit, host, ""
	}
	if rest, ok := strings.CutPrefix(wake, "human:"); ok {
		return "human", "", "", rest
	}
	return "clear", "", "", ""
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
	logins, err := a.Client.HGetAll(ctx, LoginsKey).Result()
	if err != nil {
		return fmt.Errorf("redis: read %s: %w", LoginsKey, err)
	}
	pipe := a.Client.TxPipeline()
	pipe.SRem(ctx, FriendsKey, f)
	pipe.Del(ctx, "friend:"+f+":desired", "friend:"+f+":roles", "friend:"+f+":wakepath", FriendConfigKey(f))
	for alias, owner := range logins {
		if owner == f {
			pipe.HDel(ctx, LoginsKey, alias)
		}
	}
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
	ceiling := make([]*redis.SliceCmd, len(names))
	reg := make([]*redis.MapStringStringCmd, len(names))
	for i, m := range names {
		ceiling[i] = pipe.HMGet(ctx, MachineCeilingKey(m), "slots", "cores")
		reg[i] = pipe.HGetAll(ctx, MachineKey(m))
	}
	rev := pipe.HGet(ctx, DeclKey, revField(KindMachine))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, fmt.Errorf("redis: read machines: %w", err)
	}
	k, _ := Lookup(KindMachine)
	views := make(map[string]View, len(names))
	for i, m := range names {
		c := ceiling[i].Val()
		r := reg[i].Val()
		v := View{"slots": intText(str(c, 0)), "cores": intText(str(c, 1))}
		for _, f := range k.Fields {
			if f.Name == "slots" || f.Name == "cores" {
				continue
			}
			if f.Type == TypeList {
				v[f.Name] = sortedList(r[f.Name])
			} else {
				v[f.Name] = r[f.Name]
			}
		}
		views[m] = v
	}
	return views, revValue(rev), nil
}

func (a *RedisApplier) writeMachine(ctx context.Context, row Row, prev View, actor, idem string) error {
	m := row.Name
	cores := ""
	if row.Int("cores") > 0 {
		cores = row.Fields["cores"]
	}
	if prev == nil || prev["slots"] != row.Fields["slots"] || prev["cores"] != row.Fields["cores"] {
		reply, err := a.Client.FCall(ctx, "ns_capacity_machine", nil, m, row.Fields["slots"], cores, "", actor, idem, "", "").Result()
		if err != nil {
			return fmt.Errorf("redis: machine %s ceiling: %w", m, err)
		}
		words := replyWords(reply)
		switch words[0] {
		case "SET":
		case "CEILING":
			return &RefusedError{Err: ErrCeiling, Detail: fmt.Sprintf("CEILING %s: its friends and benches desire %s slots and the row says %s; lower their slots first, or set --slots to at least %s",
				m, word(words, 2), word(words, 3), word(words, 2))}
		default:
			return fmt.Errorf("redis: machine %s ceiling: %s", m, strings.Join(words, " "))
		}
	}
	k, _ := Lookup(KindMachine)
	rev, _ := strings.CutPrefix(idem, "config:"+KindMachine+":")
	fields := []any{"rev", rev, "at", strconv.FormatInt(a.now(), 10)}
	for _, f := range k.Fields {
		if f.Name == "slots" || f.Name == "cores" {
			continue
		}
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
