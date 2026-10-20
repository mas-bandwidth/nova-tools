package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// THE WINDOWS WALL, as a sequence (docs/SPEC-SANDBOX.md, "Windows: the low-integrity job
// body"). It is the minimal body a native Windows member needs before its cards run
// contained (captain, 2026-10-20: the windows build of this tool refused every command with
// no_sandbox, so the member ran --no-wall), built from what Windows gives an ordinary user
// with no service and no driver:
//
//   - WRITES: the child runs under a LOW-INTEGRITY copy of this process's token with every
//     privilege removed (Administrators is left as it is: deny-only crashed cmake).
//     Mandatory integrity control refuses a low-integrity write to any object labelled
//     above low, so the policy's write roots (the job directory, its tmp, the data home)
//     are labelled LOW, file by file, and new files under them inherit it.
//   - THE TREE: this process joins a fresh Job Object before it starts the child, so the
//     child and everything it starts are in the job from their first instruction. The job is
//     kill-on-close and carries the policy's process cap and memory cap.
//
// WHAT IT DOES NOT DO, said here so the OK line is never read as more:
//
//   - READS ARE NOT CONFINED, and an elevated member's child keeps Administrators, so it can
//     read most of the machine.
//   - The child can write EVERY low-labelled object, not only its roots: AppData\LocalLow,
//     HKCU\Software\AppDataLow, %TEMP%\Low, and every other card's labelled directories --
//     there is NO ISOLATION BETWEEN CARDS on one member, and a card can open another card's
//     low process.
//   - On an elevated member it can open services that grant Administrators for start and
//     stop (measured on captain); a dedicated non-admin member user removes that.
//   - The network is not enforced; --net-deny is refused.
//   - Labels outlive the run (the slot's directories are the member's own scratch).
//
// THE LABEL WALK NEVER FOLLOWS A REPARSE POINT (the security read of #6037). A low child can
// make a junction inside its write root with no privilege; a relabel that followed it would
// label the junction's target low, durably -- the user's profile, for one. So the walk labels
// each entry through a handle opened on the entry itself (FILE_FLAG_OPEN_REPARSE_POINT, and a
// handle-based set, which propagates nothing), skips every reparse point and does not descend
// into it, and refuses a root that is one. And a write root that is a volume, the user
// profile, the member's temp, or an ancestor of one of them is refused outright: labelling it
// low would hand the child the tree for good.
//
// The sequence is here, on every OS, over winWallOps; the Win32 calls are wrap_windows.go's.
// The tests drive this file with a fake, so the order -- guard, label, token, job -- and
// every refusal are asserted on any host.

// winWallOps is everything the windows wall reaches Windows through.
type winWallOps interface {
	// LabelLow gives the directory tree at root a low mandatory label, entry by entry,
	// never through a reparse point (labelLowTree).
	LabelLow(root string) error
	// LowToken is a primary token for the child: low integrity and no privileges.
	// The release closes it.
	LowToken() (token uintptr, release func(), err error)
	// JoinJob puts this process in a new kill-on-close job with the given caps (0 is unset);
	// its children are then in it from their creation.
	JoinJob(activeProcesses uint32, memoryBytes int64) error
}

// winGuard is what no write root may be or contain: the user's profile, the member's own
// temporary directory, and the machine's system directories. Empty fields guard nothing.
type winGuard struct {
	Protected []string
}

// winWallPlan is what the wall asks of Windows for one policy, before it asks.
type winWallPlan struct {
	Label           []string // directories labelled low, sorted, each once
	ActiveProcesses uint32   // the job's process cap, this process included; 0 unset
	MemoryBytes     int64    // the job's memory cap; 0 unset
}

// winPathKey is a path as Windows compares it: cleaned, without case.
func winPathKey(p string) string { return strings.ToLower(filepath.Clean(p)) }

// coversPath says whether root is path or an ancestor of it.
func coversPath(root, path string) bool {
	r, p := winPathKey(root), winPathKey(path)
	if r == p {
		return true
	}
	if !strings.HasSuffix(r, string(filepath.Separator)) {
		r += string(filepath.Separator)
	}
	return strings.HasPrefix(p, r)
}

// isVolumeRoot says whether dir is the top of a volume (C:\, \\server\share\, /).
func isVolumeRoot(dir string) bool {
	c := filepath.Clean(dir)
	return filepath.Dir(c) == c
}

// planWinWall is the plan for p: every write root, the tmp and the home, absolute and once;
// none a volume, a protected directory or an ancestor of one. The process cap is the
// policy's plus this process, which is in the job too.
func planWinWall(p *Policy, g winGuard) (winWallPlan, error) {
	seen := map[string]bool{}
	var label []string
	for _, d := range append(append([]string{}, p.Writes...), p.Tmp, p.Home) {
		if d == "" {
			continue
		}
		if !filepath.IsAbs(d) {
			return winWallPlan{}, fmt.Errorf("the write root %q is not absolute", d)
		}
		if isVolumeRoot(d) {
			return winWallPlan{}, fmt.Errorf("the write root %s is a volume; labelling it low would give the child the whole volume for good", d)
		}
		for _, prot := range g.Protected {
			if prot != "" && coversPath(d, prot) {
				return winWallPlan{}, fmt.Errorf("the write root %s is or holds %s; labelling it low would give the child that tree for good", d, prot)
			}
		}
		k := winPathKey(d)
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

// prepareWinWall plans, labels, makes the token and joins the job, refusing at the first
// step that fails: a child started after a failed step would be a child the wall does not
// hold. It returns the token for the child and its release.
func prepareWinWall(ops winWallOps, p *Policy, g winGuard) (uintptr, func(), *Refusal) {
	plan, err := planWinWall(p, g)
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

// errReparseRoot is a write root that is itself a reparse point: refused, never labelled.
var errReparseRoot = errors.New("the write root is a reparse point (a junction or a link); the wall labels a real directory, never what one points at")

// labelLowTree labels root and every entry under it with label, entry by entry, through the
// two seams: isReparse says whether a path is a reparse point (read without following it),
// and label sets the low label on that one path (on Windows through a handle opened on the
// entry itself). A reparse point under the root is skipped and not descended into; a root
// that is one is refused. It returns how many entries it skipped.
func labelLowTree(root string, isReparse func(string) (bool, error), label func(string) error) (int, error) {
	rp, err := isReparse(root)
	if err != nil {
		return 0, err
	}
	if rp {
		return 0, errReparseRoot
	}
	skipped := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if path != root {
			rp, err := isReparse(path)
			if err != nil {
				return err
			}
			if rp {
				skipped++
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		return label(path)
	})
	return skipped, err
}
