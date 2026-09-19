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
	jobs, missing, incomplete := parseBenchJobs(raw)
	// A --root that does not resolve ON THE BENCH is a refusal before any state changes,
	// never a silent `jobs=0` (#1950): a quoted '~/rowan-working/tmp' reached the verb with
	// the tilde unexpanded, the listing found nothing, and the drain read that nothing as
	// "every job is gone". The listing itself reports each root it could not open.
	if len(missing) > 0 {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("--root %s does not exist on %s (name the swarm root ON the bench; this verb does not expand a leading ~ -- pass an absolute path)",
			field(strings.Join(missing, ",")), field(in.Bench)))
	}

	lines := bound(in.Stdout, in.Max)
	roots := splitList(in.Root)
	facts := drainFacts{
		state: map[string]string{}, // label -> the drain's verdict on that job
		// A root the bench could not read whole proves no absence: the traversal that
		// would have found the job may simply have been refused (#1950, Stella's
		// two-root fixture). The fold still folds what it DID see.
		complete: len(incomplete) == 0,
		// The per-label probe (Johnny's HOLD on #1984): `job-dir-gone` is never
		// inferred from a sibling job being listed. Each candidate card's OWN job
		// directory is looked for BY NAME on the bench, over the same shell seam.
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
	var done, pushed, prs, noCommit, skipped, failed int

	for _, j := range jobs {
		label := filepath.Base(j.Dir)
		if len(j.Result) == 0 {
			state[label] = jobRunning
			continue
		}
		// Not jobDone until this fold reaches a durable end for this job. A launch
		// record is released only after its result has been harvested (#1950), so a
		// job that was filtered out, or whose fetch, push or PR failed, leaves its
		// card exactly where it is.
		state[label] = jobHeld
		done++
		if j.Harvested {
			skipped++
			state[label] = jobDone
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
			state[label] = jobDone
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
		state[label] = jobDone
		lines.Line(fmt.Sprintf("HARVEST JOB bench=%s label=%s branch=%s sha=%s base=%s pr=%s#%d",
			field(in.Bench), field(label), field(branch), field(sha), field(base), field(dest.repo), pr))
	}

	drained, left, drainFailed := drainLaunched(in, facts, lines)
	failed += drainFailed
	lines.More()

	fmt.Fprintf(in.Stdout, "HARVEST BENCH %s bench=%s jobs=%d done=%d pushed=%d prs=%d no-commit=%d skipped=%d drained=%d left=%d took=%s\n",
		okOrRed(failed), field(in.Bench), len(jobs), done, pushed, prs, noCommit, skipped, drained, left,
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
// It also answers, per root, whether the traversal ON THE BENCH actually COMPLETED --
// `ROOT <path> <ok|missing|incomplete>`:
//
//   - `missing`, so a root that does not resolve there is a refusal before any state
//     changes rather than an empty listing the drain reads as "everything is gone" (#1950);
//   - `incomplete`, because a glob is silent about the difference between "nothing is here"
//     and "I was not allowed to look". Stella's fixture, run under /bin/sh: root A holds a
//     readable job, root B holds a LIVE job under a `jobs` directory at mode 000. The glob
//     `B/*/jobs/*/` matches nothing, the script exits 0, and B's live card was then
//     classified `job-dir-gone` on the strength of A's job. So every root, every slot
//     under it and every `jobs` directory under that is held against `-r` and `-x` before
//     the glob is believed, and one unreadable directory makes that WHOLE root incomplete.
//     An incomplete root proves no absence: no marker is drained as `job-dir-gone` from a
//     run that could not read the whole scope a marker might live in.
func benchListScript(roots []string) string {
	globs := make([]string, 0, len(roots))
	var checks strings.Builder
	for _, r := range roots {
		q := shellQuote(r)
		globs = append(globs, q+"/*/jobs/*/")
		// `c` is this root's completeness: any directory on the path to a job that is
		// there but cannot be read or entered makes the traversal incomplete.
		fmt.Fprintf(&checks, "if [ -d %s ]; then c=1; if [ ! -r %s ] || [ ! -x %s ]; then c=0; fi; "+
			"for s in %s/*/; do s=${s%%/}; [ -d \"$s\" ] || continue; "+
			"if [ ! -r \"$s\" ] || [ ! -x \"$s\" ]; then c=0; continue; fi; "+
			"[ -e \"$s/jobs\" ] || continue; "+
			"if [ ! -d \"$s/jobs\" ] || [ ! -r \"$s/jobs\" ] || [ ! -x \"$s/jobs\" ]; then c=0; fi; done; "+
			"if [ \"$c\" = 1 ]; then printf 'ROOT\\t%%s\\tok\\n' %s; else printf 'ROOT\\t%%s\\tincomplete\\n' %s; fi; "+
			"else printf 'ROOT\\t%%s\\tmissing\\n' %s; fi; ",
			q, q, q, q, q, q, q)
	}
	return checks.String() +
		"for j in " + strings.Join(globs, " ") + "; do j=${j%/}; [ -d \"$j\" ] || continue; " +
		"printf 'JOB\\t%s\\n' \"$j\"; r=\"$j/RESULT.md\"; " +
		"if [ -f \"$r\" ]; then printf 'MTIME\\t%s\\n' \"$(stat -c %Y \"$r\" 2>/dev/null || echo 0)\"; " +
		"if [ -f \"$j/.harvested\" ]; then printf 'HARVESTED\\n'; fi; sed 's/^/R\\t/' \"$r\"; fi; " +
		"printf 'END\\n'; done"
}

// probeLabelMax caps one probe script: a shared launched directory holds a few hundred
// cards at most, and a script naming more than this is a sign something else is wrong.
const probeLabelMax = 500

// benchProbeScript asks the bench about ONE THING per label: is there a job directory of
// this name under any root, and could every place it might be actually be read?
//
// Johnny's HOLD on #1984: `job-dir-gone` fired when any SIBLING job was listed, never
// because this card's own directory was probed absent. A card is now called dead only when
// the bench was asked for it BY NAME and said `absent`:
//
//	PROBE <label> present   -- the job directory is there; the card is alive
//	PROBE <label> absent    -- every readable place it could be was looked in; it is gone
//	PROBE <label> unknown   -- a `jobs` directory on the way could not be read or entered
//
// `unknown` beats `absent` and `present` beats both: no permission failure is ever read as
// an absence (Stella's third residual, at the label's own scope this time).
func benchProbeScript(roots, labels []string) string {
	if len(labels) > probeLabelMax {
		labels = labels[:probeLabelMax]
	}
	quoted := make([]string, 0, len(labels))
	for _, l := range labels {
		quoted = append(quoted, shellQuote(l))
	}
	slots := make([]string, 0, len(roots))
	for _, r := range roots {
		slots = append(slots, shellQuote(r)+"/*/")
	}
	return "for l in " + strings.Join(quoted, " ") + "; do p=0; u=0; " +
		"for s in " + strings.Join(slots, " ") + "; do s=${s%/}; [ -d \"$s\" ] || continue; " +
		"j=\"$s/jobs\"; [ -d \"$j\" ] || continue; " +
		"if [ ! -r \"$j\" ] || [ ! -x \"$j\" ]; then u=1; continue; fi; " +
		"if [ -e \"$j/$l\" ]; then p=1; fi; done; " +
		"if [ \"$p\" = 1 ]; then printf 'PROBE\\t%s\\tpresent\\n' \"$l\"; " +
		"elif [ \"$u\" = 1 ]; then printf 'PROBE\\t%s\\tunknown\\n' \"$l\"; " +
		"else printf 'PROBE\\t%s\\tabsent\\n' \"$l\"; fi; done"
}

// parseProbes reads the PROBE lines back. A label the bench said nothing about is absent
// from the map, which the drain reads as "not proven" and leaves alone.
func parseProbes(out string) map[string]string {
	probed := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		rest, ok := strings.CutPrefix(line, "PROBE\t")
		if !ok {
			continue
		}
		label, state, ok := strings.Cut(rest, "\t")
		if !ok {
			continue
		}
		switch state = strings.TrimSpace(state); state {
		case "present", "absent", "unknown":
			probed[strings.TrimSpace(label)] = state
		}
	}
	return probed
}

// parseBenchJobs reads what benchListScript printed: JOB, then MTIME and the R lines when
// the job has a RESULT.md, then END. A line the parser does not know is skipped rather
// than guessed at -- a bench that prints a warning on login does not lose the harvest.
// It returns the roots the bench answered `missing` and `incomplete` for beside the jobs; a
// listing that names no root at all (an older bench script, a test's fake shell) reports
// neither, so the refusal is on evidence and never on silence.
func parseBenchJobs(out string) (jobs []benchJob, missing, incomplete []string) {
	var cur *benchJob
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "ROOT\t"):
			path, state, ok := strings.Cut(strings.TrimPrefix(line, "ROOT\t"), "\t")
			if !ok {
				continue
			}
			switch strings.TrimSpace(state) {
			case "missing":
				missing = append(missing, strings.TrimSpace(path))
			case "incomplete":
				incomplete = append(incomplete, strings.TrimSpace(path))
			}
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
	return jobs, missing, incomplete
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

// The drain's verdict on one job, as the fold reached it. Only jobDone releases the card
// that launched it: a job this run filtered out, or whose fetch, push or forge call failed,
// is jobHeld and its card stays exactly where it is, because a launch record is removed
// only after its result has been durably harvested (#1950).
const (
	jobRunning = "running"
	jobDone    = "done"
	jobHeld    = "held"
)

// drainLaunched releases the lane of every launched card THIS harvest folded, and touches
// nothing else. The card leaves --launched for --done or --failed and its launched marker
// MOVES with it, so the lane is free on the next fill tick and the routing metadata
// survives. Nothing but `manager` drained --launched before this, and a lane taken by a
// card that finished hours ago stayed occupied forever (dogfood, 2026-09-18).
//
// #1950 is why the rest of this function is a wall of guards. The launched directory of a
// pull queue is SHARED -- seven manager lanes drop cards into one ready/ and the resident
// fill loops move them into one launched/ -- and this drain read ONE bench's job listing
// and then took `not in that listing` as `the job is gone` for EVERY card in the directory.
// One `harvest --bench vision --max 1` emptied a live 151-card queue in 733 ms, marked
// every card failed and deleted every marker, including five cards of other lanes whose
// jobs were running on other benches. The rule now, one guard per clause:
//
//	(a) the record must be the caller's   -- a KNOWN session against the marker's session=
//	(b) it must match --bench when given  -- --bench against the marker's bench=
//	(c) the job must be finished, or provably dead -- a RESULT.md this fold harvested, or
//	    a label absent from a COMPLETE, readable listing that covered this card's bench
//	(d) --max bounds what is CONSUMED, not just what is printed
//
// Everything else is left byte-identical and counted in `left`, with one bounded
// `HARVEST LEFT reason=<r> cards=<n>` line per reason, so `drained=<n> left=<m>` is the
// whole receipt: what this harvest took, what it deliberately did not, and why.
//
// The card and its marker move as a PAIR or not at all (Stella's HOLD on #1984): the
// destination is checked for a collision first, the MARKER moves before the card, and a
// card move that fails rolls the marker back. A drain that could not complete is never
// counted in `drained`, prints `HARVEST DRAIN-FAIL` with a named reason, and makes the verb
// exit non-zero. Reporting `drained=1` over a split pair is worse than reporting nothing.
func drainLaunched(in HarvestInput, facts drainFacts, lines *boundedList) (drained, left, failed int) {
	if strings.TrimSpace(in.Launched) == "" {
		return 0, 0, 0
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

	leftBy := map[string]int{}
	cards := readyCards(in.Launched)
	sort.Strings(cards)

	// Pass one decides everything that can be decided from what the fold already knows,
	// and collects the labels that need the bench asked about them BY NAME.
	type candidate struct {
		card, base, label string
		m                 map[string]string
	}
	items := make([]candidate, 0, len(cards))
	var unprobed []string
	for _, card := range cards {
		base := filepath.Base(card)
		m := readLaunchedMarker(in.Launched, base)
		label := m["label"]
		if label == "" {
			label = strings.TrimSuffix(base, ".md")
		}
		items = append(items, candidate{card: card, base: base, label: label, m: m})
		if _, _, reason := drainVerdict(in, m, facts, label); reason == "unprobed" {
			unprobed = append(unprobed, label)
		}
	}
	// The probe is ONE call for every candidate, and it is the only thing that can turn
	// an unknown label into `job-dir-gone` (Johnny's HOLD on #1984).
	if len(unprobed) > 0 && facts.probe != nil {
		facts.probed = facts.probe(unprobed)
	}

	for _, it := range items {
		card, base, label, m := it.card, it.base, it.label, it.m
		st, why, reason := drainVerdict(in, m, facts, label)
		if st == "" {
			left++
			leftBy[reason]++
			continue
		}
		// (d) --max bounds the mutation. `--max 1` printed one line and drained 151.
		if in.Max > 0 && drained >= in.Max {
			left++
			leftBy["max"]++
			continue
		}
		dir := doneDir
		if st == "failed" {
			dir = failedDir
		}
		fail := func(reason, detail string) {
			failed++
			lines.Line(fmt.Sprintf("HARVEST DRAIN-FAIL card=%s lane=%s bench=%s reason=%s %s",
				field(base), field(m["lane"]), field(m["bench"]), reason, detail))
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail("destination", oneline.Err(err))
			continue
		}
		// The pair moves whole or not at all. A destination that already holds either
		// half is evidence somebody else wrote, and a rename would overwrite it.
		destCard, destMarker := filepath.Join(dir, base), launchedMarker(dir, base)
		srcMarker := launchedMarker(in.Launched, base)
		hasMarker := exists(srcMarker)
		if exists(destCard) || (hasMarker && exists(destMarker)) {
			fail("destination-exists", fmt.Sprintf("dir=%s (something is already there; this run overwrites no evidence)", field(dir)))
			continue
		}
		// The launched marker MOVES with its card and is never deleted: it is the only
		// record of which bench the job is on, and a card whose marker was deleted
		// cannot be harvested by anyone afterwards -- 149 had to be reconstructed by
		// hand (#1950). It moves FIRST: a marker that cannot move leaves the card
		// exactly where it is, with its record beside it.
		if hasMarker {
			if err := renameForDrain(srcMarker, destMarker); err != nil {
				fail("marker-move", fmt.Sprintf("%s (the card stays where it is, with its record)", oneline.Err(err)))
				continue
			}
		}
		if err := renameForDrain(card, destCard); err != nil {
			detail := oneline.Err(err)
			if hasMarker {
				// Put the record back beside its card: the pair is recoverable in
				// ONE place, which is the whole point of moving the marker first.
				if back := renameForDrain(destMarker, srcMarker); back != nil {
					fail("rollback", fmt.Sprintf("%s; the marker is at %s and its card at %s -- MOVE IT BACK BY HAND",
						detail, field(destMarker), field(card)))
					continue
				}
			}
			fail("card-move", fmt.Sprintf("%s (the pair is untouched under --launched)", detail))
			continue
		}
		note := fmt.Sprintf("%s\tlane=%s\tbench=%s\tlabel=%s\twhy=%s\n", stamp, m["lane"], m["bench"], label, why)
		_ = os.WriteFile(marker(dir, base, st, stamp), []byte(note), 0o644)
		drained++
		lines.Line(fmt.Sprintf("HARVEST DRAIN card=%s lane=%s state=%s bench=%s why=%s",
			field(base), field(m["lane"]), st, field(m["bench"]), why))
	}
	for _, reason := range leftReasons {
		if n := leftBy[reason]; n > 0 {
			lines.Line(fmt.Sprintf("HARVEST LEFT reason=%s cards=%d", reason, n))
		}
	}
	if leftBy["no-session"] > 0 {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE drain: --launched without --session drains nothing (name the session whose cards these are: the one `fill --session` stamped into the launched markers)\n")
	}
	return drained, left, failed
}

// renameForDrain is the drain's one mutation, held in a variable so a test can inject a
// failing rename and prove the card and its marker are never left in two places. Nothing
// but a test ever replaces it.
var renameForDrain = os.Rename

// exists says whether a path is there at all -- a file, a directory, anything.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// leftReasons is the printing order of the `HARVEST LEFT` counts: one bounded line per
// reason, never one per card, because a shared queue holds a hundred cards that are simply
// somebody else's and saying so a hundred times is not a receipt.
var leftReasons = []string{"running", "unharvested", "no-session", "other-session", "other-bench",
	"probe-present", "probe-unknown", "unprobed", "unproven", "incomplete-listing", "max"}

// drainFacts is everything the fold learned, and the ONLY evidence the drain is allowed to
// decide on: what became of each job, the session each job's own RESULT.md named, and
// whether the bench's traversal was complete and readable. Anything not in here is unknown,
// and unknown means leave the card alone.
type drainFacts struct {
	state    map[string]string // label -> jobRunning | jobDone | jobHeld
	complete bool              // every --root was enumerated whole, and readable
	// probed is the per-label answer to "is THIS card's own job directory there?":
	// present | absent | unknown. probe fills it in one call for the labels the fold
	// said nothing about. A label in neither is not proven gone and is left alone.
	probed map[string]string
	probe  func(labels []string) map[string]string
}

// drainVerdict answers what this harvest may do with ONE launched card, from that card's
// own launch record and from what the fold learned. Everything this run cannot PROVE is its
// own and finished is left exactly as found, with the reason it was left. This is #1950's
// rule and the only place it is decided.
func drainVerdict(in HarvestInput, m map[string]string, facts drainFacts, label string) (st, why, reason string) {
	// (a) The record must be the caller's, and the caller must SAY who that is. Stella's
	// ownership ruling and Johnny's finding on #1984: an omitted --session silently meant
	// every session in a shared queue. It is not inferred from anything -- not from the
	// job's RESULT.md, not from the lane, not from the bench. No --session, no drain.
	want := strings.TrimSpace(in.Session)
	if want == "" {
		return "", "", "no-session"
	}
	if m["session"] != want {
		return "", "", "other-session"
	}
	// (b) It must be on the bench this harvest looked at. The drain printed
	// `bench=captainamerica` under `--bench vision` and moved the card anyway.
	if m["bench"] != strings.TrimSpace(in.Bench) {
		return "", "", "other-bench"
	}
	// (c) Finished, or provably dead. jobHeld and an unknown job are both "not proven".
	switch facts.state[label] {
	case jobDone:
		return "done", "result", ""
	case jobRunning:
		return "", "", "running"
	case jobHeld:
		return "", "", "unharvested"
	}
	// A label the listing did not name is `job-dir-gone` only on EVIDENCE OF ABSENCE, and
	// there are three ways to have none.
	//
	// An empty listing proves nothing -- `jobs=0 ... drained=151` was the whole of #1950.
	// A traversal that could not read a directory proves nothing either: Stella's fixture
	// put a live job under a `jobs` directory at mode 000, the glob matched nothing, the
	// script exited 0, and a second root's visible job was enough to call that live card
	// dead. And a sibling job being listed says nothing at all about THIS card (Johnny),
	// so the bench is asked for this label BY NAME and only its own answer counts.
	if !facts.complete {
		return "", "", "incomplete-listing"
	}
	if len(facts.state) == 0 {
		return "", "", "unproven"
	}
	switch facts.probed[label] {
	case "absent":
		return "failed", "job-dir-gone", ""
	case "present":
		return "", "", "probe-present"
	case "unknown":
		return "", "", "probe-unknown"
	}
	// Nobody has asked the bench about this label yet, or the probe never answered.
	return "", "", "unprobed"
}

// localJobStates is the drain's state map when there is no --bench: every
// `<root>/<slot>/jobs/<label>` under the root, done when it carries a RESULT.md.
// It answers `complete` too: a directory that is there and could not be read is a hole in
// the traversal, and a hole means this run cannot say any label is absent (#1950).
func localJobStates(root string) (map[string]string, bool) {
	out := map[string]string{}
	slots, err := os.ReadDir(root)
	if err != nil {
		return out, false
	}
	complete := true
	for _, s := range slots {
		if !s.IsDir() {
			continue
		}
		jobsDir := filepath.Join(root, s.Name(), "jobs")
		jobs, err := os.ReadDir(jobsDir)
		if err != nil {
			if !os.IsNotExist(err) {
				complete = false
			}
			continue
		}
		for _, j := range jobs {
			if !j.IsDir() {
				continue
			}
			st := jobRunning
			if _, err := os.Stat(filepath.Join(jobsDir, j.Name(), "RESULT.md")); err == nil {
				st = jobDone
			}
			out[j.Name()] = st
		}
	}
	return out, complete
}

// localProbe is the local form's per-label probe, the same question benchProbeScript asks
// over ssh: is THIS label's own job directory under this root, and could every `jobs`
// directory it might be in actually be read? os.ReadDir reports the permission error a
// glob swallows, so an unreadable directory answers `unknown` and never `absent`.
func localProbe(root string, labels []string) map[string]string {
	out := map[string]string{}
	slots, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	unknown := false
	dirs := make([]string, 0, len(slots))
	for _, s := range slots {
		if !s.IsDir() {
			continue
		}
		jobsDir := filepath.Join(root, s.Name(), "jobs")
		if _, err := os.ReadDir(jobsDir); err != nil {
			if !os.IsNotExist(err) {
				unknown = true
			}
			continue
		}
		dirs = append(dirs, jobsDir)
	}
	for _, label := range labels {
		state := "absent"
		for _, d := range dirs {
			if exists(filepath.Join(d, label)) {
				state = "present"
				break
			}
		}
		if state == "absent" && unknown {
			state = "unknown"
		}
		out[label] = state
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
