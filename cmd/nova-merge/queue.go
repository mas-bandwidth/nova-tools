package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The merge queue (docs/SPEC-MERGE.md, "The merge queue (#1142), 2026-09-17"): one queue,
// one hold file, one order. `queue` is the mechanical hand that keeps the lane's order,
// and `queue classify` records one typed decision behind the merge group's floor.

// scanQueueArgs reads key=value and key value flags anywhere on the line, so a subverb's
// positional argument may sit between them. Everything that is not a known flag is
// positional.
func scanQueueArgs(args []string, known map[string]bool) (map[string]string, []string, error) {
	opts := map[string]string{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			name, val, has := strings.Cut(a[2:], "=")
			if !known[name] {
				return nil, nil, fmt.Errorf("unknown flag --%s", name)
			}
			if !has {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("--%s wants a value", name)
				}
				i++
				val = args[i]
			}
			opts[name] = val
			continue
		}
		pos = append(pos, a)
	}
	return opts, pos, nil
}

// queueRefuse is the queue's "could not run": one line on stderr, exit 2, and one refuse
// event so a refusal is on the stream beside the reads that worked. The emitter may be the
// provisional stderr one (see cmdQueue) when the line could not even be scanned.
func queueRefuse(em *log.Emitter, stderr io.Writer, reason string) int {
	fmt.Fprintf(stderr, "QUEUE REFUSED: %s\n", oneline.Escape(oneline.Cap(reason, oneline.TailBytes)))
	emitErr(em, log.EventRefuse, "queue: the read was refused", errors.New(oneline.Cap(reason, oneline.TailBytes)))
	return 2
}

func classifyRefuse(em *log.Emitter, stderr io.Writer, reason string) int {
	fmt.Fprintf(stderr, "CLASSIFY REFUSED: %s\n", oneline.Escape(oneline.Cap(reason, oneline.TailBytes)))
	emitErr(em, log.EventRefuse, "queue classify: the record was refused", errors.New(oneline.Cap(reason, oneline.TailBytes)))
	return 2
}

func hasInt(list []int, n int) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

func cmdQueue(args []string, stdout, stderr io.Writer, deps Deps) int {
	// THE PROVISIONAL EMITTER. The sink --log names is on the line this function has not
	// scanned yet, and a line that cannot be scanned is still a refusal somebody must see
	// on the stream; so the emitter starts on stderr -- which under systemd is the unit's
	// journal -- and is replaced by the real one the moment the flags are read.
	em := log.NewEmitter(stderr, sourceMerge, "queue", "")
	// `classify` is the one subverb with a flag set of its own (--run, --verdict, --head,
	// --note, --pr, --branch, --test), so it is taken off the line before the queue's own
	// flags are scanned -- scanQueueArgs refuses a flag it does not know, and those are
	// the subverb's, not the queue's.
	if len(args) > 0 && args[0] == "classify" {
		return cmdQueueClassify(args[1:], stdout, stderr, deps)
	}
	// `audit` is the second subverb with a flag set of its own (--repo, --dry-run,
	// --timeout) and, unlike every other one, it is NOT A LANE VERB: a repository's
	// standing auto-merges are a property of the forge, so it is taken off the line before
	// the lane is opened.
	if len(args) > 0 && args[0] == "audit" {
		return cmdQueueAudit(auditArgsFor(args[1:]), stdout, stderr, deps)
	}
	known := map[string]bool{"lane": true, "timeout": true, "max": true, "who": true, "window": true,
		"log": true, "bench": true}
	opts, pos, err := scanQueueArgs(args, known)
	if err != nil {
		return queueRefuse(em, stderr, err.Error())
	}
	realEm, closeEvents, code := openEmitter("queue", opts["bench"], opts["log"], stderr, deps)
	if code != 0 {
		return code
	}
	defer closeEvents()
	em = realEm
	lane := opts["lane"]
	if strings.TrimSpace(lane) == "" {
		return queueRefuse(em, stderr, "--lane is required and is the lane's own directory; refusing to guess")
	}
	st, code := openLane("queue", lane, stderr)
	if st == nil {
		return code
	}
	timeout := 120 * time.Second
	if raw, ok := opts["timeout"]; ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return queueRefuse(em, stderr, fmt.Sprintf("--timeout is a number of seconds, got %q", raw))
		}
		timeout = time.Duration(n) * time.Second
	}
	if len(pos) == 0 {
		return queueRefuse(em, stderr, "queue wants one of hold, release, skip, unskip, front, sweep, classify, audit: "+
			`nova-merge queue --lane <dir> hold "<reason>" | release | skip <pr>... | unskip <pr>... | front <pr> | sweep --window <duration> | classify --run <id> --verdict <verdict>`)
	}
	sub, rest := pos[0], pos[1:]
	switch sub {
	case "hold":
		reason := strings.TrimSpace(strings.Join(rest, " "))
		if reason == "" {
			return queueRefuse(em, stderr, `a hold wants a reason; a hold nobody can read is not a hold: nova-merge queue hold "<reason>"`)
		}
		by := opts["who"]
		if by == "" {
			by = "unknown"
		}
		release, err := merge.Lock(filepath.Join(lane, merge.StateLock), timeout)
		if err != nil {
			return queueRefuse(em, stderr, oneline.Err(err))
		}
		if err := merge.WriteHold(lane, reason, by, deps.Now()); err != nil {
			release()
			return queueRefuse(em, stderr, oneline.Err(err))
		}
		release()
		merge.Appendf(lane, deps.Now(), "QUEUE HOLD reason=%s by=%s", reason, by)
		fmt.Fprintf(stdout, "QUEUE HOLD reason=%s path=%s by=%s\n",
			oneline.Field(reason), oneline.Field(merge.HoldPath(lane)), oneline.Field(by))
		// A hold stops the whole lane, so the audit names no entry and the depth that
		// follows it reports nothing running.
		emitQueueAudit(em, 0, "hold", reason+" (by "+by+")")
		emitLaneDepth(em, "hold", lane, st, true)
		return 0
	case "release":
		release, err := merge.Lock(filepath.Join(lane, merge.StateLock), timeout)
		if err != nil {
			return queueRefuse(em, stderr, oneline.Err(err))
		}
		h, present, err := merge.ClearHold(lane)
		release()
		if err != nil {
			return queueRefuse(em, stderr, oneline.Err(err))
		}
		held := time.Duration(0)
		if present && h.At != "" {
			if at, err := time.Parse(merge.Stamp, h.At); err == nil {
				held = deps.Now().Sub(at)
			}
		}
		merge.Appendf(lane, deps.Now(), "QUEUE RELEASE held=%s", held)
		fmt.Fprintf(stdout, "QUEUE RELEASE held=%s path=%s\n",
			oneline.Field(held.String()), oneline.Field(merge.HoldPath(lane)))
		emitLaneDepth(em, "release", lane, st, false)
		return 0
	case "skip", "unskip":
		prs, err := parsePRs(rest)
		if err != nil {
			return queueRefuse(em, stderr, fmt.Sprintf("%s: %s; nova-merge queue %s <pr>...", oneline.Field(sub), oneline.Escape(err.Error()), oneline.Field(sub)))
		}
		if len(prs) == 0 {
			return queueRefuse(em, stderr, fmt.Sprintf("nova-merge queue %s <pr>...", oneline.Field(sub)))
		}
		q, err := merge.UpdateQueue(lane, st, timeout, func(q *merge.Queue) error {
			for _, pr := range prs {
				q.Queued = merge.QueueRemove(q.Queued, pr)
				if sub == "skip" {
					if !hasInt(q.Skipped, pr) {
						q.Skipped = append(q.Skipped, pr)
					}
				} else {
					q.Skipped = merge.QueueRemove(q.Skipped, pr)
					q.DropPark(pr)
					if !hasInt(q.Queued, pr) {
						q.Queued = append(q.Queued, pr)
					}
				}
			}
			return nil
		})
		if err != nil {
			return queueRefuse(em, stderr, oneline.Err(err))
		}
		for _, pr := range prs {
			verb := "SKIP"
			if sub == "unskip" {
				verb = "UNSKIP"
			}
			fmt.Fprintf(stdout, "QUEUE %s entry=%d skipped=%d queued=%d\n", oneline.Field(verb), pr, len(q.Skipped), len(q.Queued))
			if sub == "skip" {
				emitQueueAudit(em, pr, "skip", "a hand took this entry out of the lane's order")
			}
		}
		merge.Appendf(lane, deps.Now(), "QUEUE %s %v", strings.ToUpper(sub), prs)
		emitQueueDepth(em, sub, q, holdStands(lane))
		return 0
	case "front":
		if len(rest) != 1 {
			return queueRefuse(em, stderr, "front wants one pull request: nova-merge queue front <pr>")
		}
		pr, err := strconv.Atoi(rest[0])
		if err != nil || pr < 1 {
			return queueRefuse(em, stderr, fmt.Sprintf("front wants a pull request's number, got %q", rest[0]))
		}
		id := strconv.Itoa(pr)
		if st.Find(id) == nil {
			return queueRefuse(em, stderr, fmt.Sprintf("pull request %d is not in this lane: nova-merge add --lane %s --pr %d", pr, oneline.Field(lane), pr))
		}
		host := deps.NewHost(st.Repo, timeout)
		if data, err := host.PR(pr); err != nil {
			return queueRefuse(em, stderr, fmt.Sprintf("pull request %d could not be read: %s", pr, oneline.Err(err)))
		} else if data.Merged || data.Closed {
			return queueRefuse(em, stderr, fmt.Sprintf("pull request %d is not open; front moves an open pull request: nova-merge add --lane %s --pr %d", pr, oneline.Field(lane), pr))
		} else {
			checks, err := host.Checks(data.HeadOID)
			if err != nil {
				return queueRefuse(em, stderr, fmt.Sprintf("pull request %d's checks could not be read: %s", pr, oneline.Err(err)))
			}
			if checks.Red > 0 || checks.Pending > 0 || checks.Total() == 0 {
				return queueRefuse(em, stderr, fmt.Sprintf("pull request %d is not green (%s); front moves a green pull request", pr, oneline.Field(checks.Field())))
			}
		}
		var displaced int
		q, err := merge.UpdateQueue(lane, st, timeout, func(q *merge.Queue) error {
			displaced = len(q.Queued)
			for i, x := range q.Queued {
				if x == pr {
					displaced = i
					break
				}
			}
			q.Queued = merge.QueueRemove(q.Queued, pr)
			q.Skipped = merge.QueueRemove(q.Skipped, pr)
			q.DropPark(pr)
			q.Queued = append([]int{pr}, q.Queued...)
			return nil
		})
		if err != nil {
			return queueRefuse(em, stderr, oneline.Err(err))
		}
		merge.Appendf(lane, deps.Now(), "QUEUE FRONT entry=%d displaced=%d", pr, displaced)
		fmt.Fprintf(stdout, "QUEUE FRONT entry=%d position=1 queued=%d displaced=%d\n", pr, len(q.Queued), displaced)
		emitQueueDepth(em, "front", q, holdStands(lane))
		return 0
	case "sweep":
		return cmdQueueSweep(lane, opts, st, timeout, stdout, stderr, deps, em)
	}
	return queueRefuse(em, stderr, fmt.Sprintf("queue subverb %q is not one of hold, release, skip, unskip, front, sweep, classify, audit", sub))
}

func parsePRs(args []string) ([]int, error) {
	var out []int
	for _, a := range args {
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%q is not a pull request's number", a)
		}
		out = append(out, n)
	}
	return out, nil
}

// queueHost is the sweep's seam to the forge's listing. The method is QueuePRs and not
// OpenPRs because a host also answers the rebase cutter's list, whose rows are the four
// fields of a merge.RebasePR; the sweep reads a whole merge.PR -- head oid, mergeable
// state, when the host last saw it move -- and one method cannot answer both shapes.
type queueHost interface {
	QueuePRs() ([]merge.PR, error)
}

// poisonHost is the poison detector's seam: the failing tests, the changed packages and
// the issue a park names.
type poisonHost interface {
	PoisonFailures(pr int) []merge.Failure
	ChangedPackages(pr int) []string
	IssueFor(pr int) string
}

func cmdQueueSweep(lane string, opts map[string]string, st *merge.State, timeout time.Duration, stdout, stderr io.Writer, deps Deps, em *log.Emitter) int {
	rawWindow, ok := opts["window"]
	if !ok || strings.TrimSpace(rawWindow) == "" {
		return queueRefuse(em, stderr, "a sweep wants the session window it walks; --window <duration>  # a sweep with no window would enqueue the world")
	}
	window, err := time.ParseDuration(rawWindow)
	if err != nil || window <= 0 {
		return queueRefuse(em, stderr, fmt.Sprintf("--window wants a duration, got %q; --window <duration>", rawWindow))
	}
	if h, present, err := merge.ReadHold(lane); err != nil {
		return queueRefuse(em, stderr, oneline.Err(err))
	} else if present {
		return queueRefuse(em, stderr, fmt.Sprintf("a hold is standing (%s): %s; nova-merge queue release", oneline.Field(h.Reason), oneline.Field(h.By)))
	}
	host := deps.NewHost(st.Repo, timeout)
	lister, ok := host.(queueHost)
	if !ok {
		return queueRefuse(em, stderr, "this host cannot list open pull requests, and a sweep walks them: no host, no sweep")
	}
	prs, err := lister.QueuePRs()
	if err != nil {
		return queueRefuse(em, stderr, oneline.Err(err))
	}
	classifyRecs, err := merge.LoadClassifies(lane)
	if err != nil {
		return queueRefuse(em, stderr, oneline.Err(err))
	}
	ph, _ := host.(poisonHost)
	var scanned, green, queued, already, skipped, dirty, parkedCount, staleRed, rerun int
	var parkLines []string
	var parked []merge.Park
	var depth *merge.Queue
	if _, err := merge.UpdateQueue(lane, st, timeout, func(q *merge.Queue) error {
		for _, pr := range prs {
			if pr.Closed || pr.Merged {
				continue
			}
			if pr.UpdatedAt != "" {
				if at, err := time.Parse(merge.Stamp, pr.UpdatedAt); err == nil && deps.Now().Sub(at) > window {
					continue
				}
			}
			scanned++
			switch {
			case q.IsParked(pr.Number):
				parkedCount++
			case q.HasSkip(pr.Number):
				skipped++
			case strings.EqualFold(pr.Mergeable, "CONFLICTING"):
				dirty++
			default:
				checks, err := host.Checks(pr.HeadOID)
				if err != nil {
					return err
				}
				head := checks.ForSHA(pr.HeadOID)
				switch {
				case head.Red > 0:
					// The poison detector reads the pull request's own run, so a queued
					// candidate that turns red is still parked.
					if ph != nil {
						if p, line, armed := poison(ph, classifyRecs, pr); armed {
							q.DropPark(pr.Number)
							q.Parked = append(q.Parked, p)
							q.Skipped = append(q.Skipped, pr.Number)
							q.Queued = merge.QueueRemove(q.Queued, pr.Number)
							parkedCount++
							parked = append(parked, p)
							parkLines = append(parkLines, line)
						}
					}
				case head.Red == 0 && head.Pending == 0 && head.Green > 0:
					if hasInt(q.Queued, pr.Number) {
						already++
					} else {
						green++
						q.Queued = append(q.Queued, pr.Number)
						queued++
					}
				case hasStaleRed(checks, pr.HeadOID):
					staleRed++
					if len(q.Queued) <= 5 {
						q.Queued = append(q.Queued, pr.Number)
						queued++
						rerun++
					}
				default:
					if hasInt(q.Queued, pr.Number) {
						already++
					}
				}
			}
		}
		depth = q
		return nil
	}); err != nil {
		return queueRefuse(em, stderr, oneline.Err(err))
	}
	for _, line := range parkLines {
		fmt.Fprintln(stdout, oneline.Escape(line))
	}
	// One audit per entry the sweep itself took out of the lane: the poison detector's
	// park is the queue turning an entry's automatic merge off, and the test that armed
	// it is the reason.
	for _, p := range parked {
		emitQueueAudit(em, p.PR, "park",
			fmt.Sprintf("%s failed %d times in %s; issue %s",
				oneline.Field(p.Test), p.Runs, oneline.Field(p.Package), oneline.Field(p.Issue)))
	}
	fmt.Fprintf(stdout, "QUEUE SWEEP window=%s scanned=%d green=%d queued=%d already=%d skipped=%d dirty=%d parked=%d stale_red=%d rerun=%d\n",
		oneline.Field(mergeWindow(window)), scanned, green, queued, already, skipped, dirty, parkedCount, staleRed, rerun)
	merge.Appendf(lane, deps.Now(), "QUEUE SWEEP window=%s scanned=%d queued=%d rerun=%d", mergeWindow(window), scanned, queued, rerun)
	if depth != nil {
		emitQueueDepth(em, "sweep", depth, false)
	}
	return 0
}

func mergeWindow(d time.Duration) string { return d.String() }

// holdStands is whether a hold is standing over this lane right now. It is what makes
// `running` zero on the depth line: a held lane is acting on nothing at all. A hold file
// that cannot be read is no hold, because the depth line is an observation and never a gate.
func holdStands(lane string) bool {
	_, present, err := merge.ReadHold(lane)
	return err == nil && present
}

// emitLaneDepth is emitQueueDepth for the two subverbs that do not open the queue
// themselves -- hold and release, which write the hold file. The queue is read here so that
// EVERY read of the lane puts one depth line on the stream, which is what the panel counts
// on: a lane held for an hour must not go quiet on the dashboard.
func emitLaneDepth(e *log.Emitter, sub, lane string, st *merge.State, held bool) {
	q, err := merge.LoadQueue(lane, st)
	if err != nil {
		return
	}
	emitQueueDepth(e, sub, q, held)
}

// hasStaleRed reports whether this commit carries a red conclusion on a sha it has moved past.
func hasStaleRed(c merge.Checks, oid string) bool {
	for _, d := range c.Details {
		if merge.Bucket(d.Conclusion) == "red" && d.SHA != "" && d.SHA != oid {
			return true
		}
	}
	return false
}

// poison arms the detector: a test that failed at least twice, in a package this pull
// request changed, whose newest classify for the head is own-change.
func poison(ph poisonHost, recs map[string]merge.ClassRecord, pr merge.PR) (merge.Park, string, bool) {
	changed := map[string]bool{}
	for _, p := range ph.ChangedPackages(pr.Number) {
		changed[p] = true
	}
	for _, f := range ph.PoisonFailures(pr.Number) {
		if f.Count < 2 || !changed[f.Package] {
			continue
		}
		verdict := ""
		for _, rec := range recs {
			if rec.Head == pr.HeadOID || rec.Head == "" {
				if rec.Verdict == merge.ClassOwnChange {
					verdict = rec.Verdict
					break
				}
			}
		}
		if verdict != merge.ClassOwnChange {
			return merge.Park{}, "", false
		}
		issue := ph.IssueFor(pr.Number)
		if issue == "" {
			issue = "-"
		}
		p := merge.Park{PR: pr.Number, Test: f.Test, Package: f.Package, Runs: f.Count, Issue: issue}
		line := fmt.Sprintf("QUEUE PARK entry=%d test=%s package=%s runs=%d issue=%s",
			pr.Number, oneline.Field(f.Test), oneline.Field(f.Package), f.Count, oneline.Field(issue))
		return p, line, true
	}
	return merge.Park{}, "", false
}

// cmdQueueClassify records one typed decision somebody already reached about a run, as
// one immutable record the queue sweep reads. It is `nova-merge queue classify` and not
// the top-level `classify` (classify_run.go), which ASKS a provider for a decision about
// a failed merge-group run and records nothing: two different asks, so two verbs.
func cmdQueueClassify(args []string, stdout, stderr io.Writer, deps Deps) int {
	known := map[string]bool{"lane": true, "timeout": true, "run": true, "head": true,
		"verdict": true, "note": true, "pr": true, "branch": true, "test": true, "who": true,
		"log": true, "bench": true}
	em := log.NewEmitter(stderr, sourceMerge, "queue", "")
	opts, _, err := scanQueueArgs(args, known)
	if err != nil {
		return classifyRefuse(em, stderr, err.Error())
	}
	realEm, closeEvents, code := openEmitter("queue", opts["bench"], opts["log"], stderr, deps)
	if code != 0 {
		return code
	}
	defer closeEvents()
	em = realEm
	lane := opts["lane"]
	if strings.TrimSpace(lane) == "" {
		return classifyRefuse(em, stderr, "--lane is required and is the lane's own directory; refusing to guess")
	}
	if strings.TrimSpace(opts["run"]) == "" {
		return classifyRefuse(em, stderr, "classify wants the run id it is about; --run <id>")
	}
	verdict := opts["verdict"]
	if !merge.ValidClass(verdict) {
		return classifyRefuse(em, stderr, fmt.Sprintf("--verdict is %s, %s or %s, got %q; refusing to guess",
			oneline.Field(merge.ClassFlaky), oneline.Field(merge.ClassOwnChange),
			oneline.Field(merge.ClassEnvironment), oneline.Escape(verdict)))
	}
	st, laneCode := openLane("classify", lane, stderr)
	if st == nil {
		return laneCode
	}
	timeout := 120 * time.Second
	if raw, ok := opts["timeout"]; ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return classifyRefuse(em, stderr, fmt.Sprintf("--timeout is a number of seconds, got %q", raw))
		}
		timeout = time.Duration(n) * time.Second
	}
	entry := "-"
	if pr, ok := opts["pr"]; ok && pr != "" {
		entry = pr
	} else if b, ok := opts["branch"]; ok && b != "" {
		entry = b
	}
	sub, err := merge.NewSubmission(deps.Now())
	if err != nil {
		return classifyRefuse(em, stderr, oneline.Err(err))
	}
	item, rec, err := merge.NewClassRecord(opts["run"], opts["head"], entry, verdict, opts["note"], opts["who"], sub)
	if err != nil {
		return classifyRefuse(em, stderr, oneline.Err(err))
	}
	test := opts["test"]
	if test == "" {
		test = "-"
	}
	by := opts["who"]
	if by == "" {
		by = "-"
	}
	recs := merge.NewRecords(lane, st.LaneBranch, "origin", merge.NewGit(lane, timeout, deps.Runner), timeout)
	merge.Appendf(lane, deps.Now(), "CLASSIFY run=%s entry=%s verdict=%s file=%s", opts["run"], entry, verdict, rec.File)
	if pushErr := recs.Deliver(sub, []merge.Item{item}); pushErr != nil {
		fmt.Fprintf(stderr, "CLASSIFY FAIL run=%s file=%s pushed=false: %s; re-run the same verb to push it\n",
			oneline.Field(opts["run"]), oneline.Field(rec.File),
			oneline.Escape(oneline.Cap(pushErr.Error(), oneline.TailBytes)))
		return 1
	}
	fmt.Fprintf(stdout, "CLASSIFY OK run=%s entry=%s verdict=%s test=%s by=%s file=%s pushed=true\n",
		oneline.Field(opts["run"]), oneline.Field(entry), oneline.Field(verdict), oneline.Field(test),
		oneline.Field(by), oneline.Field(rec.File))
	return 0
}
