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
// FinishedOnce, TakenStaysTaken, RestartResumes, DeadClientReleases, DeadModelReleases;
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

// Client is one friend's lane client. Name is her name (her row is friend.<name>), Width
// her lanes (lanes 1..Width are asked), Ask the server, Dir the brief cache (a directory the
// client may find empty at any start).
type Client struct {
	Name  string
	Width int
	Ask   Ask
	Dir   string

	told map[string]bool // the holds told this process's life: connection state, never decides
}

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

// Round asks the server once for every lane, 1..Width, and answers every hold the server
// names and, of them, the ones this client has not told the model yet (fresh), lane order.
// Not proven, it asks nothing: an ask renews a lease, and only a proving model's lanes are
// renewed. A lane whose ask fails is skipped this round, its error kept; the server's record
// stands, and the next round asks again.
func (c *Client) Round(ctx context.Context, proven bool) (holds, fresh []Hold, errs []error) {
	if !proven || c.Width <= 0 {
		return nil, nil, nil
	}
	if c.told == nil {
		c.told = map[string]bool{}
	}
	for n := 1; n <= c.Width; n++ {
		h, ok, err := c.ask(ctx, n)
		if err != nil {
			errs = append(errs, fmt.Errorf("lane %d: %w", n, err))
			continue
		}
		if !ok {
			continue
		}
		holds = append(holds, h)
		if !c.told[h.key()] {
			fresh = append(fresh, h)
		}
	}
	return holds, fresh, errs
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
