package sandbox

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// THE WINDOWS WALL, as a sequence (docs/SPEC-SANDBOX.md, "Windows"). It is the minimal body
// a native Windows member needs before its cards run contained (captain, 2026-10-20: the
// windows build of this tool refused every command with no_sandbox, so the member ran
// --no-wall), and it is built from what Windows gives an ordinary user with no service and
// no driver:
//
//   - WRITES: the child runs under a LOW-INTEGRITY copy of this process's token, with every
//     privilege removed (Administrators is left as it is: deny-only crashed cmake). Windows' mandatory integrity control
//     refuses a low-integrity write to any object labelled above low -- which is every file
//     and directory on the machine by default -- so the policy's write roots (the job
//     directory, its tmp, the data home, the shared caches) are labelled LOW, inherited by
//     what is made under them, and they are the only places the child can write.
//   - THE TREE: this process joins a fresh Job Object before it starts the child, so the
//     child and everything it starts are in the job from their first instruction. The job is
//     kill-on-close (this process ending, by any path, ends the tree), and it carries the
//     policy's process cap (ActiveProcessLimit) and memory cap (JobMemoryLimit).
//   - THE ENVIRONMENT: the child is handed the environment the caller built (ChildEnv over
//     native's allowlist), never this process's own.
//
// WHAT IT DOES NOT DO, said here so the OK line is never read as more: READS ARE NOT
// CONFINED (an integrity level stops writes up, not reads), and the NETWORK is not
// enforced. A read grant on the line is a statement of the policy, not of the kernel.
//
// The sequence is here, on every OS, over winWallOps; the Win32 calls are wrap_windows.go's.
// The tests drive this file with a fake, so the order -- label, token, job, start -- and
// every refusal are asserted on any host.

// winWallOps is everything the windows wall reaches Windows through.
type winWallOps interface {
	// LabelLow gives dir a low mandatory label, inherited by its children.
	LabelLow(dir string) error
	// LowToken is a primary token for the child: low integrity and no privileges.
	// The release closes it.
	LowToken() (token uintptr, release func(), err error)
	// JoinJob puts this process in a new kill-on-close job with the given caps (0 is unset);
	// its children are then in it from their creation.
	JoinJob(activeProcesses uint32, memoryBytes int64) error
}

// winWallPlan is what the wall asks of Windows for one policy, before it asks.
type winWallPlan struct {
	Label           []string // directories labelled low, sorted, each once
	ActiveProcesses uint32   // the job's process cap, this process included; 0 unset
	MemoryBytes     int64    // the job's memory cap; 0 unset
}

// planWinWall is the plan for p: every write root, the tmp and the home, absolute and once;
// the process cap is the policy's plus this process, which is in the job too.
func planWinWall(p *Policy) (winWallPlan, error) {
	seen := map[string]bool{}
	var label []string
	for _, d := range append(append([]string{}, p.Writes...), p.Tmp, p.Home) {
		if d == "" {
			continue
		}
		if !filepath.IsAbs(d) {
			return winWallPlan{}, fmt.Errorf("the write root %q is not absolute", d)
		}
		k := strings.ToLower(filepath.Clean(d))
		if seen[k] {
			continue
		}
		seen[k] = true
		label = append(label, filepath.Clean(d))
	}
	if len(label) == 0 {
		return winWallPlan{}, fmt.Errorf("the policy names no write root; a low-integrity child could write nowhere at all")
	}
	sort.Strings(label)
	plan := winWallPlan{Label: label, MemoryBytes: p.MaxMem}
	if p.MaxProcs > 0 {
		plan.ActiveProcesses = uint32(p.MaxProcs) + 1
	}
	return plan, nil
}

// prepareWinWall labels, then makes the token, then joins the job, refusing at the first
// step that fails: a child started after a failed step would be a child the wall does not
// hold. It returns the token for the child and its release.
func prepareWinWall(ops winWallOps, p *Policy) (uintptr, func(), *Refusal) {
	plan, err := planWinWall(p)
	if err != nil {
		r := refuse("sandbox_failed", "the windows wall cannot be planned: %v", err)
		return 0, nil, &r
	}
	for _, d := range plan.Label {
		if err := ops.LabelLow(d); err != nil {
			r := refuse("sandbox_failed", "the write root %s could not be labelled low integrity, so the child could not write there: %v", d, err)
			return 0, nil, &r
		}
	}
	tok, release, err := ops.LowToken()
	if err != nil {
		r := refuse("sandbox_failed", "the low-integrity token could not be made: %v", err)
		return 0, nil, &r
	}
	if err := ops.JoinJob(plan.ActiveProcesses, plan.MemoryBytes); err != nil {
		release()
		r := refuse("sandbox_failed", "the job object could not be made or joined, so the tree would not be held: %v", err)
		return 0, nil, &r
	}
	return tok, release, nil
}
