package pulse

// `harvest --bench <name>`: the verb that replaces ~/rowan-working/bin/harvest-bench.sh.
//
// Six edges the schema dogfood loop found on 2026-09-18, three cards on hulk:
//
//   - harvest read `<root>/<slot>/jobs/<label>/RESULT.md` locally and had no --bench, so a
//     card that finished green on a bench, with a committed branch, was reported as a
//     retry. --bench lists the bench's jobs over the shell seam and reads each RESULT.md
//     from there.
//   - the script had no session filter and took every `rowan/*` job under six hours,
//     whoever cut it. --session and --branch-prefix are the filter, and a job passed over
//     is named with its reason.
//   - the script's no-commit guard was hardcoded to `origin/dev`, and its PR base to `main`
//     for schema. Both are the card's base: the RESULT.md's `BASE` line, else --base.
//   - the script cut PR titles at 110 characters mid-word and then appended the
//     `(label, bench)` suffix, so the title lost a word and ran past the cap.
//   - nothing drained `--launched` except `manager`, so a lane taken by a card that had
//     long since finished stayed occupied forever.
//
// The branch is pushed from HERE, not from the bench: the job's clone is fetched over
// `ssh://<bench><job>/repo` into `refs/harvest/<branch>` in a local clone and pushed from
// there, which is what harvest-bench.sh does and why -- the benches hold no GitHub push
// credential, and the rule that secrets are never copied between machines is why they never
// will (memory: "Secrets never copied between machines").
//
// Everything that touches the world is a seam: BenchShell instead of ssh, Forge instead of
// gh, and git through the PATH the tests put a fake on. No test here opens a connection.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// requireBench holds a bench NAME against the machines registry before the verb opens an
// ssh to it. Glenn's lock of 2026-09-18: runner hosts are CI-only, and a harvest's ssh --
// the job listing, the `.harvested` touch, the fetch over ssh://<bench> -- is exactly the
// reach a runner host may not take. The name is resolved ONCE, here, at the verb's edge;
// everything below this line is reached only through it.
//
// An unnamed registry is the one narrowing, the same one the fleet verbs carry: the verb is
// driven in tests, and by hand against a bench not in the registry yet, without one.
// cmd/nova-pulse names the registry on every real invocation.
func requireBench(stderr io.Writer, machines, bench, verb string) int {
	if strings.TrimSpace(machines) == "" {
		return 0
	}
	reg, err := fleet.ReadRegistry(machines)
	if err != nil {
		return refusal(stderr, verb, err)
	}
	err = reg.RequireBench(bench)
	var r *fleet.Refusal
	switch {
	case errors.As(err, &r):
		fmt.Fprintln(stderr, r.Line(verb))
		return 2
	case err != nil:
		return refusal(stderr, verb, err)
	}
	return 0
}

// DefaultBranchPrefix is the branch prefix a bench harvest takes when the caller names
// none: this line's own branches and nobody else's.
const DefaultBranchPrefix = "rowan/"

// DefaultBase is the base a card that names none is measured and opened against.
const DefaultBase = "dev"

// prTitleMax is the whole PR title's cap, suffix included. The script capped the RESULT
// line at 110 and then appended ` (<label>, <bench>)` on top of it.
const prTitleMax = 110

// BenchShell runs one command on a bench and returns its stdout. The shipped one is ssh; a
// test's is a fake, so no test of this package opens a connection.
type BenchShell interface {
	Run(bench, script string) (string, error)
}

// Forge opens pull requests. The shipped one runs gh; a test's records what it was asked
// for. FindPR answers 0 when the branch has no open PR.
type Forge interface {
	FindPR(repo, branch string) (int, error)
	CreatePR(repo, base, branch, title, body string) (int, error)
}

// benchJob is one job directory on the bench: where it is, when its RESULT.md was written,
// whether a previous harvest already took it, and the RESULT.md body. A job with no result
// lines is still running.
type benchJob struct {
	Dir       string
	Mtime     int64
	Harvested bool
	Result    []string
}

// harvestBench is `harvest --bench`: list the bench's jobs once, fold every finished job
// this session cut, then release the lane of every launched card whose job is done.
func harvestBench(in HarvestInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	started := in.Now()
	if strings.TrimSpace(in.Root) == "" {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("missing --root; refusing to guess (name the swarm root ON the bench, comma separated for more than one)"))
	}
	// The one place this verb's bench name is resolved. Everything below reaches the
	// machine only through what this guard let past.
	if code := requireBench(in.Stderr, in.Machines, in.Bench, "HARVEST"); code != 0 {
		return code
	}
	shell := in.Shell
	if shell == nil {
		shell = sshShell{Program: in.SSH}
	}
	forge := in.Forge
	if forge == nil {
		forge = ghForge{}
	}
	prefix := in.BranchPrefix
	if prefix == "" {
		prefix = DefaultBranchPrefix
	}
	// --branch-prefix may NARROW the selection and never escape it: a caller may harvest
	// only `rowan/spec/`, and no caller may harvest someone else's branches. Without this
	// the flag was the configuration escape Stella's ruling on #1824 says there is not.
	if !strings.HasPrefix(prefix, DefaultBranchPrefix) {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("--branch-prefix %s is not under %s; this verb harvests this line's own branches and the prefix may be narrowed, never widened",
			field(prefix), field(DefaultBranchPrefix)))
	}
	fallbackBase := in.Base
	if fallbackBase == "" {
		fallbackBase = DefaultBase
	}

	raw, err := shell.Run(in.Bench, benchListScript(splitList(in.Root)))
	if err != nil {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("listing jobs on %s: %s", field(in.Bench), oneline.Err(err)))
	}
	jobs := parseBenchJobs(raw)

	lines := bound(in.Stdout, in.Max)
	state := map[string]string{} // label -> done|running, for the drain
	var done, pushed, prs, noCommit, skipped, failed int

	for _, j := range jobs {
		label := filepath.Base(j.Dir)
		if len(j.Result) == 0 {
			state[label] = "running"
			continue
		}
		state[label] = "done"
		done++
		if j.Harvested {
			skipped++
			continue
		}
		skip := func(reason, detail string) {
			skipped++
			lines.Line(fmt.Sprintf("HARVEST SKIP bench=%s label=%s reason=%s %s",
				field(in.Bench), field(label), reason, detail))
		}
		line1 := firstNonEmpty(j.Result)
		branch := resultField(j.Result, "BRANCH")
		repo := strings.TrimPrefix(resultField(j.Result, "REPO"), "github.com/")
		base := resultField(j.Result, "BASE")
		if base == "" {
			base = fallbackBase
		}
		if want := strings.TrimSpace(in.Session); want != "" {
			if got := resultField(j.Result, "SESSION"); got != want {
				skip("session", fmt.Sprintf("session=%s want=%s", field(got), field(want)))
				continue
			}
		}
		if branch == "" || !strings.HasPrefix(branch, prefix) {
			skip("branch-prefix", fmt.Sprintf("branch=%s want=%s*", field(branch), field(prefix)))
			continue
		}
		if in.Since > 0 && j.Mtime > 0 && in.Now().Sub(time.Unix(j.Mtime, 0)) > in.Since {
			skip("age", fmt.Sprintf("branch=%s older=%s", field(branch), in.Since))
			continue
		}
		if repo == "" {
			skip("no-repo", fmt.Sprintf("branch=%s (the RESULT.md names no REPO line)", field(branch)))
			continue
		}
		clone := cloneFor(in.Clones, repo)
		if clone == "" {
			skip("no-clone", fmt.Sprintf("repo=%s (pass --clone %s=<dir>)", field(repo), field(repo)))
			continue
		}

		url := benchRepoURL(in.Bench, j.Dir)
		ref := "refs/harvest/" + branch
		if out, err := gitIn(clone, "fetch", url, "+"+branch+":"+ref); err != nil {
			failed++
			lines.Line(fmt.Sprintf("HARVEST FETCH-FAIL bench=%s label=%s branch=%s: %s",
				field(in.Bench), field(label), field(branch), oneline.Cap(out, 200)))
			continue
		}
		count, err := commitCount(clone, "origin/"+base+".."+ref)
		if err != nil {
			skip("no-count", fmt.Sprintf("branch=%s base=%s: %s", field(branch), field(base), oneline.Err(err)))
			continue
		}
		if count == 0 {
			noCommit++
			markHarvested(shell, in.Bench, j.Dir)
			lines.Line(fmt.Sprintf("HARVEST NO-COMMIT bench=%s label=%s branch=%s base=%s (nothing was committed; not pushed)",
				field(in.Bench), field(label), field(branch), field(base)))
			continue
		}
		// THE KEY-SHAPE SCAN COMES BEFORE THE PUSH (#1814). Every Space card is harvested
		// through this verb, and this verb pushes the branch and builds the PR body out of
		// the bench's RESULT.md, so it is the path that most needs the guard. The diff is
		// read from the fetched ref in the local clone, which is exactly what the push
		// would carry. A hit refuses, quarantines the job ON THE BENCH over the same shell
		// seam, writes the HUMAN line, and never marks the job harvested.
		findings, scanErr := secretFindings(j.Dir, clone, "origin/"+base+".."+ref, j.Result)
		if scanErr != nil {
			failed++
			fmt.Fprintln(in.Stderr, secretScanRefusalLine("harvest-bench", label, scanErr))
			continue
		}
		if len(findings) > 0 {
			failed++
			secretRefusal{Site: "harvest-bench", Label: label, JobDir: j.Dir,
				Out: in.Stderr, Move: benchMover(func(script string) (string, error) { return shell.Run(in.Bench, script) })}.refuse(findings)
			continue
		}
		sha, _ := gitIn(clone, "rev-parse", "--short", ref)
		// The same branch rule the local path applies. The --branch-prefix filter above
		// SKIPS a job whose branch is off-prefix, which is a selection, not a guard: it
		// is the caller's own prefix and a caller could widen it. This refuses.
		if err := mustBranchPrefix(branch); err != nil {
			failed++
			lines.Line(fmt.Sprintf("HARVEST PUSH-REFUSED bench=%s label=%s branch=%s: %s",
				field(in.Bench), field(label), field(branch), oneline.Err(err)))
			continue
		}
		// THE DESTINATION, resolved once for this job and used by both the push below
		// and the CreatePR further down -- a refusal skips both. `repo` off the
		// RESULT.md only ever chose the clone and was then handed straight to
		// `CreatePR(repo, ...)`, so a bench card named the repository its own pull
		// request opened on (Johnny's HOLD of #1809).
		//
		// A bench job carries no launch record to this verb, and its own clone is on
		// the BENCH -- the worker's machine -- and is never read here: the branch
		// arrives as a fetched ref. The answer is the COORDINATOR's `--clone`, which
		// this verb already requires: the `<owner>/<name>` the operator typed, or the
		// origin of a directory on THIS machine that no worker has been handed.
		d, derr := dispatchFromCoordinatorClone(in.Clones, clone)
		if derr != nil {
			failed++
			lines.Line(fmt.Sprintf("HARVEST REFUSED repo-unknown card=%s: %s", field(label), oneline.Err(derr)))
			continue
		}
		// The worker clone is "" on purpose: no clone HERE was touched by the worker,
		// and the bench's own copy of `origin` is exactly the claim Johnny's HOLD at
		// 7f692ef6 is about. The RESULT's `repo` claim is still checked.
		dest, err := resolveDestination(label, d, "", repo)
		if err != nil {
			failed++
			lines.Line(err.Error())
			continue
		}
		if out, err := gitIn(clone, "push", "origin", ref+":refs/heads/"+branch); err != nil {
			failed++
			lines.Line(fmt.Sprintf("HARVEST PUSH-FAIL bench=%s label=%s branch=%s: %s",
				field(in.Bench), field(label), field(branch), oneline.Cap(out, 200)))
			continue
		}
		pushed++
		pr, err := forge.FindPR(dest.repo, branch)
		if err != nil {
			failed++
			lines.Line(fmt.Sprintf("HARVEST PR-FAIL bench=%s label=%s branch=%s: %s",
				field(in.Bench), field(label), field(branch), oneline.Err(err)))
			continue
		}
		if pr == 0 {
			pr, err = forge.CreatePR(dest.repo, base, branch, prTitle(line1, label, in.Bench), benchPRBody(in.Bench, j, in.MaxBodyBytes))
			if err != nil {
				failed++
				lines.Line(fmt.Sprintf("HARVEST PR-FAIL bench=%s label=%s branch=%s: %s",
					field(in.Bench), field(label), field(branch), oneline.Err(err)))
				continue
			}
		}
		prs++
		markHarvested(shell, in.Bench, j.Dir)
		lines.Line(fmt.Sprintf("HARVEST JOB bench=%s label=%s branch=%s sha=%s base=%s pr=%s#%d",
			field(in.Bench), field(label), field(branch), field(sha), field(base), field(dest.repo), pr))
	}

	drained := drainLaunched(in, state, lines)
	lines.More()

	fmt.Fprintf(in.Stdout, "HARVEST BENCH %s bench=%s jobs=%d done=%d pushed=%d prs=%d no-commit=%d skipped=%d drained=%d took=%s\n",
		okOrRed(failed), field(in.Bench), len(jobs), done, pushed, prs, noCommit, skipped, drained,
		in.Now().Sub(started).Round(time.Millisecond))
	if failed > 0 {
		return 1
	}
	return 0
}

func okOrRed(failed int) string {
	if failed > 0 {
		return "RED"
	}
	return "OK"
}

// benchListScript is the one script the bench runs: every job directory under the roots,
// its RESULT.md mtime and body when it has one, and nothing else. Every decision -- the
// session, the branch prefix, the base, the age -- is taken here in Go, never in the
// script: half the defects of the hand loop were the shell itself (memory: "No shell for
// coordination").
func benchListScript(roots []string) string {
	globs := make([]string, 0, len(roots))
	for _, r := range roots {
		globs = append(globs, shellQuote(r)+"/*/jobs/*/")
	}
	return "for j in " + strings.Join(globs, " ") + "; do j=${j%/}; [ -d \"$j\" ] || continue; " +
		"printf 'JOB\\t%s\\n' \"$j\"; r=\"$j/RESULT.md\"; " +
		"if [ -f \"$r\" ]; then printf 'MTIME\\t%s\\n' \"$(stat -c %Y \"$r\" 2>/dev/null || echo 0)\"; " +
		"if [ -f \"$j/.harvested\" ]; then printf 'HARVESTED\\n'; fi; sed 's/^/R\\t/' \"$r\"; fi; " +
		"printf 'END\\n'; done"
}

// parseBenchJobs reads what benchListScript printed: JOB, then MTIME and the R lines when
// the job has a RESULT.md, then END. A line the parser does not know is skipped rather
// than guessed at -- a bench that prints a warning on login does not lose the harvest.
func parseBenchJobs(out string) []benchJob {
	var jobs []benchJob
	var cur *benchJob
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "JOB\t"):
			if cur != nil {
				jobs = append(jobs, *cur)
			}
			cur = &benchJob{Dir: strings.TrimSpace(strings.TrimPrefix(line, "JOB\t"))}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "MTIME\t"):
			cur.Mtime, _ = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "MTIME\t")), 10, 64)
		case line == "HARVESTED":
			cur.Harvested = true
		case strings.HasPrefix(line, "R\t"):
			cur.Result = append(cur.Result, strings.TrimPrefix(line, "R\t"))
		case line == "END":
			jobs = append(jobs, *cur)
			cur = nil
		}
	}
	if cur != nil {
		jobs = append(jobs, *cur)
	}
	return jobs
}

// resultField reads a RESULT.md field line: `NAME <value>` or `NAME: <value>`, the first
// one that names it. The workers write `BRANCH rowan/x` and `REPO owner/name`; `BASE` and
// `SESSION` are read the same way.
func resultField(lines []string, name string) string {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, name) {
			continue
		}
		rest := strings.TrimPrefix(t, name)
		rest = strings.TrimPrefix(rest, ":")
		if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		if v := strings.TrimSpace(rest); v != "" {
			return v
		}
	}
	return ""
}

// prTitle is one harvested job's PR title: the RESULT line without its `RESULT` keyword,
// cut at a WORD boundary, with ` (<label>, <bench>)` kept whole. The script cut at 110
// bytes mid-word and then appended the suffix on top, so the title both lost a word and ran
// past the cap it was cut for.
func prTitle(result, label, bench string) string {
	const ellipsis = "..."
	suffix := fmt.Sprintf(" (%s, %s)", label, bench)
	head := strings.TrimSpace(result)
	for _, p := range []string{"RESULT: ", "RESULT "} {
		if s, ok := strings.CutPrefix(head, p); ok {
			head = strings.TrimSpace(s)
			break
		}
	}
	budget := prTitleMax - len(suffix) - len(ellipsis)
	if budget < 1 {
		return oneline.Cap(head+suffix, prTitleMax)
	}
	if len(head)+len(suffix) <= prTitleMax {
		return head + suffix
	}
	cut := head[:budget]
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return cut + ellipsis + suffix
}

// benchPRBody is the PR body: the RESULT.md as the worker wrote it, bounded, then the line
// that says where it came from.
func benchPRBody(bench string, j benchJob, max int) string {
	if max <= 0 {
		max = 4096
	}
	body := strings.Join(j.Result, "\n")
	if len(body) > max {
		body = body[:max]
	}
	return body + fmt.Sprintf("\n\nHarvested from %s job %s by `nova-pulse harvest --bench`. The branch was pushed from the coordinator, not from the bench.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n", bench, j.Dir)
}

// benchRepoURL is the job clone's git URL on the bench. A job directory is absolute, so the
// path after the host is absolute too; a relative one is read from the bench's home.
func benchRepoURL(bench, dir string) string {
	if strings.HasPrefix(dir, "/") {
		return "ssh://" + bench + dir + "/repo"
	}
	return "ssh://" + bench + "/~/" + dir + "/repo"
}

// cloneFor is the local clone a repo's branch is pushed from: `--clone <owner>/<name>=<dir>`
// names one repo's clone, and a bare `--clone <dir>` is the clone for any repo that no
// entry names. The script carried the same table with two repos hardcoded in it.
func cloneFor(clones []string, repo string) string {
	fallback := ""
	for _, c := range clones {
		name, dir, ok := strings.Cut(c, "=")
		if !ok {
			if fallback == "" {
				fallback = strings.TrimSpace(c)
			}
			continue
		}
		if strings.TrimSpace(name) == repo {
			return strings.TrimSpace(dir)
		}
	}
	return fallback
}

// commitCount is `git rev-list --count <range>`: how many commits the job made on top of
// its base. A job that committed nothing is not pushed.
func commitCount(clone, rng string) (int, error) {
	out, err := gitIn(clone, "rev-list", "--count", rng)
	if err != nil {
		return 0, fmt.Errorf("%s: %s", oneline.Err(err), oneline.Cap(out, 120))
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(lastField(out)))
	if convErr != nil {
		return 0, fmt.Errorf("rev-list answered %q", oneline.Cap(out, 120))
	}
	return n, nil
}

func lastField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// gitIn runs one git child in the named clone, bounded, and returns what it said.
func gitIn(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// markHarvested leaves the `.harvested` marker beside the job on the bench, so the next
// harvest does not open the same PR again. It is the bench's memory of this verb.
func markHarvested(shell BenchShell, bench, dir string) {
	_, _ = shell.Run(bench, "touch "+shellQuote(dir+"/.harvested"))
}

// shellQuote wraps one argument in single quotes for the remote shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// drainLaunched releases the lane of every launched card whose job has finished: the card
// leaves --launched for --done or --failed and its launched marker goes with it, so the
// lane is free on the next fill tick. A job still running is left alone. Nothing but
// `manager` drained --launched before this, and a lane taken by a card that finished hours
// ago stayed occupied forever (dogfood, 2026-09-18).
//
// The state map is label -> done|running. A launched card no job dir carries any more is
// failed: the job is gone, nothing came back, and the lane is still not the card's to hold.
func drainLaunched(in HarvestInput, state map[string]string, lines *boundedList) int {
	if strings.TrimSpace(in.Launched) == "" {
		return 0
	}
	doneDir, failedDir := in.Done, in.Failed
	if doneDir == "" {
		doneDir = filepath.Join(filepath.Dir(in.Launched), "done")
	}
	if failedDir == "" {
		failedDir = filepath.Join(filepath.Dir(in.Launched), "failed")
	}
	now := in.Now
	if now == nil {
		now = time.Now
	}
	stamp := now().UTC().Format("20060102T150405Z")

	drained := 0
	cards := readyCards(in.Launched)
	sort.Strings(cards)
	for _, card := range cards {
		base := filepath.Base(card)
		m := readLaunchedMarker(in.Launched, base)
		label := m["label"]
		if label == "" {
			label = strings.TrimSuffix(base, ".md")
		}
		st, why := "failed", "job-dir-gone"
		switch state[label] {
		case "running":
			continue
		case "done":
			st, why = "done", "result"
		case "provider":
			st, why = "provider", "provider-error"
		}

		readyDir := in.Ready
		if readyDir == "" {
			if _, err := os.Stat(filepath.Join(filepath.Dir(in.Launched), "pending")); err == nil {
				readyDir = filepath.Join(filepath.Dir(in.Launched), "pending")
			} else {
				readyDir = filepath.Join(filepath.Dir(in.Launched), "ready")
			}
		}

		isProvErr := st == "provider" || (in.Root != "" && IsJobProviderError(filepath.Join(in.Root, label)))
		if !isProvErr && in.Root != "" {
			if matches, _ := filepath.Glob(filepath.Join(in.Root, "*", "jobs", label)); len(matches) > 0 {
				for _, m := range matches {
					if IsJobProviderError(m) {
						isProvErr = true
						break
					}
				}
			}
		}

		if (st == "failed" || st == "provider") && isProvErr {
			requeued, _, rerr := RequeueProviderCard(readyDir, in.Launched, base, DefaultMaxProviderRetries)
			if rerr == nil && requeued {
				_ = os.Remove(launchedMarker(in.Launched, base))
				note := fmt.Sprintf("%s\tlane=%s\tbench=%s\tlabel=%s\twhy=%s\n", stamp, m["lane"], m["bench"], label, "provider-requeued")
				_ = os.WriteFile(marker(readyDir, base, "requeued", stamp), []byte(note), 0o644)
				drained++
				lines.Line(fmt.Sprintf("HARVEST DRAIN card=%s lane=%s state=%s bench=%s why=%s",
					field(base), field(m["lane"]), "requeued", field(m["bench"]), "provider-requeued"))
				continue
			}
			st = "failed"
			why = "provider-failed"
		}

		dir := doneDir
		if st == "failed" {
			dir = failedDir
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "HARVEST NOTE drain %s: %s\n", field(base), oneline.Err(err))
			continue
		}
		if err := os.Rename(card, filepath.Join(dir, base)); err != nil {
			fmt.Fprintf(in.Stderr, "HARVEST NOTE drain %s: %s\n", field(base), oneline.Err(err))
			continue
		}
		_ = os.Remove(launchedMarker(in.Launched, base))
		note := fmt.Sprintf("%s\tlane=%s\tbench=%s\tlabel=%s\twhy=%s\n", stamp, m["lane"], m["bench"], label, why)
		_ = os.WriteFile(marker(dir, base, st, stamp), []byte(note), 0o644)
		drained++
		lines.Line(fmt.Sprintf("HARVEST DRAIN card=%s lane=%s state=%s bench=%s why=%s",
			field(base), field(m["lane"]), st, field(m["bench"]), why))
	}
	return drained
}

// localJobStates is the drain's state map when there is no --bench: every
// `<root>/<slot>/jobs/<label>` under the root, done when it carries a RESULT.md.
func localJobStates(root string) map[string]string {
	out := map[string]string{}
	slots, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, s := range slots {
		if !s.IsDir() {
			continue
		}
		jobs, err := os.ReadDir(filepath.Join(root, s.Name(), "jobs"))
		if err != nil {
			continue
		}
		for _, j := range jobs {
			if !j.IsDir() {
				continue
			}
			st := "running"
			jobDir := filepath.Join(root, s.Name(), "jobs", j.Name())
			if _, err := os.Stat(filepath.Join(jobDir, "RESULT.md")); err == nil {
				st = "done"
			} else if IsJobProviderError(jobDir) {
				st = "provider"
			}
			out[j.Name()] = st
		}
	}
	return out
}

// sshShell is the shipped BenchShell: one bounded ssh per call, the script as an argument
// and never on stdin (`ssh -n host bash -s < script` runs nothing at all -- the edge of
// 2026-09-17).
type sshShell struct{ Program string }

func (s sshShell) Run(bench, script string) (string, error) {
	prog := s.Program
	if prog == "" {
		prog = "ssh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	testguard.RefuseHosts(prog, "-n", "-o", "BatchMode=yes", bench, script)
	cmd := exec.CommandContext(ctx, prog, "-n", "-o", "BatchMode=yes", bench, script)
	var out bytes.Buffer
	said := &benchTail{}
	cmd.Stdout, cmd.Stderr = &out, said
	if err := cmd.Run(); err != nil {
		return out.String(), said.wrap(err)
	}
	return out.String(), nil
}

// benchTail keeps the last words a child said, so a failure names its cause and not only
// its exit status.
type benchTail struct{ buf []byte }

func (t *benchTail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > oneline.TailBytes {
		t.buf = t.buf[len(t.buf)-oneline.TailBytes:]
	}
	return len(p), nil
}

func (t *benchTail) wrap(err error) error {
	said := strings.TrimSpace(string(t.buf))
	if said == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, oneline.Cap(said, 200))
}

// ghForge is the shipped Forge: gh, bounded, and never a merge.
type ghForge struct{}

func (ghForge) FindPR(repo, branch string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "list", "-R", repo, "--head", branch,
		"--state", "open", "--json", "number", "-q", ".[0].number")
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("gh pr list -R %s --head %s: %s", repo, branch, oneline.Err(err))
	}
	s := strings.TrimSpace(string(out))
	if s == "" || s == "null" {
		return 0, nil
	}
	n, convErr := strconv.Atoi(s)
	if convErr != nil {
		return 0, fmt.Errorf("gh pr list answered %q", oneline.Cap(s, 120))
	}
	return n, nil
}

func (ghForge) CreatePR(repo, base, branch, title, body string) (int, error) {
	// The shipped forge is the last thing between a branch name and a real pull request,
	// so the rule is here too and not only in its callers. The class test found this one:
	// I had guarded the four callers and walked past the adapter they all go through.
	if err := mustBranchPrefix(branch); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "create", "-R", repo, "--base", base,
		"--head", branch, "--title", title, "--body-file", "-")
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("%s: %s", oneline.Err(err), oneline.Cap(string(out), 200))
	}
	return parsePRNumber(string(out)), nil
}
