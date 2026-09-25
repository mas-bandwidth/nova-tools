// The ci verb (#2756 4.8 and section 10, nova-tools #2936) registers itself
// through the S0 registry, so it never edits main.go. cut, rerun and dispose
// are one guarded Redis Function call each; show and status only read.
//
// request, run and compare are our own CI (#3597, #3349): a head requested
// into ci:pool, a bench's runner-only pass over it, and the one budgeted
// parity read against GitHub. status --repo --sha prints that record's rows.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "ci",
		Summary: "request, run, status and compare of our own ci; cut, show, rerun, dispose and parity of ci cards",
		Run:     runCI,
	})
}

const ciUsage = "want request, run, status, compare, cut, show, rerun, dispose or parity"

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
	default:
		return refuse(errOut, "ci", "unknown subverb "+args[0]+"; "+ciUsage)
	}
}

// splitPositional lets a positional sha sit before or after the flags.
func splitPositional(args []string) (flags []string, pos []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return flags, pos
}

func runCICut(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci cut")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	repo := fs.String("repo", "", "")
	pr := fs.Int("pr", 0, "")
	sha := fs.String("sha", "", "")
	base := fs.String("base", "", "")
	baseRef := fs.String("base-ref", "", "")
	leg := fs.String("leg", "go", "")
	paths := fs.String("paths", "", "")
	actor := fs.String("actor", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci cut", err.Error())
	}
	if *sha == "" || *base == "" || *baseRef == "" {
		return refuse(errOut, "ci cut", "needs --sha, --base and --base-ref: the head and base tip read by REST for the PR")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci cut", err.Error())
	}
	defer st.Close()
	r, err := ci.Cut(ctx, st, ci.CutRequest{Sprint: *sprint, Repo: *repo, PR: *pr, Head: *sha,
		Base: *base, BaseRef: *baseRef, Leg: *leg, Paths: *paths, Actor: *actor})
	if err != nil {
		return refuse(errOut, "ci cut", err.Error())
	}
	fmt.Fprintf(out, "%s %s\n", r, ci.Label(*pr, *sha, *base))
	return r.ExitCode()
}

func runCIShow(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags, pos := splitPositional(args)
	fs := taskFlags("ci show")
	redisAddr := fs.String("redis", "", "")
	repo := fs.String("repo", "nova-tools", "")
	_ = fs.String("sprint", "", "")
	if err := fs.Parse(flags); err != nil {
		return refuse(errOut, "ci show", err.Error())
	}
	if len(pos) != 1 {
		return refuse(errOut, "ci show", "needs one head sha")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci show", err.Error())
	}
	defer st.Close()
	rec, err := ci.Read(ctx, st, *repo, pos[0])
	if err != nil {
		return refuse(errOut, "ci show", err.Error())
	}
	return ci.WriteShow(out, rec)
}

func runCIRerun(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags, pos := splitPositional(args)
	fs := taskFlags("ci rerun")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	repo := fs.String("repo", "nova-tools", "")
	pr := fs.Int("pr", 0, "")
	base := fs.String("base", "", "")
	reason := fs.String("reason", "", "")
	actor := fs.String("actor", "", "")
	if err := fs.Parse(flags); err != nil {
		return refuse(errOut, "ci rerun", err.Error())
	}
	if len(pos) != 1 || len(pos[0]) != 40 {
		return refuse(errOut, "ci rerun", "needs one full head sha")
	}
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
		label = ci.Label(*pr, pos[0], *base)
	} else {
		rec, err := ci.Read(ctx, st, *repo, pos[0])
		if err != nil {
			return refuse(errOut, "ci rerun", err.Error())
		}
		s, l, ok := strings.Cut(rec.Fields["card"], "/")
		if !ok || s != *sprint {
			return refuse(errOut, "ci rerun", "no ci card for that head in sprint "+*sprint+"; pass --pr and --base")
		}
		label = l
	}
	r, err := ci.Rerun(ctx, st, *sprint, label, *actor, *reason)
	if err != nil {
		return refuse(errOut, "ci rerun", err.Error())
	}
	fmt.Fprintf(out, "%s %s\n", r, label)
	return r.ExitCode()
}

func runCIDispose(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags, pos := splitPositional(args)
	fs := taskFlags("ci dispose")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	repo := fs.String("repo", "nova-tools", "")
	disposition := fs.String("disposition", "", "")
	friend := fs.String("as", "", "")
	url := fs.String("url", "", "")
	if err := fs.Parse(flags); err != nil {
		return refuse(errOut, "ci dispose", err.Error())
	}
	if len(pos) != 1 {
		return refuse(errOut, "ci dispose", "needs one head sha")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci dispose", err.Error())
	}
	defer st.Close()
	r, err := ci.Dispose(ctx, st, *sprint, *repo, pos[0], *disposition, *friend, *url)
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
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	repo := fs.String("repo", "", "")
	sha := fs.String("sha", "", "")
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

// runCIParity is #3041 (#2756 10.8.1): every head of a sprint PR that
// Actions passed (completed workflow_run entries on ev:github) must be OK on
// ci:<repo>:<head>:<gid>. It prints PARITY FAIL <head> per miss, then PARITY n/m,
// and exits 1 on a miss or under --min heads (the gate is n/n, n >= 20).
func runCIParity(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ci parity")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	minHeads := fs.Int("min", 20, "")
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
	redisAddr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	sha := fs.String("sha", "", "")
	pr := fs.Int("pr", 0, "")
	url := fs.String("url", "", "")
	checks := fs.String("checks", "", "")
	again := fs.Bool("again", false, "")
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
	redisAddr := fs.String("redis", "", "")
	bench := fs.String("bench", "", "")
	results := fs.String("results", "", "")
	scratch := fs.String("scratch", "", "")
	mirror := fs.String("mirror-root", "", "")
	lease := fs.Duration("lease", 30*time.Minute, "")
	timeout := fs.Duration("check-timeout", 20*time.Minute, "")
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
	res, err := ci.Run(ctx, st, ci.RunOptions{Bench: *bench, Scratch: *scratch, ResultsRoot: *results,
		MirrorRoot: *mirror, Lease: *lease, Timeout: *timeout, Out: out})
	switch {
	case err == ci.ErrBlocked:
		fmt.Fprintf(errOut, "nova-sprint ci run: %s; the request is back in the pool, fix the bench's mirror\n", res.Blocked)
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
	redisAddr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	sha := fs.String("sha", "", "")
	owner := fs.String("owner", "mas-bandwidth", "")
	api := fs.String("forge-api", "https://api.github.com", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ci compare", err.Error())
	}
	if *repo == "" || *sha == "" {
		return refuse(errOut, "ci compare", "needs --repo <r> and --sha <full sha>")
	}
	tok := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	if tok == "" {
		tok = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ci compare", err.Error())
	}
	defer st.Close()
	p, err := ci.Compare(ctx, st, ci.CompareRequest{Repo: *repo, SHA: *sha, Owner: *owner, BaseURL: *api, Token: tok})
	if err != nil {
		return refuse(errOut, "ci compare", err.Error())
	}
	return ci.WriteCompare(out, p)
}
