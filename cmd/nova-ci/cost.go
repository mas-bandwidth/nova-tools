package main

// cost.go is `nova-ci cost`: displaying the table of recent CI runs and per-job
// cost breakdown from the ci:cost stream, or pricing and recording the one COST
// line of a CI run to its log and to the ci:cost stream.
//
// In display mode (no jobs listing on stdin, or when --pr, --head, --limit, or
// --json are passed), it connects to Redis and prints a table or JSON array of
// recent runs: RUN, PR, HEAD, JOBS, TOTAL_S, SPIN_S, and per-job cost breakdown.
//
// In write mode (a run's job listing on stdin with the run receipt flags), it
// reads the forge's job listing for the run on stdin (the body of
// `repos/<owner>/<name>/actions/runs/<id>/jobs`: one JSON object whose jobs array
// holds exactly total_count jobs, whether one complete page or jobs combined
// across every page into one object; raw concatenated pages are refused), prices it
// (internal/cicost) and prints the line on stdout; with --redis it appends the
// same entry to ci:cost first and the line ends in the entry's id. The run's
// identity is the run receipt's: the flags are `nova-ci github receipt`'s, spelt
// the same, so the ci-ok step that writes the receipt writes the cost from the
// same context, and the store is opened the same way (the seat's user and
// password from the environment, never a flag).
//
// The verb makes no call of its own: what fetched the listing is the
// caller's business, and everything on stdin is DATA from a host. A complete
// listing is required; a partial or count-mismatched listing is refused (exit 2)
// before any dial. A write the store will not take is one line on stderr at
// exit 1. If an XADD write succeeds but closing the connection subsequently
// fails, the COST line with its event id is printed on stdout, the close
// failure is reported on stderr, and the verb exits 1 without instructing the
// caller to rerun the write (preventing duplicate entries).

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cicost"
	"github.com/mas-bandwidth/nova-tools/internal/cireceipt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// costUsage is the verb's flag line.
const costUsage = "nova-ci cost [--pr <n>] [--head <sha>] [--limit <n>] [--json] [--repo owner/name] [--redis <addr>] | nova-ci cost --repo owner/name --sha <40hex> --run-id <n> --workflow <name> --conclusion success|failure|cancelled [--pr <n>] [--at <rfc3339>] [--redis <addr>] < jobs.json"

// costListingCap bounds what the verb reads from stdin: a run has tens of
// jobs, each a few kilobytes of JSON with its steps, so a listing past this
// is not a listing.
const costListingCap = 16 << 20

// costWriteTimeout bounds the one XADD or query, so a store that is down or hung
// reddens ci-ok inside its cap instead of holding the job to it.
const costWriteTimeout = 20 * time.Second

// Store is the interface needed for writing and querying ci:cost entries.
type Store interface {
	cicost.Writer
	cicost.Reader
}

// costOpener opens the store at addr and returns the store and its close. It
// is the verb's one seam: production opens the store as this process's seat,
// and the tests hand in a fake that records the entry.
type costOpener func(ctx context.Context, addr string) (Store, func() error, error)

// openCostStore is the production opener: store.Open, the same seat handling
// the receipt's writer uses (NOVA_SPRINT_REDIS_USER and the password env).
func openCostStore(ctx context.Context, addr string) (Store, func() error, error) {
	st, err := store.Open(ctx, addr)
	if err != nil {
		return nil, nil, err
	}
	return st.Client(), st.Close, nil
}

// cmdCost is the verb. Exit 0 with the table, JSON, or COST line on stdout; 1
// when the store would not take the entry, could not be read, or closing it
// failed; 2 for a refusal before any dial.
func cmdCost(args []string, stdin io.Reader, stdout, stderr io.Writer, open costOpener) int {
	return cmdCostWith(args, stdin, stdout, stderr, open, os.Getenv)
}

func cmdCostWith(args []string, stdin io.Reader, stdout, stderr io.Writer, open costOpener, getenv func(string) string) int {
	const where = " cost"
	fs := flag.NewFlagSet("cost", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var r cireceipt.Receipt
	fs.StringVar(&r.Repo, "repo", "", "owner/name")
	fs.StringVar(&r.SHA, "sha", "", "the 40-hex head the run tested")
	var headFlag string
	fs.StringVar(&headFlag, "head", "", "the commit sha to display cost for")
	fs.StringVar(&r.RunID, "run-id", "", "github.run_id")
	fs.StringVar(&r.PR, "pr", "", "the pull request number, or nothing")
	fs.StringVar(&r.Workflow, "workflow", "", "github.workflow")
	fs.StringVar(&r.Conclusion, "conclusion", "", "job.status of ci-ok: success, failure or cancelled")
	fs.StringVar(&r.At, "at", "", "RFC3339; empty is now")
	redisAddr := fs.String("redis", "", "the store to append the entry to, or read from")
	limitFlag := fs.Int("limit", 0, "maximum number of recent runs to display")
	jsonFlag := fs.Bool("json", false, "output cost entries as JSON")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, where, oneline.Cap(err.Error(), oneline.TailBytes)+": "+costUsage)
	}
	if fs.NArg() > 0 {
		return refuse(stderr, where, fmt.Sprintf("unexpected argument %q; the listing is read on stdin: %s", fs.Arg(0), costUsage))
	}

	isWrite := false
	if r.RunID != "" || r.Workflow != "" || r.Conclusion != "" {
		isWrite = true
	} else if headFlag != "" || *limitFlag > 0 || *jsonFlag || (r.PR != "" && r.SHA == "") {
		isWrite = false
	} else {
		hasData := false
		if stdin != nil {
			if f, ok := stdin.(*os.File); ok {
				if fi, err := f.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
					hasData = false
				} else {
					hasData = true
				}
			} else if sr, ok := stdin.(*strings.Reader); ok {
				hasData = sr.Len() > 0
			} else if br, ok := stdin.(*bytes.Buffer); ok {
				hasData = br.Len() > 0
			} else {
				bufr := bufio.NewReader(stdin)
				peek, err := bufr.Peek(1)
				if err == nil && len(peek) > 0 {
					hasData = true
				}
				stdin = bufr
			}
		}
		isWrite = hasData
	}

	if isWrite {
		if err := r.Validate(); err != nil {
			return refuse(stderr, where, oneline.Err(err)+": "+costUsage)
		}
		raw, err := io.ReadAll(io.LimitReader(stdin, costListingCap+1))
		if err != nil {
			return refuse(stderr, where, "stdin: "+oneline.Err(err))
		}
		if len(raw) > costListingCap {
			return refuse(stderr, where, fmt.Sprintf("stdin is over %d bytes; a run's job listing is not", costListingCap))
		}
		if strings.TrimSpace(string(raw)) == "" {
			return refuse(stderr, where, "stdin is empty; it wants the run's job listing (repos/<owner>/<name>/actions/runs/<id>/jobs)")
		}
		totalCount, jobs, err := cicost.ParseJobs(raw)
		if err != nil {
			return refuse(stderr, where, "stdin is not the forge's job listing (repos/<owner>/<name>/actions/runs/<id>/jobs): "+oneline.Err(err))
		}
		if len(jobs) == 0 {
			return refuse(stderr, where, "the listing holds no jobs; a run with none has no cost to write")
		}
		if len(jobs) < totalCount {
			return refuse(stderr, where, fmt.Sprintf("listing is partial (%d jobs read, %d expected); page through the forge's listing, or pass every page", len(jobs), totalCount))
		}
		if len(jobs) > totalCount {
			return refuse(stderr, where, fmt.Sprintf("listing holds %d jobs but total_count is %d", len(jobs), totalCount))
		}
		cost := cicost.FromJobs(jobs)

		ev := ""
		var closeErr error
		if addr := strings.TrimSpace(*redisAddr); addr != "" {
			if open == nil {
				return refuse(stderr, where, "no store opener")
			}
			ctx, cancel := context.WithTimeout(context.Background(), costWriteTimeout)
			defer cancel()
			w, closeStore, err := open(ctx, addr)
			if err != nil {
				return costUnwritten(stderr, "open "+oneline.Field(addr)+": "+oneline.Err(err))
			}
			ev, err = cicost.Write(ctx, w, &r, cost)
			closeErr = closeStore()
			if err != nil {
				return costUnwritten(stderr, oneline.Err(err))
			}
		}
		fmt.Fprintln(stdout, cost.Line(r, ev))
		if closeErr != nil && !errors.Is(closeErr, context.Canceled) {
			fmt.Fprintf(stderr, "nova-ci cost: close: %s\n", oneline.Escape(oneline.Err(closeErr)))
			return 1
		}
		return 0
	}

	// Display mode: query recent runs from Redis.
	addr := strings.TrimSpace(*redisAddr)
	if addr == "" && getenv != nil {
		addr = strings.TrimSpace(getenv("NOVA_REDIS_ADDR"))
	}
	if addr == "" && getenv != nil {
		addr = strings.TrimSpace(getenv("NOVA_CARD_REDIS"))
	}
	if addr == "" {
		return refuse(stderr, where, "needs --redis <addr>, NOVA_REDIS_ADDR, or NOVA_CARD_REDIS")
	}
	if open == nil {
		return refuse(stderr, where, "no store opener")
	}
	ctx, cancel := context.WithTimeout(context.Background(), costWriteTimeout)
	defer cancel()
	st, closeStore, err := open(ctx, addr)
	if err != nil {
		fmt.Fprintf(stderr, "nova-ci cost: open %s: %s\n", oneline.Field(addr), oneline.Err(err))
		return 1
	}
	defer func() {
		_ = closeStore()
	}()

	prNum := 0
	if r.PR != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(r.PR, "#"))
		if err != nil {
			return refuse(stderr, where, fmt.Sprintf("--pr wants the decimal pull request number, got %q", r.PR))
		}
		prNum = n
	}
	head := headFlag
	if head == "" {
		head = r.SHA
	}

	filter := cicost.QueryFilter{
		Repo:  r.Repo,
		PR:    prNum,
		Head:  head,
		Limit: *limitFlag,
	}
	entries, err := cicost.Query(ctx, st, filter)
	if err != nil {
		fmt.Fprintf(stderr, "nova-ci cost: %s\n", oneline.Err(err))
		return 1
	}

	if *jsonFlag {
		if err := cicost.RenderJSON(stdout, entries); err != nil {
			fmt.Fprintf(stderr, "nova-ci cost: json: %s\n", oneline.Err(err))
			return 1
		}
	} else {
		if err := cicost.RenderTable(stdout, entries); err != nil {
			fmt.Fprintf(stderr, "nova-ci cost: table: %s\n", oneline.Err(err))
			return 1
		}
	}
	return 0
}

// costUnwritten is the one line a store that would not take the entry gets:
// the cause, then the state and the next action. Exit 1, the receipt's code
// for the same failure, so ci-ok reddens rather than logging a line that
// pretends the entry landed.
func costUnwritten(stderr io.Writer, cause string) int {
	fmt.Fprintf(stderr, "nova-ci cost: %s; the COST entry was not written: fix the store or the bench seat and rerun ci-ok\n", oneline.Escape(cause))
	return 1
}
