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
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// THE BRIEF DECISION BEFORE ADD (docs/SPEC-NOVA-DECIDE.md section 9; the owner,
// 2026-09-30: "give it all the details it needs, like a real child agent"). Under
// JEV_API_KEY, add asks nova-decide's brief decision of every card it names with a
// brief, after its own checks (the arguments, the files, the card lint) and before it
// writes: is the repository and branch named, where the work is, the gate, the commit
// message, what to report, is the task one thing, which step is ambiguous, how many
// minutes, and p(converges). The whole batch has one deadline (briefDeadline). It runs
// where add is typed: with a server, add runs its checks here first (gateOnly), asks,
// and sends the server the op ids (--brief-op), because the briefs are this machine's
// files and the key is this caller's, and the server's one line of control holds no
// backend call. One BRIEF line per card, marked uncalibrated=true; with the sprint
// row's decide_brief_bar set, a card under it is refused, exit 2, nothing written. A
// decision that cannot be made is one NOTE line and the add goes on. The card stores
// the op and the record (sprint.FieldBriefOp, FieldBriefRecord), and land and drop
// attach the card's end to that exact decision: landed at attempt 1, reworked later,
// dropped.

// briefDeadline bounds the whole batch of an add's brief decisions: past it, what is
// unanswered is a NOTE each and the add goes on.
const briefDeadline = time.Minute

// defaultBriefRecord is the coordinator's record of brief decisions,
// <root>/decide/brief.jsonl with the root ~/nova-sprint: a durable directory, never a
// cache, beside nothing a cleanup removes; add --decide-record names another.
func (a *app) defaultBriefRecord() (string, error) {
	home, err := a.home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "nova-sprint", "decide", "brief.jsonl"), nil
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

// briefAsked is what an add's brief decisions give its cards: each card's op id and
// the record that holds them (sprint.AddReq's BriefOps and BriefRecord).
type briefAsked struct {
	ops    map[string]string
	record string
}

// briefGate asks the brief decision of the cards (id -> brief) as one batch under one
// deadline, after add's own checks; code is non-zero when a card is under the bar and
// the add is refused, nothing written. Inside the server it asks nothing: the caller
// asked before sending and the op ids are its --brief-op words.
func (a *app) briefGate(cards map[string]string, recordFlag string, sent []string, c *common, stdout, stderr io.Writer) (briefAsked, int) {
	if a.serveAddr != "" {
		asked := briefAsked{ops: map[string]string{}, record: recordFlag}
		for _, w := range sent {
			if id, op, ok := strings.Cut(w, "="); ok && cards[id] != "" {
				asked.ops[id] = op
			}
		}
		return asked, 0
	}
	key := a.getenv(decide.JevSecret)
	if key == "" || len(cards) == 0 {
		return briefAsked{}, 0
	}
	record := recordFlag
	if record == "" && a.briefRecord != nil {
		var err error
		if record, err = a.briefRecord(); err != nil {
			record = ""
		}
	}
	out := stdout
	if c.json {
		out = stderr // the add's one JSON object stays alone on stdout
	}
	note := func(what string) { fmt.Fprintf(out, "NOTE brief: %s\n", oneline.Escape(what)) }
	if record == "" {
		return briefAsked{}, 0 // a test's app names no record: no brief decision
	}
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		note("no brief decision: the record: " + err.Error())
		return briefAsked{}, 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), briefDeadline)
	defer cancel()
	var bar decide.BriefBar
	raw, err := a.briefBar(ctx)
	if err == nil {
		bar, err = decide.ParseBriefBar(raw)
	}
	if err != nil {
		note("the sprint row's " + config.FieldDecideBriefBar + " could not be read (" + err.Error() + "); every brief is reported and none refused")
	}
	made, err := decide.Briefs(ctx, a.decideBackend(key), cards, record, a.now(), decide.BriefWidth, 0)
	if err != nil {
		note("no brief decision: " + err.Error())
		return briefAsked{}, 0
	}
	asked := briefAsked{ops: map[string]string{}, record: record}
	var refused []string
	for _, m := range made {
		id := m.Inputs["card"]
		if m.Err != nil {
			note("no brief decision of " + id + ": " + m.Err.Error())
			continue
		}
		asked.ops[id] = m.ID
		b := decide.BriefOf(m.Decision)
		recorded := map[bool]string{true: "existing", false: "new"}[m.Existing]
		fmt.Fprintf(out, "BRIEF card=%s op=%s %s recorded=%s\n", oneline.Field(id), oneline.Field(m.ID), oneline.Escape(b.Line()), oneline.Field(recorded))
		if bar.Refuses(b) {
			refused = append(refused, fmt.Sprintf("%s ranks p(converges)=%.2f, under %s %.2f, failing %s", id, b.Converges,
				config.FieldDecideBriefBar, bar.At, cmp.Or(strings.Join(b.Failed, ", "), "no named question")))
		}
	}
	if len(refused) > 0 {
		return briefAsked{}, refuse(stderr, "add", fmt.Sprintf("the brief of %s; the brief decision is uncalibrated (docs/SPEC-NOVA-DECIDE.md section 9), so this bar is the sprint row's choice, not a measured one; nothing was written; run: nova-config sprint set --decide_brief_bar '' --as <you> to report only, or rewrite the brief and add it again",
			strings.Join(refused, "; ")))
	}
	if a.gateOnly != nil {
		*a.gateOnly = asked
	}
	return asked, 0
}

// gateForward runs add's checks and its brief decisions here before the add is sent to
// the server (briefGate, in gateOnly: add returns before the store): a refusal here is
// the add's and nothing is sent; else the words that carry the op ids to the server. A
// caller with no key asks nothing and sends the add as it is.
func (a *app) gateForward(args []string, stdout, stderr io.Writer) (extra []string, code int) {
	if a.getenv(decide.JevSecret) == "" {
		return nil, 0
	}
	asked := briefAsked{}
	a.gateOnly = &asked
	defer func() { a.gateOnly = nil }()
	if code := a.cmdAdd(args[1:], stdout, stderr); code != 0 {
		return nil, code
	}
	for _, id := range slices.Sorted(maps.Keys(asked.ops)) {
		extra = append(extra, "--brief-op", id+"="+asked.ops[id])
	}
	if len(extra) > 0 && !slices.ContainsFunc(args, func(w string) bool { return w == "--decide-record" || strings.HasPrefix(w, "--decide-record=") }) {
		extra = append(extra, "--decide-record", asked.record)
	}
	return extra, 0
}

// attachBriefs attaches each card's end to the brief decision the card names (its op in
// its record), one write per record; it returns one line per card whose end could not
// be attached. A card that names no decision has none to train.
func (a *app) attachBriefs(cards []briefEnd) []string {
	byRecord := map[string]map[string]decide.End{}
	card := map[string]string{}
	for _, c := range cards {
		if c.op == "" {
			continue
		}
		if byRecord[c.record] == nil {
			byRecord[c.record] = map[string]decide.End{}
		}
		byRecord[c.record][c.op], card[c.op] = c.end, c.id
	}
	var lines []string
	for _, record := range slices.Sorted(maps.Keys(byRecord)) {
		_, failed, err := decide.AttachBriefs(record, byRecord[record], a.now())
		if err != nil {
			lines = append(lines, "the cards' ends were not attached to their brief decisions in "+record+": "+err.Error())
			continue
		}
		for _, op := range slices.Sorted(maps.Keys(failed)) {
			lines = append(lines, fmt.Sprintf("the end of %s (%s) was not attached to its brief decision: %v", card[op], byRecord[record][op].Label, failed[op]))
		}
	}
	return lines
}

// briefEnd is one card's end and the brief decision it names.
type briefEnd struct {
	id, op, record string
	end            decide.End
}

// briefEndOf is the brief decision a card names (sprint.FieldBriefOp, FieldBriefRecord).
func briefEndOf(id string, c *sprint.Card, end decide.End) briefEnd {
	if c == nil {
		return briefEnd{id: id, end: end}
	}
	return briefEnd{id: id, op: c.F(sprint.FieldBriefOp), record: c.F(sprint.FieldBriefRecord), end: end}
}

// droppedBriefs attaches dropped, with the reason, to the brief decision of each card a
// drop moved (a moved line is `<id> <from> -> off the table (<reason>)`, sprint.Drop's),
// read from the card's kept record.
func (a *app) droppedBriefs(ctx context.Context, st *store.Store, res store.Result, reason string) []string {
	var ends []briefEnd
	for _, line := range res.Moved {
		id, rest, _ := strings.Cut(line, " ")
		if !strings.Contains(rest, " -> off the table (") {
			continue
		}
		info, err := st.CardOf(ctx, id)
		if err != nil {
			return []string{"the dropped cards' brief decisions could not be read: " + err.Error()}
		}
		ends = append(ends, briefEndOf(id, info.Primary, decide.End{Label: decide.BriefDropped, Note: reason}))
	}
	return a.attachBriefs(ends)
}
