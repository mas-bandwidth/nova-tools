//go:build functional && linux

package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// lostAppliedFinish consumes the real server's successful answer, then makes
// the client see EOF. The store read in afterApply establishes that the reply
// was lost after the Finish step, never before it reached the server.
type lostAppliedFinish struct {
	base       http.RoundTripper
	beforeSend func() error
	afterApply func() error
	onDrop     func()
	mu         sync.Mutex
	dropped    bool
	operations []string
}

func (tr *lostAppliedFinish) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := req.GetBody()
	if err != nil {
		return tr.base.RoundTrip(req)
	}
	raw, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		return nil, err
	}
	var wire sprintwire.Request
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	finish, op := false, ""
	for _, argv := range wire.Verbs {
		if len(argv) > 0 && argv[0] == "finish" {
			finish = true
			for i := 0; i+1 < len(argv); i++ {
				if argv[i] == "--op" {
					op = argv[i+1]
				}
			}
		}
	}
	if finish {
		tr.mu.Lock()
		first := !tr.dropped
		tr.mu.Unlock()
		if first && tr.beforeSend != nil {
			if err := tr.beforeSend(); err != nil {
				return nil, err
			}
		}
	}
	resp, err := tr.base.RoundTrip(req)
	if err != nil || !finish {
		return resp, err
	}
	tr.mu.Lock()
	tr.operations = append(tr.operations, op)
	drop := !tr.dropped
	tr.mu.Unlock()
	if !drop {
		return resp, nil
	}
	answer, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var got sprintwire.Response
	if err := json.Unmarshal(answer, &got); err != nil || len(got.Results) != 1 || got.Results[0].Code != 0 {
		return nil, errors.New("server did not successfully apply the selected Finish before reply drop: " + string(answer))
	}
	if err := tr.afterApply(); err != nil {
		return nil, err
	}
	tr.mu.Lock()
	tr.dropped = true
	tr.mu.Unlock()
	tr.onDrop()
	return nil, io.ErrUnexpectedEOF
}

func (tr *lostAppliedFinish) observed() (bool, []string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.dropped, slices.Clone(tr.operations)
}

func boundaryServerBinary(t *testing.T, redisAddr string, advanceEpoch bool) (string, *store.Store, uint64) {
	t.Helper()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	bin := filepath.Join(t.TempDir(), "nova-sprint")
	build, cancelBuild := subproc.Command(context.Background(), subproc.Go, "go", "build", "-o", bin, "./cmd/nova-sprint")
	build.Dir = repo
	cache := os.Getenv("GOCACHE") // functionalrun's private volume, outside the read-only source
	if cache == "" {
		cache = filepath.Join(t.TempDir(), "go-cache")
	}
	build.Env = append(goenv.Clean(os.Environ()), "GOCACHE="+cache, "GOTMPDIR="+t.TempDir(), "GOFLAGS=-mod=readonly", "NOVA_TEST_NO_HOST=1")
	out, err := build.CombinedOutput()
	cancelBuild()
	require.NoError(t, err, "build server binary once: %s", out)
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, fn.Load(ctx, admin))
	st := &store.Store{B: &store.Redis{C: admin, Now: time.Now}, Actor: "ada", Now: time.Now}
	require.NoError(t, st.Init(ctx))
	_, err = st.InitSeat(ctx, "ada")
	require.NoError(t, err)
	epoch := uint64(0)
	if advanceEpoch {
		cleared, err := st.Clear(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(1), cleared.To, "the epoch-one case uses an actual store reset")
		epoch = cleared.To
	}
	_, _, _, err = st.SyncFriends(ctx, []store.FriendSpec{{Name: "bob", Width: 1, Mode: ModeOneShot, Class: "flash"}})
	require.NoError(t, err)
	_, err = st.FriendBeat(ctx, "bob")
	require.NoError(t, err)
	_, status, _, err := st.FriendHealth(ctx, "bob", "ada", sprint.FriendHealth{State: sprint.Up, Seen: time.Now().Add(-time.Second), Generation: 1}, "")
	require.NoError(t, err)
	require.Equal(t, sprint.Up, status)
	brief := "c: bounded refill proof\nREPO: mas-bandwidth/nova-tools\nWHO: only friend bob\n\nProve a native completion and refill."
	res, err := st.Run(ctx, store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "s1-1", Brief: brief}, {ID: "s1-2", Brief: brief}}}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	_, _, _, err = st.SetMachine(ctx, true)
	require.NoError(t, err)
	_, err = st.Tick(ctx)
	require.NoError(t, err)
	res, err = st.Run(ctx, store.FriendStartStep(sprint.FriendStartReq{Friend: "bob", IDs: []string{"s1-1.w1"}, Gens: map[string]int{"s1-1.w1": 1}}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	snap, err := st.Load(ctx, store.All, nil)
	require.NoError(t, err)
	require.Equal(t, epoch, snap.Epoch, "the real server fixture must use the selected sprint epoch")
	a := snap.Fleet.Card("s1-1.w1")
	require.NotNil(t, a)
	require.Equal(t, sprint.Working, a.Col)
	b := snap.Work.Card("s1-2")
	require.NotNil(t, b)
	require.Equal(t, sprint.Ready, b.Col)
	require.Equal(t, 1, snap.Fleet.Count(sprint.FriendRow("bob"), sprint.Working)+snap.Fleet.Count(sprint.FriendRow("bob"), sprint.Ready))
	require.Nil(t, snap.Fleet.Card("s1-2.w1"), "width one holds the next card before completion")

	return bin, st, epoch
}

func boundaryStartServer(t *testing.T, bin, redisAddr string) string {
	t.Helper()
	addr := "127.0.0.1:" + testutil.FreePort(t)
	logPath := filepath.Join(t.TempDir(), "server.log")
	log, err := os.Create(logPath)
	require.NoError(t, err)
	serverCtx, cancelServer := context.WithCancel(context.Background())
	cmd := subproc.Long(serverCtx, bin, "run", "--listen", addr, "--tick-deadline", "5s")
	cmd.Env = append(goenv.WithoutSecrets(os.Environ()), "NOVA_SPRINT_REDIS="+redisAddr, "NOVA_SPRINT_ACTOR=ada", "NOVA_SPRINT_SERVER="+addr)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		cancelServer()
		_ = log.Close()
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		cancelServer()
		_ = cmd.Wait()
		_ = log.Close()
	})
	client := sprintwire.Client{Addr: addr, HTTP: &http.Client{Timeout: time.Second}}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		res, err := client.Do(context.Background(), FriendCardsArgv("bob"))
		if err == nil && len(res) == 1 && res[0].Code == 0 {
			return addr
		}
		time.Sleep(25 * time.Millisecond)
	}
	raw, _ := os.ReadFile(logPath)
	t.Fatalf("isolated sprint server did not serve friend cards: %s", raw)
	return ""
}

// This joins the existing native group/disk fixture to an actual separate
// sprint server process and its Redis store. Only the bus and one-shot session
// are simulated; no live provider, production store, or host process is used.
func TestCompletionBoundaryNativeServerLostFinishRefillsWidthOne(t *testing.T) {
	t.Parallel()
	runCompletionBoundaryNativeServerLostFinish(t, true)
}

// The first sprint epoch has bare job names. This variant runs the same native
// server/restart/refill boundary without Clear, including the outbox binding
// required for those bare jobs.
func TestCompletionBoundaryNativeServerLostFinishRefillsInitialEpoch(t *testing.T) {
	t.Parallel()
	runCompletionBoundaryNativeServerLostFinish(t, false)
}

func runCompletionBoundaryNativeServerLostFinish(t *testing.T, advanceEpoch bool) {
	t.Helper()
	ctx := context.Background()
	redisAddr := testutil.Start(t)
	bin, st, epoch := boundaryServerBinary(t, redisAddr, advanceEpoch)
	serverAddr := boundaryStartServer(t, bin, redisAddr)
	client := sprintwire.Client{Addr: serverAddr, HTTP: &http.Client{Timeout: 10 * time.Second}}
	ask := func(ctx context.Context, argv []string) (string, error) {
		res, err := client.Do(ctx, argv)
		if err != nil {
			return "", err
		}
		if len(res) != 1 {
			return "", fmt.Errorf("friend cards returned %d results", len(res))
		}
		if res[0].Code != 0 {
			return "", errors.New("server refused friend cards: " + res[0].Stderr)
		}
		return res[0].Stdout, nil
	}
	stateDir := t.TempDir()
	workDir := t.TempDir()
	aJob := sprint.StoredID("s1-1.w1", epoch)
	bJob := sprint.StoredID("s1-2.w1", epoch)
	aCard := Card{ID: "s1-1.w1", Outbox: filepath.Join(workDir, "outbox", aJob)}
	require.NoError(t, os.MkdirAll(aCard.Outbox, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "jobs", aJob), 0o755))
	pid, runID, anchor := boundaryNativeCompletion(t, workDir, aJob, aCard)
	anchorPath := runReceiptPath(workDir, aJob) + ".anchor"
	seq := 0
	aClaim := &boundaryClaim{Job: aJob, Epoch: strconv.FormatUint(epoch, 10), Generation: 1, RunID: runID}
	yes := true
	traceBoundary(t, &seq, boundaryTrace{Event: "native_group_live", EvidenceKind: "native", Source: "shell-group-receipt-readback", Claim: aClaim, PID: pid, BirthKnown: true, Runnable: &yes})
	require.NoError(t, WriteLanes(stateDir, LaneState{Started: map[string]Started{aJob: {Lane: 1, Card: aCard, At: time.Now().Add(-time.Minute), RunID: runID, Pid: pid}}}))
	started, owed := boundaryDisk(t, stateDir)
	traceBoundary(t, &seq, boundaryTrace{Event: "daemon_restart", EvidenceKind: "disk", Source: "saved-lanes-readback", Claim: aClaim, Mode: ModeOneShot, Machine: "RUNNING", Started: started, StopOwed: owed})
	newDaemon := func() (*rig, *lanesHarness) {
		r := newRig(t)
		h := &lanesHarness{dir: workDir, finish: map[string]bool{}, active: map[string]int{}, block: make(chan struct{})}
		r.d.Deliver, r.passive, r.d.Dir, r.d.StateDir = h, true, workDir, stateDir
		r.d.Width = 1
		r.d.Row = func() (string, int) { return ModeOneShot, 1 }
		r.d.Coordinator = "ada"
		r.d.Rules = func() LaneRules { return LaneRules{} }
		r.d.ProcessIdentity = ProcessIdentity
		r.d.MachineKnown = func() bool { return true }
		r.d.MachineStopped = func() bool { return false }
		r.d.Held = HeldVia("bob", ask, nil, time.Now)
		r.d.LoadLanes = func() (LaneState, error) { return ReadLanes(stateDir) }
		r.d.SaveLanes = func(s LaneState) error { return WriteLanes(stateDir, s) }
		r.d.Pause = func(ctx context.Context, _ time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(50 * time.Millisecond):
			}
		}
		return r, h
	}
	r1, h1 := newDaemon()
	journalObserved := false
	r1.d.SaveLanes = func(s LaneState) error {
		if err := WriteLanes(stateDir, s); err != nil {
			return err
		}
		if !journalObserved {
			saved, _ := boundaryDisk(t, stateDir)
			if !slices.Contains(saved, aJob) {
				require.FileExists(t, finishNoticePath(r1.d, aJob))
				traceBoundary(t, &seq, boundaryTrace{Event: "finish_journal_and_started_clear_saved", EvidenceKind: "disk", Source: "file-readback", Claim: aClaim, Started: saved})
				journalObserved = true
			}
		}
		return nil
	}
	var appliedAt time.Time
	transport := &lostAppliedFinish{base: http.DefaultTransport, beforeSend: func() error {
		s, err := st.Load(ctx, store.All, nil)
		if err != nil {
			return err
		}
		if c := s.Fleet.Card(aCard.ID); c == nil || c.Col != sprint.Working {
			return errors.New("native claim left Working before Finish reached the server")
		}
		if c := s.Fleet.Card("s1-2.w1"); c != nil && c.Placed() {
			return errors.New("width-one refill occurred before the completed claim's Finish")
		}
		traceBoundary(t, &seq, boundaryTrace{Event: "before_server_finish_width_one_held", EvidenceKind: "server", Source: "redis-card-readback", Claim: aClaim, Mode: ModeOneShot, Machine: "RUNNING"})
		return nil
	}, afterApply: func() error {
		s, err := st.Load(ctx, store.All, nil)
		if err != nil {
			return err
		}
		if c := s.Fleet.Card(aCard.ID); c == nil || c.Col != sprint.DoneFailed {
			return errors.New("first Finish reply dropped before server apply")
		}
		appliedAt = time.Now()
		traceBoundary(t, &seq, boundaryTrace{Event: "server_finish_applied_before_reply_drop", EvidenceKind: "server", Source: "http-result-and-redis-card-readback", Claim: aClaim})
		return nil
	}, onDrop: func() { r1.cancel() }}
	faultClient := sprintwire.Client{Addr: serverAddr, HTTP: &http.Client{Transport: transport, Timeout: 10 * time.Second}}
	r1.d.Finish = func(ctx context.Context, argv []string) error {
		res, err := faultClient.Do(ctx, argv)
		if err != nil {
			return err
		}
		if len(res) != 1 {
			return fmt.Errorf("Finish returned %d results", len(res))
		}
		if res[0].Code != 0 {
			return errors.New("Finish refused: " + res[0].Stderr)
		}
		return nil
	}
	r1.at[2] = func() {
		started, _ := boundaryDisk(t, stateDir)
		require.Contains(t, started, aJob)
		require.True(t, procgroup.GroupRunnable(pid))
		require.NoError(t, os.WriteFile(anchorPath, anchor, 0o600))
		receipt, err := readRunReceipt(workDir, aJob)
		require.NoError(t, err)
		quietCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		require.True(t, procgroup.ReapVerified(quietCtx, pid, receipt.Identity, anchorPath, 100*time.Millisecond))
		no := false
		traceBoundary(t, &seq, boundaryTrace{Event: "group_quiet_verified", EvidenceKind: "native", Source: "process-birth-anchor", Claim: aClaim, PID: pid, BirthKnown: true, Runnable: &no})
	}
	r1.run(t, 60)
	firstTurns, _, _ := h1.got()
	require.False(t, slices.ContainsFunc(firstTurns, func(turn string) bool { return strings.HasSuffix(turn, ": s1-1.w1") }), "native completed A must not start a duplicate session turn during recovery")
	require.False(t, slices.ContainsFunc(firstTurns, func(turn string) bool { return strings.HasSuffix(turn, ": s1-2.w1") }), "reply loss must stop the first daemon before it starts B")
	dropped, ops := transport.observed()
	if !dropped {
		row, rowErr := r1.d.Held(ctx)
		snap, snapErr := st.Load(ctx, store.All, nil)
		var fleetA *sprint.Card
		if snap != nil {
			fleetA = snap.Fleet.Card(aCard.ID)
		}
		t.Logf("lost-reply diagnostic: finish_calls=%v records=%v held=%+v held_error=%v snapshot_error=%v fleet_A=%+v", ops, r1.records, row, rowErr, snapErr, fleetA)
	}
	require.True(t, dropped, "server must apply Finish then lose the answer")
	require.Len(t, ops, 1)
	require.NotEmpty(t, ops[0])
	traceBoundary(t, &seq, boundaryTrace{Event: "finish_reply_dropped", EvidenceKind: "simulated", Source: "client-roundtripper-eof-after-server-apply", Claim: aClaim})
	started, _ = boundaryDisk(t, stateDir)
	require.NotContains(t, started, aJob, "native completion claim clears only after report and finish journal persist")
	require.True(t, journalObserved)

	r2, h2 := newDaemon()
	traceBoundary(t, &seq, boundaryTrace{Event: "daemon_restart_after_lost_reply", EvidenceKind: "disk", Source: "saved-lanes-readback", Claim: aClaim, Started: started})
	var replayOps []string
	r2.d.Finish = func(ctx context.Context, argv []string) error {
		for i := 0; i+1 < len(argv); i++ {
			if argv[i] == "--op" {
				replayOps = append(replayOps, argv[i+1])
			}
		}
		res, err := client.Do(ctx, argv)
		if err != nil {
			return err
		}
		if len(res) != 1 {
			return fmt.Errorf("replayed Finish returned %d results", len(res))
		}
		if res[0].Code != 0 {
			return errors.New("replayed Finish refused: " + res[0].Stderr)
		}
		return nil
	}
	for beat := 1; beat <= 100; beat++ {
		r2.at[beat] = func() {
			turns, _, _ := h2.got()
			if slices.ContainsFunc(turns, func(turn string) bool { return strings.HasSuffix(turn, ": s1-2.w1") }) {
				r2.cancel()
			}
		}
	}
	r2.run(t, 100)
	require.LessOrEqual(t, len(replayOps), 1, "one settled claim cannot produce repeated restart finishes")
	for _, op := range replayOps {
		require.Equal(t, ops[0], op, "any restart retry must reuse the exact Finish operation")
	}
	t.Logf("restart Finish resend count=%d (zero means the test observed already-settled server state without another Finish)", len(replayOps))
	snap, err := st.Load(ctx, store.All, nil)
	require.NoError(t, err)
	settledA := snap.Fleet.Card(aCard.ID)
	require.NotNil(t, settledA)
	require.Equal(t, sprint.DoneFailed, settledA.Col)
	b := snap.Fleet.Card("s1-2.w1")
	require.NotNil(t, b, "server tick refills the one-shot row without a prompt or reminder")
	require.Equal(t, sprint.FriendRow("bob"), b.Row)
	require.LessOrEqual(t, snap.Fleet.Count(sprint.FriendRow("bob"), sprint.Ready)+snap.Fleet.Count(sprint.FriendRow("bob"), sprint.Working), 1)
	row, err := r2.d.Held(ctx)
	require.NoError(t, err)
	require.Len(t, row.Cards, 1)
	require.Equal(t, "s1-2.w1", row.Cards[0].Card)
	require.Equal(t, bJob, row.Cards[0].Job)
	turns, _, _ := h2.got()
	require.False(t, slices.ContainsFunc(turns, func(turn string) bool { return strings.HasSuffix(turn, ": s1-1.w1") }), "restarted daemon must not relaunch the already-completed native A")
	require.Equal(t, 1, len(slices.DeleteFunc(turns, func(turn string) bool { return !strings.HasSuffix(turn, ": s1-2.w1") })), "second card enters one lane turn once")
	r2.mu.Lock()
	beatsToRefill := r2.beats
	r2.mu.Unlock()
	require.LessOrEqual(t, beatsToRefill, 40, "width-one refill reaches the next card within forty daemon beats")
	t.Logf("refill observed after %d restart beats, elapsed since applied Finish %s", beatsToRefill, time.Since(appliedAt))
	bClaim := &boundaryClaim{Job: bJob, Epoch: strconv.FormatUint(epoch, 10), Generation: 1}
	traceBoundary(t, &seq, boundaryTrace{Event: "second_card_refilled_at_width_one", EvidenceKind: "server", Source: "redis-row-readback", Claim: bClaim, Mode: ModeOneShot, Machine: "RUNNING"})
	traceBoundary(t, &seq, boundaryTrace{Event: "second_card_session_turn_once", EvidenceKind: "simulated", Source: "one-shot-session-harness", Claim: bClaim})
}
