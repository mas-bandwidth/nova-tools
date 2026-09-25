// The task card verbs (nova-tools #3778; rowan-new specs/ws-index.md "Tasks
// are cards"): a sprint task is the record task:<id> with one where pointer,
// moved between ws:<stream>:<where> and friend:<f>:cards:<where> only by the
// one move (fn/lua/02_card_move.lua, internal/nsprint/taskcard). Each verb is
// one FCALL (fsck FCALL_RO; ls one ZRANGE; migrate the one SCAN) and prints
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
// --ids, --stream, --set); land, ls, fsck, expire and migrate always.
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

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

const taskCardUsage = `nova-sprint task: the task card verbs (#3778), one Redis Function call each

usage:
  nova-sprint task push    --actor <a> --id <id> [--stream <s>] [--friend|--to <f>] [--waiting | --depends-on <c>]
                           [--kind <k>] [--ref <repo#n>] [--origin <url>] [--title <t>] [--head <sha>] [--pr <n>] [--repo <r>] [--front]
  nova-sprint task take    --actor <f> [--id <id>] [--n <k>]
  nova-sprint task beat    --actor <f> --id <id>
  nova-sprint task done    --actor <a> --id <id> --evidence <text> [--pr <n>]
  nova-sprint task land    --actor <a> --id <id> --sha <merge sha8>
  nova-sprint task cancel  --actor <a> --id <id> --why <text>
  nova-sprint task block   --actor <a> --id <id> (--on <conditions> | --why <text>)
  nova-sprint task unblock --actor <a> --id <id>
  nova-sprint task front   --actor <a> --id <id>
  nova-sprint task move    --actor <a> --id <id> (--to-friend <f> | --to-stream <s> | --to-where <w> [--ok ok|fail] [--why <text>])
  nova-sprint task expire  --actor <a> [--friend <f>]...
  nova-sprint task ls      (--stream <s> | --friend <f>) --where <w>
  nova-sprint task fsck    --sprint <S>
  nova-sprint task migrate --actor <a> --sprint <S> --truth <stream|where|id|kind|repo#n|state file>
under a harness (NOVA_FRIEND set) --actor must be the seat.
every verb also takes --redis <addr> (else NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR) and --sprint <S>
(the legacy idx sets' sprint; else FRIEND_QUEUE_SPRINT, else the first of sprint:order).

done moves working -> merging when the task names a PR (its pr field or --pr), else -> done.
land moves merging (or working) -> landed at the merge sha. take starts a lease the child
renews with beat every 60 s; expire moves a working task whose lease lapsed back to ready.
fsck prints one line per drift and exits 1 when there is any.
`

// cardAlways are the card subverbs no other task form has.
var cardAlways = map[string]bool{"land": true, "ls": true, "fsck": true, "expire": true, "migrate": true}

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
	ok, truth                                       *string
	n, batch                                        *int
	waiting, front, help                            *bool
	friends                                         multiFlag
}

func runTaskCard(ctx context.Context, sub string, args []string, out, errOut io.Writer) int {
	verb := "task " + sub
	fs := taskFlags(verb)
	c := &cardCmd{verb: verb}
	c.redis = fs.String("redis", "", "")
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
	c.truth = fs.String("truth", "", "")
	c.n = fs.Int("n", 1, "")
	c.batch = fs.Int("batch", 200, "")
	c.waiting = fs.Bool("waiting", false, "")
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
	case "push", "done", "land", "cancel", "block", "unblock", "front", "move", "beat":
		checks = append(checks, need(c.actor, "actor"), need(c.id, "id"))
	case "take", "expire":
		checks = append(checks, need(c.actor, "actor"))
	case "migrate":
		checks = append(checks, need(c.actor, "actor"), need(c.truth, "truth"))
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
		r, err := taskcard.Push(ctx, cl, taskcard.PushRequest{ID: *c.id, Stream: *c.stream, Friend: *c.friend,
			Sprint: *c.sprint, Kind: *c.kind, Ref: *c.ref, Origin: *c.origin, Title: *c.title, Head: *c.head,
			PR: *c.pr, Repo: *c.repo, DependsOn: *c.on, Front: *c.front, By: *c.actor, Why: *c.why,
			Where: map[bool]string{true: "waiting", false: ""}[*c.waiting]})
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
		got, err := taskcard.Take(ctx, cl, *c.actor, *c.n, *c.actor, ids...)
		if err != nil {
			return refused(err)
		}
		_, _ = fmt.Fprintf(out, "TASK take n=%d ids=%s ms=%d\n", len(got), strings.Join(got, ","), ms())
		return 0
	case "beat":
		until, err := taskcard.Beat(ctx, cl, *c.id, *c.actor)
		if err != nil {
			return refused(err)
		}
		_, _ = fmt.Fprintf(out, "TASK beat id=%s lease_until=%d ms=%d\n", *c.id, until, ms())
		return 0
	case "done":
		return moved(taskcard.Done(ctx, cl, *c.id, *c.actor, *c.evidence, *c.pr))
	case "land":
		return moved(taskcard.Land(ctx, cl, *c.id, *c.actor, *c.sha, *c.why))
	case "cancel":
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
		ids, err := taskcard.Expire(ctx, cl, *c.actor, c.friends...)
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		_, _ = fmt.Fprintf(out, "TASK expire n=%d ids=%s ms=%d\n", len(ids), strings.Join(ids, ","), ms())
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
	case "migrate":
		f, err := os.Open(*c.truth)
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		truth, err := taskcard.ReadTruth(f)
		_ = f.Close()
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		r, err := taskcard.Migrate(ctx, cl, *c.sprint, *c.actor, truth, *c.batch)
		if err != nil {
			return refuse(errOut, c.verb, err.Error())
		}
		_, _ = fmt.Fprintf(out, "TASK migrate scanned=%d %s skipped=%s ms=%d\n", r.Scanned, counts(r.Placed, true), counts(r.Skipped, false), ms())
		return 0
	}
	return refuse(errOut, c.verb, "unknown card subverb "+sub)
}

// replace is front and move: the one move to the task's own where (front,
// --to-friend, --to-stream) or to --to-where.
func (c *cardCmd) replace(ctx context.Context, st *store.Store, sub string, o taskcard.Opts, moved func(taskcard.Result, error) int) int {
	cl := st.Client()
	to := *c.toWhr
	if to == "" {
		w, err := cl.HGet(ctx, taskcard.Key(*c.id), "where").Result()
		if err != nil {
			return moved(taskcard.Result{}, &taskcard.Refused{Why: "NOTASK task:" + *c.id + " (or no where: run nova-sprint task migrate)"})
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
// so a friend's working column counts only tasks a live child beats.
type taskLeaseDuty struct{ st *store.Store }

func (d *taskLeaseDuty) Run(ctx context.Context, _ *reconcile.Lease) (reconcile.Counts, error) {
	ids, err := taskcard.Expire(ctx, d.st.Client(), "reconciler")
	return reconcile.Counts{Expired: len(ids)}, err
}

func init() {
	registerReconcileDuty("task-lease", func(st *store.Store) (reconcileDuty, error) {
		return &taskLeaseDuty{st: st}, nil
	})
}
