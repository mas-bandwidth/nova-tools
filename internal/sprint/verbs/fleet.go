package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The fleet verbs (section 3's table: fleet up, fleet down, fleet beat, reader
// add; 1.4.4; item IT20). A member's status is its control card's (1.3.1: up,
// down or held); a beat is fresh while beat:<m> lies above R in the due set,
// in running time, and never by the wall stamp of the beat record (1.4.4).

// RowsAddMax is the most row names one step adds without an advance (1.0's
// bounds table: 100). fleet up and reader add of more new rows go in steps of
// this many.
const RowsAddMax = 100

// The words of the fleet verbs' notes: the subject of a judgment about the
// sprint, and the cause "no fleet member is up" is kept under (R6 raises it
// with the rule's name, deal; 2.2, 1.3.4).
const (
	flSubjectSprint = "sprint"
	flCauseNoMember = "deal"
)

// The control card's fields (1.3.1): the status, and the wall stamp of its
// last change for the reader. held is the hold the present machine keeps beside
// a status of down, which fleet up clears with the status.
const (
	wkfStatus = "status"
	wkfSince  = "since"
	wkfHeld   = "held"
	wkfKind   = "kind"
)

// FleetReq is fleet up m... or fleet down m...: the members, and the op a
// repeat returns the recorded result by (section 3: --op).
type FleetReq struct {
	Members []string
	Op      string
}

// flSprintRows is what the fleet verbs read of the four tables' rows: the rows
// of each (AL3), from which the members, readers and streams of the size
// bounds are counted (section 3, "Size of the sprint").
type flSprintRows struct {
	fleet, readers, streams map[string]bool
}

// flRowsQueries are the rows of the four tables, in clear's order.
func flRowsQueries() []tset.ReadQuery {
	out := make([]tset.ReadQuery, 0, 4)
	for _, t := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		out = append(out, tset.ReadQuery{Kind: "rows", Table: t})
	}
	return out
}

// flRowsOf decodes flRowsQueries' answers, at Layer 1 slots 0 to 3. A stream is a
// row of the work table or the merge table.
func flRowsOf(rd *sprintfn.ReadReply) (flSprintRows, error) {
	if rd == nil || len(rd.Tset) < 4 {
		return flSprintRows{}, fmt.Errorf("verbs: the read has no rows of the four tables")
	}
	set := func(a tset.ReadAnswer) map[string]bool {
		m := map[string]bool{}
		for _, r := range a.Rows {
			m[r.Row] = true
		}
		return m
	}
	streams := set(rd.Tset[0])
	for s := range set(rd.Tset[2]) {
		streams[s] = true
	}
	return flSprintRows{fleet: set(rd.Tset[3]), readers: set(rd.Tset[1]), streams: streams}, nil
}

// flNamedRows checks a set of member or reader names: at least one, each a name,
// sorted, each once.
func flNamedRows(verb, what string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s names no %s", verb, what)
	}
	for _, n := range names {
		if !sprint.ValidID(n) {
			return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%q is not a %s's name (letters, digits, _ and -)", n, what)
		}
	}
	out := wkUniqueSorted(names)
	if len(out) != len(names) {
		return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "a %s is named twice", what)
	}
	return out, nil
}

// flNamesArg is a list of names as an intent carries it (L1 5): the list
// itself, or its digest when it is long (IDsDigest), so the intent stays under
// 64 KiB.
func flNamesArg(names []string) any {
	if len(names) > 64 {
		return IDsDigest(names)
	}
	return names
}

// flEpochOf is the read's epoch as a number, for the stored ids of control cards.
func flEpochOf(rd *sprintfn.ReadReply) (uint64, error) {
	n, err := strconv.ParseUint(string(rd.Epoch), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("verbs: the read's epoch %q is not a number", rd.Epoch)
	}
	return n, nil
}

// flEpochNum is an epoch as a number, 0 when it is not one (the read refuses it).
func flEpochNum(d tset.Decimal) uint64 {
	n, _ := strconv.ParseUint(string(d), 10, 64)
	return n
}

// flControlIDs are the stored ids of the members' control cards at an epoch.
func flControlIDs(members []string, epoch uint64) []string {
	out := make([]string, len(members))
	for i, m := range members {
		out[i] = sprint.StoredID(sprint.CtlID(m), epoch)
	}
	return out
}

// flBeatQuery is the read of beat:<m> in the due set for each member (1.4.4).
func flBeatQuery(members []string) sprintfn.SprintQuery {
	q, ref := sprintfn.EncodeKeyQ(sprintfn.KeyQ{Kind: sprintfn.KeyBeat, IDs: members})
	if ref != nil {
		panic(fmt.Sprintf("verbs: the beat query refused: %v", ref)) // members are checked names
	}
	return q
}

// flBeatsAt decodes the beat read at a sprint slot: each member's beat:<m>
// score, and whether it has one.
func flBeatsAt(rd *sprintfn.ReadReply, slot int, members []string) (map[string]int64, error) {
	if slot >= len(rd.Sprint) {
		return nil, fmt.Errorf("verbs: the read has no beat answer")
	}
	qr, err := sprintfn.DecodeResult(sprintfn.KeyBeat, rd.Sprint[slot])
	if err != nil {
		return nil, err
	}
	b, ok := qr.(sprintfn.BeatResult)
	if !ok || len(b.Scores) != len(members) {
		return nil, fmt.Errorf("verbs: the beat answer holds %d scores for %d members", len(b.Scores), len(members))
	}
	out := map[string]int64{}
	for i, s := range b.Scores {
		if s == nil {
			continue
		}
		n, err := strconv.ParseInt(*s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("verbs: beat:%s is at %q, not a running time", members[i], *s)
		}
		out[members[i]] = n
	}
	return out, nil
}

// flInRowSteps runs a verb over names in steps whose new rows are at most
// RowsAddMax (1.0's bounds table), each its own read and step; a set whose new
// rows fit one step is one step. An op names one step: a set of more new rows
// than a step adds is refused with an op.
func flInRowSteps(ctx context.Context, verb, op string, names []string, run func(ctx context.Context, names []string) (Result, error)) (Result, error) {
	if len(names) <= RowsAddMax {
		return run(ctx, names)
	}
	if op != "" {
		return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s of %d names is more than one step's %d new rows: run it without --op, or in sets of %d", verb, len(names), RowsAddMax, RowsAddMax)
	}
	return wkInSteps(ctx, verb, len(names), RowsAddMax, func(ctx context.Context, lo, hi int) (Result, error) {
		return run(ctx, names[lo:hi])
	})
}

// ---- fleet up

// FleetUp brings members up (section 3, fleet up; the model's fleetup,
// SprintEvents.tla VEff and VGuard): each named member's status is up when its
// beat is fresh (beat:<m> above R in the due set, read in the same snapshot as
// R, 1.4.4), and down otherwise, so a member held down by fleet down is
// released and comes up at its next beat (the beat part enters seen:<m> for a
// member that is down, and R1 raises it). A member with no fleet row gets its
// row and its control card, refused past the sprint's size bounds (section 3).
// A member already up is left as it is. The step is guarded by each control
// card's revision as read, by the beat entry as read for a member brought up
// (XGUARD when a pop took it since: the member went stale), and, for a member
// left down, by no beat above R (beatstale: a beat since the read refuses the
// step, and the verb plans again and finds it fresh). KNOW "fleet member up"
// names the members that came up, and "no fleet member is up" is closed with
// them (the model: JClose nomember when fresh). Two round trips.
func FleetUp(ctx context.Context, e *Env, req FleetReq) (Result, error) {
	const verb = "fleet up"
	members, err := flNamedRows(verb, "member", req.Members)
	if err != nil {
		return Result{Verb: verb}, err
	}
	return flInRowSteps(ctx, verb, req.Op, members, func(ctx context.Context, members []string) (Result, error) {
		return flUpStep(ctx, e, req.Op, members)
	})
}

func flUpStep(ctx context.Context, e *Env, op string, members []string) (Result, error) {
	const verb = "fleet up"
	var came, down []string
	res, err := e.Do(ctx, Planned{Verb: verb, Op: op, Args: map[string]any{"members": flNamesArg(members)},
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
			return &sprintfn.ReadRequest{Epoch: epoch,
				Tset:   append(flRowsQueries(), wkIdsRead(sprint.Fleet, flControlIDs(members, flEpochNum(epoch)), []string{wkfStatus, wkfHeld})),
				Sprint: []sprintfn.SprintQuery{wkClockQuery(), flBeatQuery(members)}}
		},
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			came, down = came[:0], down[:0]
			epoch, err := flEpochOf(rd)
			if err != nil {
				return nil, err
			}
			rows, err := flRowsOf(rd)
			if err != nil {
				return nil, err
			}
			ctls, err := wkRecordsAt(rd, 4, len(members))
			if err != nil {
				return nil, err
			}
			r, wall, err := wkClockAt(rd, 0)
			if err != nil {
				return nil, err
			}
			beats, err := flBeatsAt(rd, 1, members)
			if err != nil {
				return nil, err
			}
			var newRows, bad []string
			var entries []tset.Entry
			var guards []sprintfn.XGuard
			for i, m := range members {
				ctl, id := ctls[i], sprint.StoredID(sprint.CtlID(m), epoch)
				score, has := beats[m]
				fresh := has && score > r
				status := sprint.Down
				if fresh {
					status = sprint.Up
				}
				switch {
				case ctl.Exists && ctl.Place == nil:
					bad = append(bad, fmt.Sprintf("%s: its control card was removed in this epoch, and an id is never used again within its epoch (clear restores it)", m))
					continue
				case !ctl.Exists:
					if !rows.fleet[m] {
						newRows = append(newRows, m)
					}
					entries = append(entries, tset.Entry{Kind: "create", Table: sprint.Fleet, To: m + ":" + sprint.Ctl,
						IDs: []string{id}, Scores: []string{"0"}, About: []string{id},
						Set: map[string]string{wkfKind: "member", wkfStatus: status, wkfSince: wkWallStamp(wall)}})
				default:
					if ctl.Place.Row != m || ctl.Place.Col != sprint.Ctl {
						bad = append(bad, fmt.Sprintf("%s: its control card is at %s, not %s:%s", m, wkPlaceOf(ctl), m, sprint.Ctl))
						continue
					}
					was := wkFieldStr(ctl, wkfStatus)
					if was == sprint.Up || (was == status && wkFieldStr(ctl, wkfHeld) == "") {
						continue // up already, or down and not fresh: nothing to change
					}
					entries = append(entries, tset.Entry{Kind: "move", Table: sprint.Fleet, From: m + ":" + sprint.Ctl, To: m + ":" + sprint.Ctl,
						IDs: []string{id}, Revs: []tset.Decimal{ctl.Revision}, About: []string{id},
						Set: map[string]string{wkfStatus: status, wkfSince: wkWallStamp(wall)}, Unset: []string{wkfHeld}})
				}
				if fresh {
					came = append(came, m)
					guards = append(guards, sprintfn.XGuard{Kind: sprintfn.XGuardDue, Key: "beat:" + m, Score: score})
				} else {
					down = append(down, m)
					guards = append(guards, sprintfn.XGuard{Kind: sprintfn.XGuardBeatStale, Member: m})
				}
			}
			if len(bad) > 0 {
				return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s", strings.Join(bad, "; "))
			}
			if len(entries) == 0 {
				return nil, nil // every member named is up already, or down with no fresh beat
			}
			if len(newRows) > 0 {
				if err := SizeBounds(len(rows.fleet)+len(newRows), len(rows.readers), len(rows.streams)); err != nil {
					return nil, wkWorkRefuse(verb, sprintfn.CodeLimit, "adding %s: %v", wkNameList(newRows), err)
				}
				entries = append([]tset.Entry{{Kind: "rows", Table: sprint.Fleet, Add: newRows}}, entries...)
			}
			var notes []sprintfn.NoteReq
			if len(came) > 0 {
				notes = append(notes,
					wkKnowNote(sprint.NMemberUp, "fleet member up: "+wkNameList(came)+" (fleet up; its beat is fresh)", came),
					sprintfn.NoteReq{Op: sprintfn.JOpClose, Type: sprint.NNoMember, Cause: flCauseNoMember,
						Subjects: []string{flSubjectSprint}, Text: "a member is up: " + wkNameList(came)})
			}
			return &sprintfn.Request{Body: sprintfn.Body{Entries: entries, Guards: guards, Notes: notes}}, nil
		}})
	if err != nil {
		return res, err
	}
	if res.Replay {
		return res, nil
	}
	if res.Step == nil {
		res.Said = "fleet up: nothing to change: " + wkNameList(members) + " up already, or down with no fresh beat"
		return res, nil
	}
	var parts []string
	if len(came) > 0 {
		parts = append(parts, "up: "+wkNameList(came))
	}
	if len(down) > 0 {
		parts = append(parts, "down until they beat: "+wkNameList(down))
	}
	res.Said = "fleet up: " + strings.Join(parts, "; ")
	return res, nil
}

// ---- fleet down

// FleetDown holds members down (section 3, fleet down; the model's fleetdown):
// each named member's status is held, which is sticky: a beat does not bring a
// held member up (the beat part enters seen:<m> only for a status of down,
// 1.4.4), and only fleet up releases it. Its line queues down:<m>, and R2 deals
// the member's cards away (2.1, R2); the verb writes the status and its
// notice, nothing else (V5). A member already held is left as it is. The step
// is guarded by each control card's revision as read. KNOW "fleet member down"
// names the members held. Two round trips.
func FleetDown(ctx context.Context, e *Env, req FleetReq) (Result, error) {
	const verb = "fleet down"
	members, err := flNamedRows(verb, "member", req.Members)
	if err != nil {
		return Result{Verb: verb}, err
	}
	var held []string
	res, err := e.Do(ctx, Planned{Verb: verb, Op: req.Op, Args: map[string]any{"members": flNamesArg(members)},
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
			return &sprintfn.ReadRequest{Epoch: epoch,
				Tset:   []tset.ReadQuery{wkIdsRead(sprint.Fleet, flControlIDs(members, flEpochNum(epoch)), []string{wkfStatus})},
				Sprint: []sprintfn.SprintQuery{wkClockQuery()}}
		},
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			held = held[:0]
			ctls, err := wkRecordsAt(rd, 0, len(members))
			if err != nil {
				return nil, err
			}
			_, wall, err := wkClockAt(rd, 0)
			if err != nil {
				return nil, err
			}
			var bad []string
			var entries []tset.Entry
			for i, m := range members {
				ctl := ctls[i]
				if !ctl.Exists || ctl.Place == nil || ctl.Place.Row != m || ctl.Place.Col != sprint.Ctl {
					bad = append(bad, fmt.Sprintf("%s: no fleet member (its control card: %s)", m, wkPlaceOf(ctl)))
					continue
				}
				if wkFieldStr(ctl, wkfStatus) == sprint.Held {
					continue // held already
				}
				entries = append(entries, tset.Entry{Kind: "move", Table: sprint.Fleet, From: m + ":" + sprint.Ctl, To: m + ":" + sprint.Ctl,
					IDs: []string{ctl.ID}, Revs: []tset.Decimal{ctl.Revision}, About: []string{ctl.ID},
					Set: map[string]string{wkfStatus: sprint.Held, wkfSince: wkWallStamp(wall)}})
				held = append(held, m)
			}
			if len(bad) > 0 {
				return nil, wkWorkRefuse(verb, sprintfn.CodeRequest, "%s", strings.Join(bad, "; "))
			}
			if len(entries) == 0 {
				return nil, nil
			}
			return &sprintfn.Request{Body: sprintfn.Body{Entries: entries,
				Notes: []sprintfn.NoteReq{wkKnowNote(sprint.NMemberDown, "fleet member down: "+wkNameList(held)+" (fleet down: held until fleet up)", held)}}}, nil
		}})
	if err != nil || res.Replay {
		return res, err
	}
	if res.Step == nil {
		res.Said = "fleet down: " + wkNameList(members) + " held already; nothing to change"
		return res, nil
	}
	res.Said = "fleet down: held " + wkNameList(held) + " (until fleet up)"
	return res, nil
}

// ---- fleet beat

// FleetBeatReq is fleet beat m...: the members a machine beats for, each with
// its load sample for the reader.
type FleetBeatReq struct {
	Members []string
	Loads   map[string]string
}

// BeatSaid is what the beat part decided (IT16's reply): when the beat stays
// fresh until (R + 15 s), how many members beat, the members it entered in
// seen:<m> (down, or with no fleet row) and the strangers it noticed.
type BeatSaid struct {
	FreshUntil string   `json:"fresh_until"`
	Members    int      `json:"members"`
	Seen       []string `json:"seen"`
	Strangers  []string `json:"strangers"`
}

// FleetBeat is one beat for a set of members (section 3, fleet beat; 1.4.4):
// one ns_sprint_step{beat}, with no read (the members' control cards are read
// in the pre stage), one round trip. For each member the beat part writes the
// beat record, moves beat:<m> to R + 15 s, and enters seen:<m> when the
// member's status is down (held does not count) or it has no fleet row. It
// writes no line and changes no table record, so it is idempotent: a beat
// again is the same beat, later.
func FleetBeat(ctx context.Context, e *Env, req FleetBeatReq) (Result, error) {
	const verb = "fleet beat"
	members, err := flNamedRows(verb, "member", req.Members)
	if err != nil {
		return Result{Verb: verb}, err
	}
	if len(members) > sprintfn.SprintMembersMax {
		return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeLimit, "a beat is for at most %d members, the sprint's most", sprintfn.SprintMembersMax)
	}
	for m, load := range req.Loads {
		if len(load) > sprintfn.BeatLoadBytesMax || strings.ContainsAny(load, "\x00\r\n") {
			return Result{Verb: verb}, wkWorkRefuse(verb, sprintfn.CodeRequest, "the load of %s is over %d bytes or not one line", m, sprintfn.BeatLoadBytesMax)
		}
	}
	part := &sprintfn.BeatPart{}
	for _, m := range members {
		part.Members = append(part.Members, sprintfn.BeatMember{Member: m, Load: req.Loads[m]})
	}
	res, err := e.Do(ctx, Planned{Verb: verb,
		Plan: func(*sprintfn.ReadReply) (*sprintfn.Request, error) {
			return &sprintfn.Request{Beat: part}, nil
		}})
	if err != nil {
		return res, err
	}
	var said BeatSaid
	if res.Step != nil {
		if raw, ok := res.Step.Parts[sprintfn.PartBeat]; ok {
			if err := json.Unmarshal(raw, &said); err != nil {
				return res, fmt.Errorf("fleet beat: the beat part's reply: %w", err)
			}
		}
	}
	res.Said = fmt.Sprintf("fleet beat: %d members, fresh until R %s", len(members), said.FreshUntil)
	if len(said.Seen) > 0 {
		res.Said += "; seen: " + wkNameList(said.Seen)
	}
	if len(said.Strangers) > 0 {
		res.Said += "; no fleet row: " + wkNameList(said.Strangers) + " (add with fleet up)"
	}
	return res, nil
}

// ---- reader add

// ReaderAddReq is reader add r...: the readers, and the op.
type ReaderAddReq struct {
	Readers []string
	Op      string
}

// ReaderAdd adds readers (section 3, reader add): each reader's row in the
// readers table, refused past the sprint's size bounds (members + readers + 2
// x streams at most 1,024), so clear can restore every row in one step. A
// reader with a row already is left as it is; a set of only those writes
// nothing. Its line queues askwait (2.1), so primaries that could not be asked
// are asked again. Two round trips.
func ReaderAdd(ctx context.Context, e *Env, req ReaderAddReq) (Result, error) {
	const verb = "reader add"
	readers, err := flNamedRows(verb, "reader", req.Readers)
	if err != nil {
		return Result{Verb: verb}, err
	}
	return flInRowSteps(ctx, verb, req.Op, readers, func(ctx context.Context, readers []string) (Result, error) {
		return flReaderAddStep(ctx, e, req.Op, readers)
	})
}

func flReaderAddStep(ctx context.Context, e *Env, op string, readers []string) (Result, error) {
	const verb = "reader add"
	var added []string
	res, err := e.Do(ctx, Planned{Verb: verb, Op: op, Args: map[string]any{"readers": flNamesArg(readers)},
		Read: func(epoch tset.Decimal) *sprintfn.ReadRequest {
			return &sprintfn.ReadRequest{Epoch: epoch, Tset: flRowsQueries()}
		},
		Plan: func(rd *sprintfn.ReadReply) (*sprintfn.Request, error) {
			added = added[:0]
			rows, err := flRowsOf(rd)
			if err != nil {
				return nil, err
			}
			for _, r := range readers {
				if !rows.readers[r] {
					added = append(added, r)
				}
			}
			if len(added) == 0 {
				return nil, nil
			}
			if err := SizeBounds(len(rows.fleet), len(rows.readers)+len(added), len(rows.streams)); err != nil {
				return nil, wkWorkRefuse(verb, sprintfn.CodeLimit, "adding %s: %v", wkNameList(added), err)
			}
			return &sprintfn.Request{Body: sprintfn.Body{Entries: []tset.Entry{{Kind: "rows", Table: sprint.Readers, Add: added}}}}, nil
		}})
	if err != nil || res.Replay {
		return res, err
	}
	if res.Step == nil {
		res.Said = "reader add: " + wkNameList(readers) + " are readers already; nothing to change"
		return res, nil
	}
	sort.Strings(added)
	res.Said = "reader add: " + wkNameList(added)
	return res, nil
}
