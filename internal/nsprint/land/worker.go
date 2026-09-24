package land

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// WorkerConfig configures the land gate worker (spec 5).
type WorkerConfig struct {
	Client                 *redis.Client
	Store                  *store.Store
	Bench                  string
	Repos                  []string
	Slots                  int
	Class                  string
	MirrorDir              string
	HeartbeatInterval      time.Duration
	QuarantineReapInterval time.Duration
}

// RequeuedBatch records a batch requeued by SweepReclaim.
type RequeuedBatch struct {
	BatchID  string
	Attempt  int
	Token    string
	EntryID  string
	OldBench string
	OldSlot  string
}

// SweepReclaim sweeps batches in gating state; if worker heartbeat is absent,
// requeues the attempt (spec 5.6, control L5).
func SweepReclaim(ctx context.Context, c *redis.Client, repo, base string) ([]RequeuedBatch, error) {
	chainKey := ChainKey(repo, base)
	batchIDs, err := c.ZRange(ctx, chainKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("sweep reclaim zrange: %w", err)
	}

	var requeued []RequeuedBatch
	for _, bID := range batchIDs {
		bkey := BatchKey(repo, base, bID)
		vals, err := c.HMGet(ctx, bkey, "state", "bench", "slot", "attempt", "token").Result()
		if err != nil || len(vals) < 5 || vals[0] == nil {
			continue
		}
		state, _ := vals[0].(string)
		if state != "gating" {
			continue
		}
		bench, _ := vals[1].(string)
		slot, _ := vals[2].(string)
		if bench == "" || slot == "" {
			continue
		}

		wkey := fmt.Sprintf("worker:%s:%s", bench, slot)
		exists, err := c.Exists(ctx, wkey).Result()
		if err != nil {
			continue
		}
		if exists == 0 {
			// Heartbeat expired or worker dead: requeue (spec 5.6)
			token, entryID, err := CallRequeue(ctx, c, repo, base, bID)
			if err != nil {
				continue
			}
			attStr, _ := vals[3].(string)
			att, _ := strconv.Atoi(attStr)
			requeued = append(requeued, RequeuedBatch{
				BatchID:  bID,
				Attempt:  att + 1,
				Token:    token,
				EntryID:  entryID,
				OldBench: bench,
				OldSlot:  slot,
			})
		}
	}
	return requeued, nil
}

// Worker executes gate attempts across benches and slots (spec 5).
type Worker struct {
	cfg WorkerConfig
}

// NewWorker creates a new Worker instance.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.Slots <= 0 {
		cfg.Slots = 1
	}
	if len(cfg.Repos) == 0 {
		cfg.Repos = []string{"nova-tools"}
	}
	if cfg.Class == "" {
		cfg.Class = ClassGo
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 5 * time.Second
	}
	if cfg.QuarantineReapInterval <= 0 {
		cfg.QuarantineReapInterval = 5 * time.Second
	}
	return &Worker{cfg: cfg}
}

// Run executes the worker loop across all configured slots until ctx is canceled (spec 5).
func (w *Worker) Run(ctx context.Context) error {
	// First at start before any take: reap quarantined debits (spec 5.1, L20c)
	if w.cfg.Store != nil {
		_, _ = capacity.Reap(ctx, w.cfg.Store, w.cfg.Bench)
	}

	// Background reaper every 5 s
	go func() {
		ticker := time.NewTicker(w.cfg.QuarantineReapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if w.cfg.Store != nil {
					_, _ = capacity.Reap(ctx, w.cfg.Store, w.cfg.Bench)
				}
			}
		}
	}()

	var wg sync.WaitGroup
	for s := 1; s <= w.cfg.Slots; s++ {
		slotID := fmt.Sprintf("slot-%d", s)
		wg.Add(1)
		go func(slot string) {
			defer wg.Done()
			w.runSlot(ctx, slot)
		}(slotID)
	}
	wg.Wait()
	return nil
}

// RunOnce attempts to process one gate attempt across any configured repo and returns whether one was run.
func (w *Worker) RunOnce(ctx context.Context, slot string) (bool, error) {
	for _, repo := range w.cfg.Repos {
		res, err := CallGateTake(ctx, w.cfg.Client, repo, w.cfg.Bench, slot, w.cfg.Class, 0, 0)
		if err != nil {
			return false, err
		}
		if res.Status == "OK" {
			w.executeGate(ctx, repo, res.Base, res.BatchID, res.Attempt, res.Token, slot)
			return true, nil
		}
	}
	return false, nil
}

func (w *Worker) runSlot(ctx context.Context, slot string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		processed := false
		for _, repo := range w.cfg.Repos {
			res, err := CallGateTake(ctx, w.cfg.Client, repo, w.cfg.Bench, slot, w.cfg.Class, 0, 0)
			if err != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if res.Status == "OK" {
				w.executeGate(ctx, repo, res.Base, res.BatchID, res.Attempt, res.Token, slot)
				processed = true
				break
			} else if res.Status == "NOBUDGET" {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if !processed {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (w *Worker) executeGate(ctx context.Context, repo, base, batchID string, attempt int, token, slot string) {
	// 1. Start heartbeat renewing worker:<bench>:<slot> PX 15000 every 5 s (spec 5.2)
	hbCtx, cancelHB := context.WithCancel(ctx)
	defer cancelHB()

	go func() {
		ticker := time.NewTicker(w.cfg.HeartbeatInterval)
		defer ticker.Stop()
		wkey := fmt.Sprintf("worker:%s:%s", w.cfg.Bench, slot)
		val := fmt.Sprintf("%s:%d:%s", batchID, attempt, token)
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				_ = w.cfg.Client.Set(hbCtx, wkey, val, 15*time.Second).Err()
				if w.cfg.Store != nil {
					_ = capacity.Renew(hbCtx, w.cfg.Store, w.cfg.Bench, "land:"+w.cfg.Bench+":"+slot, 0, 30000)
				}
			}
		}
	}()

	// 2. Read batch info
	bkey := BatchKey(repo, base, batchID)
	bvals, err := w.cfg.Client.HMGet(ctx, bkey, "from_tip", "members", "class", "created_at", "input_id").Result()
	if err != nil || len(bvals) < 5 {
		return
	}
	fromTip, _ := bvals[0].(string)
	membersCSV, _ := bvals[1].(string)
	class, _ := bvals[2].(string)
	createdAt, _ := bvals[3].(string)
	inputID, _ := bvals[4].(string)

	var memberHeads []string
	var changedFiles []string
	if membersCSV != "" {
		for _, m := range strings.Split(membersCSV, ",") {
			parts := strings.Split(m, "@")
			if len(parts) == 2 {
				memberHeads = append(memberHeads, parts[1])
			}
		}
	}

	// 3. Build deterministic train (spec 5.3)
	trainHead := fromTip
	trainTree := ""
	if w.cfg.MirrorDir != "" && len(memberHeads) > 0 {
		tr, err := BuildTrain(ctx, TrainParams{
			GitDir:    w.cfg.MirrorDir,
			FromTip:   fromTip,
			Members:   memberHeads,
			BatchID:   batchID,
			CreatedAt: createdAt,
		})
		if err != nil {
			verdict := "ERROR"
			if strings.Contains(err.Error(), "conflict") || strings.Contains(err.Error(), "CONFLICT") {
				verdict = "CONFLICT"
			}
			_, _ = CallGateReceipt(ctx, w.cfg.Client, repo, base, batchID, attempt, token, verdict, w.cfg.Bench, "worker-"+slot, trainHead, trainTree, inputID, "", "", "", "", "0")
			return
		}
		trainHead = tr.TrainHead
		trainTree = tr.TrainTree
	}

	// 4. Test selection (spec 5.4, B4)
	sel := Select(changedFiles, nil)
	steps := merge.StepsFor(merge.Selection{Class: class, Packages: sel.Packages})

	// 5. Run steps (spec 5, B5)
	runner := &merge.StepRunner{
		Dir:     w.cfg.MirrorDir,
		Timeout: 5 * time.Minute,
	}
	results := runner.Run(steps)

	verdict := "GREEN"
	failing := ""
	for _, res := range results {
		if res.Err != nil {
			verdict = "RED"
			failing = res.Step.Name
			break
		}
	}

	// 6. Write receipt with atomic ack (spec 5.5, L31)
	_, _ = CallGateReceipt(ctx, w.cfg.Client, repo, base, batchID, attempt, token, verdict, w.cfg.Bench, "worker-"+slot, trainHead, trainTree, inputID, sel.Checks, "", failing, "", "10")
}
