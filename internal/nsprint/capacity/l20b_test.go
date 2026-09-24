//go:build unix

package capacity_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
)

// TestL20b is control L20b of issue #3139 rev 7:
// 1,000 concurrent takes from land, swarm and ci on one machine never exceed its budget,
// and a killed consumer's debit returns only after its process group is confirmed gone.
func TestL20b(t *testing.T) {
	st, c := redisControl(t)
	ctx := context.Background()

	const machine = "bench-studio"
	const budgetCPU = 32000 // 32 cores (in milli)
	const budgetMem = 65536 // 64 GB (in MB)

	// Set machine budget
	if err := capacity.SetBudget(ctx, st, machine, budgetCPU, budgetMem, "operator", ""); err != nil {
		t.Fatalf("set budget: %v", err)
	}

	// Verify budget is read
	budget, ok, err := capacity.GetBudget(ctx, st, machine)
	if err != nil || !ok || budget.CPUMilli != budgetCPU || budget.MemMB != budgetMem {
		t.Fatalf("get budget got %+v, ok=%v, err=%v", budget, ok, err)
	}

	// 1. 1,000 concurrent takes from land, swarm, and ci on one machine
	const totalTakes = 1000
	var (
		maxActiveCPU int64
		maxActiveMem int64
		successCount int64
		refusedCount int64
		wg           sync.WaitGroup
	)

	updateMax := func(target *int64, val int64) {
		for {
			cur := atomic.LoadInt64(target)
			if val <= cur || atomic.CompareAndSwapInt64(target, cur, val) {
				break
			}
		}
	}

	wg.Add(totalTakes)
	for i := 0; i < totalTakes; i++ {
		go func(id int) {
			defer wg.Done()

			var kind string
			var cpu, mem int
			switch id % 3 {
			case 0:
				kind = capacity.KindLand
				cpu = 4000
				mem = 8192
			case 1:
				kind = capacity.KindSwarm
				cpu = 2000
				mem = 4096
			case 2:
				kind = capacity.KindCI
				cpu = 3000
				mem = 6144
			}

			consumer := fmt.Sprintf("%s-worker-%d", kind, id)
			res, err := capacity.Take(ctx, st, capacity.TakeRequest{
				Machine:  machine,
				Consumer: consumer,
				CPUMilli: cpu,
				MemMB:    mem,
				TTLMs:    30000,
				Kind:     kind,
				Actor:    "test",
			})

			if err != nil {
				var bErr *capacity.BudgetError
				if errors.As(err, &bErr) {
					atomic.AddInt64(&refusedCount, 1)
					return
				}
				t.Errorf("unexpected take error: %v", err)
				return
			}

			if !res.Allowed {
				t.Errorf("expected allowed=true when err is nil")
				return
			}

			atomic.AddInt64(&successCount, 1)
			updateMax(&maxActiveCPU, int64(res.UsedCPU))
			updateMax(&maxActiveMem, int64(res.UsedMem))

			// INVARIANT: Never exceed machine budget under concurrency
			if res.UsedCPU > budgetCPU {
				t.Errorf("INVARIANT VIOLATED: active CPU %d > budget %d", res.UsedCPU, budgetCPU)
			}
			if res.UsedMem > budgetMem {
				t.Errorf("INVARIANT VIOLATED: active Mem %d > budget %d", res.UsedMem, budgetMem)
			}

			// Simulate work
			time.Sleep(time.Duration(100+(id%20)*10) * time.Microsecond)

			// Give back capacity
			if err := capacity.Give(ctx, st, capacity.GiveRequest{
				Machine:  machine,
				Consumer: consumer,
			}); err != nil {
				t.Errorf("give %s: %v", consumer, err)
			}
		}(i)
	}

	wg.Wait()

	if successCount == 0 {
		t.Fatalf("expected some takes to succeed, got 0")
	}
	if refusedCount == 0 {
		t.Fatalf("expected some takes to be refused under 1000 concurrent requests, got 0")
	}
	if maxActiveCPU > budgetCPU {
		t.Fatalf("max active CPU %d exceeded budget %d", maxActiveCPU, budgetCPU)
	}
	if maxActiveMem > budgetMem {
		t.Fatalf("max active Mem %d exceeded budget %d", maxActiveMem, budgetMem)
	}

	// Verify all debits returned
	debits, err := capacity.ListDebits(ctx, st, machine)
	if err != nil {
		t.Fatalf("list debits: %v", err)
	}
	if len(debits) != 0 {
		t.Fatalf("expected 0 debits after completion, got %d", len(debits))
	}

	// 2. A killed consumer's debit returns only after its process group is confirmed gone
	// Spawn a real child process in its own process group
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child sleep process: %v", err)
	}
	pgid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	const killedConsumer = "killed-gate-worker"
	// Take debit taking most of the budget
	_, err = capacity.Take(ctx, st, capacity.TakeRequest{
		Machine:  machine,
		Consumer: killedConsumer,
		CPUMilli: 30000,
		MemMB:    60000,
		TTLMs:    50, // short TTL so it expires quickly
		PGID:     pgid,
		Kind:     capacity.KindLand,
		Actor:    "land",
	})
	if err != nil {
		t.Fatalf("take killedConsumer: %v", err)
	}

	// Another consumer tries to take 5,000 CPU - should be refused because 30,000 is held
	_, err = capacity.Take(ctx, st, capacity.TakeRequest{
		Machine:  machine,
		Consumer: "other-worker",
		CPUMilli: 5000,
		MemMB:    10000,
		Kind:     capacity.KindLand,
	})
	if err == nil {
		t.Fatalf("expected NOBUDGET while killedConsumer holds budget")
	}

	// Wait for TTL to expire and debit to become quarantined
	time.Sleep(80 * time.Millisecond)
	// Force transition in Redis if needed
	c.HSet(ctx, fmt.Sprintf("machine:%s:debit:%s", machine, killedConsumer), "state", "quarantined")

	// While process group is STILL ALIVE, unconfirmed give must fail
	err = capacity.Give(ctx, st, capacity.GiveRequest{
		Machine:   machine,
		Consumer:  killedConsumer,
		PGID:      pgid,
		Confirmed: false,
	})
	if err == nil {
		t.Fatalf("Give of quarantined debit with living process group MUST be refused")
	}
	var saErr *capacity.StillAliveError
	if !errors.As(err, &saErr) {
		t.Fatalf("expected StillAliveError, got %v", err)
	}

	// And taking replacement capacity while process group is alive is STILL refused!
	_, err = capacity.Take(ctx, st, capacity.TakeRequest{
		Machine:  machine,
		Consumer: "replacement-worker",
		CPUMilli: 5000,
		MemMB:    10000,
		Kind:     capacity.KindLand,
	})
	if err == nil {
		t.Fatalf("expected NOBUDGET while quarantined debit still alive")
	}

	// Now SIGKILL the process group and confirm it is gone
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill process group: %v", err)
	}
	_ = cmd.Wait()

	// Wait until kill -0 -<pgid> answers ESRCH
	dead := false
	for i := 0; i < 50; i++ {
		if kerr := syscall.Kill(-pgid, 0); errors.Is(kerr, syscall.ESRCH) {
			dead = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !dead {
		t.Fatalf("process group %d failed to terminate", pgid)
	}

	// Now Reap confirms ESRCH and returns the capacity
	reaped, err := capacity.Reap(ctx, st, machine)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if len(reaped) != 1 || reaped[0].Consumer != killedConsumer {
		t.Fatalf("expected reaped 1 debit for %s, got %+v", killedConsumer, reaped)
	}

	// Now that capacity has been returned after process group confirmed gone,
	// replacement take SUCCEEDS!
	res, err := capacity.Take(ctx, st, capacity.TakeRequest{
		Machine:  machine,
		Consumer: "replacement-worker",
		CPUMilli: 5000,
		MemMB:    10000,
		Kind:     capacity.KindLand,
	})
	if err != nil {
		t.Fatalf("replacement take failed after reap: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("expected replacement take to be allowed")
	}

	// Clean up replacement
	_ = capacity.Give(ctx, st, capacity.GiveRequest{
		Machine:  machine,
		Consumer: "replacement-worker",
	})
}
