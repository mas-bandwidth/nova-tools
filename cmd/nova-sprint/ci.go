// The ci verb (#2756 4.8 and section 10, nova-tools #2936) registers itself
// through the S0 registry, so it never edits main.go. cut, rerun and dispose
// are one guarded Redis Function call each; show and status only read.
//
// request, run and compare are our own CI (#3597, #3349): a head requested
// into ci:pool, a bench's runner-only pass over it, and the one budgeted
// parity read against GitHub. github is the GitHub leg (#3597): the
// ci-github consumer of ev:github writes ci:<repo>:<sha>:gh from the webhook
// deliveries, so nothing polls GitHub for a check state; github --from-runner
// (card gh-ci-receipts) is the ci-ok job writing the same record itself. status --repo --sha
// prints that record's rows and the GitHub leg, from Redis only.
// parity (#3041) is the sprint's measurement from Redis alone: no GitHub poll.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchrole"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
)

func init() {
	register(Verb{
		Name:    "ci",
		Summary: "request, run, status and compare of our own ci; github writes Actions results from ev:github, github --from-runner is ci-ok reporting its own run; cut, show, rerun, dispose and parity of ci cards",
		Run:     runCI,
	})
}

const ciUsage = "want request, run, status, compare, github, cut, show, rerun, dispose or parity"

const ciRunnerUsage = "ci github --from-runner --redis <addr> --repo owner/name --sha <40hex> --run-id <n> --event <ev> --workflow <name> --conclusion success|failure|cancelled [--head-branch <b>] [--base-branch <b>] [--pr <n>] [--at <rfc3339>] --job <name>=<result>..."

func runCI(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "ci", ciUsage)
	}
	switch args[0] {
	case "cut":
		return runCICut(ctx, args[1:], out, errOut)
	case "show":
		return runCIShow(ctx, args[1:], out, errOut)
	case "rerun":
		return runCIRerun(ctx, args[1:], out, errOut)
	case "dispose":
		return runCIDispose(ctx, args[1:], out, errOut)
	case "status":
		return runCIStatus(ctx, args[1:], out, errOut)
	case "parity":
		return runCIParity(ctx, args[1:], out, errOut)
	case "request":
		return runCIRequest(ctx, args[1:], out, errOut)
	case "run":
		return runCIRun(ctx, args[1:], out, errOut)
	case "compare":
		return runCICompare(ctx, args[1:], out, errOut)
	case "github":
		return runCIGitHub(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "ci", "unknown subverb "+args[0]+"; "+ciUsage)
	}
}

func runCICut(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci cut")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	pr := fs.Int("pr", 0, verbflag.HelpPR)
	sha := fs.String("sha", "", "the PR's head sha the ci card tests")
	base := fs.String("base", "", "the tested base tip sha, 40 hex")
	baseRef := fs.String("base-ref", "", "the base ref (dev) the base sha is the tip of")
	leg := fs.String("leg", "go", "the CI leg the card runs: go, schema, ...")
	paths := fs.String("paths", "", "the paths the PR touches, comma-separated (picks the legs)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci cut", err.Error())
	}
	actor := seatActor()
	if *sha == "" || *base == "" || *baseRef == "" {
		return refuse(errOut, "ci cut", "needs --sha, --base and --base-ref: the head and base tip read by REST for the PR")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci cut", err.Error())
	}
	defer st.Close()
	r, err := ci.Cut(ctx, st, ci.CutRequest{Sprint: *sprint, Repo: *repo, PR: *pr, Head: *sha,
		Base: *base, BaseRef: *baseRef, Leg: *leg, Paths: *paths, Actor: actor})
	if err != nil {
		return refuse(errOut, "ci cut", err.Error())
	}
	fmt.Fprintf(out, "%s %s\n", r, ci.Label(*pr, *sha, *base))
	return r.ExitCode()
}

func runCIShow(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci show")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	repo := fs.String("repo", "nova-tools", verbflag.HelpRepo)
	sha := fs.String("sha", "", "the head sha the ci record is of")
	_ = fs.String("sprint", "", verbflag.HelpSprint)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci show", err.Error())
	}
	if fs.NArg() > 0 || *sha == "" {
		return refuse(errOut, "ci show", "needs --sha <head>")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci show", err.Error())
	}
	defer st.Close()
	rec, err := ci.Read(ctx, st, *repo, *sha)
	if err != nil {
		return refuse(errOut, "ci show", err.Error())
	}
	return ci.WriteShow(out, rec)
}

func runCIRerun(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci rerun")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	repo := fs.String("repo", "nova-tools", verbflag.HelpRepo)
	pr := fs.Int("pr", 0, verbflag.HelpPR)
	sha := fs.String("sha", "", "the full head sha to rerun, 40 hex")
	base := fs.String("base", "", "the full tested base tip sha (with --pr)")
	why := fs.String("why", "", verbflag.HelpWhy)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci rerun", err.Error())
	}
	if fs.NArg() > 0 || len(*sha) != 40 {
		return refuse(errOut, "ci rerun", "needs --sha <head>, the full head sha")
	}
	actor := seatActor()
	if *pr > 0 && len(*base) != 40 {
		return refuse(errOut, "ci rerun", "--pr needs --base, the full tested base tip sha: the label is ci-<pr>-<head8>-<base8>")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci rerun", err.Error())
	}
	defer st.Close()
	label := ""
	if *pr > 0 {
		label = ci.Label(*pr, *sha, *base)
	} else {
		rec, err := ci.Read(ctx, st, *repo, *sha)
		if err != nil {
			return refuse(errOut, "ci rerun", err.Error())
		}
		s, l, ok := strings.Cut(rec.Fields["card"], "/")
		if !ok || s != *sprint {
			return refuse(errOut, "ci rerun", "no ci card for that head in sprint "+*sprint+"; pass --pr and --base")
		}
		label = l
	}
	r, err := ci.Rerun(ctx, st, *sprint, label, actor, *why)
	if err != nil {
		return refuse(errOut, "ci rerun", err.Error())
	}
	fmt.Fprintf(out, "%s %s\n", r, label)
	return r.ExitCode()
}

func runCIDispose(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci dispose")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	repo := fs.String("repo", "nova-tools", verbflag.HelpRepo)
	sha := fs.String("sha", "", "the head sha the disposition is of")
	disposition := fs.String("disposition", "", "the typed DISPOSITION line's verdict")
	friend := fs.String("as", "", verbflag.HelpAs)
	url := fs.String("url", "", "the comment url the disposition was posted at")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci dispose", err.Error())
	}
	if fs.NArg() > 0 || *sha == "" {
		return refuse(errOut, "ci dispose", "needs --sha <head>")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci dispose", err.Error())
	}
	defer st.Close()
	r, err := ci.Dispose(ctx, st, *sprint, *repo, *sha, *disposition, *friend, *url)
	if err != nil {
		return refuse(errOut, "ci dispose", err.Error())
	}
	fmt.Fprintln(out, r)
	return r.ExitCode()
}

// runCIStatus is two reads: --sprint prints the sprint's ci card line;
// --repo and --sha print one head's request record and its check rows.
func runCIStatus(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci status")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	sha := fs.String("sha", "", "the head whose request record and check rows are printed")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci status", err.Error())
	}
	*repo = bareRepo(*repo)
	if (*repo == "") != (*sha == "") {
		return refuse(errOut, "ci status", "needs both --repo and --sha for one head, or --sprint")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci status", err.Error())
	}
	defer st.Close()
	if *repo != "" {
		rows, err := ci.ReadRows(ctx, st, *repo, *sha)
		if err != nil {
			return refuse(errOut, "ci status", err.Error())
		}
		return ci.WriteRows(out, rows)
	}
	s, err := ci.ReadStatus(ctx, st, *sprint)
	if err != nil {
		return refuse(errOut, "ci status", err.Error())
	}
	fmt.Fprintln(out, s)
	return 0
}

// runCIParity is #3041 (#2756 10.8.1): over the sprint's window (s:<S>
// opened_at..closed_at), every head of a sprint PR that Actions passed
// (completed workflow_run entries on ev:github) must be green on our record
// ci:<repo>:<sha>. Redis alone, no GitHub poll. It prints PARITY FAIL <head>
// with its remedy per miss, then PARITY n/m sprint=<S>, and exits 1 on a miss
// or under --min heads (the gate is n/n, n >= 20).
func runCIParity(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci parity")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	minHeads := fs.Int("min", 20, "the fewest heads the parity read needs before it reports")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci parity", err.Error())
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci parity", err.Error())
	}
	defer st.Close()
	p, err := ci.ReadParity(ctx, st, ci.ParityRequest{Sprint: *sprint, Min: *minHeads})
	if err != nil {
		return refuse(errOut, "ci parity", err.Error())
	}
	return ci.WriteParity(out, p)
}

// runCIRequest writes the request record for one head and puts it in the
// pool. --checks narrows the repo's declared set; each name must be declared.
// A second request is EXISTS; --again resets the record to attempt 0 and
// re-pools it (RESET), the one way back after a capped FAIL.
func runCIRequest(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci request")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	sha := fs.String("sha", "", "the head requested into ci:pool")
	pr := fs.Int("pr", 0, verbflag.HelpPR)
	url := fs.String("url", "", "the PR url the request names")
	checks := fs.String("checks", "", "the check names the request wants green, comma-separated")
	again := fs.Bool("again", false, "request a head already requested once more")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci request", err.Error())
	}
	*repo = bareRepo(*repo)
	if *repo == "" || *sha == "" {
		return refuse(errOut, "ci request", "needs --repo <r> and --sha <full sha>")
	}
	var wanted []string
	for _, c := range strings.Split(*checks, ",") {
		if c = strings.TrimSpace(c); c != "" {
			wanted = append(wanted, c)
		}
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci request", err.Error())
	}
	defer st.Close()
	r, err := ci.Request(ctx, st, ci.RequestRequest{Repo: *repo, SHA: *sha, PR: *pr, URL: *url, Checks: wanted, Again: *again})
	if err != nil {
		return refuse(errOut, "ci request", err.Error())
	}
	if r.Status == "REFUSED" {
		return refuse(errOut, "ci request", r.Detail)
	}
	fmt.Fprintf(out, "%s %s@%s %s\n", r.Status, *repo, (*sha)[:8], r.Detail)
	return r.ExitCode()
}

// runCIRun is one runner-only pass for a bench: claim one request, stage
// the sha from the bench mirror (--mirror-root, default ~/nova-bench/mirror;
// a repo with no mirror clones the request's --url, never the forge's ssh
// url), run the checks, write the receipts. IDLE (exit 0) when the pool
// has nothing claimable; BLOCKED (exit 1) when the clone failed and the
// request went back to the pool.
func runCIRun(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci run")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	bench := fs.String("bench", "", "the bench running the pass")
	results := fs.String("results", "", "the results directory the run writes")
	scratch := fs.String("scratch", "", "the scratch directory the run clones into")
	mirror := fs.String("mirror-root", "", "the bench mirror root the clone references")
	lease := fs.Duration("lease", 30*time.Minute, "how long the run's lease on the head holds")
	timeout := fs.Duration("check-timeout", 20*time.Minute, "how long one check may run")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci run", err.Error())
	}
	if *bench == "" {
		return refuse(errOut, "ci run", "needs --bench <name>")
	}
	home, _ := os.UserHomeDir()
	if *results == "" && home != "" {
		*results = filepath.Join(home, "nova-bench", "results")
	}
	if *mirror == "" && home != "" {
		*mirror = filepath.Join(home, "nova-bench", "mirror")
	}
	if !filepath.IsAbs(*results) {
		return refuse(errOut, "ci run", "needs --results <absolute dir> (the results root the logs land under)")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci run", err.Error())
	}
	defer st.Close()
	var roleErr *benchrole.Error
	res, err := ci.Run(ctx, st, ci.RunOptions{Bench: *bench, Scratch: *scratch, ResultsRoot: *results,
		MirrorRoot: *mirror, Lease: *lease, Timeout: *timeout, Out: out})
	switch {
	case errors.As(err, &roleErr):
		fmt.Fprintln(errOut, roleErr.Error())
		return roleErr.ExitCode()
	case err == ci.ErrBlocked:
		fmt.Fprintf(errOut, "nova-sprint ci run: %s; the request is back in the pool, fix the bench's mirror\n", res.Blocked)
		return 1
	case err == ci.ErrInfra:
		fmt.Fprintf(errOut, "nova-sprint ci run: %s; the bench killed the check, the request is back in the pool for a rerun; look at what sends TERM on bench %s\n", res.Blocked, *bench)
		return 1
	case err != nil:
		return refuse(errOut, "ci run", err.Error())
	case !res.Claimed:
		fmt.Fprintf(out, "IDLE bench=%s\n", *bench)
	}
	return 0
}

// runCICompare is the one budgeted REST read: our receipts beside GitHub's
// check conclusions for the sha. Exit 0 when every check agrees.
func runCICompare(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci compare")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	sha := fs.String("sha", "", "the head compared against GitHub's check state")
	owner := fs.String("owner", "mas-bandwidth", "the GitHub owner the repo lives under")
	api := fs.String("forge-api", gh.DefaultAPI, "the forge's REST base url")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci compare", err.Error())
	}
	if *repo == "" || *sha == "" {
		return refuse(errOut, "ci compare", "needs --repo <r> and --sha <full sha>")
	}
	tok, err := envGitHubToken()
	if err != nil {
		return refuse(errOut, "ci compare", err.Error())
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci compare", err.Error())
	}
	defer st.Close()
	p, err := ci.Compare(ctx, st, ci.CompareRequest{Repo: *repo, SHA: *sha, Owner: *owner, BaseURL: *api, Token: tok, Redis: st.Client()})
	if err != nil {
		return refuse(errOut, "ci compare", err.Error())
	}
	return ci.WriteCompare(out, p)
}

// runCIGitHub is the ci-github consumer of ev:github (#3597):
//
//	nova-sprint ci github --redis <addr> [--as <seat>] [--once]
//
// Each check_run and workflow_run entry the webhook receiver appended
// becomes one field of ci:<repo>:<sha>:gh, written and acked in one
// ns_ci_github call; other kinds are acked as no-ops. It prints one CIGH line
// per pass that handled an entry (every pass with --once, which drains and
// exits) and runs until SIGINT or SIGTERM otherwise. Exit 0 stopped or
// drained, 1 a pass failed, 2 usage.
//
// --from-runner (card gh-ci-receipts) is the runner as the event source:
//
//	nova-sprint ci github --from-runner --redis <addr> --repo owner/name --sha <head>
//	    --run-id <github.run_id> --event <github.event_name> --workflow <github.workflow>
//	    --conclusion <job.status> [--head-branch <b>] [--base-branch <b>] [--pr <n>]
//	    [--at <rfc3339>] --job <name>=<needs.name.result>...
//
// The ci-ok job calls it at the end of every run (.github/workflows/ci.yml),
// and it writes what the receiver path would have: one ev:github row and
// the ci:<repo>:<sha>:gh record through ns_ci_github, nothing else (never
// pr:<repo>:<n>, which is the lander's; internal/nsprint/webhook/runner.go).
// One CIGH RUNNER line; exit 0 written, 1 the store refused the write (which
// reddens ci-ok: a landing never waits on a receipt that silently did not
// happen), 2 usage.
func runCIGitHub(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci github")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	consumer := fs.String("as", "", verbflag.HelpAs)
	once := fs.Bool("once", false, "drain what is queued and return, no loop")
	fromRunner := fs.Bool("from-runner", false, "write one run's receipt from the CI runner instead of consuming the webhook stream")
	var r webhook.Receipt
	fs.StringVar(&r.Repo, "repo", "", verbflag.HelpRepo)
	fs.StringVar(&r.SHA, "sha", "", "the head sha the run tested (--from-runner)")
	fs.StringVar(&r.RunID, "run-id", "", "the Actions run id (--from-runner)")
	fs.StringVar(&r.Event, "event", "", "the event that started the run: push, pull_request (--from-runner)")
	fs.StringVar(&r.HeadBranch, "head-branch", "", "the run's head branch (--from-runner)")
	fs.StringVar(&r.BaseBranch, "base-branch", "", "the run's base branch (--from-runner)")
	fs.StringVar(&r.PR, "pr", "", verbflag.HelpPR)
	fs.StringVar(&r.Workflow, "workflow", "", "the workflow name (--from-runner)")
	fs.StringVar(&r.Conclusion, "conclusion", "", "the run's conclusion: success, failure, cancelled (--from-runner)")
	fs.StringVar(&r.At, "at", "", "when the run finished, RFC 3339 (--from-runner)")
	var jobs multiFlag
	fs.Var(&jobs, "job", "one job of the run, <name>=<conclusion>; repeatable, since a name may hold commas (--from-runner)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci github", err.Error())
	}
	if *fromRunner {
		if fs.NArg() > 0 {
			return refuse(errOut, "ci github --from-runner", "takes flags only, nothing positional: "+ciRunnerUsage)
		}
		for _, v := range jobs {
			j, err := webhook.ParseJob(v)
			if err != nil {
				return refuse(errOut, "ci github --from-runner", err.Error()+": "+ciRunnerUsage)
			}
			r.Jobs = append(r.Jobs, j)
		}
		if err := r.Validate(); err != nil {
			return refuse(errOut, "ci github --from-runner", err.Error()+": "+ciRunnerUsage)
		}
		addr := landRedisAddr(*redisAddr)
		if addr == "" {
			return refuse(errOut, "ci github --from-runner", "needs --redis <addr> or NOVA_REDIS_ADDR: "+ciRunnerUsage)
		}
		st, err := store.Open(ctx, addr)
		if err != nil {
			return refuse(errOut, "ci github --from-runner", err.Error())
		}
		defer st.Close()
		w, err := webhook.Write(ctx, st.Client(), r)
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint ci github --from-runner: %v; no receipt is in Redis, so land pr would wait forever: fix the store or the bench seat and rerun ci-ok\n", err)
			return 1
		}
		fmt.Fprintln(out, w.Line())
		return 0
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "ci github", "takes --redis <addr> [--as <seat>] [--once], nothing positional")
	}
	if *consumer == "" {
		host, _ := os.Hostname()
		*consumer = host + "-" + strconv.Itoa(os.Getpid())
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci github", err.Error())
	}
	defer st.Close()
	c := &webhook.Consumer{Client: st.Client(), Name: *consumer}
	if *once {
		c.Block = -1
	}
	if err := c.Start(ctx); err != nil {
		fmt.Fprintf(errOut, "nova-sprint ci github: %v\n", err)
		return 1
	}
	for {
		n, err := c.Pass(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint ci github: %v\n", err)
			return 1
		}
		if *once {
			fmt.Fprintln(out, n.Line())
			return 0
		}
		if n != (webhook.Counts{}) {
			fmt.Fprintln(out, n.Line())
		}
	}
}
