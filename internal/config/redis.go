package config

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/redis/go-redis/v9"
)

// The Redis keys apply writes, per kind. A friend's keys are the ones the
// nova-sprint verbs wrote by hand until now, through the same Redis
// Functions (internal/nsprint/fn/lua: capacity.lua's ns_capacity_desired
// for slots and tiers, friend_roles.lua's ns_friend_roles for roles), and her
// width, a plain field of friend:<f>:desired no function touches. Her
// logins and wake path are what she would just know: her own presence
// writes them, apply never touches friends:login or friend:<f>:wakepath. A
// machine's ceiling goes through ns_capacity_machine; its registry row has
// no writer in the library, so it is the hash machine:<m> and the set
// `machines`, both nova-config's own. The fleet row is two plain keys of
// nova-config's own, fleet:store and fleet:coordinator, each a machine's
// name; the sprint row is sprint:coordinator, a friend's name (each absent
// when the row names none).
//
// A loop's row is the hash loop:<name> with every field, the derived log
// path, rev and at, and its name in the set `loops`: nova-config's own keys,
// which the plays read to render one unit per row. A route's row is the hash
// route:<name> with every field, rev and at, and its name in the set
// `routes`, which the deal reads (hashKinds); a tier's row is the hash
// tier:<name> and its name in the set `tiers`, read by the deal beside the
// routes.
//
// config:decl is the stamp: rev:<kind> is the Postgres revision last applied
// and at:<kind> the server time it was written (the shape of friends:decl in
// friend_declare.lua).
const (
	DeclKey     = "config:decl"
	FriendsKey  = "friends"
	MachinesKey = "machines"
	CapLogKey   = "cap:log"
	LoopsKey    = "loops"
	RoutesKey   = "routes"
	TiersKey    = "tiers"
)

// LoopKey is a loop's hash: its fields, log, rev and at.
func LoopKey(name string) string { return "loop:" + name }

// RouteKey is a route's hash: its fields, name, rev and at. The deal reads
// every route of the set RoutesKey (internal/sprint/store, the routes read).
func RouteKey(name string) string { return "route:" + name }

// TierKey is a tier's hash: its route array (routes), name, rev and at. The deal
// reads its routes field with the routes (internal/sprint/store, the routes read).
func TierKey(name string) string { return "tier:" + name }

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

	prepareOnce sync.Once
	prepareErr  error

	coordinator     string
	coordinatorRead bool
	friendHosts     map[string]string // friend name -> beat host
}

func (a *RedisApplier) now() int64 {
	if a.Now != nil {
		return a.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// Prepare installs the nova_sprint function library when the store has none
// (fn.LoadMissing: never replaces a deployed one). It runs at most once per
// process (RedisApplier).
func (a *RedisApplier) Prepare(ctx context.Context) error {
	a.prepareOnce.Do(func() {
		a.prepareErr = fn.LoadMissing(ctx, a.Client)
	})
	return a.prepareErr
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
	case KindLoop, KindRoute, KindTier:
		return a.readHashes(ctx, hashKinds[kind])
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
	case KindLoop, KindRoute, KindTier:
		return a.writeHash(ctx, hashKinds[kind], row, idem)
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
	case KindLoop, KindRoute, KindTier:
		return a.removeHash(ctx, hashKinds[kind], name, actor, idem)
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
	beats := make([]*redis.StringCmd, len(names))
	for i, f := range names {
		desired[i] = pipe.HMGet(ctx, "friend:"+f+":desired", "slots", "tiers", "width")
		roles[i] = pipe.HGet(ctx, "friend:"+f+":roles", "roles")
		beats[i] = pipe.HGet(ctx, FriendBeatKey(f), "host")
	}
	coordCmd := pipe.Get(ctx, FleetKey("coordinator"))
	rev := pipe.HGet(ctx, DeclKey, revField(KindFriend))
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, 0, fmt.Errorf("redis: read friends: %w", err)
	}
	a.friendHosts = make(map[string]string, len(names))
	for i, f := range names {
		a.friendHosts[f] = beats[i].Val()
	}
	a.coordinator = coordCmd.Val()
	a.coordinatorRead = true
	views := make(map[string]View, len(names))
	for i, f := range names {
		d := desired[i].Val()
		views[f] = View{
			"slots": intText(str(d, 0)),
			"tiers": sortedList(str(d, 1)),
			"roles": sortedList(roles[i].Val()),
			"width": intText(str(d, 2)),
		}
	}
	return views, revValue(rev), nil
}

// PrefetchFriends pipelines the per-friend beat reads and fleet coordinator
// lookup across all friends being applied (redis.go:255, 262).
func (a *RedisApplier) PrefetchFriends(ctx context.Context, names []string) error {
	var missing []string
	for _, f := range names {
		if a.friendHosts == nil {
			missing = append(missing, f)
			continue
		}
		if _, ok := a.friendHosts[f]; !ok {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 && a.coordinatorRead {
		return nil
	}
	pipe := a.Client.Pipeline()
	beats := make([]*redis.StringCmd, len(missing))
	for i, f := range missing {
		beats[i] = pipe.HGet(ctx, FriendBeatKey(f), "host")
	}
	var coordCmd *redis.StringCmd
	if !a.coordinatorRead {
		coordCmd = pipe.Get(ctx, FleetKey("coordinator"))
	}
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return fmt.Errorf("redis: prefetch friends: %w", err)
	}
	if a.friendHosts == nil {
		a.friendHosts = make(map[string]string, len(missing))
	}
	for i, f := range missing {
		a.friendHosts[f] = beats[i].Val()
	}
	if coordCmd != nil {
		a.coordinator = coordCmd.Val()
		a.coordinatorRead = true
	}
	return nil
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
	var host string
	if a.friendHosts != nil {
		host = a.friendHosts[f]
	}
	if host == "" {
		if a.friendHosts == nil || func() bool { _, ok := a.friendHosts[f]; return !ok }() {
			h, err := a.Client.HGet(ctx, FriendBeatKey(f), "host").Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return "", fmt.Errorf("redis: read %s: %w", FriendBeatKey(f), err)
			}
			host = h
			if a.friendHosts == nil {
				a.friendHosts = make(map[string]string)
			}
			a.friendHosts[f] = host
		}
	}
	if host != "" {
		return host, nil
	}
	if !a.coordinatorRead {
		coord, err := a.Client.Get(ctx, FleetKey("coordinator")).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return "", fmt.Errorf("redis: read %s: %w", FleetKey("coordinator"), err)
		}
		a.coordinator = coord
		a.coordinatorRead = true
	}
	if a.coordinator == "" {
		return "", &RefusedError{Err: ErrCeiling, Detail: fmt.Sprintf("friend %s has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply", f)}
	}
	return a.coordinator, nil
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
	// 2. her width, the desired hash's own field beside slots and tiers that
	// ns_capacity_desired neither reads nor writes, and 3. roles
	// (ns_friend_roles: the actor must be a coordinator, or nobody is one
	// yet and this row makes the first; the sprint row's coordinator carries
	// the role here, derived by Kind.Derive): each only when it differs,
	// both in one round trip, after slots registered her.
	writeWidth := prev == nil || prev["width"] != row.Fields["width"]
	writeRoles := prev == nil && row.Fields["roles"] != "" || prev != nil && prev["roles"] != row.Fields["roles"]
	if !writeWidth && !writeRoles {
		return nil
	}
	pipe := a.Client.Pipeline()
	var roles *redis.Cmd
	if writeWidth {
		pipe.HSet(ctx, "friend:"+f+":desired", "width", row.Fields["width"])
	}
	if writeRoles {
		roles = pipe.FCall(ctx, "ns_friend_roles", nil, f, row.Fields["roles"], actor, idem)
	}
	// An error of the width's own is returned here; one the roles call
	// carries (its refusal, or the connection's, which every command of the
	// pipe carries) is read below with the roles call's words.
	if err := redisconn.Exec(ctx, pipe); err != nil && (roles == nil || roles.Err() == nil) {
		return fmt.Errorf("redis: friend %s width: %w", f, err)
	}
	if roles != nil {
		reply, err := roles.Result()
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
	ErrInUse   = errors.New("in use")
)

// RedisRefusal is a *RefusedError with a formatted detail.
func RedisRefusal(format string, args ...any) *RefusedError {
	return &RefusedError{Err: ErrActor, Detail: fmt.Sprintf(format, args...)}
}

func (a *RedisApplier) removeFriend(ctx context.Context, f, actor, idem string) error {
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
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, 0, fmt.Errorf("redis: read machines: %w", err)
	}
	views := make(map[string]View, len(names))
	for i, m := range names {
		views[m] = machineView(reg[i].Val(), ceiling[i].Val())
	}
	return views, revValue(rev), nil
}

// machineView is a machine's view from its registry hash and its ceiling.
// slots is read from the ceiling, the key the runtime guards on, so a
// ceiling moved by hand is put back by the next apply.
func machineView(reg map[string]string, ceiling string) View {
	k, _ := Lookup(KindMachine)
	v := View{"slots": intText(ceiling)}
	for _, f := range k.Fields {
		switch {
		case f.Name == "slots":
		case f.Type == TypeInt && f.Nullable && reg[f.Name] == "":
			v[f.Name] = "" // unset (a machine's width: the default), as the row holds it
		case f.Type == TypeInt:
			v[f.Name] = intText(reg[f.Name])
		default:
			v[f.Name] = reg[f.Name]
		}
	}
	return v
}

// Snapshot reads the applied state the inventory is built from in two
// round trips, whatever the fleet's size: the names (the machines and loops
// sets), every declared fleet field and config:decl first, then every machine's hash,
// ceiling and beat and every loop's hash in one pipeline. It writes nothing.
func (a *RedisApplier) Snapshot(ctx context.Context) (*Snapshot, error) {
	pipe := a.Client.Pipeline()
	machines := pipe.SMembers(ctx, MachinesKey)
	loops := pipe.SMembers(ctx, LoopsKey)
	fleetKind, _ := Lookup(KindFleet)
	fleetValues := make([]*redis.StringCmd, len(fleetKind.Fields))
	for i, f := range fleetKind.Fields {
		fleetValues[i] = pipe.Get(ctx, FleetKey(f.Name))
	}
	decl := pipe.HGetAll(ctx, DeclKey)
	if err := redisconn.Exec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("redis: read the applied names: %w", err)
	}
	snap := &Snapshot{
		Machines: map[string]View{}, Beats: map[string]*Beat{}, Revs: map[string]int64{},
		Fleet: View{},
	}
	for i, f := range fleetKind.Fields {
		snap.Fleet[f.Name] = fleetValues[i].Val()
	}
	for f, v := range decl.Val() {
		if kind, ok := strings.CutPrefix(f, "rev:"); ok {
			snap.Revs[kind], _ = strconv.ParseInt(v, 10, 64)
		}
	}
	mnames, lnames := machines.Val(), loops.Val()
	pipe = a.Client.Pipeline()
	reg := make([]*redis.MapStringStringCmd, len(mnames))
	ceiling := make([]*redis.StringCmd, len(mnames))
	beat := make([]*redis.MapStringStringCmd, len(mnames))
	for i, m := range mnames {
		reg[i] = pipe.HGetAll(ctx, MachineKey(m))
		ceiling[i] = pipe.HGet(ctx, MachineCeilingKey(m), "slots")
		beat[i] = pipe.HGetAll(ctx, BeatKey(m))
	}
	lv := make([]*redis.MapStringStringCmd, len(lnames))
	for i, n := range lnames {
		lv[i] = pipe.HGetAll(ctx, LoopKey(n))
	}
	if len(mnames)+len(lnames) > 0 {
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return nil, fmt.Errorf("redis: read the applied rows: %w", err)
		}
	}
	for i, m := range mnames {
		snap.Machines[m] = machineView(reg[i].Val(), ceiling[i].Val())
		if h := beat[i].Val(); len(h) > 0 {
			snap.Beats[m] = &Beat{OS: h["os"], Arch: h["arch"], Cores: h["ncpu"], MemoryGB: h["memory_gb"]}
		}
	}
	if _, applied := snap.Revs[KindLoop]; applied {
		snap.Loops = make(map[string]View, len(lnames))
		for i, n := range lnames {
			snap.Loops[n] = View(lv[i].Val())
		}
	}
	return snap, nil
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
		if len(names) == 0 {
			continue
		}
		pipe := a.Client.Pipeline()
		cmds := make([]*redis.StringCmd, len(names))
		for i, n := range names {
			cmds[i] = pipe.HGet(ctx, kind+":"+n+":desired", "machine")
		}
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return fmt.Errorf("redis: read %ss: %w", kind, err)
		}
		for i, n := range names {
			if cmds[i].Val() == m {
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

// --- loops and routes: a hash per row and a set of names ---------------------

// hashKind is a kind whose Redis view is one hash per row, key(name), with
// every field, name, rev and at (and extra's derived fields), and the row's
// name in the set: nova-config's own keys, read by the plays (loops) and
// the deal (routes).
type hashKind struct {
	kind  string
	set   string
	key   func(string) string
	extra func(dir, name string) []any // derived fields beside the row's; nil for none
}

var hashKinds = map[string]hashKind{
	KindLoop:  {kind: KindLoop, set: LoopsKey, key: LoopKey, extra: func(dir, n string) []any { return []any{"log", LoopLog(dir, n)} }},
	KindRoute: {kind: KindRoute, set: RoutesKey, key: RouteKey},
	KindTier:  {kind: KindTier, set: TiersKey, key: TierKey},
}

// readHashes reads the set and the stamp in one round trip, then every
// row's hash in a second; a store with no row takes the one trip alone.
// A field the hash lacks reads as the type's zero, so a hash written by hand
// short of a field is put right by the next apply.
func (a *RedisApplier) readHashes(ctx context.Context, h hashKind) (map[string]View, int64, error) {
	first := a.Client.Pipeline()
	members := first.SMembers(ctx, h.set)
	rev := first.HGet(ctx, DeclKey, revField(h.kind))
	if err := redisconn.Exec(ctx, first); err != nil {
		return nil, 0, fmt.Errorf("redis: read %s: %w", h.set, err)
	}
	names := members.Val()
	sort.Strings(names)
	hashes := make([]*redis.MapStringStringCmd, len(names))
	if len(names) > 0 {
		pipe := a.Client.Pipeline()
		for i, n := range names {
			hashes[i] = pipe.HGetAll(ctx, h.key(n))
		}
		if err := redisconn.Exec(ctx, pipe); err != nil {
			return nil, 0, fmt.Errorf("redis: read %s: %w", h.set, err)
		}
	}
	k, _ := Lookup(h.kind)
	views := make(map[string]View, len(names))
	for i, n := range names {
		hv := hashes[i].Val()
		v := View{}
		for _, f := range k.Fields {
			switch f.Type {
			case TypeInt:
				v[f.Name] = intText(hv[f.Name])
			case TypeBool:
				v[f.Name] = boolText(hv[f.Name])
			default:
				v[f.Name] = hv[f.Name]
			}
		}
		views[n] = v
	}
	return views, revValue(rev), nil
}

// boolText canonicalises a bool read from a hash: anything but a true
// spelling is "false".
func boolText(s string) string {
	b, _ := strconv.ParseBool(s)
	return strconv.FormatBool(b)
}

// writeHash writes the row's whole hash (every field, name, rev, at and the
// derived fields) and its name into the set, in one transaction. The hash
// is replaced, not merged: a field the row leaves empty is written empty.
func (a *RedisApplier) writeHash(ctx context.Context, h hashKind, row Row, idem string) error {
	k, _ := Lookup(h.kind)
	rev, _ := strings.CutPrefix(idem, "config:"+h.kind+":")
	fields := []any{"name", row.Name, "rev", rev, "at", strconv.FormatInt(a.now(), 10)}
	if h.extra != nil {
		dir := ""
		if h.kind == KindLoop {
			d, err := a.Client.Get(ctx, FleetKey("loops_dir")).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return fmt.Errorf("redis: read %s: %w", FleetKey("loops_dir"), err)
			}
			dir = d
		}
		fields = append(fields, h.extra(dir, row.Name)...)
	}
	for _, f := range k.Fields {
		fields = append(fields, f.Name, row.Fields[f.Name])
	}
	pipe := a.Client.TxPipeline()
	pipe.SAdd(ctx, h.set, row.Name)
	pipe.HSet(ctx, h.key(row.Name), fields...)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: %s %s: %w", h.kind, row.Name, err)
	}
	return nil
}

// removeHash removes the row's hash and its name, with a config-remove
// receipt in cap:log, in one transaction.
func (a *RedisApplier) removeHash(ctx context.Context, h hashKind, name, actor, idem string) error {
	pipe := a.Client.TxPipeline()
	pipe.SRem(ctx, h.set, name)
	pipe.Del(ctx, h.key(name))
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: CapLogKey, MaxLen: 100000, Approx: true, Values: map[string]any{
		"kind": "config-remove", "subject": h.kind + ":" + name, "actor": actor, "idem": idem, "at": strconv.FormatInt(a.now(), 10)}})
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: remove %s %s: %w", h.kind, name, err)
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
	if err := redisconn.Exec(ctx, pipe); err != nil {
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
// Postgres or typed configuration because measured facts come from the live
// beat. Cores is the beat's ncpu; OS, Arch and MemoryGB are ""
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
	if err := redisconn.Exec(ctx, pipe); err != nil {
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
