// The sprint verb (#2939) opens a sprint from a work set, closes it, folds
// it once closed (#2618, internal/nsprint/sprint/fold.go) and prints its
// status as x/y z% -> eta. It registers itself through the registry
// (registry.go), so main.go is unchanged.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
	"github.com/redis/go-redis/v9"
)

func init() {
	register(Verb{
		Name:    "sprint",
		Summary: "open --from a work set, close, fold (the end-of-sprint refinement, #2618), and status as x/y z% -> eta",
		Run:     runSprintVerb,
	})
}

// sprintGate is the line-up gate seam (#2939): nil is GREEN. #3108 wires
// preflight.Lineup into it. On RED, open exits 1 with the reason on stderr
// and writes nothing.
var sprintGate func(ctx context.Context, name string) (bool, string)

// sprintStoreOpen opens the store; a test swaps it to hook the client.
var sprintStoreOpen = store.Open

func runSprintVerb(ctx context.Context, args []string, out, errOut io.Writer) int {
	const want = "want open --sprint <S> [--from <work-set.lisp>], close --sprint <S>, fold --sprint <S>, status [--sprint <S>] [--now <unix>] or clear --why <why> [--force] [--checkpoint <file>] [--redis host:port]"
	if len(args) == 0 {
		return refuse(errOut, "sprint", want)
	}
	sub := args[0]
	switch sub {
	case "open", "close", "status", "fold":
	case "clear":
		return runSprintClear(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "sprint", fmt.Sprintf("unknown subverb %s; %s", sub, want))
	}
	verb := "sprint " + sub
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	name := fs.String("sprint", "", "")
	from := fs.String("from", "", "")
	nowUnix := fs.Int64("now", 0, "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if (sub != "status" || *name != "") && !sprint.ValidName(*name) {
		return refuse(errOut, verb, "needs --sprint <name> ([a-z0-9-]{1,40})")
	}
	if *from != "" && sub != "open" {
		return refuse(errOut, verb, "--from is for open only")
	}
	now := time.Now()
	if *nowUnix > 0 {
		now = time.Unix(*nowUnix, 0)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var plan *openPlan
	if sub == "open" && *from != "" {
		var code int
		plan, code = readOpenPlan(*from, out, errOut)
		if plan == nil {
			return code
		}
	}
	st, err := sprintStoreOpen(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	switch sub {
	case "open":
		if plan == nil {
			plan = &openPlan{}
		}
		return runSprintOpen(ctx, st, *name, *from, plan, now, out, errOut)
	case "close":
		status, retired, refused, err := sprint.SetClosed(ctx, st, *name, now)
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		if refused != "" {
			// #3571: a name never opened, or a sprint already closed, is
			// refused so status=closed exit 0 always means it was open.
			fmt.Fprintln(out, refused)
			return 1
		}
		if retired > 0 {
			// #3925: the cards the close moved to done/fail.
			fmt.Fprintf(out, "%s retired=%d\n", sprint.Line(*name, status), retired)
			return 0
		}
		fmt.Fprintln(out, sprint.Line(*name, status))
		return 0
	case "fold":
		// #2618: the report lines, then the receipt; a re-fold replaces
		// s:<S>:fold:<section>. Exit 1 is a refusal with its remedy.
		rep, refused, err := sprint.Fold(ctx, st.Client(), *name, now)
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		if refused != "" {
			fmt.Fprintln(out, refused)
			return 1
		}
		for _, l := range append(rep.Lines, rep.Receipt) {
			fmt.Fprintln(out, l)
		}
		return 0
	default:
		lines, err := sprint.StatusLines(ctx, st, *name, now)
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		for _, l := range lines {
			fmt.Fprintln(out, l)
		}
		return 0
	}
}

// openPlan is a work set read and checked for open: the tasks to push in
// file order, the ids skipped as done, and the file's sha256.
type openPlan struct {
	setID   string
	sha     string
	tasks   []task.PushRequest
	skipped int
	owners  []string // distinct, in file order
}

// readOpenPlan is open's content checks: the file is read with
// worklang.ParseWorkSet and gets set check's rules, and every unit to be
// pushed needs a :done-when, an :owner that is one friend (not empty, not
// all) and a task kind other than read or review. Any finding prints one line
// per finding and exits 1 before Redis is touched.
func readOpenPlan(path string, out, errOut io.Writer) (*openPlan, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint sprint open: %v\n", err)
		return nil, 1
	}
	ws, err := worklang.ParseWorkSet(path, data, worklang.DefaultLimits())
	if err != nil {
		// A refused set is a content finding: one line, naming the unit
		// when the reader's refusal does, and nothing written.
		fmt.Fprintf(out, "SET REFUSED %s\n", strings.Join(strings.Fields(err.Error()), " "))
		return nil, 1
	}
	var findings []string
	checked, _ := ws.Check(worklang.Options{})
	for _, f := range checked {
		line := fmt.Sprintf("SET %s unit=%s", f.Rule, f.Unit)
		if d := f.Detail(); d != "" {
			line += " " + d
		}
		findings = append(findings, line+": "+f.Remedy)
	}
	sum := sha256.Sum256(data)
	plan := &openPlan{setID: ws.ID, sha: hex.EncodeToString(sum[:])}
	done := ws.Decided(worklang.Options{})
	for _, u := range ws.Units {
		if saysDone(u) {
			done[u.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, u := range ws.Units {
		if done[u.ID] {
			plan.skipped++
			continue
		}
		doneWhen := unitText(u, "done-when")
		if strings.TrimSpace(doneWhen) == "" {
			findings = append(findings, fmt.Sprintf("DONE-WHEN %s missing: every unit names the sentence a test can fail", u.ID))
		}
		owner := unitText(u, "owner")
		if strings.TrimSpace(owner) == "" || owner == "all" {
			shown := owner
			if strings.TrimSpace(shown) == "" {
				shown = "none"
			}
			findings = append(findings, fmt.Sprintf("OWNER %s %s: open pushes to one friend; give the unit an :owner (not empty, not all)", u.ID, shown))
		}
		kind := unitText(u, "kind")
		if kind == "" {
			kind = string(task.KindWork)
		}
		switch task.Kind(kind) {
		case task.KindRead, task.KindReview:
			findings = append(findings, fmt.Sprintf("KIND %s %s: open pushes no read or review task", u.ID, kind))
		case task.KindWork, task.KindFix, task.KindHarvest:
		default:
			findings = append(findings, fmt.Sprintf("KIND %s %s: not a task kind (work, fix or harvest)", u.ID, kind))
		}
		title := unitText(u, "title") + " | DONE-WHEN: " + doneWhen
		var needs []string
		if all := u.Needs(); len(all) > 0 {
			// The #3067 lint reads DEPENDS-ON from the title, never from needs.
			title += " | DEPENDS-ON: " + strings.Join(all, " ")
			for _, n := range all {
				if !done[n] {
					needs = append(needs, n)
				}
			}
		}
		plan.tasks = append(plan.tasks, task.PushRequest{
			ID: u.ID, Kind: task.Kind(kind), Title: title, To: owner,
			Needs: needs, Actor: "sprint open", ErrOut: errOut,
		})
		if owner != "" && !seen[owner] {
			seen[owner] = true
			plan.owners = append(plan.owners, owner)
		}
	}
	if len(findings) > 0 {
		for _, f := range findings {
			fmt.Fprintln(out, f)
		}
		return nil, 1
	}
	return plan, 0
}

// saysDone reads a unit's :done. worklang keeps :done among a unit's unknown
// keys, so Unit.Done (which reads Fields) never sees it; this reads it with
// the same spellings worklang's truthy accepts.
func saysDone(u worklang.Unit) bool {
	f, ok := u.Unknown["done"]
	if !ok {
		f, ok = u.Fields["done"]
	}
	if !ok {
		return false
	}
	if f.Kind == worklang.Integer {
		return f.Int != 0
	}
	switch strings.ToLower(unitFormText(f)) {
	case "true", "t", "yes", "done", "closed":
		return true
	}
	return false
}

// unitText is a unit field as text: a string's value, or a symbol's or
// keyword's name.
func unitText(u worklang.Unit, key string) string {
	f, ok := u.Fields[key]
	if !ok {
		return ""
	}
	return unitFormText(f)
}

func unitFormText(f worklang.Form) string {
	switch f.Kind {
	case worklang.String, worklang.Symbol, worklang.Keyword:
		return f.Value
	}
	return ""
}

// runSprintOpen is open after the content checks, in the fixed order: the
// existing-sprint read (one pipeline), the gate seam, ns_sprint_begin, the
// pushes in file order, then ns_sprint_open.
func runSprintOpen(ctx context.Context, st *store.Store, name, from string, plan *openPlan, now time.Time, out, errOut io.Writer) int {
	existing, err := sprint.ReadExisting(ctx, st, name, plan.owners)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint sprint open: %v\n", err)
		return 6
	}
	if existing.RegistryRefused != "" {
		// #3570: the seat's ACL refuses the friends registry read. Refuse
		// before anything is written: ns_task_push re-reads friends inside
		// the function under the same ACL, so going on would leave the sprint
		// opening with its pushes refused.
		fmt.Fprintf(out, "OWNERS %s unchecked: the friends registry read was refused by ACL (%s); remedy: open from a seat whose ACL grants SCARD and SISMEMBER on friends\n",
			name, existing.RegistryRefused)
		return 1
	}
	notMember := false
	for _, t := range plan.tasks {
		if existing.Member[t.To] {
			continue
		}
		notMember = true
		if existing.Friends == 0 {
			fmt.Fprintf(out, "OWNER %s %s not in friends: no friends registered yet (friends is empty on this store)\n", t.ID, t.To)
			continue
		}
		fmt.Fprintf(out, "OWNER %s %s not in friends\n", t.ID, t.To)
	}
	if notMember {
		return 1
	}
	if line := existing.Refusal(name, plan.sha); line != "" {
		fmt.Fprintln(out, line)
		return 1
	}
	if sprintGate != nil {
		if ok, reason := sprintGate(ctx, name); !ok {
			fmt.Fprintf(errOut, "nova-sprint sprint open: gate RED: %s\n", reason)
			return 1
		}
	}
	line, err := sprint.Begin(ctx, st, name, plan.setID, plan.sha, now)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint sprint open: %v\n", err)
		return 6
	}
	if line != "" {
		fmt.Fprintln(out, line)
		return 1
	}
	var pushed, existed, closed, refused int
	units := make([]string, 0, len(plan.tasks))
	for _, req := range plan.tasks {
		req.Sprint = name
		res, err := task.PushChecked(ctx, st, req)
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint sprint open: %v\n", err)
			return 6
		}
		switch res.Status {
		case task.PushCreated:
			pushed++
		case task.PushExists:
			existed++
		case task.PushClosed:
			closed++
		case task.PushConflict:
			refused++
			// one task store (#3778): ids are global, so a new sprint does not
			// free the id; the unit needs its own
			fmt.Fprintf(out, "CONFLICT %s %s: id held by another payload (task:%s; ids are global); remedy: give the unit a new id in %s\n",
				name, req.ID, req.ID, from)
			continue
		case task.PushInvalid:
			refused++
			fmt.Fprintf(out, "INVALID %s %s\n", name, req.ID)
			continue
		case task.PushOverlap:
			refused++
			fmt.Fprintf(out, "OVERLAP %s %s\n", name, res.Overlap)
			continue
		}
		units = append(units, req.ID)
	}
	n := pushed + existed + closed + plan.skipped + refused
	counts := fmt.Sprintf("units=%d pushed=%d existed=%d closed=%d skipped_done=%d", n, pushed, existed, closed, plan.skipped)
	if refused > 0 {
		fmt.Fprintf(out, "NOT-OPENED %s %s refused=%d\n", name, counts, refused)
		return 1
	}
	line, err = sprint.Finish(ctx, st, name, plan.setID, plan.sha, now, units)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint sprint open: %v\n", err)
		return 6
	}
	if line != "" {
		fmt.Fprintln(out, line)
		return 1
	}
	fmt.Fprintf(out, "OPEN %s %s\n", name, counts)
	return 0
}

// runSprintClear is `nova-sprint sprint clear` (Glenn 2026-09-26 8:40 AM ET,
// "reset the sprint table. zeros everywhere"; 8:41 AM: "make sprint clearing
// a verb. It should be simple and fast"): one Redis Function call,
// ns_sprint_clear, which is ONE INCR of sprint:epoch (nova-tools#4238; Glenn
// 9:25 AM ET). Every set the tables read is named by the epoch, so the next
// tick reads the new epoch's empty sets and every member of the old epoch
// is invisible for good; nothing is moved or deleted, and a writer still
// holding the old epoch cannot make a cell non-zero. Cards working or
// merging are in flight and refuse the clear without --force. The pit stop
// is kept, never lifted: the receipt says which one it found.
// --checkpoint <file> writes every ws set's members (the epoch's) before
// the INCR. Prints CLEARED streams=<n> cards=<n> copies=<n> consumers=<n>
// epoch=<n> by=<who> ms=<n>, PITSTOP kept sprint=<S> | PITSTOP none, then
// STREAM <name> cards=<n> per stream (what the clear made invisible).
func runSprintClear(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "sprint clear"
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "")
	why := fs.String("why", "", "")
	by := fs.String("by", "", "")
	force := fs.Bool("force", false, "")
	checkpoint := fs.String("checkpoint", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	if strings.TrimSpace(*why) == "" {
		return refuse(errOut, verb, "needs --why <why>")
	}
	who := *by
	if who == "" {
		who = os.Getenv(seatEnv)
	}
	if who == "" {
		who = "sprint-clear"
	}
	at := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := sprintStoreOpen(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	if *checkpoint != "" {
		if err := writeClearCheckpoint(ctx, st.Client(), *checkpoint, at); err != nil {
			return refuse(errOut, verb, "checkpoint: "+err.Error())
		}
	}
	forceArg := "0"
	if *force {
		forceArg = "1"
	}
	// ms is the clear's own time: the one call, not the dial or the
	// checkpoint before it (DONE-WHEN of #4238: under 10 ms)
	start := time.Now()
	reply, err := st.Client().FCall(ctx, "ns_sprint_clear", nil, who, *why, forceArg).Slice()

	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	words := make([]string, len(reply))
	for i, v := range reply {
		words[i] = fmt.Sprint(v)
	}
	if len(words) == 0 || words[0] != "CLEARED" {
		if len(words) >= 2 && words[0] == "REFUSED" {
			fmt.Fprintf(errOut, "REFUSED %s: %s\n", verb, oneline.Escape(words[1]))
			return 1
		}
		return refuse(errOut, verb, "unexpected reply "+oneline.Escape(strings.Join(words, " ")))
	}
	if len(words) < 8 {
		return refuse(errOut, verb, "short reply "+oneline.Escape(strings.Join(words, " ")))
	}
	fmt.Fprintf(out, "CLEARED streams=%s cards=%s copies=%s consumers=%s epoch=%s by=%s ms=%d\n", words[1], words[2], words[3], words[4], words[5], who, time.Since(start).Milliseconds())
	if words[6] == "kept" {
		fmt.Fprintf(out, "PITSTOP kept sprint=%s\n", oneline.Escape(words[7]))
	} else {
		fmt.Fprintln(out, "PITSTOP none")
	}
	for i := 8; i+1 < len(words); i += 2 {
		fmt.Fprintf(out, "STREAM %s cards=%s\n", oneline.Escape(words[i]), words[i+1])
	}
	return 0
}

// writeClearCheckpoint records every stream's set members under the current
// epoch, one line per card (stream, where, id), before the clear's INCR
// makes them invisible.
func writeClearCheckpoint(ctx context.Context, client redis.UniversalClient, path string, at time.Time) error {
	streams, err := client.ZRange(ctx, "ws:order", 0, -1).Result()
	if err != nil {
		return err
	}
	epoch, err := ws.Epoch(ctx, client)
	if err != nil {
		return err
	}
	pipe := client.Pipeline()
	cmds := map[string]*redis.StringSliceCmd{}
	for _, s := range streams {
		for _, w := range table.WSStates {
			cmds[s+"\t"+w] = pipe.ZRange(ctx, ws.KeyAt(epoch, s, w), 0, -1)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# nova-sprint sprint clear checkpoint at=%s epoch=%d streams=%d\n", at.UTC().Format(time.RFC3339), epoch, len(streams))

	for _, s := range streams {
		for _, w := range table.WSStates {
			for _, id := range cmds[s+"\t"+w].Val() {
				fmt.Fprintf(&b, "%s\t%s\t%s\n", s, w, id)
			}
		}
	}
	return writeAtomic(path, b.String())
}
