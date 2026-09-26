// The table moves (nova-tools #3929; rowan-new specs/table-moves.md): the
// verbs that move cards across the stream, host and friend tables, each one
// FCALL of the TM functions in internal/nsprint/fn/lua/02_card_move.lua
// (internal/nsprint/taskcard moves.go), batch by default, one receipt line,
// exit 0 done (a repeat card end with the same evidence is ALREADY, exit 0),
// 1 refused (nothing written) or drift found, 2 usage or Redis, 3 FENCED (a
// stale token or lease), 4 CONFLICT (other evidence on an ended copy).
// A consumer is bench:<b> or friend:<f>; no verb branches on which.
//
//	card deal  --to <consumer> [--n <k>] [--stream <s>] [--ids @file|a,b]
//	card work  --as <consumer> (--fill | --n <k> | --ids @file|a,b)
//	card end   (--id <copy> | --ids @file|a,b) (--ok [--pr <repo>#<n> --head <sha> --repo <checkout at head> [--test <finding test>]] [--done-already <sha>] |
//	           --score <N>/10 [--gates <g>] [--finding <text>] [--reader <who>] | --fail <why>) [--token <t>] [result flags]
//	           (a --fail moves the primary to review, #4072; --exit <rc> is its evidence; see review.go)
//	card assign --id <primary> --to <consumer> [--revoke] [--why <why>]
//	card beat  --as <consumer> (--id <copy> | --ids ...)
//	card land  --stream <s> --sha <merge sha>
//	card cancel (--id <id> | --ids ...) --why <why> [--each]   (--each: every id on its own, #4309)
//	card expire [--as <consumer>]...
//	card fsck  [--repair]                 (no --sprint: the copy links; --sprint is the sprint card fsck)
//	card table [--as <consumer>]...       the consumer cells: ready working done ok fail ok%
//	card consumers [--add <consumer>] [--rm <consumer>]
//	card render --id <copy>               the copy's card file (the bench runs it)
//	card session --as bench:<b> [--wrapper <path>]   the bench harness's session start (#3998):
//	                                      card work --fill, then one detached nova-card copy per copy
//	(a friend's session pulls, ends and beats its copies through friend pull | done | beat,
//	friend_copies.go, #4233: the same moves, the person's brief instead of the wrapper)
//
// every verb also takes --redis <addr> (else NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR)
// and --actor <a> (else NOVA_FRIEND, else nova-sprint).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/brief"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/launch"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/redis/go-redis/v9"
)

// cardMoveVerbs are the table move subverbs no other card form has.
var cardMoveVerbs = map[string]bool{"deal": true, "work": true, "land": true, "cancel": true, "expire": true,
	"table": true, "consumers": true, "render": true, "assign": true, "session": true}

// isCardMove says whether a card call is a table move. end and beat are
// the move form with --id, --ids or --as (the bench attempt form names a
// label and --token); fsck is with no --sprint.
func isCardMove(sub string, args []string) bool {
	if cardMoveVerbs[sub] {
		return true
	}
	has := func(names ...string) bool {
		for _, a := range args {
			n := strings.TrimLeft(a, "-")
			if i := strings.IndexByte(n, '='); i >= 0 {
				n = n[:i]
			}
			if !strings.HasPrefix(a, "-") {
				continue
			}
			for _, want := range names {
				if n == want {
					return true
				}
			}
		}
		return false
	}
	switch sub {
	case "end", "beat":
		return has("id", "ids", "as")
	case "fsck":
		return !has("sprint")
	}
	return false
}

// moveCmd is one table move's flags.
type moveCmd struct {
	redis, actor, to, as, stream, ids, id, why, sha *string
	pr, head, doneAlready, score, gates, finding    *string
	reader, fail, add, rm, token, wrapper           *string
	checkout, findingTest                           *string
	// parent is the caller's context: the spec gate at card end --ok --pr
	// runs under it, not under the store's 30 s
	parent                                context.Context
	n                                     *int
	fill, ok, repair, revoke, brief, each *bool
	result                                map[string]*string
	consumers                             multiFlag
}

// resultFlags are card end's result fields (written onto the primary).
var resultFlags = []string{"line1", "line2", "check", "paths", "branch", "commit", "base", "base-sha", "model", "route",
	"wall", "evidence", "tier", "key", "exit"}

func runCardMove(ctx context.Context, sub string, args []string, out, errOut io.Writer) int {
	verb := "card " + sub
	fs := verbflag.New(verb) // -h prints the move's usage and flags, exit 2 (#3254)
	m := &moveCmd{result: map[string]*string{}}
	m.redis = fs.String("redis", redisDefault(), "")
	m.actor = fs.String("actor", "", "")
	m.to = fs.String("to", "", "")
	m.stream = fs.String("stream", "", "")
	m.ids = fs.String("ids", "", "")
	m.id = fs.String("id", "", "")
	m.why = fs.String("why", "", "")
	m.sha = fs.String("sha", "", "")
	m.pr = fs.String("pr", "", "")
	m.head = fs.String("head", "", "")
	m.doneAlready = fs.String("done-already", "", "")
	m.score = fs.String("score", "", "")
	m.gates = fs.String("gates", "", "")
	m.finding = fs.String("finding", "", "")
	m.reader = fs.String("reader", "", "")
	m.fail = fs.String("fail", "", "")
	m.add = fs.String("add", "", "")
	m.rm = fs.String("rm", "", "")
	m.token = fs.String("token", "", "")
	m.wrapper = fs.String("wrapper", "", "")
	m.checkout = fs.String("repo", "", "")
	m.findingTest = fs.String("test", "", "")
	m.revoke = fs.Bool("revoke", false, "")
	m.n = fs.Int("n", 0, "")
	m.fill = fs.Bool("fill", false, "")
	m.ok = fs.Bool("ok", false, "")
	m.repair = fs.Bool("repair", false, "")
	m.brief = fs.Bool("brief", false, "")
	m.each = fs.Bool("each", false, "")
	if sub == "table" || sub == "expire" {
		fs.Var(&m.consumers, "as", "")
		m.as = new(string)
	} else {
		m.as = fs.String("as", "", "")
	}
	for _, f := range resultFlags {
		m.result[f] = fs.String(f, "", "")
	}
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if *m.actor == "" {
		*m.actor = os.Getenv(seatEnv)
	}
	if seat := os.Getenv(seatEnv); seat != "" && *m.actor != seat {
		return refuse(errOut, verb, fmt.Sprintf("--actor %s is not the seat (%s=%s)", *m.actor, seatEnv, seat))
	}
	if *m.actor == "" {
		*m.actor = "nova-sprint"
	}
	ids, err := m.idList()
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if why := m.usage(sub, ids); why != "" {
		return refuse(errOut, verb, why)
	}
	m.parent = ctx
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, taskAddr(*m.redis))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	return m.run(ctx, st.Client(), sub, ids, out, errOut)
}

// idList is --id and --ids (a,b,c or @file of ids, one per line or space
// separated), in order.
func (m *moveCmd) idList() ([]string, error) {
	var ids []string
	if *m.id != "" {
		ids = append(ids, *m.id)
	}
	v := *m.ids
	if strings.HasPrefix(v, "@") {
		b, err := os.ReadFile(v[1:])
		if err != nil {
			return nil, err
		}
		ids = append(ids, strings.Fields(string(b))...)
	} else if v != "" {
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// usage names the first thing a verb's flags lack, or "".
func (m *moveCmd) usage(sub string, ids []string) string {
	switch sub {
	case "deal":
		if *m.to == "" {
			return "deal wants --to bench:<b>|friend:<f> and --n <k> (optionally --stream <s>) or --ids"
		}
		if len(ids) == 0 && *m.n < 1 {
			return "deal wants --n <k> or --ids"
		}
	case "work":
		if *m.as == "" {
			return "work wants --as bench:<b>|friend:<f> and --fill, --n <k> or --ids"
		}
		if !*m.fill && *m.n < 1 && len(ids) == 0 {
			return "work wants --fill, --n <k> or --ids"
		}
	case "end":
		if len(ids) == 0 {
			return "end wants --id <copy> or --ids"
		}
		n := 0
		for _, on := range []bool{*m.ok, *m.fail != "", *m.score != ""} {
			if on {
				n++
			}
		}
		if n != 1 && !(*m.ok && *m.score != "") {
			return "end wants exactly one of --ok, --score <N>/10 and --fail <why>"
		}
		if *m.pr != "" && *m.head == "" {
			return "end --pr wants --head <sha>"
		}
		if *m.pr != "" && len(ids) != 1 {
			return "end --pr ends one copy: --id <copy>"
		}
	case "beat":
		if *m.as == "" || len(ids) == 0 {
			return "beat wants --as <consumer> and --id <copy> or --ids"
		}
	case "land":
		if *m.stream == "" || *m.sha == "" {
			return "land wants --stream <s> --sha <merge sha>"
		}
	case "cancel":
		if len(ids) == 0 || *m.why == "" {
			return "cancel wants --id <id> or --ids, and --why <why>"
		}
	case "render":
		if len(ids) != 1 {
			return "render wants --id <copy>|<primary> [--brief [--model <m>]]"
		}
	case "assign":
		if len(ids) != 1 || *m.to == "" {
			return "assign wants --id <primary> --to bench:<b>|friend:<f> [--revoke]"
		}
	case "consumers":
		if *m.add != "" && *m.rm != "" {
			return "consumers wants at most one of --add and --rm"
		}
	case "session":
		if !strings.HasPrefix(*m.as, "bench:") {
			return "session wants --as bench:<b> (a friend takes its copies through friend pull)"
		}
	}
	return ""
}

func consumerArg(s string) (taskcard.Consumer, error) { return taskcard.ParseConsumer(s) }

// moveRefusedCode is a move refusal's exit: 3 FENCED, 4 CONFLICT, else 1.
func moveRefusedCode(why string) int {
	switch {
	case strings.HasPrefix(why, "FENCED"):
		return 3
	case strings.HasPrefix(why, "CONFLICT"):
		return 4
	}
	return 1
}

// printEnded prints card end's lines, one per copy: ENDED with the
// primary's move, or ALREADY for the same end again.
func printEnded(out io.Writer, e []taskcard.Ended) {
	for _, x := range e {
		if x.To == "already" {
			fmt.Fprintf(out, "ALREADY %s primary=%s ended=%s\n", x.Copy, x.Primary, x.From)
			continue
		}
		fmt.Fprintf(out, "ENDED %s primary=%s from=%s to=%s next=%s\n", x.Copy, x.Primary, x.From, x.To, dash(x.Next))
	}
}

func (m *moveCmd) run(ctx context.Context, c *redis.Client, sub string, ids []string, out, errOut io.Writer) int {
	start := time.Now()
	ms := func() int64 { return time.Since(start).Milliseconds() }
	verb := strings.ToUpper("card " + sub)
	refused := func(err error, what string) int {
		if why, ok := taskcard.IsRefused(err); ok {
			fmt.Fprintf(out, "%s REFUSED %s why=%s ms=%d\n", verb, what, quoteField(why), ms())
			return moveRefusedCode(why)
		}
		return refuse(errOut, "card "+sub, err.Error())
	}
	switch sub {
	case "deal":
		to, err := consumerArg(*m.to)
		if err != nil {
			return refuse(errOut, "card deal", err.Error())
		}
		d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: to, N: *m.n, Stream: *m.stream, IDs: ids, By: *m.actor})
		if err != nil {
			return refused(err, "to="+to.String())
		}
		copies := make([]string, len(d))
		for i, x := range d {
			copies[i] = x.Copy
		}
		fmt.Fprintf(out, "CARD DEAL to=%s n=%d copies=%s ms=%d\n", to, len(d), dash(strings.Join(copies, ",")), ms())
		return 0
	case "work":
		as, err := consumerArg(*m.as)
		if err != nil {
			return refuse(errOut, "card work", err.Error())
		}
		w, err := taskcard.Work(ctx, c, as, *m.actor, *m.n, *m.fill, ids...)
		if err != nil {
			return refused(err, "as="+as.String())
		}
		fmt.Fprintf(out, "CARD WORK as=%s n=%d free=%d ids=%s ms=%d\n", as, len(w.IDs), w.Free, dash(strings.Join(w.IDs, ",")), ms())
		return 0
	case "end":
		r := taskcard.EndRequest{IDs: ids, OK: *m.fail == "", Why: *m.fail, Head: *m.head, DoneAlready: *m.doneAlready,
			Gates: *m.gates, Finding: *m.finding, Reader: *m.reader, Token: *m.token, By: *m.actor}
		if *m.why != "" && r.Why == "" {
			r.Why = *m.why
		}
		if *m.pr != "" {
			repo, n, ok := strings.Cut(*m.pr, "#")
			if !ok || repo == "" || n == "" {
				return refuse(errOut, "card end", "--pr wants <repo>#<n>")
			}
			r.Repo, r.PR = repo, n
		}
		if *m.score != "" {
			s, err := strconv.Atoi(strings.TrimSuffix(*m.score, "/10"))
			if err != nil || s < 1 || s > 10 {
				return refuse(errOut, "card end", "--score wants N/10, N 1-10")
			}
			r.Score = s
		}
		if r.OK && r.PR != "" {
			// the spec gate (#4313) at this door as at friend done: a code
			// copy's ok with a PR runs it in --repo, the checkout at --head,
			// before the end (nova-tools#4401 read, DOORS)
			rec, err := c.HGetAll(ctx, taskcard.Key(ids[0])).Result()
			if err != nil {
				return refuse(errOut, "card end", err.Error())
			}
			if why := gateCopyEnd(m.parent, c, ids[0], rec, *m.checkout, *m.findingTest, *m.head, out); why != "" {
				fmt.Fprintf(out, "CARD END REFUSED ids=%s why=%s ms=%d\n", ids[0], quoteField(why), ms())
				return 1
			}
			// the end's own round trips get their 30 s after the gate
			after, cancelAfter := context.WithTimeout(m.parent, 30*time.Second)
			defer cancelAfter()
			ctx = after
		}
		for _, f := range resultFlags {
			if v := *m.result[f]; v != "" {
				r.Fields = append(r.Fields, strings.ReplaceAll(f, "-", "_"), v)
			}
		}
		e, err := taskcard.End(ctx, c, r)
		if err != nil {
			return refused(err, "ids="+strings.Join(ids, ","))
		}
		printEnded(out, e)
		fmt.Fprintf(out, "CARD END n=%d ms=%d\n", len(e), ms())
		return 0
	case "assign":
		to, err := consumerArg(*m.to)
		if err != nil {
			return refuse(errOut, "card assign", err.Error())
		}
		a, err := taskcard.Assign(ctx, c, to, ids[0], *m.revoke, *m.actor, *m.why)
		if err != nil {
			return refused(err, "id="+ids[0])
		}
		fmt.Fprintf(out, "CARD ASSIGN id=%s to=%s copy=%s revoked=%s ms=%d\n", a.Primary, to, a.Copy, dash(a.Revoked), ms())
		return 0
	case "beat":
		as, err := consumerArg(*m.as)
		if err != nil {
			return refuse(errOut, "card beat", err.Error())
		}
		until, err := taskcard.BeatCopies(ctx, c, as, ids...)
		if err != nil {
			return refused(err, "as="+as.String())
		}
		fmt.Fprintf(out, "CARD BEAT as=%s n=%d lease_until=%d ms=%d\n", as, len(ids), until, ms())
		return 0
	case "land":
		l, err := taskcard.LandStream(ctx, c, *m.stream, *m.sha, *m.actor, *m.why)
		if err != nil {
			return refuse(errOut, "card land", err.Error())
		}
		for _, x := range l.Landed {
			fmt.Fprintf(out, "LANDED %s ref=%s origin=%s\n", x.ID, quoteField(x.Ref), quoteField(x.Origin))
		}
		keys := make([]string, 0, len(l.Refused))
		for id := range l.Refused {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		for _, id := range keys {
			fmt.Fprintf(out, "REFUSED %s why=%s\n", id, quoteField(l.Refused[id]))
		}
		fmt.Fprintf(out, "CARD LAND stream=%s sha=%s n=%d refused=%d ms=%d\n", quoteField(*m.stream), *m.sha, len(l.Landed), len(l.Refused), ms())
		if len(l.Refused) > 0 {
			return 1
		}
		return 0
	case "cancel":
		// --each (#4309): every id on its own, a receipt per id, exit 1 when
		// any refused; without it the batch is all or nothing.
		if *m.each {
			r, err := taskcard.CancelEach(ctx, c, *m.actor, *m.why, ids...)
			if err != nil {
				return refused(err, "ids="+strings.Join(ids, ","))
			}
			ok, no := 0, 0
			for _, x := range r {
				if x.Why != "" {
					no++
					fmt.Fprintf(out, "REFUSED %s why=%s\n", x.ID, quoteField(x.Why))
					continue
				}
				ok++
				fmt.Fprintf(out, "CANCELLED %s to=%s\n", x.ID, x.To)
			}
			fmt.Fprintf(out, "CARD CANCEL n=%d refused=%d ms=%d\n", ok, no, ms())
			if no > 0 {
				return 1
			}
			return 0
		}
		e, err := taskcard.CancelCards(ctx, c, *m.actor, *m.why, ids...)
		if err != nil {
			return refused(err, "ids="+strings.Join(ids, ","))
		}
		for _, x := range e {
			fmt.Fprintf(out, "CANCELLED %s to=%s\n", x.Copy, x.To)
		}
		fmt.Fprintf(out, "CARD CANCEL n=%d ms=%d\n", len(e), ms())
		return 0
	case "expire":
		var ks []taskcard.Consumer
		for _, s := range m.consumers {
			k, err := consumerArg(s)
			if err != nil {
				return refuse(errOut, "card expire", err.Error())
			}
			ks = append(ks, k)
		}
		e, err := taskcard.ExpireCopies(ctx, c, *m.actor, ks...)
		if err != nil {
			return refuse(errOut, "card expire", err.Error())
		}
		n, refused := 0, 0
		for _, x := range e {
			if x.Why != "" {
				// a lapsed copy the move could not end: it stays, with its why
				refused++
				fmt.Fprintf(out, "EXPIRE %s REFUSED why=%s\n", x.Copy, quoteField(x.Why))
				continue
			}
			n++
			fmt.Fprintf(out, "EXPIRED %s to=%s\n", x.Copy, x.To)
		}
		fmt.Fprintf(out, "CARD EXPIRE n=%d refused=%d ms=%d\n", n, refused, ms())
		if refused > 0 {
			return 1
		}
		return 0
	case "fsck":
		r, err := taskcard.FsckMoves(ctx, c, *m.repair)
		if err != nil {
			return refuse(errOut, "card fsck", err.Error())
		}
		for _, l := range r.Lines {
			fmt.Fprintln(out, "DRIFT "+l)
		}
		fmt.Fprintf(out, "CARD FSCK consumers=%d live=%d retired=%d primaries=%d drift=%d fixed=%d ms=%d\n",
			r.Consumers, r.Live, r.Retired, r.Primaries, r.Drift, r.Fixed, ms())
		if r.Drift > r.Fixed {
			return 1
		}
		return 0
	case "table":
		var ks []taskcard.Consumer
		for _, s := range m.consumers {
			k, err := consumerArg(s)
			if err != nil {
				return refuse(errOut, "card table", err.Error())
			}
			ks = append(ks, k)
		}
		if len(ks) == 0 {
			var err error
			if ks, err = taskcard.Roster(ctx, c); err != nil {
				return refuse(errOut, "card table", err.Error())
			}
		}
		rows, err := taskcard.ReadCells(ctx, c, ks)
		if err != nil {
			return refuse(errOut, "card table", err.Error())
		}
		for _, r := range rows {
			fmt.Fprintln(out, "CELLS "+r.Line())
		}
		fmt.Fprintf(out, "CARD TABLE consumers=%d ms=%d\n", len(rows), ms())
		return 0
	case "consumers":
		for _, s := range []struct {
			v  string
			on bool
		}{{*m.add, true}, {*m.rm, false}} {
			if s.v == "" {
				continue
			}
			k, err := consumerArg(s.v)
			if err != nil {
				return refuse(errOut, "card consumers", err.Error())
			}
			if err := taskcard.Enroll(ctx, c, k, s.on); err != nil {
				return refuse(errOut, "card consumers", err.Error())
			}
		}
		ks, err := taskcard.Roster(ctx, c)
		if err != nil {
			return refuse(errOut, "card consumers", err.Error())
		}
		names := make([]string, len(ks))
		for i, k := range ks {
			names[i] = k.String()
		}
		fmt.Fprintf(out, "CARD CONSUMERS n=%d consumers=%s ms=%d\n", len(ks), dash(strings.Join(names, ",")), ms())
		return 0
	case "session":
		as, err := consumerArg(*m.as)
		if err != nil {
			return refuse(errOut, "card session", err.Error())
		}
		wrapper, err := wrapperPath(*m.wrapper)
		if err != nil {
			return refuse(errOut, "card session", err.Error())
		}
		s, err := card.OpenCopySession(ctx, c, as.Name, *m.actor, func(l card.CopyLaunch) error {
			_, err := launch.LaunchCopy(wrapper, launch.CopyLine{Copy: l.Copy, Token: l.Token}, launch.DefaultBudget)
			return err
		})
		if err != nil {
			return refuse(errOut, "card session", err.Error())
		}
		fmt.Fprintf(out, "CARD %s ms=%d\n", s.Line(as.Name), ms())
		if len(s.GivenBack) > 0 {
			return 1
		}
		return 0
	case "render":
		// A copy renders its card (RenderCopy: the leg's DO and end call); a
		// primary renders the harness card its work copy would get (#3911:
		// RenderHeader of the record, nothing generated) or, with --brief,
		// the friend brief from the same record. stdout is exactly the
		// card; the one receipt line goes to stderr.
		rec, err := c.HGetAll(ctx, taskcard.Key(ids[0])).Result()
		if err != nil {
			// A store that did not answer is not a missing card: the Redis
			// error is the line, exit 2, never NOTASK.
			return refuse(errOut, "card render", err.Error())
		}
		if len(rec) == 0 {
			if taskcard.IsCopy(ids[0]) {
				return refused(&taskcard.Refused{Why: "NOCOPY task:" + ids[0]}, "id="+ids[0])
			}
			return refused(&taskcard.Refused{Why: "NOTASK task:" + ids[0]}, "id="+ids[0])
		}
		var body []byte
		switch {
		case taskcard.IsCopy(ids[0]):
			body, err = card.RenderCopy(card.CopyCardFrom(ids[0], rec))
		case *m.brief:
			body, err = brief.RenderCard(ids[0], rec, *m.result["model"])
		default:
			body, err = taskcard.RenderHeader(ids[0], rec)
		}
		if why, ok := taskcard.IsRefused(err); ok {
			return refused(&taskcard.Refused{Why: why}, "id="+ids[0])
		}
		if err != nil {
			return refused(&taskcard.Refused{Why: err.Error()}, "id="+ids[0])
		}
		if _, err := out.Write(body); err != nil {
			// The bench runs what it reads here: a short card is a failure.
			return refuse(errOut, "card render", "write card: "+err.Error())
		}
		fmt.Fprintf(errOut, "RENDERED card id=%s brief=%t bytes=%d ms=%d\n", ids[0], *m.brief, len(body), ms())
		return 0
	}
	return refuse(errOut, "card "+sub, "unknown table move")
}
