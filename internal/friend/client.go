// Package friend is the friend lane client: the part of a friend's supervised hook service
// (nova-hook.client.<name>) that takes her cards from the sprint server and tells her model
// once (the card the-friend-lane-is-a-client-with-no-local-state, v1.3, pulled forward by
// the owner on 2026-10-14).
//
// The server (nova-sprint) owns every friend lane's record: the card it holds, its
// generation, the lease (lane_at, renewed by every ask and progress, the tick returning a
// card unheard past LaneGoneAfter), and the finish. The client holds a cache it rebuilds
// from the server's answers and nothing else: started in an empty directory it asks the
// server what each lane holds and resumes from that alone. The protocol is the fleet's: one
// `take --as friend.<name> --lane <n>` per lane (pkg/friend LaneTakeArgv, ParseLaneHold),
// and the model finishes through the server's own finish verb.
//
// Liveness is the hook record's alone: the client asks, and so renews the leases, only
// while the model proves (Round's proven). A dead client renews nothing, and neither does a
// live client whose model stopped answering its challenges: the lease runs out and the
// server returns the card. The model is tla/FriendLaneClient.tla (OneLanePerCard,
// FinishedOnce, TakenStaysTaken, RestartResumes, DeadClientReleases, DeadModelReleases, and
// for the width config.go applies NoLostCard, NoDuplicateHold, DrainNeverTakes, GrowTakes.
// One batch ask attempts every held lane in an answered round (AllHeldRenewedEachAnsweredRound),
// and a server notice is not sent again by the client (NoticedOnce); witnesses are in
// MCFriendLaneClientBroken*.cfg).
package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	lane "github.com/mas-bandwidth/nova-tools/pkg/friend"
)

// Subject is the subject prefix of the one notice a round's new holds make.
const Subject = "SPRINT CARDS "

// Reply is one verb's answer in a round's one request: what it printed, or its refusal.
type Reply struct {
	Out string
	Err error
}

// Ask sends a round's verbs to the sprint server in one request and answers one reply per
// verb, in the request's order. An error is a request the server did not answer at all; every
// reply in it is then unknown.
type Ask func(ctx context.Context, argvs [][]string) ([]Reply, error)

// Unanswered is a round the server did not answer at all (a transport error or the round's
// bound): only such a round stops the round, and the next is asked again soon.
type Unanswered struct{ Err error }

func (u *Unanswered) Error() string { return u.Err.Error() }
func (u *Unanswered) Unwrap() error { return u.Err }

// Client is one friend's lane client. Name is her name (her row is friend.<name>), Ask the
// server, Fetch her canonical nova-config row (config.go), Dir the brief cache (a directory
// the client may find empty at any start). It keeps no width of its own: the lanes that take
// are the applied row's (Intake), read again every ConfigEvery.
type Client struct {
	Name  string
	Ask   Ask
	Fetch Fetch
	Dir   string

	told map[string]bool // the holds told this process's life: connection state, never decides
	held []int           // the lanes the server last said hold a card: renewed next round even when friend cards does not answer
	conf applied
}

// MaxLanes bounds one round's free-lane asks (the sprint server's MaxWidth): a bound on a
// round against a server that never answers a free lane empty, never a width.
const MaxLanes = 1024

// Hold is one lane's card as the server's answer names it, and where its brief is cached.
type Hold struct {
	Lane   int
	Card   string
	Gen    int
	Epoch  uint64
	Kind   string
	Job    string
	Brief  string // the cached BRIEF.md, "" when the server sent no brief
	Notice bool   // the server's take answer said notice=sent: the friend was told by the server
}

func (h Hold) key() string { return fmt.Sprintf("%d %s@%d %d", h.Lane, h.Card, h.Gen, h.Epoch) }

// Row is her fleet row on the server.
func (c *Client) Row() string { return "friend." + c.Name }

// LaneArgv is lane n's ask: the fleet's lane take, its answer as JSON (the LANE line and the
// card's packet with its brief).
func (c *Client) LaneArgv(n int) []string { return append(lane.LaneTakeArgv(c.Row(), n), "--json") }

// answer is the part of the take verb's JSON the client reads.
type answer struct {
	Says    []string `json:"says"`
	Packets []struct {
		Card  string `json:"card"`
		Brief string `json:"brief"`
	} `json:"packets"`
}

// Round is one round of the lanes, in one ask: the request carries friend cards, a lane take
// for every lane the last round found held (the server's, or when friend cards fails, the
// client's own view), and a take for each free lane up to the applied width less the held
// count. It answers every hold the server names and, of them, the ones this client has not
// told the model yet (fresh), lane order.
//
// A lane's refusal or unreadable answer is that lane's alone: it is skipped, its error kept,
// and every other lane in the request is still renewed or taken. Only a request that does not
// answer at all (a transport error, or the round's bound) stops the round: its error is
// Unanswered, and the next round is asked again soon. An empty answer on a free lane ends the
// free-lane takes, as before; the server's own width bound still holds every take.
//
// A held lane the server answers with notice=sent has already been told to the friend by the
// server (nova-sprint's take), so the client marks it told and sends no notice of its own. A
// hold without the fact is noticed as before, so either side of that server card works.
//
// The bound: a held lane the client does not know (a fresh client, or one restarted, has found
// none yet) is not renewed while friend cards keeps failing; its lease (the server's 10m)
// returns the card if that lasts. A lowered width drains: the free-lane room is the width less
// the held count, so no lane takes until the held cards are under it, and the held ones finish
// where they are (the server's own take bound holds the rest). Not proven, it asks nothing.
func (c *Client) Round(ctx context.Context, proven bool) (holds, fresh []Hold, errs []error) {
	if !proven {
		return nil, nil, nil
	}
	if c.told == nil {
		c.told = map[string]bool{}
	}
	held := slices.Clone(c.held)
	slices.Sort(held)
	held = slices.Compact(held)
	verbs := [][]string{lane.FriendCardsArgv(c.Name)}
	lanes := []int{0}
	for _, n := range held {
		verbs = append(verbs, c.LaneArgv(n))
		lanes = append(lanes, n)
	}
	room := c.Intake() - len(held)
	for n := 1; n <= MaxLanes && room > 0; n++ {
		if slices.Contains(held, n) {
			continue
		}
		verbs = append(verbs, c.LaneArgv(n))
		lanes = append(lanes, n)
		room--
	}
	replies, err := c.Ask(ctx, verbs)
	if err != nil {
		errs = append(errs, &Unanswered{Err: fmt.Errorf("the lane round: %w", err)})
		return nil, nil, errs
	}
	if len(replies) != len(verbs) {
		errs = append(errs, &Unanswered{Err: fmt.Errorf("the lane round: the server answered %d results for %d verbs", len(replies), len(verbs))})
		return nil, nil, errs
	}
	var heldCard []lane.HeldCard
	if replies[0].Err != nil {
		errs = append(errs, fmt.Errorf("friend cards: %w", replies[0].Err))
	} else if hc, perr := lane.ParseHeld(c.Name, replies[0].Out); perr != nil {
		errs = append(errs, perr)
	} else {
		heldCard = hc
	}
	for i := 1; i < len(replies); i++ {
		n := lanes[i]
		if replies[i].Err != nil {
			errs = append(errs, fmt.Errorf("lane %d: %w", n, replies[i].Err))
			holds = append(holds, Hold{Lane: n})
			continue
		}
		h, ok, aerr := c.readLane(n, replies[i].Out)
		if aerr != nil {
			errs = append(errs, fmt.Errorf("lane %d: %w", n, aerr))
			holds = append(holds, Hold{Lane: n})
			continue
		}
		if !ok {
			// A held lane remains part of the renewal set even if its answer is
			// empty; an empty free lane ends takes for this round.
			if !slices.Contains(held, n) {
				break
			}
			continue
		}
		holds = append(holds, h)
	}
	c.held = c.held[:0]
	var known []Hold
	for _, h := range holds {
		c.held = append(c.held, h.Lane)
		if h.Card == "" {
			continue // a held lane whose ask failed: renewed again next round, told nothing
		}
		known = append(known, h)
		if h.Notice {
			c.told[h.key()] = true
			continue
		}
		if !c.told[h.key()] {
			fresh = append(fresh, h)
		}
	}
	// a held lane the friend-cards answer named that no take covered (a fresh client, or
	// one above the applied width) is folded into the view so the next round renews it and
	// its lease never runs out (the model AllHeldRenewedEachAnsweredRound)
	for _, h := range heldCard {
		if h.Col == "working" && h.Lane > 0 && !slices.Contains(c.held, h.Lane) {
			c.held = append(c.held, h.Lane)
		}
	}
	slices.Sort(c.held)
	slices.SortFunc(known, func(x, y Hold) int { return x.Lane - y.Lane })
	slices.SortFunc(fresh, func(x, y Hold) int { return x.Lane - y.Lane })
	return known, fresh, errs
}

// readLane reads lane n's answer: the card, whether it names one, or why it cannot be read.
func (c *Client) readLane(n int, out string) (Hold, bool, error) {
	var a answer
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &a); err != nil {
		return Hold{}, false, fmt.Errorf("the server's answer is not the take verb's JSON: %w", err)
	}
	lh, err := lane.ParseLaneHold(c.Row(), n, strings.Join(a.Says, "\n"))
	if err != nil {
		return Hold{}, false, err
	}
	if lh.Card == "" {
		return Hold{}, false, nil
	}
	h := Hold{Lane: n, Card: lh.Card, Gen: lh.Gen, Epoch: lh.Epoch, Kind: lh.Kind, Job: lh.Job, Notice: lh.Notice}
	for _, p := range a.Packets {
		if p.Card == h.Card && strings.TrimSpace(p.Brief) != "" && c.Dir != "" {
			path := filepath.Join(c.Dir, h.Job, "BRIEF.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return h, true, err
			}
			if err := atomicfile.Write(path, []byte(p.Brief), 0o600); err != nil {
				return h, true, err
			}
			h.Brief = path
		}
	}
	return h, true, nil
}

// Told marks holds as told to the model (the notice was sent).
func (c *Client) Told(hs []Hold) {
	if c.told == nil {
		c.told = map[string]bool{}
	}
	for _, h := range hs {
		c.told[h.key()] = true
	}
}

// Notice is the one bus message for a round's fresh holds: its subject and body, the body
// naming every card the server says she holds now, its brief and the verbs that finish it.
func (c *Client) Notice(fresh, holds []Hold) (subject, body string) {
	var paths []string
	for _, h := range fresh {
		paths = append(paths, cmpOr(h.Brief, "nova-sprint card "+h.Card))
	}
	subject = fmt.Sprintf("%sn=%d as=%s", Subject, len(fresh), c.Row())
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s taken; briefs at %s; work each in a child agent to width.\n", len(fresh), plural(len(fresh), "card", "cards"), strings.Join(paths, ", "))
	b.WriteString("The sprint server holds these lanes; your hook service renews each lease while you answer your challenges, and a lease not renewed returns its card. Finish each through the server, by the ids below:\n")
	sorted := slices.Clone(holds)
	slices.SortFunc(sorted, func(x, y Hold) int { return x.Lane - y.Lane })
	for _, h := range sorted {
		at := fmt.Sprintf("%s@%d --epoch %d --lane %d", h.Card, h.Gen, h.Epoch, h.Lane)
		if h.Kind == "read" {
			fmt.Fprintf(&b, "- lane %d %s (read, job %s): nova-sprint read --as %s (--ok | --broken) %s --epoch %d --lane %d\n", h.Lane, h.Card, h.Job, c.Row(), h.Card, h.Epoch, h.Lane)
			continue
		}
		fmt.Fprintf(&b, "- lane %d %s (job %s): nova-sprint finish --as %s %s (--head <commit> | --failed) --report <text>; alive: nova-sprint progress --as %s %s\n", h.Lane, h.Card, h.Job, c.Row(), at, c.Row(), at)
	}
	return subject, b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
