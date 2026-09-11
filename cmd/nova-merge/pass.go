package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdRun is the verb that ACTS: one pass, or a loop with a written deadline.
//
// EVERY LOOP ENDS ON ITS OWN. --loop requires --hours, because a loop with no deadline is
// a lane that is stuck rather than working and nobody outside can tell the two apart
// (Glenn, 2026-09-09: every ask, child or read has a written deadline and a default
// action; never wait forever). The tool never matches a process by its own command line
// and never touches /tmp.
func cmdRun(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("run")
	once := f.fs.Bool("once", false, "")
	loop := f.fs.Duration("loop", 0, "")
	hours := f.fs.Float64("hours", 0, "")
	plannedRed := f.fs.String("planned-red", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	switch {
	case *once && *loop != 0:
		f.problem("--once and --loop are two ways to run and a pass is one of them; give one")
	case !*once && *loop == 0:
		f.problem("--once or --loop <duration> is required; refusing to guess whether this is one pass or a watch")
	case *loop != 0 && *hours <= 0:
		f.problem("--loop requires --hours <h>, the deadline this loop ends on by itself; a loop with no deadline is a lane that is stuck rather than working and nobody outside can tell the two apart")
	case *loop < 0:
		f.problem(fmt.Sprintf("--loop is how long this waits between passes, and is positive, got %s", *loop))
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("run", *f.lane, stderr)
	if st == nil {
		return code
	}
	// One `run` per lane, for the whole pass: two passes on one lane interleave their
	// reads of a base that each has already moved. The lock is the kernel's, so a killed
	// pass holds nothing.
	release, err := merge.Lock(filepath.Join(*f.lane, merge.RunLock), f.dur())
	if err != nil {
		fmt.Fprintf(stderr, "RUN REFUSED: %s\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	defer release()

	build := deps.BuildID()
	deadline := deps.Now().Add(time.Duration(*hours * float64(time.Hour)))
	exit := 0
	for n := 1; ; n++ {
		if _, err := os.Stat(filepath.Join(*f.lane, merge.StopName)); err == nil {
			fmt.Fprintf(stdout, "RUN NOTE %s\n", oneline.Escape("a stop file is present in this lane: start nothing new; remove it to run again"))
			return exit
		}
		exit = onePass(n, *f.lane, st, f, stdout, stderr, deps, build, *plannedRed)
		if *once {
			return exit
		}
		// Rule 16: at the end of every pass under --loop, read the build id of the
		// binary at this tool's own path. A restart is a person's explicit act.
		if disk := deps.BuildID(); disk != build {
			fmt.Fprintf(stdout, "RUN NEWER build=%s on_disk=%s: the binary changed; this loop ends after this pass; restart it by hand\n",
				oneline.Field(build), oneline.Field(disk))
			return 0
		}
		if !deps.Now().Before(deadline) {
			return exit
		}
		deps.Sleep(*loop)
		if !deps.Now().Before(deadline) {
			return exit
		}
		if fresh, err := merge.Load(*f.lane); err == nil {
			st = fresh
		}
	}
}

// onePass folds the branch's records, runs the pass, and writes the state back.
func onePass(n int, lane string, st *merge.State, f *laneFlags, stdout, stderr io.Writer, deps Deps, build, plannedRed string) int {
	recs := merge.NewRecords(lane, st.LaneBranch, "origin", merge.NewGit(lane, f.dur(), deps.Runner), f.dur())
	pulled, problems, err := foldInto(lane, st, recs, f.dur())
	if err != nil {
		return foldRefused("RUN", stderr, err)
	}
	p := &merge.Pass{
		Lane: lane, State: st, Host: deps.NewHost(st.Repo, f.dur()),
		Clone:   merge.NewGit(filepath.Join(lane, merge.RepoDir), f.dur(), deps.Runner),
		Records: recs, Remote: "origin", Max: *f.max, PlannedRed: plannedRed,
		Build: build, Now: deps.Now(), Stdout: stdout, Stderr: stderr,
		Problems: problems, Pulled: pulled,
	}
	if plannedRed != "" {
		merge.Appendf(lane, deps.Now(), "RUN PASS planned_red=%s", plannedRed)
	}
	res := p.Run(n)
	// The pass wrote its verdicts onto the entries; the lane's own order and those
	// verdicts are what state.json holds, and the write is one read-modify-write under
	// the state lock.
	_ = merge.Update(lane, f.dur(), func(s *merge.State) error {
		s.PRs, s.Branches = st.PRs, st.Branches
		return nil
	})
	return res.Exit()
}

// cmdStatus REPORTS and exits 0 whatever the lane holds.
func cmdStatus(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("status")
	reads := f.fs.String("reads", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("status", *f.lane, stderr)
	if st == nil {
		return code
	}
	recs := merge.NewRecords(*f.lane, st.LaneBranch, "origin", merge.NewGit(*f.lane, f.dur(), deps.Runner), f.dur())
	_, problems, err := foldInto(*f.lane, st, recs, f.dur())
	if err != nil {
		return foldRefused("STATUS", stderr, err)
	}
	p := &merge.Pass{
		Lane: *f.lane, State: st, Host: deps.NewHost(st.Repo, f.dur()),
		Clone:   merge.NewGit(filepath.Join(*f.lane, merge.RepoDir), f.dur(), deps.Runner),
		Records: recs, Remote: "origin", Max: *f.max, Now: deps.Now(),
		Stdout: stdout, Stderr: stderr, Problems: problems,
	}
	return p.Status(*reads)
}

// cmdDryRun is the survey: every read `run` performs, the whole plan rather than a stop
// at the first merge, and NO PATH TO THE MUTATING HELPER AT ALL. Its fold is in memory
// over the lane tip it fetched, so state.json and the checkout are byte-identical
// afterwards -- a property a test can pin and a flag never is.
func cmdDryRun(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("dry-run")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("dry-run", *f.lane, stderr)
	if st == nil {
		return code
	}
	recs := merge.NewRecords(*f.lane, st.LaneBranch, "origin", merge.NewGit(*f.lane, f.dur(), deps.Runner), f.dur())
	tip, err := recs.FetchTip()
	if err != nil {
		fmt.Fprintf(stderr, "RUN REFUSED: the lane branch could not be fetched: %s\n", oneline.Err(err))
		return 2
	}
	folded, err := recs.FoldTip(tip)
	if err != nil {
		fmt.Fprintf(stderr, "RUN REFUSED: the lane branch's records could not be folded: %s\n", oneline.Err(err))
		return 2
	}
	// The fold is IN MEMORY: the state on disk is not touched, so a survey leaves the
	// lane byte-identical.
	snapshot := *st
	snapshot.Apply(folded)
	p := &merge.Pass{
		Lane: *f.lane, State: &snapshot, Host: deps.NewHost(st.Repo, f.dur()),
		Clone:   merge.NewGit(filepath.Join(*f.lane, merge.RepoDir), f.dur(), deps.Runner),
		Records: recs, Remote: "origin", Max: *f.max, Now: deps.Now(),
		Stdout: stdout, Stderr: stderr, Problems: folded.Problems,
		Survey: true, Pulled: folded.Files, LaneTip: tip,
	}
	res := p.Run(0)
	return res.Exit()
}

// cmdPacket hands a reader the smallest sufficient packet: pointers, never the diff.
func cmdPacket(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("packet")
	pr := f.fs.Int("pr", 0, "")
	branch := f.fs.String("branch", "", "")
	who := f.fs.String("who", "", "")
	all := f.fs.Bool("all", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	f.require("who", *who, "the reader this packet is for, as this lane knows them")
	id := entrySelector(f, pr, branch, *all)
	if !*all && id == "" && len(f.problems) == 0 {
		f.problem("--pr <n>, --branch <name> or --all is required; refusing to guess which entries this reader is being handed")
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("packet", *f.lane, stderr)
	if st == nil {
		return code
	}
	recs := merge.NewRecords(*f.lane, st.LaneBranch, "origin", merge.NewGit(*f.lane, f.dur(), deps.Runner), f.dur())
	folded, err := recs.Fold()
	if err != nil {
		fmt.Fprintf(stderr, "RUN REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	// packet is derived from the fold and the host: it WRITES NOTHING and TAKES NO LOCK.
	snapshot := *st
	snapshot.Apply(folded)
	p := &merge.Pass{
		Lane: *f.lane, State: &snapshot, Host: deps.NewHost(st.Repo, f.dur()),
		Clone:   merge.NewGit(filepath.Join(*f.lane, merge.RepoDir), f.dur(), deps.Runner),
		Records: recs, Remote: "origin", Max: *f.max, Now: deps.Now(),
		Stdout: stdout, Stderr: stderr, Problems: folded.Problems,
	}
	return p.Packet(*who, id, *all)
}

// cmdStop writes the stop file: start nothing new and exit.
func cmdStop(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("stop")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("stop", *f.lane, stderr)
	if st == nil {
		return code
	}
	path := filepath.Join(*f.lane, merge.StopName)
	if err := os.WriteFile(path, []byte(deps.Now().UTC().Format(merge.Stamp)+"\n"), 0o644); err != nil {
		fmt.Fprintf(stderr, "RUN REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	merge.Appendf(*f.lane, deps.Now(), "STOP lane=%s", *f.lane)
	fmt.Fprintf(stdout, "STOP OK lane=%s\n", oneline.Field(*f.lane))
	return 0
}
