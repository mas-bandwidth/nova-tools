// The task card verbs (nova-tools #3778; rowan-new specs/ws-index.md "Tasks
// are cards"): a sprint task is the record task:<id> with one where pointer,
// moved between ws:<stream>:<where> and friend:<f>:cards:<where> only by the
// one move (fn/lua/02_card_move.lua, internal/nsprint/taskcard). Each verb is
// one FCALL (fsck FCALL_RO; ls one ZRANGE) and prints
// one receipt line:
//
//	TASK <verb> id=<id> from=<w> to=<w> ms=<n>
//	TASK <verb> REFUSED id=<id> why=<why> ms=<n>
//
// Exit 0 done, 1 refused (nothing written) or drift found, 2 could not run.
// They are the card form of the task subverbs (isTaskCard): push with --as,
// take always; done, cancel and beat without the one task store's --token;
// block, unblock, front, move, land, ls, fsck and expire always.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// The card form's flags, one grammar (#4352 A): the actor of every receipt
// is the seat (NOVA_FRIEND, else the login user), never a flag; --ids names
// the card; --to the target worker; --stream the stream; --where a table
// column; -h prints the flags.
//
//	task push    --ids <id> [--stream <s>] [--to friend:<f>] [--waiting | --depends-on <c>]
//	             [--kind <k>] [--ref <repo#n>] [--origin <url>] [--title <t>] [--head <sha>] [--pr <n>] [--repo <r>] [--front]
//	             [--issue <file|->] [--route pro|flash|friend] [--base <b>] [--base-sha <sha>] [--paths <p>]
//	task take    [--as friend:<f>] [--ids <id>] [--n <k>]
//	task beat    --ids <id>
//	task done    --ids <id> --evidence <text> [--pr <n>]
//	task land    (--ids <id> | --stream <s>) --sha <merge sha8>
//	task cancel  --ids <id> --why <text>
//	task block   --ids <id> (--on <conditions> | --why <text>)
//	task unblock --ids <id>
//	task front   --ids <id>
//	task move    --ids <id> (--to friend:<f> | --stream <s> | --where <w> [--ok ok|fail] [--why <text>])
//	task expire  [--as friend:<f>,...]
//	task ls      [--stream <s> | --as friend:<f>] [--where <w>]   (bare: every stream, every live column)
//	task fsck    --sprint <S> [--repair]
//
// under a harness (NOVA_FRIEND set) --as, when given, must be the seat.
// every verb also takes --redis <addr> (else NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR) and --sprint <S>
// (the legacy idx sets' sprint; else FRIEND_QUEUE_SPRINT, else the first of sprint:order).
//
// done moves working -> merging when the task names a PR (its pr field or --pr), else -> done.
// take, done, beat and cancel are card work, end, beat and cancel for a friend's consumer copies
// (<primary>~<n>, #3929): take works the friend's ready copies first, then takes friend-queue
// tasks; done of a copy returns it to its primary (--pr <n> --head <sha>: the primary is review).
// land moves merging (or working) -> landed at the merge sha; land --stream moves every
// member of ws:<s>:merging and prints LANDED <id> ref=<repo#n> origin=<url> per member (the
// lander closes those PRs and issues with the CLOSE line). take starts a lease the child
// renews with beat every 60 s; expire moves a working task whose lease lapsed back to ready and unlinks a finished task left in a friend's working set.
// fsck prints one line per drift and exits 1 when there is any; a registered stream with no
// sentinel (#4318) is a NOSENTINEL line, and --repair creates every missing one.
// push --issue fills the card from the issue text (#3911): every KEY: line a card header
// carries (ROUTE WHO KIND TYPE REPO BASE BASE-SHA PATHS TEST DEPENDS-ON DONE-WHEN EST PRIORITY
// SOURCE TASK STREAM ORIGIN) and the text as body; the flags override it. A pro or flash card
// lacking a field a swarm run needs is refused naming it. nova-sprint card render --ids <id>
// prints the harness card from the record (a copy id: its card), --brief --model <m> the friend brief.

// cardAlways are the card subverbs no other task form has.
var cardAlways = map[string]bool{"land": true, "ls": true, "fsck": true, "expire": true}

// cardByToken are the subverbs whose one-task-store form (task.go) carries
// the attempt's --token; the card form never does.
var cardByToken = map[string]bool{"done": true, "cancel": true, "beat": true}

// isTaskCard says whether a task subverb call is the card form: land, ls,
// fsck, expire and take always; push when --as names the pusher (the one task
// store's push reads the seat from the environment alone); done, cancel and
// beat unless --token names an attempt (#4352 A: the two forms were told
// apart by the spelling of the actor flag, which is retired).
func isTaskCard(sub string, args []string) bool {
	if cardAlways[sub] || sub == "take" {
		return true
	}
	has := func(name string) bool {
		for _, a := range args {
			if a == "--"+name || a == "-"+name || strings.HasPrefix(a, "--"+name+"=") || strings.HasPrefix(a, "-"+name+"=") {
				return true
			}
		}
		return false
	}
	if sub == "push" {
		return has("as")
	}
	if cardByToken[sub] {
		return !has("token")
	}
	return cardByID[sub]
}

// cardByID are the subverbs whose card form names one --ids.
var cardByID = map[string]bool{"done": true, "cancel": true, "block": true, "unblock": true, "front": true,
	"move": true, "beat": true}

// cardCmd is one card verb's parsed flags.
type cardCmd struct {
	verb                                         string
	redis, as, sprint, ids, why, stream, to      *string
	kind, ref, origin, title, head, pr, repo, on *string
	dependsOn, evidence, sha, where, ok          *string
	issue, route, base, baseSHA, paths           *string
	n                                            *int
	waiting, front, repair                       *bool
	actor, id                                    string   // the seat, and the one id of --ids
	workers                                      []string // --as as a list (expire)
}

func runTaskCard(ctx context.Context, sub string, args []string, out, errOut io.Writer) int {
	verb := "task " + sub
	fs := taskFlags(verb)
	c := &cardCmd{verb: verb}
	c.redis = fs.String("redis", redisDefault(), verbflag.HelpRedis)
	c.as = fs.String("as", "", verbflag.HelpAs)
	c.sprint = fs.String("sprint", "", verbflag.HelpSprint)
	c.ids = fs.String("ids", "", verbflag.HelpIDs)
	c.why = fs.String("why", "", verbflag.HelpWhy)
	c.stream = fs.String("stream", "", verbflag.HelpStream)
	c.to = fs.String("to", "", verbflag.HelpTo)
	c.kind = fs.String("kind", "", "the card's KIND: work, read, fix or build")
	c.ref = fs.String("ref", "", "the card's ref, <repo>#<n>")
	c.origin = fs.String("origin", "", "the GitHub issue the card came from, a url")
	c.title = fs.String("title", "", "the card's title")
	c.head = fs.String("head", "", "the PR's head sha (done --pr)")
	c.pr = fs.String("pr", "", verbflag.HelpPR)
	c.repo = fs.String("repo", "", verbflag.HelpRepo)
	c.on = fs.String("on", "", "the conditions a blocked card waits on (block)")
	c.dependsOn = fs.String("depends-on", "", "the card's DEPENDS-ON, comma-separated ids the card waits on (push)")
	c.evidence = fs.String("evidence", "", "what proves the work is done: a PR, a url, a line (done)")
	c.sha = fs.String("sha", "", "the merge commit's sha (land)")
	c.where = fs.String("where", "", "a table column: waiting, ready, working, merging, landed, ok, fail (ls, move)")
	c.ok = fs.String("ok", "", "the end a move to ok or fail records: ok or fail (move)")
	c.issue = fs.String("issue", "", "the issue text that fills the card, a file or - for stdin (push)")
	c.route = fs.String("route", "", "the card's ROUTE: pro, flash or friend (push)")
	c.base = fs.String("base", "", "the card's BASE branch (push)")
	c.baseSHA = fs.String("base-sha", "", "the card's BASE-SHA, 40 hex (push)")
	c.paths = fs.String("paths", "", "the card's PATHS, the files it may change (push)")
	c.n = fs.Int("n", 1, verbflag.HelpN)
	c.waiting = fs.Bool("waiting", false, "push the card to waiting instead of ready (push)")
	c.repair = fs.Bool("repair", false, "create every registered stream's missing sentinel card (fsck)")
	c.front = fs.Bool("front", false, "push the card to the front of its column (push)")
	if sub == "ls" {
		fs.Spelled("state", "where") // the column is --where on the card verbs (#4399)
	}
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	c.id = oneID(*c.ids)
	if *c.ids != "" && c.id == "" {
		return refuse(errOut, verb, "--ids wants one id here")
	}
	c.workers = verbflag.List(*c.as)
	// #2929: a seat's verbs act as the seat. Under a harness (NOVA_FRIEND
	// set) --as, when given, must name it; a coordinator shell (none set)
	// is its user.
	c.actor = seatActor()
	// A bench is no seat: take --as bench:<b> works the bench's copies from
	// any seat, as card work --as bench:<b> does (#4399: the help offers it).
	if seat := os.Getenv(seatEnv); seat != "" && *c.as != "" && sub != "ls" && sub != "expire" && !strings.HasPrefix(*c.as, "bench:") && strings.TrimPrefix(*c.as, "friend:") != seat {
		return refuse(errOut, verb, fmt.Sprintf("--as %s is not the seat (%s=%s)", *c.as, seatEnv, seat))
	}
	if *c.sprint == "" {
		*c.sprint = os.Getenv("FRIEND_QUEUE_SPRINT")
	}
	if want := c.missing(sub); want != "" {
		return refuse(errOut, verb, want)
	}
	st, err := store.Open(ctx, taskAddr(*c.redis))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	return c.run(ctx, st, sub, out, errOut)
}

// missing names the first required flag a verb lacks, or "".
func (c *cardCmd) missing(sub string) string {
	need := func(v *string, name string) string {
		if *v == "" {
			return "--" + name + " is required"
		}
		return ""
	}
	var checks []string
	switch sub {
	case "push", "done", "cancel", "block", "unblock", "front", "move", "beat":
		checks = append(checks, need(&c.id, "ids"))
	case "land":
		if (c.id == "") == (*c.stream == "") {
			checks = append(checks, "want exactly one of --ids <id> and --stream <s>")
		}
	case "fsck":
		checks = append(checks, need(c.sprint, "sprint"))
	case "ls":
		if *c.stream != "" && *c.as != "" {
			checks = append(checks, "want at most one of --stream <s> and --as friend:<f>")
		}
	}
	switch sub {
	case "done":
		checks = append(checks, need(c.evidence, "evidence"))
	case "land":
		checks = append(checks, need(c.sha, "sha"))
	case "cancel":
		checks = append(checks, need(c.why, "why"))
	case "block":
		if *c.on == "" && *c.why == "" {
			checks = append(checks, "want --on <conditions> or --why <text>")
		}
	case "move":
		n := 0
		for _, v := range []string{*c.to, *c.stream, *c.where} {
			if v != "" {
				n++
			}
		}
		if n != 1 {
			checks = append(checks, "want exactly one of --to friend:<f>, --stream <s> and --where <w>")
		}
	}
	for _, s := range checks {
		if s != "" {
			return s
		}
	}
	return ""
}

func (c *cardCmd) run(ctx context.Context, st *store.Store, sub string, out, errOut io.Writer) int {
	cl := st.Client()
	start := time.Now()
	ms := func() int64 { return time.Since(start).Milliseconds() }
	refused := func(err error) int {
		if why, ok := taskcard.IsRefused(err); ok {
			_, _ = fmt.Fprintf(out, "TASK %s REFUSED id=%s why=%s ms=%d\n", sub, c.id, quoteField(why), ms())
			return 1
		}
		return refuse(errOut, c.verb, err.Error())
	}
	moved := func(r taskcard.Result, err error) int {
		if err != nil {
			return refused(err)
		}
		from := r.From
		if from == "" {
			from = "-"
		}
		_, _ = fmt.Fprintf(out, "TASK %s id=%s from=%s to=%s ms=%d\n", sub, c.id, from, r.To, ms())
		return 0
	}
	o := taskcard.Opts{By: c.actor, Why: *c.why, Sprint: *c.sprint}
	// the worker of take, ls and expire: --as (friend:<f> or a bare name), else the seat
	asFriend := strings.TrimPrefix(*c.as, "friend:")
	if asFriend == "" {
		asFriend = c.actor
	}
	switch sub {
	case "push":
		spec, err := c.spec()
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		r, err := taskcard.Push(ctx, cl, taskcard.PushRequest{ID: c.id, Stream: *c.stream, Friend: strings.TrimPrefix(*c.to, "friend:"),
			Sprint: *c.sprint, Kind: *c.kind, Ref: *c.ref, Origin: *c.origin, Title: *c.title, Head: *c.head,
			PR: *c.pr, Repo: *c.repo, DependsOn: *c.dependsOn, Front: *c.front, By: c.actor, Why: *c.why,
			Where: map[bool]string{true: "waiting", false: ""}[*c.waiting], Spec: spec})
		if err != nil {
			return refused(err)
		}
		_, _ = fmt.Fprintf(out, "TASK push id=%s from=- to=%s ms=%d\n", c.id, r.Where, ms())
		return 0
	case "take":
		var ids []string
		if c.id != "" {
			ids = []string{c.id}
		}
		// --as bench:<b> is card work for the bench (#4399): its ready
		// copies; a bench has no friend queue, so nothing else is taken.
		if k, err := taskcard.ParseConsumer(*c.as); err == nil && k.Kind == "bench" {
			w, err := taskcard.Work(ctx, cl, k, c.actor, *c.n, false, ids...)
			if why, ok := taskcard.IsRefused(err); ok && strings.HasPrefix(why, "SLOTS ") {
				_, _ = fmt.Fprintf(out, "TASK take REFUSED as=%s why=%s remedy=%s ms=%d\n", k, quoteField(why),
					quoteField("nova-sprint capacity bench --as "+k.Name+" --machine <m> --slots <n>"), ms())
				return 1
			}
			if err != nil {
				return refused(err)
			}
			_, _ = fmt.Fprintf(out, "TASK take n=%d ids=%s ms=%d\n", len(w.IDs), strings.Join(w.IDs, ","), ms())
			return 0
		}
		// task take is card work for a friend harness (#3929): the friend's
		// ready copies first (card work --as friend:<f>), then friend-queue
		// tasks for what is left of --n.
		var got []string
		if c.id == "" || taskcard.IsCopy(c.id) {
			w, err := taskcard.Work(ctx, cl, taskcard.Consumer{Kind: "friend", Name: asFriend}, c.actor, *c.n, false, ids...)
			if err != nil {
				// a friend with no declared slots holds no copies: the friend queue only
				if why, ok := taskcard.IsRefused(err); !ok || c.id != "" || !strings.HasPrefix(why, "SLOTS") {
					return refused(err)
				}
			}
			got = w.IDs
		}
		if len(got) < *c.n && (c.id == "" || !taskcard.IsCopy(c.id)) {
			more, err := taskcard.Take(ctx, cl, asFriend, *c.n-len(got), c.actor, ids...)
			if err != nil {
				return refused(err)
			}
			got = append(got, more...)
		}
		_, _ = fmt.Fprintf(out, "TASK take n=%d ids=%s ms=%d\n", len(got), strings.Join(got, ","), ms())
		return 0
	case "beat":
		beat := taskcard.Beat
		if taskcard.IsCopy(c.id) { // card beat --as friend:<f> (#3929)
			beat = func(ctx context.Context, cl redis.Cmdable, id, as string) (int64, error) {
				return taskcard.BeatCopies(ctx, cl, taskcard.Consumer{Kind: "friend", Name: as}, id)
			}
		}
		// the worker that took it (--as, else the seat), as take names it
		until, err := beat(ctx, cl, c.id, asFriend)
		if err != nil {
			return refused(err)
		}
		_, _ = fmt.Fprintf(out, "TASK beat id=%s lease_until=%d ms=%d\n", c.id, until, ms())
		return 0
	case "done":
		if taskcard.IsCopy(c.id) {
			// task done of a copy is card end (#3929): the copy returns to its
			// primary (ok; with --pr <n> --head <sha> the primary moves to review).
			r := taskcard.EndRequest{IDs: []string{c.id}, OK: true, PR: *c.pr, Head: *c.head, By: c.actor,
				Fields: []string{"evidence", *c.evidence}}
			if *c.pr != "" {
				r.Repo = cl.HGet(ctx, taskcard.Key(c.id), "repo").Val()
			}
			e, err := taskcard.End(ctx, cl, r)
			if err != nil {
				return refused(err)
			}
			_, _ = fmt.Fprintf(out, "TASK done id=%s from=working to=ok primary=%s primary_to=%s ms=%d\n", c.id, e[0].Primary, e[0].To, ms())
			return 0
		}
		return moved(taskcard.Done(ctx, cl, c.id, asFriend, *c.evidence, *c.pr))
	case "land":
		if *c.stream == "" {
			return moved(taskcard.Land(ctx, cl, c.id, c.actor, *c.sha, *c.why))
		}
		// the stream lander's step: every merging member, one call; the
		// lines name what the lander closes with the CLOSE line
		r, err := taskcard.LandStream(ctx, cl, *c.stream, *c.sha, c.actor, *c.why)
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		for _, m := range r.Landed {
			_, _ = fmt.Fprintf(out, "LANDED %s ref=%s origin=%s\n", m.ID, quoteField(m.Ref), quoteField(m.Origin))
		}
		ids := make([]string, 0, len(r.Refused))
		for id := range r.Refused {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			_, _ = fmt.Fprintf(out, "REFUSED %s why=%s\n", id, quoteField(r.Refused[id]))
		}
		_, _ = fmt.Fprintf(out, "TASK land stream=%s sha=%s n=%d refused=%d ms=%d\n", quoteField(*c.stream), *c.sha, len(r.Landed), len(r.Refused), ms())
		if len(r.Refused) > 0 {
			return 1
		}
		return 0
	case "cancel":
		if taskcard.IsCopy(c.id) { // a copy given back is card cancel (#3929)
			e, err := taskcard.CancelCards(ctx, cl, c.actor, *c.why, c.id)
			if err != nil {
				return refused(err)
			}
			_, _ = fmt.Fprintf(out, "TASK cancel id=%s from=working to=fail primary_to=%s ms=%d\n", c.id, e[0].To, ms())
			return 0
		}
		return moved(taskcard.Cancel(ctx, cl, c.id, c.actor, *c.why))
	case "block":
		why := *c.why
		if why == "" {
			why = "on " + *c.on
		}
		o.Why = why
		o.Fields = []string{"blocked_on", *c.on, "blocked_reason", why}
		return moved(taskcard.Move(ctx, cl, c.id, "waiting", o))
	case "unblock":
		if o.Why == "" {
			o.Why = "unblock"
		}
		return moved(taskcard.Move(ctx, cl, c.id, "ready", o))
	case "front", "move":
		return c.replace(ctx, st, sub, o, moved)
	case "expire":
		friends := make([]string, 0, len(c.workers))
		for _, w := range c.workers {
			friends = append(friends, strings.TrimPrefix(w, "friend:"))
		}
		r, err := taskcard.Reap(ctx, cl, c.actor, friends...)
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		_, _ = fmt.Fprintf(out, "TASK expire n=%d ids=%s unlinked=%d unlinked_ids=%s ms=%d\n", len(r.Expired), strings.Join(r.Expired, ","),
			len(r.Unlinked), strings.Join(r.Unlinked, ","), ms())
		return 0
	case "ls":
		if *c.where != "" && (*c.stream != "" || *c.as != "") {
			var ids []string
			var err error
			if *c.stream != "" {
				ids, err = taskcard.Ls(ctx, cl, *c.stream, *c.where)
			} else {
				ids, err = taskcard.LsFriend(ctx, cl, asFriend, *c.where)
			}
			if err != nil {
				return refuse(errOut, c.verb, err.Error())
			}
			for _, id := range ids {
				_, _ = fmt.Fprintln(out, id)
			}
			_, _ = fmt.Fprintf(out, "TASK ls n=%d where=%s ms=%d\n", len(ids), *c.where, ms())
			return 0
		}
		return c.lsWide(ctx, cl, asFriend, out, errOut, ms)
	case "fsck":
		if *c.repair {
			// the one repair: every registered stream has its sentinel (#4318)
			sw, err := taskcard.SentinelsWalk(ctx, cl, true)
			if err != nil {
				return refuse(errOut, c.verb, err.Error())
			}
			for _, m := range sw.Missing {
				_, _ = fmt.Fprintf(out, "SENTINEL created stream=%s id=%s\n", quoteField(m.Stream), m.ID)
			}
			_, _ = fmt.Fprintf(out, "TASK fsck repair sentinels=%d created=%d\n", len(sw.Missing), sw.Created)
		}
		r, err := taskcard.Fsck(ctx, cl, *c.sprint)
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		for _, l := range r.Lines {
			_, _ = fmt.Fprintln(out, "DRIFT "+l)
		}
		var cells []string
		for _, w := range taskcard.Wheres {
			cells = append(cells, fmt.Sprintf("%s=%d", w, r.Counts[w]))
		}
		_, _ = fmt.Fprintf(out, "TASK fsck sprint=%s tasks=%d null=%d %s unplaced=%d drift=%d ms=%d\n",
			r.Sprint, r.Tasks, r.Null, strings.Join(cells, " "), r.Unplaced, r.Drift, ms())
		if r.Drift > 0 {
			return 1
		}
		return 0
	}
	return refuse(errOut, c.verb, "unknown card subverb "+sub)
}

// spec is push's card content (#3911): the --issue text through the one
// parser, then the flags over it; nil when the push names neither.
func (c *cardCmd) spec() (*taskcard.Spec, error) {
	if *c.issue == "" && *c.route == "" && *c.base == "" && *c.baseSHA == "" && *c.paths == "" {
		return nil, nil
	}
	var s taskcard.Spec
	if *c.issue != "" {
		var text []byte
		var err error
		if *c.issue == "-" {
			text, err = io.ReadAll(os.Stdin)
		} else {
			text, err = os.ReadFile(*c.issue)
		}
		if err != nil {
			return nil, err
		}
		s = taskcard.ParseIssue(string(text))
		if len(s.Retired) > 0 {
			return nil, errors.New(s.Retired[0])
		}
	}
	for _, kv := range []struct {
		v   string
		dst *string
	}{{*c.route, &s.Route}, {*c.base, &s.Base}, {*c.baseSHA, &s.BaseSHA}, {*c.paths, &s.Paths}, {*c.repo, &s.Repo}} {
		if kv.v != "" {
			*kv.dst = kv.v
		}
	}
	return &s, nil
}

// replace is front and move: the one move to the task's own where (front,
// --to friend:<f>, --stream <s>) or to --where <w>.
func (c *cardCmd) replace(ctx context.Context, st *store.Store, sub string, o taskcard.Opts, moved func(taskcard.Result, error) int) int {
	cl := st.Client()
	to := *c.where
	if to == "" {
		w, err := cl.HGet(ctx, taskcard.Key(c.id), "where").Result()
		if err != nil {
			return moved(taskcard.Result{}, &taskcard.Refused{Why: "NOTASK task:" + c.id + ""})
		}
		to = w
	}
	switch {
	case sub == "front":
		o.Front = true
		o.Fields = []string{"order", "0"}
		if o.Why == "" {
			o.Why = "front"
		}
	case *c.to != "":
		o.Friend, o.SetFriend = strings.TrimPrefix(*c.to, "friend:"), true
	case *c.stream != "":
		o.Stream, o.SetStream = *c.stream, true
	default:
		o.OK = *c.ok
	}
	return moved(taskcard.Move(ctx, cl, c.id, to, o))
}

// counts prints a tally: placed as where=n in the card order (null first),
// skipped as reason:n by name, comma-separated; "-" when empty.
func counts(m map[string]int, places bool) string {
	var cells []string
	if places {
		for _, w := range append([]string{""}, taskcard.Wheres...) {
			name := w
			if name == "" {
				name = "null"
			}
			cells = append(cells, name+"="+strconv.Itoa(m[w]))
		}
		return strings.Join(cells, " ")
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cells = append(cells, k+":"+strconv.Itoa(m[k]))
	}
	if len(cells) == 0 {
		return "-"
	}
	return strings.Join(cells, ",")
}

// taskLeaseDuty is the reconciler's lease sweep over task cards (#3778, Glenn
// 08:15 AM: "rowan working=65 while two children were alive"): every pass
// moves each working task whose lease lapsed back to ready (ns_tcard_expire),
// and unlinks from friend:<f>:cards:working every id whose record is not
// that friend's working task (#3892: emma's 11 finished cards), so a
// friend's working set holds only tasks a live lease holds.
type taskLeaseDuty struct{ st *store.Store }

func (d *taskLeaseDuty) Run(ctx context.Context, _ *reconcile.Lease) (reconcile.Counts, error) {
	r, err := taskcard.Reap(ctx, d.st.Client(), "reconciler")
	return reconcile.Counts{Expired: len(r.Expired), Reaped: len(r.Unlinked)}, err
}

func init() {
	registerReconcileDuty("task-lease", func(st *store.Store) (reconcileDuty, error) {
		return &taskLeaseDuty{st: st}, nil
	})
}

// lsWide is task ls without the one column or the one stream (#4399: the
// cold session typed `task ls`, `task ls --stream <s>` and was refused
// twice): every live column (--where, else waiting through landed) of the
// stream, the friend (--as), or every stream in ws:order, one pipelined
// round, one line per card:
//
//	<id> stream=<s> where=<w>
//	TASK ls n=<n> streams=<k> where=<w|live> ms=<ms>
func (c *cardCmd) lsWide(ctx context.Context, cl *redis.Client, asFriend string, out, errOut io.Writer, ms func() int64) int {
	wheres := ws.Stream
	label := "live"
	if *c.where != "" {
		wheres, label = []string{*c.where}, *c.where
	}
	var streams []string
	switch {
	case *c.stream != "":
		streams = verbflag.List(*c.stream)
	case *c.as != "":
	default:
		var err error
		if streams, err = cl.ZRange(ctx, "ws:order", 0, -1).Result(); err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
	}
	type key struct{ stream, where string }
	var keys []key
	pipe := cl.Pipeline()
	var cmds []*redis.StringSliceCmd
	if *c.as != "" {
		for _, w := range wheres {
			keys = append(keys, key{"friend:" + asFriend, w})
			cmds = append(cmds, pipe.ZRange(ctx, taskcard.FriendKey(asFriend, w), 0, -1))
		}
	}
	for _, s := range streams {
		for _, w := range wheres {
			keys = append(keys, key{s, w})
			cmds = append(cmds, pipe.ZRange(ctx, taskcard.StreamKey(s, w), 0, -1))
		}
	}
	if len(cmds) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return refuse(errOut, c.verb, err.Error())
		}
	}
	n := 0
	for i, cmd := range cmds {
		for _, id := range cmd.Val() {
			n++
			_, _ = fmt.Fprintf(out, "%s stream=%s where=%s\n", id, quoteField(keys[i].stream), keys[i].where)
		}
	}
	_, _ = fmt.Fprintf(out, "TASK ls n=%d streams=%d where=%s ms=%d\n", n, len(streams), label, ms())
	return 0
}
