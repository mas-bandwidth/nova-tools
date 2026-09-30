package member

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptedSprint is a thread-safe test double for Sprint.
type scriptedSprint struct {
	mu    sync.Mutex
	calls [][]string
	runFn func(args ...string) (int, []byte)
}

func newScriptedSprint(fn func(args ...string) (int, []byte)) *scriptedSprint {
	return &scriptedSprint{runFn: fn}
}

func (s *scriptedSprint) Run(args ...string) (int, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, append([]string(nil), args...))
	if s.runFn != nil {
		return s.runFn(args...)
	}
	return 0, nil
}

func (s *scriptedSprint) Calls() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([][]string, len(s.calls))
	copy(cp, s.calls)
	return cp
}

// scriptedChild is a thread-safe test double for Child.
type scriptedChild struct {
	mu     sync.Mutex
	done   bool
	result Result
}

func (c *scriptedChild) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done
}

func (c *scriptedChild) Result() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.result
}

func (c *scriptedChild) SetDone(ok bool, head, report string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.done = true
	c.result = Result{OK: ok, Head: head, Report: report}
}

// scriptedRunner is a thread-safe test double for Runner.
type scriptedRunner struct {
	mu       sync.Mutex
	started  []Packet
	children map[string]*scriptedChild
	startErr map[string]error
}

func newScriptedRunner() *scriptedRunner {
	return &scriptedRunner{
		children: make(map[string]*scriptedChild),
		startErr: make(map[string]error),
	}
}

func (r *scriptedRunner) Start(p Packet) (Child, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, p)
	if err, ok := r.startErr[p.Card]; ok {
		return nil, err
	}
	if ch, ok := r.children[p.Card]; ok {
		return ch, nil
	}
	ch := &scriptedChild{done: false}
	r.children[p.Card] = ch
	return ch, nil
}

func (r *scriptedRunner) Started() []Packet {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]Packet, len(r.started))
	copy(cp, r.started)
	return cp
}

func (r *scriptedRunner) Child(card string) *scriptedChild {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.children[card]; ok {
		return ch
	}
	ch := &scriptedChild{done: false}
	r.children[card] = ch
	return ch
}

func queueJSON(as string, epoch uint64, cards ...queueCard) []byte {
	b, _ := json.Marshal(queueOut{
		As:    as,
		Epoch: epoch,
		Cards: cards,
	})
	return b
}

func takeJSON(packets ...Packet) []byte {
	b, _ := json.Marshal(takeOut{
		Packets: packets,
	})
	return b
}

// TestMemberBeatDispatch verifies heartbeat dispatch under different loads and reader modes.
func TestMemberBeatDispatch(t *testing.T) {
	t.Parallel()

	t.Run("calculates load and dispatches fleet beat", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			if len(args) >= 2 && args[0] == "fleet" && args[1] == "beat" {
				return 0, []byte("OK")
			}
			if len(args) >= 1 && args[0] == "queue" {
				return 0, queueJSON("m1", 1)
			}
			return 0, nil
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 4}, sp, rn, &out)

		// 0 running -> load = 0
		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected tick error: %v", err)
		}
		if acted != 0 {
			t.Fatalf("expected 0 acted, got %d", acted)
		}

		calls := sp.Calls()
		if len(calls) < 2 {
			t.Fatalf("expected at least 2 calls (beat, queue), got %d", len(calls))
		}
		expectedBeat := []string{"fleet", "beat", "m1", "--load", "0"}
		if strings.Join(calls[0], " ") != strings.Join(expectedBeat, " ") {
			t.Fatalf("got beat call %v, want %v", calls[0], expectedBeat)
		}

		// Inject 2 running children -> load = 50% (2*100/4)
		m.running["c1"] = &scriptedChild{done: false}
		m.running["c2"] = &scriptedChild{done: false}

		callsBefore := len(sp.Calls())
		_, err = m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected tick error: %v", err)
		}
		calls = sp.Calls()
		expectedBeat50 := []string{"fleet", "beat", "m1", "--load", "50"}
		if strings.Join(calls[callsBefore], " ") != strings.Join(expectedBeat50, " ") {
			t.Fatalf("got beat call %v, want %v", calls[callsBefore], expectedBeat50)
		}
	})

	t.Run("zero width produces zero load without division by zero", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			if len(args) >= 2 && args[0] == "fleet" && args[1] == "beat" {
				return 0, nil
			}
			if len(args) >= 1 && args[0] == "queue" {
				return 0, queueJSON("m1", 1)
			}
			return 0, nil
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 0}, sp, rn, &out)

		_, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		calls := sp.Calls()
		expectedBeat := []string{"fleet", "beat", "m1", "--load", "0"}
		if strings.Join(calls[0], " ") != strings.Join(expectedBeat, " ") {
			t.Fatalf("got %v, want %v", calls[0], expectedBeat)
		}
	})

	t.Run("reader does not dispatch fleet beat", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			if len(args) >= 1 && args[0] == "queue" {
				return 0, queueJSON("reader-1", 1)
			}
			return 0, nil
		})
		rn := newScriptedRunner()
		m := New(Config{As: "reader-1", Width: 2, Reader: true}, sp, rn, &out)

		_, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, call := range sp.Calls() {
			if len(call) >= 1 && call[0] == "fleet" {
				t.Fatalf("reader dispatched fleet verb: %v", call)
			}
		}
	})

	t.Run("store failure on beat returns error and halts tick", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			if len(args) >= 2 && args[0] == "fleet" && args[1] == "beat" {
				return 2, []byte("connection refused")
			}
			return 0, nil
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

		acted, err := m.Tick(time.Now())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if acted != 0 {
			t.Fatalf("expected 0 acted, got %d", acted)
		}
		if !strings.Contains(err.Error(), "beat: the store did not answer: connection refused") {
			t.Fatalf("unexpected error message: %v", err)
		}
		if len(sp.Calls()) != 1 {
			t.Fatalf("expected loop to halt after beat error, got calls: %v", sp.Calls())
		}
	})
}

// TestMemberQueueAndCapacity verifies queue reading and capacity-bounded take behavior.
func TestMemberQueueAndCapacity(t *testing.T) {
	t.Parallel()

	t.Run("takes up to available capacity width minus running", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer

		epoch := uint64(7)
		qCards := []queueCard{
			{ID: "c1", Col: "ready"},
			{ID: "c2", Col: "ready"},
			{ID: "c3", Col: "ready"},
			{ID: "c4", Col: "ready"},
		}
		packets := []Packet{
			{Card: "c1", Gen: 1, Attempt: 1},
			{Card: "c2", Gen: 1, Attempt: 1},
			{Card: "c3", Gen: 1, Attempt: 1},
		}

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "fleet":
				return 0, nil
			case "queue":
				return 0, queueJSON("m1", epoch, qCards...)
			case "take":
				// Verify limit matches capacity (width 3 - 0 running = 3)
				return 0, takeJSON(packets...)
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 3}, sp, rn, &out)

		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected tick error: %v", err)
		}
		if acted != 3 {
			t.Fatalf("expected 3 acted, got %d", acted)
		}
		if m.Running() != 3 {
			t.Fatalf("expected 3 running, got %d", m.Running())
		}

		// Verify take command arguments
		var takeCall []string
		for _, c := range sp.Calls() {
			if len(c) > 0 && c[0] == "take" {
				takeCall = c
				break
			}
		}
		if takeCall == nil {
			t.Fatal("expected take call, none found")
		}
		expectedTake := []string{"take", "--as", "m1", "--limit", "3", "--json", "--epoch", "7"}
		if strings.Join(takeCall, " ") != strings.Join(expectedTake, " ") {
			t.Fatalf("got take %v, want %v", takeCall, expectedTake)
		}
	})

	t.Run("limits take to ready cards count when ready is less than room", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer

		epoch := uint64(12)
		qCards := []queueCard{
			{ID: "c1", Col: "ready"},
		}
		packets := []Packet{
			{Card: "c1", Gen: 1, Attempt: 1},
		}

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "fleet":
				return 0, nil
			case "queue":
				return 0, queueJSON("m1", epoch, qCards...)
			case "take":
				return 0, takeJSON(packets...)
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		// Width is 5, but only 1 ready card -> take limit must be clamped to 1
		m := New(Config{As: "m1", Width: 5}, sp, rn, &out)

		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected tick error: %v", err)
		}
		if acted != 1 {
			t.Fatalf("expected 1 acted, got %d", acted)
		}

		var takeCall []string
		for _, c := range sp.Calls() {
			if len(c) > 0 && c[0] == "take" {
				takeCall = c
				break
			}
		}
		if takeCall == nil {
			t.Fatal("expected take call")
		}
		expectedTake := []string{"take", "--as", "m1", "--limit", "1", "--json", "--epoch", "12"}
		if strings.Join(takeCall, " ") != strings.Join(expectedTake, " ") {
			t.Fatalf("got %v, want %v", takeCall, expectedTake)
		}
	})

	t.Run("does not take when room is zero or ready is zero", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "fleet":
				return 0, nil
			case "queue":
				return 0, queueJSON("m1", 1)
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

		// 0 ready cards -> no take
		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if acted != 0 {
			t.Fatalf("expected 0 acted, got %d", acted)
		}
		for _, c := range sp.Calls() {
			if len(c) > 0 && c[0] == "take" {
				t.Fatalf("unexpected take call: %v", c)
			}
		}

		// Room is zero -> no take
		m.running["c1"] = &scriptedChild{done: false}
		m.running["c2"] = &scriptedChild{done: false}
		_, err = m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, c := range sp.Calls() {
			if len(c) > 0 && c[0] == "take" {
				t.Fatalf("unexpected take call when capacity full: %v", c)
			}
		}
	})

	t.Run("take refused is non-fatal and printed to out", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "fleet":
				return 0, nil
			case "queue":
				return 0, queueJSON("m1", 1, queueCard{ID: "c1", Col: "ready"})
			case "take":
				return 1, []byte("take refused: cards locked")
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("take refusal should not return tick error, got: %v", err)
		}
		if acted != 0 {
			t.Fatalf("expected 0 acted, got %d", acted)
		}
		if !strings.Contains(out.String(), "take refused: take refused: cards locked") {
			t.Fatalf("expected refusal printed to out, got: %q", out.String())
		}
	})

	t.Run("take store failure returns error", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "fleet":
				return 0, nil
			case "queue":
				return 0, queueJSON("m1", 1, queueCard{ID: "c1", Col: "ready"})
			case "take":
				return 2, []byte("redis connection lost")
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

		_, err := m.Tick(time.Now())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "take: the store did not answer: redis connection lost") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("queue non-zero exit or invalid json returns error", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer

		// queue exit non-zero
		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			if args[0] == "queue" {
				return 1, []byte("queue failed")
			}
			return 0, nil
		})
		m := New(Config{As: "m1", Width: 2}, sp, newScriptedRunner(), &out)
		_, err := m.Tick(time.Now())
		if err == nil || !strings.Contains(err.Error(), "queue: exit 1: queue failed") {
			t.Fatalf("expected queue error, got: %v", err)
		}

		// queue invalid json
		sp2 := newScriptedSprint(func(args ...string) (int, []byte) {
			if args[0] == "queue" {
				return 0, []byte("not valid json")
			}
			return 0, nil
		})
		m2 := New(Config{As: "m1", Width: 2}, sp2, newScriptedRunner(), &out)
		_, err = m2.Tick(time.Now())
		if err == nil || !strings.Contains(err.Error(), "queue: not JSON:") {
			t.Fatalf("expected json error, got: %v", err)
		}
	})
}

// TestMemberChildExecutionAndFinish verifies child execution via runner and reporting via finish.
func TestMemberChildExecutionAndFinish(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	epoch := uint64(5)

	cardID := "card-work-1"
	qCard := queueCard{
		ID:  cardID,
		Col: "working",
		Gen: 3,
		Packet: &Packet{
			Card:   cardID,
			Branch: "stream/feat",
			Gen:    3,
		},
	}

	sp := newScriptedSprint(func(args ...string) (int, []byte) {
		switch args[0] {
		case "fleet":
			return 0, nil
		case "queue":
			return 0, queueJSON("m1", epoch, qCard)
		case "finish":
			return 0, []byte("OK")
		default:
			return 0, nil
		}
	})
	rn := newScriptedRunner()
	m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

	// Set child running
	child := rn.Child(cardID)
	m.running[cardID] = child

	// Tick 1: child is not done -> finish must not be called
	acted, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("unexpected tick error: %v", err)
	}
	if acted != 0 {
		t.Fatalf("expected 0 acted while child running, got %d", acted)
	}
	if m.Running() != 1 {
		t.Fatalf("expected child to remain running, got %d", m.Running())
	}
	for _, call := range sp.Calls() {
		if len(call) > 0 && call[0] == "finish" {
			t.Fatalf("finish called while child not done: %v", call)
		}
	}

	// Mark child as completed OK
	child.SetDone(true, "rev: 9a8b7c", "# Result\n\nAll 14 tests pass cleanly")

	// Tick 2: child is done -> finish called and child removed from running
	acted, err = m.Tick(time.Now())
	if err != nil {
		t.Fatalf("unexpected tick error: %v", err)
	}
	if acted != 1 {
		t.Fatalf("expected 1 acted on finish, got %d", acted)
	}
	if m.Running() != 0 {
		t.Fatalf("expected child removed from running, got %d", m.Running())
	}

	var finishCall []string
	for _, call := range sp.Calls() {
		if len(call) > 0 && call[0] == "finish" {
			finishCall = call
			break
		}
	}
	if finishCall == nil {
		t.Fatal("expected finish call")
	}

	expectedFinish := []string{
		"finish",
		"--as", "m1",
		"card-work-1@3",
		"--report", "All 14 tests pass cleanly",
		"--head", "rev: 9a8b7c",
		"--branch", "stream/feat",
		"--epoch", "5",
	}
	if strings.Join(finishCall, " ") != strings.Join(expectedFinish, " ") {
		t.Fatalf("got finish call:\n%v\nwant:\n%v", finishCall, expectedFinish)
	}

	expectedLog := "finish card-work-1 ok=true exit=0\n"
	if !strings.Contains(out.String(), expectedLog) {
		t.Fatalf("expected log %q in output %q", expectedLog, out.String())
	}
}

// TestMemberChildFailureFinish verifies failed child reporting with --failed.
func TestMemberChildFailureFinish(t *testing.T) {
	t.Parallel()

	t.Run("reports child failure with --failed flag", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		epoch := uint64(9)

		cardID := "card-fail-1"
		qCard := queueCard{
			ID:  cardID,
			Col: "working",
			Gen: 2,
			Packet: &Packet{
				Card:   cardID,
				Branch: "stream/bugfix",
				Gen:    2,
			},
		}

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "fleet":
				return 0, nil
			case "queue":
				return 0, queueJSON("m1", epoch, qCard)
			case "finish":
				return 0, []byte("OK")
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

		child := rn.Child(cardID)
		child.SetDone(false, "", "compiler panic: nil pointer")
		m.running[cardID] = child

		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected tick error: %v", err)
		}
		if acted != 1 {
			t.Fatalf("expected 1 acted, got %d", acted)
		}
		if m.Running() != 0 {
			t.Fatalf("expected child removed from running, got %d", m.Running())
		}

		var finishCall []string
		for _, call := range sp.Calls() {
			if len(call) > 0 && call[0] == "finish" {
				finishCall = call
				break
			}
		}
		if finishCall == nil {
			t.Fatal("expected finish call")
		}

		expectedFinish := []string{
			"finish",
			"--as", "m1",
			"card-fail-1@2",
			"--report", "compiler panic: nil pointer",
			"--branch", "stream/bugfix",
			"--failed",
			"--epoch", "9",
		}
		if strings.Join(finishCall, " ") != strings.Join(expectedFinish, " ") {
			t.Fatalf("got finish call:\n%v\nwant:\n%v", finishCall, expectedFinish)
		}

		expectedLog := "finish card-fail-1 ok=false exit=0\n"
		if !strings.Contains(out.String(), expectedLog) {
			t.Fatalf("expected log %q in output %q", expectedLog, out.String())
		}
	})

	t.Run("finish store failure halts tick with error", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer

		cardID := "card-down"
		qCard := queueCard{
			ID:     cardID,
			Col:    "working",
			Gen:    1,
			Packet: &Packet{Card: cardID, Gen: 1},
		}

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "fleet":
				return 0, nil
			case "queue":
				return 0, queueJSON("m1", 1, qCard)
			case "finish":
				return 2, []byte("store dead")
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

		child := rn.Child(cardID)
		child.SetDone(true, "rev: 123", "done")
		m.running[cardID] = child

		acted, err := m.Tick(time.Now())
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "finish card-down: the store did not answer: store dead") {
			t.Fatalf("unexpected error message: %v", err)
		}
		// Acted is not incremented when store fails
		if acted != 0 {
			t.Fatalf("expected 0 acted, got %d", acted)
		}
	})

	t.Run("finish refused still removes child from running", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer

		cardID := "card-refused"
		qCard := queueCard{
			ID:     cardID,
			Col:    "working",
			Gen:    1,
			Packet: &Packet{Card: cardID, Gen: 1},
		}

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "fleet":
				return 0, nil
			case "queue":
				return 0, queueJSON("m1", 1, qCard)
			case "finish":
				return 1, []byte("card already finished by another member")
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

		child := rn.Child(cardID)
		child.SetDone(true, "rev: 123", "done")
		m.running[cardID] = child

		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("refused finish should not error, got: %v", err)
		}
		if acted != 1 {
			t.Fatalf("expected acted=1, got %d", acted)
		}
		if m.Running() != 0 {
			t.Fatalf("expected child removed from running on refusal, running=%d", m.Running())
		}
		expectedLog := "finish card-refused ok=true exit=1\n"
		if !strings.Contains(out.String(), expectedLog) {
			t.Fatalf("expected log %q, got %q", expectedLog, out.String())
		}
	})
}

// TestMemberRestartRecovery verifies recovery of in-flight cards after a member restart.
func TestMemberRestartRecovery(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	epoch := uint64(3)

	cardID := "card-in-flight"
	packet := Packet{
		Card:    cardID,
		Kind:    "work",
		As:      "m1",
		Attempt: 2,
		Gen:     5,
		Branch:  "feat/recovery",
	}

	qCard := queueCard{
		ID:     cardID,
		Col:    "working",
		Gen:    5,
		Packet: &packet,
	}

	sp := newScriptedSprint(func(args ...string) (int, []byte) {
		switch args[0] {
		case "fleet":
			return 0, nil
		case "queue":
			return 0, queueJSON("m1", epoch, qCard)
		default:
			return 0, nil
		}
	})
	rn := newScriptedRunner()
	// New member has empty running map, representing a fresh restart
	m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

	// Tick 1: member notices card is in-flight in queue but not in m.running
	acted, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("unexpected tick error: %v", err)
	}
	if acted != 1 {
		t.Fatalf("expected 1 acted on recovery start, got %d", acted)
	}
	if m.Running() != 1 {
		t.Fatalf("expected 1 running child recovered, got %d", m.Running())
	}

	started := rn.Started()
	if len(started) != 1 {
		t.Fatalf("expected runner to start 1 child, got %d", len(started))
	}
	if started[0].Card != cardID || started[0].Gen != 5 || started[0].Attempt != 2 {
		t.Fatalf("unexpected recovered packet: %+v", started[0])
	}

	// Verify finish was NOT called on recovery tick
	for _, call := range sp.Calls() {
		if len(call) > 0 && call[0] == "finish" {
			t.Fatalf("finish called during recovery: %v", call)
		}
	}

	// Verify recovery log output
	expectedLog := "start card-in-flight attempt=2 gen=5 running=1/2\n"
	if !strings.Contains(out.String(), expectedLog) {
		t.Fatalf("expected log %q, got %q", expectedLog, out.String())
	}
}

// TestReaderModeLifecycle verifies the readers table loop: no fleet beat, read --begin, and read --ok/--broken.
func TestReaderModeLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("reader takes work via read --begin and starts children", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		epoch := uint64(15)

		askedCard := queueCard{
			ID:  "read-c1",
			Col: "asked",
			Packet: &Packet{
				Card:    "read-c1",
				Kind:    "read",
				As:      "reader-1",
				Attempt: 1,
				Epoch:   epoch,
			},
		}

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "queue":
				return 0, queueJSON("reader-1", epoch, askedCard)
			case "read":
				// read --begin
				return 0, []byte("OK")
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "reader-1", Width: 2, Reader: true}, sp, rn, &out)

		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if acted != 1 {
			t.Fatalf("expected 1 acted, got %d", acted)
		}
		if m.Running() != 1 {
			t.Fatalf("expected 1 running, got %d", m.Running())
		}

		// Verify read --begin call
		var beginCall []string
		for _, call := range sp.Calls() {
			if len(call) >= 4 && call[0] == "read" && call[3] == "--begin" {
				beginCall = call
				break
			}
		}
		if beginCall == nil {
			t.Fatal("expected read --begin call")
		}
		expectedBegin := []string{
			"read", "--as", "reader-1", "--begin", "--limit", "1", "--json", "--epoch", "15",
		}
		if strings.Join(beginCall, " ") != strings.Join(expectedBegin, " ") {
			t.Fatalf("got read --begin:\n%v\nwant:\n%v", beginCall, expectedBegin)
		}
	})

	t.Run("reader reports completed read with read --ok", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		epoch := uint64(20)

		cardID := "read-ok-1"
		qCard := queueCard{
			ID:  cardID,
			Col: "reading",
			Packet: &Packet{
				Card: cardID,
				Kind: "read",
			},
		}

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "queue":
				return 0, queueJSON("reader-1", epoch, qCard)
			case "read":
				return 0, []byte("OK")
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "reader-1", Width: 2, Reader: true}, sp, rn, &out)

		child := rn.Child(cardID)
		child.SetDone(true, "", "LGTM, clean implementation")
		m.running[cardID] = child

		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if acted != 1 {
			t.Fatalf("expected 1 acted, got %d", acted)
		}
		if m.Running() != 0 {
			t.Fatalf("expected running 0, got %d", m.Running())
		}

		var okCall []string
		for _, call := range sp.Calls() {
			if len(call) >= 4 && call[0] == "read" && call[3] == "--ok" {
				okCall = call
				break
			}
		}
		if okCall == nil {
			t.Fatal("expected read --ok call")
		}
		expectedOK := []string{
			"read", "--as", "reader-1", "--ok", cardID, "--finding", "LGTM, clean implementation", "--epoch", "20",
		}
		if strings.Join(okCall, " ") != strings.Join(expectedOK, " ") {
			t.Fatalf("got read --ok call:\n%v\nwant:\n%v", okCall, expectedOK)
		}
	})

	t.Run("reader reports failed read with read --broken", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		epoch := uint64(25)

		cardID := "read-bad-1"
		qCard := queueCard{
			ID:  cardID,
			Col: "reading",
			Packet: &Packet{
				Card: cardID,
				Kind: "read",
			},
		}

		sp := newScriptedSprint(func(args ...string) (int, []byte) {
			switch args[0] {
			case "queue":
				return 0, queueJSON("reader-1", epoch, qCard)
			case "read":
				return 0, []byte("OK")
			default:
				return 0, nil
			}
		})
		rn := newScriptedRunner()
		m := New(Config{As: "reader-1", Width: 2, Reader: true}, sp, rn, &out)

		child := rn.Child(cardID)
		child.SetDone(false, "", "Unit test failed in package member")
		m.running[cardID] = child

		acted, err := m.Tick(time.Now())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if acted != 1 {
			t.Fatalf("expected 1 acted, got %d", acted)
		}

		var brokenCall []string
		for _, call := range sp.Calls() {
			if len(call) >= 4 && call[0] == "read" && call[3] == "--broken" {
				brokenCall = call
				break
			}
		}
		if brokenCall == nil {
			t.Fatal("expected read --broken call")
		}
		expectedBroken := []string{
			"read", "--as", "reader-1", "--broken", cardID, "--finding", "Unit test failed in package member", "--epoch", "25",
		}
		if strings.Join(brokenCall, " ") != strings.Join(expectedBroken, " ") {
			t.Fatalf("got read --broken call:\n%v\nwant:\n%v", brokenCall, expectedBroken)
		}
	})
}

// TestOneLine verifies one-line report extraction and truncation.
func TestOneLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain single line",
			input: "All tests pass cleanly",
			want:  "All tests pass cleanly",
		},
		{
			name:  "leading markdown headers and empty lines",
			input: "# Result\n\n## Summary\n\nBug fixed in loop\nDetails below",
			want:  "Bug fixed in loop",
		},
		{
			name:  "whitespace trimmed",
			input: "\n   \t\n   Trimmed finding   \n",
			want:  "Trimmed finding",
		},
		{
			name:  "truncated at 500 bytes",
			input: strings.Repeat("a", 600),
			want:  strings.Repeat("a", 500),
		},
		{
			name:  "empty when only comments or blanks",
			input: "# Header\n# Another header\n\n   \n",
			want:  "",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := oneLine(tc.input)
			if got != tc.want {
				t.Fatalf("oneLine(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestCardText verifies markdown card generation for both work and read packets.
func TestCardText(t *testing.T) {
	t.Parallel()

	t.Run("work packet generates expected card sections", func(t *testing.T) {
		t.Parallel()
		p := Packet{
			Card:    "work-card-1",
			Kind:    "work",
			As:      "m1",
			Primary: "pri-1",
			Stream:  "stream-a",
			Attempt: 2,
			Gen:     3,
			Epoch:   10,
			Branch:  "feature-branch",
			Base:    "main",
			Brief:   "Implement feature X",
			Fix:     "Avoid off-by-one error",
			Notes:   []string{"Note 1", "Note 2"},
		}

		txt := CardText(p, "nova-sprint")

		wants := []string{
			"# work-card-1: attempt 2 of pri-1 (stream stream-a)",
			"Work on branch feature-branch from main.",
			"## Brief\n\nImplement feature X",
			"## Fix, this attempt\n\nAvoid off-by-one error",
			"## Note\n\nNote 1",
			"## Note\n\nNote 2",
			"nova-sprint finish --as m1 work-card-1@3 --epoch 10 --branch feature-branch --head <sha> --report '<one line>' [--failed]",
		}

		for _, want := range wants {
			if !strings.Contains(txt, want) {
				t.Errorf("CardText missing expected substring:\n%q\nIn text:\n%s", want, txt)
			}
		}
	})

	t.Run("read packet generates expected card sections", func(t *testing.T) {
		t.Parallel()
		p := Packet{
			Card:       "read-card-1",
			Kind:       "read",
			As:         "reader-1",
			Primary:    "pri-1",
			Worker:     "worker-m1",
			Attempt:    1,
			Epoch:      10,
			Head:       "sha123",
			WorkBranch: "feature-branch",
			WorkBase:   "main",
			Report:     "Completed work successfully",
			Brief:      "Review code quality",
		}

		txt := CardText(p, "nova-sprint")

		wants := []string{
			"# Read read-card-1: attempt 1 of pri-1 by worker-m1",
			"Read the work at head sha123 on branch feature-branch (base main).",
			"The worker's report:\n\nCompleted work successfully",
			"## Brief\n\nReview code quality",
			"nova-sprint read --as reader-1 (--ok | --broken) read-card-1 --epoch 10 --finding '<one line>'",
		}

		for _, want := range wants {
			if !strings.Contains(txt, want) {
				t.Errorf("CardText missing expected substring:\n%q\nIn text:\n%s", want, txt)
			}
		}
	})
}

// TestMemberRunnerStartError verifies handling when runner.Start returns an error.
func TestMemberRunnerStartError(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer

	sp := newScriptedSprint(func(args ...string) (int, []byte) {
		switch args[0] {
		case "fleet":
			return 0, nil
		case "queue":
			return 0, queueJSON("m1", 1, queueCard{ID: "err-card", Col: "ready"})
		case "take":
			return 0, takeJSON(Packet{Card: "err-card"})
		default:
			return 0, nil
		}
	})
	rn := newScriptedRunner()
	rn.startErr["err-card"] = errors.New("cannot create slot directory")

	m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

	acted, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("unexpected tick error: %v", err)
	}
	// start failure is logged and card not started
	if acted != 0 {
		t.Fatalf("expected 0 acted, got %d", acted)
	}
	if m.Running() != 0 {
		t.Fatalf("expected 0 running, got %d", m.Running())
	}
	expectedLog := "start err-card: cannot create slot directory\n"
	if !strings.Contains(out.String(), expectedLog) {
		t.Fatalf("expected log %q in output %q", expectedLog, out.String())
	}
}

// TestMemberMultiTickProgression tests a realistic sequence of multiple ticks:
// Tick 1: Take 2 cards and start them.
// Tick 2: Both still running, beat has load 100%, no new cards taken.
// Tick 3: 1 child finishes, reported via finish, room opens up, takes 1 new card.
func TestMemberMultiTickProgression(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer

	epoch := uint64(100)
	var qCards []queueCard

	sp := newScriptedSprint(func(args ...string) (int, []byte) {
		switch args[0] {
		case "fleet":
			return 0, nil
		case "queue":
			return 0, queueJSON("m1", epoch, qCards...)
		case "take":
			// If asked for limit 2 (tick 1), return c1 and c2
			limit := args[4]
			if limit == "2" {
				return 0, takeJSON(
					Packet{Card: "c1", Gen: 1, Branch: "b1"},
					Packet{Card: "c2", Gen: 1, Branch: "b2"},
				)
			}
			// If asked for limit 1 (tick 3), return c3
			if limit == "1" {
				return 0, takeJSON(Packet{Card: "c3", Gen: 1, Branch: "b3"})
			}
			return 0, takeJSON()
		case "finish":
			return 0, []byte("OK")
		default:
			return 0, nil
		}
	})
	rn := newScriptedRunner()
	m := New(Config{As: "m1", Width: 2}, sp, rn, &out)

	// --- Tick 1 ---
	qCards = []queueCard{
		{ID: "c1", Col: "ready"},
		{ID: "c2", Col: "ready"},
		{ID: "c3", Col: "ready"},
	}
	acted, err := m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick 1 error: %v", err)
	}
	if acted != 2 || m.Running() != 2 {
		t.Fatalf("tick 1: acted=%d running=%d, want 2 and 2", acted, m.Running())
	}

	// --- Tick 2 ---
	// Cards now in working state in queue, neither child done
	qCards = []queueCard{
		{ID: "c1", Col: "working", Gen: 1},
		{ID: "c2", Col: "working", Gen: 1},
		{ID: "c3", Col: "ready"},
	}
	acted, err = m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick 2 error: %v", err)
	}
	if acted != 0 || m.Running() != 2 {
		t.Fatalf("tick 2: acted=%d running=%d, want 0 and 2", acted, m.Running())
	}

	// --- Tick 3 ---
	// c1 finishes, c2 still running
	rn.Child("c1").SetDone(true, "rev: 1111", "c1 finished")
	acted, err = m.Tick(time.Now())
	if err != nil {
		t.Fatalf("tick 3 error: %v", err)
	}
	// acted: 1 finish (c1) + 1 start (c3) = 2
	if acted != 2 {
		t.Fatalf("tick 3: acted=%d, want 2 (1 finish + 1 start)", acted)
	}
	if m.Running() != 2 { // c2 still running + c3 running
		t.Fatalf("tick 3: running=%d, want 2", m.Running())
	}
	if _, ok := m.running["c1"]; ok {
		t.Fatal("c1 should no longer be running")
	}
	if _, ok := m.running["c2"]; !ok {
		t.Fatal("c2 should still be running")
	}
	if _, ok := m.running["c3"]; !ok {
		t.Fatal("c3 should be running")
	}
}
