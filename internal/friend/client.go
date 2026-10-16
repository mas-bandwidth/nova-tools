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
// for the width config.go applies NoLostCard, NoDuplicateHold, DrainNeverTakes, GrowTakes;
// witnesses MCFriendLaneClientBroken*.cfg).
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

// Ask sends one verb to the sprint server and answers what it printed, or its refusal.
type Ask func(ctx context.Context, argv []string) (string, error)

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
	Lane  int
	Card  string
	Gen   int
	Epoch uint64
	Kind  string
	Job   string
	Brief string // the cached BRIEF.md, "" when the server sent no brief
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

// Round is one round of the lanes: every lane the server says holds a card of hers is asked
// (and so renewed), whatever the width now; then, while her held cards are fewer than the
// applied width (Intake), free lanes are asked in order, each taking her next ready card,
// until one answers empty (the server had none to give, or its own width bound held). It
// answers every hold the server names and, of them, the ones this client has not told the
// model yet (fresh), lane order.
//
// The held lanes are the server's answer to friend cards (each working card's lane). When it
// does not answer the round is blind: the lane take is the only lane ask the server has (no
// renew-only form: a free lane's ask is a take, and progress, the one verb that renews a lane
// without dealing, also stamps the late rule's sign that the card moves, which the client
// cannot say), so a blind round asks only the lanes the last round found held, and only
// while their count is within the applied width (Intake above 0): a held lane that freed
// since then takes at most into a lane she held, never past her width, never while her row
// is unread. Otherwise it asks nothing and keeps what it last found. The bound: a held lane
// the client does not know (a fresh client, or one restarted, has found none yet), or one
// held while she drains past a lowered width or her row is unread, is not renewed while
// friend cards keeps failing, and its lease (the server's 10m) returns the card if that
// lasts. A lowered width drains: no lane takes until the held cards are under it, and the
// held ones finish where they are. A raised width takes on the next round.
// Not proven, it asks nothing: an ask renews a lease, and only a proving model's lanes are
// renewed. A lane whose ask fails is skipped this round, its error kept, and takes stop for
// the round; the server's record stands, and the next round asks again.
func (c *Client) Round(ctx context.Context, proven bool) (holds, fresh []Hold, errs []error) {
	if !proven {
		return nil, nil, nil
	}
	if c.told == nil {
		c.told = map[string]bool{}
	}
	asked := map[int]bool{}
	lanes, err := c.serverHolds(ctx)
	blind := err != nil
	if blind {
		// friend cards did not answer: the lanes the last round found held are asked again,
		// and only while they are within her width, so no blind ask takes past it (an ask of a
		// held lane that finished since is a take, the server's lane ask having no renew-only
		// form); else nothing is asked and the last view kept (tla/FriendLaneClient.tla
		// RenewBlind, BlindAsk; witness MCFriendLaneClientBrokenBlindTakes.cfg)
		errs = append(errs, err)
		if w := c.Intake(); w == 0 || len(c.held) > w {
			return nil, nil, errs
		}
		lanes = slices.Clone(c.held)
	}
	slices.Sort(lanes)
	lanes = slices.Compact(lanes)
	failed := false
	ask := func(n int) (Hold, bool) {
		asked[n] = true
		h, ok, err := c.ask(ctx, n)
		if err != nil {
			errs = append(errs, fmt.Errorf("lane %d: %w", n, err))
			failed = true
			if slices.Contains(lanes, n) {
				// asked again next round: a lane whose answer did not read still holds its card
				holds = append(holds, Hold{Lane: n})
			}
			return Hold{}, false
		}
		return h, ok
	}
	for _, n := range lanes {
		if h, ok := ask(n); ok {
			holds = append(holds, h)
		}
	}
	for n := 1; n <= MaxLanes && !blind && !failed && len(holds) < c.Intake(); n++ {
		if asked[n] {
			continue
		}
		h, ok := ask(n)
		if !ok {
			break
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
		if !c.told[h.key()] {
			fresh = append(fresh, h)
		}
	}
	slices.SortFunc(known, func(x, y Hold) int { return x.Lane - y.Lane })
	slices.SortFunc(fresh, func(x, y Hold) int { return x.Lane - y.Lane })
	return known, fresh, errs
}

// serverHolds is the lanes of her row the server says hold a working card (friend cards, the
// read verb the hook's heartbeat server already asks).
func (c *Client) serverHolds(ctx context.Context) ([]int, error) {
	out, err := c.Ask(ctx, lane.FriendCardsArgv(c.Name))
	if err != nil {
		return nil, fmt.Errorf("friend cards: %w", err)
	}
	cards, err := lane.ParseHeld(c.Name, out)
	if err != nil {
		return nil, err
	}
	var lanes []int
	for _, h := range cards {
		if h.Col == "working" && h.Lane > 0 {
			lanes = append(lanes, h.Lane)
		}
	}
	return lanes, nil
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

// ask is lane n's ask: the server's answer read, the brief cached under Dir/<job>/BRIEF.md.
func (c *Client) ask(ctx context.Context, n int) (Hold, bool, error) {
	out, err := c.Ask(ctx, c.LaneArgv(n))
	if err != nil {
		return Hold{}, false, err
	}
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
	h := Hold{Lane: n, Card: lh.Card, Gen: lh.Gen, Epoch: lh.Epoch, Kind: lh.Kind, Job: lh.Job}
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
