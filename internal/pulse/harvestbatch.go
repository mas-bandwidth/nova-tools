package pulse

// `harvest --bench <name> --batch`: harvestBench's fold with the TRANSFER batched (#2756:
// card end -> harvest start under 60 s, a verified PR under 5 min, no hand anywhere).
//
// Measured on 2026-09-22 (space, hetzner): a pass of `harvest --bench` took 10+ minutes
// because every finished job cost its own round trips -- one `git fetch` over ssh (4-15 s
// each), one GitHub fetch to pin the target, one push, two gh calls and one ssh to touch
// `.harvested` -- and a job refused for a stale base was fetched again on every pass. The
// batch pass pays for the wire ONCE per phase:
//
//  1. one ssh lists the jobs (benchListScript, unchanged);
//  2. one ssh STAGES every candidate branch on the bench: a bare stage repo per repository
//     under $HOME/nova-bench/harvest-stage/, its objects backed by the bench's mirror through
//     alternates, takes each job's branch as refs/harvest/<label> and answers the branch's
//     sha, so the pass knows every head before anything crosses the wire;
//  3. one `git fetch` per repository pulls every staged branch into the coordinator's clone;
//  4. every check is local (commit count, key shapes, destination, stale base against a
//     target pinned ONCE per pass);
//  5. one `git push --porcelain` per repository with every refspec;
//  6. pull requests are opened through REST (`gh api`), a few at a time, bounded by GitHub;
//  7. one ssh touches every `.harvested` marker.
//
// What a pass REMEMBERS lives in the fleet Redis, never in a file: a job refused for a
// stale base is recorded as `<label> <head>` in a set, and the same (job, head) is never
// fetched again; a rebase on the bench moves the head and the job is tried afresh. The
// pass ends by writing its row (at, done, prs, skipped, took_ms) to a hash the sprint table
// can read. Both keys sit under `cards:harvest:<bench>` because the bench ACL user may
// write `cards:*` and not `harvest:*` (measured 2026-09-22: NOPERM).
//
// A job is a candidate only when it is DONE: the RESULT.md's state line (the first
// non-empty line after the RESULT line) says DONE. An ABSTAIN, a RED, a BLOCKED or a
// template left in place is not a branch to publish, whatever BRANCH line it carries.
//
// Everything that touches the world is the same seam harvestBench uses: BenchShell, Forge,
// git on PATH, and HarvestMemory for the store. No test here opens a connection.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/keyshape"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// HarvestMemory is what a batch pass remembers between passes and reports after each one.
// The shipped one is the fleet Redis; a test's is a map.
type HarvestMemory interface {
	// Refused answers every `<label> <head>` this bench has refused for a stale base.
	Refused(ctx context.Context, bench string) (map[string]bool, error)
	// Remember records one (label, head) as refused for a stale base.
	Remember(ctx context.Context, bench, label, head string) error
	// Report writes the pass's row for the sprint table.
	Report(ctx context.Context, bench string, fields map[string]any) error
}

// harvestKeyPrefix is where the batch pass keeps its memory and its row. The bench ACL
// user may write cards:* (harvest:* is NOPERM, measured 2026-09-22).
const harvestKeyPrefix = "cards:harvest:"

// harvestRefusedTTL bounds the refused set: a (label, head) nobody rebased in a week is a
// dead card, not a memory worth keeping.
const harvestRefusedTTL = 7 * 24 * time.Hour

type redisHarvestMemory struct{ rdb *redis.Client }

func (m redisHarvestMemory) Refused(ctx context.Context, bench string) (map[string]bool, error) {
	members, err := m.rdb.SMembers(ctx, harvestKeyPrefix+bench+":refused").Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(members))
	for _, s := range members {
		out[s] = true
	}
	return out, nil
}

func (m redisHarvestMemory) Remember(ctx context.Context, bench, label, head string) error {
	key := harvestKeyPrefix + bench + ":refused"
	if err := m.rdb.SAdd(ctx, key, label+" "+head).Err(); err != nil {
		return err
	}
	return m.rdb.Expire(ctx, key, harvestRefusedTTL).Err()
}

func (m redisHarvestMemory) Report(ctx context.Context, bench string, fields map[string]any) error {
	return m.rdb.HSet(ctx, harvestKeyPrefix+bench, fields).Err()
}

// batchCandidate is one DONE job the pass is carrying through its phases.
type batchCandidate struct {
	job    benchJob
	label  string
	branch string
	repo   string // the RESULT.md's REPO claim, which only ever picks the clone
	base   string
	clone  string
	line1  string
	head   string // the branch's sha on the bench, from the stage
	ref    string // refs/harvest/<branch> in the clone, once fetched
	sha    string // short head, for the JOB line
	dest   destination
}

// harvestBenchBatch is `harvest --bench --batch`. Same guards and same lines as
// harvestBench; the phases above instead of a round trip per job.
func harvestBenchBatch(in HarvestInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	started := in.Now()
	if strings.TrimSpace(in.Root) == "" {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("missing --root; refusing to guess (name the swarm root ON the bench, comma separated for more than one)"))
	}
	if code := requireBench(in.Stderr, in.Machines, in.Bench, "HARVEST"); code != 0 {
		return code
	}
	shell := in.Shell
	if shell == nil {
		shell = sshShell{Program: in.SSH}
	}
	forge := in.Forge
	if forge == nil {
		forge = restForge{}
	}
	prefix := in.BranchPrefix
	if prefix == "" {
		prefix = DefaultBranchPrefix
	}
	if !strings.HasPrefix(prefix, DefaultBranchPrefix) {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("--branch-prefix %s is not under %s; this verb harvests this line's own branches and the prefix may be narrowed, never widened",
			field(prefix), field(DefaultBranchPrefix)))
	}
	fallbackBase := in.Base
	if fallbackBase == "" {
		fallbackBase = DefaultBase
	}
	ctx := context.Background()
	memory := in.Memory
	if memory == nil && strings.TrimSpace(in.Store.Addr) != "" {
		rdb, err := DialStore(ctx, in.Store)
		if err != nil {
			fmt.Fprintf(in.Stderr, "HARVEST NOTE store: %s (this pass remembers no refusal and writes no row)\n", oneline.Err(err))
		} else {
			defer rdb.Close()
			memory = redisHarvestMemory{rdb: rdb}
		}
	}
	refused := map[string]bool{}
	if memory != nil {
		r, err := memory.Refused(ctx, in.Bench)
		if err != nil {
			fmt.Fprintf(in.Stderr, "HARVEST NOTE store: reading the refused set: %s (this pass fetches every candidate)\n", oneline.Err(err))
		} else {
			refused = r
		}
	}

	// Every phase is timed and said on one PHASES line: a pass over the target is a
	// measurement, never a guess about which phase ate it.
	phases := map[string]time.Duration{}
	phase := func(name string, since time.Time) { phases[name] += in.Now().Sub(since) }
	// Phase 1: the listing. One ssh.
	t := in.Now()
	roots := splitList(in.Root)
	raw, err := shell.Run(in.Bench, benchListScript(roots))
	if err != nil {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("listing jobs on %s: %s", field(in.Bench), oneline.Err(err)))
	}
	jobs, missing, incomplete := parseBenchJobs(raw)
	phase("list", t)
	if len(missing) > 0 {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("--root %s does not exist on %s (name the swarm root ON the bench; this verb does not expand a leading ~ -- pass an absolute path)",
			field(strings.Join(missing, ",")), field(in.Bench)))
	}
	lines := bound(in.Stdout, in.Max)
	facts := drainFacts{
		state:    map[string]string{},
		complete: len(incomplete) == 0,
		probe: func(labels []string) map[string]string {
			out, err := shell.Run(in.Bench, benchProbeScript(roots, labels))
			if err != nil {
				fmt.Fprintf(in.Stderr, "HARVEST NOTE probe on %s: %s\n", field(in.Bench), oneline.Err(err))
				return map[string]string{}
			}
			return parseProbes(out)
		},
	}
	for _, r := range incomplete {
		lines.Line(fmt.Sprintf("HARVEST ROOT-INCOMPLETE bench=%s root=%s (a directory under it could not be read or entered; this run infers no absence and drains no card as job-dir-gone)",
			field(in.Bench), field(r)))
	}
	state := facts.state
	var done, fetched, pushed, prs, noCommit, skipped, failed int
	skippedBy := map[string]int{}
	skip := func(label, reason string) {
		skipped++
		skippedBy[reason]++
	}

	// Selection: every DONE, unharvested job on our prefix, its clone known.
	var cands []*batchCandidate
	for _, j := range jobs {
		label := filepath.Base(j.Dir)
		if len(j.Result) == 0 {
			state[label] = jobRunning
			continue
		}
		state[label] = jobHeld
		done++
		if j.Harvested {
			skipped++
			skippedBy["harvested"]++
			state[label] = jobDone
			continue
		}
		if st := resultState(j.Result); st != typedrec.StatusDone {
			skip(label, "state")
			continue
		}
		branch := resultField(j.Result, "BRANCH")
		repo := strings.TrimPrefix(resultField(j.Result, "REPO"), "github.com/")
		base := resultField(j.Result, "BASE")
		if base == "" {
			base = fallbackBase
		}
		if want := strings.TrimSpace(in.Session); want != "" {
			if got := resultField(j.Result, "SESSION"); got != want {
				skip(label, "session")
				continue
			}
		}
		if branch == "" || !strings.HasPrefix(branch, prefix) {
			skip(label, "branch-prefix")
			continue
		}
		if in.Since > 0 && j.Mtime > 0 && in.Now().Sub(time.Unix(j.Mtime, 0)) > in.Since {
			skip(label, "age")
			continue
		}
		if repo == "" {
			skip(label, "no-repo")
			continue
		}
		clone := cloneFor(in.Clones, repo)
		if clone == "" {
			skip(label, "no-clone")
			continue
		}
		cands = append(cands, &batchCandidate{job: j, label: label, branch: branch, repo: repo, base: base, clone: clone, line1: firstNonEmpty(j.Result)})
	}

	// Phase 2: stage every candidate on the bench. One ssh, whatever the count.
	stages := map[string]string{} // repo name -> stage path on the bench
	staged := map[string]string{} // label -> head sha
	stageFail := map[string]string{}
	t = in.Now()
	if len(cands) > 0 {
		out, err := shell.Run(in.Bench, benchStageScript(cands))
		if err != nil {
			failed++
			lines.Line(fmt.Sprintf("HARVEST STAGE-FAIL bench=%s candidates=%d: %s (nothing was fetched this pass)", field(in.Bench), len(cands), oneline.Err(err)))
			cands = nil
		} else {
			stages, staged, stageFail = parseStaged(out)
		}
	}
	phase("stage", t)
	t = in.Now()

	// Phase 3: one fetch per repository into the coordinator's clone. The refspecs name
	// each staged label and land it as refs/harvest/<branch>, exactly where harvestBench
	// lands a job's branch, so every check below reads what harvestBench would have read.
	byClone := map[string][]*batchCandidate{}
	seenBranch := map[string]string{}
	var live []*batchCandidate
	for _, c := range cands {
		head, ok := staged[c.label]
		if !ok {
			failed++
			lines.Line(fmt.Sprintf("HARVEST FETCH-FAIL bench=%s label=%s branch=%s: %s",
				field(in.Bench), field(c.label), field(c.branch), oneline.Cap(orString(stageFail[c.label], "the stage did not answer for this job"), 200)))
			continue
		}
		c.head = head
		if refused[c.label+" "+head] {
			skip(c.label, "refused-stale-base")
			continue
		}
		if other, dup := seenBranch[c.branch]; dup {
			failed++
			lines.Line(fmt.Sprintf("HARVEST FETCH-FAIL bench=%s label=%s branch=%s: the same branch is named by job %s in this pass; one branch, one job",
				field(in.Bench), field(c.label), field(c.branch), field(other)))
			continue
		}
		seenBranch[c.branch] = c.label
		c.ref = "refs/harvest/" + c.branch
		byClone[c.clone] = append(byClone[c.clone], c)
	}
	for _, clone := range harvestKeys(byClone) {
		group := byClone[clone]
		name := repoName(group[0].repo)
		stage, ok := stages[name]
		if !ok {
			for _, c := range group {
				failed++
				lines.Line(fmt.Sprintf("HARVEST FETCH-FAIL bench=%s label=%s branch=%s: no stage for repository %s on the bench",
					field(in.Bench), field(c.label), field(c.branch), field(name)))
			}
			continue
		}
		args := []string{"fetch", "--no-tags", benchURL(in.Bench, stage)}
		for _, c := range group {
			args = append(args, "+refs/harvest/"+c.label+":"+c.ref)
		}
		// One fetch carries the whole group, so one broken pipe would fail every job in
		// it: an `early EOF` under load (hetzner, 2026-09-22, 23 jobs at once) is retried
		// once after a pause before the group is called failed.
		out, err := gitInBounded(clone, groupTimeout(len(group)), args...)
		if err != nil {
			time.Sleep(fetchRetryPause)
			out, err = gitInBounded(clone, groupTimeout(len(group)), args...)
		}
		if err != nil {
			for _, c := range group {
				failed++
				lines.Line(fmt.Sprintf("HARVEST FETCH-FAIL bench=%s label=%s branch=%s: %s",
					field(in.Bench), field(c.label), field(c.branch), oneline.Cap(out, 200)))
			}
			continue
		}
		fetched += len(group)
		live = append(live, group...)
	}
	phase("fetch", t)
	t = in.Now()

	// Phase 4: every check, local. The destination is resolved ONCE per clone and each
	// target pinned ONCE per pass, before the loop; then every job's own git reads (its
	// commit count, its diff for the key-shape scan, its head, its stale-base walk) run a
	// few at a time -- they are independent processes, and on a loaded coordinator (load
	// 40-100 measured 2026-09-22) one at a time cost 7 s per job -- and their verdicts are
	// applied in listing order, so the lines read the same as a serial pass.
	launchedDirs := splitList(in.Launched)
	index := launchedIndex{}
	dests := map[string]destResolved{}
	pins := map[string]pinned{}
	for _, c := range live {
		d, ok := dests[c.clone]
		if !ok {
			d.dispatch, d.err = dispatchFromCoordinatorClone(in.Clones, c.clone)
			dests[c.clone] = d
		}
		if d.err != nil {
			continue
		}
		dest, err := resolveDestination(c.label, d.dispatch, "", c.repo)
		if err != nil {
			continue
		}
		target := harvestTargetName(c.base)
		if target == "" {
			continue
		}
		key := dest.url + " " + target
		if _, ok := pins[key]; !ok {
			var p pinned
			p.oid, p.err = pinAuthorizedTarget(c.clone, dest.url, target)
			pins[key] = p
		}
	}
	// check runs one job's local reads: the same checks harvestBench makes, in the same
	// order, against the fetched ref. It is a closure of this function on purpose: the
	// key-shape scan is the one guard every path to a forge passes, and the class test
	// holds that guard in the publishing function itself. It writes nothing shared: the
	// destination and the pin were resolved before it ran, and its verdict is applied by
	// the loop below, in listing order.
	check := func(c *batchCandidate, d destResolved, cardPath string) checkVerdict {
		var v checkVerdict
		rng := "origin/" + c.base + ".." + c.ref
		v.count, v.countErr = commitCount(c.clone, rng)
		if v.countErr != nil || v.count == 0 {
			return v
		}
		v.findings, v.scanErr = secretFindings(c.job.Dir, c.clone, rng, c.job.Result)
		if v.scanErr != nil || len(v.findings) > 0 {
			return v
		}
		c.sha, _ = gitIn(c.clone, "rev-parse", "--short", c.ref)
		if v.prefixErr = mustBranchPrefix(c.branch); v.prefixErr != nil {
			return v
		}
		if v.dispatchErr = d.err; v.dispatchErr != nil {
			return v
		}
		dest, err := resolveDestination(c.label, d.dispatch, "", c.repo)
		if v.destErr = err; v.destErr != nil {
			return v
		}
		c.dest = dest
		globs, declared := harvestDeclaredPaths(cardPath, c.job.Result)
		target := harvestTargetName(c.base)
		if target == "" {
			v.stale = fmt.Errorf("stale-base: no explicit target branch")
			return v
		}
		p, ok := pins[dest.url+" "+target]
		switch {
		case !ok:
			v.stale = fmt.Errorf("stale-base MISSING target=%s: no pin was taken for this target", field(target))
		case p.err != nil:
			v.stale = fmt.Errorf("stale-base MISSING target=%s: the authorized destination's target could not be fetched or pinned, so no diff was walked: %s",
				field(target), oneline.Err(p.err))
		default:
			v.stale = staleBaseVerdict(c.clone, p.oid, c.ref, globs, declared)
		}
		return v
	}
	verdicts := make([]checkVerdict, len(live))
	cards := make([]string, len(live))
	for i, c := range live {
		cards[i] = index.lookup(launchedDirs, c.label)
	}
	var cwg sync.WaitGroup
	csem := make(chan struct{}, checkWorkers)
	for i, c := range live {
		i, c := i, c
		cwg.Add(1)
		csem <- struct{}{}
		go func() {
			defer cwg.Done()
			defer func() { <-csem }()
			verdicts[i] = check(c, dests[c.clone], cards[i])
		}()
	}
	cwg.Wait()
	var marks []string
	var ready []*batchCandidate
	for i, c := range live {
		v := verdicts[i]
		j, label, branch, base := c.job, c.label, c.branch, c.base
		switch {
		case v.countErr != nil:
			skip(label, "no-count")
			lines.Line(fmt.Sprintf("HARVEST SKIP bench=%s label=%s reason=no-count branch=%s base=%s: %s",
				field(in.Bench), field(label), field(branch), field(base), oneline.Err(v.countErr)))
		case v.count == 0:
			noCommit++
			marks = append(marks, j.Dir)
			state[label] = jobDone
			lines.Line(fmt.Sprintf("HARVEST NO-COMMIT bench=%s label=%s branch=%s base=%s (nothing was committed; not pushed)",
				field(in.Bench), field(label), field(branch), field(base)))
		case v.scanErr != nil:
			failed++
			fmt.Fprintln(in.Stderr, secretScanRefusalLine("harvest-bench", label, v.scanErr))
		case len(v.findings) > 0:
			failed++
			secretRefusal{Site: "harvest-bench", Label: label, JobDir: j.Dir,
				Out: in.Stderr, Move: benchMover(func(script string) (string, error) { return shell.Run(in.Bench, script) })}.refuse(v.findings)
		case v.prefixErr != nil:
			failed++
			lines.Line(fmt.Sprintf("HARVEST PUSH-REFUSED bench=%s label=%s branch=%s: %s",
				field(in.Bench), field(label), field(branch), oneline.Err(v.prefixErr)))
		case v.dispatchErr != nil:
			failed++
			lines.Line(fmt.Sprintf("HARVEST REFUSED repo-unknown card=%s: %s", field(label), oneline.Err(v.dispatchErr)))
		case v.destErr != nil:
			failed++
			lines.Line(v.destErr.Error())
		case v.stale != nil:
			failed++
			lines.Line(fmt.Sprintf("HARVEST REFUSED stale-base bench=%s label=%s: %s",
				field(in.Bench), field(label), oneline.Err(v.stale)))
			if memory != nil && !strings.Contains(v.stale.Error(), "MISSING") {
				if err := memory.Remember(ctx, in.Bench, label, c.head); err != nil {
					fmt.Fprintf(in.Stderr, "HARVEST NOTE store: remembering %s: %s\n", field(label), oneline.Err(err))
				}
			}
		default:
			ready = append(ready, c)
		}
	}
	phase("check", t)
	t = in.Now()

	// Phase 5: one push per repository, every refspec at once. --porcelain names each
	// ref's fate, so one rejected branch never hides the others (and never stops them:
	// this is deliberately not --atomic).
	byClone = map[string][]*batchCandidate{}
	for _, c := range ready {
		byClone[c.clone] = append(byClone[c.clone], c)
	}
	var toOpen []*batchCandidate
	for _, clone := range harvestKeys(byClone) {
		group := byClone[clone]
		args := []string{"push", "--porcelain", "origin"}
		for _, c := range group {
			args = append(args, c.ref+":refs/heads/"+c.branch)
		}
		out, err := gitInBounded(clone, groupTimeout(len(group)), args...)
		fate := parsePorcelainPush(out)
		for _, c := range group {
			f, seen := fate["refs/heads/"+c.branch]
			switch {
			case seen && f.ok:
				pushed++
				toOpen = append(toOpen, c)
			case seen:
				failed++
				lines.Line(fmt.Sprintf("HARVEST PUSH-FAIL bench=%s label=%s branch=%s: %s",
					field(in.Bench), field(c.label), field(c.branch), oneline.Cap(f.summary, 200)))
			default:
				failed++
				msg := out
				if err != nil {
					msg = oneline.Err(err) + ": " + out
				}
				lines.Line(fmt.Sprintf("HARVEST PUSH-FAIL bench=%s label=%s branch=%s: %s",
					field(in.Bench), field(c.label), field(c.branch), oneline.Cap(msg, 200)))
			}
		}
	}

	phase("push", t)
	t = in.Now()
	// Phase 6: pull requests, one at a time -- GitHub asks that content creation never
	// run concurrently, and four parallel `gh` calls answered `exit status 1` for 7 of 50
	// on 2026-09-22. CREATE FIRST: a job just harvested has no pull request yet, so the
	// common case is one request per job; only GitHub's "already exists" answer (a job
	// harvested before, un-marked) costs the lookup. The old order paid the lookup for
	// every job and then the create.
	for _, c := range toOpen {
		pr, err := forge.CreatePR(c.dest.repo, c.base, c.branch, prTitle(c.line1, c.label, in.Bench), benchPRBody(in.Bench, c.job, in.MaxBodyBytes))
		if prExists(err) {
			found, ferr := forge.FindPR(c.dest.repo, c.branch)
			pr, err = findExistingPR(err, found, ferr)
		}
		if err != nil {
			failed++
			lines.Line(fmt.Sprintf("HARVEST PR-FAIL bench=%s label=%s branch=%s: %s",
				field(in.Bench), field(c.label), field(c.branch), oneline.Err(err)))
			continue
		}
		prs++
		marks = append(marks, c.job.Dir)
		state[c.label] = jobDone
		lines.Line(fmt.Sprintf("HARVEST JOB bench=%s label=%s branch=%s sha=%s base=%s pr=%s#%d",
			field(in.Bench), field(c.label), field(c.branch), field(c.sha), field(c.base), field(c.dest.repo), pr))
	}
	phase("pr", t)
	t = in.Now()

	// Phase 7: one ssh leaves every `.harvested` marker. A touch that fails is said, and
	// the next pass finds the PR through the forge instead of opening it twice.
	if len(marks) > 0 {
		sort.Strings(marks)
		if _, err := shell.Run(in.Bench, benchMarkScript(marks)); err != nil {
			fmt.Fprintf(in.Stderr, "HARVEST NOTE marking %d harvested on %s: %s\n", len(marks), field(in.Bench), oneline.Err(err))
		}
	}

	phase("mark", t)
	t = in.Now()
	drainIn := in
	if dirs := splitList(in.Launched); len(dirs) > 0 {
		drainIn.Launched = dirs[0]
	}
	drained, left, drainFailed := drainLaunched(drainIn, facts, lines)
	failed += drainFailed
	phase("drain", t)
	var ph []string
	for _, name := range []string{"list", "stage", "fetch", "check", "push", "pr", "mark", "drain"} {
		ph = append(ph, fmt.Sprintf("%s=%dms", name, phases[name].Milliseconds()))
	}
	lines.Line(fmt.Sprintf("HARVEST PHASES bench=%s candidates=%d %s", field(in.Bench), len(cands), strings.Join(ph, " ")))
	for _, reason := range sortedCounts(skippedBy) {
		lines.Line(fmt.Sprintf("HARVEST SKIPPED bench=%s reason=%s count=%d", field(in.Bench), reason, skippedBy[reason]))
	}
	lines.More()

	took := in.Now().Sub(started)
	fmt.Fprintf(in.Stdout, "HARVEST %s jobs=%d done=%d fetched=%d pushed=%d prs=%d skipped=%d took=%dms\n",
		field(in.Bench), len(jobs), done, fetched, pushed, prs, skipped, took.Milliseconds())
	fmt.Fprintf(in.Stdout, "HARVEST BENCH %s bench=%s jobs=%d done=%d pushed=%d prs=%d no-commit=%d skipped=%d drained=%d left=%d took=%s\n",
		okOrRed(failed), field(in.Bench), len(jobs), done, pushed, prs, noCommit, skipped, drained, left, took.Round(time.Millisecond))
	if memory != nil {
		row := map[string]any{
			"at": in.Now().UTC().Format(time.RFC3339), "jobs": len(jobs), "done": done, "fetched": fetched,
			"pushed": pushed, "prs": prs, "skipped": skipped, "failed": failed, "took_ms": took.Milliseconds(),
		}
		if err := memory.Report(ctx, in.Bench, row); err != nil {
			fmt.Fprintf(in.Stderr, "HARVEST NOTE store: writing the row: %s\n", oneline.Err(err))
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// fetchRetryPause is the pause before a group fetch's one retry; a test sets it to zero.
var fetchRetryPause = 5 * time.Second

// groupTimeout bounds one transfer that carries a whole group: childTimeout is a
// per-job bound, and a fetch of 23 branches from hetzner measured 51 s on a coordinator
// at load 55 (2026-09-22), so a group gets the per-job bound plus a share per branch.
func groupTimeout(n int) time.Duration {
	return childTimeout + time.Duration(n)*10*time.Second
}

// gitInBounded is gitIn with the caller's own bound.
func gitInBounded(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// prExists says whether a create was refused because the branch's pull request is
// already open (GitHub's 422 "A pull request already exists"), the one refusal a lookup
// answers; every other refusal is the caller's to report.
func prExists(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "already exists")
}

// findExistingPR turns the lookup's answer into the pull request the create said exists,
// and names both calls when the lookup cannot produce it.
func findExistingPR(created error, pr int, found error) (int, error) {
	if found != nil {
		return 0, fmt.Errorf("%s; then finding it: %s", oneline.Err(created), oneline.Err(found))
	}
	if pr == 0 {
		return 0, fmt.Errorf("%s, but no open pull request was found", oneline.Err(created))
	}
	return pr, nil
}

type pinned struct {
	oid string
	err error
}

// destResolved is one clone's dispatch, resolved once per pass.
type destResolved struct {
	dispatch dispatch
	err      error
}

// checkWorkers is how many jobs' local git reads run at once in the check phase.
const checkWorkers = 8

// checkVerdict is everything the check phase learned about one job, applied in order
// after every job's reads are in. Exactly one of its failure fields is set, in the order
// harvestBench tests them; none set means the job is ready to push.
type checkVerdict struct {
	count       int
	countErr    error
	scanErr     error
	findings    []keyshape.Finding
	prefixErr   error
	dispatchErr error
	destErr     error
	stale       error
}

// launchedIndex answers a job's launched card by DIRECT PATH: `fill` names the card
// `<label>.md` and writes `label=<basename without .md>` into its marker (writeLaunchedMarker),
// so the two spellings launchedCardFor matched are the same file, and the walk it made over
// the whole directory (16,850 cards on 2026-09-22, a marker read per card, for EVERY job)
// answered nothing a stat does not. Every launched directory named (comma separated) is
// tried in order; the first card found wins, as the walk's lexical order did.
type launchedIndex map[string]string

func (idx launchedIndex) lookup(dirs []string, label string) string {
	if strings.TrimSpace(label) == "" {
		return ""
	}
	if c, ok := idx[label]; ok {
		return c
	}
	found := ""
	for _, d := range dirs {
		if c := filepath.Join(d, label+".md"); exists(c) {
			found = c
			break
		}
	}
	idx[label] = found
	return found
}

// resultState is the RESULT.md's state: the first word of the first non-empty line after
// the RESULT line, trailing punctuation dropped. `DONE`, `DONE (green tests)` and `DONE:`
// are all DONE; `ABSTAIN not-per-leg ...`, `RED`, `BLOCKED <why>` and a template's
// `DONE <- or: ABSTAIN <why>` are not. The whole state line is read, not only its first
// word: the card's own template line (docs/SPEC-PULSE.md, the RESULT grammar: line 2 is
// one of `DONE`, `ABSTAIN <why>`, `BLOCKED <why>`) also starts with DONE, so a line that
// still carries the template's `<-` arrow or its `<why>` placeholder is TEMPLATE, the
// unedited card, and is never harvested (Stella's HOLD of #2926 at 2b48d622).
func resultState(lines []string) string {
	return typedrec.ResultState(lines)
}

// repoName is the repository's own name: the part after the owner.
func repoName(repo string) string {
	if _, name, ok := strings.Cut(repo, "/"); ok {
		return name
	}
	return repo
}

// benchStageScript is the one script that stages every candidate branch on the bench. A
// bare repository per repository name under $HOME/nova-bench/harvest-stage, its objects
// backed by the bench mirror through alternates when the mirror is there (so a job's
// branch costs its own new commits and nothing else); gc.auto off; refs/harvest/* dropped
// at the start of each pass so the stage carries only this pass's candidates. Each job's
// branch is taken as refs/harvest/<label> and answered with its sha:
//
//	STAGE <name> <path>          -- the stage for repository <name> is at <path>
//	STAGED <label> <sha>         -- the branch is staged, at this head
//	STAGE-FAIL <label> <reason>  -- it is not (no such branch, no repo, a broken clone)
func benchStageScript(cands []*batchCandidate) string {
	var b strings.Builder
	b.WriteString(`SG="$HOME/nova-bench/harvest-stage"; mkdir -p "$SG" || exit 1; ` +
		`stage(){ st="$SG/$1.git"; if [ ! -d "$st" ]; then git init -q --bare "$st" || return 1; git -C "$st" config gc.auto 0; ` +
		`m="$HOME/nova-bench/mirror/$1.git"; if [ -d "$m/objects" ]; then printf '%s\n' "$m/objects" > "$st/objects/info/alternates"; fi; fi; ` +
		`git -C "$st" for-each-ref --format='delete %(refname)' refs/harvest | git -C "$st" update-ref --stdin >/dev/null 2>&1; ` +
		`printf 'STAGE\t%s\t%s\n' "$1" "$st"; }; ` +
		`take(){ st="$SG/$1.git"; if out=$(git -C "$st" fetch -q --no-tags "$2/repo" "+refs/heads/$3:refs/harvest/$4" 2>&1); then ` +
		`printf 'STAGED\t%s\t%s\n' "$4" "$(git -C "$st" rev-parse "refs/harvest/$4")"; else ` +
		`printf 'STAGE-FAIL\t%s\t%s\n' "$4" "$(printf '%s' "$out" | tr '\n\t' '  ' | cut -c1-200)"; fi; }; `)
	names := map[string]bool{}
	for _, c := range cands {
		if n := repoName(c.repo); !names[n] {
			names[n] = true
			fmt.Fprintf(&b, "stage %s; ", shellQuote(n))
		}
	}
	for _, c := range cands {
		fmt.Fprintf(&b, "take %s %s %s %s; ", shellQuote(repoName(c.repo)), shellQuote(c.job.Dir), shellQuote(c.branch), shellQuote(c.label))
	}
	return b.String()
}

// parseStaged reads the stage script's answer back.
func parseStaged(out string) (stages, staged, failed map[string]string) {
	stages, staged, failed = map[string]string{}, map[string]string{}, map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 3 {
			continue
		}
		k, v := strings.TrimSpace(f[1]), strings.TrimSpace(f[2])
		switch f[0] {
		case "STAGE":
			stages[k] = v
		case "STAGED":
			if isHex(v) {
				staged[k] = v
			} else {
				failed[k] = "the stage answered no sha: " + v
			}
		case "STAGE-FAIL":
			failed[k] = v
		}
	}
	return stages, staged, failed
}

// benchMarkScript touches every harvested job's marker in one call.
func benchMarkScript(dirs []string) string {
	var b strings.Builder
	b.WriteString("touch")
	for _, d := range dirs {
		b.WriteString(" " + shellQuote(d+"/.harvested"))
	}
	return b.String()
}

// benchURL is a path on the bench as a git ssh URL, the same shape benchRepoURL builds
// for a job's clone.
func benchURL(bench, path string) string {
	if strings.HasPrefix(path, "/") {
		return "ssh://" + bench + path
	}
	return "ssh://" + bench + "/~/" + path
}

// pushFate is one ref's line of `git push --porcelain`.
type pushFate struct {
	ok      bool
	summary string
}

// parsePorcelainPush reads `git push --porcelain`: one line per ref, `<flag>TAB<from>:<to>TAB<summary>`,
// where the flag is a space (fast-forward), `+` (forced), `-` (deleted), `*` (new), `=` (up
// to date) or `!` (rejected). Keyed by the destination ref.
func parsePorcelainPush(out string) map[string]pushFate {
	fate := map[string]pushFate{}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 || line == "" {
			continue
		}
		flag := line[0]
		_, to, ok := strings.Cut(strings.TrimSpace(f[1]), ":")
		if !ok {
			continue
		}
		summary := ""
		if len(f) > 2 {
			summary = strings.TrimSpace(f[2])
		}
		switch flag {
		case ' ', '+', '-', '*', '=':
			fate[to] = pushFate{ok: true, summary: summary}
		case '!':
			fate[to] = pushFate{ok: false, summary: orString(summary, "rejected")}
		}
	}
	return fate
}

func orString(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func harvestKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedCounts(m map[string]int) []string { return harvestKeys(m) }

// restForge opens pull requests through GitHub's REST API with `gh api`: one request to
// find, one to create, and none of the repository lookups `gh pr create` adds on top.
type restForge struct{}

func (restForge) FindPR(repo, branch string) (int, error) {
	owner, _, _ := strings.Cut(repo, "/")
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	path := "repos/" + repo + "/pulls?state=open&per_page=1&head=" + url.QueryEscape(owner+":"+branch)
	cmd := exec.CommandContext(ctx, "gh", "api", path, "--jq", ".[0].number")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("gh api %s: %s: %s", path, oneline.Err(err), oneline.Cap(string(out), 200))
	}
	s := strings.TrimSpace(string(out))
	if s == "" || s == "null" {
		return 0, nil
	}
	n, convErr := strconv.Atoi(s)
	if convErr != nil {
		return 0, fmt.Errorf("gh api pulls answered %q", oneline.Cap(s, 120))
	}
	return n, nil
}

func (restForge) CreatePR(repo, base, branch, title, body string) (int, error) {
	if err := mustBranchPrefix(branch); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	payload, err := json.Marshal(map[string]string{"title": title, "head": branch, "base": base, "body": body})
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, "gh", "api", "-X", "POST", "repos/"+repo+"/pulls", "--input", "-", "--jq", ".number")
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("%s: %s", oneline.Err(err), oneline.Cap(string(out), 200))
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil {
		return 0, fmt.Errorf("gh api pulls answered %q", oneline.Cap(string(out), 120))
	}
	return n, nil
}
