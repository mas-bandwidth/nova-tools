package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// openLane is what every verb but init does first: read the lane's state, and refuse a
// directory that is not one with the command that makes one. Never a state file written
// on the way past.
func openLane(verb, lane string, stderr io.Writer) (*merge.State, int) {
	st, err := merge.Load(lane)
	if errors.Is(err, merge.ErrNotALane) {
		fmt.Fprintf(stderr, "nova-merge %s: %s\n", verb, oneline.Escape(merge.NotALaneRefusal(lane)))
		return nil, 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "nova-merge %s: %s\n", verb, oneline.Err(err))
		return nil, 2
	}
	// A STOPPED LANE SAYS SO ON EVERY VERB. Only `run` read this file, so after STOP OK a
	// person checking `status` saw a normal lane and `dry-run` printed stopped=0 -- the
	// one signal that says "start nothing new" was invisible to the verbs a person looks
	// at first. `run` prints its own, on every pass of its loop.
	if verb != "run" {
		if _, err := os.Stat(filepath.Join(lane, merge.StopName)); err == nil {
			fmt.Fprintf(stderr, "%s NOTE a stop file is present in this lane: start nothing new; remove %s to run again\n",
				strings.ToUpper(verb), oneline.Field(filepath.Join(lane, merge.StopName)))
		}
	}
	return st, 0
}

// reloadLane is merge.Load, named here so a test can make add's re-read fail and pin that
// the failure is refused -- ADD REFUSED, exit 2 -- and never a nil dereference. It is the
// one seam in this verb, for the same reason records.go names its Sleep.
var reloadLane = merge.Load

// entrySelector is (--pr <n>|--branch <name>) on read, gate and packet: exactly one.
func entrySelector(f *laneFlags, pr *int, branch *string, allowNone bool) string {
	switch {
	case *pr > 0 && *branch != "":
		f.problem("--pr and --branch name two entries and a verb acts on one; give one of them")
		return ""
	case *pr > 0:
		return strconv.Itoa(*pr)
	case *branch != "":
		return *branch
	}
	if !allowNone {
		f.problem("--pr <n> or --branch <name> is required; refusing to guess which entry this is about")
	}
	return ""
}

// cmdRead records a reader's verdict: ONE IMMUTABLE FILE, written to the outbox and
// pushed to the lane's branch by the tool (rules 19 and 22).
//
// --head is required and is the full sha the reader had open. The tool never fills it in
// from the entry's current oid, because the entry's head at record time is not the head
// the reader read: if Emma finishes reading H1 and the author pushes H2 before she types
// the verb, a stamp taken at record time would put her H1 judgment on H2.
func cmdRead(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("read")
	pr := f.fs.Int("pr", 0, "")
	branch := f.fs.String("branch", "", "")
	who := f.fs.String("who", "", "")
	head := f.fs.String("head", "", "")
	verdict := f.fs.String("verdict", "", "")
	note := f.fs.String("note", "", "")
	scope := f.fs.String("scope", "", "")
	releases := f.fs.String("releases", "", "")
	// --redis names the store the typed line becomes one kind=read event in (#2683).
	// Without it the lane-branch record is the only truth and nothing else is written.
	redisAddr := f.fs.String("redis", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	id := entrySelector(f, pr, branch, false)
	f.require("who", *who, "the name of the line recording this verdict, as this lane knows it")
	f.require("head", *head, "the full 40-character sha the reader had open; the verdict binds to that sha and never to whatever the entry's head is now")
	if *head != "" && !merge.IsSHA(*head) {
		f.problem(fmt.Sprintf("--head wants the full 40-character sha the reader had open, got %q; a truncated sha might name the wrong commit", *head))
	}
	if *verdict != "approve" && *verdict != "hold" {
		f.problem(fmt.Sprintf("--verdict is approve or hold, got %q; refusing to guess", *verdict))
	}
	if *releases != "" && *scope == "" {
		f.problem("--releases requires --scope <text>")
	}
	var releaseIDs []string
	if *releases != "" {
		for _, r := range strings.Split(*releases, ",") {
			trimmed := strings.TrimSpace(r)
			if trimmed == "" {
				continue
			}
			if !isValidReleaseID(trimmed) {
				f.problem(fmt.Sprintf("release id %q is invalid: must be record:<at>, review:<id>, or comment:<id>", trimmed))
				break
			}
			releaseIDs = append(releaseIDs, trimmed)
		}
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("read", *f.lane, stderr)
	if st == nil {
		return code
	}
	nameAnEntryThisLaneDoesNotHold("read", st, id, stderr)
	nameAnUnheldObject("read", "head", *head, *f.lane, f.dur(), deps, stderr,
		"a verdict binds to a sha, and a sha nothing holds is a verdict about nothing; check it, or run nova-merge run --once first to fetch the entry's head")
	sub, err := merge.NewSubmission(deps.Now())
	if err != nil {
		fmt.Fprintf(stderr, "READ REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	item, err := merge.ReadItemScoped(merge.EntryDirName(id), *who, *head, *verdict, *note, *scope, releaseIDs, sub)
	if err != nil {
		fmt.Fprintf(stderr, "READ REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	file := item.Path
	recs := merge.NewRecords(*f.lane, st.LaneBranch, "origin", merge.NewGit(*f.lane, f.dur(), deps.Runner), f.dur())
	merge.Appendf(*f.lane, deps.Now(), "READ entry=%s who=%s head=%s verdict=%s file=%s", id, *who, *head, *verdict, file)
	pushErr := recs.Deliver(sub, []merge.Item{item})
	if pushErr != nil {
		fmt.Fprintf(stderr, "READ FAIL entry=%s who=%s head=%s file=%s pushed=false: %s; re-run the same verb to push it\n",
			oneline.Field(id), oneline.Field(*who), oneline.Field(merge.Short(*head)), oneline.Field(file),
			oneline.Escape(oneline.Cap(pushErr.Error(), oneline.TailBytes)))
		return 1
	}
	if *redisAddr != "" {
		// #2683: the typed line becomes one kind=read event on cards:done, so the 1 s
		// table's done column is a store read and not a per-tick GitHub call. The
		// record is pushed -- that is the truth -- so a store that cannot be reached
		// is a NOTE, never a lost record, and the verb's exit stands.
		silenceRedis()
		rdb := deps.Dial(*redisAddr)
		eventPR := ""
		if *pr > 0 {
			eventPR = id
		}
		ctx, cancel := context.WithTimeout(context.Background(), f.dur())
		_, _, eventErr := emitReadEvent(ctx, rdb, id, *who, *verdict, *head, eventPR, sub.At)
		cancel()
		_ = rdb.Close()
		if eventErr != nil {
			fmt.Fprintf(stderr, "READ NOTE the record is pushed and the read event could not be written to the store: %s; the 1 s table's done count reads cards:done, so re-run the same verb with --redis to post it\n",
				oneline.Err(eventErr))
		}
	}
	if _, _, err := foldInto(*f.lane, st, recs, f.dur()); err != nil {
		// The record is at the remote tip -- that is what pushed=true means -- so this
		// is a NOTE and never a lost record: the next run folds it.
		fmt.Fprintf(stderr, "READ NOTE the record is pushed and this lane could not fold the branch afterwards: %s; the next run folds it\n", oneline.Err(err))
	}
	current, approvals, holds, stale := standingOf(st, id, *head)
	fmt.Fprintf(stdout, "READ OK entry=%s who=%s verdict=%s head=%s current=%s approvals=%d holds=%d stale=%d file=%s pushed=true\n",
		oneline.Field(id), oneline.Field(*who), oneline.Field(*verdict), oneline.Field(merge.Short(*head)),
		current, approvals, holds, stale, oneline.Field(file))
	return 0
}

func isValidReleaseID(id string) bool {
	prefix, val, has := strings.Cut(id, ":")
	if !has || strings.TrimSpace(val) == "" {
		return false
	}
	switch prefix {
	case "record", "review", "comment":
		return true
	default:
		return false
	}
}

// nameAnEntryThisLaneDoesNotHold says when a record is being written for an entry this
// lane's own state does not list. A RECORD IS IMMUTABLE, so an approve for a typo'd entry
// is an approve nobody can take back -- and `READ OK ... pushed=true` said nothing at all.
//
// It is a NOTE and NOT a refusal, and rule 22 is why: "add and add-branch write state.json
// only: the order of the lane is the coordinator's and is not shared", so a reader on
// another machine has a lane that lists none of the coordinator's entries and records for
// them anyway -- which is the case TestAReadFromAnotherMachineReachesTheCoordinatorsNextPass
// drives. A wall here would break the multi-machine shape the whole rule exists for.
func nameAnEntryThisLaneDoesNotHold(verb string, st *merge.State, id string, stderr io.Writer) {
	if st.Find(id) != nil {
		return
	}
	fmt.Fprintf(stderr, "%s NOTE entry=%s: this lane's own state does not list it, so nothing here will fold this record -- which is right for a reader on another machine, and a typo otherwise; nova-merge status --lane <dir> lists what this lane holds\n",
		strings.ToUpper(verb), oneline.Field(id))
}

// nameAnUnheldObject says when a sha this record binds to is in neither the lane's clone
// nor its checkout. It is a NOTE and not a wall: a head may simply not be fetched yet. But
// `READ OK ... pushed=true` for forty zeros said nothing at all.
func nameAnUnheldObject(verb, what, sha, lane string, timeout time.Duration, deps Deps, stderr io.Writer, why string) {
	clone := merge.NewGit(filepath.Join(lane, merge.RepoDir), timeout, deps.Runner)
	if merge.HasObject(clone, sha) {
		return
	}
	fmt.Fprintf(stderr, "%s NOTE %s=%s: this lane's clone holds no commit with this sha; %s\n",
		strings.ToUpper(verb), what, oneline.Field(merge.Short(sha)), oneline.Escape(why))
}

// standingOf is READ OK's counts: current says whether the sha the reader supplied is the
// oid the last pass recorded, so a reader who is already stale hears it at once.
func standingOf(st *merge.State, id, head string) (current string, approvals, holds, stale int) {
	e := st.Find(id)
	if e == nil || e.OID == "" {
		return "-", 0, 0, 0
	}
	s := merge.EvaluateReads(e, "")
	current = "false"
	if head == e.OID {
		current = "true"
	}
	return current, s.Approves, s.Holds, s.Stale
}

// cmdGate records a gate runner's verdict for ONE integration commit, named by sha.
//
// All four are refusals rather than tolerances: a gate with a truncated sha is a gate
// that might match the wrong commit, a gate with no base is a gate for a commit nobody
// can name, a gate with no merge is a gate for an object nobody can publish, and a gate
// with no summary is a claim with no evidence behind it.
func cmdGate(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("gate")
	pr := f.fs.Int("pr", 0, "")
	branch := f.fs.String("branch", "", "")
	head := f.fs.String("head", "", "")
	baseSHA := f.fs.String("base-sha", "", "")
	mergeSHA := f.fs.String("merge", "", "")
	verdict := f.fs.String("verdict", "", "")
	summary := f.fs.String("summary", "", "")
	benchName, machines := gateBench(f)
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	bench, benchErr := gateBenchValidate(*benchName, *machines, stderr)
	if benchErr != nil {
		fmt.Fprintf(stderr, "%s\n", benchErr)
		return 2
	}
	id := entrySelector(f, pr, branch, false)
	for _, s := range []struct{ name, value, wants string }{
		{"head", *head, "the full 40-character sha of the entry's head this gate was taken for"},
		{"base-sha", *baseSHA, "the full 40-character sha of the base this gate was taken against (rule 18)"},
		{"merge", *mergeSHA, "the full 40-character sha of the integration commit that was gated (rule 21)"},
	} {
		if strings.TrimSpace(s.value) == "" {
			f.problem(fmt.Sprintf("--%s is required; refusing to guess: %s", s.name, s.wants))
		} else if !merge.IsSHA(s.value) {
			f.problem(fmt.Sprintf("--%s wants a full 40-character sha, got %q: %s", s.name, s.value, s.wants))
		}
	}
	if *verdict != "green" && *verdict != "red" {
		f.problem(fmt.Sprintf("--verdict is green or red, got %q; refusing to guess", *verdict))
	}
	f.require("summary", *summary, "the path of the gate's own summary, which must exist: a gate with no summary is a claim with no evidence behind it")
	if *summary != "" {
		if _, err := os.Stat(*summary); err != nil {
			f.problem(fmt.Sprintf("--summary %q does not exist; a gate summary is read at record time, not trusted as a path", *summary))
		}
	}
	// THE TWO KINDS, told apart by the shas. A base gate is the base merged onto itself,
	// so all three are equal; an integration gate's three differ. Any other mix names no
	// object either kind can validate.
	if merge.IsSHA(*head) && merge.IsSHA(*baseSHA) && merge.IsSHA(*mergeSHA) {
		allEqual := *head == *baseSHA && *baseSHA == *mergeSHA
		allDiffer := *head != *baseSHA && *mergeSHA != *head && *mergeSHA != *baseSHA
		if !allEqual && !allDiffer {
			f.problem("these three shas name no object either kind of gate can validate: a BASE gate has --head, --base-sha and --merge all the base's own sha, and an INTEGRATION gate has three that differ")
		}
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("gate", *f.lane, stderr)
	if st == nil {
		return code
	}
	abs, err := filepath.Abs(*summary)
	if err != nil {
		abs = *summary
	}
	sub, err := merge.NewSubmission(deps.Now())
	if err != nil {
		fmt.Fprintf(stderr, "GATE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	file := merge.GateFile(merge.EntryDirName(id), *head, *baseSHA, sub)
	rec := merge.Gate{Head: *head, Base: *baseSHA, Merge: *mergeSHA, Verdict: *verdict,
		Summary: abs, At: sub.At, Run: sub.Rand, File: file}
	if *pr > 0 {
		rec.PR = *pr
	} else {
		rec.Branch = *branch
	}
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "GATE REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	body = append(body, '\n')
	summaryBytes, err := os.ReadFile(*summary)
	if err != nil {
		fmt.Fprintf(stderr, "GATE REFUSED: the summary could not be read: %s\n", oneline.Err(err))
		return 2
	}
	nameAnUnheldObject("gate", "merge", *mergeSHA, *f.lane, f.dur(), deps, stderr,
		"this record will not satisfy the merge predicate, which asks for an object THIS CLONE holds whose parents are the base and the head (rule 21); RUN BUILT is where the merge sha comes from")
	// in_lane= is about the ENTRY, which is not the phrase a reader expects here; the
	// NOTE above is what says whether the merge object is one this lane can publish. See
	// the PR body: entry_in_lane= plus merge_in_clone= is proposed for the grammar.
	inLane := st.Find(id) != nil
	newest := isNewest(st, id, *head, *baseSHA, sub.At)
	recs := merge.NewRecords(*f.lane, st.LaneBranch, "origin", merge.NewGit(*f.lane, f.dur(), deps.Runner), f.dur())
	merge.Appendf(*f.lane, deps.Now(), "GATE entry=%s head=%s base=%s merge=%s bench=%s verdict=%s file=%s", id, *head, *baseSHA, *mergeSHA, bench, *verdict, file)
	pushErr := recs.Deliver(sub, []merge.Item{
		{Path: file, Body: body},
		{Path: merge.SummaryFile(file), Body: summaryBytes},
	})
	if pushErr != nil {
		fmt.Fprintf(stderr, "GATE FAIL entry=%s head=%s base=%s merge=%s bench=%s file=%s pushed=false: %s; re-run the same verb to push it\n",
			oneline.Field(id), oneline.Field(merge.Short(*head)), oneline.Field(merge.Short(*baseSHA)),
			oneline.Field(merge.Short(*mergeSHA)), oneline.Field(bench), oneline.Field(file),
			oneline.Escape(oneline.Cap(pushErr.Error(), oneline.TailBytes)))
		return 1
	}
	if _, _, err := foldInto(*f.lane, st, recs, f.dur()); err != nil {
		fmt.Fprintf(stderr, "GATE NOTE the record is pushed and this lane could not fold the branch afterwards: %s; the next run folds it\n", oneline.Err(err))
	}
	fmt.Fprintf(stdout, "GATE OK entry=%s head=%s base=%s merge=%s bench=%s verdict=%s summary=%s in_lane=%t newest=%t file=%s pushed=true\n",
		oneline.Field(id), oneline.Field(merge.Short(*head)), oneline.Field(merge.Short(*baseSHA)),
		oneline.Field(merge.Short(*mergeSHA)), oneline.Field(bench), oneline.Field(*verdict), oneline.Field(abs), inLane, newest, oneline.Field(file))
	return 0
}

// isNewest says whether an even newer record for the pair already exists, so GATE OK can
// say newest=false rather than leave a caller believing their record decides.
func isNewest(st *merge.State, id, head, base, at string) bool {
	for _, g := range st.Gates {
		if g.ID() == id && g.Head == head && g.Base == base && g.At > at {
			return false
		}
	}
	return true
}

// foldInto pulls the lane branch and folds every record file into the state, under the
// state lock. The lists are the fold and the files are the truth.
//
// ITS FAILURE IS RETURNED. The pull's error was assigned to `_`: a checkout-lock wait that
// ran out, or a fetch that failed, left the fold running over the local files and the
// caller deciding on the previous state, so a hold or a newer red that reached the remote
// since the last pull was simply absent from the decision and nothing said so. The fold is
// the only source of records other machines wrote.
func foldInto(lane string, st *merge.State, recs *merge.Records, timeout time.Duration) (int, []merge.FoldProblem, error) {
	pulled, err := recs.Pull()
	if err != nil {
		return pulled, nil, err
	}
	folded, err := recs.Fold()
	if err != nil {
		return pulled, nil, err
	}
	if folded == nil {
		return pulled, nil, nil
	}
	// THE FOLD'S OWN WRITE IS RETURNED. It was assigned to `_`, so a state lock another
	// verb held made the fold a no-op that said nothing: the records were pulled, the
	// caller decided on the state as it was before them, and the file kept a fold that
	// never happened.
	if err := merge.Update(lane, timeout, func(s *merge.State) error {
		s.Apply(folded)
		st.PRs, st.Branches, st.Gates = s.PRs, s.Branches, s.Gates
		return nil
	}); err != nil {
		return pulled, nil, err
	}
	if fresh, err := merge.Load(lane); err == nil {
		*st = *fresh
	}
	return pulled, folded.Problems, nil
}
