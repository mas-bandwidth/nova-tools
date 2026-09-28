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

const receiptUsage = "github receipt --from-runner --redis <addr> --repo owner/name --sha <40hex> --run-id <n> --workflow <name> --conclusion success|failure|cancelled [--pr <n>] [--at <rfc3339>]"

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
		return refuse(stderr, " github", "the verb is receipt: nova-ci "+receiptUsage)
	}
	return cmdReceipt(context.Background(), args[1:], stdout, stderr, getenv)
}

// cmdReceipt is `nova-ci github receipt --from-runner`: the ci-ok job's run
// receipt, one ev:github row (internal/cireceipt). The store is dialled as the
// environment's seat through the keep package internal/nsprint/store, the dial
// nova-sprint used for this step, so the step's nova-secrets wrapper and its
// NOVA_SPRINT_REDIS_USER / NOVA_SPRINT_REDIS_PASSWORD_ENV pair are unchanged.
// One CI RECEIPT line; exit 0 written, 1 the store refused or could not confirm
// the write, 2 usage. Repeat receipts from retries or reruns are acceptable wake
// hints for stream consumers.
func cmdReceipt(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	const where = " github receipt"
	fs := flag.NewFlagSet("github receipt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	fromRunner := fs.Bool("from-runner", false, "")
	addr := fs.String("redis", "", "")
	var r cireceipt.Receipt
	fs.StringVar(&r.Repo, "repo", "", "")
	fs.StringVar(&r.SHA, "sha", "", "")
	fs.StringVar(&r.RunID, "run-id", "", "")
	fs.StringVar(&r.PR, "pr", "", "")
	fs.StringVar(&r.Workflow, "workflow", "", "")
	fs.StringVar(&r.Conclusion, "conclusion", "", "")
	fs.StringVar(&r.At, "at", "", "")
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprintln(stdout, "nova-ci "+receiptUsage)
		return 0
	}
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, where, oneline.Cap(err.Error(), oneline.TailBytes)+": nova-ci "+receiptUsage)
	}
	if fs.NArg() > 0 {
		return refuse(stderr, where, "takes flags only, nothing positional: nova-ci "+receiptUsage)
	}
	if !*fromRunner {
		return refuse(stderr, where, "--from-runner is the one source: the ci-ok job reports its own run")
	}
	if err := r.Validate(); err != nil {
		return refuse(stderr, where, err.Error())
	}
	if strings.TrimSpace(*addr) == "" {
		*addr = strings.TrimSpace(getenv("NOVA_REDIS_ADDR"))
	}
	if *addr == "" {
		return refuse(stderr, where, "needs --redis <addr> or NOVA_REDIS_ADDR")
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
		fmt.Fprintf(stderr, "nova-ci github receipt: %s; receipt write could not be confirmed: fix the store or the bench seat and rerun ci-ok\n", oneline.Err(err))
		return 1
	}
	fmt.Fprintln(stdout, cireceipt.Line(r, id))
	return 0
}
