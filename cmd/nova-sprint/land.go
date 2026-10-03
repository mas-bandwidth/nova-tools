package main

// land is the coordinator's landing step as one command (docs/SPEC-SPRINT.md
// section 7; tla/SprintEvents.tla, the verb "land": only the head of a
// stream's merge queue lands, QHead). For each stream, in stream order, the
// merge queue is read in work order up to its first stuck card (the merge
// step's barrier) and cut into batches: each run of consecutive cards naming
// one repository and one base (the brief's REPO: and BASE: lines, read by
// swarm.ReadCardBase) is one batch. A batch is merged head by head (--no-ff)
// onto a branch cut from the base's tip on origin, checked once, pushed (never
// forced), and reported through the merge step (store.MergeStep), the step
// `merge --stream s --batch n` runs, so the store changes exactly as that verb
// changes it. A head that is missing or conflicts ends the batch before it and
// is reported with the merge step's conflict fact; a check that fails, with
// its red fact; a push rejected again after one rebuild on the moved base,
// with its rejected fact. The verb keeps no state of its own: the store
// changes only through those merge steps, and git and the check run as
// programs in the caller's environment.
//
// The push and the report are two operations on two systems, so land fences
// them: the caller's --epoch is checked before any git; the queue and the epoch
// are read again just before each push; the report is one store step that lands
// the batch, its cards by name, only while the queue still holds each of them at
// the head and attempt land built, at the epoch land read (landStep; the cards
// are looked for wherever they stand, so a card accepted or ranked ahead of them
// since changes nothing);
// a batch pushed and not reported is left in the base and recovered by running
// land again (Recovers). Only a batch pushed and reported has its cards' branches
// tagged for the cleanup, which deletes them from origin outside every batch
// (landprune.go).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// landCheckBudget bounds one --check run: a batch's whole test command.
const landCheckBudget = 30 * time.Minute

// landWords is the banner's paragraph on land; its indented lines are what
// `nova-sprint land -h` quotes.
func landWords() string {
	return strings.TrimSpace(`
Landing, the coordinator's: an external delivery (git pushes the base) and a store write (the merge step):
  nova-sprint land --stream s1 --check 'make test'
    merges each queued card's head (--no-ff) in queue order onto a branch cut
    from origin's base, one batch per run of cards naming one REPO: and BASE:
    (--base for a card naming none); runs --check once per batch; pushes, never
    forced, rebuilding once on a moved base; then reports the batch as merge
    --stream s1 --batch <n> does. A head missing or in conflict ends the batch
    before it and is reported as merge --conflict, a red check as --red, a
    second rejected push as --rejected. Each head merged is checked first, by
    script and no model: a head whose diff changes a file outside its brief's
    PATHS, or leaves a stranded sentence fragment or an unmatched backquote in
    prose, ends the batch as a head in conflict does. The clone is --repo-dir,
    else the dir= each line names; git uses the caller's environment.
  nova-sprint land --stream s1 --dry-run
    reads the store only: no git, no push, no report. The window: land pins
    each card's head and attempt as it reads them; a caller's --epoch is held
    before any git, the queue, the heads and the epoch again just before the
    push, and the report lands the batch, its cards by name, only while the queue
    holds each of them at that head at that epoch (one store step); a card
    accepted or ranked ahead since changes nothing. A clear, a return, a
    rework or a crash after the check leaves the push unreported (LAND
    FAILED, exit 2; run land again, never a bare merge, and its own checks
    decide: an unchanged card is recorded with no new push, a reworked one
    merged at its new head or met in conflict); a clear there pushes for an epoch
    just left: nothing is recorded for it, and the push is not undone.
  nova-sprint land --stream s1
    run again, it recovers once the outside is quiet (tla/Land.tla, Recovers),
    not otherwise: a run cut short between the push and the report on every
    try never reports; and a base that moves twice between the read and the
    push gives up (one rebuild, then the rejected fact, the stream stopped):
    nothing is pushed or lost and the cards stay queued; resume the stream
    (nova-sprint resume --stream s1 --did 'the base moved') and run land again.`) + "\n"
}

// landBatch is one batch's outcome, a line of output and an item of --json.
type landBatch struct {
	Stream string   `json:"stream"`
	Status string   `json:"status"` // ok, refused, failed
	Cards  int      `json:"cards"`
	IDs    []string `json:"ids"`
	Repo   string   `json:"repo,omitempty"`
	Base   string   `json:"base,omitempty"`
	Dir    string   `json:"dir,omitempty"`
	Tip    string   `json:"tip,omitempty"`
	// Fact is the merge fact reported for a refusal (conflict, red,
	// rejected); empty when nothing was reported and the store is unchanged,
	// and always empty in a dry run, which reports nothing.
	Fact string `json:"fact,omitempty"`
	// WouldRecord is, in a dry run, the fact land would report for the refusal,
	// and nothing was: a reader of fact never has to check dry_run to know
	// whether the store holds it.
	WouldRecord string `json:"would_record,omitempty"`
	Reason      string `json:"reason,omitempty"`
	// Also is every cause of the refusal after the first, each with its one next command,
	// and on a twin the verb that stands in for land: one NOTE line each.
	Also   []string `json:"also,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
	// Times is how long each of the batch's steps took; nil for a batch refused before
	// its git ran, and for a dry run.
	Times *landTimes `json:"times,omitempty"`
	// Prune is the cards' branches on origin a landed batch put on the cleanup queue
	// (landprune.go; a dry run: would put); nil for a batch that did not land, or
	// whose cards recorded no branch.
	Prune *landPrune `json:"prune,omitempty"`
}

// landTimes is a batch's steps, in seconds: the fetch, the merges (with any head
// fetched at its merge), the check, the queue read again before the push, the push and
// the report. A rebuild on a moved base adds its fetch, merges, check and push.
type landTimes struct {
	Fetch  float64 `json:"fetch"`
	Merge  float64 `json:"merge"`
	Check  float64 `json:"check"`
	Queue  float64 `json:"queue"`
	Push   float64 `json:"push"`
	Report float64 `json:"report"`
}

// since adds the seconds from start to now to *to.
func since(to *float64, start time.Time) { *to += time.Since(start).Seconds() }

// line is the batch's output line.
func (b landBatch) line() string {
	tip := b.Tip
	if tip == "" {
		tip = "-"
	}
	l := fmt.Sprintf("LAND %s stream=%s cards=%d base=%s tip=%s ids=%s", strings.ToUpper(b.Status), oneline.Field(b.Stream), b.Cards,
		oneline.Field(dashed(b.Base)), tip, oneline.Field(idSpan(b.IDs)))
	if b.Repo != "" {
		l += " repo=" + oneline.Field(b.Repo)
	}
	if b.Dir != "" {
		l += " dir=" + oneline.Field(b.Dir)
	}
	if t := b.Times; t != nil {
		l += fmt.Sprintf(" fetch=%.1fs merge=%.1fs check=%.1fs queue=%.1fs push=%.1fs report=%.1fs", t.Fetch, t.Merge, t.Check, t.Queue, t.Push, t.Report)
	}
	if p := b.Prune; p != nil {
		switch {
		case p.Why != "":
			l += " branches_queued=-"
		default:
			l += " branches_queued=" + strconv.Itoa(p.Queued)
		}
		if len(p.Kept) > 0 {
			l += " branches_kept=" + strconv.Itoa(len(p.Kept))
		}
	}
	if b.Fact != "" {
		l += " fact=" + b.Fact
	}
	if b.WouldRecord != "" {
		l += " would_record=" + b.WouldRecord
	}
	if b.DryRun {
		l += " dry_run=yes"
	}
	if b.Reason != "" {
		l += " reason=" + oneline.Escape(b.Reason)
	}
	return l
}

// idSpan is ids as <first>..<last>, one id alone, - for none.
func idSpan(ids []string) string {
	switch len(ids) {
	case 0:
		return "-"
	case 1:
		return ids[0]
	}
	return ids[0] + ".." + ids[len(ids)-1]
}

// landCard is one queued card with where it lands: the head and attempt
// land read and pins (tla/Land.tla, a head is <<card, attempt>>: a rework
// keeps the id and the epoch), and the repository and base its brief names.
type landCard struct {
	id, head, attempt, repo, base string
	brief                         *sprint.Card // the primary, whose brief decision its landing attaches to (briefdecide.go)
	paths                         []string     // the brief's PATHS globs, nil when it names none (checkCard)
}

// pin is the card as the report's guard and the operation's arguments name
// it: <id>@<attempt>:<head>.
func (c landCard) pin() string { return c.id + "@" + c.attempt + ":" + c.head }

// lander is one run of land.
type lander struct {
	a                          *app
	c                          common
	st                         *store.Store
	repoDir, base, check, root string
	dry, twin                  bool // twin: a mem twin, which has no git
	out                        []landBatch
	epoch                      uint64 // the epoch land read: every report is fenced to it
}

func (a *app) cmdLand(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("land")
	var streams listFlag
	fs.Var(&streams, "stream", "a stream to land (again, or comma separated, for more; default: every stream with cards queued to merge and not stopped)")
	repoDir := fs.String("repo-dir", "", "the clone to land in, its origin the remote pushed to (default: a clone per repository under the directory each line names)")
	base := fs.String("base", "", "the base branch of a card whose brief names no BASE: line")
	check := fs.String("check", "", "a command run once per batch, by sh -c in the clone on the batch branch, before the push (bounded to 30m); non-zero reports the batch red and pushes nothing")
	dry := fs.Bool("dry-run", false, "print the batches it would land and change nothing: reads the store only (no git, no push, no report)")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "land", err.Error())
	}
	var bad []string
	if len(pos) > 0 {
		bad = append(bad, "takes no words; a stream is --stream <s>")
	}
	if strings.HasPrefix(*base, "-") {
		bad = append(bad, "--base wants a branch name, not "+*base)
	}
	if *repoDir != "" {
		if fi, err := os.Stat(*repoDir); err != nil || !fi.IsDir() {
			bad = append(bad, "--repo-dir wants an existing clone; "+*repoDir+" is not a directory")
		}
	}
	if len(bad) > 0 {
		return refuse(stderr, "land", strings.Join(bad, "; "))
	}
	// land's reads and its report are steps of the sprint: each takes the server's one
	// line of control (a.serial) when this process is the server (run --land), so none
	// runs during a tick or a worker's batch; its git runs outside it, for as long as
	// git takes. A land by itself holds a lock nothing else wants.
	a.serial.Lock()
	st, err := a.store(*c)
	a.serial.Unlock()
	if err != nil {
		return refuse(stderr, "land", err.Error())
	}
	// the epoch the caller holds is checked before anything is read or run
	// (tla/Land.tla, Read: a stale caller is refused before any push)
	if c.epoch >= 0 && uint64(c.epoch) != st.PinnedEpoch() {
		fmt.Fprintf(stderr, "%s land: the sprint is at epoch %d, not %d (cleared since): nothing was fetched, pushed or reported; run: nova-sprint where\n", prog, st.PinnedEpoch(), c.epoch)
		return 1
	}
	l := &lander{a: a, c: *c, st: st, repoDir: *repoDir, base: *base, check: *check, dry: *dry, twin: a.twinOpen(c.redis), epoch: st.PinnedEpoch()}
	if *repoDir != "" {
		if abs, err := filepath.Abs(*repoDir); err == nil {
			l.repoDir = abs
		}
	}
	ctx := context.Background()
	a.serial.Lock()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	a.serial.Unlock()
	if err != nil {
		return a.readFailed("land", err, stderr)
	}
	// the streams in stream order: the named ones (a name that is no stream
	// last, to be refused), else every one with cards queued and not stopped
	var order []string
	for _, name := range s.Streams() {
		named := slices.Contains(streams, name)
		if named || len(streams) == 0 && s.StreamCtl(name) != nil && s.StreamCtl(name).F("state") != sprint.StreamStopped && len(landQueue(s, name)) > 0 {
			order = append(order, name)
		}
	}
	for _, name := range streams {
		if !slices.Contains(order, name) {
			order = append(order, name)
		}
	}
	failed := false
	for _, name := range order {
		if !l.stream(ctx, s, name) {
			failed = true
		}
	}
	// the cleanup, after every stream and outside every batch: the land loop's own
	// (landRound) when the loop runs this land, else once here, as the command ends
	var pruned []pruneResult
	if !a.landLazy && !l.dry {
		pruned = a.flushPrune(ctx, true)
	}
	return l.report(failed, pruned, stdout, stderr)
}

// report prints the batches, the cleanup and the summary: exit 0 when every batch
// landed, 1 when one was refused, 2 when a push landed and its report did not. A
// cleanup that failed changes no exit: its batches landed.
func (l *lander) report(failed bool, pruned []pruneResult, stdout, stderr io.Writer) int {
	batches, cards, refused := 0, 0, 0
	for _, b := range l.out {
		switch b.Status {
		case "ok":
			batches++
			cards += b.Cards
		default:
			refused++
		}
	}
	code := 0
	switch {
	case failed:
		code = 2
	case refused > 0:
		code = 1
	}
	if l.c.json {
		status := map[int]string{0: "ok", 1: "refused", 2: "failed"}[code]
		out := l.out
		if out == nil {
			out = []landBatch{}
		}
		if pruned == nil {
			pruned = []pruneResult{}
		}
		// ignored: a map of strings, numbers and plain structs of strings always encodes
		b, _ := json.Marshal(map[string]any{"verb": "land", "status": status, "exit": code, "batches": batches, "cards": cards,
			"refused": refused, "dry_run": l.dry, "items": out, "prune": pruned})
		fmt.Fprintln(stdout, string(b))
		return code
	}
	for _, b := range l.out {
		w := stdout
		if b.Status != "ok" {
			w = stderr
		}
		fmt.Fprintln(w, b.line())
		for _, x := range b.Also {
			fmt.Fprintf(w, "NOTE %s\n", oneline.Escape(x))
		}
		switch {
		case b.WouldRecord != "":
			fmt.Fprintf(w, "NOTE land would report this as merge --%s and stop stream %s; nothing was reported (dry run)\n", b.WouldRecord, oneline.Field(b.Stream))
		case b.Fact != "":
			fmt.Fprintf(w, "NOTE the stream is stopped (%s); run: nova-sprint inbox\n", b.Fact)
		case b.Status == "refused" && !l.dry:
			fmt.Fprintf(w, "NOTE nothing was pushed or reported for stream %s; its cards stay queued\n", oneline.Field(b.Stream))
		}
		if p := b.Prune; p != nil {
			if p.Why != "" {
				fmt.Fprintf(w, "NOTE %s\n", oneline.Escape(p.Why))
			}
			for _, k := range p.Kept {
				fmt.Fprintf(w, "NOTE no branch queued for deletion for %s\n", oneline.Escape(k))
			}
		}
	}
	for _, p := range pruned {
		fmt.Fprintln(stdout, p.line(true))
	}
	dry := ""
	if l.dry {
		dry = " dry_run=yes"
	}
	w := stdout
	if code != 0 {
		w = stderr // each line above names its remedy
	}
	fmt.Fprintf(w, "LAND DONE batches=%d cards=%d refused=%d%s; run: nova-sprint where\n", batches, cards, refused, dry)
	return code
}

// landQueue is the stream's queued merge cards the merge step can land, in
// work order: those before its first stuck card (steps_merge.go, the barrier).
func landQueue(s *sprint.Snapshot, stream string) []*sprint.Card {
	queued := s.Merge.Cell(stream, sprint.Queued)
	stuck := s.Merge.Cell(stream, sprint.Stuck)
	if len(stuck) == 0 {
		return queued
	}
	var before []*sprint.Card
	for _, c := range queued {
		if c.Score < stuck[0].Score || (c.Score == stuck[0].Score && c.ID < stuck[0].ID) {
			before = append(before, c)
		}
	}
	return before
}

// stream lands one stream's batches in queue order and stops at the first
// that does not land whole; false when a push landed and its report did not.
func (l *lander) stream(ctx context.Context, s *sprint.Snapshot, stream string) bool {
	refused := func(why string) bool {
		l.out = append(l.out, landBatch{Stream: stream, Status: "refused", IDs: []string{}, Reason: why})
		return true
	}
	ctl := s.StreamCtl(stream)
	switch {
	case ctl == nil:
		return refused("no such stream; run: nova-sprint where")
	case ctl.F("state") == sprint.StreamStopped:
		return refused("stopped (" + ctl.F("cause") + "); run: nova-sprint resume --stream " + stream)
	}
	queue := landQueue(s, stream)
	if len(queue) == 0 {
		return refused("nothing queued to merge in stream " + stream + "; run: nova-sprint queue --stream " + stream)
	}
	var cards []landCard
	for _, c := range queue {
		lc := landCard{id: c.ID, base: l.base}
		if pr := s.Work.Placed(c.ID); pr != nil {
			lc.head, lc.attempt, lc.brief = pr.F("head"), pr.F("attempt"), pr
			cb := swarm.ReadCardBase([]byte(pr.F("brief")))
			lc.repo, lc.paths = cb.Repo, cardPaths(pr.F("brief"))
			if cb.Ref != "" {
				lc.base = cb.Ref
			}
		}
		cards = append(cards, lc)
	}
	for len(cards) > 0 {
		n := 1
		for n < len(cards) && cards[n].repo == cards[0].repo && cards[n].base == cards[0].base {
			n++
		}
		landed, ok := l.batch(ctx, stream, cards[:n])
		if !ok {
			return false
		}
		if !landed {
			return true
		}
		cards = cards[n:]
	}
	return true
}

// cardPaths is a brief's PATHS globs; nil for none (no line, or `PATHS: none`).
func cardPaths(brief string) []string {
	var out []string
	value, _ := swarm.CardHeaderValue([]byte(brief), "PATHS")
	for _, g := range strings.Split(value, ",") {
		if g = strings.TrimSpace(g); g != "" && g != "none" {
			out = append(out, g)
		}
	}
	return out
}

// shaRE is a commit id as a head names it: hex, abbreviated or whole.
var shaRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// batch lands one batch: landed says every card of it landed (the stream
// goes on to its next batch), ok is false when a push landed and its report
// did not.
func (l *lander) batch(ctx context.Context, stream string, cards []landCard) (landed, ok bool) {
	ids := make([]string, len(cards))
	for i, c := range cards {
		ids[i] = c.id
	}
	b := landBatch{Stream: stream, Status: "refused", Cards: len(cards), IDs: ids, Repo: cards[0].repo, Base: cards[0].base, DryRun: l.dry}
	refuse := func(why string) (bool, bool) {
		b.Reason = why
		l.out = append(l.out, b)
		return false, true
	}
	if why, also := l.placeWhy(stream, cards); why != "" {
		b.Also = also
		return refuse(why)
	}
	dir, why := l.clone(ctx, b.Repo)
	if why != "" {
		return refuse(why)
	}
	b.Dir = dir
	if l.dry {
		return l.dryBatch(b, cards)
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return refuse("the clone " + dir + " is not clean (" + firstLine(out, err) + "); commit or discard its changes, then run land again")
	}
	b.Times = &landTimes{}
	merged, failed, why := l.build(ctx, dir, stream, cards, b.Times)
	if why != "" {
		return refuse(why)
	}
	for attempt := 1; len(merged) > 0; attempt++ {
		// the batch as built: this commit is what is pushed and reported, whatever the
		// clone's checkout becomes after (another landing sharing the clone cuts its own
		// branch there; pushing HEAD then pushed the other job's and reported this one
		// landed with its work nowhere on the base)
		tip, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return refuse("the batch branch has no tip: " + firstLine("", err))
		}
		start := time.Now()
		why := l.runCheck(ctx, dir)
		since(&b.Times.Check, start)
		if why != "" {
			b.Cards, b.IDs = len(merged), ids[:len(merged)]
			return l.fact(b, sprint.MergeReq{Stream: stream, Batch: len(merged), Red: true, Note: why}, cards[:len(merged)], "red", why)
		}
		start = time.Now()
		why = l.queueHead(ctx, stream, cards[:len(merged)])
		since(&b.Times.Queue, start)
		if why != "" {
			return refuse(why)
		}
		if l.a.beforePush != nil {
			l.a.beforePush(attempt)
		}
		start = time.Now()
		_, err = l.git(ctx, dir, "push", "--porcelain", "origin", tip+":refs/heads/"+b.Base)
		since(&b.Times.Push, start)
		if err == nil {
			b.Tip = tip
			if !l.landed(b, stream, cards[:len(merged)]) {
				return false, false
			}
			if failed.id != "" {
				l.conflict(stream, failed)
				return false, true
			}
			return true, true
		}
		if !rejected(err) {
			return refuse("the push to " + b.Base + " failed: " + firstLine("", err) + "; nothing was reported")
		}
		if attempt == 2 {
			b.Cards, b.IDs = len(merged), ids[:len(merged)]
			return l.fact(b, sprint.MergeReq{Stream: stream, Batch: len(merged), Rejected: true, Note: firstLine("", err)}, cards[:len(merged)], "rejected", "the push to "+b.Base+" was rejected again after a rebuild on the moved base: "+firstLine("", err))
		}
		merged, failed, why = l.build(ctx, dir, stream, cards, b.Times)
		if why != "" {
			return refuse(why)
		}
	}
	l.conflict(stream, failed)
	return false, true
}

// placeWhy is why land cannot place a batch before any git, "" when it can:
// every problem of the batch at once (a base missing, a repository missing with
// no clone given, and with them every head that is not a commit id, which land
// would meet next), so one run names all of them (docs/STANDARD.md, a refusal
// names every problem at once), each cause on its own line with its one next
// command: the first is the refusal's reason, the rest (also) its NOTE lines, and on a
// twin, which has no git, the merge that stands in for land. A base that is not a
// branch name is refused alone.
func (l *lander) placeWhy(stream string, cards []landCard) (string, []string) {
	id, base := cards[0].id, cards[0].base
	if strings.HasPrefix(base, "-") {
		return "card " + id + " names the base " + base + ", which is not a branch name; run: nova-sprint card " + id, nil
	}
	var why, flags []string
	if base == "" {
		why = append(why, "card "+id+" names no BASE: line and no --base was given")
		flags = append(flags, "--stream "+stream+" --base <branch>")
	}
	if cards[0].repo == "" && l.repoDir == "" {
		why = append(why, "the card names no REPO: line and no --repo-dir was given")
		flags = append(flags, "--repo-dir <clone>")
	}
	if len(why) == 0 {
		return "", nil
	}
	var also []string
	for _, c := range cards {
		if !shaRE.MatchString(c.head) {
			// before any git nothing was recorded and no stream stopped: the one next
			// command is the return; its judgment offers the rework
			also = append(also, "the head "+dashed(c.head)+" of "+c.id+" is not a commit id (a finish without --head records the card's id); run: nova-sprint return "+c.id+" --reason 'its head is not a commit'")
		}
	}
	if l.twin {
		also = append(also, fmt.Sprintf("this twin has no git: nova-sprint merge --stream %s --batch %d records the landing in land's place (land merges and pushes)", stream, len(cards)))
	}
	return strings.Join(why, ", and ") + "; run: nova-sprint land " + strings.Join(flags, " "), also
}

// dryBatch is the dry run's outcome of a batch land can place: it stops where
// land stops before any git, at the first head that is not a commit id
// (headNotCommit, mergeHead's own check and words): the cards before it a
// batch that lands, that card refused as land reports it, with the conflict
// fact land would record and the stream stop; nothing is recorded.
func (l *lander) dryBatch(b landBatch, cards []landCard) (landed, ok bool) {
	cut := slices.IndexFunc(cards, func(c landCard) bool { return headNotCommit(b.Stream, c) != "" })
	if cut < 0 {
		b.Status = "ok"
		l.tag(context.Background(), &b, cards)
		l.out = append(l.out, b)
		return true, true
	}
	if cut > 0 {
		before := b
		before.Status, before.Cards, before.IDs = "ok", cut, b.IDs[:cut]
		l.tag(context.Background(), &before, cards[:cut])
		l.out = append(l.out, before)
	}
	c := cards[cut]
	l.out = append(l.out, landBatch{Stream: b.Stream, Status: "refused", Cards: 1, IDs: []string{c.id}, WouldRecord: "conflict", Reason: headNotCommit(b.Stream, c), DryRun: true})
	return false, true
}

// headNotCommit is why a card's head cannot be merged whatever origin holds:
// it is not a commit id (a finish without --head records the card's id); ""
// when it is one. land meets it at the card's merge (mergeHead) and its dry
// run before any git (dryBatch), in these words.
//
// A land that meets it records the conflict fact, which stops the stream, so the
// remedy ends with the resume that starts it again: return, rework and resume,
// in that order.
func headNotCommit(stream string, c landCard) string {
	if shaRE.MatchString(c.head) {
		return ""
	}
	return "the head " + dashed(c.head) + " of " + c.id + " is not a commit id (a finish without --head records the card's id); run: nova-sprint return " + c.id +
		" --reason 'its head is not a commit', then nova-sprint rework " + c.id + " --fix 'finish with --head <commit>', then (a land that met it stopped the stream) nova-sprint resume --stream " +
		stream + " --did 'returned " + c.id + " for rework'"
}

// conflictCard is a card that did not merge, with git's words.
type conflictCard struct {
	landCard
	why string
}

// conflict reports the card that ended its batch with the conflict fact: the
// step's batch is that one card, by name, at the head that did not merge (a
// replacement attempt is never blamed).
func (l *lander) conflict(stream string, f conflictCard) {
	b := landBatch{Stream: stream, Status: "refused", Cards: 1, IDs: []string{f.id}}
	l.fact(b, sprint.MergeReq{Stream: stream, Batch: 1, Conflict: f.id, Note: f.why}, []landCard{f.landCard}, "conflict", f.why)
}

// fact reports a fact that stops the stream through the merge step, and the
// batch refused with it.
func (l *lander) fact(b landBatch, r sprint.MergeReq, pins []landCard, fact, why string) (bool, bool) {
	b.Status, b.Fact, b.Reason = "refused", fact, why
	r.Who = l.c.actor
	res, err := l.step(r, pins)
	if code := stepExit(res, err); code != 0 {
		b.Fact = ""
		b.Reason = why + "; the merge step did not record it (" + stepWhy(res, err) + "); " + againRemedy(r.Stream)
	}
	l.out = append(l.out, b)
	return false, true
}

// againRemedy is the one remedy land names when a report did not go through:
// land again, which rereads the current queue and lets its own checks decide.
// A bare merge step is never offered: after a rework the queue starts with the
// same ids at a head the base does not hold, and merge --batch would record it
// past the head guard (tla/Land.tla, idguard).
func againRemedy(stream string) string {
	return "run land again, which rereads the queue and lets its checks decide (where the cards are as they were, their merges and push are no-ops and the report records them; a card reworked since is merged at its new head, or meets a real conflict): nova-sprint land --stream " + stream
}

// landed reports a pushed batch through the merge step; false (and the line
// FAILED, with land again as the remedy) when the store did not take it.
func (l *lander) landed(b landBatch, stream string, pins []landCard) bool {
	ids := make([]string, len(pins))
	for i, c := range pins {
		ids[i] = c.id
	}
	b.Cards, b.IDs = len(ids), ids
	start := time.Now()
	res, err := l.step(sprint.MergeReq{Stream: stream, Batch: len(ids), Who: l.c.actor}, pins)
	if b.Times != nil {
		since(&b.Times.Report, start)
	}
	if code := stepExit(res, err); code != 0 || !movedExactly(res.Moved, ids) {
		b.Status, b.Reason = "failed", "the batch was pushed to "+b.Base+" at "+b.Tip+" and NOT reported ("+stepWhy(res, err)+
			"); "+againRemedy(stream)
		l.out = append(l.out, b)
		return false
	}
	b.Status = "ok"
	// pushed AND reported: only now are its cards' branches tagged for the cleanup (a
	// batch pushed and not reported keeps them: land is run again and may need the heads)
	l.tag(context.Background(), &b, pins)
	var ends []briefEnd // each card's end, attached to the brief decision it names (briefdecide.go)
	for _, c := range pins {
		label, note := decide.LandLabel(c.attempt)
		ends = append(ends, briefEndOf(c.id, c.brief, decide.End{Label: label, Note: note}))
	}
	b.Also = append(b.Also, l.a.attachBriefs(ends)...)
	l.out = append(l.out, b)
	return true
}

// movedExactly says the step's moved lines are the landings of ids, each once and no
// other (a replayed receipt of another batch is not this one), in whatever order: the
// step lands the named cards in the queue's order at the report, which a rank between
// the push and the report can change, and the cards landed are the same cards.
func movedExactly(moved, ids []string) bool {
	if len(moved) != len(ids) {
		return false
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	if len(want) != len(ids) {
		return false // a card named twice is not a batch
	}
	for _, line := range moved {
		id, rest, _ := strings.Cut(line, " ")
		if !want[id] || rest != "merging -> landed" && !strings.HasPrefix(rest, "merging -> landed") {
			return false
		}
		delete(want, id) // each once
	}
	return len(want) == 0
}

// step runs one merge step, as `merge --stream` runs it, fenced to the epoch
// land read and guarded in the same store step: it plans only while the
// stream's queue holds every pinned card, each at the head and attempt land
// read and pushed, and the batch it records is those cards by name (MergeReq.
// Cards), so a card gone from the queue since the push, or reworked to another
// head, refuses the report and nothing is recorded that was not pushed. The pins are part of
// the step's arguments, and under the caller's --op its op id is the op and
// those arguments, so a replay returns only the receipt of this very batch.
func (l *lander) step(r sprint.MergeReq, pins []landCard) (store.Result, error) {
	// the batch is the pinned cards by name, never the first n of the queue
	r.Cards = make([]string, len(pins))
	for i, c := range pins {
		r.Cards[i] = c.id
	}
	step := store.MergeStep(r)
	plan := step.Plan
	step.Plan = func(s *sprint.Snapshot) sprint.Plan {
		if why := headWhy(s, r.Stream, pins); why != "" {
			return sprint.Plan{Refused: []sprint.Refusal{{Key: r.Stream, Why: why}}}
		}
		return plan(s)
	}
	named := make([]string, len(pins))
	for i, c := range pins {
		named[i] = c.pin()
	}
	step.Args = store.ArgsOf(struct {
		Req  sprint.MergeReq
		Pins []string
	}{r, named})
	epoch := l.epoch
	step.Epoch = &epoch
	if l.c.op != "" {
		step.CallerOp = l.c.op + "." + r.Stream + "." + step.Args
	}
	l.a.serial.Lock()
	defer l.a.serial.Unlock()
	return l.st.Run(context.Background(), step)
}

// stepWhy is why a step did not move what it was given.
func stepWhy(res store.Result, err error) string {
	if err != nil {
		return oneline.Err(err)
	}
	var why []string
	for _, r := range res.Refused {
		why = append(why, r.Key+": "+r.Why)
	}
	if len(why) == 0 {
		return fmt.Sprintf("moved %d", len(res.Moved))
	}
	return strings.Join(why, "; ")
}

// queueHead is why the stream's merge queue, read again at the epoch land
// read, no longer holds the pinned cards at their heads; "" when it does.
func (l *lander) queueHead(ctx context.Context, stream string, pins []landCard) string {
	l.a.serial.Lock()
	s, err := l.st.Load(ctx, []string{sprint.Merge, sprint.Work}, nil)
	l.a.serial.Unlock()
	if err != nil {
		return "the merge queue could not be read again at epoch " + strconv.FormatUint(l.epoch, 10) + ": " + oneline.Err(err)
	}
	return headWhy(s, stream, pins)
}

// headWhy is why the stream's queue no longer holds every pinned card at the
// head and attempt pinned; "" when it does. The cards are looked for by name,
// wherever they stand: the tick's accepts put cards in the queue by their order
// of work while a landing builds, often ahead of the batch, and that is no
// change to the batch (the fleet pass of 2026-10-01 18:31 ET: with cards
// accepted every second a landing was refused almost every round, 49 queued and
// 2 landed, and one batch was pushed and could not be reported). A pinned card
// gone from the queue, or reworked to another head, still refuses.
func headWhy(s *sprint.Snapshot, stream string, pins []landCard) string {
	// a stream stopped since land read it (a red recorded while an earlier stream of the
	// same run landed) is not pushed: a stopped stream moves only after resume
	if ctl := s.StreamCtl(stream); ctl != nil && ctl.F("state") == sprint.StreamStopped {
		return "stopped (" + ctl.F("cause") + ") since it was read; run: nova-sprint resume --stream " + stream
	}
	queued := map[string]bool{}
	for _, c := range landQueue(s, stream) {
		queued[c.ID] = true
	}
	for _, c := range pins {
		if !queued[c.id] {
			return fmt.Sprintf("the merge queue of %s no longer holds %s (landed, stuck or returned since it was read); run land again", stream, c.id)
		}
		pr := s.Work.Placed(c.id)
		if pr == nil || pr.F("head") != c.head || pr.F("attempt") != c.attempt {
			return fmt.Sprintf("%s is at attempt %s head %s now, not attempt %s head %s as it was read and built (reworked since); run land again", c.id, dashed(pr.F("attempt")), dashed(pr.F("head")), dashed(c.attempt), dashed(c.head))
		}
	}
	return ""
}

// build cuts the batch branch from origin's base and merges the cards' heads
// in order, stopping at the first the card itself stops (a head that is not
// a commit on origin, or a merge that left unmerged paths): merged is the
// cards merged, failed the card that ended the batch, why a refusal of the
// whole batch that blames no card (git's own failure: the fetch, the cut, an
// identity, a hook, the disk), nothing to report. The fetch's seconds and the
// merges' are added to t.
func (l *lander) build(ctx context.Context, dir, stream string, cards []landCard, t *landTimes) (merged []string, failed conflictCard, why string) {
	base := cards[0].base
	// THE FETCH BRINGS WHAT THE BATCH NEEDS AND NOTHING ELSE: the base, and the cards'
	// heads by their ids, in one exchange. A fetch of every branch of origin costs a
	// negotiation over all of them, once a stream a round: on a repository with two
	// thousand card branches it was 15 s a fetch and landing fell to a third of the
	// fleet's rate (the fleet pass of 2026-10-01 20:18 ET, 1000 cards). A head origin
	// does not hold fails the one fetch; then the base alone is fetched and each head at
	// its merge (mergeHead), which names the card.
	baseRef := "+refs/heads/" + base + ":refs/remotes/origin/" + base
	fetch := []string{"fetch", "--no-tags", "origin", baseRef}
	for _, c := range cards {
		// a fetch by id wants the whole id; a short head is found at its merge
		if shaRE.MatchString(c.head) && (len(c.head) == 40 || len(c.head) == 64) {
			fetch = append(fetch, c.head)
		}
	}
	start := time.Now()
	_, err := l.git(ctx, dir, fetch...)
	if err != nil {
		_, err = l.git(ctx, dir, "fetch", "--no-tags", "origin", baseRef)
	}
	since(&t.Fetch, start)
	if err != nil {
		return nil, failed, "the fetch of origin in " + dir + " failed: " + firstLine("", err)
	}
	start = time.Now()
	defer since(&t.Merge, start)
	if _, err := l.git(ctx, dir, "switch", "--no-track", "--force-create", "land/"+stream, "refs/remotes/origin/"+base); err != nil {
		return nil, failed, "the base " + base + " could not be cut from origin in " + dir + ": " + firstLine("", err)
	}
	for _, c := range cards {
		before, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return nil, failed, "the batch branch has no tip before the merge of " + c.id + ": " + firstLine("", err) + "; no card is blamed and nothing was pushed or reported"
		}
		card, env := l.mergeHead(ctx, dir, stream, c)
		if card == "" && env == "" {
			card, env = l.checkCard(ctx, dir, c, before)
		}
		switch {
		case env != "":
			return nil, failed, env + "; no card is blamed and nothing was pushed or reported"
		case card != "":
			return merged, conflictCard{landCard: c, why: card}, ""
		}
		merged = append(merged, c.id)
	}
	return merged, failed, ""
}

// notOnOrigin is how a remote says it holds no object of that id.
var notOnOrigin = []string{"not our ref", "couldn't find remote ref", "no such remote ref", "unadvertised object"}

// mergeHead merges one card's head onto the batch branch. card is why the
// card itself does not merge (its head is not a commit id, origin holds no
// such commit, or the merge stopped on unmerged paths, aborted); env is why
// git failed for any other reason, which is not the card's (an identity, a
// hook, the disk, the network), with any merge in progress aborted; both ""
// when it merged. A head the clone lacks is fetched from origin by its id once.
func (l *lander) mergeHead(ctx context.Context, dir, stream string, c landCard) (card, env string) {
	if why := headNotCommit(stream, c); why != "" {
		return why, ""
	}
	merge := func() error {
		_, err := l.git(ctx, dir, "merge", "--no-ff", "--no-edit", "-m", "land "+c.id+" (sprint stream "+stream+")", c.head)
		return err
	}
	err := merge()
	if err == nil {
		return "", ""
	}
	if _, missing := l.git(ctx, dir, "cat-file", "-e", c.head+"^{commit}"); missing != nil {
		_, ferr := l.git(ctx, dir, "fetch", "--no-tags", "origin", c.head)
		switch {
		case ferr != nil && containsAny(ferr.Error(), notOnOrigin):
			// before the card is blamed, every branch is fetched once: a head given
			// short (a fetch by id wants the whole id) or one behind its branch's tip on
			// a remote that serves only advertised refs is on origin all the same
			if _, all := l.git(ctx, dir, "fetch", "--no-tags", "origin", "+refs/heads/*:refs/remotes/origin/*"); all != nil {
				return "", "the fetch of origin's branches for the head " + c.head + " of " + c.id + " failed: " + firstLine("", all)
			}
			if _, still := l.git(ctx, dir, "cat-file", "-e", c.head+"^{commit}"); still != nil {
				return "the head " + c.head + " of " + c.id + " is missing: origin holds no such commit (" + firstLine("", ferr) + ")", ""
			}
		case ferr != nil:
			return "", "the fetch of the head " + c.head + " of " + c.id + " failed: " + firstLine("", ferr)
		}
		if err = merge(); err == nil {
			return "", ""
		}
	}
	unmerged, uerr := l.git(ctx, dir, "ls-files", "--unmerged")
	_, inMerge := l.git(ctx, dir, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	if inMerge == nil {
		if _, aerr := l.git(ctx, dir, "merge", "--abort"); aerr != nil {
			return "", "git merge --abort failed after the merge of " + c.id + " stopped: " + firstLine("", aerr)
		}
	}
	if uerr == nil && unmerged != "" {
		return "the head " + c.head + " of " + c.id + " does not merge: " + firstLine("", err), ""
	}
	return "", "the merge of " + c.id + " failed in git, not on its changes: " + firstLine("", err)
}

// checkCard is the lander's mechanical checks of one card merged onto the batch branch
// at before (internal/diffcheck; docs/SPEC-SPRINT.md section 7, the lander's checks): the
// merge's own diff touches no file outside the card's PATHS (E12) and leaves no stranded
// sentence fragment or unmatched backquote (E4). A card that fails is taken off the batch
// branch (reset to before) and ends the batch as a head that does not merge does, with
// what failed; card and env are mergeHead's. A merge that made no commit (the head is in
// the base already) is not checked: it changes nothing the base does not hold.
func (l *lander) checkCard(ctx context.Context, dir string, c landCard, before string) (card, env string) {
	after, err := l.git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || after == before {
		return "", ""
	}
	diff, err := l.git(ctx, dir, "diff", "-M", "--no-color", before, after)
	if err != nil {
		return "", "the diff of the merge of " + c.id + " could not be read: " + firstLine("", err)
	}
	var why []string
	if out := diffcheck.Outside(c.paths, diff); len(out) > 0 {
		why = append(why, "it changes files outside its PATHS (E12): "+strings.Join(out, ", "))
	}
	for _, f := range diffcheck.Fragments(diff) {
		why = append(why, f.String()+" (E4)")
	}
	if len(why) == 0 {
		return "", ""
	}
	if _, err := l.git(ctx, dir, "reset", "-q", "--hard", before); err != nil {
		return "", "the batch branch could not be reset after " + c.id + " failed the lander's checks: " + firstLine("", err)
	}
	return "the head " + c.head + " of " + c.id + " fails the lander's checks: " + strings.Join(why, "; "), ""
}

// containsAny says s holds one of words.
func containsAny(s string, words []string) bool {
	return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(s, w) })
}

// runCheck runs --check in the clone: "" when it passed or there is none.
func (l *lander) runCheck(ctx context.Context, dir string) string {
	if l.check == "" {
		return ""
	}
	b := subproc.Prepare(ctx, landCheckBudget, "sh", "-c", l.check)
	defer b.Cancel()
	b.Cmd.Dir, b.Cmd.Env = dir, l.a.gitEnv
	out, err := b.Cmd.CombinedOutput()
	if err = b.Wrap("check "+l.check, err); err != nil {
		return "the check " + l.check + " failed: " + oneline.Err(err) + checkTail(string(out))
	}
	return ""
}

// checkTail is ": <the output's last line>", "" for no output.
func checkTail(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return ": " + oneline.Cap(l, 300)
	}
	return ""
}

// clone is the directory a batch of the repository lands in: --repo-dir, else
// a clone kept under the land root, made on first use. Every clone reused,
// given or kept, has its origin read and held to the repository the card
// names before any git touches it, so a batch is never pushed to another
// repository's remote (a card naming none takes --repo-dir's as it is; one
// naming none with no --repo-dir is refused before, by placeWhy). why is a
// refusal.
func (l *lander) clone(ctx context.Context, repo string) (dir, why string) {
	if l.repoDir != "" {
		if l.dry {
			return l.repoDir, ""
		}
		return l.repoDir, l.originIs(ctx, l.repoDir, repo)
	}
	if l.root == "" {
		root, err := l.a.landRoot()
		if err != nil {
			return "", "no directory to keep clones in (" + oneline.Err(err) + "); run: nova-sprint land --repo-dir <clone>"
		}
		l.root = root
	}
	dir = filepath.Join(l.root, repoDirName(repo))
	if l.dry {
		return dir, ""
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return dir, l.originIs(ctx, dir, repo)
	}
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return "", "the land directory " + l.root + " could not be made: " + oneline.Err(err)
	}
	// one branch, never every branch: the landing fetches the base and the heads it needs
	// itself (build), and a clone of a card repository's thousands of branches was 80 s
	if _, err := l.git(ctx, "", "clone", "--no-tags", "--single-branch", "--", repo, dir); err != nil {
		return "", "the clone of " + repo + " into " + dir + " failed: " + firstLine("", err)
	}
	return dir, ""
}

// originIs is why the clone's origin is not where the batch belongs, ""
// when it is: its fetch URL is the repository the card names (any, for a
// card naming none), and its effective push URLs, every one git push would
// write to (remote.origin.pushurl, else the fetch URL), are exactly one, the
// same repository.
func (l *lander) originIs(ctx context.Context, dir, repo string) string {
	run := "; nothing was fetched or pushed; run: nova-sprint land --repo-dir <a clone of " + dashed(repo) + " whose origin fetches and pushes there>"
	fetch, err := l.git(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return "the clone " + dir + " has no origin to push to: " + firstLine("", err) + run
	}
	if repo != "" && !sameRepo(fetch, repo) {
		return "the card names the repository " + repo + " and the clone " + dir + " fetches from " + fetch + run
	}
	out, err := l.git(ctx, dir, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return "the push URLs of origin in " + dir + " could not be read: " + firstLine("", err) + run
	}
	pushes := strings.Fields(out)
	if len(pushes) != 1 {
		return fmt.Sprintf("the clone %s pushes origin to %d URLs (%s), and land pushes to one", dir, len(pushes), strings.Join(pushes, ", ")) + run
	}
	if !sameRepo(pushes[0], fetch) {
		return "the clone " + dir + " fetches from " + fetch + " and pushes to " + pushes[0] + run
	}
	return ""
}

// defaultLandRoot is where land keeps its clones: nova-sprint/land in the
// user's cache directory.
func defaultLandRoot() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "nova-sprint", "land"), nil
}

// repoDirName is a repository's clone directory name: its URL or path with
// every character that is not a letter, digit, dot or dash a dash, cut to 48,
// for a person to read, then the first 16 hex digits of the SHA-256 of the
// repository's one spelling (normRepo), so two repositories whose readable
// names agree (a-b/c and a/b-c) never share a clone.
func repoDirName(repo string) string {
	key := normRepo(repo)
	sum := sha256.Sum256([]byte(key))
	name := strings.Trim(notNameRE.ReplaceAllString(key, "-"), "-.")
	if len(name) > 48 {
		name = name[len(name)-48:]
	}
	return strings.TrimLeft(name, "-.") + "-" + hex.EncodeToString(sum[:8])
}

// notNameRE is a run of characters a clone directory's name does not keep.
var notNameRE = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

// normRepo is a repository's one spelling, so an https, an ssh and an scp
// spelling of one repository compare equal: the scheme and the user dropped,
// the host lower-cased (a host has no case), the path kept as it is (a path
// may have case: /srv/Repo.git and /srv/repo.git are two repositories), its
// trailing slash and .git cut. A local path is its path.
func normRepo(u string) string {
	u = strings.TrimSpace(u)
	host, path := "", u
	if i := strings.Index(u, "://"); i >= 0 {
		host, path, _ = strings.Cut(u[i+3:], "/")
	} else if colon := strings.Index(u, ":"); colon > 0 && !strings.Contains(u[:colon], "/") {
		host, path = u[:colon], u[colon+1:] // scp-like: [user@]host:path
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	if host == "" {
		return path
	}
	return strings.ToLower(host) + "/" + strings.TrimPrefix(path, "/")
}

// sameRepo says two spellings name one repository.
func sameRepo(a, b string) bool { return normRepo(a) == normRepo(b) }

// rejected says a failed push was refused by the remote (the base moved, or
// a rule on it), not a push that could not reach it.
func rejected(err error) bool {
	return containsAny(err.Error(), []string{"[rejected]", "[remote rejected]", "non-fast-forward", "fetch first"})
}

// git runs one git in dir (none: the current directory, for a clone into a
// path it names), in the caller's environment, and returns its trimmed
// stdout; an error carries git's own words.
func (l *lander) git(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := gitrun.Run(ctx, gitrun.Options{C: dir, Env: l.a.gitEnv, OwnRepo: dir != ""}, args...)
	if err != nil {
		words := strings.TrimSpace(string(res.Stderr) + "\n" + string(res.Stdout))
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, words)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// firstLine is a failure's words on one line: the error's, else out's.
func firstLine(out string, err error) string {
	if err != nil {
		var lines []string
		for _, l := range strings.Split(err.Error(), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "hint:") {
				lines = append(lines, l)
			}
		}
		return oneline.Cap(strings.Join(lines, " | "), 400)
	}
	if out == "" {
		return "no output"
	}
	return oneline.Cap(strings.ReplaceAll(out, "\n", " | "), 400)
}
