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

import (
	"context"
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
    second rejected push as --rejected. The clone is --repo-dir, else the dir=
    each line names; git uses the caller's environment. --dry-run reads the store only.`) + "\n"
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
	// rejected); empty when nothing was reported and the store is unchanged.
	Fact   string `json:"fact,omitempty"`
	Reason string `json:"reason,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
}

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
	if b.Fact != "" {
		l += " fact=" + b.Fact
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

// landCard is one queued card with where it lands: its head, and the
// repository and base its brief names.
type landCard struct {
	id, head, repo, base string
}

// lander is one run of land.
type lander struct {
	a                          *app
	c                          common
	st                         *store.Store
	repoDir, base, check, root string
	dry                        bool
	out                        []landBatch
	steps                      int
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
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "land", err.Error())
	}
	l := &lander{a: a, c: *c, st: st, repoDir: *repoDir, base: *base, check: *check, dry: *dry}
	if *repoDir != "" {
		if abs, err := filepath.Abs(*repoDir); err == nil {
			l.repoDir = abs
		}
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
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
	return l.report(failed, stdout, stderr)
}

// report prints the batches and the summary: exit 0 when every batch
// landed, 1 when one was refused, 2 when a push landed and its report did not.
func (l *lander) report(failed bool, stdout, stderr io.Writer) int {
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
		// ignored: a map of strings, numbers and plain structs of strings always encodes
		b, _ := json.Marshal(map[string]any{"verb": "land", "status": status, "exit": code, "batches": batches, "cards": cards,
			"refused": refused, "dry_run": l.dry, "items": out})
		fmt.Fprintln(stdout, string(b))
		return code
	}
	for _, b := range l.out {
		w := stdout
		if b.Status != "ok" {
			w = stderr
		}
		fmt.Fprintln(w, b.line())
		switch {
		case b.Fact != "":
			fmt.Fprintf(w, "NOTE the stream is stopped (%s); run: nova-sprint inbox\n", b.Fact)
		case b.Status == "refused" && !l.dry:
			fmt.Fprintf(w, "NOTE nothing was pushed or reported for stream %s; its cards stay queued\n", oneline.Field(b.Stream))
		}
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
			lc.head = pr.F("head")
			cb := swarm.ReadCardBase([]byte(pr.F("brief")))
			lc.repo = cb.Repo
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
	switch {
	case b.Base == "":
		return refuse("card " + ids[0] + " names no BASE: line and no --base was given; run: nova-sprint land --stream " + stream + " --base <branch>")
	case strings.HasPrefix(b.Base, "-"):
		return refuse("card " + ids[0] + " names the base " + b.Base + ", which is not a branch name; run: nova-sprint card " + ids[0])
	}
	dir, why := l.clone(ctx, b.Repo)
	if why != "" {
		return refuse(why)
	}
	b.Dir = dir
	if l.dry {
		b.Status = "ok"
		l.out = append(l.out, b)
		return true, true
	}
	if out, err := l.git(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err != nil || out != "" {
		return refuse("the clone " + dir + " is not clean (" + firstLine(out, err) + "); commit or discard its changes, then run land again")
	}
	merged, failed, why := l.build(ctx, dir, stream, cards)
	if why != "" {
		return refuse(why)
	}
	for attempt := 1; len(merged) > 0; attempt++ {
		if why := l.runCheck(ctx, dir); why != "" {
			b.Cards, b.IDs = len(merged), ids[:len(merged)]
			return l.fact(b, sprint.MergeReq{Stream: stream, Batch: len(merged), Red: true, Note: why}, "red", why)
		}
		if why := l.queueHead(ctx, stream, ids[:len(merged)]); why != "" {
			return refuse(why)
		}
		if l.a.beforePush != nil {
			l.a.beforePush(attempt)
		}
		tip, err := l.git(ctx, dir, "rev-parse", "HEAD")
		if err != nil {
			return refuse("the batch branch has no tip: " + firstLine("", err))
		}
		_, err = l.git(ctx, dir, "push", "--porcelain", "origin", "HEAD:refs/heads/"+b.Base)
		if err == nil {
			b.Tip = tip
			if !l.landed(ctx, b, stream, ids[:len(merged)]) {
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
			return l.fact(b, sprint.MergeReq{Stream: stream, Batch: len(merged), Rejected: true, Note: firstLine("", err)}, "rejected", "the push to "+b.Base+" was rejected again after a rebuild on the moved base: "+firstLine("", err))
		}
		merged, failed, why = l.build(ctx, dir, stream, cards)
		if why != "" {
			return refuse(why)
		}
	}
	l.conflict(stream, failed)
	return false, true
}

// conflictCard is a card that did not merge, with git's words.
type conflictCard struct{ id, why string }

// conflict reports the card that ended its batch with the conflict fact: it
// is the head of the queue now, so the step's batch is that one card.
func (l *lander) conflict(stream string, f conflictCard) {
	b := landBatch{Stream: stream, Status: "refused", Cards: 1, IDs: []string{f.id}}
	l.fact(b, sprint.MergeReq{Stream: stream, Batch: 1, Conflict: f.id, Note: f.why}, "conflict", f.why)
}

// fact reports a fact that stops the stream through the merge step, and the
// batch refused with it.
func (l *lander) fact(b landBatch, r sprint.MergeReq, fact, why string) (bool, bool) {
	b.Status, b.Fact, b.Reason = "refused", fact, why
	r.Who = l.c.actor
	res, err := l.step(r)
	if code := stepExit(res, err); code != 0 {
		b.Fact = ""
		b.Reason = why + "; the merge step did not record it (" + stepWhy(res, err) + "); run: nova-sprint merge --stream " + r.Stream + " " + factFlag(r)
	}
	l.out = append(l.out, b)
	return false, true
}

// factFlag is the merge verb's flag for a request's fact.
func factFlag(r sprint.MergeReq) string {
	switch {
	case r.Conflict != "":
		return "--batch 1 --conflict " + r.Conflict
	case r.Red:
		return "--batch " + strconv.Itoa(r.Batch) + " --red"
	}
	return "--batch " + strconv.Itoa(r.Batch) + " --rejected"
}

// landed reports a pushed batch through the merge step; false (and the line
// FAILED, with the merge command to run) when the store did not take it.
func (l *lander) landed(ctx context.Context, b landBatch, stream string, ids []string) bool {
	b.Cards, b.IDs = len(ids), ids
	run := "nova-sprint merge --stream " + stream + " --batch " + strconv.Itoa(len(ids))
	if why := l.queueHead(ctx, stream, ids); why != "" {
		b.Status, b.Reason = "failed", "the batch was pushed to "+b.Base+" at "+b.Tip+" and NOT reported: "+why+"; land the pushed cards by hand: "+run
		l.out = append(l.out, b)
		return false
	}
	res, err := l.step(sprint.MergeReq{Stream: stream, Batch: len(ids), Who: l.c.actor})
	if code := stepExit(res, err); code != 0 || len(res.Moved) != len(ids) {
		b.Status, b.Reason = "failed", "the batch was pushed to "+b.Base+" at "+b.Tip+" and the merge report failed ("+stepWhy(res, err)+"); run: "+run
		l.out = append(l.out, b)
		return false
	}
	b.Status = "ok"
	l.out = append(l.out, b)
	return true
}

// step runs one merge step, as `merge --stream` runs it, under an op id of
// the caller's --op when it gave one.
func (l *lander) step(r sprint.MergeReq) (store.Result, error) {
	step := store.MergeStep(r)
	l.steps++
	if l.c.op != "" {
		step.CallerOp = l.c.op + "." + r.Stream + "." + strconv.Itoa(l.steps)
	}
	if l.c.epoch >= 0 {
		e := uint64(l.c.epoch)
		step.Epoch = &e
	}
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

// queueHead is why the stream's merge queue no longer starts with ids, ""
// when it does: the merge step lands the first n queued, so a queue changed
// since land read it would land other cards.
func (l *lander) queueHead(ctx context.Context, stream string, ids []string) string {
	s, err := l.st.Load(ctx, []string{sprint.Merge}, nil)
	if err != nil {
		return "the merge queue could not be read again: " + oneline.Err(err)
	}
	q := landQueue(s, stream)
	if len(q) < len(ids) {
		return fmt.Sprintf("the merge queue of %s holds %d cards now, fewer than the batch of %d", stream, len(q), len(ids))
	}
	for i, id := range ids {
		if q[i].ID != id {
			return fmt.Sprintf("the merge queue of %s changed since it was read (%s is now where %s was); run land again", stream, q[i].ID, id)
		}
	}
	return ""
}

// build cuts the batch branch from origin's base and merges the cards' heads
// in order, stopping at the first that is missing or conflicts: merged is
// the cards merged, failed the card that ended the batch, why a refusal of
// the whole batch (nothing to report).
func (l *lander) build(ctx context.Context, dir, stream string, cards []landCard) (merged []string, failed conflictCard, why string) {
	base := cards[0].base
	if _, err := l.git(ctx, dir, "fetch", "--no-tags", "origin"); err != nil {
		return nil, failed, "the fetch of origin in " + dir + " failed: " + firstLine("", err)
	}
	if _, err := l.git(ctx, dir, "switch", "--no-track", "--force-create", "land/"+stream, "refs/remotes/origin/"+base); err != nil {
		return nil, failed, "the base " + base + " could not be cut from origin in " + dir + ": " + firstLine("", err)
	}
	for _, c := range cards {
		if why := l.mergeHead(ctx, dir, stream, c); why != "" {
			return merged, conflictCard{id: c.id, why: why}, ""
		}
		merged = append(merged, c.id)
	}
	return merged, failed, ""
}

// mergeHead merges one card's head onto the batch branch: "" when it merged,
// else why it did not, the merge aborted. A head the clone lacks is fetched
// from origin by its id once.
func (l *lander) mergeHead(ctx context.Context, dir, stream string, c landCard) string {
	if !shaRE.MatchString(c.head) {
		return "the head " + dashed(c.head) + " of " + c.id + " is not a commit id; run: nova-sprint card " + c.id
	}
	merge := func() error {
		_, err := l.git(ctx, dir, "merge", "--no-ff", "--no-edit", "-m", "land "+c.id+" (sprint stream "+stream+")", c.head)
		return err
	}
	err := merge()
	if err == nil {
		return ""
	}
	if _, missing := l.git(ctx, dir, "cat-file", "-e", c.head+"^{commit}"); missing != nil {
		if _, err := l.git(ctx, dir, "fetch", "--no-tags", "origin", c.head); err != nil {
			return "the head " + c.head + " of " + c.id + " is missing: not on origin (" + firstLine("", err) + ")"
		}
		if err = merge(); err == nil {
			return ""
		}
	}
	why := "the head " + c.head + " of " + c.id + " does not merge: " + firstLine("", err)
	if _, abortErr := l.git(ctx, dir, "merge", "--abort"); abortErr != nil {
		why += "; and git merge --abort failed: " + firstLine("", abortErr)
	}
	return why
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

// clone is the directory a batch of the repository lands in: --repo-dir,
// whose origin must be the repository a card names; else a clone kept under
// the land root, made on first use. why is a refusal.
func (l *lander) clone(ctx context.Context, repo string) (dir, why string) {
	if l.repoDir != "" {
		if repo == "" || l.dry {
			return l.repoDir, ""
		}
		origin, err := l.git(ctx, l.repoDir, "remote", "get-url", "origin")
		if err != nil {
			return "", "the clone " + l.repoDir + " has no origin: " + firstLine("", err)
		}
		if sameRepo(origin, repo) {
			return l.repoDir, ""
		}
		return "", "the card names the repository " + repo + " and the clone " + l.repoDir + " is of " + origin + "; run: nova-sprint land with --repo-dir a clone of " + repo
	}
	if repo == "" {
		return "", "the card names no REPO: line and no --repo-dir was given; run: nova-sprint land --repo-dir <clone>"
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
		return dir, ""
	}
	if err := os.MkdirAll(l.root, 0o755); err != nil {
		return "", "the land directory " + l.root + " could not be made: " + oneline.Err(err)
	}
	if _, err := l.git(ctx, "", "clone", "--no-tags", "--", repo, dir); err != nil {
		return "", "the clone of " + repo + " into " + dir + " failed: " + firstLine("", err)
	}
	return dir, ""
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

// repoDirName is a repository's clone directory name: its URL or path, every
// character that is not a letter, digit, dot or dash a dash.
func repoDirName(repo string) string {
	return strings.Trim(notNameRE.ReplaceAllString(normRepo(repo), "-"), "-.")
}

// notNameRE is a run of characters a clone directory's name does not keep.
var notNameRE = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

// normRepo is a repository's URL or path without its scheme, user, trailing
// slash and .git, so an https and an ssh spelling of one repository compare
// equal.
func normRepo(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	} else if at := strings.Index(u, "@"); at >= 0 && strings.Contains(u[at:], ":") {
		u = strings.Replace(u, ":", "/", 1) // scp-like git@host:owner/name
	}
	if at := strings.Index(u, "@"); at >= 0 && at < strings.Index(u+"/", "/") {
		u = u[at+1:]
	}
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	return strings.ToLower(u)
}

// sameRepo says two spellings name one repository.
func sameRepo(a, b string) bool { return normRepo(a) == normRepo(b) }

// rejected says a failed push was refused by the remote (the base moved, or
// a rule on it), not a push that could not reach it.
func rejected(err error) bool {
	msg := err.Error()
	for _, w := range []string{"[rejected]", "[remote rejected]", "non-fast-forward", "fetch first"} {
		if strings.Contains(msg, w) {
			return true
		}
	}
	return false
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
