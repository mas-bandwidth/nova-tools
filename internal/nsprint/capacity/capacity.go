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
	"fmt"
	"strconv"

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
	ceiling, ok, err := r.Ceiling(ctx, machine)
	if err != nil {
		return Plan{}, err
	}
	if !ok {
		return Plan{}, fmt.Errorf("capacity: machine %s has no ceiling; run capacity machine %s <n>", machine, machine)
	}
	consumers, err := r.Consumers(ctx)
	if err != nil {
		return Plan{}, err
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

// Consumers reads the friends and benches registries and the desired hash of
// each.
func (r RedisReader) Consumers(ctx context.Context) ([]Consumer, error) {
	if r.Store == nil {
		return nil, fmt.Errorf("capacity: nil store")
	}
	client := r.Store.Client()
	friends, err := client.SMembers(ctx, "friends").Result()
	if err != nil {
		return nil, fmt.Errorf("capacity: read friends: %w", err)
	}
	benches, err := client.SMembers(ctx, "benches").Result()
	if err != nil {
		return nil, fmt.Errorf("capacity: read benches: %w", err)
	}
	out := make([]Consumer, 0, len(friends)+len(benches))
	for _, name := range friends {
		c, err := readDesired(ctx, client, KindFriend, name)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	for _, name := range benches {
		c, err := readDesired(ctx, client, KindBench, name)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
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

func readDesired(ctx context.Context, client *redis.Client, kind, name string) (Consumer, error) {
	values, err := client.HMGet(ctx, DesiredKey(kind, name), "slots", "machine").Result()
	if err != nil {
		return Consumer{}, fmt.Errorf("capacity: read %s %s desired: %w", kind, name, err)
	}
	c := Consumer{Kind: kind, Name: name}
	if len(values) > 0 {
		if text, ok := values[0].(string); ok && text != "" {
			slots, err := strconv.Atoi(text)
			if err != nil {
				return Consumer{}, fmt.Errorf("capacity: %s %s slots %q: %w", kind, name, text, err)
			}
			c.Slots = slots
		}
	}
	if len(values) > 1 {
		if text, ok := values[1].(string); ok {
			c.Machine = text
		}
	}
	return c, nil
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

// SetBench writes bench:<b>:desired under the machine ceiling.
func SetBench(ctx context.Context, st *store.Store, name, machine string, slots int, actor, idem string) (Result, error) {
	return setDesired(ctx, st, KindBench, name, machine, slots, actor, idem)
}

func setDesired(ctx context.Context, st *store.Store, kind, name, machine string, slots int, actor, idem string) (Result, error) {
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
	reply, err := st.Client().FCall(ctx, FunctionDesired, nil,
		kind, name, strconv.Itoa(slots), machine, actor, idem).Result()
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
