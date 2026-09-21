package pulse

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// SprintState represents the lifecycle state of a sprint.
type SprintState string

const (
	StateIdle     SprintState = "idle"
	StateRunning  SprintState = "running"
	StatePaused   SprintState = "paused"
	StateDraining SprintState = "draining"
	StateStopped  SprintState = "stopped"

	// Legacy aliases
	StateNone  = StateIdle
	StatePrep  = StateIdle
	StateRun   = StateRunning
	StateDrain = StateDraining
	StateStop  = StateStopped
)

const (
	SprintStateFile   = "SPRINT-STATE"
	SprintStartFile   = "SPRINT-START"
	SprintEndFile     = "SPRINT-END"
	SprintStopFile    = "STOP"
	SprintReceiptFile = "TERMINAL-RECEIPT.json"
)

// TerminalReceipt records the terminal state and receipt when a sprint is stopped.
type TerminalReceipt struct {
	State       SprintState `json:"state"`
	Timestamp   string      `json:"timestamp"`
	ActiveCards int         `json:"active_cards"`
	Reason      string      `json:"reason,omitempty"`
}

// LeaseChecker queries the number of currently active leases.
type LeaseChecker interface {
	ActiveLeases() (int, error)
}

// LeaseFunc allows a plain function to satisfy LeaseChecker.
type LeaseFunc func() (int, error)

func (f LeaseFunc) ActiveLeases() (int, error) {
	return f()
}

// DirLeaseChecker counts active leases across roots and/or launched directory.
type DirLeaseChecker struct {
	Roots    []string
	Launched string
	Now      func() time.Time
}

func (c *DirLeaseChecker) ActiveLeases() (int, error) {
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now()
	}
	count := 0

	// 1. Check launched dir for active card files
	if c.Launched != "" {
		entries, err := os.ReadDir(c.Launched)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if strings.HasSuffix(name, ".md") && !strings.HasPrefix(name, ".") {
					count++
				}
			}
		} else if !os.IsNotExist(err) {
			return 0, err
		}
	}

	// 2. Check roots for live .lease files using swarm.ReadJobLease
	for _, root := range c.Roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			slotDir := filepath.Join(root, e.Name())
			jobsDir := filepath.Join(slotDir, "jobs")
			jobEntries, err := os.ReadDir(jobsDir)
			if err != nil {
				// Check if slotDir itself holds a .lease
				lease, lerr := swarm.ReadJobLease(slotDir)
				if lerr == nil && lease.Live(now) {
					count++
				}
				continue
			}
			for _, je := range jobEntries {
				if !je.IsDir() {
					continue
				}
				jobDir := filepath.Join(jobsDir, je.Name())
				lease, lerr := swarm.ReadJobLease(jobDir)
				if lerr == nil && lease.Live(now) {
					count++
				}
			}
		}
	}

	return count, nil
}

// NormalizeSprintState converts a string to canonical SprintState.
func NormalizeSprintState(s string) SprintState {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "idle", "none", "prep":
		return StateIdle
	case "running", "run":
		return StateRunning
	case "paused", "pause":
		return StatePaused
	case "draining", "drain":
		return StateDraining
	case "stopped", "stop":
		return StateStopped
	default:
		return SprintState(strings.TrimSpace(s))
	}
}

// ReadSprintState reads the current sprint state from dir.
func ReadSprintState(dir string) (SprintState, error) {
	data, err := os.ReadFile(filepath.Join(dir, SprintStateFile))
	if err != nil {
		if os.IsNotExist(err) {
			return StateIdle, nil
		}
		return StateIdle, err
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return StateIdle, nil
	}
	return NormalizeSprintState(s), nil
}

// WriteSprintState writes the given sprint state to dir.
func WriteSprintState(dir string, state SprintState) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, SprintStateFile), []byte(string(state)+"\n"), 0o644)
}

// StateMachine manages programmatic sprint lifecycle transitions.
type StateMachine struct {
	mu          sync.Mutex
	state       SprintState
	dir         string
	stopFile    string
	leases      LeaseChecker
	now         func() time.Time
	onCancel    func()
	lastReceipt *TerminalReceipt
}

type StateMachineOption func(*StateMachine)

func WithDir(dir string) StateMachineOption {
	return func(sm *StateMachine) {
		sm.dir = dir
	}
}

func WithStopFile(path string) StateMachineOption {
	return func(sm *StateMachine) {
		sm.stopFile = path
	}
}

func WithLeaseChecker(lc LeaseChecker) StateMachineOption {
	return func(sm *StateMachine) {
		sm.leases = lc
	}
}

func WithNowFunc(now func() time.Time) StateMachineOption {
	return func(sm *StateMachine) {
		sm.now = now
	}
}

func WithCancelFunc(onCancel func()) StateMachineOption {
	return func(sm *StateMachine) {
		sm.onCancel = onCancel
	}
}

// NewStateMachine creates a new StateMachine with initial state.
func NewStateMachine(initial SprintState, opts ...StateMachineOption) *StateMachine {
	if initial == "" {
		initial = StateIdle
	}
	sm := &StateMachine{
		state: NormalizeSprintState(string(initial)),
		now:   func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(sm)
	}
	return sm
}

// Current returns the current state.
func (sm *StateMachine) Current() SprintState {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.state
}

// Start executes the transition: idle -> running.
func (sm *StateMachine) Start() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.state != StateIdle {
		return fmt.Errorf("invalid transition: cannot start sprint in state %q (must be %s)", sm.state, StateIdle)
	}

	stopPath := sm.effectiveStopFile()
	if stopPath != "" {
		_ = os.Remove(stopPath)
	}

	if sm.dir != "" {
		stamp := sm.now().Format(time.RFC3339)
		_ = os.WriteFile(filepath.Join(sm.dir, SprintStartFile), []byte(stamp+"\n"), 0o644)
		_ = os.Remove(filepath.Join(sm.dir, SprintEndFile))
		_ = os.Remove(filepath.Join(sm.dir, SprintReceiptFile))
		if err := WriteSprintState(sm.dir, StateRunning); err != nil {
			return err
		}
	}

	sm.state = StateRunning
	return nil
}

// Pause executes the transition: running -> paused.
// Suspends dispatch of new cards, keeps existing active workers alive.
func (sm *StateMachine) Pause() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.state != StateRunning {
		return fmt.Errorf("invalid transition: cannot pause sprint in state %q (must be %s)", sm.state, StateRunning)
	}

	stopPath := sm.effectiveStopFile()
	if stopPath != "" {
		if err := os.WriteFile(stopPath, nil, 0o644); err != nil {
			return err
		}
	}

	if sm.dir != "" {
		if err := WriteSprintState(sm.dir, StatePaused); err != nil {
			return err
		}
	}

	sm.state = StatePaused
	return nil
}

// Resume executes the transition: paused -> running.
func (sm *StateMachine) Resume() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.state != StatePaused {
		return fmt.Errorf("invalid transition: cannot resume sprint in state %q (must be %s)", sm.state, StatePaused)
	}

	stopPath := sm.effectiveStopFile()
	if stopPath != "" {
		_ = os.Remove(stopPath)
	}

	if sm.dir != "" {
		if err := WriteSprintState(sm.dir, StateRunning); err != nil {
			return err
		}
	}

	sm.state = StateRunning
	return nil
}

// Drain executes the transition: running | paused -> draining.
// Stops issuing new card dispatches, waits for currently running cards to complete,
// and automatically transitions to stopped when active count reaches 0.
func (sm *StateMachine) Drain(pollInterval, timeout time.Duration, sleep func(time.Duration)) error {
	sm.mu.Lock()
	if sm.state != StateRunning && sm.state != StatePaused {
		curr := sm.state
		sm.mu.Unlock()
		return fmt.Errorf("invalid transition: cannot drain sprint in state %q (must be %s or %s)", curr, StateRunning, StatePaused)
	}

	stopPath := sm.effectiveStopFile()
	if stopPath != "" {
		if err := os.WriteFile(stopPath, nil, 0o644); err != nil {
			sm.mu.Unlock()
			return err
		}
	}

	if sm.dir != "" {
		if err := WriteSprintState(sm.dir, StateDraining); err != nil {
			sm.mu.Unlock()
			return err
		}
	}

	sm.state = StateDraining
	sm.mu.Unlock()

	if sm.leases == nil {
		return sm.completeDrain()
	}

	if pollInterval <= 0 {
		pollInterval = 10 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if sleep == nil {
		sleep = time.Sleep
	}

	start := sm.now()
	for {
		active, err := sm.leases.ActiveLeases()
		if err != nil {
			return err
		}
		if active == 0 {
			return sm.completeDrain()
		}

		if sm.now().Sub(start) >= timeout {
			return fmt.Errorf("drain timeout: %d active leases remain after %s", active, timeout)
		}

		sleep(pollInterval)
	}
}

func (sm *StateMachine) completeDrain() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	stamp := sm.now().Format(time.RFC3339)
	receipt := &TerminalReceipt{
		State:       StateStopped,
		Timestamp:   stamp,
		ActiveCards: 0,
		Reason:      "drain completed",
	}

	if sm.dir != "" {
		_ = os.WriteFile(filepath.Join(sm.dir, SprintEndFile), []byte(stamp+"\n"), 0o644)
		if data, err := json.MarshalIndent(receipt, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(sm.dir, SprintReceiptFile), data, 0o644)
		}
		if err := WriteSprintState(sm.dir, StateStopped); err != nil {
			return err
		}
	}

	sm.state = StateStopped
	sm.lastReceipt = receipt
	return nil
}

// Stop executes the transition: running | paused | draining -> stopped.
// Clean termination: cancels active tasks, records terminal receipt.
func (sm *StateMachine) Stop(reason string) (*TerminalReceipt, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.state != StateRunning && sm.state != StatePaused && sm.state != StateDraining {
		return nil, fmt.Errorf("invalid transition: cannot stop sprint in state %q (must be %s, %s, or %s)", sm.state, StateRunning, StatePaused, StateDraining)
	}

	// 1. Cancel active tasks
	if sm.onCancel != nil {
		sm.onCancel()
	}

	// 2. Ensure STOP file stands
	stopPath := sm.effectiveStopFile()
	if stopPath != "" {
		_ = os.WriteFile(stopPath, nil, 0o644)
	}

	active := 0
	if sm.leases != nil {
		if a, err := sm.leases.ActiveLeases(); err == nil {
			active = a
		}
	}

	stamp := sm.now().Format(time.RFC3339)
	if reason == "" {
		reason = "manual stop"
	}
	receipt := &TerminalReceipt{
		State:       StateStopped,
		Timestamp:   stamp,
		ActiveCards: active,
		Reason:      reason,
	}

	if sm.dir != "" {
		_ = os.WriteFile(filepath.Join(sm.dir, SprintEndFile), []byte(stamp+"\n"), 0o644)
		if data, err := json.MarshalIndent(receipt, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(sm.dir, SprintReceiptFile), data, 0o644)
		}
		if err := WriteSprintState(sm.dir, StateStopped); err != nil {
			return nil, err
		}
	}

	sm.state = StateStopped
	sm.lastReceipt = receipt
	return receipt, nil
}

// LastReceipt returns the recorded terminal receipt, if any.
func (sm *StateMachine) LastReceipt() *TerminalReceipt {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.lastReceipt
}

func (sm *StateMachine) effectiveStopFile() string {
	if sm.stopFile != "" {
		return sm.stopFile
	}
	if sm.dir != "" {
		return filepath.Join(sm.dir, SprintStopFile)
	}
	return ""
}

// --- CLI inputs and execution functions ---

type SprintPrepInput struct {
	Dir    string
	Now    func() time.Time
	Stdout io.Writer
	Stderr io.Writer
}

// SprintPrep initializes or resets a sprint directory in the idle state.
func SprintPrep(in SprintPrepInput) int {
	if strings.TrimSpace(in.Dir) == "" {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: dir is required\n")
		return 2
	}
	curr, err := ReadSprintState(in.Dir)
	if err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: reading state: %v\n", err)
		return 2
	}
	if curr != StateIdle && curr != StateStopped {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: cannot prep sprint in state %q; stop prior sprint first\n", curr)
		return 2
	}

	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: mkdir %s: %v\n", in.Dir, err)
		return 2
	}

	_ = os.Remove(filepath.Join(in.Dir, SprintStartFile))
	_ = os.Remove(filepath.Join(in.Dir, SprintEndFile))
	_ = os.Remove(filepath.Join(in.Dir, SprintReceiptFile))

	if err := WriteSprintState(in.Dir, StateIdle); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing state: %v\n", err)
		return 2
	}

	fmt.Fprintf(in.Stdout, "SPRINT PREP OK\n")
	return 0
}

type SprintStartInput struct {
	Dir      string
	StopFile string
	Now      func() time.Time
	Stdout   io.Writer
	Stderr   io.Writer
}

// SprintStart executes the transition: idle -> running.
func SprintStart(in SprintStartInput) int {
	if strings.TrimSpace(in.Dir) == "" {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: dir is required\n")
		return 2
	}
	curr, err := ReadSprintState(in.Dir)
	if err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: reading state: %v\n", err)
		return 2
	}
	if curr != StateIdle {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: cannot start sprint in state %q (must be idle)\n", curr)
		return 2
	}

	now := time.Now().UTC()
	if in.Now != nil {
		now = in.Now().UTC()
	}
	stamp := now.Format(time.RFC3339)

	stopPath := in.StopFile
	if stopPath == "" {
		stopPath = filepath.Join(in.Dir, SprintStopFile)
	}
	_ = os.Remove(stopPath)

	_ = os.WriteFile(filepath.Join(in.Dir, SprintStartFile), []byte(stamp+"\n"), 0o644)
	_ = os.Remove(filepath.Join(in.Dir, SprintEndFile))
	_ = os.Remove(filepath.Join(in.Dir, SprintReceiptFile))

	if err := WriteSprintState(in.Dir, StateRunning); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing state: %v\n", err)
		return 2
	}

	fmt.Fprintf(in.Stdout, "SPRINT RUN OK stamp=%s\n", stamp)
	return 0
}

// SprintRun is an alias for SprintStart.
type SprintRunInput = SprintStartInput

func SprintRun(in SprintRunInput) int {
	return SprintStart(in)
}

type SprintPauseInput struct {
	Dir      string
	StopFile string
	Stdout   io.Writer
	Stderr   io.Writer
}

// SprintPause executes the transition: running -> paused.
// Suspends dispatch of new cards, keeps existing active workers alive.
func SprintPause(in SprintPauseInput) int {
	if strings.TrimSpace(in.Dir) == "" {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: dir is required\n")
		return 2
	}
	curr, err := ReadSprintState(in.Dir)
	if err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: reading state: %v\n", err)
		return 2
	}
	if curr != StateRunning {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: cannot pause sprint in state %q (must be running)\n", curr)
		return 2
	}

	stopPath := in.StopFile
	if stopPath == "" {
		stopPath = filepath.Join(in.Dir, SprintStopFile)
	}
	if err := os.WriteFile(stopPath, nil, 0o644); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing stop file %s: %v\n", stopPath, err)
		return 2
	}

	if err := WriteSprintState(in.Dir, StatePaused); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing state: %v\n", err)
		return 2
	}

	fmt.Fprintf(in.Stdout, "SPRINT PAUSE OK state=paused\n")
	return 0
}

type SprintResumeInput struct {
	Dir      string
	StopFile string
	Stdout   io.Writer
	Stderr   io.Writer
}

// SprintResume executes the transition: paused -> running.
func SprintResume(in SprintResumeInput) int {
	if strings.TrimSpace(in.Dir) == "" {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: dir is required\n")
		return 2
	}
	curr, err := ReadSprintState(in.Dir)
	if err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: reading state: %v\n", err)
		return 2
	}
	if curr != StatePaused {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: cannot resume sprint in state %q (must be paused)\n", curr)
		return 2
	}

	stopPath := in.StopFile
	if stopPath == "" {
		stopPath = filepath.Join(in.Dir, SprintStopFile)
	}
	_ = os.Remove(stopPath)

	if err := WriteSprintState(in.Dir, StateRunning); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing state: %v\n", err)
		return 2
	}

	fmt.Fprintf(in.Stdout, "SPRINT RESUME OK state=running\n")
	return 0
}

type SprintDrainInput struct {
	Dir          string
	StopFile     string
	Leases       LeaseChecker
	PollInterval time.Duration
	Timeout      time.Duration
	Now          func() time.Time
	Sleep        func(time.Duration)
	Stdout       io.Writer
	Stderr       io.Writer
}

// SprintDrain executes: running | paused -> draining.
// Stops issuing new card dispatches, waits for currently running cards to complete,
// and automatically transitions to stopped when active count reaches 0.
func SprintDrain(in SprintDrainInput) int {
	if strings.TrimSpace(in.Dir) == "" {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: dir is required\n")
		return 2
	}
	curr, err := ReadSprintState(in.Dir)
	if err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: reading state: %v\n", err)
		return 2
	}
	if curr != StateRunning && curr != StatePaused && curr != StateDraining {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: cannot drain sprint in state %q (must be running or paused)\n", curr)
		return 2
	}

	stopPath := in.StopFile
	if stopPath == "" {
		stopPath = filepath.Join(in.Dir, SprintStopFile)
	}
	if err := os.WriteFile(stopPath, nil, 0o644); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing stop file %s: %v\n", stopPath, err)
		return 2
	}

	if err := WriteSprintState(in.Dir, StateDraining); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing state: %v\n", err)
		return 2
	}

	fmt.Fprintf(in.Stdout, "SPRINT DRAIN: launches prevented, waiting for workers to complete cards\n")

	nowFn := in.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}

	if in.Leases == nil {
		fmt.Fprintf(in.Stdout, "SPRINT DRAIN OK: 0 active leases\n")
		return 0
	}

	poll := in.PollInterval
	if poll <= 0 {
		poll = 10 * time.Millisecond
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	sleep := in.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	start := nowFn()
	for {
		active, err := in.Leases.ActiveLeases()
		if err != nil {
			fmt.Fprintf(in.Stderr, "SPRINT DRAIN ERROR: %v\n", err)
			return 2
		}
		if active == 0 {
			fmt.Fprintf(in.Stdout, "SPRINT DRAIN OK: 0 active leases\n")
			return 0
		}

		if nowFn().Sub(start) >= timeout {
			fmt.Fprintf(in.Stderr, "SPRINT DRAIN TIMEOUT: %d active leases remain after %s\n", active, timeout)
			return 1
		}

		sleep(poll)
	}
}

type SprintStopInput struct {
	Dir         string
	StopFile    string
	Leases      LeaseChecker
	Strict      bool // when true, refuses with exit 1 if active leases linger
	CancelTasks func()
	Reason      string
	Now         func() time.Time
	Stdout      io.Writer
	Stderr      io.Writer
}

// SprintStop executes: running | paused | draining -> stopped.
// Clean termination: cancels active tasks, records terminal receipt.
func SprintStop(in SprintStopInput) int {
	if strings.TrimSpace(in.Dir) == "" {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: dir is required\n")
		return 2
	}
	curr, err := ReadSprintState(in.Dir)
	if err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: reading state: %v\n", err)
		return 2
	}
	if curr != StateRunning && curr != StatePaused && curr != StateDraining {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: cannot stop sprint in state %q (must be running, paused, or draining)\n", curr)
		return 2
	}

	active := 0
	if in.Leases != nil {
		a, err := in.Leases.ActiveLeases()
		if err != nil {
			fmt.Fprintf(in.Stderr, "SPRINT STOP ERROR: %v\n", err)
			return 2
		}
		active = a
		if in.Strict && active > 0 {
			fmt.Fprintf(in.Stderr, "SPRINT STOP WORKING: %d active leases remain\n", active)
			return 1
		}
	}

	if in.CancelTasks != nil {
		in.CancelTasks()
	}

	stopPath := in.StopFile
	if stopPath == "" {
		stopPath = filepath.Join(in.Dir, SprintStopFile)
	}
	_ = os.WriteFile(stopPath, nil, 0o644)

	now := time.Now().UTC()
	if in.Now != nil {
		now = in.Now().UTC()
	}
	stamp := now.Format(time.RFC3339)

	reason := in.Reason
	if reason == "" {
		reason = "manual stop"
	}
	receipt := TerminalReceipt{
		State:       StateStopped,
		Timestamp:   stamp,
		ActiveCards: active,
		Reason:      reason,
	}

	if err := os.WriteFile(filepath.Join(in.Dir, SprintEndFile), []byte(stamp+"\n"), 0o644); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing end stamp: %v\n", err)
		return 2
	}
	if data, err := json.MarshalIndent(receipt, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(in.Dir, SprintReceiptFile), data, 0o644)
	}

	if err := WriteSprintState(in.Dir, StateStopped); err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: writing state: %v\n", err)
		return 2
	}

	fmt.Fprintf(in.Stdout, "SPRINT-END %s\n", stamp)
	return 0
}

type SprintStatusInput struct {
	Dir    string
	Leases LeaseChecker
	Stdout io.Writer
	Stderr io.Writer
}

// SprintStatus reports the current sprint state and active leases.
func SprintStatus(in SprintStatusInput) int {
	if strings.TrimSpace(in.Dir) == "" {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: dir is required\n")
		return 2
	}
	curr, err := ReadSprintState(in.Dir)
	if err != nil {
		fmt.Fprintf(in.Stderr, "SPRINT REFUSED: reading state: %v\n", err)
		return 2
	}

	active := 0
	if in.Leases != nil {
		a, err := in.Leases.ActiveLeases()
		if err == nil {
			active = a
		}
	}

	startStamp := "-"
	if data, err := os.ReadFile(filepath.Join(in.Dir, SprintStartFile)); err == nil {
		startStamp = strings.TrimSpace(string(data))
	}

	endStamp := "-"
	if data, err := os.ReadFile(filepath.Join(in.Dir, SprintEndFile)); err == nil {
		endStamp = strings.TrimSpace(string(data))
	}

	fmt.Fprintf(in.Stdout, "SPRINT STATUS state=%s active=%d start=%s end=%s\n", curr, active, startStamp, endStamp)
	return 0
}
