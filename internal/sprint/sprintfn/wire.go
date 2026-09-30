package sprintfn

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The wire of the two calls, shared by Redis and Twin so that both refuse a
// malformed request the same way, before anything is sent or run:
//
//	FCALL    ns_sprint_step 0 tset/1 <step> <sprint>
//	FCALL_RO ns_sprint_read 0 tset/1 <read plan>
//
// <step> is Layer 1's own request bytes (tset.EncodeStep of the step's
// entries, op, intent, result and fence), so S.open validates and replays it
// unchanged; <sprint> is the sprint's half: meta, intents, guards, note
// requests, quarantine, agenda edits and the parts. <read plan> is one tset/1
// read plan whose queries are Layer 1's and Layer 2's, then the sprint's, in
// that order (AL5: answers align with queries). The design fixes neither
// envelope; this is the narrower reading, listed as an open question.
//
// Layer 1 names the deployment prefix of a step and a read with one word that
// the tree's generality guardrail also lists as a machine's name. Every use of
// it in this package is in this file, in newTSetStep, newTSetReadPlan,
// pagePrefix and readPlanWire, so that a rename by Layer 1 is one change here.

// The function names the two calls reach.
const (
	fnStep = "ns_sprint_step"
	fnRead = "ns_sprint_read"
)

// encodedStep is one request, statically checked and encoded: Layer 1's step
// (for the twin), its bytes and the sprint half's bytes (for the store).
type encodedStep struct {
	step   tset.Step
	raw    []byte
	sprint []byte
}

// newTSetStep is the Layer 1 step of a request: its entries, its op and the
// fence. The sprint's notes are J's, appended in the pre stage, so the step
// carries none.
func newTSetStep(prefix string, req *Request) tset.Step {
	st := tset.Step{Epoch: req.Epoch, Space: prefix, Fence: req.Fence, Entries: req.Body.Entries}
	if st.Entries == nil {
		st.Entries = []tset.Entry{}
	}
	if op := req.Body.Op; op != nil {
		id, intent := op.ID, op.Intent
		st.Op, st.Intent, st.Result = &id, &intent, op.Result
	}
	return st
}

// newTSetReadPlan is a Layer 1 read plan over the prefix.
func newTSetReadPlan(prefix string, epoch tset.Decimal, mode string, qs []tset.ReadQuery) tset.ReadPlan {
	return tset.ReadPlan{Epoch: epoch, Space: prefix, Mode: mode, Queries: qs}
}

// pagePrefix fills a page plan's prefix from the client's, and refuses one
// that names another deployment.
func pagePrefix(prefix string, p tset.ReadPlan) (tset.ReadPlan, *Refusal) {
	if named := p.Space; named != "" && named != prefix {
		return p, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	p.Space = prefix
	return p, nil
}

// readPlanWire is ns_sprint_read's plan: tset/1's read plan with the sprint's
// queries in its queries array.
type readPlanWire struct {
	Epoch   tset.Decimal      `json:"epoch"`
	Prefix  string            `json:"space"`
	Mode    string            `json:"mode"`
	Queries []json.RawMessage `json:"queries"`
}

// hasSprintField says a request carries anything for the sprint's phases:
// E5 refuses a fence that does.
func hasSprintField(req *Request) bool {
	b := req.Body
	return len(b.Intents) != 0 || len(b.Guards) != 0 || len(b.Notes) != 0 || len(b.Quarantine) != 0 ||
		len(b.Done) != 0 || len(b.Requeue) != 0 || req.Lease != nil || req.Pop != nil ||
		req.Ingest != nil || req.Beat != nil || req.Clock != nil || req.Sprint != nil
}

// EncodedSize is the bytes a request is sent as, both halves together: what
// L1 6 bounds at 4 MiB and what the tick's budget counts at 2 MiB of requests
// a round trip (1.0, "Bytes"; 1.4.2). A request the static checks refuse is
// that refusal, and has no size.
func EncodedSize(prefix string, req *Request) (int, *Refusal) {
	enc, ref := encodeStep(prefix, req)
	if ref != nil {
		return 0, ref
	}
	return len(enc.raw) + len(enc.sprint), nil
}

// encodeStep checks a request statically and encodes both halves. A refusal
// here means nothing was sent (Redis) or run (Twin).
func encodeStep(prefix string, req *Request) (encodedStep, *Refusal) {
	if req == nil || !tset.ValidDecimal(req.Epoch) {
		return encodedStep{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	if req.Fence {
		// E5: an op, no entries, no caller result, and no sprint field.
		if req.Body.Op == nil || len(req.Body.Entries) != 0 || req.Body.Op.Result != "" || hasSprintField(req) {
			return encodedStep{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
		}
	}
	if req.Pop != nil && (req.Pop.Limit < 1 || req.Pop.Limit > PopMax) {
		return encodedStep{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	// The sprint part owns the quarantine's records: a quarantine that only the
	// body names would be lost, since a part runs only when its field is set.
	if !quarantineCarried(req) {
		return encodedStep{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	if req.Clock != nil {
		switch req.Clock.Verb {
		case ClockInit, ClockStart, ClockStop, ClockClear:
		default:
			return encodedStep{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
		}
	}
	st := newTSetStep(prefix, req)
	raw, err := tset.EncodeStep(st)
	if err != nil {
		return encodedStep{}, refusalOf(PhaseOpen, err)
	}
	sp, err := json.Marshal(sprintWireOf(req))
	if err != nil {
		return encodedStep{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	// L1 6: the encoded request is at most 4 MiB, both halves together.
	if len(raw)+len(sp) > tset.MaxWriteRequestBytes {
		return encodedStep{}, refuse(PhaseOpen, CodeLimit, RefusalDetail{RefusalDetail: tset.RefusalDetail{Budget: "request_bytes"}})
	}
	return encodedStep{step: st, raw: raw, sprint: sp}, nil
}

// encodeRead checks an atomic read statically and encodes it.
func encodeRead(prefix string, rr *ReadRequest) ([]byte, *Refusal) {
	if rr == nil || !tset.ValidDecimal(rr.Epoch) {
		return nil, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	n := len(rr.Tset) + len(rr.Sprint)
	if n == 0 {
		return nil, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	if n > tset.MaxQueries { // AL4: at least 1,024 queries a read
		return nil, refuse(PhaseOpen, CodeLimit, RefusalDetail{})
	}
	queries := make([]json.RawMessage, 0, n)
	if len(rr.Tset) != 0 {
		if err := tset.ValidateReadPlan(newTSetReadPlan(prefix, rr.Epoch, "atomic", rr.Tset)); err != nil {
			return nil, refusalOf(PhaseOpen, err)
		}
		for _, q := range rr.Tset {
			b, err := json.Marshal(q)
			if err != nil {
				return nil, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
			}
			queries = append(queries, b)
		}
	}
	for i, q := range rr.Sprint {
		if r := checkSprintQuery(q); r != nil {
			index := len(rr.Tset) + i
			r.Detail.QueryIndex = &index
			return nil, r
		}
		queries = append(queries, q.Query)
	}
	b, err := json.Marshal(readPlanWire{Epoch: rr.Epoch, Prefix: prefix, Mode: "atomic", Queries: queries})
	if err != nil {
		return nil, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	if len(b) > tset.MaxReadRequestBytes {
		return nil, refuse(PhaseOpen, CodeLimit, RefusalDetail{})
	}
	return b, nil
}

// checkSprintQuery holds a sprint query to E6's shape: a JSON object whose
// kind is its Kind, not one of Layer 1's or Layer 2's kinds, with an explicit
// fields array.
func checkSprintQuery(q SprintQuery) *Refusal {
	var m map[string]json.RawMessage
	if q.Kind == "" || lowerKinds[q.Kind] || json.Unmarshal(q.Query, &m) != nil {
		return refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	var kind string
	if json.Unmarshal(m["kind"], &kind) != nil || kind != q.Kind {
		return refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	var fields []string
	if raw, ok := m["fields"]; !ok || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	return nil
}

// lowerKinds are Layer 1's and Layer 2's query kinds, which a sprint query
// may not take (E6: a duplicate kind is CONFIG in the store; here it is the
// caller's fault).
var lowerKinds = map[string]bool{"range": true, "count": true, "rcount": true, "ids": true,
	"rows": true, "done": true, "last": true, "lines": true, "cardlines": true}

// checkPage holds a page plan to errata E2: Mode "page", exactly one Kind
// "lines" query.
func checkPage(prefix string, p *tset.ReadPlan) (tset.ReadPlan, *Refusal) {
	if p == nil || p.Mode != "page" || len(p.Queries) != 1 || p.Queries[0].Kind != "lines" {
		return tset.ReadPlan{}, refuse(PhaseOpen, CodeRequest, RefusalDetail{})
	}
	plan, r := pagePrefix(prefix, *p)
	if r != nil {
		return tset.ReadPlan{}, r
	}
	if err := tset.ValidateReadPlan(plan); err != nil {
		return tset.ReadPlan{}, refusalOf(PhaseOpen, err)
	}
	return plan, nil
}

// refusalOf turns a Layer 1 refusal error into this package's; any other
// error from an encoder is the caller's malformed request.
func refusalOf(phase string, err error) *Refusal {
	var r *tset.Refusal
	if errors.As(err, &r) {
		return fromTset(phase, r)
	}
	return refuse(phase, CodeRequest, RefusalDetail{})
}

// The sprint half of a step on the wire. Integers that name a generation, a
// score or a time are exact decimal strings, as Layer 1's wire has them.

type sprintWire struct {
	Meta       metaWire         `json:"meta"`
	Intents    []intentWire     `json:"intents,omitempty"`
	Guards     []guardWire      `json:"guards,omitempty"`
	Notes      []noteReqWire    `json:"notes,omitempty"`
	Quarantine []quarantineWire `json:"quarantine,omitempty"`
	Done       []string         `json:"done,omitempty"`
	Requeue    []string         `json:"requeue,omitempty"`
	Lease      *leaseWire       `json:"lease,omitempty"`
	Pop        *popWire         `json:"pop,omitempty"`
	Ingest     *ingestWire      `json:"ingest,omitempty"`
	Beat       *beatWire        `json:"beat,omitempty"`
	Clock      *clockWire       `json:"clock,omitempty"`
	Sprint     *sprintPartWire  `json:"sprint,omitempty"`
}

type metaWire struct {
	Verb  string `json:"verb,omitempty"`
	Actor string `json:"actor,omitempty"`
	Rule  string `json:"rule,omitempty"`
	Tick  bool   `json:"tick,omitempty"`
	Gen   string `json:"gen,omitempty"`
}

type intentWire struct {
	Kind    string   `json:"kind"`
	Card    string   `json:"card"`
	Need    string   `json:"need,omitempty"`
	Needs   []string `json:"needs,omitempty"`
	Waiters []string `json:"waiters,omitempty"`
}

type guardWire struct {
	Kind   string `json:"kind"`
	Member string `json:"member,omitempty"`
	Key    string `json:"key,omitempty"`
	Score  string `json:"score"`
}

type noteReqWire struct {
	Op        string   `json:"op"`
	Type      string   `json:"type"`
	Cause     string   `json:"cause"`
	Subjects  []string `json:"subjects"`
	Text      string   `json:"text,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	Until     string   `json:"until,omitempty"`
}

type quarantineWire struct {
	ID     string   `json:"id"`
	Stream string   `json:"stream,omitempty"`
	Code   string   `json:"code"`
	Rule   string   `json:"rule,omitempty"`
	Cells  []string `json:"cells"`
}

type leaseWire struct {
	Owner     string            `json:"owner"`
	Name      string            `json:"name,omitempty"`
	HoldMS    string            `json:"hold_ms"`
	Heartbeat map[string]string `json:"heartbeat,omitempty"`
	Stopped   bool              `json:"stopped,omitempty"`
}

type popWire struct {
	Limit int `json:"limit"`
}

type ingestWire struct {
	From tset.Decimal    `json:"from"`
	To   tset.Decimal    `json:"to"`
	Keys []agendaKeyWire `json:"keys"`
}

type agendaKeyWire struct {
	Key string `json:"key"`
	Seq string `json:"seq"`
}

type beatWire struct {
	Members []beatMemberWire `json:"members"`
}

type beatMemberWire struct {
	Member string `json:"member"`
	Load   string `json:"load,omitempty"`
}

type clockWire struct {
	Verb string `json:"verb"`
}

type sprintPartWire struct {
	Counter     *counterWire      `json:"counter,omitempty"`
	Dropping    map[string]string `json:"dropping,omitempty"`
	Undrop      map[string]string `json:"undrop,omitempty"`
	Goals       map[string]string `json:"goals,omitempty"`
	Sweep       string            `json:"sweep,omitempty"`
	Park        []parkedWire      `json:"park,omitempty"`
	Unpark      []string          `json:"unpark,omitempty"`
	Coordinator string            `json:"coordinator,omitempty"`
	Quarantine  []quarantineWire  `json:"quarantine,omitempty"`
	TickEnd     *tickEndWire      `json:"tickend,omitempty"`
	Time        *timeWire         `json:"time,omitempty"`
}

type timeWire struct {
	Due         []dueAtWire     `json:"due,omitempty"`
	UnarmBehind bool            `json:"unarm_behind,omitempty"`
	Goals       []goalClaimWire `json:"goals,omitempty"`
	Clock       *clockSetWire   `json:"clock,omitempty"`
}

type dueAtWire struct {
	Key string       `json:"key"`
	At  tset.Decimal `json:"at"`
}

type goalClaimWire struct {
	Person string       `json:"person"`
	R      tset.Decimal `json:"r"`
}

type clockSetWire struct {
	DueSince   *tset.Decimal `json:"due_since_ms,omitempty"`
	StopRaised *tset.Decimal `json:"stopraised_ms,omitempty"`
}

type parkedWire struct {
	Key    string `json:"key"`
	Rule   string `json:"rule,omitempty"`
	Code   string `json:"code"`
	Budget string `json:"budget,omitempty"`
	Actual string `json:"actual,omitempty"`
	Limit  string `json:"limit,omitempty"`
}

type tickEndWire struct {
	Backlog tset.Decimal `json:"backlog"`
}

type counterWire struct {
	Read map[string]string `json:"read"`
	Set  map[string]string `json:"set"`
}

func decimal64(v int64) string { return strconv.FormatInt(v, 10) }

func sprintWireOf(req *Request) sprintWire {
	w := sprintWire{Meta: metaWire{Verb: req.Meta.Verb, Actor: req.Meta.Actor, Rule: req.Meta.Rule, Tick: req.Meta.Tick}}
	if req.Meta.Gen != 0 {
		w.Meta.Gen = strconv.FormatUint(req.Meta.Gen, 10)
	}
	for _, in := range req.Body.Intents {
		w.Intents = append(w.Intents, intentWire{Kind: in.Kind, Card: in.Card, Need: in.Need, Needs: in.Needs, Waiters: in.Waiters})
	}
	for _, g := range req.Body.Guards {
		w.Guards = append(w.Guards, guardWire{Kind: g.Kind, Member: g.Member, Key: g.Key, Score: decimal64(g.Score)})
	}
	for _, n := range req.Body.Notes {
		nw := noteReqWire{Op: n.Op, Type: n.Type, Cause: n.Cause, Subjects: n.Subjects, Text: n.Text, Decisions: n.Decisions}
		if nw.Subjects == nil {
			nw.Subjects = []string{}
		}
		if n.Until != 0 {
			nw.Until = decimal64(n.Until)
		}
		w.Notes = append(w.Notes, nw)
	}
	for _, q := range req.Body.Quarantine {
		cells := q.Cells
		if cells == nil {
			cells = []string{}
		}
		w.Quarantine = append(w.Quarantine, quarantineWire{ID: q.ID, Stream: q.Stream, Code: q.Code, Rule: q.Rule, Cells: cells})
	}
	w.Done, w.Requeue = req.Body.Done, req.Body.Requeue
	if l := req.Lease; l != nil {
		w.Lease = &leaseWire{Owner: l.Owner, Name: l.Name, HoldMS: decimal64(l.HoldMS), Heartbeat: l.Heartbeat, Stopped: l.Stopped}
	}
	if p := req.Pop; p != nil {
		w.Pop = &popWire{Limit: p.Limit}
	}
	if in := req.Ingest; in != nil {
		iw := &ingestWire{From: in.From, To: in.To, Keys: []agendaKeyWire{}}
		for _, k := range in.Keys {
			iw.Keys = append(iw.Keys, agendaKeyWire{Key: k.Key, Seq: strconv.FormatUint(k.Seq, 10)})
		}
		w.Ingest = iw
	}
	if b := req.Beat; b != nil {
		bw := &beatWire{Members: []beatMemberWire{}}
		for _, m := range b.Members {
			bw.Members = append(bw.Members, beatMemberWire{Member: m.Member, Load: m.Load})
		}
		w.Beat = bw
	}
	if c := req.Clock; c != nil {
		w.Clock = &clockWire{Verb: c.Verb}
	}
	if s := req.Sprint; s != nil {
		sw := &sprintPartWire{Dropping: s.Dropping, Undrop: s.Undrop, Goals: s.Goals, Sweep: s.Sweep, Unpark: s.Unpark,
			Coordinator: s.Coordinator}
		if s.Counter != nil {
			sw.Counter = &counterWire{Read: s.Counter.Read, Set: s.Counter.Set}
		}
		for _, k := range s.Park {
			sw.Park = append(sw.Park, parkedWire{Key: k.Key, Rule: k.Rule, Code: k.Code, Budget: k.Budget, Actual: k.Actual, Limit: k.Limit})
		}
		for _, q := range s.Quarantine {
			cells := q.Cells
			if cells == nil {
				cells = []string{}
			}
			sw.Quarantine = append(sw.Quarantine, quarantineWire{ID: q.ID, Stream: q.Stream, Code: q.Code, Rule: q.Rule, Cells: cells})
		}
		if s.TickEnd != nil {
			sw.TickEnd = &tickEndWire{Backlog: s.TickEnd.Backlog}
		}
		if t := s.Time; t != nil {
			tw := &timeWire{UnarmBehind: t.UnarmBehind}
			for _, d := range t.Due {
				tw.Due = append(tw.Due, dueAtWire{Key: d.Key, At: d.At})
			}
			for _, g := range t.Goals {
				tw.Goals = append(tw.Goals, goalClaimWire{Person: g.Person, R: g.R})
			}
			if c := t.Clock; c != nil {
				tw.Clock = &clockSetWire{DueSince: c.DueSince, StopRaised: c.StopRaised}
			}
			sw.Time = tw
		}
		w.Sprint = sw
	}
	return w
}
