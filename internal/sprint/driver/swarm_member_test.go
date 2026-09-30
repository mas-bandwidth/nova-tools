package driver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// MemberWhere represents the view state of a member as read via `where --as <member>`.
type MemberWhere struct {
	Member  string
	Status  string
	Width   int
	Ready   []*sprint.Card
	Working []*sprint.Card
}

// SwarmMemberDaemon implements a real member daemon loop on a fleet machine.
// In nova-swarm, a member is a process on a machine with a width equal to its
// child capacity (concurrency limit). It runs a concurrent loop that beats,
// reads available ready cards, batches takes up to remaining capacity,
// executes jobs concurrently across slots, and dispatches finishes.
type SwarmMemberDaemon struct {
	Member       string
	Width        int
	Store        *store.Store
	JobDuration  time.Duration
	PollInterval time.Duration

	mu       sync.Mutex
	active   map[string]bool // cards currently executing in worker slots
	taken    []string        // history of all cards taken
	finished []string        // history of all cards finished
}

func NewSwarmMemberDaemon(member string, width int, st *store.Store, jobDuration, pollInterval time.Duration) *SwarmMemberDaemon {
	return &SwarmMemberDaemon{
		Member:       member,
		Width:        width,
		Store:        st,
		JobDuration:  jobDuration,
		PollInterval: pollInterval,
		active:       make(map[string]bool),
	}
}

// WhereAs reads the member's view state, corresponding to `where --as <member>`.
func (d *SwarmMemberDaemon) WhereAs(ctx context.Context) (*MemberWhere, error) {
	snap, err := d.Store.Load(ctx, []string{sprint.Fleet}, nil)
	if err != nil {
		return nil, err
	}
	ctl := snap.MemberCtl(d.Member)
	status := ""
	width := d.Width
	if ctl != nil {
		status = ctl.F("status")
		if w := ctl.Int(sprint.FieldWidth); w > 0 {
			width = w
		}
	}
	ready := snap.Fleet.Cell(d.Member, sprint.Ready)
	working := snap.Fleet.Cell(d.Member, sprint.Working)
	return &MemberWhere{
		Member:  d.Member,
		Status:  status,
		Width:   width,
		Ready:   ready,
		Working: working,
	}, nil
}

// Run executes the member daemon's concurrent loop until ctx is canceled.
func (d *SwarmMemberDaemon) Run(ctx context.Context) {
	ticker := time.NewTicker(d.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// 1. Periodic heartbeat: fleet --beat <member>
		zero := 0.0
		_, _ = d.Store.Beat(ctx, d.Member, &zero, hostload.Source{})

		// 2. Read available ready cards via where --as <member>
		view, err := d.WhereAs(ctx)
		if err == nil && view.Status == sprint.Up {
			d.mu.Lock()
			held := len(d.active)
			d.mu.Unlock()

			remainingCapacity := d.Width - held

			// 3. Batch take requests up to remaining capacity (width - held)
			if len(view.Ready) > 0 && remainingCapacity > 0 {
				takeBatch := len(view.Ready)
				if takeBatch > remainingCapacity {
					takeBatch = remainingCapacity
				}
				res, err := d.Store.Run(ctx, store.TakeStep(sprint.TakeReq{
					As:  d.Member,
					Sel: sprint.Sel{Limit: takeBatch},
					Who: d.Member,
				}))
				if err == nil && len(res.Moved) > 0 {
					// Read working cells to get generation for taken cards
					afterView, err := d.WhereAs(ctx)
					if err == nil {
						for _, c := range afterView.Working {
							cardID := c.ID
							gen := c.Int("gen")
							d.mu.Lock()
							if !d.active[cardID] {
								d.active[cardID] = true
								d.taken = append(d.taken, cardID)
								// 4. Simulate worker job execution concurrently across slots
								go d.executeJob(ctx, cardID, gen)
							}
							d.mu.Unlock()
						}
					}
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *SwarmMemberDaemon) executeJob(ctx context.Context, cardID string, gen int) {
	if d.JobDuration > 0 {
		select {
		case <-ctx.Done():
			d.mu.Lock()
			delete(d.active, cardID)
			d.mu.Unlock()
			return
		case <-time.After(d.JobDuration):
		}
	}

	// 5. Dispatch finish calls as jobs finish
	if ctx.Err() != nil {
		// Crashed or cancelled before dispatching finish
		d.mu.Lock()
		delete(d.active, cardID)
		d.mu.Unlock()
		return
	}

	_, err := d.Store.Run(context.Background(), store.FinishStep(sprint.FinishReq{
		As:   d.Member,
		Sel:  sprint.Sel{IDs: []string{cardID}},
		Gens: map[string]int{cardID: gen},
		Who:  d.Member,
	}))

	d.mu.Lock()
	delete(d.active, cardID)
	if err == nil {
		d.finished = append(d.finished, cardID)
	}
	d.mu.Unlock()
}

func (d *SwarmMemberDaemon) Stats() (taken int, finished int, active int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.taken), len(d.finished), len(d.active)
}

// swarmTestHarness provides a test harness with controllable time.
type swarmTestHarness struct {
	t   *testing.T
	st  *store.Store
	m   *store.Mem
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

func newSwarmTestHarness(t *testing.T) *swarmTestHarness {
	t.Helper()
	m := store.NewMem()
	h := &swarmTestHarness{
		t:   t,
		m:   m,
		ctx: context.Background(),
		now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	n := 0
	h.st = &store.Store{
		B:     m,
		Names: sprint.Names{Prefix: "t-"},
		Actor: "coordinator",
		Now:   func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		NewID: func() string { h.mu.Lock(); defer h.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(d time.Duration) { time.Sleep(d) },
	}
	if err := h.st.Init(h.ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.m.RowsAdd(h.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}); err != nil {
		t.Fatal(err)
	}
	if err := h.m.SetCoordinator(h.ctx, h.st.Actor); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *swarmTestHarness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
}

func (h *swarmTestHarness) must(step store.Step) store.Result {
	h.t.Helper()
	res, err := h.st.Run(h.ctx, step)
	if err != nil {
		h.t.Fatalf("%s: %v", step.Verb, err)
	}
	if len(res.Refused) > 0 {
		h.t.Fatalf("%s refused: %v", step.Verb, res.Refused)
	}
	return res
}

func (h *swarmTestHarness) memberStore(member string) *store.Store {
	return &store.Store{
		B:     h.m,
		Names: h.st.Names,
		Actor: member,
		Now:   h.st.Now,
		NewID: h.st.NewID,
		Sleep: h.st.Sleep,
	}
}

// TestRealMemberSwarmHookinLifecycle tests the real swarm member hook-in lifecycle:
// - Starts 3 real member daemon loops, each with a defined width (width=4).
// - Each member daemon runs a real concurrent loop (beats, reads ready cards via
//   where --as <member>, batches takes up to width - held, simulates worker job
//   execution across slots, dispatches finishes).
// - Verifies that cards distribute evenly across members according to rolling index and width.
// - Verifies that when a member crashes (stops beating), the store takes it down and
//   redeals/levels remaining cards without deadlock.
// - Verifies that when the member restarts, it resumes taking work smoothly.
func TestRealMemberSwarmHookinLifecycle(t *testing.T) {
	h := newSwarmTestHarness(t)
	const width = 4
	members := []string{"m1", "m2", "m3"}

	// Register members with width 4
	for _, m := range members {
		h.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: m, Width: width}))
	}

	// Start machine in store
	if _, _, _, err := h.st.SetMachine(h.ctx, true); err != nil {
		t.Fatalf("start machine: %v", err)
	}

	// Create and start 3 real member daemons concurrently
	daemons := make(map[string]*SwarmMemberDaemon)
	cancels := make(map[string]context.CancelFunc)
	var wg sync.WaitGroup

	startDaemon := func(m string) {
		ctx, cancel := context.WithCancel(h.ctx)
		cancels[m] = cancel
		d := NewSwarmMemberDaemon(m, width, h.memberStore(m), 5*time.Millisecond, 5*time.Millisecond)
		daemons[m] = d
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Run(ctx)
		}()
	}

	for _, m := range members {
		startDaemon(m)
	}

	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		wg.Wait()
	}()

	// Allow daemons to send initial beats
	time.Sleep(30 * time.Millisecond)
	if _, err := h.st.Tick(h.ctx); err != nil {
		t.Fatalf("initial tick: %v", err)
	}

	// PHASE 1: Add 12 cards (3 members * width 4 = 12 total capacity).
	// Verify even distribution across members according to rolling index and width.
	h.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 12}))

	// Run deal tick to distribute cards round the fleet
	dealTick, err := h.st.Tick(h.ctx)
	if err != nil {
		t.Fatalf("phase 1 deal tick: %v", err)
	}

	// Verify deal distributed cards evenly (exactly width 4 to each member)
	dealtCounts := make(map[string]int)
	for _, p := range dealTick.Parts {
		if p.Name == "deal" {
			for _, line := range p.Moved {
				for _, m := range members {
					if _, ok := strings.CutPrefix(line, m); ok || strings.Contains(line, "member="+m) {
						dealtCounts[m]++
					}
				}
			}
		}
	}
	t.Logf("Phase 1 dealt counts: %v", dealtCounts)
	for _, m := range members {
		if dealtCounts[m] != width {
			t.Fatalf("member %s dealt %d cards, want width %d", m, dealtCounts[m], width)
		}
	}

	// Wait for member daemons to take, execute across slots, and finish all 12 cards
	deadline := time.Now().Add(3 * time.Second)
	phase1Done := false
	for time.Now().Before(deadline) {
		snap, err := h.st.Load(h.ctx, []string{sprint.Fleet, sprint.Work}, nil)
		if err == nil {
			working := len(snap.Fleet.Column(sprint.Working))
			ready := len(snap.Fleet.Column(sprint.Ready))
			if working == 0 && ready == 0 {
				phase1Done = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !phase1Done {
		t.Fatalf("phase 1 timed out waiting for all 12 cards to finish")
	}

	// Verify all 12 cards finished and each member participated
	totalFinished := 0
	for _, m := range members {
		taken, finished, active := daemons[m].Stats()
		t.Logf("Member %s phase 1 stats: taken=%d, finished=%d, active=%d", m, taken, finished, active)
		if taken == 0 || finished == 0 || active != 0 {
			t.Fatalf("member %s: taken=%d, finished=%d, active=%d; want active=0 and progress made", m, taken, finished, active)
		}
		totalFinished += finished
	}
	if totalFinished != 12 {
		t.Fatalf("total finished %d, want 12", totalFinished)
	}

	// PHASE 2: Member crash and redeal without deadlock.
	// Add 6 new cards. m2 will take work, then crash (stops beating).
	// Store must mark m2 down, redeal m2's cards to m1 and m3, and m1/m3 must finish them.
	h.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 6}))
	if _, err := h.st.Tick(h.ctx); err != nil {
		t.Fatalf("phase 2 deal tick: %v", err)
	}

	// Wait briefly for m2 to take cards
	time.Sleep(15 * time.Millisecond)

	// Simulate m2 crash: cancel m2's context so its daemon process stops beating
	t.Logf("Simulating crash of member m2...")
	cancels["m2"]()
	time.Sleep(10 * time.Millisecond)

	// Advance time past BeatDeadline (15s) so m2's heartbeat expires
	h.advance(sprint.BeatDeadline + 2*time.Second)

	// Live members m1 and m3 beat at the new time during their loops.
	time.Sleep(20 * time.Millisecond)

	// Run machine tick: store should detect m2 is down, redeal m2's cards to m1 and m3
	downTick, err := h.st.Tick(h.ctx)
	if err != nil {
		t.Fatalf("down tick: %v", err)
	}
	t.Logf("Down tick parts: %d", len(downTick.Parts))

	// Verify m2 is down in the store
	snap, err := h.st.Load(h.ctx, []string{sprint.Fleet}, nil)
	if err != nil {
		t.Fatalf("load fleet: %v", err)
	}
	if status := snap.MemberCtl("m2").F("status"); status != sprint.Down {
		t.Fatalf("member m2 status is %q, want %q", status, sprint.Down)
	}

	// Wait for m1 and m3 to take redealt cards and finish all work without deadlock
	deadline = time.Now().Add(3 * time.Second)
	allFinished := false
	for time.Now().Before(deadline) {
		snap, err := h.st.Load(h.ctx, []string{sprint.Fleet, sprint.Work}, nil)
		if err == nil {
			working := len(snap.Fleet.Column(sprint.Working))
			ready := len(snap.Fleet.Column(sprint.Ready))
			if working == 0 && ready == 0 {
				allFinished = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !allFinished {
		t.Fatalf("deadlock detected: cards remained unfinished after m2 crash")
	}

	// Verify m2 has 0 cards and m1/m3 completed all remaining cards
	snap, _ = h.st.Load(h.ctx, []string{sprint.Fleet}, nil)
	m2Cards := len(snap.Fleet.Cell("m2", sprint.Ready)) + len(snap.Fleet.Cell("m2", sprint.Working))
	if m2Cards != 0 {
		t.Fatalf("m2 still holds %d cards after being marked down and redealt", m2Cards)
	}
	t.Log("Phase 2 passed: m2 was marked down and all cards redealt to m1/m3 without deadlock.")

	// PHASE 3: Restart member m2.
	// When m2 restarts, it resumes sending heartbeats, is marked up, and takes work smoothly.
	t.Logf("Restarting member m2 daemon...")
	startDaemon("m2")

	// Allow m2 to beat at current time
	time.Sleep(20 * time.Millisecond)
	upTick, err := h.st.Tick(h.ctx)
	if err != nil {
		t.Fatalf("up tick: %v", err)
	}
	t.Logf("Up tick completed: parts=%d", len(upTick.Parts))

	// Verify m2 is UP again
	snap, err = h.st.Load(h.ctx, []string{sprint.Fleet}, nil)
	if err != nil {
		t.Fatalf("load fleet: %v", err)
	}
	if status := snap.MemberCtl("m2").F("status"); status != sprint.Up {
		t.Fatalf("restarted member m2 status is %q, want %q", status, sprint.Up)
	}

	// Add 6 new cards to verify m2 takes and finishes new work smoothly
	h.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 6}))
	if _, err := h.st.Tick(h.ctx); err != nil {
		t.Fatalf("phase 3 deal tick: %v", err)
	}

	// Wait for all members including m2 to finish work
	deadline = time.Now().Add(3 * time.Second)
	phase3Done := false
	for time.Now().Before(deadline) {
		snap, err := h.st.Load(h.ctx, []string{sprint.Fleet, sprint.Work}, nil)
		if err == nil {
			working := len(snap.Fleet.Column(sprint.Working))
			ready := len(snap.Fleet.Column(sprint.Ready))
			if working == 0 && ready == 0 {
				phase3Done = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !phase3Done {
		t.Fatalf("phase 3 failed: work was not completed after m2 restart")
	}

	// Verify m2 took and finished work after restart
	m2Taken, m2Finished, _ := daemons["m2"].Stats()
	t.Logf("Member m2 after restart: taken=%d, finished=%d", m2Taken, m2Finished)
	if m2Finished == 0 {
		t.Fatalf("restarted member m2 did not finish any cards")
	}

	t.Log("TestRealMemberSwarmHookinLifecycle passed successfully across all phases.")
}
