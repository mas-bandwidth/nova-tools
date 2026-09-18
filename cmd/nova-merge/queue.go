package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
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

func queueRefuse(stderr io.Writer, reason string) int {
	fmt.Fprintf(stderr, "QUEUE REFUSED: %s\n", oneline.Escape(oneline.Cap(reason, oneline.TailBytes)))
	return 2
}

func classifyRefuse(stderr io.Writer, reason string) int {
	fmt.Fprintf(stderr, "CLASSIFY REFUSED: %s\n", oneline.Escape(oneline.Cap(reason, oneline.TailBytes)))
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
	known := map[string]bool{"lane": true, "timeout": true, "max": true, "who": true, "window": true}
	opts, pos, err := scanQueueArgs(args, known)
	if err != nil {
		return queueRefuse(stderr, err.Error())
	}
	lane := opts["lane"]
	if strings.TrimSpace(lane) == "" {
		return queueRefuse(stderr, "--lane is required and is the lane's own directory; refusing to guess")
	}
	st, code := openLane("queue", lane, stderr)
	if st == nil {
		return code
	}
	timeout := 120 * time.Second
	if raw, ok := opts["timeout"]; ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return queueRefuse(stderr, fmt.Sprintf("--timeout is a number of seconds, got %q", raw))
		}
		timeout = time.Duration(n) * time.Second
	}
	if len(pos) == 0 {
		return queueRefuse(stderr, "queue wants one of status, hold, release, skip, unskip, front, sweep, classify, audit: "+
			`nova-merge queue --lane <dir> status | hold "<reason>" --who <name> | release | skip <pr>... | unskip <pr>... | front <pr> | sweep --window <duration> | classify --run <id> --verdict <verdict> | audit --repo <owner>/<name>`)
	}
	sub, rest := pos[0], pos[1:]
	switch sub {
	case "status":
		return cmdQueueStatus(lane, st, opts, stdout, stderr)
	case "hold":
		reason := strings.TrimSpace(strings.Join(rest, " "))
		if reason == "" {
			return queueRefuse(stderr, `a hold wants a reason; a hold nobody can read is not a hold: nova-merge queue hold "<reason>" --who <name>`)
		}
		// EDGE 11: --who was undocumented and optional, and a hold written without it
		// said `by=unknown` -- a hold whose owner nobody can ask is a hold nobody dares
		// release. It is required, like every other fact this tool refuses to guess
		// (rule 20); this package reads no environment variable, so `$USER` is the
		// caller's to pass and never this tool's to assume.
		by := strings.TrimSpace(opts["who"])
		if by == "" {
			return queueRefuse(stderr, `--who is required and is the person this hold belongs to; refusing to guess: nova-merge queue hold "<reason>" --who <name>`)
		}
		release, err := merge.Lock(filepath.Join(lane, merge.StateLock), timeout)
		if err != nil {
			return queueRefuse(stderr, oneline.Err(err))
		}
		if err := merge.WriteHold(lane, reason, by, deps.Now()); err != nil {
			release()
			return queueRefuse(stderr, oneline.Err(err))
		}
		release()
		merge.Appendf(lane, deps.Now(), "QUEUE HOLD reason=%s by=%s", reason, by)
		fmt.Fprintf(stdout, "QUEUE HOLD reason=%s path=%s by=%s\n",
			oneline.Field(reason), oneline.Field(merge.HoldPath(lane)), oneline.Field(by))
		return 0
	case "release":
		release, err := merge.Lock(filepath.Join(lane, merge.StateLock), timeout)
		if err != nil {
			return queueRefuse(stderr, oneline.Err(err))
		}
		h, present, err := merge.ClearHold(lane)
		release()
		if err != nil {
			return queueRefuse(stderr, oneline.Err(err))
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
		return 0
	case "skip", "unskip":
		prs, err := parsePRs(rest)
		if err != nil {
			return queueRefuse(stderr, fmt.Sprintf("%s: %s; nova-merge queue %s <pr>...", oneline.Field(sub), oneline.Escape(err.Error()), oneline.Field(sub)))
		}
		if len(prs) == 0 {
			return queueRefuse(stderr, fmt.Sprintf("nova-merge queue %s <pr>...", oneline.Field(sub)))
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
			return queueRefuse(stderr, oneline.Err(err))
		}
		for _, pr := range prs {
			verb := "SKIP"
			if sub == "unskip" {
				verb = "UNSKIP"
			}
			fmt.Fprintf(stdout, "QUEUE %s entry=%d skipped=%d queued=%d\n", oneline.Field(verb), pr, len(q.Skipped), len(q.Queued))
		}
		merge.Appendf(lane, deps.Now(), "QUEUE %s %v", strings.ToUpper(sub), prs)
		return 0
	case "front":
		if len(rest) != 1 {
			return queueRefuse(stderr, "front wants one pull request: nova-merge queue front <pr>")
		}
		pr, err := strconv.Atoi(rest[0])
		if err != nil || pr < 1 {
			return queueRefuse(stderr, fmt.Sprintf("front wants a pull request's number, got %q", rest[0]))
		}
		id := strconv.Itoa(pr)
		if st.Find(id) == nil {
			return queueRefuse(stderr, fmt.Sprintf("pull request %d is not in this lane: nova-merge add --lane %s --pr %d", pr, oneline.Field(lane), pr))
		}
		// EDGE 10: `front` REORDERS NUMBERS IN A FILE. It used to read the pull request
		// and its checks from the forge first and refuse one that was not open and green,
		// which made the one local verb in this family need a network, a gh and a token --
		// and refuse on a bench that had none, over a judgement `run` makes again anyway.
		// The order is not a verdict: `run` re-reads the host every pass and will not
		// land a red or a closed entry whatever position it sits in. So this is local,
		// exactly like skip and unskip, and the one thing it still checks is the one thing
		// it can know by itself -- that the entry is in this lane.
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
			return queueRefuse(stderr, oneline.Err(err))
		}
		merge.Appendf(lane, deps.Now(), "QUEUE FRONT entry=%d displaced=%d", pr, displaced)
		fmt.Fprintf(stdout, "QUEUE FRONT entry=%d position=1 queued=%d displaced=%d\n", pr, len(q.Queued), displaced)
		return 0
	case "sweep":
		return cmdQueueSweep(lane, opts, st, timeout, stdout, stderr, deps)
	}
	return queueRefuse(stderr, fmt.Sprintf("queue subverb %q is not one of status, hold, release, skip, unskip, front, sweep, classify, audit", sub))
}

// cmdQueueStatus is the queue's own report, and it READS NOTHING BUT THE LANE'S FILES.
//
// EDGE 7, 2026-09-18: there was no way to see the queue at all. `nova-merge status` walks
// the entries and names neither the hold nor the skip set, so a lane that was standing
// still because somebody held it looked exactly like a lane with nothing to do, and a
// pull request that was skipped looked exactly like one nobody had queued. Both facts are
// one line here now: `hold=<reason|->` and `skipped=<n>`.
//
// It takes no host, no clone and no lock: queue.json and hold are two files under --lane,
// and a report that cannot be read on a bench with no gh is a report nobody runs.
func cmdQueueStatus(lane string, st *merge.State, opts map[string]string, stdout, stderr io.Writer) int {
	max := bounded.Default
	if raw, ok := opts["max"]; ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return queueRefuse(stderr, fmt.Sprintf("--max is a ceiling on a listing: 0 means all and a negative one is a typo with two readings, got %q", raw))
		}
		max = n
	}
	q, err := merge.LoadQueue(lane, st)
	if err != nil {
		return queueRefuse(stderr, fmt.Sprintf("%s: %s; repair or remove it -- a queue this tool cannot read is an order it must not guess at", oneline.Field(filepath.Join(lane, merge.QueueName)), oneline.Err(err)))
	}
	h, held, err := merge.ReadHold(lane)
	if err != nil {
		return queueRefuse(stderr, oneline.Err(err))
	}
	reason, by := "-", "-"
	if held {
		reason, by = h.Reason, h.By
		if strings.TrimSpace(by) == "" {
			by = "-"
		}
	}
	list := bounded.Capped(stdout, max, "QUEUE", "entry",
		fmt.Sprintf("nova-merge queue --lane %s status --max 0", oneline.Field(lane)))
	for i, pr := range q.Queued {
		list.Line(fmt.Sprintf("QUEUE ENTRY pos=%d entry=%d state=queued", i+1, pr))
	}
	for _, pr := range sortedIDsOf(q.Skipped) {
		if q.IsParked(pr) {
			list.Line(fmt.Sprintf("QUEUE ENTRY pos=- entry=%d state=parked", pr))
			continue
		}
		list.Line(fmt.Sprintf("QUEUE ENTRY pos=- entry=%d state=skipped", pr))
	}
	list.More()
	fmt.Fprintf(stdout, "QUEUE STATUS lane=%s queued=%d skipped=%d parked=%d hold=%s by=%s\n",
		oneline.Field(lane), len(q.Queued), len(q.Skipped), len(q.Parked),
		oneline.Field(reason), oneline.Field(by))
	return 0
}

// sortedIDsOf is the skipped set in a deterministic order, so two runs of `queue status`
// over one file print the same lines.
func sortedIDsOf(list []int) []int {
	out := append([]int(nil), list...)
	sort.Ints(out)
	return out
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

func cmdQueueSweep(lane string, opts map[string]string, st *merge.State, timeout time.Duration, stdout, stderr io.Writer, deps Deps) int {
	rawWindow, ok := opts["window"]
	if !ok || strings.TrimSpace(rawWindow) == "" {
		return queueRefuse(stderr, "a sweep wants the session window it walks; --window <duration>  # a sweep with no window would enqueue the world")
	}
	window, err := time.ParseDuration(rawWindow)
	if err != nil || window <= 0 {
		return queueRefuse(stderr, fmt.Sprintf("--window wants a duration, got %q; --window <duration>", rawWindow))
	}
	if h, present, err := merge.ReadHold(lane); err != nil {
		return queueRefuse(stderr, oneline.Err(err))
	} else if present {
		return queueRefuse(stderr, fmt.Sprintf("a hold is standing (%s): %s; nova-merge queue release", oneline.Field(h.Reason), oneline.Field(h.By)))
	}
	host := deps.NewHost(st.Repo, timeout)
	lister, ok := host.(queueHost)
	if !ok {
		return queueRefuse(stderr, "this host cannot list open pull requests, and a sweep walks them: no host, no sweep")
	}
	prs, err := lister.QueuePRs()
	if err != nil {
		return queueRefuse(stderr, oneline.Err(err))
	}
	classifyRecs, err := merge.LoadClassifies(lane)
	if err != nil {
		return queueRefuse(stderr, oneline.Err(err))
	}
	ph, _ := host.(poisonHost)
	var scanned, green, queued, already, skipped, dirty, parked, staleRed, rerun int
	var parkLines []string
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
				parked++
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
							parked++
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
		return nil
	}); err != nil {
		return queueRefuse(stderr, oneline.Err(err))
	}
	for _, line := range parkLines {
		fmt.Fprintln(stdout, oneline.Escape(line))
	}
	fmt.Fprintf(stdout, "QUEUE SWEEP window=%s scanned=%d green=%d queued=%d already=%d skipped=%d dirty=%d parked=%d stale_red=%d rerun=%d\n",
		oneline.Field(mergeWindow(window)), scanned, green, queued, already, skipped, dirty, parked, staleRed, rerun)
	merge.Appendf(lane, deps.Now(), "QUEUE SWEEP window=%s scanned=%d queued=%d rerun=%d", mergeWindow(window), scanned, queued, rerun)
	return 0
}

func mergeWindow(d time.Duration) string { return d.String() }

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
		"verdict": true, "note": true, "pr": true, "branch": true, "test": true, "who": true}
	opts, _, err := scanQueueArgs(args, known)
	if err != nil {
		return classifyRefuse(stderr, err.Error())
	}
	lane := opts["lane"]
	if strings.TrimSpace(lane) == "" {
		return classifyRefuse(stderr, "--lane is required and is the lane's own directory; refusing to guess")
	}
	if strings.TrimSpace(opts["run"]) == "" {
		return classifyRefuse(stderr, "classify wants the run id it is about; --run <id>")
	}
	verdict := opts["verdict"]
	if !merge.ValidClass(verdict) {
		return classifyRefuse(stderr, fmt.Sprintf("--verdict is %s, %s or %s, got %q; refusing to guess",
			oneline.Field(merge.ClassFlaky), oneline.Field(merge.ClassOwnChange),
			oneline.Field(merge.ClassEnvironment), oneline.Escape(verdict)))
	}
	st, code := openLane("classify", lane, stderr)
	if st == nil {
		return code
	}
	timeout := 120 * time.Second
	if raw, ok := opts["timeout"]; ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return classifyRefuse(stderr, fmt.Sprintf("--timeout is a number of seconds, got %q", raw))
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
		return classifyRefuse(stderr, oneline.Err(err))
	}
	item, rec, err := merge.NewClassRecord(opts["run"], opts["head"], entry, verdict, opts["note"], opts["who"], sub)
	if err != nil {
		return classifyRefuse(stderr, oneline.Err(err))
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
