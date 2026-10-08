package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The roadmap verbs (docs/SPEC-SPRINT.md, "The roadmap: work deferred to a later release";
// sprint.Roadmap): defer moves the waiting cards of a later release off the table into the
// work record's roadmap, each with its whole brief, in one verb; roadmap restore adds one
// back as its twin; roadmap render writes the public ROADMAP.md. On 2026-10-04 the
// coordinator did the first by hand with redis-cli and jq, and a misread state column moved
// cards that were not waiting with the rest: here the state is read off the table, and the
// drop's step refuses any card that is not waiting when it runs.

func init() {
	verbClasses["defer"] = classCoordinator
	verbClasses["roadmap restore"] = classCoordinator
	verbClasses["roadmap render"] = classRead
	fileFlags = append(fileFlags, "record", "out")
}

// roadmapWords is the roadmap verbs' -h past their flags.
const roadmapWords = `the roadmap: work deferred to a later release leaves the sprint into the work
record, one file per product and release, <record>/roadmaps/<product>-<release>.sexp, a
:roadmap form (:product, :releases, :streams, each stream's :cards) that a Lisp reader
reads back. Each card keeps its id, stream, tier, needs, who, repo and whole brief; its
product is the name of its brief's REPO: line.

  nova-sprint defer --release v2 --stream later --expect 3 --record ~/work-record
  nova-sprint roadmap restore later-2 --record ~/work-record
  nova-sprint roadmap render --record ~/work-record

defer takes waiting cards only: a card named in another state is refused, and --stream
or --repo leave the others (LEFT lines). The file is written, read back and counted, and
then the cards are dropped in one step, "deferred to release <name>"; a drop refused puts
the file back as it was. roadmap restore adds a card back as its twin (the old id with the
next letter, replaces=<old id>), its brief byte for byte, and takes it out of the file.
roadmap render writes ROADMAP.md with releases, streams and card counts only: no card id,
brief or name.
`

// roadmapFailed is a roadmap verb that could not do what was asked of a well-formed call
// (a file that does not read or write, a card no roadmap holds): exit 1.
func roadmapFailed(stderr io.Writer, verbName, what string) int {
	fmt.Fprintf(stderr, "%s %s: %s\n", prog, verbName, oneline.WithRemedy(what, prog+" "+verbName+" -h"))
	return 1
}

// roadmapsDir is where the work record keeps its roadmaps.
func roadmapsDir(record string) string { return filepath.Join(record, "roadmaps") }

// cmdDefer is defer --release <name> (<id>... | --stream <s> | --repo <owner/name>)
// [--expect <n>] --record <dir>: the waiting cards chosen are written into
// <dir>/roadmaps/<product>-<release>.sexp under their streams, the file read back and
// counted, and then dropped in one step with the reason "deferred to release <name>". A card
// named that is not waiting is refused, and nothing changes; --stream and --repo take only
// the waiting cards and name the others they leave. --expect refuses a count that differs.
func (a *app) cmdDefer(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("defer")
	release := fs.String("release", "", "the later release the cards are deferred to: a part of the roadmap's file name")
	stream := fs.String("stream", "", "the waiting cards of one stream")
	repo := fs.String("repo", "", "the waiting cards whose brief's REPO: is this owner/name (or names this product)")
	expect := fs.Int("expect", 0, "the number of cards meant: another number found is refused and nothing changes")
	record := fs.String("record", "", "the work record: the roadmap is written under <dir>/roadmaps")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "defer", err.Error())
	}
	ways := 0
	for _, given := range []bool{len(ids) > 0, *stream != "", *repo != ""} {
		if given {
			ways++
		}
	}
	switch {
	case *release == "" || *record == "" || ways != 1:
		return refuse(stderr, "defer", "wants --release <name>, --record <dir> and one of: ids, --stream <s>, --repo <owner/name>")
	case sprint.RoadmapNameWhy("--release", *release) != "":
		return refuse(stderr, "defer", sprint.RoadmapNameWhy("--release", *release))
	case *expect < 0:
		return refuse(stderr, "defer", "--expect wants the number of cards, a whole number from 1")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "defer", err.Error())
	}
	ctx := context.Background()
	s, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return a.readFailed("defer", err, stderr)
	}
	chosen, left, refused := deferChoose(s, ids, *stream, *repo)
	fail := func(lines ...string) int {
		listed(stderr, "REFUSED", refused, c.max, "defer")
		for _, l := range lines {
			fmt.Fprintln(stderr, oneline.Escape(l))
		}
		fmt.Fprintf(stderr, "DEFER FAILED moved=0 refused=%d changed=no; run: nova-sprint view cards --stream <s> and defer the waiting cards\n", len(refused))
		return 1
	}
	if len(refused) > 0 {
		return fail()
	}
	listed(stdout, "LEFT", left, c.max, "defer")
	if *expect > 0 && len(chosen) != *expect {
		var found []string
		for _, rc := range chosen {
			found = append(found, rc.ID)
		}
		return fail(fmt.Sprintf("defer: %d waiting cards found, not %d as --expect says; nothing changed; found: %s", len(chosen), *expect, dashed(strings.Join(found, ","))))
	}
	if len(chosen) == 0 {
		return fail("defer: no waiting card is chosen; nothing changed")
	}
	written, err := writeRoadmaps(roadmapsDir(*record), *release, chosen)
	if err != nil {
		return roadmapFailed(stderr, "defer", err.Error()+"; nothing was dropped")
	}
	r := sprint.DeferReq{Reason: "deferred to release " + *release, Who: c.actor}
	for _, rc := range chosen {
		r.IDs = append(r.IDs, rc.ID)
	}
	step := store.Step{Named: true, Args: store.ArgsOf(r), Verb: "defer", Load: store.All, Mirrors: true, CallerOp: c.op,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Defer(s, r) }}
	res, err := st.Run(ctx, step)
	c.says = append(c.says, res.Said...)
	syncFailed := false
	var syncErr *store.SyncError
	if errors.As(err, &syncErr) {
		syncFailed = true
	}
	switch {
	case err == nil && len(res.Refused) == 0:
		for _, w := range written {
			c.says = append(c.says, fmt.Sprintf("roadmap %s holds %d cards (%d deferred now)", w.path, w.count, w.added))
		}
		c.says = append(c.says, a.droppedBriefs(ctx, st, res, r.Reason)...)
	case errors.Is(err, store.ErrUnknown):
		// the drop may have been written: the roadmap keeps the cards, as a card still on
		// the table is refused by restore
		c.says = append(c.says, "the drop is unknown: the roadmap files keep the cards; read the cards and run defer again for those still waiting")
	case syncFailed:
		// store.SyncError: the write committed and the display cells did not sync.
		// The cards are off the table. Putting the files back would lose the only copy.
		c.says = append(c.says, "the drop is written and the display cells did not sync: the roadmap files keep the cards")
	default:
		c.says = append(c.says, undoRoadmaps(written)...)
	}
	return a.report(ctx, "defer", *c, st, res, err, stdout, stderr)
}

// deferChoose is the cards defer takes, in work order, each as the roadmap keeps it: the named
// cards, each refused unless it is a waiting primary with a REPO: line, or the waiting
// primaries of the stream or the repo, with the others of it left and named.
func deferChoose(s *sprint.Snapshot, ids []string, stream, repo string) (chosen []sprint.RoadmapCard, left, refused []string) {
	repoOf := func(c *sprint.Card) string { v, _ := cardhdr.Value(c.F("brief"), "REPO"); return strings.TrimSpace(v) }
	take := func(c *sprint.Card) {
		r := repoOf(c)
		if r == "" {
			refused = append(refused, c.ID+": its brief names no REPO: line, so no product's roadmap")
			return
		}
		chosen = append(chosen, sprint.RoadmapCardOf(c, r))
	}
	if len(ids) > 0 {
		for _, id := range ids {
			c := s.Work.Placed(id)
			switch {
			case c == nil:
				refused = append(refused, id+": not on the table")
			case sprint.IsSentinel(c):
				refused = append(refused, id+": a sentinel, not a primary")
			case c.Col != sprint.Waiting:
				refused = append(refused, id+": is "+c.Col+", not waiting: only a waiting card is deferred")
			default:
				take(c)
			}
		}
		return chosen, nil, refused
	}
	var open []sprint.State
	for _, st := range sprint.States {
		if sprint.IsOpen(st) {
			open = append(open, st)
		}
	}
	for _, c := range s.Work.Column(open...) {
		if sprint.IsSentinel(c) {
			continue
		}
		if stream != "" && c.Row != stream {
			continue
		}
		if repo != "" {
			r := repoOf(c)
			if r != repo && sprint.RoadmapProduct(r) != repo {
				continue
			}
		}
		if c.Col != sprint.Waiting {
			left = append(left, c.ID+" "+c.Col+": only waiting cards are deferred")
			continue
		}
		take(c)
	}
	return chosen, left, refused
}

// roadmapWrite is one roadmap file defer wrote: what it was before, to undo it.
type roadmapWrite struct {
	path         string
	before       []byte // nil: the file was not there
	count, added int
}

// writeRoadmaps writes the cards into their products' roadmaps of the release, each file
// read back and counted; on any failure every file written is put back as it was.
func writeRoadmaps(dir, release string, cards []sprint.RoadmapCard) ([]roadmapWrite, error) {
	byProduct := map[string][]sprint.RoadmapCard{}
	var products []string
	for _, c := range cards {
		p := sprint.RoadmapProduct(c.Repo)
		if why := sprint.RoadmapNameWhy("the product of "+c.ID, p); why != "" {
			return nil, errors.New(why)
		}
		if byProduct[p] == nil {
			products = append(products, p)
		}
		byProduct[p] = append(byProduct[p], c)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []roadmapWrite
	for _, p := range products {
		w, err := writeRoadmap(filepath.Join(dir, sprint.RoadmapFile(p, release)), p, release, byProduct[p])
		if err != nil {
			undoRoadmaps(written)
			return nil, err
		}
		written = append(written, w)
	}
	return written, nil
}

func writeRoadmap(path, product, release string, cards []sprint.RoadmapCard) (roadmapWrite, error) {
	w := roadmapWrite{path: path, added: len(cards)}
	rm := sprint.Roadmap{Product: product, Releases: []string{release}}
	before, err := os.ReadFile(path)
	switch {
	case err == nil:
		w.before = before
		if rm, err = sprint.ParseRoadmap(before); err != nil {
			return w, fmt.Errorf("the roadmap %s does not read: %w; it is left as it is", path, err)
		}
		if rm.Product != product {
			return w, fmt.Errorf("the roadmap %s is of product %q, not %q; it is left as it is", path, rm.Product, product)
		}
		if !slices.Contains(rm.Releases, release) {
			rm.Releases = append(rm.Releases, release)
		}
	case !errors.Is(err, os.ErrNotExist):
		return w, err
	}
	had := rm.Count()
	if err := rm.Append(cards); err != nil {
		return w, fmt.Errorf("the roadmap %s: %w", path, err)
	}
	if err := writeFileAtomic(path, sprint.FormatRoadmap(rm)); err != nil {
		return w, err
	}
	// read back and counted before anything is dropped
	back, err := os.ReadFile(path)
	if err == nil {
		var got sprint.Roadmap
		if got, err = sprint.ParseRoadmap(back); err == nil {
			err = roadmapHolds(got, had+len(cards), cards)
		}
	}
	if err != nil {
		undoRoadmaps([]roadmapWrite{w})
		return w, fmt.Errorf("the roadmap %s read back wrong: %w; it is put back as it was", path, err)
	}
	w.count = had + len(cards)
	return w, nil
}

// roadmapHolds says the roadmap holds n cards, each of cards as it was written.
func roadmapHolds(rm sprint.Roadmap, n int, cards []sprint.RoadmapCard) error {
	if rm.Count() != n {
		return fmt.Errorf("it holds %d cards, not %d", rm.Count(), n)
	}
	for _, c := range cards {
		got, _ := rm.Find(c.ID)
		if got == nil {
			return fmt.Errorf("%s is not in it", c.ID)
		}
		if fmt.Sprintf("%q", *got) != fmt.Sprintf("%q", c) {
			return fmt.Errorf("%s reads back changed", c.ID)
		}
	}
	return nil
}

// undoRoadmaps puts every file written back as it was, saying what could not be.
func undoRoadmaps(written []roadmapWrite) []string {
	var says []string
	for _, w := range written {
		var err error
		if w.before == nil {
			err = os.Remove(w.path)
		} else {
			err = writeFileAtomic(w.path, w.before)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			says = append(says, "the roadmap "+w.path+" could not be put back: "+err.Error())
		} else {
			says = append(says, "the roadmap "+w.path+" is put back as it was")
		}
	}
	return says
}

// writeFileAtomic writes the file whole or not at all: a temporary file beside it, renamed.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // ignored: the write's error is the one told
	}
	return err
}

// roadmapFiles is every roadmap of the record, by path, in name order.
func roadmapFiles(record string) ([]string, map[string]sprint.Roadmap, error) {
	paths, err := filepath.Glob(filepath.Join(roadmapsDir(record), "*.sexp"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	out := map[string]sprint.Roadmap{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		rm, err := sprint.ParseRoadmap(b)
		if err != nil {
			return nil, nil, fmt.Errorf("the roadmap %s does not read: %w", p, err)
		}
		out[p] = rm
	}
	return paths, out, nil
}

// cmdRoadmapRestore is roadmap restore <id> --record <dir>: the deferred card added back from
// its roadmap as its twin (sprint.Restore), its brief unchanged, then taken out of the file.
func (a *app) cmdRoadmapRestore(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("roadmap restore")
	record := fs.String("record", "", "the work record whose roadmaps hold the card")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "roadmap restore", err.Error())
	}
	if len(ids) != 1 || *record == "" {
		return refuse(stderr, "roadmap restore", "wants one deferred card's id and --record <dir>")
	}
	st, err := a.store(*c) // first: the coordinator's alone, refused before a file is read
	if err != nil {
		return refuse(stderr, "roadmap restore", err.Error())
	}
	paths, rms, err := roadmapFiles(*record)
	if err != nil {
		return roadmapFailed(stderr, "roadmap restore", err.Error())
	}
	var path string
	var card sprint.RoadmapCard
	for _, p := range paths {
		rm := rms[p]
		if got, _ := rm.Find(ids[0]); got != nil {
			path, card = p, *got
			break
		}
	}
	if path == "" {
		return roadmapFailed(stderr, "roadmap restore", "no roadmap under "+roadmapsDir(*record)+" holds "+ids[0]+"; nothing was changed")
	}
	r := sprint.RestoreReq{Card: card, Who: c.actor}
	step := store.Step{Named: true, Args: store.ArgsOf(r), Verb: "roadmap restore", Load: store.All, Mirrors: true,
		Extras: func(s *sprint.Snapshot) map[string][]string {
			old := sprint.RestoreTwin(s, card)
			ids := append(append([]string{card.ID}, card.Needs...), sprint.TwinIDs(old)...)
			return map[string][]string{sprint.Work: ids, sprint.Merge: {sprint.CtlID(card.Stream)}}
		},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Restore(s, r) }}
	c.after = func(_ context.Context, _ *store.Store, res store.Result) []string {
		// The file is rewritten only when the restore moved the card: a step
		// refused whole (Store.Run returns nil error with Refused set) or one
		// that moved nothing leaves the roadmap exactly as it was, so a
		// refused restore can never silently drop the card from the file.
		if len(res.Refused) > 0 || len(res.Moved) == 0 {
			return nil
		}
		rm := rms[path]
		rm.Remove(card.ID)
		var err error
		if rm.Count() == 0 {
			err = os.Remove(path)
		} else {
			err = writeFileAtomic(path, sprint.FormatRoadmap(rm))
		}
		if err != nil {
			return []string{"the card is restored but the roadmap " + path + " still holds " + card.ID + ": " + err.Error() + "; take it out by hand"}
		}
		return []string{"roadmap " + path + " holds " + fmt.Sprint(rm.Count()) + " cards"}
	}
	return a.runStep("roadmap restore", *c, st, step, stdout, stderr)
}

// cmdRoadmapRender is roadmap render --record <dir> [--out <file>]: the public ROADMAP.md
// (sprint.RenderRoadmaps), releases, streams and card counts only.
func (a *app) cmdRoadmapRender(args []string, stdout, stderr io.Writer) int {
	fs, _ := a.verbSetup("roadmap render")
	record := fs.String("record", "", "the work record whose roadmaps are rendered")
	out := fs.String("out", "", "the file written (default: <record>/ROADMAP.md)")
	ids, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "roadmap render", err.Error())
	}
	if len(ids) > 0 || *record == "" {
		return refuse(stderr, "roadmap render", "wants --record <dir> and no ids")
	}
	if *out == "" {
		*out = filepath.Join(*record, "ROADMAP.md")
	}
	paths, rms, err := roadmapFiles(*record)
	if err != nil {
		return roadmapFailed(stderr, "roadmap render", err.Error())
	}
	var all []sprint.Roadmap
	streams, cards := 0, 0
	releases := map[string]bool{}
	for _, p := range paths {
		rm := rms[p]
		all = append(all, rm)
		streams += len(rm.Streams)
		cards += rm.Count()
		for _, r := range rm.Releases {
			releases[rm.Product+" "+r] = true
		}
	}
	text := sprint.RenderRoadmaps(all)
	if old, err := os.ReadFile(*out); err != nil || !bytes.Equal(old, []byte(text)) {
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return roadmapFailed(stderr, "roadmap render", err.Error())
		}
		if err := writeFileAtomic(*out, []byte(text)); err != nil {
			return roadmapFailed(stderr, "roadmap render", err.Error())
		}
	}
	fmt.Fprintf(stdout, "ROADMAP-RENDER OK path=%s releases=%d streams=%d cards=%d\n", oneline.Field(*out), len(releases), streams, cards)
	return 0
}
