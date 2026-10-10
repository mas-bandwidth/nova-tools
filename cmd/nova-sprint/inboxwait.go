package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// inbox --wait and --push: the coordinator is woken by what is new for it, and
// never by a judgment it holds (the owner, 2026-10-02: "push notifications for
// inbox from nova-sprint so she doesn't have to poll"; nova-tools#5096). What
// is new is a judgment, or a note addressed to the coordinator, whose note id
// the wait had not seen: a held or waited judgment stays open and is never
// new again, so a sentinel held on purpose wakes nobody. The wait sleeps on the
// tick-end note as before (store.WaitTickEnd on the store; the log's tick-end
// notes polled through the server), looks at the inbox at each one and every
// waitLook while nothing ticks (the machine stopping is seen within it), and
// ends at the first look that finds something new, or when the machine stops
// having run, or when --timeout passes.
//
// --push <dir> keeps running: each new note is written once to
// <dir>/<note id>.md, the group as inbox --open prints it and the clock, and
// the files in the directory are the cursor: a note with a file is not written
// again, so a restart pushes nothing twice and misses nothing. It runs until
// it is interrupted; the machine stopping is one line and the loop goes on,
// since its supervisor would only start it again.

// waitLook is the longest inbox --wait goes between two looks at the inbox
// while nothing ticks.
const waitLook = 15 * time.Second

// tickEndPoll is how often inbox --wait through the server reads the log for a tick end.
const tickEndPoll = time.Second

// machineRunning is the machine line of a running machine (store.MachineLine).
const machineRunning = "machine: running"

// lineRunning says a machine line is a running machine's: "machine: running",
// or with its late tick, "machine: running (tick late 16s)", which is no stop
// (docs/SPEC-SPRINT.md section 14).
func lineRunning(line string) bool {
	return line == machineRunning || strings.HasPrefix(line, machineRunning+" (")
}

// inboxSource is where inbox --wait reads: the store itself, or the sprint's
// server. Each read is one exchange, and a source holds nothing between them.
type inboxSource interface {
	// tickEnd blocks until a tick-end note comes after the last one it saw,
	// or for at most d.
	tickEnd(ctx context.Context, d time.Duration) error
	// inbox is the inbox's groups, the machine's line and the seat's holder;
	// the group open names is read whole (its members and needs), as inbox
	// --open reads it.
	inbox(ctx context.Context, open string) (inboxLook, error)
}

// inboxLook is one look at the inbox: its groups, the machine's line and who
// holds the seat (whose inbox --push seat writes to).
type inboxLook struct {
	groups  []sprint.Group
	machine string
	holder  string
}

// storeSource is inbox --wait on the store.
type storeSource struct {
	st              *store.Store
	redis           string // the resolved direct endpoint, carried into proof replies
	after           string // the notes' tail at the last tick end seen
	deadline, stale time.Duration
}

func (a *app) storeSource(ctx context.Context, st *store.Store, redis string, deadline, stale time.Duration) (*storeSource, error) {
	_, after, err := st.B.Tails(ctx)
	return &storeSource{st: st, redis: redis, after: after, deadline: deadline, stale: stale}, err
}

func (s *storeSource) tickEnd(ctx context.Context, d time.Duration) error {
	woke, err := s.st.WaitTickEnd(ctx, s.after, d)
	if err != nil || !woke {
		return err
	}
	_, s.after, err = s.st.B.Tails(ctx)
	return err
}

func (s *storeSource) inbox(ctx context.Context, _ string) (inboxLook, error) {
	v, err := s.st.Inbox(ctx, s.deadline, s.stale, 10000)
	if err != nil {
		return inboxLook{}, err
	}
	holder, err := s.st.B.Coordinator(ctx)
	// every group carries its members and needs
	return inboxLook{groups: v.Groups, machine: s.st.MachineLine(ctx), holder: holder}, err
}

// exitErr is a source's failure already said on stderr, with its exit code.
type exitErr struct{ code int }

func (e *exitErr) Error() string { return fmt.Sprintf("exit %d", e.code) }

// serverSource is inbox --wait through the sprint's server, which never runs a
// wait: the log's tick-end notes (log --json, as far back as the wait and a
// poll) are read every tickEndPoll on this process's clock, and the inbox is
// read as inbox --json.
type serverSource struct {
	a       *app
	addr    string
	plain   []string // the verb's words without its wait flags
	timeout time.Duration
	from    string // the last tick-end note id seen
	stdout  io.Writer
	stderr  io.Writer
}

func (a *app) serverSource(ctx context.Context, addr string, fs *flag.FlagSet, args []string, timeout time.Duration, stdout, stderr io.Writer) (*serverSource, error) {
	s := &serverSource{a: a, addr: addr, plain: without(fs, args, "wait", "timeout", "push", "json"), timeout: timeout, stdout: stdout, stderr: stderr}
	var err error
	s.from, err = s.lastTickEnd(ctx)
	return s, err
}

// lastTickEnd is the id of the last tick-end note on the server's log.
func (s *serverSource) lastTickEnd(ctx context.Context) (string, error) {
	res, err := s.a.ask(ctx, s.addr, []string{"log"}, []string{"--json", "--since", (s.timeout + tickEndPoll).String()})
	if err != nil {
		return "", &exitErr{s.a.unanswered("inbox --wait", s.addr, err, s.stderr)}
	}
	if res.Code != 0 {
		s.a.answer(res, s.stdout, s.stderr)
		return "", &exitErr{res.Code}
	}
	var log struct {
		Lines []sprint.Line `json:"lines"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &log); err != nil {
		return "", fmt.Errorf("the server's log is not JSON: %w", err)
	}
	last := ""
	for _, l := range log.Lines {
		if l.Note != nil && l.Note.Type == sprint.NTickEnd {
			last = l.Note.ID
		}
	}
	return last, nil
}

func (s *serverSource) tickEnd(ctx context.Context, d time.Duration) error {
	for end := s.a.now().Add(d); s.a.now().Before(end) && ctx.Err() == nil; {
		s.a.sleep(min(end.Sub(s.a.now()), tickEndPoll))
		last, err := s.lastTickEnd(ctx)
		if err != nil {
			return err
		}
		if last != "" && last != s.from {
			s.from = last
			return nil
		}
	}
	return nil
}

func (s *serverSource) inbox(ctx context.Context, open string) (inboxLook, error) {
	words := append(append([]string{}, s.plain...), "--json")
	if open != "" {
		words = append(words, "--open", open)
	}
	res, err := s.a.ask(ctx, s.addr, []string{"inbox"}, words)
	if err != nil {
		return inboxLook{}, &exitErr{s.a.unanswered("inbox", s.addr, err, s.stderr)}
	}
	if res.Code == 1 && open != "" {
		return inboxLook{}, nil // the group closed since the look that found it: nothing to read
	}
	if res.Code != 0 {
		s.a.answer(res, s.stdout, s.stderr)
		return inboxLook{}, &exitErr{res.Code}
	}
	var out struct {
		Groups      []sprint.Group `json:"groups"`
		Open        []string       `json:"open"`
		Needs       []string       `json:"needs"`
		Machine     string         `json:"machine"`
		Coordinator string         `json:"coordinator"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		return inboxLook{}, fmt.Errorf("the server's inbox is not JSON: %w", err)
	}
	for i := range out.Groups {
		if out.Groups[i].ID == open {
			out.Groups[i].Members, out.Groups[i].Needs = out.Open, out.Needs
		}
	}
	return inboxLook{groups: out.Groups, machine: out.Machine, holder: out.Coordinator}, nil
}

// forCoordinator says the group wakes the coordinator: a judgment, or a note
// addressed to someone (the sprint is done, the tick failed).
func forCoordinator(g sprint.Group) bool { return g.Kind == sprint.Judgment || g.To != "" }

// noteKeys is what tells a group's notes apart: each note's id, or the
// group's own id for one computed at read time (stale:<stream>,
// machine:silent), which has no note; each as noteKey spells it.
func noteKeys(g sprint.Group) []string {
	ids := g.Notes
	if len(ids) == 0 {
		ids = []string{g.ID}
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = noteKey(id)
	}
	return keys
}

// noteKey is a note's id as a file name's stem: the one character a file name
// cannot carry everywhere (the ':' of a group computed at read time) replaced.
func noteKey(id string) string { return strings.ReplaceAll(id, ":", "-") }

// seenKeys is every key of the groups that wake the coordinator.
func seenKeys(groups []sprint.Group) map[string]bool {
	seen := map[string]bool{}
	for _, g := range groups {
		if forCoordinator(g) {
			for _, k := range noteKeys(g) {
				seen[k] = true
			}
		}
	}
	return seen
}

// unseen is the groups that wake the coordinator and hold a key not in seen,
// in the inbox's order.
func unseen(seen map[string]bool, groups []sprint.Group) []sprint.Group {
	var out []sprint.Group
	for _, g := range groups {
		if !forCoordinator(g) {
			continue
		}
		for _, k := range noteKeys(g) {
			if !seen[k] {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

// idsOf is the groups' ids, what --open and --group take.
func idsOf(groups []sprint.Group) []string {
	ids := make([]string, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	return ids
}

// waitNew blocks until a look at the inbox finds something new (fresh: what
// the look holds that the caller has not seen), or the machine stops (running
// says it ran at the last look), or timeout passes; an interrupt ends it as a
// timeout does. It returns the groups with something new and its last look
// (its machine line running when it looked at nothing).
func (a *app) waitNew(ctx context.Context, src inboxSource, fresh func(inboxLook) ([]sprint.Group, error), running bool, timeout time.Duration) ([]sprint.Group, inboxLook, error) {
	var look inboxLook
	if running {
		look.machine = machineRunning
	}
	for end := a.now().Add(timeout); ; {
		left := end.Sub(a.now())
		if left <= 0 || ctx.Err() != nil {
			return nil, look, nil
		}
		if err := src.tickEnd(ctx, min(left, waitLook)); err != nil && ctx.Err() == nil {
			return nil, look, err
		}
		if ctx.Err() != nil {
			return nil, look, nil
		}
		l, err := src.inbox(ctx, "")
		if err != nil {
			return nil, look, err
		}
		look = l
		groups, err := fresh(look)
		if err != nil || len(groups) > 0 || (running && !lineRunning(look.machine)) {
			return groups, look, err
		}
	}
}

// seenFresh is the fresh of a wait that wakes for what seen does not hold.
func seenFresh(seen map[string]bool) func(inboxLook) ([]sprint.Group, error) {
	return func(l inboxLook) ([]sprint.Group, error) { return unseen(seen, l.groups), nil }
}

// waitFailed says why the wait failed and is its exit code.
func (a *app) waitFailed(err error, stderr io.Writer) int {
	var e *exitErr
	if errors.As(err, &e) {
		return e.code // said by the source
	}
	return a.readFailed("inbox --wait", err, stderr)
}

// wokeLine is what inbox --wait says of how its wait ended, before the inbox:
// the new groups by id, the machine stopped, or nothing new in the timeout.
func wokeLine(fresh []sprint.Group, stopped bool, timeout time.Duration) string {
	switch {
	case len(fresh) > 0:
		return "inbox --wait: new=" + strings.Join(idsOf(fresh), ",")
	case stopped:
		return "inbox --wait: the machine stopped"
	}
	return "inbox --wait: nothing new in " + timeout.String()
}

// sayWoke says how the wait ended, where inbox --wait says it: on stdout in
// the plain rendering; under --json only a wait that woke for nothing is said,
// on stderr (stdout stays one JSON object, which carries woke and new).
func sayWoke(fresh []sprint.Group, stopped bool, timeout time.Duration, asJSON bool, stdout, stderr io.Writer) {
	switch {
	case !asJSON:
		fmt.Fprintln(stdout, wokeLine(fresh, stopped, timeout))
	case len(fresh) == 0 && !stopped:
		fmt.Fprintln(stderr, wokeLine(fresh, stopped, timeout))
	}
}

// inboxWaitAt is inbox --wait through the sprint's server, which never runs a
// wait: the wait runs here on a serverSource, then the inbox is read through
// the server and printed as inbox --wait prints it (sayWoke; woke and new
// under --json).
func (a *app) inboxWaitAt(addr string, fs *flag.FlagSet, args []string, atEpoch int64, timeout time.Duration, push string, asJSON bool, stdout, stderr io.Writer) int {
	if atEpoch >= 0 || timeout <= 0 {
		return refuse(stderr, "inbox", "--wait waits on the sprint's epoch for at most a --timeout above zero")
	}
	ctx := context.Background()
	src, err := a.serverSource(ctx, addr, fs, args, timeout, stdout, stderr)
	if err != nil {
		return a.waitFailed(err, stderr)
	}
	if push != "" {
		return a.pushLoop(ctx, src, push, timeout, asJSON, stdout, stderr)
	}
	first, err := src.inbox(ctx, "")
	if err != nil {
		return a.waitFailed(err, stderr)
	}
	fresh, after, err := a.waitNew(ctx, src, seenFresh(seenKeys(first.groups)), lineRunning(first.machine), timeout)
	if err != nil {
		return a.waitFailed(err, stderr)
	}
	stopped := lineRunning(first.machine) && !lineRunning(after.machine)
	sayWoke(fresh, stopped, timeout, asJSON, stdout, stderr)
	// The wait ended: read the inbox itself.
	res, err := a.ask(ctx, addr, []string{"inbox"}, without(fs, args, "wait", "timeout", "push"))
	if err != nil {
		return a.unanswered("inbox", addr, err, stderr)
	}
	var out map[string]json.RawMessage
	if !asJSON || res.Code != 0 || json.Unmarshal([]byte(res.Stdout), &out) != nil {
		a.answer(res, stdout, stderr)
		return res.Code
	}
	// --json carries woke and new, as inbox --wait's does
	out["woke"], _ = json.Marshal(len(fresh) > 0 || stopped) // ignored: a bool always encodes
	out["new"], _ = json.Marshal(idsOf(fresh))               // ignored: strings always encode
	b, _ := json.Marshal(out)                                // ignored: a map of raw JSON the server encoded
	a.answer(sprintwire.Result{Stdout: string(b) + "\n", Stderr: res.Stderr}, stdout, stderr)
	return 0
}

// pushLoop is inbox --wait --push <dir>: every judgment and every note to
// the coordinator the directory does not hold is written to it, then it waits
// for the next, until it is interrupted (exit 0). With --push seat the
// directory is the holder's inbox, read again at every look, so a seat change
// moves the push to the new holder's (pushTarget). A failure of the store, the
// server or the directory ends it with its line.
func (a *app) pushLoop(ctx context.Context, src inboxSource, dir string, timeout time.Duration, asJSON bool, stdout, stderr io.Writer) int {
	p := &pushTarget{a: a, seen: map[string]map[string]bool{}}
	if dir != pushSeat {
		p.fixed = dir
		if _, err := p.keys(dir); err != nil {
			return refuse(stderr, "inbox", "--push: "+err.Error())
		}
	}
	ctx, stop := a.notify(ctx)
	defer stop()
	look, err := src.inbox(ctx, "")
	if err != nil {
		return a.waitFailed(err, stderr)
	}
	if code := p.follow(look.holder, true, stdout, stderr); code != 0 {
		return code
	}
	if p.fixed == "" {
		a.prove(ctx, src, look.holder, asJSON, stdout)
	}
	fresh, err := p.unseen(look)
	if err != nil {
		return refuse(stderr, "inbox", "--push: "+err.Error())
	}
	// the tick's lateness, at every look (pushlate.go): a tick that runs late cannot tell of itself
	late := &lateWatch{}
	a.pushLate(ctx, src, p, late, look, asJSON, stdout, stderr)
	for {
		var texts []string
		for _, g := range fresh {
			text, code := a.push(ctx, src, p, look.holder, g, asJSON, stdout, stderr)
			if code != 0 {
				return code
			}
			if text != "" {
				texts = append(texts, text)
			}
		}
		if p.fixed == "" {
			a.pushJudgments(ctx, src, look.holder, texts, asJSON, stdout)
		}
		if ctx.Err() != nil {
			return 0
		}
		running := lineRunning(look.machine)
		fresh, look, err = a.waitNew(ctx, src, func(l inboxLook) ([]sprint.Group, error) {
			p.follow(l.holder, false, stdout, stderr)
			if p.fixed == "" {
				a.prove(ctx, src, l.holder, asJSON, stdout)
			}
			a.pushLate(ctx, src, p, late, l, asJSON, stdout, stderr)
			return p.unseen(l)
		}, running, timeout)
		if err != nil {
			return a.waitFailed(err, stderr)
		}
		if running && !lineRunning(look.machine) && ctx.Err() == nil {
			if asJSON {
				b, _ := json.Marshal(map[string]any{"machine": look.machine, "at": a.now()}) // ignored: strings and a time always encode
				fmt.Fprintln(stdout, string(b))
			} else {
				fmt.Fprintln(stdout, look.machine)
			}
		}
	}
}

// push writes the group's notes its directory does not hold, each as
// <dir>/<note id>.md, the group read whole first (as inbox --open reads it)
// so that the file carries its members and needs. A group that closed since
// the look that found it is nothing to write. It returns the group's text when
// it wrote a file: what the push loop then delivers into the holder's session
// (pushJudgments), the file kept as the record of what was pushed.
func (a *app) push(ctx context.Context, src inboxSource, p *pushTarget, holder string, g sprint.Group, asJSON bool, stdout, stderr io.Writer) (string, int) {
	look, err := src.inbox(ctx, g.ID)
	if err != nil {
		return "", a.waitFailed(err, stderr)
	}
	whole, ok := sprint.FindGroup(look.groups, g.ID)
	if !ok {
		return "", 0
	}
	dir := p.dirOf(holder, whole)
	if dir == "" {
		return "", 0 // the inbox it goes to went away since the look: the next look finds it
	}
	seen, err := p.keys(dir)
	if err != nil {
		fmt.Fprintf(stderr, "%s inbox --push: %s\n", prog, oneline.Escape(err.Error()))
		return "", 1
	}
	now := a.now()
	text := groupText(whole, now, true) + "clock: " + now.UTC().Format(time.RFC3339) + "\n"
	wrote := false
	for _, id := range noteKeys(whole) {
		if seen[id] {
			continue
		}
		seen[id] = true
		path := filepath.Join(dir, id+".md")
		written, err := writeOnce(path, text)
		if err != nil {
			fmt.Fprintf(stderr, "%s inbox --push: %s\n", prog, oneline.Escape(err.Error()))
			return "", 1
		}
		wrote = wrote || written
		switch {
		case asJSON:
			b, _ := json.Marshal(map[string]any{"pushed": id, "group": whole.ID, "file": path, "written": written, "at": now}) // ignored: strings, a bool and a time always encode
			fmt.Fprintln(stdout, string(b))
		case written:
			fmt.Fprintf(stdout, "INBOX OK pushed=%s file=%s\n", oneline.Field(id), oneline.Field(path))
		default:
			fmt.Fprintf(stdout, "NOTE %s exists: kept, not written again\n", oneline.Field(path))
		}
	}
	if !wrote {
		return "", 0
	}
	return text, 0
}

// pushedKeys is the keys of the notes the directory holds: its .md files' stems.
func pushedKeys(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".md"); ok {
			seen[name] = true
		}
	}
	return seen, nil
}

// writeOnce writes text to path unless a file is there: the text is written
// whole beside it and linked into place, so a watcher never reads half of
// it, and an existing file is never replaced. It says whether it wrote.
func writeOnce(path, text string) (bool, error) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return false, err
	}
	err := os.Link(tmp, path)
	if rmErr := os.Remove(tmp); err == nil && rmErr != nil {
		return true, rmErr
	}
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	return err == nil, err
}
