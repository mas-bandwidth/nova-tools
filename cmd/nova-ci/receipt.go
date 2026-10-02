package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cireceipt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// receiptTimeout bounds the one XADD, so a store that is down or hung reddens
// ci-ok inside its two-minute cap instead of holding the job to it.
const receiptTimeout = 20 * time.Second

// cmdGitHub is `nova-ci github <verb>`; its one verb is receipt. getenv is
// os.Getenv in production, the seam a test reads NOVA_REDIS_ADDR through.
func cmdGitHub(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) > 0 {
		verbflag.HelpIfAsked(args[:1], "github")
	}
	if len(args) == 0 || args[0] != "receipt" {
		what := "a verb is needed after github; the one verb is receipt"
		if len(args) > 0 {
			what = fmt.Sprintf("unknown verb %q after github; the one verb is receipt", args[0])
		}
		return refuseRun(stderr, " github", what, "nova-ci github receipt -h")
	}
	return cmdReceipt(context.Background(), args[1:], stdout, stderr, getenv)
}

// cmdReceipt is `nova-ci github receipt --from-runner`: the ci-ok job's run
// receipt, one ev:github row (internal/cireceipt). The store is dialled as the
// environment's seat through the keep package internal/nsprint/store, the dial
// nova-sprint used for this step, so the step's nova-secrets wrapper and its
// NOVA_SPRINT_REDIS_USER / NOVA_SPRINT_REDIS_PASSWORD_ENV pair are unchanged.
// One CI RECEIPT line; exit 0 written, 1 the store refused or could not confirm
// the write, 2 usage. Every refused field and a missing store are named in one
// refusal. --dry-run checks the fields and prints the line with ev=-, and
// dials nothing. Repeat receipts from retries or reruns are acceptable wake
// hints for stream consumers.
func cmdReceipt(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	const where = " github receipt"
	fs := flag.NewFlagSet("github receipt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	fromRunner := fs.Bool("from-runner", false, "the receipt's source: the ci-ok job reporting its own run (required; the only source)")
	addr := fs.String("redis", "", "the store's host:port (default NOVA_REDIS_ADDR); dialled as the seat in NOVA_SPRINT_REDIS_USER")
	dryRun := fs.Bool("dry-run", false, "check the fields and print the line with ev=-; dial and write nothing")
	var r cireceipt.Receipt
	fs.StringVar(&r.Repo, "repo", "", "the repository the run tested, owner/name (github.repository)")
	fs.StringVar(&r.SHA, "sha", "", "the 40-hex head commit the run tested (github.sha)")
	fs.StringVar(&r.RunID, "run-id", "", "the run's decimal id (github.run_id)")
	fs.StringVar(&r.PR, "pr", "", "the pull request number, or nothing for a run that names no PR")
	fs.StringVar(&r.Workflow, "workflow", "", "the workflow's name (github.workflow)")
	fs.StringVar(&r.Conclusion, "conclusion", "", "success, failure or cancelled (job.status)")
	fs.StringVar(&r.At, "at", "", "when the run finished, RFC3339 (default now)")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, where, flagProblem(fs, err))
	}
	var problems []string
	if fs.NArg() > 0 {
		problems = append(problems, fmt.Sprintf("unexpected argument %q (receipt takes flags only)", fs.Arg(0)))
	}
	if !*fromRunner {
		problems = append(problems, "--from-runner is required: the ci-ok job reports its own run, the one source")
	}
	if err := r.Validate(); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(*addr) == "" {
		*addr = strings.TrimSpace(getenv("NOVA_REDIS_ADDR"))
	}
	if *addr == "" && !*dryRun {
		problems = append(problems, "needs --redis <host:port> or NOVA_REDIS_ADDR (or --dry-run, which dials nothing)")
	}
	if len(problems) > 0 {
		return refuse(stderr, where, strings.Join(problems, "; "))
	}
	if *dryRun {
		fmt.Fprintln(stdout, cireceipt.Line(r, "-"))
		fmt.Fprintln(stdout, "CI RECEIPT NOTE --dry-run: the fields are good; nothing was dialled or written; drop --dry-run to write it")
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, receiptTimeout)
	defer cancel()
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(stderr, where, oneline.Err(err))
	}
	defer st.Close()
	id, err := cireceipt.Write(ctx, st.Client(), r)
	if err != nil {
		fmt.Fprintf(stderr, "nova-ci github receipt FAILED: %s; receipt write could not be confirmed: fix the store or the bench seat and rerun ci-ok\n", oneline.Err(err))
		return 1
	}
	fmt.Fprintln(stdout, cireceipt.Line(r, id))
	return 0
}
