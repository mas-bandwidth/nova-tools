package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// THE BRIEF DECISION BEFORE ADD (docs/SPEC-NOVA-DECIDE.md section 9; the owner,
// 2026-09-30: "give it all the details it needs, like a real child agent"). Under
// JEV_API_KEY, add asks nova-decide's brief decision of every card it names, before
// anything is sent or written: is the repository and branch named, the files and lines,
// the gate, the commit message, what to report, is the task one thing, which step is
// ambiguous, how many minutes, and p(converges). It runs where add is typed, ahead of
// the server, because the briefs are that machine's files and the key is that caller's
// (the server's one line of control holds no backend call). One BRIEF line per card;
// with the sprint row's decide_brief_bar set, a card whose p(converges) is under it is
// refused, exit 2, nothing written, naming the questions it failed. A decision that
// cannot be made (the bar, the record or the backend) is one NOTE line and the add goes
// on. A card's end attaches to its brief as the decision's outcome: landed at attempt
// 1, reworked at a later attempt, or dropped, so the bar is read from what converged.

// briefWidth is how many briefs add asks at once, and briefWait how long the backend
// may take over one, as nova-decide's own --timeout default.
const (
	briefWidth = 16
	briefWait  = time.Minute
)

// defaultBriefRecord is the record of brief decisions: nova-decide/brief.jsonl under
// the user's cache directory, where add records each card's brief and land and drop
// attach its end.
func defaultBriefRecord() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nova-decide", "brief.jsonl"), nil
}

// readBriefBar is the sprint row's decide_brief_bar, read from nova-config.
func (a *app) readBriefBar(ctx context.Context) (string, error) {
	var bar string
	err := a.withConfig(ctx, "", func(ctx context.Context, st config.Store) error {
		row, found, err := st.Get(ctx, config.KindSprint, config.KindSprint)
		if found {
			bar = row.Fields[config.FieldDecideBriefBar]
		}
		return err
	})
	return bar, err
}

// briefGate asks the brief decision of every card an add names, when the environment
// holds the backend's key and the process has a record; stop is true when it refused
// the add. It never runs inside the server (the caller asked before sending).
func (a *app) briefGate(args []string, stdout, stderr io.Writer) (code int, stop bool) {
	v := readVerb(args)
	key := a.getenv(decide.JevSecret)
	if v.name != "add" || v.help || v.err != nil || key == "" || a.briefRecord == nil || a.serveAddr != "" {
		return 0, false
	}
	cards := addBriefs(args[v.words:])
	if len(cards) == 0 {
		return 0, false
	}
	out := stdout
	if v.on("json") {
		out = stderr // the add's one JSON object stays alone on stdout
	}
	note := func(what string) { fmt.Fprintf(out, "NOTE brief: %s\n", oneline.Escape(what)) }
	ctx := context.Background()
	var bar decide.BriefBar
	raw, err := a.briefBar(ctx)
	if err == nil {
		bar, err = decide.ParseBriefBar(raw)
	}
	if err != nil {
		note("the sprint row's " + config.FieldDecideBriefBar + " could not be read (" + err.Error() + "); every brief is reported and none refused")
	}
	record, err := a.briefRecord()
	if err == nil {
		err = os.MkdirAll(filepath.Dir(record), 0o755)
	}
	if err != nil {
		note("no brief decision: the record: " + err.Error())
		return 0, false
	}
	made, err := decide.Briefs(ctx, a.decideBackend(key), cards, record, a.now(), briefWidth, briefWait)
	if err != nil {
		note("no brief decision: " + err.Error())
		return 0, false
	}
	var refused []string
	for _, m := range made {
		id := m.Inputs["card"]
		if m.Err != nil {
			note("no brief decision of " + id + ": " + m.Err.Error())
			continue
		}
		b := decide.BriefOf(m.Decision)
		recorded := map[bool]string{true: "existing", false: "new"}[m.Existing]
		fmt.Fprintf(out, "BRIEF card=%s op=%s %s recorded=%s\n", oneline.Field(id), oneline.Field(m.ID), oneline.Escape(b.Line()), oneline.Field(recorded))
		if bar.Refuses(b) {
			refused = append(refused, fmt.Sprintf("%s converges at p=%.2f, under %s %.2f, failing %s", id, b.Converges,
				config.FieldDecideBriefBar, bar.At, cmp.Or(strings.Join(b.Failed, ", "), "no named question")))
		}
	}
	if len(refused) == 0 {
		return 0, false
	}
	return refuse(stderr, "add", fmt.Sprintf("the brief of %s; a card is a flash child's whole brief: say what it lacks and add it again; nothing was written; run: nova-decide brief --card <file> --backend jev --record <file>",
		strings.Join(refused, "; "))), true
}

// addBriefs is the cards an add's arguments name with a brief, id -> brief as add stores
// it: a card per brief file (--brief-dir, or --brief-file alone or again), or the one
// brief (--brief, or one --brief-file) of each id named. A --count add names no ids
// before the store numbers them, a sentinel carries no brief, and a file add cannot read
// is the verb's to refuse: none of them is asked.
func addBriefs(args []string) map[string]string {
	fs := verbFlags("add")
	if fs == nil {
		return nil
	}
	ids, err := parse(fs, args)
	if err != nil {
		return nil
	}
	str := func(name string) string { return fs.Lookup(name).Value.String() }
	files := *fs.Lookup("brief-file").Value.(*stringList)
	dir, brief, counted, sentinel := str("brief-dir"), str("brief"), str("count") != "0", str("sentinel") != ""
	cards := map[string]string{}
	if dir != "" || len(files) > 1 || len(files) == 1 && len(ids) == 0 && !counted && !sentinel {
		if dir != "" {
			files, _ = filepath.Glob(filepath.Join(dir, "*.md"))
		}
		for _, f := range files {
			if text, err := readTextFile(f, briefReadCap); err == nil && strings.TrimSpace(text) != "" {
				cards[strings.TrimSuffix(filepath.Base(f), ".md")] = strings.TrimSuffix(text, "\n")
			}
		}
		return cards
	}
	if len(files) == 1 {
		if text, err := readBriefFile(files[0]); err == nil {
			brief = text
		}
	}
	if brief == "" || sentinel {
		return nil
	}
	for _, id := range ids {
		cards[id] = brief
	}
	return cards
}

// attachBriefs attaches each card's end to its brief decision in the record, in one
// write (a card added with no brief decision has none, and nothing is attached); it
// returns one line per card whose end could not be attached.
func (a *app) attachBriefs(ends map[string]decide.End) []string {
	if a.briefRecord == nil || len(ends) == 0 {
		return nil
	}
	record, err := a.briefRecord()
	if err != nil {
		return []string{"the brief record: " + err.Error()}
	}
	_, failed, err := decide.AttachBriefs(record, ends, a.now())
	if err != nil {
		return []string{"the cards' ends were not attached to their brief decisions: " + err.Error()}
	}
	var lines []string
	for _, id := range slices.Sorted(maps.Keys(failed)) {
		lines = append(lines, fmt.Sprintf("the end of %s (%s) was not attached to its brief decision: %v", id, ends[id].Label, failed[id]))
	}
	return lines
}

// droppedBriefs attaches dropped, with the reason, to the brief of each card a drop
// moved (a moved line is `<id> <from> -> off the table (<reason>)`, sprint.Drop's).
func (a *app) droppedBriefs(res store.Result, reason string) []string {
	ends := map[string]decide.End{}
	for _, line := range res.Moved {
		if id, rest, _ := strings.Cut(line, " "); strings.Contains(rest, " -> off the table (") {
			ends[id] = decide.End{Label: decide.BriefDropped, Note: reason}
		}
	}
	return a.attachBriefs(ends)
}
