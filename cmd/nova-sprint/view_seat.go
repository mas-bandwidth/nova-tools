package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// THE SEAT'S MODEL (the owner, 2026-10-05: "you should always query your own version of the
// dashboard, maybe json"; "If you have an out of date model of what is going on, you are going
// to make incorrect decisions"; docs/SPEC-SPRINT.md section 11, "Role views", view seat;
// docs/SPRINT-COORDINATOR.md, first). `nova-sprint view seat` is the coordinator's whole
// model in one compact object, read before every decision: one GET of the dashboard's
// /api/sprint?release=all, the very snapshot the page draws, decoded as the whereView it
// carries, and nothing else read, so the seat and the page can never disagree. Every age in
// it is at the snapshot's fetchedAt; a snapshot older than modelMaxAge is refused naming
// the dashboard, never shown.

func init() {
	verbs = append(verbs, verb{"view seat", "[--dashboard <address:port | http(s) URL>] [--json]", "view seat --json", (*app).cmdViewSeat})
	verbClasses["view seat"] = classRead
	// it reads the dashboard where it is typed, never the store: the server runs it for nobody
	notServed = append(notServed, "view seat")
	verbExit["view seat"] = "exit codes: 0 done, 2 usage, a dashboard that did not answer or served no snapshot, or a snapshot older than 30s (each refusal names the dashboard)"
	verbEffect["view seat"] = "inspection: one GET of the dashboard's /api/sprint snapshot, nothing else read; writes nothing"
}

const (
	// modelMaxAge is the oldest snapshot view seat shows: older is an old model, refused.
	modelMaxAge = 30 * time.Second
	// modelOutMax is the most things out of place it lists; nout counts them all.
	modelOutMax = 5
	// modelTimeout bounds the one GET.
	modelTimeout = 5 * time.Second
)

// The kinds of thing out of place, in the order they are listed.
const (
	outStopped = "stream stopped"
	outLate    = "past deadline"
	outIdle    = "friend idle"
	outReader  = "reader behind"
	outBeat    = "friend stale"
)

var outRank = map[string]int{outStopped: 0, outLate: 1, outIdle: 2, outReader: 3, outBeat: 4}

// modelRow is a friend's or a machine's row as the snapshot carries it.
type modelRow struct {
	N  string `json:"n"`
	St string `json:"st"`
	R  int    `json:"r"`
	W  int    `json:"w"`
	Wd int    `json:"wd"`
	// Beat is how long before fetchedAt the last beat came, or "never". A friend's always.
	// A machine's is taken from the snapshot's machines array, and stays empty when that
	// array is absent: where --json does not write it yet (cmd/nova-sprint/reads.go).
	Beat string `json:"beat,omitempty"`
	Load string `json:"load,omitempty"` // a machine's: its load, present while its beat is fresh
}

// modelStream is a live stream (cards on the table, not all landed) and its column counts.
type modelStream struct {
	N      string `json:"n"`
	St     string `json:"st"` // the merge table's state, "held" while every card waits
	Wait   int    `json:"wait"`
	Ready  int    `json:"ready"`
	Work   int    `json:"work"`
	Review int    `json:"review"`
	Merge  int    `json:"merge"`
	Landed int    `json:"landed"`
	All    int    `json:"all"`
}

// modelGate is a gate and the cards waiting behind it: one of the heaviest open cards
// (where's critical), a stream held, or a hold in force.
type modelGate struct {
	G  string `json:"g"`
	K  string `json:"k"` // card, stream, or the hold's kind
	B  int    `json:"b,omitempty"`
	St string `json:"st,omitempty"`
	S  string `json:"s,omitempty"` // its stream, or why it holds
}

// modelOut is a thing out of place, one line, and the verb that answers it.
type modelOut struct {
	K    string `json:"k"`
	S    string `json:"s"`
	Next string `json:"next"`
	mag  time.Duration
}

// modelView is view seat's document, schema 1.
type modelView struct {
	View      string         `json:"view"`
	Schema    int            `json:"schema"`
	Server    string         `json:"server"`
	FetchedAt time.Time      `json:"fetchedAt"`
	Age       string         `json:"age"`
	Sum       string         `json:"sum"`
	Machine   string         `json:"machine,omitempty"`
	Friends   []modelRow     `json:"friends"`
	Fleet     []modelRow     `json:"fleet"`
	Streams   []modelStream  `json:"streams"`
	J         map[string]int `json:"j"`
	Ready     int64          `json:"ready"`
	Width     int            `json:"width"`
	Gates     []modelGate    `json:"gates"`
	Out       []modelOut     `json:"out"`
	NOut      int            `json:"nout"`
}

func (a *app) cmdViewSeat(args []string, stdout, stderr io.Writer) int {
	const name = "view seat"
	fs, c := a.verbSetup(name)
	dash := fs.String("dashboard", "", "the dashboard to read: its address:port or its http(s) URL; default $"+DashboardEnv+", else "+DashboardListenDefault)
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	server, err := modelServer(cmp.Or(*dash, a.getenv(DashboardEnv), DashboardListenDefault))
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	body, err := a.modelGet(server)
	if err != nil {
		return refuse(stderr, name, "the dashboard at "+server+" did not answer: "+err.Error()+"; run: nova-sprint seat check")
	}
	v, err := modelOf(body, server, a.now())
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	if c.json {
		viewJSON(stdout, v)
		return 0
	}
	fmt.Fprint(stdout, modelText(v))
	return 0
}

// modelServer is the snapshot's URL of the dashboard named: an address:port or an http(s) URL.
func modelServer(dash string) (string, error) {
	if !strings.Contains(dash, "://") {
		dash = "http://" + dash
	}
	u, err := url.Parse(dash)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		// the value is not echoed: it may carry a credential
		return "", errors.New("--dashboard wants the dashboard's address:port or its http(s) URL, with no credential; run: nova-sprint view seat --dashboard " + DashboardListenDefault)
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/api/sprint") + "/api/sprint"
	u.RawQuery = "release=all" // every stream, as the coordinator decides over every release
	return u.String(), nil
}

// modelGet is the one GET of the snapshot.
func (a *app) modelGet(server string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), modelTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Transport: a.transport}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // ignored: the body is read to its end or abandoned
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("it answered " + resp.Status)
	}
	return body, nil
}

// machineBeats is each machine's last beat from the snapshot's data.machines
// (name, beat as a time), and whether that array was present. An absent array is
// not "never": where --json does not write machines yet (cmd/nova-sprint/reads.go),
// and a fleet row's beat stays empty rather than claiming a beat that was not shown.
// A present array with a zero or missing time for a machine is never.
func machineBeats(data json.RawMessage) (map[string]time.Time, bool) {
	var box struct {
		Machines json.RawMessage `json:"machines"`
	}
	if json.Unmarshal(data, &box) != nil || len(box.Machines) == 0 || string(box.Machines) == "null" {
		return nil, false
	}
	var rows []struct {
		Name string    `json:"name"`
		Beat time.Time `json:"beat"`
	}
	if json.Unmarshal(box.Machines, &rows) != nil {
		return nil, false
	}
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		out[r.Name] = r.Beat
	}
	return out, true
}

// modelOf is the seat's model of the snapshot, read at now: refused when the snapshot holds
// no sprint or is older than modelMaxAge. A pure function of the bytes the page reads.
func modelOf(body []byte, server string, now time.Time) (modelView, error) {
	var snap struct {
		Data      json.RawMessage `json:"data"`
		FetchedAt *time.Time      `json:"fetchedAt"`
		Error     *string         `json:"error"`
	}
	var d whereView
	if json.Unmarshal(body, &snap) != nil || snap.FetchedAt == nil || len(snap.Data) == 0 || string(snap.Data) == "null" || json.Unmarshal(snap.Data, &d) != nil {
		why := "no sprint read yet"
		if snap.Error != nil {
			why = *snap.Error
		}
		return modelView{}, fmt.Errorf("the dashboard at %s served no snapshot of the sprint (%s); run: nova-sprint seat check", server, why)
	}
	at := *snap.FetchedAt
	if age := now.Sub(at); age > modelMaxAge {
		return modelView{}, fmt.Errorf("the dashboard at %s served a snapshot %s old (fetchedAt %s), older than %s: a decision on it is a decision on an old model; run: nova-sprint seat check",
			server, ageWord(age), at.UTC().Format(time.RFC3339), modelMaxAge)
	}
	v := modelView{View: "seat", Schema: viewSchema, Server: server, FetchedAt: at.UTC(), Age: ageWord(now.Sub(at)), Sum: d.Summary, Machine: strings.TrimPrefix(d.Machine, "machine: "),
		Friends: []modelRow{}, Fleet: []modelRow{}, Streams: []modelStream{}, J: map[string]int{}, Ready: d.Ready, Width: d.Width, Gates: []modelGate{}, Out: []modelOut{}}
	cell := func(table, row, col string) string { s, _ := d.Tables[table][row][col].(string); return s }
	num := func(table, row, col string) int { n, _ := strconv.Atoi(cell(table, row, col)); return n } // ignored: a cell that is no number counts 0
	var out []modelOut

	// the friends, with their beats; the machines of the fleet table, with the last
	// beat the snapshot's machines array carries (absent: the row's beat stays empty)
	machineBeat, haveMachines := machineBeats(snap.Data)
	for _, f := range d.Friends {
		beat := "never"
		if !f.Beat.IsZero() {
			beat = ageWord(at.Sub(f.Beat))
		}
		v.Friends = append(v.Friends, modelRow{N: f.Name, St: f.Status, R: f.Ready, W: f.Working, Wd: f.Width, Beat: beat})
		if f.Ready+f.Working > 0 && (f.Beat.IsZero() || at.Sub(f.Beat) > viewStaleReport) {
			out = append(out, modelOut{K: outBeat, S: fmt.Sprintf("friend %s holds %d ready, %d working, last beat %s", f.Name, f.Ready, f.Working, beat),
				Next: "nova-sprint friend take " + f.Name + " --all-unstarted --reason '" + f.Name + " has not beaten for " + beat + "'", mag: at.Sub(f.Beat)})
		}
	}
	for _, m := range slices.Sorted(maps.Keys(d.Tables[sprint.Fleet])) {
		r := modelRow{N: m, St: cell(sprint.Fleet, m, sprint.Status), R: num(sprint.Fleet, m, sprint.Ready), W: num(sprint.Fleet, m, sprint.Working), Load: cell(sprint.Fleet, m, sprint.Load)}
		r.Wd, _ = strconv.Atoi(cell(sprint.Fleet, m, sprint.FieldWidth)) // ignored: a width that is no number counts 0
		if haveMachines {
			r.Beat = "never"
			if t := machineBeat[m]; !t.IsZero() {
				r.Beat = ageWord(at.Sub(t))
			}
		}
		v.Fleet = append(v.Fleet, r)
	}
	for _, f := range v.Friends {
		// the ready pool is every primary ready, queued on another row or not dealt yet
		if f.St == sprint.Up && f.Wd > 0 && f.R+f.W == 0 && d.Ready > 0 {
			out = append(out, modelOut{K: outIdle, S: fmt.Sprintf("friend %s is up at 0 of width %d with %d cards ready elsewhere", f.N, f.Wd, d.Ready),
				Next: "nova-sprint friend level", mag: time.Duration(d.Ready)})
		}
	}

	// the live streams and their counts, a stream stopped, a stream held as a gate
	clocks := map[string]sprint.StreamClock{}
	for _, c := range d.Streams {
		clocks[c.Stream] = c
	}
	for _, s := range slices.Sorted(maps.Keys(d.Tables[sprint.Work])) {
		n := modelStream{N: s, Wait: num(sprint.Work, s, sprint.Waiting), Ready: num(sprint.Work, s, sprint.Ready), Work: num(sprint.Work, s, sprint.Working),
			Review: num(sprint.Work, s, sprint.Review), Merge: num(sprint.Work, s, sprint.Merging), Landed: num(sprint.Work, s, sprint.Landed)}
		n.All = n.Wait + n.Ready + n.Work + n.Review + n.Merge + n.Landed
		if n.All == 0 || n.Landed == n.All {
			continue
		}
		c := clocks[s]
		n.St = cmp.Or(cell(sprint.Merge, s, sprint.StateCol), c.State)
		if c.Held && n.St != sprint.StreamStopped {
			n.St = sprint.Held
			v.Gates = append(v.Gates, modelGate{G: s, K: "stream", B: n.Wait, S: cmp.Or(c.Reason, "every card not landed waits on a sentinel or a need")})
		}
		v.Streams = append(v.Streams, n)
		if n.St == sprint.StreamStopped {
			out = append(out, modelOut{K: outStopped, S: fmt.Sprintf("stream %s is stopped with %d of %d cards not landed", s, n.All-n.Landed, n.All),
				Next: "nova-sprint resume --stream " + s + " --did '<what you fixed>'", mag: time.Duration(n.All - n.Landed)})
		}
	}

	// the open judgments the snapshot carries (on the dealt cards), by kind
	for _, j := range d.Judgments {
		v.J[j.Kind]++
	}

	// the gates: the heaviest open cards, then the holds in force
	for _, c := range d.Critical {
		v.Gates = append(v.Gates, modelGate{G: c.ID, K: "card", B: c.Behind, St: c.State})
	}
	for _, h := range d.Holds {
		v.Gates = append(v.Gates, modelGate{G: h.Name, K: h.Kind, S: viewClip(h.Reason)})
	}

	// the cards past their deadline at fetchedAt
	for _, c := range d.Cards {
		if !c.Deadline.IsZero() && c.Deadline.Before(at) {
			out = append(out, modelOut{K: outLate, S: fmt.Sprintf("%s on %s is %s past its deadline (%s)", c.ID, c.Member, ageWord(at.Sub(c.Deadline)), c.State),
				Next: "nova-sprint log --card " + c.Primary + " --since 1h", mag: at.Sub(c.Deadline)})
		}
	}

	// a reader asked and reading nothing
	for _, r := range slices.Sorted(maps.Keys(d.Tables[sprint.Readers])) {
		if asked := num(sprint.Readers, r, sprint.Asked); asked > 0 && num(sprint.Readers, r, sprint.Reading) == 0 {
			out = append(out, modelOut{K: outReader, S: fmt.Sprintf("reader %s has %d asked and reads none", r, asked),
				Next: "nova-sprint queue --as " + r, mag: time.Duration(asked)})
		}
	}

	slices.SortStableFunc(out, func(x, y modelOut) int {
		return cmp.Or(cmp.Compare(outRank[x.K], outRank[y.K]), cmp.Compare(y.mag, x.mag))
	})
	v.NOut = len(out)
	v.Out = append(v.Out, out[:min(len(out), modelOutMax)]...)
	return v, nil
}

// modelText is the model as lines: the snapshot's line, the rows, the streams, the counts, the
// gates, then each thing out of place with its verb.
func modelText(v modelView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VIEW seat fetchedAt=%s age=%s server=%s\n%s\n", v.FetchedAt.Format(time.RFC3339), v.Age, v.Server, oneline.Escape(v.Sum))
	if v.Machine != "" {
		fmt.Fprintf(&b, "machine %s\n", oneline.Escape(v.Machine))
	}
	for _, r := range v.Friends {
		fmt.Fprintf(&b, "friend %s %s r=%d w=%d wd=%d beat=%s\n", r.N, r.St, r.R, r.W, r.Wd, r.Beat)
	}
	for _, r := range v.Fleet {
		fmt.Fprintf(&b, "machine %s %s r=%d w=%d wd=%d load=%s beat=%s\n", r.N, cmp.Or(r.St, "-"), r.R, r.W, r.Wd, cmp.Or(r.Load, "-"), cmp.Or(r.Beat, "-"))
	}
	for _, s := range v.Streams {
		fmt.Fprintf(&b, "stream %s %s wait=%d ready=%d work=%d review=%d merge=%d landed=%d/%d\n", s.N, cmp.Or(s.St, "-"), s.Wait, s.Ready, s.Work, s.Review, s.Merge, s.Landed, s.All)
	}
	var js []string
	for _, k := range slices.Sorted(maps.Keys(v.J)) {
		js = append(js, k+"="+strconv.Itoa(v.J[k]))
	}
	fmt.Fprintf(&b, "ready=%d width=%d judgments=%d %s\n", v.Ready, v.Width, len(js), strings.Join(js, ", "))
	for _, g := range v.Gates {
		fmt.Fprintf(&b, "gate %s %s behind=%d %s %s\n", g.K, g.G, g.B, cmp.Or(g.St, "-"), oneline.Escape(cmp.Or(g.S, "-")))
	}
	for _, o := range v.Out {
		fmt.Fprintf(&b, "OUT %s: %s; run: %s\n", o.K, oneline.Escape(o.S), o.Next)
	}
	fmt.Fprintf(&b, "out=%d shown=%d\n", v.NOut, len(v.Out))
	return b.String()
}
