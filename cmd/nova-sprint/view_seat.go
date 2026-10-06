package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
)

// THE SEAT VIEW (the owner, 2026-10-05: "you should always query your own version of the
// dashboard, maybe json"; "If you have an out of date model of what is going on, you are
// going to make incorrect decisions"; docs/SPEC-SPRINT.md, "Role views", view seat). `view
// seat` is the coordinator's whole model of the sprint in one compact object, read before
// any decision, from the snapshot the dashboard serves (/api/sprint, every stream) and
// nothing else, so the page and the seat can never disagree: the snapshot's fetchedAt,
// every friend's and machine's row, the live streams with their column counts, the open
// judgments by kind, the ready pool and what waits behind which gate, and the five things
// most out of place, each with the command that answers it. A snapshot older than
// seatFresh is refused, naming the dashboard: an old model is the mistake this verb is for.

func init() {
	verbs = append(verbs, verb{"view seat", "[--dashboard <host:port>] [--json]", "view seat --json", (*app).cmdViewSeat})
	verbClasses["view seat"] = classRead
	verbEffect["view seat"] = "inspection: reads the dashboard's snapshot (GET /api/sprint), writes nothing"
	verbExit["view seat"] = "exit codes: 0 done, 1 the snapshot is older than 30s or holds no good read (refused, naming the dashboard), 2 usage or a dashboard that did not answer"
	// the dashboard read is this machine's: the server never runs it for a caller
	notServed = append(notServed, "view seat")
}

const (
	// seatFresh is the oldest snapshot the seat view reads: the dashboard reads the sprint
	// every second, so a snapshot older than this is a dashboard whose reads have stopped.
	seatFresh = 30 * time.Second
	// seatOutMax is how many things out of place the view carries; nout counts them all.
	seatOutMax = 5
	// seatBeatStale is how long an up friend goes without a beat before her row is out of
	// place (view coordinator's viewStaleReport).
	seatBeatStale = viewStaleReport
	// seatBodyMax bounds the snapshot read, as a puller bounds its upstream's.
	seatBodyMax = 64 << 20
	// seatTimeout bounds the dashboard read.
	seatTimeout = sprintdash.UpstreamTimeout
)

// The kinds of a thing out of place, in the order they rank among equal cards.
const (
	outFriendIdle = "friend idle"
	outStopped    = "stream stopped"
	outLate       = "card late"
	outReader     = "reader behind"
	outBeat       = "beat stale"
)

var outRank = map[string]int{outFriendIdle: 0, outStopped: 1, outLate: 2, outReader: 3, outBeat: 4}

// seatModel is view seat's document, schema 1.
type seatModel struct {
	View      string         `json:"view"`
	Schema    int            `json:"schema"`
	Server    string         `json:"server"`    // the dashboard read
	FetchedAt time.Time      `json:"fetchedAt"` // the snapshot's, as the dashboard serves it
	Age       string         `json:"age"`       // fetchedAt's age at the read
	Stale     bool           `json:"stale,omitempty"`
	Sum       string         `json:"sum"`   // the snapshot's summary line
	Ready     int64          `json:"ready"` // the ready pool: ready primaries across the streams
	Width     int            `json:"width"` // the up machines' width
	Held      int64          `json:"held,omitempty"`
	Rows      []seatRow      `json:"rows"`
	Streams   []seatStream   `json:"streams"`
	J         map[string]int `json:"j"`     // the open judgments on dealt cards, by kind
	Gates     []seatGate     `json:"gates"` // what waits behind which gate
	Out       []seatOut      `json:"out"`   // the things most out of place, at most seatOutMax
	NOut      int            `json:"nout"`  // every thing out of place
}

// seatRow is a friend's (f:<name>) or a machine's (m:<name>) row as the dashboard shows it.
type seatRow struct {
	K    string `json:"k"`
	St   string `json:"st"`
	R    int    `json:"r"`
	W    int    `json:"w"`
	Wd   int    `json:"wd"`
	Beat string `json:"beat,omitempty"` // since its last beat at fetchedAt, "never"; a machine's the snapshot does not carry
}

// seatStream is a live stream: its state and the work table's column counts.
type seatStream struct {
	S      string `json:"s"`
	St     string `json:"st,omitempty"`
	Wait   int    `json:"wait"`
	Ready  int    `json:"ready"`
	Work   int    `json:"work"`
	Review int    `json:"review"`
	Merge  int    `json:"merge"`
	Landed int    `json:"landed"`
}

// seatGate is a gate and the cards waiting behind it.
type seatGate struct {
	G string `json:"g"`           // card:<id>, hold:<kind>:<name>, held
	N int    `json:"n"`           // the cards behind it
	S string `json:"s,omitempty"` // its state or its reason
}

// seatOut is one thing out of place, one line, with the command that answers it.
type seatOut struct {
	W    string `json:"w"`
	S    string `json:"s"`
	Next string `json:"next"`
	b    int
	k    string
}

// seatSnapshot is /api/sprint's body as the seat view reads it: the data is where --json
// --cards's own shape.
type seatSnapshot struct {
	OK        bool       `json:"ok"`
	FetchedAt *time.Time `json:"fetchedAt"`
	Error     *string    `json:"error"`
	Stale     bool       `json:"stale"`
	Data      whereView  `json:"data"`
}

func (a *app) cmdViewSeat(args []string, stdout, stderr io.Writer) int {
	const name = "view seat"
	fs, c := a.verbSetup(name)
	dash := fs.String("dashboard", "", "the dashboard whose snapshot is read, `host:port` or its http:// URL (default "+DashboardEnv+", else "+DashboardListenDefault+")")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	server := cmp.Or(*dash, a.getenv(DashboardEnv), DashboardListenDefault)
	base := server
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	status, body, err := a.seatGet(context.Background(), strings.TrimSuffix(base, "/")+"/api/sprint?release="+sprintdash.AllReleases)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("it answered HTTP %d", status)
	}
	if err != nil {
		return refuse(stderr, name, "the dashboard at "+server+" did not answer: "+err.Error()+"; run: nova-sprint seat check (its dashboard line), or name another with --dashboard <host:port>")
	}
	var snap seatSnapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		return refuse(stderr, name, "the dashboard at "+server+" answered no snapshot JSON: "+err.Error()+"; run: nova-sprint seat check")
	}
	v, why := seatOf(snap, server, a.now())
	if why != "" {
		fmt.Fprintf(stderr, "%s %s REFUSED: %s; run: nova-sprint seat check\n", prog, name, oneline.Escape(why))
		return 1
	}
	if c.json {
		viewJSON(stdout, v)
		return 0
	}
	fmt.Fprint(stdout, seatText(v))
	return 0
}

// seatGet is one GET of the dashboard: the seat check's reach when a test sets it, else a
// bounded read over the app's transport.
func (a *app) seatGet(ctx context.Context, url string) (int, []byte, error) {
	if a.outside.httpGet != nil {
		return a.outside.httpGet(ctx, url)
	}
	ctx, cancel := context.WithTimeout(ctx, seatTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := (&http.Client{Transport: a.transport}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }() // ignored: the body is read to its end or abandoned
	body, err := io.ReadAll(io.LimitReader(resp.Body, seatBodyMax))
	return resp.StatusCode, body, err
}

// seatOf is the seat view of a snapshot read from server at now, or why it is refused: no
// good read, no fetchedAt, or a fetchedAt older than seatFresh. Every field is the
// snapshot's; nothing else is read.
func seatOf(snap seatSnapshot, server string, now time.Time) (seatModel, string) {
	if !snap.OK || snap.FetchedAt == nil {
		why := "it has made no good read yet"
		if snap.Error != nil {
			why = "its last read failed: " + *snap.Error
		}
		return seatModel{}, "the dashboard at " + server + " serves no good snapshot (" + why + ")"
	}
	at := snap.FetchedAt.UTC()
	age := now.Sub(at)
	if age > seatFresh {
		return seatModel{}, fmt.Sprintf("the dashboard at %s serves a snapshot %s old (fetchedAt %s), older than %s: its reads have stopped and a decision on it is a decision on the past",
			server, ageWord(age), at.Format(time.RFC3339), seatFresh)
	}
	d := snap.Data
	v := seatModel{View: "seat", Schema: viewSchema, Server: server, FetchedAt: at, Age: ageWord(age), Stale: snap.Stale,
		Sum: d.Summary, Ready: d.Ready, Width: d.Width, Held: d.Held,
		Rows: []seatRow{}, Streams: []seatStream{}, J: map[string]int{}, Gates: []seatGate{}, Out: []seatOut{}}
	var out []seatOut

	// the rows: every friend's, then every machine's, by name
	beats := map[string]time.Time{}
	for _, f := range d.Friends {
		beats[f.Name] = f.Beat
	}
	friendReady := map[string]int{}
	friends := tableRows(d.Tables[sprint.Friends])
	for _, name := range friends {
		cells := d.Tables[sprint.Friends][name]
		r := seatRow{K: "f:" + name, St: cellText(cells[sprint.Status]), R: cellInt(cells[string(sprint.Ready)]),
			W: cellInt(cells[string(sprint.Working)]), Wd: cellInt(cells[sprint.FieldWidth]), Beat: "never"}
		b := beats[name]
		if !b.IsZero() {
			r.Beat = ageWord(at.Sub(b))
		}
		friendReady[name] = r.R
		v.Rows = append(v.Rows, r)
		if r.St != sprint.Up {
			continue
		}
		if b.IsZero() || at.Sub(b) > seatBeatStale {
			last := "has never beaten"
			if !b.IsZero() {
				last = "last beat " + r.Beat + " ago"
			}
			o := seatOut{W: outBeat, k: name, b: r.R + r.W,
				S:    name + " is up and " + last + ", holding " + strconv.Itoa(r.R+r.W) + " cards",
				Next: "nova-sprint friend down " + name + " --reason 'no beat for " + r.Beat + "'"}
			if r.R+r.W > 0 {
				o.Next = "nova-sprint friend take " + name + " --all-unstarted --reason 'no beat for " + r.Beat + "'"
			}
			out = append(out, o)
		}
	}
	for _, name := range tableRows(d.Tables[sprint.Fleet]) {
		cells := d.Tables[sprint.Fleet][name]
		v.Rows = append(v.Rows, seatRow{K: "m:" + name, St: cellText(cells[sprint.Status]), R: cellInt(cells[string(sprint.Ready)]),
			W: cellInt(cells[string(sprint.Working)]), Wd: cellInt(cells[sprint.FieldWidth])})
	}

	// an up friend at 0 with a beat that is fresh, while cards wait elsewhere: unstarted on
	// another friend (taken back, they are dealt again), or in the ready pool
	for _, r := range v.Rows {
		name, ok := strings.CutPrefix(r.K, "f:")
		if !ok || r.St != sprint.Up || r.R+r.W > 0 || slices.ContainsFunc(out, func(o seatOut) bool { return o.W == outBeat && o.k == name }) {
			continue
		}
		holder, most := "", 0
		for _, other := range friends {
			if other != name && friendReady[other] > most {
				holder, most = other, friendReady[other]
			}
		}
		switch {
		case holder != "":
			out = append(out, seatOut{W: outFriendIdle, k: name, b: most,
				S:    name + " is up at 0 while " + holder + " holds " + strconv.Itoa(most) + " unstarted",
				Next: "nova-sprint friend take " + holder + " --all-unstarted --reason '" + name + " is up at 0'"})
		case d.Ready > 0:
			out = append(out, seatOut{W: outFriendIdle, k: name, b: int(d.Ready),
				S:    name + " is up at 0 while " + strconv.FormatInt(d.Ready, 10) + " cards are ready",
				Next: "nova-sprint friend sync"})
		}
	}

	// the streams: every one not wholly landed, or stopped, in the snapshot's order
	clocks := map[string]sprint.StreamClock{}
	var order []string
	for _, s := range d.Streams {
		if _, ok := d.Tables[sprint.Work][s.Stream]; ok && clocks[s.Stream].Stream == "" {
			order = append(order, s.Stream)
		}
		clocks[s.Stream] = s
	}
	for _, s := range tableRows(d.Tables[sprint.Work]) {
		if !slices.Contains(order, s) {
			order = append(order, s)
		}
	}
	for _, s := range order {
		cells := d.Tables[sprint.Work][s]
		st := seatStream{S: s, St: clocks[s].State, Wait: cellInt(cells[string(sprint.Waiting)]), Ready: cellInt(cells[string(sprint.Ready)]),
			Work: cellInt(cells[string(sprint.Working)]), Review: cellInt(cells[string(sprint.Review)]),
			Merge: cellInt(cells[string(sprint.Merging)]), Landed: cellInt(cells[string(sprint.Landed)])}
		open := st.Wait + st.Ready + st.Work + st.Review + st.Merge
		if open == 0 && st.St != sprint.StreamStopped {
			continue
		}
		v.Streams = append(v.Streams, st)
		if st.St == sprint.StreamStopped {
			why := ""
			if c := clocks[s]; c.Reason != "" {
				why = " (" + c.Reason + ")"
			}
			out = append(out, seatOut{W: outStopped, k: s, b: open,
				S:    "stream " + s + " is stopped" + why + " with " + strconv.Itoa(open) + " cards not landed",
				Next: "nova-sprint resume --stream " + s})
		}
	}

	// the open judgments the snapshot carries (those naming a dealt card), once each, by kind
	seen := map[string]bool{}
	for _, j := range d.Judgments {
		if !seen[j.ID] {
			seen[j.ID] = true
			v.J[j.Kind]++
		}
	}

	// the gates: the heaviest cards and the cards behind each, every hold, and the cards
	// held behind a sentinel not released or admitted held
	for _, c := range d.Critical {
		v.Gates = append(v.Gates, seatGate{G: "card:" + c.ID, N: c.Behind, S: c.State})
	}
	for _, h := range d.Holds {
		g := seatGate{G: "hold:" + h.Kind + ":" + h.Name, S: h.Reason}
		if h.Kind == "stream" {
			cells := d.Tables[sprint.Work][h.Name]
			g.N = cellInt(cells[string(sprint.Waiting)]) + cellInt(cells[string(sprint.Ready)])
		}
		v.Gates = append(v.Gates, g)
	}
	if d.Held > 0 {
		v.Gates = append(v.Gates, seatGate{G: "held", N: int(d.Held), S: "behind a sentinel not released, or admitted held"})
	}

	// the cards past their deadline at fetchedAt
	for _, c := range d.Cards {
		if c.Deadline.IsZero() || !c.Deadline.Before(at) {
			continue
		}
		late := ageWord(at.Sub(c.Deadline))
		o := seatOut{W: outLate, k: c.ID, b: 1, S: c.ID + " on " + c.Member + " (" + c.State + ") is " + late + " past its deadline",
			Next: "nova-sprint log --card " + c.ID}
		if f, ok := sprint.FriendOfRow(c.Member); ok {
			o.Next = "nova-sprint friend take " + f + " " + c.ID + " --reason '" + late + " past its deadline'"
		}
		out = append(out, o)
	}

	// the readers asked to read and reading nothing
	for _, r := range tableRows(d.Tables[sprint.Readers]) {
		cells := d.Tables[sprint.Readers][r]
		asked, reading := cellInt(cells[sprint.Asked]), cellInt(cells[sprint.Reading])
		if asked == 0 || reading > 0 {
			continue
		}
		o := seatOut{W: outReader, k: r, b: asked, S: r + " has " + strconv.Itoa(asked) + " asked and reads none", Next: "nova-sprint queue --as " + r}
		if cellText(cells[sprint.FieldWidth]) == "0" {
			o.S += " (width 0)"
			o.Next = "nova-sprint reader up " + r
		}
		out = append(out, o)
	}

	slices.SortStableFunc(out, func(x, y seatOut) int {
		return cmp.Or(cmp.Compare(y.b, x.b), cmp.Compare(outRank[x.W], outRank[y.W]), cmp.Compare(x.k, y.k))
	})
	v.NOut = len(out)
	v.Out = append(v.Out, out[:min(len(out), seatOutMax)]...)
	return v, ""
}

// tableRows is a table's row keys, sorted.
func tableRows(t map[string]map[string]any) []string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// cellInt is a count cell as a number; a cell that is no number ("-", "?") is 0.
func cellInt(cell any) int {
	n, _ := strconv.Atoi(strings.TrimSpace(cellText(cell))) // ignored: a cell that is no number counts 0
	return n
}

// seatText is the view as lines: the head, then a line a row, stream, gate and thing out of
// place.
func seatText(v seatModel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VIEW seat server=%s fetchedAt=%s age=%s ready=%d width=%d out=%d/%d %s\n",
		v.Server, v.FetchedAt.Format(time.RFC3339), v.Age, v.Ready, v.Width, len(v.Out), v.NOut, oneline.Escape(v.Sum))
	for _, o := range v.Out {
		fmt.Fprintf(&b, "OUT %s: %s -> %s\n", o.W, oneline.Escape(o.S), o.Next)
	}
	for _, r := range v.Rows {
		fmt.Fprintf(&b, "ROW %s %s r=%d w=%d wd=%d beat=%s\n", r.K, cmp.Or(r.St, "-"), r.R, r.W, r.Wd, cmp.Or(r.Beat, "-"))
	}
	for _, s := range v.Streams {
		fmt.Fprintf(&b, "STREAM %s %s wait=%d ready=%d work=%d review=%d merge=%d landed=%d\n", s.S, cmp.Or(s.St, "-"), s.Wait, s.Ready, s.Work, s.Review, s.Merge, s.Landed)
	}
	kinds := make([]string, 0, len(v.J))
	for k := range v.J {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	for _, k := range kinds {
		fmt.Fprintf(&b, "JUDGMENT %s %d\n", oneline.Escape(k), v.J[k])
	}
	for _, g := range v.Gates {
		fmt.Fprintf(&b, "GATE %s %d %s\n", g.G, g.N, oneline.Escape(cmp.Or(g.S, "-")))
	}
	return b.String()
}
