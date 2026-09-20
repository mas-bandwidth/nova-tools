package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// LiveStatus represents the runtime execution status of a slot.
type LiveStatus string

const (
	// LiveStatusRunning indicates a worker process is currently alive and executing.
	LiveStatusRunning LiveStatus = "RUNNING"
	// LiveStatusCrashed indicates the worker process terminated abruptly without a completion receipt.
	LiveStatusCrashed LiveStatus = "CRASHED"
	// LiveStatusCompleted indicates the worker finished and left a valid completion receipt.
	LiveStatusCompleted LiveStatus = "COMPLETED"
	// LiveStatusIdle indicates the slot has no active or pending execution.
	LiveStatusIdle LiveStatus = "IDLE"
)

// SlotStatus captures the live execution status and commit drift for one slot.
type SlotStatus struct {
	Slot             string     `json:"slot"`
	Job              string     `json:"job,omitempty"`
	AttemptID        string     `json:"attempt_id,omitempty"`
	Timestamp        time.Time  `json:"timestamp,omitempty"`
	CardHash         string     `json:"card_hash,omitempty"`
	Lane             string     `json:"lane,omitempty"`
	RunningCommitSHA string     `json:"running_commit_sha,omitempty"`
	DiskCommitSHA    string     `json:"disk_commit_sha,omitempty"`
	SHAMatch         bool       `json:"sha_match"`
	LiveStatus       LiveStatus `json:"live_status"`
	Pid              int        `json:"pid,omitempty"`
}

// NodeStatus captures the overall execution status and slot inventory for one fleet node.
type NodeStatus struct {
	Name           string       `json:"name"`
	DiskSHA        string       `json:"disk_sha"`
	Slots          []SlotStatus `json:"slots"`
	LiveCount      int          `json:"live_count"`
	CrashedCount   int          `json:"crashed_count"`
	IdleCount      int          `json:"idle_count"`
	CompletedCount int          `json:"completed_count"`
	DriftCount     int          `json:"drift_count"`
}

// FleetSummary aggregates totals across all nodes in the fleet.
type FleetSummary struct {
	TotalNodes     int `json:"total_nodes"`
	TotalSlots     int `json:"total_slots"`
	LiveSlots      int `json:"live_slots"`
	CrashedSlots   int `json:"crashed_slots"`
	IdleSlots      int `json:"idle_slots"`
	CompletedSlots int `json:"completed_slots"`
	DriftCount     int `json:"drift_count"`
}

// FleetStatusReport is the top-level report containing all nodes and summary.
type FleetStatusReport struct {
	Nodes   []NodeStatus `json:"nodes"`
	Summary FleetSummary `json:"summary"`
}

// QueryNodeOptions specifies parameters for querying a single node.
type QueryNodeOptions struct {
	NodeName string
	NodeRoot string
	RepoDir  string
	DiskSHA  string
	Slots    []string
	IsAlive  func(pid int) bool
}

// QueryNodeStatus queries live execution status and commit alignment for one fleet node.
func QueryNodeStatus(opts QueryNodeOptions) (NodeStatus, error) {
	node := NodeStatus{
		Name:    opts.NodeName,
		DiskSHA: strings.TrimSpace(opts.DiskSHA),
		Slots:   []SlotStatus{},
	}

	if node.DiskSHA == "" && opts.RepoDir != "" {
		node.DiskSHA = CurrentCommitSHA(opts.RepoDir)
	}

	isAlive := opts.IsAlive
	if isAlive == nil {
		isAlive = func(pid int) bool {
			return swarm.Alive(pid, "")
		}
	}

	// Discover slot directories.
	slotDirs := findSlotDirs(opts.NodeRoot, opts.Slots)

	for _, slotDir := range slotDirs {
		slotName := filepath.Base(slotDir)
		st := inspectSlot(slotName, slotDir, node.DiskSHA, isAlive)
		node.Slots = append(node.Slots, st)

		switch st.LiveStatus {
		case LiveStatusRunning:
			node.LiveCount++
		case LiveStatusCrashed:
			node.CrashedCount++
		case LiveStatusIdle:
			node.IdleCount++
		case LiveStatusCompleted:
			node.CompletedCount++
		}

		if st.LiveStatus != LiveStatusIdle && !st.SHAMatch {
			node.DriftCount++
		}
	}

	return node, nil
}

// QueryFleetStatus queries all given fleet nodes and computes the aggregated fleet report.
func QueryFleetStatus(nodes []QueryNodeOptions) (FleetStatusReport, error) {
	report := FleetStatusReport{
		Nodes: make([]NodeStatus, 0, len(nodes)),
	}

	for _, opt := range nodes {
		ns, err := QueryNodeStatus(opt)
		if err != nil {
			return report, fmt.Errorf("query node %s: %w", opt.NodeName, err)
		}
		report.Nodes = append(report.Nodes, ns)
		report.Summary.TotalNodes++
		report.Summary.TotalSlots += len(ns.Slots)
		report.Summary.LiveSlots += ns.LiveCount
		report.Summary.CrashedSlots += ns.CrashedCount
		report.Summary.IdleSlots += ns.IdleCount
		report.Summary.CompletedSlots += ns.CompletedCount
		report.Summary.DriftCount += ns.DriftCount
	}

	return report, nil
}

// findSlotDirs locates slot directories under nodeRoot.
func findSlotDirs(nodeRoot string, explicitSlots []string) []string {
	if len(explicitSlots) > 0 {
		out := make([]string, 0, len(explicitSlots))
		for _, s := range explicitSlots {
			if filepath.IsAbs(s) {
				out = append(out, s)
			} else {
				out = append(out, filepath.Join(nodeRoot, s))
			}
		}
		return out
	}

	if nodeRoot == "" {
		return nil
	}

	// Check if <nodeRoot>/slots exists.
	slotsSubdir := filepath.Join(nodeRoot, "slots")
	if info, err := os.Stat(slotsSubdir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(slotsSubdir)
		if err == nil {
			var dirs []string
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					dirs = append(dirs, filepath.Join(slotsSubdir, e.Name()))
				}
			}
			sort.Strings(dirs)
			return dirs
		}
	}

	// Otherwise check subdirectories directly under nodeRoot.
	entries, err := os.ReadDir(nodeRoot)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, filepath.Join(nodeRoot, e.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs
}

// inspectSlot inspects one slot directory and returns its SlotStatus.
func inspectSlot(slotName, slotDir, diskSHA string, isAlive func(int) bool) SlotStatus {
	st := SlotStatus{
		Slot:          slotName,
		DiskCommitSHA: diskSHA,
		LiveStatus:    LiveStatusIdle,
	}

	// Look for launch record in slotDir first, or inside job subdirectories.
	rec, err := ReadLaunchRecord(slotDir)
	var jobDir string
	if err != nil {
		// Try finding job subdirectories: <slotDir>/jobs/<job>/launch.json
		jobsDir := filepath.Join(slotDir, "jobs")
		if entries, rerr := os.ReadDir(jobsDir); rerr == nil {
			for _, e := range entries {
				if e.IsDir() {
					candidate := filepath.Join(jobsDir, e.Name())
					if jrec, jerr := ReadLaunchRecord(candidate); jerr == nil {
						rec = jrec
						jobDir = candidate
						err = nil
						break
					}
				}
			}
		}
	}

	if err != nil {
		// No launch record found. Check if slot directory has a lease or active process.
		if slotHasActiveLease(slotDir, isAlive) {
			st.LiveStatus = LiveStatusRunning
		} else {
			st.LiveStatus = LiveStatusIdle
		}
		return st
	}

	// Launch record exists! Populate fields.
	st.Job = rec.Job
	st.AttemptID = rec.AttemptID
	st.Timestamp = rec.Timestamp
	st.CardHash = rec.CardHash
	st.Lane = rec.Lane
	st.RunningCommitSHA = rec.RunningCommitSHA
	st.Pid = rec.Pid

	if diskSHA != "" && rec.RunningCommitSHA != "" {
		st.SHAMatch = (rec.RunningCommitSHA == diskSHA)
	}

	if jobDir == "" && rec.Job != "" {
		jobDir = filepath.Join(slotDir, "jobs", rec.Job)
	}

	// Check execution liveness:
	if rec.Pid > 0 && isAlive(rec.Pid) {
		st.LiveStatus = LiveStatusRunning
		return st
	}

	// PID is 0 or no longer alive. Did it complete cleanly?
	if isJobCompleted(jobDir, rec) {
		st.LiveStatus = LiveStatusCompleted
		return st
	}

	// Process is dead and no completion receipt exists: sudden worker crash!
	st.LiveStatus = LiveStatusCrashed
	return st
}

// isJobCompleted checks whether a job wrote a completion receipt.
func isJobCompleted(jobDir string, rec LaunchRecord) bool {
	if rec.State == "COMPLETED" {
		return true
	}
	if jobDir == "" {
		return false
	}
	// Check for RESULT.md
	if _, err := os.Stat(filepath.Join(jobDir, "RESULT.md")); err == nil {
		return true
	}
	// Check for complete.json or .complete
	if _, err := os.Stat(filepath.Join(jobDir, "complete.json")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(jobDir, ".complete")); err == nil {
		return true
	}
	return false
}

// slotHasActiveLease checks if a slot has a live .lease or lease file.
func slotHasActiveLease(slotDir string, isAlive func(int) bool) bool {
	leasePath := filepath.Join(slotDir, ".lease")
	if raw, err := os.ReadFile(leasePath); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "pid=") {
				var pid int
				if _, serr := fmt.Sscanf(line, "pid=%d", &pid); serr == nil && pid > 0 {
					return isAlive(pid)
				}
			}
		}
	}
	return false
}

// FormatFleetStatus formats a FleetStatusReport into parseable lines.
func FormatFleetStatus(report FleetStatusReport) string {
	var b strings.Builder
	for _, n := range report.Nodes {
		diskSHA := n.DiskSHA
		if diskSHA == "" {
			diskSHA = "-"
		}
		fmt.Fprintf(&b, "FLEET NODE name=%s disk_sha=%s slots=%d live=%d crashed=%d completed=%d idle=%d drift=%d\n",
			oneline.Field(n.Name), oneline.Field(diskSHA), len(n.Slots), n.LiveCount, n.CrashedCount, n.CompletedCount, n.IdleCount, n.DriftCount)

		for _, s := range n.Slots {
			job := s.Job
			if job == "" {
				job = "-"
			}
			attempt := s.AttemptID
			if attempt == "" {
				attempt = "-"
			}
			lane := s.Lane
			if lane == "" {
				lane = "-"
			}
			cardHash := s.CardHash
			if cardHash == "" {
				cardHash = "-"
			}
			runningSHA := s.RunningCommitSHA
			if runningSHA == "" {
				runningSHA = "-"
			}
			diskSHA := s.DiskCommitSHA
			if diskSHA == "" {
				diskSHA = "-"
			}

			fmt.Fprintf(&b, "  SLOT slot=%s job=%s attempt=%s lane=%s card_hash=%s status=%s running_sha=%s disk_sha=%s match=%t pid=%d\n",
				oneline.Field(s.Slot), oneline.Field(job), oneline.Field(attempt), oneline.Field(lane),
				oneline.Field(cardHash), oneline.Field(string(s.LiveStatus)), oneline.Field(runningSHA),
				oneline.Field(diskSHA), s.SHAMatch, s.Pid)
		}
	}

	s := report.Summary
	fmt.Fprintf(&b, "FLEET SUMMARY nodes=%d slots=%d live=%d crashed=%d completed=%d idle=%d drift=%d\n",
		s.TotalNodes, s.TotalSlots, s.LiveSlots, s.CrashedSlots, s.CompletedSlots, s.IdleSlots, s.DriftCount)

	return b.String()
}
