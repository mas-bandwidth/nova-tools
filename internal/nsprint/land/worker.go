package land

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
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
	// Log receives one line per gate fault (a failed read, a refused receipt,
	// an infrastructure ERROR); nil is os.Stderr.
	Log io.Writer
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
	if cfg.Log == nil {
		cfg.Log = os.Stderr
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
// A gate that ran but could not be receipted, or was receipted ERROR, returns (true, err).
func (w *Worker) RunOnce(ctx context.Context, slot string) (bool, error) {
	for _, repo := range w.cfg.Repos {
		res, err := CallGateTake(ctx, w.cfg.Client, repo, w.cfg.Bench, slot, w.cfg.Class, 0, 0)
		if err != nil {
			return false, err
		}
		if res.Status == "OK" {
			return true, w.executeGate(ctx, repo, res.Base, res.BatchID, res.Attempt, res.Token, slot)
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
				w.logf("%s %s: gate take: %v", repo, slot, err)
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if res.Status == "OK" {
				if err := w.executeGate(ctx, repo, res.Base, res.BatchID, res.Attempt, res.Token, slot); err != nil {
					w.logf("%s %s: %v", repo, slot, err)
				}
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

func (w *Worker) logf(format string, args ...any) {
	fmt.Fprintf(w.cfg.Log, "nova-sprint land worker: "+format+"\n", args...)
}

// GateReceiptErr turns an ns_gate_receipt reply into an error: OK is written,
// ALREADY is the write-once receipt already there (a duplicate, not a fault);
// STALE, NOTFOUND or any other reply means this attempt's receipt was not
// written, and the batch would sit in gating until the heartbeat reclaim.
func GateReceiptErr(reply string, err error) error {
	if err != nil {
		return fmt.Errorf("gate receipt: %w", err)
	}
	switch reply {
	case "OK", "ALREADY":
		return nil
	}
	return fmt.Errorf("gate receipt refused: %s", reply)
}

// gateAttempt is one taken attempt: what every receipt it writes carries.
type gateAttempt struct {
	repo, base, batchID, token, slot string
	attempt                          int
	gid                              *GateReceiptGIDParams
}

// receipt writes the attempt's receipt and returns an error when it was not written.
func (w *Worker) receipt(ctx context.Context, a gateAttempt, verdict, trainHead, trainTree, inputID, selection, failing, coreS string) error {
	reply, err := CallGateReceiptWithGID(ctx, w.cfg.Client, a.repo, a.base, a.batchID, a.attempt, a.token, verdict, w.cfg.Bench, "worker-"+a.slot, trainHead, trainTree, inputID, selection, "", failing, "", coreS, a.gid)
	if err := GateReceiptErr(reply, err); err != nil {
		return fmt.Errorf("batch %s attempt %d %s: %w", a.batchID, a.attempt, verdict, err)
	}
	return nil
}

// infraError receipts ERROR (retryable: the attempt is requeued, the members are
// not blamed) for a fault of the bench rather than the train, and returns it.
func (w *Worker) infraError(ctx context.Context, a gateAttempt, trainHead, trainTree, inputID string, cause error) error {
	if err := w.receipt(ctx, a, "ERROR", trainHead, trainTree, inputID, "", "", "0"); err != nil {
		return fmt.Errorf("batch %s attempt %d: %v; and %w", a.batchID, a.attempt, cause, err)
	}
	return fmt.Errorf("batch %s attempt %d receipted ERROR: %w", a.batchID, a.attempt, cause)
}

func (w *Worker) executeGate(ctx context.Context, repo, base, batchID string, attempt int, token, slot string) error {
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

	a := gateAttempt{repo: repo, base: base, batchID: batchID, token: token, slot: slot, attempt: attempt}

	// 2. Read batch info
	bkey := BatchKey(repo, base, batchID)
	bvals, err := w.cfg.Client.HMGet(ctx, bkey, "from_tip", "members", "class", "created_at", "input_id", "paths").Result()
	if err != nil || len(bvals) < 6 {
		if err == nil {
			err = fmt.Errorf("short reply (%d fields)", len(bvals))
		}
		return w.infraError(ctx, a, "", "", "", fmt.Errorf("read batch %s: %w", bkey, err))
	}
	fromTip, _ := bvals[0].(string)
	membersCSV, _ := bvals[1].(string)
	class, _ := bvals[2].(string)
	createdAt, _ := bvals[3].(string)
	planInputID, _ := bvals[4].(string)

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
	if pathsCSV, ok := bvals[5].(string); ok && pathsCSV != "" {
		for _, p := range strings.Split(pathsCSV, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				changedFiles = append(changedFiles, p)
			}
		}
	}

	// Read base policy for GID receipt and input_id (spec 3.7, 5.5)
	var policyID, requiredSetID, runnerID string
	pkey := PolicyKey(repo, base)
	pvals, err := w.cfg.Client.HMGet(ctx, pkey, "policy_id", "required_set_id", "runner_id").Result()
	if err != nil {
		return w.infraError(ctx, a, "", "", planInputID, fmt.Errorf("read policy %s: %w", pkey, err))
	}
	if len(pvals) >= 3 {
		policyID, _ = pvals[0].(string)
		requiredSetID, _ = pvals[1].(string)
		runnerID, _ = pvals[2].(string)
	}
	if policyID != "" || requiredSetID != "" || runnerID != "" {
		kind := "full"
		head := fromTip
		if len(memberHeads) == 1 {
			kind = "single"
			head = memberHeads[0]
		} else if len(memberHeads) == 0 {
			kind = "tip"
			head = fromTip
		}
		a.gid = &GateReceiptGIDParams{
			GID:           GID(kind, base, fromTip, requiredSetID, policyID, runnerID),
			Kind:          kind,
			Head:          head,
			BaseSHA:       fromTip,
			RequiredSetID: requiredSetID,
			PolicyID:      policyID,
			RunnerID:      runnerID,
		}
	}

	// The gate tests a checkout of the train; with no mirror there is nothing to
	// check out, and running the steps anywhere else would receipt a verdict on
	// the wrong tree.
	if w.cfg.MirrorDir == "" {
		return w.infraError(ctx, a, "", "", planInputID, errors.New("no mirror configured (--mirror)"))
	}
	if fromTip == "" {
		return w.infraError(ctx, a, "", "", planInputID, errors.New("batch has no from_tip"))
	}

	// 3. Build deterministic train (spec 5.3)
	trainHead := fromTip
	trainTree := ""
	if len(memberHeads) > 0 {
		tr, err := BuildTrain(ctx, TrainParams{
			GitDir:    w.cfg.MirrorDir,
			FromTip:   fromTip,
			Members:   memberHeads,
			BatchID:   batchID,
			CreatedAt: createdAt,
		})
		if err != nil {
			var conflictErr *MergeConflictError
			if errors.As(err, &conflictErr) {
				return w.receipt(ctx, a, "CONFLICT", trainHead, trainTree, planInputID, "", "", "0")
			}
			return w.infraError(ctx, a, trainHead, trainTree, planInputID, fmt.Errorf("build train: %w", err))
		}
		trainHead = tr.TrainHead
		trainTree = tr.TrainTree
	}
	if a.gid != nil && a.gid.Kind == "full" {
		a.gid.Head = trainHead
	}

	if fromTip != trainHead {
		cmd := exec.CommandContext(ctx, "git", "-C", w.cfg.MirrorDir, "diff", "--name-only", fromTip, trainHead)
		out, err := cmd.Output()
		if err != nil {
			return w.infraError(ctx, a, trainHead, trainTree, planInputID, fmt.Errorf("diff %s %s: %w", fromTip, trainHead, err))
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				changedFiles = append(changedFiles, line)
			}
		}
	}
	changedFiles = dedupeAndSort(changedFiles)

	// Create disposable worktree at train head for testing (spec 5.3). A bench
	// that cannot make one receipts ERROR: the steps never fall back to the mirror.
	tempRoot := os.TempDir()
	if eval, err := filepath.EvalSymlinks(tempRoot); err == nil {
		tempRoot = eval
	}
	tmpDir, err := os.MkdirTemp(tempRoot, "land-wt-*")
	if err != nil {
		return w.infraError(ctx, a, trainHead, trainTree, planInputID, fmt.Errorf("worktree dir: %w", err))
	}
	if eval, err := filepath.EvalSymlinks(tmpDir); err == nil {
		tmpDir = eval
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", w.cfg.MirrorDir, "worktree", "add", "--detach", tmpDir, trainHead).CombinedOutput(); err != nil {
		_ = safepath.RemoveUnder(tempRoot, tmpDir)
		return w.infraError(ctx, a, trainHead, trainTree, planInputID, fmt.Errorf("worktree add %s: %w: %s", trainHead, err, strings.TrimSpace(string(out))))
	}
	wtDir := tmpDir
	defer func() {
		_ = exec.Command("git", "-C", w.cfg.MirrorDir, "worktree", "remove", "--force", wtDir).Run()
		_ = safepath.RemoveUnder(tempRoot, wtDir)
	}()

	if trainTree == "" {
		out, err := exec.CommandContext(ctx, "git", "-C", w.cfg.MirrorDir, "rev-parse", trainHead+"^{tree}").Output()
		if err != nil {
			return w.infraError(ctx, a, trainHead, trainTree, planInputID, fmt.Errorf("rev-parse %s tree: %w", trainHead, err))
		}
		trainTree = strings.TrimSpace(string(out))
	}
	out, err := exec.CommandContext(ctx, "git", "-C", w.cfg.MirrorDir, "rev-parse", fromTip+"^{tree}").Output()
	if err != nil {
		return w.infraError(ctx, a, trainHead, trainTree, planInputID, fmt.Errorf("rev-parse %s tree: %w", fromTip, err))
	}
	baseTree := strings.TrimSpace(string(out))

	// 4. Test selection (spec 5.4, B4)
	goos := runtime.GOOS
	cfgHash := ConfigHash(nil, runtime.Version(), "", "")
	var baseGraph *Graph
	if revs, found, _ := SelGet(ctx, w.cfg.Client, repo, baseTree, goos, cfgHash); found {
		baseGraph = &Graph{
			Tree:        baseTree,
			GOOS:        goos,
			ReverseDeps: revs,
		}
	}

	var trainGraph *Graph
	if tg, err := LoadLiveGraph(ctx, wtDir, goos, nil); err == nil {
		trainGraph = tg
		trainGraph.Tree = trainTree
		_, _ = SelPut(ctx, w.cfg.Client, repo, trainTree, goos, cfgHash, time.Now().UnixMilli(), trainGraph.ReverseDeps)
	} else if revs, found, _ := SelGet(ctx, w.cfg.Client, repo, trainTree, goos, cfgHash); found {
		trainGraph = &Graph{
			Tree:        trainTree,
			GOOS:        goos,
			ReverseDeps: revs,
		}
	}

	// The input identity is computed here, from what this gate actually tested
	// (spec 5.5, L31), never taken on trust from the plan.
	inputID := GateInputID(fromTip, memberHeads, class, baseTree, trainTree, goos, cfgHash, policyID, runnerID)
	if planInputID != "" && planInputID != inputID {
		w.logf("batch %s attempt %d: plan input_id %s differs from the gate's %s; receipting the gate's", batchID, attempt, planInputID, inputID)
	}

	sel := SelectUnion(changedFiles, baseGraph, trainGraph)
	steps := merge.StepsFor(merge.Selection{Class: class, Packages: sel.Packages})

	// 5. Run steps in worktree (spec 5, B5)
	runner := &merge.StepRunner{
		Dir:     wtDir,
		Timeout: 5 * time.Minute,
	}
	startTime := time.Now()
	results := runner.Run(steps)
	elapsed := time.Since(startTime)

	verdict := "GREEN"
	failing := ""
	for _, res := range results {
		if res.Err != nil {
			verdict = "RED"
			failing = res.Step.Name
			break
		}
	}

	var totalCoreSec float64
	for _, res := range results {
		totalCoreSec += res.Duration.Seconds()
	}
	if totalCoreSec == 0 {
		totalCoreSec = elapsed.Seconds()
	}
	coreS := fmt.Sprintf("%.2f", totalCoreSec)

	// 6. Write receipt with atomic ack (spec 5.5, L31)
	return w.receipt(ctx, a, verdict, trainHead, trainTree, inputID, sel.Checks, failing, coreS)
}
