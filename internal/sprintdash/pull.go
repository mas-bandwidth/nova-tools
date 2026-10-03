package sprintdash

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// The pull routes (docs/SPEC-SPRINT.md, the dashboard): a worker's own view of the sprint,
// asked for when the worker wants it, from the same cached copy the page reads. The owner,
// 2026-10-03: "Think from the point of view of the worker. How to get the current in the
// dashboard to them efficiently for their own visibility, on request (pull)." The views
// are pure functions of the copy (pullView, Text); the handler is the transport.

// sprintCopy is the part of where --json --cards the pull routes read.
type sprintCopy struct {
	At        time.Time                               `json:"at"`
	Landed    int64                                   `json:"landed"`
	All       int64                                   `json:"all"`
	Held      int64                                   `json:"held"`
	Summary   string                                  `json:"summary"`
	Machine   string                                  `json:"machine"`
	Tables    map[string]map[string]map[string]string `json:"tables"`
	Cards     []PullCard                              `json:"cards"`
	Judgments []PullJudgment                          `json:"judgments"`
}

// PullCard is a work card dealt to a row and not finished, as where --json --cards prints it.
type PullCard struct {
	ID       string    `json:"id"`
	Primary  string    `json:"primary,omitempty"`
	Stream   string    `json:"stream"`
	Member   string    `json:"member,omitempty"`
	State    string    `json:"state"`
	Since    time.Time `json:"since,omitzero"`
	Deadline time.Time `json:"deadline,omitzero"`
	Branch   string    `json:"branch"`
}

// PullJudgment is an open judgment naming a card's primary: its note id and its kind.
type PullJudgment struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Card string `json:"card"`
}

// SprintLine is the sprint in one line: landed of all, held, the ETA and the machine.
type SprintLine struct {
	Landed  int64  `json:"landed"`
	All     int64  `json:"all"`
	Held    int64  `json:"held"`
	ETA     string `json:"eta"`
	Machine string `json:"machine"`
}

// PullRow is a row of the friends table or the fleet table as where prints its cells.
type PullRow struct {
	Status  string `json:"status"`
	Ready   string `json:"ready"`
	Working string `json:"working"`
	Width   string `json:"width"`
	Done    string `json:"done"`
	OKPct   string `json:"okpct"`
	Load    string `json:"load,omitempty"`
}

// PullView is one friend's or one machine's view: the sprint line, the row, the cards
// dealt to it and not finished, and the open judgments naming them.
type PullView struct {
	At        time.Time      `json:"at"`
	Kind      string         `json:"kind"` // friend or machine
	Name      string         `json:"name"`
	Sprint    SprintLine     `json:"sprint"`
	Row       PullRow        `json:"row"`
	Cards     []PullCard     `json:"cards"`
	Judgments []PullJudgment `json:"judgments"`
}

// The kinds of view, and the table and the member each reads.
const (
	KindFriend  = "friend"
	KindMachine = "machine"
)

// friendRowPrefix is the fleet row a friend's cards are dealt to (sprint.FriendRow).
const friendRowPrefix = "friend."

// pullView is the view of the row named, false when the table has no such row: a friend
// is a row of the friends table and her cards are dealt to friend.<name>; a machine is a
// row of the fleet table and its cards are dealt to its name.
func pullView(c *sprintCopy, kind, name string) (PullView, bool) {
	table, member := "friends", friendRowPrefix+name
	if kind == KindMachine {
		table, member = "fleet", name
	}
	cells, ok := c.Tables[table][name]
	if !ok || name == "" {
		return PullView{}, false
	}
	v := PullView{At: c.At, Kind: kind, Name: name, Cards: []PullCard{}, Judgments: []PullJudgment{},
		Sprint: SprintLine{Landed: c.Landed, All: c.All, Held: c.Held, ETA: etaOf(c.Summary), Machine: strings.TrimPrefix(c.Machine, "machine: ")},
		Row: PullRow{Status: cells["status"], Ready: cells["ready"], Working: cells["working"], Width: cells["width"],
			Done: cells["done"], OKPct: cells["okpct"], Load: cells["load"]}}
	mine := map[string]bool{}
	for _, card := range c.Cards {
		if card.Member == member {
			mine[card.Primary] = true
			card.Member, card.Primary = "", ""
			v.Cards = append(v.Cards, card)
		}
	}
	for _, j := range c.Judgments {
		if mine[j.Card] {
			v.Judgments = append(v.Judgments, j)
		}
	}
	return v, true
}

// TeamView is every friend's block (the owner, 2026-10-03 11:56 AM: "we need to get the
// friends working together."): the sprint line, then each friend's row and the cards she
// holds, so each friend sees what every other is on.
type TeamView struct {
	At      time.Time    `json:"at"`
	Sprint  SprintLine   `json:"sprint"`
	Friends []TeamFriend `json:"friends"`
}

// TeamFriend is one friend's block: her row and her cards.
type TeamFriend struct {
	Name  string     `json:"name"`
	Row   PullRow    `json:"row"`
	Cards []TeamCard `json:"cards"`
}

// TeamCard is a card a friend holds, as the team sees it.
type TeamCard struct {
	ID     string    `json:"id"`
	Stream string    `json:"stream"`
	State  string    `json:"state"`
	Since  time.Time `json:"since,omitzero"`
}

// teamView is every friend of the friends table, by name, each her own view's row and cards.
func teamView(c *sprintCopy) TeamView {
	t := TeamView{At: c.At, Friends: []TeamFriend{}}
	names := make([]string, 0, len(c.Tables["friends"]))
	for name := range c.Tables["friends"] {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		v, _ := pullView(c, KindFriend, name)
		t.Sprint = v.Sprint
		f := TeamFriend{Name: name, Row: v.Row, Cards: []TeamCard{}}
		for _, card := range v.Cards {
			f.Cards = append(f.Cards, TeamCard{ID: card.ID, Stream: card.Stream, State: card.State, Since: card.Since})
		}
		t.Friends = append(t.Friends, f)
	}
	if len(names) == 0 {
		t.Sprint = SprintLine{Landed: c.Landed, All: c.All, Held: c.Held, ETA: etaOf(c.Summary), Machine: strings.TrimPrefix(c.Machine, "machine: ")}
	}
	return t
}

// Text is the team as plain text: the sprint line, then for each friend a line of her
// row and an indented line per card she holds (id, stream, state, how long in it).
func (t TeamView) Text() string {
	var b strings.Builder
	b.WriteString(sprintText(t.Sprint, t.At))
	for _, f := range t.Friends {
		r := f.Row
		fmt.Fprintf(&b, "friend %s %s working %s/%s ready %s done %s ok %s\n", f.Name, dash(r.Status), dash(r.Working), dash(r.Width), dash(r.Ready), dash(r.Done), dash(r.OKPct))
		for _, c := range f.Cards {
			fmt.Fprintf(&b, "  %s %s %s %s\n", c.ID, dash(c.Stream), c.State, since(t.At, c.Since))
		}
	}
	return b.String()
}

func sprintText(s SprintLine, at time.Time) string {
	return fmt.Sprintf("sprint %d/%d landed held %d eta %s machine %s at %s\n", s.Landed, s.All, s.Held, s.ETA, dash(s.Machine), at.Format("3:04:05 PM"))
}

// etaOf is the ETA the summary line ends with ("... -> ETA 2d7h"), "-" for none.
func etaOf(summary string) string {
	if _, eta, ok := strings.Cut(summary, "ETA "); ok && strings.TrimSpace(eta) != "" {
		return strings.TrimSpace(eta)
	}
	return "-"
}

// Text is the view as plain text, one line an item and no markup: the sprint line, the
// row, a line per card (id, stream, state, how long in it, the time to its deadline or
// past it, the branch), then a line per judgment. Times are from the copy's at.
func (v PullView) Text() string {
	var b strings.Builder
	b.WriteString(sprintText(v.Sprint, v.At))
	r := v.Row
	fmt.Fprintf(&b, "%s %s %s ready %s working %s/%s done %s ok %s", v.Kind, v.Name, dash(r.Status), dash(r.Ready), dash(r.Working), dash(r.Width), dash(r.Done), dash(r.OKPct))
	if v.Kind == KindMachine {
		fmt.Fprintf(&b, " load %s", dash(r.Load))
	}
	b.WriteString("\n")
	for _, c := range v.Cards {
		fmt.Fprintf(&b, "%s %s %s %s %s %s\n", c.ID, dash(c.Stream), c.State, since(v.At, c.Since), due(v.At, c.Deadline), dash(c.Branch))
	}
	for _, j := range v.Judgments {
		fmt.Fprintf(&b, "judgment %s %s on %s\n", j.ID, j.Kind, j.Card)
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// since is how long a card has been in its state at now: "16m", "-" when unknown.
func since(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return span(now.Sub(t))
}

// due is the time left to a deadline at now, "due 1h44m", or past it, "late 5m".
func due(now, t time.Time) string {
	switch {
	case t.IsZero():
		return "due -"
	case t.Before(now):
		return "late " + span(now.Sub(t))
	}
	return "due " + span(t.Sub(now))
}

// span is a duration in the sprint line's words: 45s, 16m, 1h44m, 2d7h.
func span(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

// Pull is the handler of the pull routes, served on listeners of their own (the page's
// proxy never fronts them): /api/sprint, /api/friend/<name>, /friend/<name>,
// /api/machine/<name>, /machine/<name>, /team, /api/team, the event streams /events, /events/friend/<name>
// and /events/machine/<name>, and /healthz. Each answers from the cached copy, read at
// most once per Every however many ask; every answer is no-store and (but a stream's)
// carries the copy's time in Sprint-At; nothing is written.
func (s *Server) Pull() http.Handler { return http.HandlerFunc(s.servePull) }

func (s *Server) servePull(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store, max-age=0")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "the pull routes are read-only: GET only", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/healthz":
		s.send(w, "text/plain; charset=utf-8", []byte("ok\n"))
		return
	case "/events":
		s.events(w, r, func(*sprintCopy) ([]byte, bool) { return s.Snapshot(), true })
		return
	}
	s.Refresh()
	s.mu.Lock()
	c := s.copy
	s.mu.Unlock()
	if c == nil {
		http.Error(w, "the sprint has not been read yet; ask again in a second", http.StatusServiceUnavailable)
		return
	}
	h.Set("Sprint-At", c.At.Format(time.RFC3339))
	path := r.URL.Path
	if path == "/api/sprint" {
		s.send(w, "application/json", s.Snapshot())
		return
	}
	rest, api := strings.CutPrefix(path, "/api/")
	rest, stream := strings.CutPrefix(rest, "/events/")
	if !api && !stream {
		rest = strings.TrimPrefix(path, "/")
	}
	if rest == "team" && !stream {
		t := teamView(c)
		if api {
			s.send(w, "application/json", mustJSON(t))
		} else {
			s.send(w, "text/plain; charset=utf-8", []byte(t.Text()))
		}
		return
	}
	kind, name, ok := strings.Cut(rest, "/")
	if !ok || (kind != KindFriend && kind != KindMachine) {
		http.Error(w, "not found: the pull routes are /friend/<name>, /machine/<name>, their /api/ and /events/ forms, /team, /api/team, /api/sprint and /events", http.StatusNotFound)
		return
	}
	v, found := pullView(c, kind, name)
	if !found {
		table := map[string]string{KindFriend: "friends", KindMachine: "fleet"}[kind]
		http.Error(w, fmt.Sprintf("no %s named %q on the %s table", kind, name, table), http.StatusNotFound)
		return
	}
	switch {
	case stream:
		s.events(w, r, func(c *sprintCopy) ([]byte, bool) {
			v, ok := pullView(c, kind, name)
			return mustJSON(v), ok
		})
		return
	case !api:
		s.send(w, "text/plain; charset=utf-8", []byte(v.Text()))
		return
	}
	s.send(w, "application/json", mustJSON(v))
}

func mustJSON(v any) []byte {
	body, err := json.Marshal(v)
	if err != nil {
		panic("dashboard: a pull view does not marshal: " + err.Error())
	}
	return body
}
