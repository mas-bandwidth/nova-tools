// Package driver plays the world outside the sprint table, on a tick: workers
// taking and finishing work cards, readers reporting read cards, each
// stream's merge step with its facts, and fleet members going down and up.
// The mechanical moves are the machine's (nova-sprint run): the driver plays
// only the outside actors, and refuses to play while no machine is running.
// It has no access of its own to the core or the store: everything it does is
// a nova-sprint verb run through the command's own entry point with an
// argument list, and everything it knows it reads from the read verbs' --json
// output. It never runs the coordinator's verbs (accept, rework, drop, rank,
// resume, return, start, stop) nor the machine's (tick, run); it keeps playing
// while things wait for the coordinator, says what waits and for how long,
// and stops when every stream has landed.
package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Facts is where the outside world's results come from. The loop does not
// know whether they are real or invented.
type Facts interface {
	// Work is how a worker's work card came back.
	Work(card string) (ok bool, report string)
	// Read is what a reader found in a read card.
	Read(card string) (ok bool, finding string)
	// Merge is how a stream's batch went on its branch. others reads, when
	// called, the cards queued in the other streams' merge queues, which a
	// card may need first: a source calls it only for a cross-stream fact,
	// and it reads them then, after every merge step before this one.
	Merge(stream string, batch []string, others func() []string) Outcome
	// Up is which members are up this tick, given which are up now: a member
	// up goes down, and a member down comes up, with the same chance.
	Up(tick int, members []string, up map[string]bool) map[string]bool
}

// Outcome is a merge batch's facts: at most one of them; none is green.
type Outcome struct {
	Conflict string // a card that did not merge
	Cross    string // <card>=<other>: a card needs a card of another stream first
	Red      bool
	Suspects []string // on red, the cards the branch's failure points at
}

// Clock is the driver's time: read, and slept on between ticks.
type Clock interface {
	Now() time.Time
	Sleep(time.Duration)
}

// Config is how the driver plays.
type Config struct {
	Every     time.Duration // between ticks
	Batch     int           // a merge step's batch
	TakeLimit int           // work cards a member takes a tick
	ReadLimit int           // read cards a reader reports a tick
	Ticks     int           // stop after this many ticks; 0 is until every stream lands
}

// Driver runs the loop. Run is the command's entry point; Base is the flags
// every verb it runs gets (the store and the prefix), and they are printed
// with each command line so the line can be typed by hand.
type Driver struct {
	Run    func(args []string, stdout, stderr io.Writer) int
	Base   []string
	Facts  Facts
	Clock  Clock
	Out    io.Writer
	Config Config
	downed map[string]bool // members this driver took down and has not brought up
	// held is the sprint's epoch the driver read at its start: every verb it
	// runs that writes carries it. cleared says a verb was refused because
	// the sprint left it: the driver stops, running nothing more.
	held    uint64
	cleared bool
}

// clearedMark is how a verb holding an epoch the sprint has left says so.
const clearedMark = "the sprint was cleared at"

// coordinatorVerbs are never run by the driver: the coordinator's, and the
// machine's.
var coordinatorVerbs = map[string]bool{"accept": true, "rework": true, "drop": true, "rank": true, "resume": true, "return": true,
	"start": true, "stop": true, "tick": true, "run": true, "resolve": true, "ask": true}

// run runs one verb, prints its command line and its summary shortened, and
// returns its exit code and stdout.
func (d *Driver) run(quiet bool, args ...string) (int, string) {
	if coordinatorVerbs[args[0]] {
		panic("the driver never runs the coordinator's verb " + args[0])
	}
	if d.cleared {
		return 1, ""
	}
	full := append(append([]string{}, args...), d.Base...)
	var out, errb bytes.Buffer
	code := d.Run(full, &out, &errb)
	if code != 0 && strings.Contains(out.String()+errb.String(), clearedMark) {
		d.cleared = true
	}
	if !quiet {
		fmt.Fprintf(d.Out, "  %-60s %s\n", commandLine(full), short(args[0], out.String()+errb.String(), code))
	}
	return code, out.String()
}

// commandLine is the verb as it is typed: nova-sprint and its words, a word
// holding a blank, a quote or a shell character in single quotes.
func commandLine(args []string) string {
	words := []string{"nova-sprint"}
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " '\"$`\\|&;<>()*?[]#~") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		words = append(words, a)
	}
	return strings.Join(words, " ")
}

// short is a verb's summary line, shortened to its token, status and counts.
func short(verb, text string, code int) string {
	tok := strings.ToUpper(verb)
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && strings.HasPrefix(f[0], tok) && (f[1] == "OK" || f[1] == "FAIL") {
			keep := []string{f[0], f[1]}
			for _, x := range f[2:] {
				if strings.HasPrefix(x, "moved=") || strings.HasPrefix(x, "refused=") || strings.HasPrefix(x, "pending=") || strings.HasPrefix(x, "changed=") {
					keep = append(keep, x)
				}
			}
			return strings.Join(keep, " ")
		}
	}
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) != "" {
			return fmt.Sprintf("exit %d: %s", code, strings.TrimSpace(l))
		}
	}
	return fmt.Sprintf("exit %d", code)
}

// where is what the driver reads of the view.
type where struct {
	Epoch   uint64                                  `json:"epoch"`
	Landed  int64                                   `json:"landed"`
	All     int64                                   `json:"all"`
	Summary string                                  `json:"summary"`
	Tables  map[string]map[string]map[string]string `json:"tables"`
	Streams []struct {
		Stream string `json:"Stream"`
		State  string `json:"State"`
	} `json:"streams"`
	Pending string `json:"pending"`
	Machine string `json:"machine"`
}

type queue struct {
	Cards []struct {
		ID  string `json:"id"`
		Col string `json:"col"`
		Gen int    `json:"gen"`
	} `json:"cards"`
}

type inbox struct {
	Groups []struct {
		Kind    string    `json:"kind"`
		Type    string    `json:"type"`
		Stream  string    `json:"stream"`
		Count   int       `json:"count"`
		Oldest  time.Time `json:"oldest"`
		Overdue bool      `json:"overdue"`
	} `json:"groups"`
}

func (d *Driver) read(v any, args ...string) bool {
	code, out := d.run(true, append(args, "--json")...)
	if code != 0 {
		return false
	}
	return json.Unmarshal([]byte(out), v) == nil
}

func sortedRows(t map[string]map[string]string) []string {
	var out []string
	for r := range t {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string][]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Loop plays until every stream has landed (exit 0), or until Ticks ticks.
// It returns the reason it stopped.
func (d *Driver) Loop() (string, error) {
	c := d.Config
	if c.Batch <= 0 {
		c.Batch = 100
	}
	if c.TakeLimit <= 0 {
		c.TakeLimit = 10
	}
	if c.ReadLimit <= 0 {
		c.ReadLimit = 10
	}
	var first where
	if !d.read(&first, "where") {
		return "", fmt.Errorf("the view could not be read: run: %s", commandLine(append([]string{"where"}, d.Base...)))
	}
	if first.Machine != "machine: running" && !strings.HasPrefix(first.Machine, "machine: running;") {
		return "", fmt.Errorf("no machine is running (%s): the driver plays only the outside actors; run: nova-sprint start, and nova-sprint run", orDash(first.Machine))
	}
	d.held = first.Epoch
	for tick := 1; c.Ticks == 0 || tick <= c.Ticks; tick++ {
		var w where
		if !d.read(&w, "where") {
			return "", fmt.Errorf("tick %d: the view could not be read: run: %s", tick, commandLine(append([]string{"where"}, d.Base...)))
		}
		if w.Epoch != d.held {
			d.cleared = true
		}
		if d.cleared {
			return d.stopCleared(), nil
		}
		if landed(w) {
			fmt.Fprintf(d.Out, "every stream has landed: %s\n", w.Summary)
			d.restore()
			return "landed", nil
		}
		fmt.Fprintf(d.Out, "tick %d %s\n", tick, d.Clock.Now().Format("15:04:05"))
		d.tick(tick, c, w)
		if d.cleared {
			return d.stopCleared(), nil
		}
		d.waits()
		var after where
		if d.read(&after, "where") {
			fmt.Fprintf(d.Out, "  %s\n", after.Summary)
			if landed(after) {
				fmt.Fprintf(d.Out, "every stream has landed: %s\n", after.Summary)
				d.restore()
				return "landed", nil
			}
		}
		d.Clock.Sleep(c.Every)
	}
	d.restore()
	return "ticks", nil
}

// stopCleared is the driver stopping at a clear: the sprint left the epoch it
// holds, so it writes nothing more and brings up no member (the new epoch's
// fleet is the clear's).
func (d *Driver) stopCleared() string {
	fmt.Fprintf(d.Out, "the sprint was cleared: this driver holds epoch %d, which the sprint has left; it stops without writing; run: nova-sprint where\n", d.held)
	return "cleared"
}

// restore brings up every member this driver took down, so a run never ends
// with the fleet short of what it started with. Each fleet up holds the
// driver's epoch: after a clear it is refused.
func (d *Driver) restore() {
	var ms []string
	for m := range d.downed {
		ms = append(ms, m)
	}
	sort.Strings(ms)
	for _, m := range ms {
		if code, _ := d.run(false, "fleet", "up", m, "--epoch", strconv.FormatUint(d.held, 10)); code == 0 {
			delete(d.downed, m)
		}
	}
}

// landed says every primary on the table has landed: a stream with no
// primaries (a new epoch's, say) has nothing to land.
func landed(w where) bool {
	return w.All > 0 && w.Landed == w.All
}

func (d *Driver) tick(tick int, c Config, w where) {
	// Every verb that writes holds the driver's epoch: a clear since refuses
	// it, naming the clear, and nothing of the old epoch lands in the new one.
	held := []string{"--epoch", strconv.FormatUint(d.held, 10)}
	// Members up and down, as the facts say.
	fleet := w.Tables["fleet"]
	members := sortedRows(fleet)
	up := map[string]bool{}
	for _, m := range members {
		up[m] = fleet[m]["status"] == "up"
	}
	next := d.Facts.Up(tick, members, up)
	for _, m := range members {
		switch {
		case next[m] && !up[m]:
			if code, _ := d.run(false, append([]string{"fleet", "up", m}, held...)...); code == 0 {
				delete(d.downed, m)
			}
		case !next[m] && up[m]:
			if code, _ := d.run(false, append([]string{"fleet", "down", m}, held...)...); code == 0 {
				if d.downed == nil {
					d.downed = map[string]bool{}
				}
				d.downed[m] = true
			}
		}
	}
	// Workers: finish what they took last tick, then take the oldest ready
	// cards; every card is named <card>@<gen>, the generation from the queue.
	for _, m := range members {
		if !next[m] {
			continue
		}
		var q queue
		if !d.read(&q, "queue", "--as", m) {
			continue
		}
		var good, ready []string
		bad := map[string][]string{}
		for _, card := range q.Cards {
			word := card.ID + "@" + strconv.Itoa(card.Gen)
			if card.Col == "ready" && len(ready) < c.TakeLimit {
				ready = append(ready, word)
			}
			if card.Col != "working" {
				continue
			}
			if ok, report := d.Facts.Work(card.ID); ok {
				good = append(good, word)
			} else {
				bad[report] = append(bad[report], word)
			}
		}
		if len(good) > 0 {
			d.run(false, append(append([]string{"finish", "--as", m}, held...), good...)...)
		}
		for _, report := range sortedKeys(bad) {
			d.run(false, append(append([]string{"finish", "--as", m, "--failed", "--report", report}, held...), bad[report]...)...)
		}
		if len(ready) > 0 {
			d.run(false, append(append([]string{"take", "--as", m}, held...), ready...)...)
		}
	}
	// Readers report what is asked of them.
	for _, r := range sortedRows(w.Tables["readers"]) {
		var q queue
		if !d.read(&q, "queue", "--as", r) {
			continue
		}
		var good []string
		broken := map[string][]string{}
		for i, card := range q.Cards {
			if i == c.ReadLimit {
				break
			}
			if ok, finding := d.Facts.Read(card.ID); ok {
				good = append(good, card.ID)
			} else {
				broken[finding] = append(broken[finding], card.ID)
			}
		}
		if len(good) > 0 {
			d.run(false, append(append([]string{"read", "--as", r, "--ok"}, held...), good...)...)
		}
		for _, f := range sortedKeys(broken) {
			d.run(false, append(append([]string{"read", "--as", r, "--broken", "--finding", f}, held...), broken[f]...)...)
		}
	}
	// Each stream's merge step, with its facts. A stream's queue is read just
	// before its step, and the other streams' queues only when a fact needs
	// them, after every step before it has run.
	merge := w.Tables["merge"]
	var streams []string
	for _, s := range w.Streams {
		streams = append(streams, s.Stream)
	}
	sort.Strings(streams)
	queued := func(s string) []string {
		var out []string
		var q queue
		if d.read(&q, "queue", "--stream", s) {
			for _, card := range q.Cards {
				if card.Col == "queued" {
					out = append(out, card.ID)
				}
			}
		}
		return out
	}
	for _, s := range streams {
		st := merge[s]["state"]
		if st == "stopped" || st == "landed" {
			continue
		}
		var batch []string
		if atoi(merge[s]["queued"]) > 0 {
			batch = queued(s)
		}
		work := w.Tables["work"][s]
		open := atoi(work["waiting"]) + atoi(work["ready"]) + atoi(work["working"]) + atoi(work["review"]) + atoi(work["merging"])
		if len(batch) == 0 && !(open == 0 && atoi(work["landed"]) > 0) && st != "merging" {
			continue
		}
		if len(batch) > c.Batch {
			batch = batch[:c.Batch]
		}
		others := func() []string {
			var out []string
			for _, o := range streams {
				if o != s && merge[o]["state"] != "landed" {
					out = append(out, queued(o)...)
				}
			}
			return out
		}
		args := []string{"merge", "--stream", s, "--batch", strconv.Itoa(c.Batch)}
		if len(batch) > 0 {
			out := d.Facts.Merge(s, batch, others)
			switch {
			case out.Conflict != "":
				args = append(args, "--conflict", out.Conflict)
			case out.Cross != "":
				args = append(args, "--cross", out.Cross)
			case out.Red:
				args = append(args, "--red")
				for _, x := range out.Suspects {
					args = append(args, "--suspect", x)
				}
			}
		}
		d.run(false, append(args, held...)...)
	}
}

// waits prints what waits for the coordinator, and for how long.
func (d *Driver) waits() {
	var in inbox
	if !d.read(&in, "inbox") {
		return
	}
	now := d.Clock.Now()
	var parts []string
	for _, g := range in.Groups {
		if g.Kind != "judgment" {
			continue
		}
		p := fmt.Sprintf("%s %s x%d %s", g.Type, g.Stream, g.Count, now.Sub(g.Oldest).Round(time.Second))
		if g.Overdue {
			p += " OVERDUE"
		}
		parts = append(parts, p)
	}
	if len(parts) > 0 {
		fmt.Fprintf(d.Out, "  waits for the coordinator: %s\n", strings.Join(parts, "; "))
	}
}
