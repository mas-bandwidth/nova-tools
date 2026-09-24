package capacity

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

const (
	FunctionBudgetSet    = "ns_budget_set"
	FunctionBudgetTake   = "ns_budget_take"
	FunctionBudgetRenew  = "ns_budget_renew"
	FunctionBudgetGive   = "ns_budget_give"
	FunctionBudgetDebits = "ns_budget_debits"

	KindLand  = "land"
	KindSwarm = "swarm"
	KindCI    = "ci"
)

// Budget is the resource capacity on one machine (spec 5.1).
type Budget struct {
	CPUMilli int `json:"cpu_milli"`
	MemMB    int `json:"mem_mb"`
}

// Debit is one consumer's active allocation of cpu and memory (spec 5.1).
type Debit struct {
	Consumer string `json:"consumer"`
	Machine  string `json:"machine"`
	CPUMilli int    `json:"cpu_milli"`
	MemMB    int    `json:"mem_mb"`
	PGID     int    `json:"pgid"`
	RenewAt  int64  `json:"renew_at"`
	State    string `json:"state"` // "live", "quarantined"
	Kind     string `json:"kind"`  // "land", "swarm", "ci"
}

// TakeRequest is the input to atomic budget admission (spec 5.1).
type TakeRequest struct {
	Machine  string
	Consumer string
	CPUMilli int
	MemMB    int
	TTLMs    int
	PGID     int
	Kind     string
	Actor    string
	Idem     string
}

// BudgetResult is the outcome of a budget take.
type BudgetResult struct {
	Allowed  bool   `json:"allowed"`
	Machine  string `json:"machine"`
	UsedCPU  int    `json:"used_cpu"`
	TotalCPU int    `json:"total_cpu"`
	UsedMem  int    `json:"used_mem"`
	TotalMem int    `json:"total_mem"`
}

// BudgetError is returned when a take exceeds available budget.
type BudgetError struct {
	Machine  string
	UsedCPU  int
	TotalCPU int
	UsedMem  int
	TotalMem int
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("NOBUDGET %s cpu=%d/%d mem=%d/%d", e.Machine, e.UsedCPU, e.TotalCPU, e.UsedMem, e.TotalMem)
}

func (e *BudgetError) ExitCode() int { return 2 }

// StillAliveError is returned when giving a quarantined debit whose process group is still alive.
type StillAliveError struct {
	Consumer string
	PGID     string
}

func (e *StillAliveError) Error() string {
	return fmt.Sprintf("STILLALIVE %s pgid=%s", e.Consumer, e.PGID)
}

func (e *StillAliveError) ExitCode() int { return 2 }

// GiveRequest is the input to returning budget capacity.
type GiveRequest struct {
	Machine   string
	Consumer  string
	PGID      int
	Confirmed bool
}

// SetBudget writes machine:<m>:budget (cpu_milli, mem_mb).
func SetBudget(ctx context.Context, st *store.Store, machine string, cpuMilli, memMB int, actor, idem string) error {
	if st == nil {
		return fmt.Errorf("budget set: nil store")
	}
	if machine == "" {
		return fmt.Errorf("budget set: machine is required")
	}
	reply, err := st.Client().FCall(ctx, FunctionBudgetSet, nil,
		machine, strconv.Itoa(cpuMilli), strconv.Itoa(memMB), actor, idem).Result()
	if err != nil {
		return fmt.Errorf("budget set %s: %w", machine, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return fmt.Errorf("budget set %s: unexpected reply %T", machine, reply)
	}
	if status, _ := values[0].(string); status != "SET" {
		return fmt.Errorf("budget set %s: unexpected status %q", machine, status)
	}
	return nil
}

// GetBudget reads machine:<m>:budget.
func GetBudget(ctx context.Context, st *store.Store, machine string) (Budget, bool, error) {
	if st == nil {
		return Budget{}, false, fmt.Errorf("budget get: nil store")
	}
	values, err := st.Client().HMGet(ctx, "machine:"+machine+":budget", "cpu_milli", "mem_mb").Result()
	if err != nil {
		return Budget{}, false, err
	}
	if len(values) < 2 || values[0] == nil || values[1] == nil {
		return Budget{}, false, nil
	}
	cpuStr, _ := values[0].(string)
	memStr, _ := values[1].(string)
	cpu, _ := strconv.Atoi(cpuStr)
	mem, _ := strconv.Atoi(memStr)
	if cpu == 0 && mem == 0 {
		return Budget{}, false, nil
	}
	return Budget{CPUMilli: cpu, MemMB: mem}, true, nil
}

// Take debits machine:<m>:budget atomically (spec 5.1).
func Take(ctx context.Context, st *store.Store, req TakeRequest) (BudgetResult, error) {
	if st == nil {
		return BudgetResult{}, fmt.Errorf("budget take: nil store")
	}
	if req.Machine == "" {
		return BudgetResult{}, fmt.Errorf("budget take: machine is required")
	}
	if req.Consumer == "" {
		return BudgetResult{}, fmt.Errorf("budget take: consumer is required")
	}
	ttlMs := req.TTLMs
	if ttlMs <= 0 {
		ttlMs = 30000
	}
	pgidStr := ""
	if req.PGID != 0 {
		pgidStr = strconv.Itoa(req.PGID)
	}
	reply, err := st.Client().FCall(ctx, FunctionBudgetTake, nil,
		req.Machine, req.Consumer, strconv.Itoa(req.CPUMilli), strconv.Itoa(req.MemMB),
		strconv.Itoa(ttlMs), pgidStr, req.Kind).Result()
	if err != nil {
		return BudgetResult{}, fmt.Errorf("budget take %s %s: %w", req.Machine, req.Consumer, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return BudgetResult{}, fmt.Errorf("budget take: unexpected reply %T", reply)
	}
	status, _ := values[0].(string)
	res := BudgetResult{Machine: req.Machine}
	if len(values) > 2 {
		res.UsedCPU = replyInt(values[2])
	}
	if len(values) > 3 {
		res.TotalCPU = replyInt(values[3])
	}
	if len(values) > 4 {
		res.UsedMem = replyInt(values[4])
	}
	if len(values) > 5 {
		res.TotalMem = replyInt(values[5])
	}
	if status == "NOBUDGET" {
		res.Allowed = false
		return res, &BudgetError{
			Machine:  res.Machine,
			UsedCPU:  res.UsedCPU,
			TotalCPU: res.TotalCPU,
			UsedMem:  res.UsedMem,
			TotalMem: res.TotalMem,
		}
	}
	if status != "OK" {
		return res, fmt.Errorf("budget take %s %s: unexpected status %q", req.Machine, req.Consumer, status)
	}
	res.Allowed = true
	return res, nil
}

// Renew refreshes the renew_at timestamp and optionally updates pgid (spec 5.1).
func Renew(ctx context.Context, st *store.Store, machine, consumer string, pgid, ttlMs int) error {
	if st == nil {
		return fmt.Errorf("budget renew: nil store")
	}
	pgidStr := ""
	if pgid != 0 {
		pgidStr = strconv.Itoa(pgid)
	}
	reply, err := st.Client().FCall(ctx, FunctionBudgetRenew, nil,
		machine, consumer, pgidStr, strconv.Itoa(ttlMs)).Result()
	if err != nil {
		return fmt.Errorf("budget renew %s: %w", consumer, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return fmt.Errorf("budget renew %s: unexpected reply %T", consumer, reply)
	}
	if status, _ := values[0].(string); status != "OK" {
		return fmt.Errorf("budget renew %s: %s", consumer, status)
	}
	return nil
}

// Give credits capacity back when a consumer finishes or is reaped (spec 5.1).
func Give(ctx context.Context, st *store.Store, req GiveRequest) error {
	if st == nil {
		return fmt.Errorf("budget give: nil store")
	}
	pgidStr := ""
	if req.PGID != 0 {
		pgidStr = strconv.Itoa(req.PGID)
	}
	confirmedStr := ""
	if req.Confirmed {
		confirmedStr = "confirmed"
	}
	reply, err := st.Client().FCall(ctx, FunctionBudgetGive, nil,
		req.Machine, req.Consumer, pgidStr, confirmedStr).Result()
	if err != nil {
		return fmt.Errorf("budget give %s: %w", req.Consumer, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return fmt.Errorf("budget give %s: unexpected reply %T", req.Consumer, reply)
	}
	status, _ := values[0].(string)
	if status == "STILLALIVE" {
		pgid := ""
		if len(values) > 2 {
			pgid = fmt.Sprint(values[2])
		}
		return &StillAliveError{Consumer: req.Consumer, PGID: pgid}
	}
	if status != "OK" {
		return fmt.Errorf("budget give %s: unexpected status %q", req.Consumer, status)
	}
	return nil
}

// ListDebits reads all active and quarantined debits on a machine.
func ListDebits(ctx context.Context, st *store.Store, machine string) ([]Debit, error) {
	if st == nil {
		return nil, fmt.Errorf("budget list: nil store")
	}
	reply, err := st.Client().FCall(ctx, FunctionBudgetDebits, nil, machine).Result()
	if err != nil {
		return nil, fmt.Errorf("budget debits %s: %w", machine, err)
	}
	values, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("budget debits %s: unexpected reply %T", machine, reply)
	}
	var out []Debit
	for _, v := range values {
		str, ok := v.(string)
		if !ok {
			continue
		}
		parts := strings.Split(str, ":")
		if len(parts) < 6 {
			continue
		}
		cpu, _ := strconv.Atoi(parts[1])
		mem, _ := strconv.Atoi(parts[2])
		pgid, _ := strconv.Atoi(parts[3])
		renewAt, _ := strconv.ParseInt(parts[4], 10, 64)
		kind := ""
		if len(parts) >= 7 {
			kind = parts[6]
		}
		out = append(out, Debit{
			Consumer: parts[0],
			Machine:  machine,
			CPUMilli: cpu,
			MemMB:    mem,
			PGID:     pgid,
			RenewAt:  renewAt,
			State:    parts[5],
			Kind:     kind,
		})
	}
	return out, nil
}

// Reap cleans up quarantined or orphaned debits whose process group is confirmed gone (spec 5.1).
func Reap(ctx context.Context, st *store.Store, machine string) ([]Debit, error) {
	debits, err := ListDebits(ctx, st, machine)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	var reaped []Debit
	for _, d := range debits {
		isQuarantined := d.State == "quarantined" || (d.RenewAt > 0 && now > d.RenewAt)
		if !isQuarantined {
			continue
		}
		confirmed := false
		if d.PGID > 0 {
			// kill -0 -<pgid> answers ESRCH?
			err := syscall.Kill(-d.PGID, 0)
			if errors.Is(err, syscall.ESRCH) {
				confirmed = true
			} else if err == nil {
				// Process group is still running: SIGKILL it per 5.1
				_ = syscall.Kill(-d.PGID, syscall.SIGKILL)
				for i := 0; i < 10; i++ {
					time.Sleep(10 * time.Millisecond)
					if kerr := syscall.Kill(-d.PGID, 0); errors.Is(kerr, syscall.ESRCH) {
						confirmed = true
						break
					}
				}
			}
		} else {
			confirmed = true
		}

		if confirmed {
			err := Give(ctx, st, GiveRequest{
				Machine:   machine,
				Consumer:  d.Consumer,
				PGID:      d.PGID,
				Confirmed: true,
			})
			if err == nil {
				reaped = append(reaped, d)
			}
		}
	}
	return reaped, nil
}
