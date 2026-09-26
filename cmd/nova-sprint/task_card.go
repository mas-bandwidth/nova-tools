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
// They are the card form of the task subverbs: push with --actor, take with
// --actor and no --as;
// done, cancel, block, unblock, front, move and beat with --actor and --id and
// none of the one task store's --token, --as or --to (nor the batch forms'
// --ids, --stream, --set); land, ls, fsck and expire always.
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

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

const taskCardUsage = `nova-sprint task: the task card verbs (#3778), one Redis Function call each

usage:
  nova-sprint task push    --actor <a> --id <id> [--stream <s>] [--friend|--to <f>] [--waiting | --depends-on <c>]
                           [--kind <k>] [--ref <repo#n>] [--origin <url>] [--title <t>] [--head <sha>] [--pr <n>] [--repo <r>] [--front]
                           [--issue <file|->] [--route pro|flash|friend] [--base <b>] [--base-sha <sha>] [--paths <p>]
  nova-sprint task take    --actor <f> [--id <id>] [--n <k>]
  nova-sprint task beat    --actor <f> --id <id>
  nova-sprint task done    --actor <a> --id <id> --evidence <text> [--pr <n>]
  nova-sprint task land    --actor <a> (--id <id> | --stream <s>) --sha <merge sha8>
  nova-sprint task cancel  --actor <a> --id <id> --why <text>
  nova-sprint task block   --actor <a> --id <id> (--on <conditions> | --why <text>)
  nova-sprint task unblock --actor <a> --id <id>
  nova-sprint task front   --actor <a> --id <id>
  nova-sprint task move    --actor <a> --id <id> (--to-friend <f> | --to-stream <s> | --to-where <w> [--ok ok|fail] [--why <text>])
  nova-sprint task expire  --actor <a> [--friend <f>]...
  nova-sprint task ls      (--stream <s> | --friend <f>) --where <w>
  nova-sprint task fsck    --sprint <S> [--repair]
under a harness (NOVA_FRIEND set) --actor must be the seat.
every verb also takes --redis <addr> (else NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR) and --sprint <S>
(the legacy idx sets' sprint; else FRIEND_QUEUE_SPRINT, else the first of sprint:order).

done moves working -> merging when the task names a PR (its pr field or --pr), else -> done.
take, done, beat and cancel are card work, end, beat and cancel for a friend's consumer copies
(<primary>~<n>, #3929): take works the friend's ready copies first, then takes friend-queue
tasks; done of a copy returns it to its primary (--pr <n> --head <sha>: the primary is review).
land moves merging (or working) -> landed at the merge sha; land --stream moves every
member of ws:<s>:merging and prints LANDED <id> ref=<repo#n> origin=<url> per member (the
lander closes those PRs and issues with the CLOSE line). take starts a lease the child
renews with beat every 60 s; expire moves a working task whose lease lapsed back to ready and unlinks a finished task left in a friend's working set.
fsck prints one line per drift and exits 1 when there is any; a registered stream with no
sentinel (#4318) is a NOSENTINEL line, and --repair creates every missing one.
push --issue fills the card from the issue text (#3911): every KEY: line a card header
carries (ROUTE WHO KIND TYPE REPO BASE base-sha PATHS TEST DEPENDS-ON DONE-WHEN EST PRIORITY
SOURCE TASK STREAM ORIGIN) and the text as body; the flags override it. A pro or flash card
lacking a field a swarm run needs is refused naming it. nova-sprint card render --id <id>
prints the harness card from the record (a copy id: its card), --brief --model <m> the friend brief.
`

// cardAlways are the card subverbs no other task form has.
var cardAlways = map[string]bool{"land": true, "ls": true, "fsck": true, "expire": true}

// cardByID are the subverbs whose card form names one --id with --actor.
var cardByID = map[string]bool{"done": true, "cancel": true, "block": true, "unblock": true, "front": true,
	"move": true, "beat": true}

// isTaskCard says whether a task subverb call is the card form.
func isTaskCard(sub string, args []string) bool {
	if cardAlways[sub] {
		return true
	}
	flags := map[string]bool{}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if i := strings.Index(name, "="); i >= 0 {
			name = name[:i]
		}
		flags[name] = true
	}
	if !flags["actor"] {
		return false
	}
	if sub == "push" {
		return true
	}
	if sub == "take" {
		return !flags["as"] // the one task store's take is --as <seat>
	}
	if !cardByID[sub] || !flags["id"] {
		return false
	}
	for _, legacy := range []string{"token", "as", "to", "ids", "stream", "set"} {
		if flags[legacy] {
			return false
		}
	}
	return true
}

// cardCmd is one card verb's parsed flags.
type cardCmd struct {
	verb                                            string
	redis, actor, sprint, id, why, stream, friend   *string
	kind, ref, origin, title, head, pr, repo, on    *string
	evidence, sha, where, toFriend, toStream, toWhr *string
	ok                                              *string
	issue, route, base, baseSHA, paths              *string
	n                                               *int
	waiting, front, help, repair                    *bool
	friends                                         multiFlag
}

func runTaskCard(ctx context.Context, sub string, args []string, out, errOut io.Writer) int {
	verb := "task " + sub
	fs := taskFlags(verb)
	c := &cardCmd{verb: verb}
	c.redis = fs.String("redis", redisDefault(), "")
	c.actor = fs.String("actor", "", "")
	c.sprint = fs.String("sprint", "", "")
	c.id = fs.String("id", "", "")
	c.why = fs.String("why", "", "")
	c.stream = fs.String("stream", "", "")
	c.kind = fs.String("kind", "", "")
	c.ref = fs.String("ref", "", "")
	c.origin = fs.String("origin", "", "")
	c.title = fs.String("title", "", "")
	c.head = fs.String("head", "", "")
	c.pr = fs.String("pr", "", "")
	c.repo = fs.String("repo", "", "")
	c.on = fs.String("on", "", "")
	fs.StringVar(c.on, "depends-on", "", "")
	c.evidence = fs.String("evidence", "", "")
	c.sha = fs.String("sha", "", "")
	c.where = fs.String("where", "", "")
	c.toFriend = fs.String("to-friend", "", "")
	c.toStream = fs.String("to-stream", "", "")
	c.toWhr = fs.String("to-where", "", "")
	c.ok = fs.String("ok", "", "")
	c.issue = fs.String("issue", "", "")
	c.route = fs.String("route", "", "")
	c.base = fs.String("base", "", "")
	c.baseSHA = fs.String("base-sha", "", "")
	c.paths = fs.String("paths", "", "")
	c.n = fs.Int("n", 1, "")
	c.waiting = fs.Bool("waiting", false, "")
	c.repair = fs.Bool("repair", false, "")
	c.front = fs.Bool("front", false, "")
	c.help = fs.Bool("help", false, "")
	fs.BoolVar(c.help, "h", false, "")
	c.friend = new(string)
	if sub == "expire" {
		fs.Var(&c.friends, "friend", "")
	} else {
		fs.StringVar(c.friend, "friend", "", "")
		if sub == "push" {
			fs.StringVar(c.friend, "to", "", "") // friend-queue push's spelling
		}
	}
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error()+"; see nova-sprint task "+sub+" --help")
	}
	if *c.help {
		_, _ = io.WriteString(out, taskCardUsage)
		return 0
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if *c.sprint == "" {
		*c.sprint = os.Getenv("FRIEND_QUEUE_SPRINT")
	}
	// #2929: a seat's verbs act as the seat. Under a harness (NOVA_FRIEND
	// set) --actor must name it; a coordinator shell (none set) names itself.
	if seat := os.Getenv(seatEnv); seat != "" && *c.actor != "" && *c.actor != seat {
		return refuse(errOut, verb, fmt.Sprintf("--actor %s is not the seat (%s=%s)", *c.actor, seatEnv, seat))
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
		checks = append(checks, need(c.actor, "actor"), need(c.id, "id"))
	case "land":
		checks = append(checks, need(c.actor, "actor"))
		if (*c.id == "") == (*c.stream == "") {
			checks = append(checks, "want exactly one of --id <id> and --stream <s>")
		}
	case "take", "expire":
		checks = append(checks, need(c.actor, "actor"))
	case "fsck":
		checks = append(checks, need(c.sprint, "sprint"))
	case "ls":
		checks = append(checks, need(c.where, "where"))
		if (*c.stream == "") == (*c.friend == "") {
			checks = append(checks, "want exactly one of --stream <s> and --friend <f>")
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
		for _, v := range []string{*c.toFriend, *c.toStream, *c.toWhr} {
			if v != "" {
				n++
			}
		}
		if n != 1 {
			checks = append(checks, "want exactly one of --to-friend <f>, --to-stream <s> and --to-where <w>")
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
			_, _ = fmt.Fprintf(out, "TASK %s REFUSED id=%s why=%s ms=%d\n", sub, *c.id, quoteField(why), ms())
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
		_, _ = fmt.Fprintf(out, "TASK %s id=%s from=%s to=%s ms=%d\n", sub, *c.id, from, r.To, ms())
		return 0
	}
	o := taskcard.Opts{By: *c.actor, Why: *c.why, Sprint: *c.sprint}
	switch sub {
	case "push":
		spec, err := c.spec()
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		// A push that carries a card is one invariant (#4396): refused
		// before any write, one REFUSED card-lint line per rule.
		if spec != nil {
			repo := firstOf(*c.repo, spec.Repo)
			if rs := cardhdr.LintOneInvariant(cardhdr.Card{Text: taskLintText(*c.kind, spec), Files: card.FilesAt(repo, spec.BaseSHA)}); rs != nil {
				_, _ = fmt.Fprintf(out, "TASK push REFUSED id=%s why=%s ms=%d\n", *c.id, quoteField("card-lint "+rs.Rules()), ms())
				_, _ = fmt.Fprint(out, card.LintLines(*c.id, rs))
				return 1
			}
		}
		r, err := taskcard.Push(ctx, cl, taskcard.PushRequest{ID: *c.id, Stream: *c.stream, Friend: *c.friend,
			Sprint: *c.sprint, Kind: *c.kind, Ref: *c.ref, Origin: *c.origin, Title: *c.title, Head: *c.head,
			PR: *c.pr, Repo: *c.repo, DependsOn: *c.on, Front: *c.front, By: *c.actor, Why: *c.why,
			Where: map[bool]string{true: "waiting", false: ""}[*c.waiting], Spec: spec})
		if err != nil {
			return refused(err)
		}
		_, _ = fmt.Fprintf(out, "TASK push id=%s from=- to=%s ms=%d\n", *c.id, r.Where, ms())
		return 0
	case "take":
		var ids []string
		if *c.id != "" {
			ids = []string{*c.id}
		}
		// task take is card work for a friend harness (#3929): the friend's
		// ready copies first (card work --as friend:<f>), then friend-queue
		// tasks for what is left of --n.
		var got []string
		if *c.id == "" || taskcard.IsCopy(*c.id) {
			w, err := taskcard.Work(ctx, cl, taskcard.Consumer{Kind: "friend", Name: *c.actor}, *c.actor, *c.n, false, ids...)
			if err != nil {
				// a friend with no declared slots holds no copies: the friend queue only
				if why, ok := taskcard.IsRefused(err); !ok || *c.id != "" || !strings.HasPrefix(why, "SLOTS") {
					return refused(err)
				}
			}
			got = w.IDs
		}
		if len(got) < *c.n && (*c.id == "" || !taskcard.IsCopy(*c.id)) {
			more, err := taskcard.Take(ctx, cl, *c.actor, *c.n-len(got), *c.actor, ids...)
			if err != nil {
				return refused(err)
			}
			got = append(got, more...)
		}
		_, _ = fmt.Fprintf(out, "TASK take n=%d ids=%s ms=%d\n", len(got), strings.Join(got, ","), ms())
		return 0
	case "beat":
		beat := taskcard.Beat
		if taskcard.IsCopy(*c.id) { // card beat --as friend:<f> (#3929)
			beat = func(ctx context.Context, cl redis.Cmdable, id, as string) (int64, error) {
				return taskcard.BeatCopies(ctx, cl, taskcard.Consumer{Kind: "friend", Name: as}, id)
			}
		}
		until, err := beat(ctx, cl, *c.id, *c.actor)
		if err != nil {
			return refused(err)
		}
		_, _ = fmt.Fprintf(out, "TASK beat id=%s lease_until=%d ms=%d\n", *c.id, until, ms())
		return 0
	case "done":
		if taskcard.IsCopy(*c.id) {
			// task done of a copy is card end (#3929): the copy returns to its
			// primary (ok; with --pr <n> --head <sha> the primary moves to review).
			r := taskcard.EndRequest{IDs: []string{*c.id}, OK: true, PR: *c.pr, Head: *c.head, By: *c.actor,
				Fields: []string{"evidence", *c.evidence}}
			if *c.pr != "" {
				r.Repo = cl.HGet(ctx, taskcard.Key(*c.id), "repo").Val()
			}
			e, err := taskcard.End(ctx, cl, r)
			if err != nil {
				return refused(err)
			}
			_, _ = fmt.Fprintf(out, "TASK done id=%s from=working to=ok primary=%s primary_to=%s ms=%d\n", *c.id, e[0].Primary, e[0].To, ms())
			return 0
		}
		return moved(taskcard.Done(ctx, cl, *c.id, *c.actor, *c.evidence, *c.pr))
	case "land":
		if *c.stream == "" {
			return moved(taskcard.Land(ctx, cl, *c.id, *c.actor, *c.sha, *c.why))
		}
		// the stream lander's step: every merging member, one call; the
		// lines name what the lander closes with the CLOSE line
		r, err := taskcard.LandStream(ctx, cl, *c.stream, *c.sha, *c.actor, *c.why)
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
		if taskcard.IsCopy(*c.id) { // a copy given back is card cancel (#3929)
			e, err := taskcard.CancelCards(ctx, cl, *c.actor, *c.why, *c.id)
			if err != nil {
				return refused(err)
			}
			_, _ = fmt.Fprintf(out, "TASK cancel id=%s from=working to=fail primary_to=%s ms=%d\n", *c.id, e[0].To, ms())
			return 0
		}
		return moved(taskcard.Cancel(ctx, cl, *c.id, *c.actor, *c.why))
	case "block":
		why := *c.why
		if why == "" {
			why = "on " + *c.on
		}
		o.Why = why
		o.Fields = []string{"blocked_on", *c.on, "blocked_reason", why}
		return moved(taskcard.Move(ctx, cl, *c.id, "waiting", o))
	case "unblock":
		if o.Why == "" {
			o.Why = "unblock"
		}
		return moved(taskcard.Move(ctx, cl, *c.id, "ready", o))
	case "front", "move":
		return c.replace(ctx, st, sub, o, moved)
	case "expire":
		r, err := taskcard.Reap(ctx, cl, *c.actor, c.friends...)
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		_, _ = fmt.Fprintf(out, "TASK expire n=%d ids=%s unlinked=%d unlinked_ids=%s ms=%d\n", len(r.Expired), strings.Join(r.Expired, ","),
			len(r.Unlinked), strings.Join(r.Unlinked, ","), ms())
		return 0
	case "ls":
		var ids []string
		var err error
		if *c.stream != "" {
			ids, err = taskcard.Ls(ctx, cl, *c.stream, *c.where)
		} else {
			ids, err = taskcard.LsFriend(ctx, cl, *c.friend, *c.where)
		}
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		for _, id := range ids {
			_, _ = fmt.Fprintln(out, id)
		}
		_, _ = fmt.Fprintf(out, "TASK ls n=%d where=%s ms=%d\n", len(ids), *c.where, ms())
		return 0
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

// taskLintText is the card a task push is linted as (#4396): the KIND,
// PATHS and DONE-WHEN it is pushed with (a flag over the issue's line), then
// the issue text.
func taskLintText(kind string, s *taskcard.Spec) string {
	var b strings.Builder
	for _, kv := range [][2]string{{"KIND", firstOf(kind, s.Kind)}, {"PATHS", s.Paths}, {"DONE-WHEN", s.DoneWhen}} {
		if kv[1] != "" {
			fmt.Fprintf(&b, "%s: %s\n", kv[0], kv[1])
		}
	}
	b.WriteString("\n" + s.Body + "\n")
	return b.String()
}

// replace is front and move: the one move to the task's own where (front,
// --to-friend, --to-stream) or to --to-where.
func (c *cardCmd) replace(ctx context.Context, st *store.Store, sub string, o taskcard.Opts, moved func(taskcard.Result, error) int) int {
	cl := st.Client()
	to := *c.toWhr
	if to == "" {
		w, err := cl.HGet(ctx, taskcard.Key(*c.id), "where").Result()
		if err != nil {
			return moved(taskcard.Result{}, &taskcard.Refused{Why: "NOTASK task:" + *c.id + ""})
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
	case *c.toFriend != "":
		o.Friend, o.SetFriend = *c.toFriend, true
	case *c.toStream != "":
		o.Stream, o.SetStream = *c.toStream, true
	default:
		o.OK = *c.ok
	}
	return moved(taskcard.Move(ctx, cl, *c.id, to, o))
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
