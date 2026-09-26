package jev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/redis/go-redis/v9"
)

// Usage is the ledger's subverbs on nova-sprint help.
const Usage = "sync [--n <moves>] | ask [--n <rows>] [--key-env <VAR>] [--base-url <url>] | " +
	"report [--type <t>] [--version <v>] | outcome --type <t> --subject <s> --outcome <o> --why <text> [--by <who>]"

// env is what the verb reads from outside: TypeSafe Jev and the process
// environment. Main uses the real ones; a test passes its own (a unit test
// never calls the real Jev).
type env struct {
	newAsker func(baseURL, keyEnv string) (Asker, error)
	getenv   func(string) string
}

var realEnv = env{
	newAsker: func(baseURL, keyEnv string) (Asker, error) { return decide.New(baseURL, keyEnv) },
	getenv:   os.Getenv,
}

// Main is `nova-sprint jev sync|ask|report|outcome` (nova-tools #4316):
//
//	sync     read ws:log after jev:cursor: every decision point a move is,
//	         as a row, and every outcome a move settles, joined to its row
//	ask      take rows off jev:pending and ask Jev each, one typed call per
//	         row, in shadow: the answer goes on the row, never on the card
//	report   per type, source (rules, jev) and prompt version: answers,
//	         outcomes, agreement, the coordinator's overrides, cost, ms
//	outcome  join an outcome by hand (the coordinator's confirm or override)
//
// Every refusal prints one JEV REFUSED line with its remedy. Exit 0 done, 1
// refused (Redis, Jev, no row), 2 usage.
func Main(ctx context.Context, args []string, out, errOut io.Writer) int {
	return run(ctx, args, out, errOut, realEnv)
}

func run(ctx context.Context, args []string, out, errOut io.Writer, e env) int {
	if len(args) == 0 {
		return usage(errOut, "", "want "+Usage)
	}
	switch args[0] {
	case "sync":
		return runSync(ctx, args[1:], out, errOut, e)
	case "ask":
		return runAsk(ctx, args[1:], out, errOut, e)
	case "report":
		return runReport(ctx, args[1:], out, errOut, e)
	case "outcome":
		return runOutcome(ctx, args[1:], out, errOut, e)
	}
	return usage(errOut, "", "unknown subverb "+args[0]+"; want "+Usage)
}

func usage(errOut io.Writer, sub, what string) int {
	verb := "jev"
	if sub != "" {
		verb += " " + sub
	}
	fmt.Fprintf(errOut, "nova-sprint %s: %s; run: nova-sprint help\n", verb, oneline.Escape(what))
	return 2
}

// refused is every refusal's one line.
func refused(out io.Writer, sub, why, remedy string) int {
	fmt.Fprintf(out, "JEV REFUSED %s why=%s remedy=%s\n", sub, oneline.Field(why), oneline.Field(remedy))
	return 1
}

// addr is --redis, else NOVA_SPRINT_REDIS, else NOVA_REDIS_ADDR.
func (e env) addr(flagVal string) string {
	for _, v := range []string{flagVal, e.getenv("NOVA_SPRINT_REDIS"), e.getenv("NOVA_REDIS_ADDR")} {
		if v != "" {
			return v
		}
	}
	return ""
}

func (e env) open(ctx context.Context, sub, flagVal string, out io.Writer) (*store.Store, int) {
	a := e.addr(flagVal)
	if a == "" {
		return nil, refused(out, sub, "no Redis named", "pass --redis <addr> or set NOVA_SPRINT_REDIS")
	}
	st, err := store.Open(ctx, a)
	if err != nil {
		return nil, refused(out, sub, "no Redis at "+a+": "+err.Error(), "start Redis or pass --redis <addr>")
	}
	return st, 0
}

func runSync(ctx context.Context, args []string, out, errOut io.Writer, e env) int {
	fs := verbflag.New("jev sync")
	redisAddr := fs.String("redis", seatcred.Addr(), "")
	n := fs.Int64("n", 1000, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *n <= 0 {
		return usage(errOut, "sync", "want sync [--n <moves, 1 or more>] [--redis <addr>]")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, code := e.open(ctx, "sync", *redisAddr, out)
	if code != 0 {
		return code
	}
	defer st.Close()
	res, err := Sync(ctx, st.Client(), *n)
	if err != nil {
		return refused(out, "sync", err.Error(), "check Redis and rerun; the cursor did not move")
	}
	if res.Gap != "" {
		fmt.Fprintf(out, "JEV SYNC GAP between=%s why=ws:log was trimmed past jev:cursor remedy=run jev sync more often than ws:log turns over\n", res.Gap)
	}
	for _, m := range res.Moved {
		fmt.Fprintf(out, "JEV SYNC MOVED review %s why=the record moved on to a later review before sync read it remedy=run jev sync more often; no row is made from another move's record\n", oneline.Field(m))
	}
	fmt.Fprintf(out, "JEV SYNC moves=%d decisions=%d outcomes=%d moved=%d from=%s cursor=%s\n", res.Events, res.Decisions,
		res.Outcomes, len(res.Moved), dash(res.From), dash(res.Cursor))
	return 0
}

func runAsk(ctx context.Context, args []string, out, errOut io.Writer, e env) int {
	fs := verbflag.New("jev ask")
	redisAddr := fs.String("redis", seatcred.Addr(), "")
	n := fs.Int64("n", 16, "")
	keyEnv := fs.String("key-env", decide.DefaultKeyEnv, "")
	baseURL := fs.String("base-url", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *n <= 0 || *n > 256 {
		return usage(errOut, "ask", "want ask [--n <rows, 1-256>] [--key-env <VAR>] [--base-url <url>] [--redis <addr>]")
	}
	asker, err := e.newAsker(*baseURL, *keyEnv)
	if err != nil {
		return refused(out, "ask", err.Error(), "export "+*keyEnv+" (the TypeSafe Jev key) and rerun")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	st, code := e.open(ctx, "ask", *redisAddr, out)
	if code != 0 {
		return code
	}
	defer st.Close()
	return ask(ctx, st.Client(), asker, *n, out)
}

// ask is the verb after its flags and its Jev: one line per row, then the
// count.
func ask(ctx context.Context, c redis.Cmdable, a Asker, n int64, out io.Writer) int {
	res, err := AskPending(ctx, c, a, n)
	answered, failed := 0, 0
	for _, r := range res {
		t, s, _ := strings.Cut(r.Member, " ")
		if r.Err != nil {
			failed++
			remedy := "none; the row is not asked again"
			if r.Again {
				remedy = "back on jev:pending; the next jev ask asks it again"
			}
			refused(out, "ask "+t+" "+s, r.Err.Error(), remedy)
			continue
		}
		answered++
		a := r.Answer
		fmt.Fprintf(out, "JEV ASKED %s %s answer=%s conf=%.2f version=%s ms=%d cost=%s\n", t, s, a.Answer, a.Conf,
			a.Version, a.MS, a.Cost())
	}
	if err != nil {
		return refused(out, "ask", err.Error(), "check Redis and rerun; rows left in "+KeyAsking+" go back with SMOVE to "+KeyPending)
	}
	var pending, asking *redis.IntCmd
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	_, _ = c.Pipelined(rctx, func(p redis.Pipeliner) error {
		pending, asking = p.SCard(rctx, KeyPending), p.SCard(rctx, KeyAsking)
		return nil
	})
	fmt.Fprintf(out, "JEV ASK asked=%d answered=%d failed=%d pending=%d asking=%d\n", len(res), answered, failed,
		pending.Val(), asking.Val())
	if failed > 0 {
		return 1
	}
	return 0
}

func runReport(ctx context.Context, args []string, out, errOut io.Writer, e env) int {
	fs := verbflag.New("jev report")
	redisAddr := fs.String("redis", seatcred.Addr(), "")
	typ := fs.String("type", "", "")
	version := fs.String("version", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return usage(errOut, "report", "want report [--type <t>] [--version <v>] [--redis <addr>]")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, code := e.open(ctx, "report", *redisAddr, out)
	if code != 0 {
		return code
	}
	defer st.Close()
	var types []string
	if *typ != "" {
		types = []string{*typ}
	}
	rows, pending, err := LoadRows(ctx, st.Client(), types)
	if err != nil {
		return refused(out, "report", err.Error(), "check Redis and rerun")
	}
	Print(out, rows, pending, *typ, *version)
	return 0
}

// Print is the report: a header, then one line per type, source and
// version.
func Print(out io.Writer, rows []Row, pending int64, typ, version string) {
	lines := Filter(Summarize(rows), typ, version)
	fmt.Fprintf(out, "JEV REPORT rows=%d pending=%d lines=%d\n", len(rows), pending, len(lines))
	for _, l := range lines {
		fmt.Fprintln(out, l.String())
	}
}

func runOutcome(ctx context.Context, args []string, out, errOut io.Writer, e env) int {
	fs := verbflag.New("jev outcome")
	redisAddr := fs.String("redis", seatcred.Addr(), "")
	typ := fs.String("type", "", "")
	subject := fs.String("subject", "", "")
	outcome := fs.String("outcome", "", "")
	why := fs.String("why", "", "")
	by := fs.String("by", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *typ == "" || *subject == "" || *outcome == "" ||
		strings.TrimSpace(*why) == "" {
		return usage(errOut, "outcome", "want outcome --type <t> --subject <s> --outcome <o> --why <text> [--by <who>] [--redis <addr>]")
	}
	if *by == "" {
		*by = e.getenv("NOVA_FRIEND")
	}
	if *by == "" {
		*by = "nova-sprint"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, code := e.open(ctx, "outcome", *redisAddr, out)
	if code != 0 {
		return code
	}
	defer st.Close()
	o := Outcome{Type: *typ, Subject: *subject, Outcome: *outcome, By: *by, Why: *why}
	if err := Join(ctx, st.Client(), o); err != nil {
		remedy := "check Redis and rerun"
		if errors.Is(err, ErrNoRow) {
			remedy = "jev report --type " + *typ + " lists the rows; an outcome joins a decision, it never makes one"
		}
		return refused(out, "outcome "+*typ+" "+*subject, err.Error(), remedy)
	}
	fmt.Fprintf(out, "JEV OUTCOME %s %s outcome=%s by=%s\n", *typ, oneline.Field(*subject), oneline.Field(*outcome),
		oneline.Field(*by))
	return 0
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
