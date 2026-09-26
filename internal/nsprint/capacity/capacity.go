// Package capacity implements the machine child-ceiling invariant of spec 2.4:
// the sum of desired slots over every bench and every friend whose machine is
// m MUST NOT exceed machine:<m>:ceiling. capacity friend, capacity bench and
// friend hello --slots compute that sum and refuse a raise that would break it
// with exit 2 CEILING <m> <sum>/<ceiling>; nothing is clamped silently.
//
// The guard is repeated atomically inside a Redis Function (see
// internal/nsprint/fn/lua/capacity.lua), which also writes the desired hash and
// one cap:log receipt stamped with Redis TIME. Evaluate is the same guard in Go
// over a Reader, so a caller can decide the refusal before the function call
// and the rule can be tested without a live Redis. The function re-checks the
// sum after the Go fast path, so two concurrent raises cannot both win.
package capacity

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchrole"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Function names registered by internal/nsprint/fn/lua/capacity.lua. The
// library is `nova_sprint` (fn.Library); the loader prepends its header.
const (
	// FunctionDesired sets friend:<f>:desired or bench:<b>:desired under the
	// machine ceiling.
	FunctionDesired = "ns_capacity_desired"
	// FunctionMachine sets machine:<m>:ceiling.
	FunctionMachine = "ns_capacity_machine"
)

// LogKey is the global capacity receipt stream (spec 2.2).
const LogKey = "cap:log"

// Consumer kinds written into the desired hashes.
const (
	KindFriend = "friend"
	KindBench  = "bench"
)

// Consumer is one registered friend or bench with the desired slots and the
// machine from its desired hash. A consumer whose desired hash is absent reads
// as zero slots and no machine, so it never inflates a machine sum.
type Consumer struct {
	Kind    string
	Name    string
	Slots   int
	Machine string
}

// Reader is the read surface the guard needs. RedisReader reads the store;
// tests supply an in-memory implementation so the rule is exercised with no
// server.
type Reader interface {
	// Ceiling returns machine:<m>:ceiling slots and whether the hash exists.
	Ceiling(ctx context.Context, machine string) (int, bool, error)
	// Consumers returns every registered friend and bench with its desired
	// slots and machine.
	Consumers(ctx context.Context) ([]Consumer, error)
}

// Plan is the prospective outcome of a raise before anything is written.
type Plan struct {
	// Allowed is true when the prospective sum is within the ceiling.
	Allowed bool
	// Sum is the prospective sum of desired slots on the machine.
	Sum int
	// Ceiling is machine:<m>:ceiling slots.
	Ceiling int
}

// CeilingError is the exit 2 refusal of spec 2.4. Its Error text is exactly
// `CEILING <m> <sum>/<ceiling>` so the CLI prints the required line.
type CeilingError struct {
	Machine string
	Sum     int
	Ceiling int
}

func (e *CeilingError) Error() string {
	return fmt.Sprintf("CEILING %s %d/%d", e.Machine, e.Sum, e.Ceiling)
}

// ExitCode is the refusal's CLI exit status (spec 2.4).
func (e *CeilingError) ExitCode() int { return 2 }

// NameIsLoginError is the exit 2 refusal of #3604: a name mapped in
// friends:login is a login alias and never registers as a friend, so
// capacity friend refuses it by name before any ceiling check or write,
// exactly as friend hello does (#3593).
type NameIsLoginError struct {
	Name string
}

func (e *NameIsLoginError) Error() string {
	return fmt.Sprintf("NAME-IS-LOGIN %s", e.Name)
}

// ExitCode is the refusal's CLI exit status (#3604).
func (e *NameIsLoginError) ExitCode() int { return 2 }

// LoginMapKey is the hash that maps login aliases to their friend (#3092).
const LoginMapKey = "friends:login"

// Evaluate is the pure ceiling guard: it reads the machine ceiling and every
// registered consumer, substitutes the requested slots for the requesting
// consumer (on its requested machine), and reports whether the resulting sum
// fits. It never writes, so a refusal leaves the old slots in place. SetFriend
// and SetBench call Evaluate before the Redis Function; the function repeats it
// atomically to close the read-then-write race.
func Evaluate(ctx context.Context, r Reader, machine, kind, name string, slots int) (Plan, error) {
	if r == nil {
		return Plan{}, fmt.Errorf("capacity: nil reader")
	}
	if machine == "" {
		return Plan{}, fmt.Errorf("capacity: machine is required")
	}
	if kind != KindFriend && kind != KindBench {
		return Plan{}, fmt.Errorf("capacity: kind must be %s or %s", KindFriend, KindBench)
	}
	if slots < 0 {
		return Plan{}, fmt.Errorf("capacity: slots must be nonnegative")
	}
	var ceiling int
	var ok bool
	var consumers []Consumer
	var err error
	if snap, one := r.(loginSnapshotReader); one {
		// #3604: the login-aware read checks friends:login in the same
		// pipeline as the ceiling and consumers, so a mapped login is refused
		// NAME-IS-LOGIN without an extra round trip and before the ceiling.
		var isLogin bool
		ceiling, ok, consumers, isLogin, err = snap.snapshotLogin(ctx, machine, kind, name)
		if err == nil && isLogin {
			return Plan{}, &NameIsLoginError{Name: name}
		}
	} else if snap, one := r.(snapshotReader); one {
		ceiling, ok, consumers, err = snap.Snapshot(ctx, machine)
	} else {
		ceiling, ok, err = r.Ceiling(ctx, machine)
		if err == nil && ok {
			consumers, err = r.Consumers(ctx)
		}
	}
	if err != nil {
		return Plan{}, err
	}
	if !ok {
		return Plan{}, fmt.Errorf("capacity: machine %s has no ceiling; run capacity machine %s <n>", machine, machine)
	}
	sum := 0
	counted := false
	for _, c := range consumers {
		if c.Kind == kind && c.Name == name {
			// The requester moves to (or stays on) this machine with the
			// requested slots; its old contribution is replaced, not added.
			counted = true
			sum += slots
			continue
		}
		if c.Machine == machine {
			sum += c.Slots
		}
	}
	if !counted {
		sum += slots
	}
	return Plan{Allowed: sum <= ceiling, Sum: sum, Ceiling: ceiling}, nil
}

// RedisReader adapts the shared store to the guard's read surface.
type RedisReader struct {
	Store *store.Store
}

// Ceiling reads machine:<m>:ceiling slots.
func (r RedisReader) Ceiling(ctx context.Context, machine string) (int, bool, error) {
	if r.Store == nil {
		return 0, false, fmt.Errorf("capacity: nil store")
	}
	slots, err := r.Store.Client().HGet(ctx, "machine:"+machine+":ceiling", "slots").Int()
	if err == redis.Nil {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("capacity: read machine %s ceiling: %w", machine, err)
	}
	return slots, true, nil
}

// snapshotReader is a Reader that reads the ceiling and every consumer in one
// round trip; Evaluate prefers it (#3265).
type snapshotReader interface {
	Snapshot(ctx context.Context, machine string) (int, bool, []Consumer, error)
}

// loginSnapshotReader is a snapshotReader that also reports whether name is a
// mapped login (kind friend) in the same pipeline (#3604). Evaluate prefers it
// over snapshotReader, so NAME-IS-LOGIN is refused before the ceiling check.
type loginSnapshotReader interface {
	snapshotLogin(ctx context.Context, machine, kind, name string) (ceiling int, ok bool, consumers []Consumer, isLogin bool, err error)
}

// snapshotLogin reads machine:<m>:ceiling slots, every consumer's desired hash
// and, for a friend, whether its name is bound in friends:login, in one
// pipeline (#3604): still one round trip for any number of consumers.
func (r RedisReader) snapshotLogin(ctx context.Context, machine, kind, name string) (int, bool, []Consumer, bool, error) {
	if r.Store == nil {
		return 0, false, nil, false, fmt.Errorf("capacity: nil store")
	}
	pipe := r.Store.Client().Pipeline()
	ceilingCmd := pipe.HGet(ctx, MachineCeilingKey(machine), "slots")
	consumersCmd := pipe.FCall(ctx, "ns_capacity_consumers", nil)
	var loginCmd *redis.BoolCmd
	if kind == KindFriend {
		loginCmd = pipe.HExists(ctx, LoginMapKey, name)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, false, nil, false, fmt.Errorf("capacity: read machine %s and consumers: %w", machine, err)
	}
	if err := consumersCmd.Err(); err != nil {
		return 0, false, nil, false, fmt.Errorf("capacity: read consumers: %w", err)
	}
	consumers, err := parseConsumers(consumersCmd.Val())
	if err != nil {
		return 0, false, nil, false, err
	}
	ceiling, err := ceilingCmd.Int()
	if err == redis.Nil {
		return 0, false, consumers, loginValue(loginCmd), nil
	}
	if err != nil {
		return 0, false, nil, false, fmt.Errorf("capacity: read machine %s ceiling: %w", machine, err)
	}
	return ceiling, true, consumers, loginValue(loginCmd), nil
}

func loginValue(cmd *redis.BoolCmd) bool {
	if cmd == nil {
		return false
	}
	return cmd.Val()
}

// Snapshot reads machine:<m>:ceiling slots and every consumer's desired hash
// (ns_capacity_consumers) in one pipeline: one round trip for any number of
// friends and benches (#3265; it was 2 + F + B serial reads).
func (r RedisReader) Snapshot(ctx context.Context, machine string) (int, bool, []Consumer, error) {
	if r.Store == nil {
		return 0, false, nil, fmt.Errorf("capacity: nil store")
	}
	pipe := r.Store.Client().Pipeline()
	ceilingCmd := pipe.HGet(ctx, MachineCeilingKey(machine), "slots")
	consumersCmd := pipe.FCallRO(ctx, "ns_capacity_consumers", nil)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return 0, false, nil, fmt.Errorf("capacity: read machine %s and consumers: %w", machine, err)
	}
	if err := consumersCmd.Err(); err != nil {
		return 0, false, nil, fmt.Errorf("capacity: read consumers: %w", err)
	}
	consumers, err := parseConsumers(consumersCmd.Val())
	if err != nil {
		return 0, false, nil, err
	}
	ceiling, err := ceilingCmd.Int()
	if err == redis.Nil {
		return 0, false, consumers, nil
	}
	if err != nil {
		return 0, false, nil, fmt.Errorf("capacity: read machine %s ceiling: %w", machine, err)
	}
	return ceiling, true, consumers, nil
}

// Consumers reads the friends and benches registries and the desired hash of
// each in one round trip (the ns_capacity_consumers function).
func (r RedisReader) Consumers(ctx context.Context) ([]Consumer, error) {
	if r.Store == nil {
		return nil, fmt.Errorf("capacity: nil store")
	}
	cmd := r.Store.Client().FCallRO(ctx, "ns_capacity_consumers", nil)
	if err := cmd.Err(); err != nil {
		return nil, fmt.Errorf("capacity: read consumers: %w", err)
	}
	return parseConsumers(cmd.Val())
}

// parseConsumers reads the flat kind, name, slots, machine reply of
// ns_capacity_consumers.
func parseConsumers(reply any) ([]Consumer, error) {
	values, ok := reply.([]any)
	if !ok || len(values)%4 != 0 {
		return nil, fmt.Errorf("capacity: consumers reply %T length %d", reply, len(values))
	}
	out := make([]Consumer, 0, len(values)/4)
	for i := 0; i < len(values); i += 4 {
		slots := 0
		if text := fmt.Sprint(values[i+2]); text != "" {
			var err error
			slots, err = strconv.Atoi(text)
			if err != nil {
				return nil, fmt.Errorf("capacity: %s %s slots %q: %w", values[i], values[i+1], values[i+2], err)
			}
		}
		out = append(out, Consumer{Kind: fmt.Sprint(values[i]), Name: fmt.Sprint(values[i+1]), Slots: slots, Machine: fmt.Sprint(values[i+3])})
	}
	return out, nil
}

// DesiredKey is the desired hash key of one consumer (spec 2.2).
func DesiredKey(kind, name string) string {
	return kind + ":" + name + ":desired"
}

// MachineCeilingKey is the ceiling hash key of one machine (spec 2.2).
func MachineCeilingKey(machine string) string {
	return "machine:" + machine + ":ceiling"
}

// Result is the outcome of one accepted capacity write.
type Result struct {
	Status  string
	Machine string
	Sum     int
	Ceiling int
	Slots   int
}

// SetFriend writes friend:<f>:desired under the machine ceiling.
func SetFriend(ctx context.Context, st *store.Store, name, machine string, slots int, actor, idem string) (Result, error) {
	return setDesired(ctx, st, KindFriend, name, machine, slots, actor, idem)
}

// DesiredOpts are the optional seventh and eighth args of
// ns_capacity_desired (#3206 rev 4 PR A). Paused "" keeps the stored value,
// "0" or "1" sets it, on a friend or a bench (#4308: worker pause|resume is
// the verb over it); the eighth arg (register) is always 0: since #2934 every
// desired write adds the friend to `friends` (no beat is written). The zero
// value is the six-arg call.
type DesiredOpts struct {
	Paused string
	// Legs (#3349) is a bench's declared CI legs, the ninth arg, already
	// normalized by NormalizeLegs; "" keeps the stored list.
	Legs string
	// Role (#3634) is a bench's registry role, the tenth arg: benchrole.Friends
	// or benchrole.Fleet; "" keeps the stored role. Legs on a bench whose role
	// is or becomes friends is refused with a *benchrole.Error.
	Role string
	// Kinds and Tiers (#4270) are the consumer's copy filters, the eleventh
	// and twelfth args: comma lists the card moves' TM.may reads (kinds of
	// work, read, fix; tiers by name). "" keeps the stored value, Clear
	// ("-") clears it.
	Kinds, Tiers string
}

// Clear is the DesiredOpts value that clears a stored filter (kinds, tiers).
const Clear = "-"

// NormalizeKinds turns a --kinds value ("work,read" or "work read") into the
// comma list ns_capacity_desired stores: each name one of work, read, fix,
// duplicates dropped; "" is Clear.
func NormalizeKinds(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return Clear, nil
	}
	var kinds []string
	seen := map[string]bool{}
	for _, k := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		switch k {
		case "work", "read", "fix":
		default:
			return "", fmt.Errorf("kind %q: want work, read or fix", k)
		}
		if !seen[k] {
			seen[k] = true
			kinds = append(kinds, k)
		}
	}
	return strings.Join(kinds, ","), nil
}

// NormalizeTiers turns a --tiers value ("frontier,pro" or "frontier pro")
// into the comma list ns_capacity_desired stores: each name one of the three
// model types (cardhdr.Routes: frontier, pro, flash), duplicates dropped;
// "" is Clear. The list is what the worker advertises it can run; a worker
// with none stored is cardhdr.DefaultTiers.
func NormalizeTiers(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return Clear, nil
	}
	var tiers []string
	seen := map[string]bool{}
	for _, t := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if !cardhdr.IsRoute(t) {
			return "", fmt.Errorf("tier %q: want %s", t, cardhdr.RouteList)
		}
		if !seen[t] {
			seen[t] = true
			tiers = append(tiers, t)
		}
	}
	return strings.Join(tiers, ","), nil
}

// NormalizeLegs turns a --legs value ("go,schema" or "go schema") into the
// comma-joined list ns_capacity_desired stores, in the given order with
// duplicates dropped. A leg name is letters, digits, '_' or '-'.
func NormalizeLegs(raw string) (string, error) {
	var legs []string
	seen := map[string]bool{}
	for _, leg := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		for _, r := range leg {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return "", fmt.Errorf("leg %q: want letters, digits, _ or -", leg)
			}
		}
		if !seen[leg] {
			seen[leg] = true
			legs = append(legs, leg)
		}
	}
	if len(legs) == 0 {
		return "", errors.New("no leg named")
	}
	return strings.Join(legs, ","), nil
}

// SetBenchWith is SetBench with the legs arg (#3349). An identical write
// returns Status SAME and writes nothing.
func SetBenchWith(ctx context.Context, st *store.Store, name, machine string, slots int, actor, idem string, opts DesiredOpts) (Result, error) {
	return setDesiredWith(ctx, st, KindBench, name, machine, slots, actor, idem, opts)
}

// ErrUnregistered is the UNREGISTERED status of a capacity function (the
// sprint plan's unknown consumer; ns_capacity_desired registers since #2934).
var ErrUnregistered = errors.New("UNREGISTERED")

// SetFriendWith is SetFriend with the paused and register args. An identical
// write returns Status SAME and writes nothing.
func SetFriendWith(ctx context.Context, st *store.Store, name, machine string, slots int, actor, idem string, opts DesiredOpts) (Result, error) {
	return setDesiredWith(ctx, st, KindFriend, name, machine, slots, actor, idem, opts)
}

func setDesired(ctx context.Context, st *store.Store, kind, name, machine string, slots int, actor, idem string) (Result, error) {
	return setDesiredWith(ctx, st, kind, name, machine, slots, actor, idem, DesiredOpts{})
}

func setDesiredWith(ctx context.Context, st *store.Store, kind, name, machine string, slots int, actor, idem string, opts DesiredOpts) (Result, error) {
	if st == nil {
		return Result{}, fmt.Errorf("capacity %s: nil store", kind)
	}
	if name == "" {
		return Result{}, fmt.Errorf("capacity %s: name is required", kind)
	}
	plan, err := Evaluate(ctx, RedisReader{Store: st}, machine, kind, name, slots)
	if err != nil {
		return Result{}, err
	}
	if !plan.Allowed {
		return Result{}, &CeilingError{Machine: machine, Sum: plan.Sum, Ceiling: plan.Ceiling}
	}
	fargs := []any{kind, name, strconv.Itoa(slots), machine, actor, idem}
	filters := opts.Kinds != "" || opts.Tiers != ""
	if opts.Paused != "" || opts.Legs != "" || opts.Role != "" || filters {
		fargs = append(fargs, opts.Paused, "0")
		if opts.Legs != "" || opts.Role != "" || filters {
			fargs = append(fargs, opts.Legs)
		}
		if opts.Role != "" || filters {
			fargs = append(fargs, opts.Role)
		}
		if filters {
			fargs = append(fargs, opts.Kinds, opts.Tiers)
		}
	}
	reply, err := st.Client().FCall(ctx, FunctionDesired, nil, fargs...).Result()
	if err != nil {
		return Result{}, fmt.Errorf("capacity %s %s: %w", kind, name, err)
	}
	return parseDesiredReply(reply, kind, name, machine, slots)
}

// SetMachine writes machine:<m>:ceiling and sets budget if resources are provided.
func SetMachine(ctx context.Context, st *store.Store, machine string, slots, cores, memGB int, actor, idem string) (Result, error) {
	return SetMachineBudget(ctx, st, machine, slots, cores, memGB, 0, 0, actor, idem)
}

// SetMachineBudget writes machine:<m>:ceiling and machine:<m>:budget (spec 5.1).
func SetMachineBudget(ctx context.Context, st *store.Store, machine string, slots, cores, memGB, cpuMilli, memMB int, actor, idem string) (Result, error) {
	if st == nil {
		return Result{}, fmt.Errorf("capacity machine: nil store")
	}
	if machine == "" {
		return Result{}, fmt.Errorf("capacity machine: machine is required")
	}
	if slots < 0 {
		return Result{}, fmt.Errorf("capacity machine: slots must be nonnegative")
	}
	if cores < 0 || memGB < 0 || cpuMilli < 0 || memMB < 0 {
		return Result{}, fmt.Errorf("capacity machine: resources must be nonnegative")
	}
	consumers, err := RedisReader{Store: st}.Consumers(ctx)
	if err != nil {
		return Result{}, err
	}
	sum := 0
	for _, c := range consumers {
		if c.Machine == machine {
			sum += c.Slots
		}
	}
	if slots < sum {
		return Result{}, &CeilingError{Machine: machine, Sum: sum, Ceiling: slots}
	}
	reply, err := st.Client().FCall(ctx, FunctionMachine, nil,
		machine, strconv.Itoa(slots), optionalPositive(cores), optionalPositive(memGB),
		actor, idem, optionalPositive(cpuMilli), optionalPositive(memMB)).Result()
	if err != nil {
		return Result{}, fmt.Errorf("capacity machine %s: %w", machine, err)
	}
	return parseMachineReply(reply, machine, slots)
}

func replyValues(reply any) ([]any, error) {
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("capacity: unexpected function reply %T", reply)
	}
	return values, nil
}

func replyInt(v any) int {
	n, _ := strconv.Atoi(fmt.Sprint(v))
	return n
}

func parseDesiredReply(reply any, kind, name, machine string, slots int) (Result, error) {
	values, err := replyValues(reply)
	if err != nil {
		return Result{}, err
	}
	status, _ := values[0].(string)
	switch status {
	case "SET", "SAME":
	case "UNREGISTERED":
		return Result{}, fmt.Errorf("capacity %s %s: %w", kind, name, ErrUnregistered)
	case "NAME-IS-LOGIN":
		return Result{}, &NameIsLoginError{Name: name}
	case "ROLE":
		role := benchrole.Friends
		if len(values) > 1 {
			role = fmt.Sprint(values[1])
		}
		return Result{}, benchrole.Refused(name, role, "no CI legs on a friends bench (capacity bench --legs is the CI runner registration)")
	case "CEILING":
		result := Result{Status: status, Machine: machine, Slots: slots}
		if len(values) > 2 {
			result.Sum = replyInt(values[2])
		}
		if len(values) > 3 {
			result.Ceiling = replyInt(values[3])
		}
		return Result{}, &CeilingError{Machine: machine, Sum: result.Sum, Ceiling: result.Ceiling}
	default:
		return Result{}, fmt.Errorf("capacity %s %s: unexpected status %q", kind, name, status)
	}
	result := Result{Status: status, Machine: machine, Slots: slots}
	if len(values) > 2 {
		result.Sum = replyInt(values[2])
	}
	if len(values) > 3 {
		result.Ceiling = replyInt(values[3])
	}
	return result, nil
}

func parseMachineReply(reply any, machine string, slots int) (Result, error) {
	values, err := replyValues(reply)
	if err != nil {
		return Result{}, err
	}
	status, _ := values[0].(string)
	switch status {
	case "SET":
	case "CEILING":
		result := Result{Status: status, Machine: machine, Slots: slots}
		if len(values) > 2 {
			result.Sum = replyInt(values[2])
		}
		if len(values) > 3 {
			result.Ceiling = replyInt(values[3])
		}
		return Result{}, &CeilingError{Machine: machine, Sum: result.Sum, Ceiling: result.Ceiling}
	default:
		return Result{}, fmt.Errorf("capacity machine %s: unexpected status %q", machine, status)
	}
	result := Result{Status: status, Machine: machine, Slots: slots}
	if len(values) > 2 {
		result.Sum = replyInt(values[2])
	}
	if len(values) > 3 {
		result.Ceiling = replyInt(values[3])
	}
	return result, nil
}

func optionalPositive(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}
