package member

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A worker asks its queue for the packets it may use this pass and no others (the fleet
// load test of 2026-10-01 20:18 ET: a reader's answer, its 150 asked reads each with its
// brief, was 579,181 bytes, every pass): a reader the reads its lanes may begin, a member
// none (its take hands its packets), and both every in-flight card they hold no launch for.
// These tests run the member against packetServer, a sprint that keeps its cards and
// answers the queue by the server's rule (cmd/nova-sprint, wanted.of).

// packetServer is a sprint of one worker's cards that answers queue, take, read --begin and
// the reports as the server does, handing a queue's packets as --packets and --have ask. old
// is a server from before the flags: it refuses --packets as the verb's parse does.
type packetServer struct {
	mu      sync.Mutex
	epoch   uint64
	cards   []queueCard // in queue order; each with its packet, which the queue hands or not
	old     bool
	calls   [][]string
	handed  []int // the packets each queue answer carried
	answers []int // each queue answer's bytes
}

func (s *packetServer) Run(args ...string) (int, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, slices.Clone(args))
	flag := func(name string) (string, bool) {
		i := slices.Index(args, name)
		if i < 0 || i+1 >= len(args) {
			return "", false
		}
		return args[i+1], true
	}
	switch verbOf(args) {
	case "queue":
		n, asked := flag("--packets")
		if asked && s.old {
			return 2, []byte("nova-sprint queue: unknown flag --packets; run: nova-sprint help queue")
		}
		left := -1
		if asked {
			left, _ = strconv.Atoi(n)
		}
		haveWords, _ := flag("--have")
		have := strings.Split(haveWords, ",")
		out := queueOut{As: "w", Epoch: s.epoch, Cards: []queueCard{}}
		handed := 0
		for _, c := range s.cards {
			switch {
			case left < 0:
			case slices.Contains(have, c.ID):
				c.Packet = nil
			case c.Col == "asked" || c.Col == "ready":
				if left == 0 {
					c.Packet = nil
					break
				}
				left--
			}
			if c.Packet != nil {
				handed++
			}
			out.Cards = append(out.Cards, c)
		}
		b, _ := json.Marshal(out)
		s.handed = append(s.handed, handed)
		s.answers = append(s.answers, len(b))
		return 0, b
	case "begin":
		for i, c := range s.cards {
			if slices.Contains(args, c.ID) {
				s.cards[i].Col = "reading"
			}
		}
		return 0, nil
	case "take":
		n, _ := flag("--limit")
		limit, _ := strconv.Atoi(n)
		var t takeOut
		for i, c := range s.cards {
			if c.Col == "ready" && len(t.Packets) < limit {
				s.cards[i].Col = "working"
				t.Packets = append(t.Packets, *c.Packet)
			}
		}
		b, _ := json.Marshal(t)
		return 0, b
	case "return":
		// a read returned is asked again in place (of this reader, here)
		for i, c := range s.cards {
			if c.ID == args[4] {
				s.cards[i].Col = "asked"
			}
		}
		return 0, nil
	case "report", "finish":
		id := args[4] // read --as r --ok <id>; finish --as m <id>@<gen> is args[3]
		if args[0] == "finish" {
			id, _, _ = strings.Cut(args[3], "@")
		}
		s.cards = slices.DeleteFunc(s.cards, func(c queueCard) bool { return c.ID == id })
		return 0, nil
	}
	return 0, nil
}

// queues is the pass's queue verbs, each a line, since the last forget.
func (s *packetServer) queues() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		if c[0] == "queue" {
			out = append(out, strings.Join(c, " "))
		}
	}
	return out
}

// verbs is every verb since the last forget, each a line.
func (s *packetServer) verbs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func (s *packetServer) lastHanded() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handed[len(s.handed)-1]
}

func (s *packetServer) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

// set changes the card of the id.
func (s *packetServer) set(id string, change func(*queueCard)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.cards {
		if s.cards[i].ID == id {
			change(&s.cards[i])
		}
	}
}

// readCard is an asked read of a 2.5 KB brief, at attempt 1, epoch 7.
func readCard(i int) queueCard {
	id := fmt.Sprintf("p%03d.r1.r", i)
	p := Packet{Card: id, Kind: "read", As: "r", Primary: fmt.Sprintf("p%03d", i), Stream: "s", Attempt: 1, Epoch: 7,
		Brief: strings.Repeat("The work, in the words a child reads. ", 66), Notes: []string{}}
	return queueCard{ID: id, Table: "readers", Row: "r", Col: "asked", Attempt: 1, Packet: &p}
}

// workCard is a work card of the column, at attempt 1, gen 1, epoch 7.
func workCard(i int, col string) queueCard {
	id := fmt.Sprintf("p%03d.w1", i)
	p := pk(id)
	p.Brief = strings.Repeat("The work, in the words a child reads. ", 66)
	return queueCard{ID: id, Table: "fleet", Row: "m", Col: col, Gen: 1, Attempt: 1, Packet: &p}
}

func packetRig(cfg Config, s *packetServer) *rig {
	g := newRig(cfg)
	g.m.sprint = s
	return g
}

// TestAReaderAsksForThePacketsOfItsFreeLanesOnly: a reader of width 8 holding 150 asked
// reads sends one queue a pass, asking for 8 packets, is handed 8 and begins 8. With every
// lane reading it asks for none and is handed none. A read that ends frees its lane in the
// pass that reports it: that pass asks for one packet and begins one read.
func TestAReaderAsksForThePacketsOfItsFreeLanesOnly(t *testing.T) {
	t.Parallel()
	s := &packetServer{epoch: 7}
	for i := range 150 {
		s.cards = append(s.cards, readCard(i))
	}
	g := packetRig(Config{As: "r", Width: 8, Reader: true}, s)

	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"queue --as r --json --packets 8"}, s.queues(), "one queue a pass")
	assert.Equal(t, 8, s.lastHanded())
	assert.Len(t, g.r.started(), 8)
	assert.Equal(t, 8, g.m.Running())
	first := slices.Clone(g.r.started())

	s.forget()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"queue --as r --json --packets 0 --have " + strings.Join(first, ",")}, s.queues(), "all lanes busy: no packet asked")
	assert.Zero(t, s.lastHanded())
	assert.Len(t, g.r.started(), 8, "nothing more begun")

	// a read ends: the pass that reports it begins the next in the lane it frees
	g.r.child(first[0]).end(Result{Ran: true, OK: true, Verdict: "ok", Report: "fine"})
	s.forget()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"queue --as r --json --packets 1 --have " + strings.Join(first, ",")}, s.queues())
	assert.Equal(t, 1, s.lastHanded())
	assert.Len(t, g.r.started(), 9, "the freed lane began a read in the same pass")
	assert.Equal(t, 8, g.m.Running())
	t.Logf("the reader's answers, by pass: %d, %d, %d bytes", s.answers[0], s.answers[1], s.answers[2])
}

// TestAReadReturnedAMomentAgoTakesNoLaneOfTheAsk: a read this reader returned with no
// verdict, asked of it again, is not begun before ReadStageRetry; it is named in --have, so
// the packets asked for go to reads it will begin. Asked for by count alone, the returned
// read, first in queue order, would take the packet and the lane would stay empty.
func TestAReadReturnedAMomentAgoTakesNoLaneOfTheAsk(t *testing.T) {
	t.Parallel()
	s := &packetServer{epoch: 7}
	for i := range 6 {
		s.cards = append(s.cards, readCard(i))
	}
	g := packetRig(Config{As: "r", Width: 2, Reader: true}, s)
	_, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"p000.r1.r", "p001.r1.r"}, g.r.started())

	g.r.child("p000.r1.r").end(Result{Ran: false, Report: "no verdict"})
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Contains(t, g.out.String(), "read p000.r1.r: returned")
	require.Equal(t, []string{"p000.r1.r", "p001.r1.r", "p002.r1.r"}, g.r.started())

	g.r.child("p001.r1.r").end(Result{Ran: true, OK: true, Verdict: "ok", Report: "fine"})
	s.forget()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"queue --as r --json --packets 1 --have p000.r1.r,p001.r1.r,p002.r1.r"}, s.queues())
	assert.Equal(t, []string{"p000.r1.r", "p001.r1.r", "p002.r1.r", "p003.r1.r"}, g.r.started(), "the freed lane began the next read, not the returned one")
}

// A read this reader handed back is named in --have only until it may begin it again
// (ReadStageRetry): past that it is asked for like any other, its packet comes, and it is
// begun again. Named for ever, the server would never send its packet and the read, still
// asked of this reader, would never be begun again (Stella's finding on 3bb54ac3).
func TestAReturnedReadIsAskedForAgainOnceItMayBeBegun(t *testing.T) {
	t.Parallel()
	s := &packetServer{epoch: 7}
	s.cards = append(s.cards, readCard(0))
	g := packetRig(Config{As: "r", Width: 1, Reader: true}, s)
	_, err := g.tickAt(t, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"p000.r1.r"}, g.r.started())
	g.r.child("p000.r1.r").end(Result{Ran: false, Report: "no verdict"})
	_, err = g.tickAt(t, 0)
	require.NoError(t, err)
	require.Contains(t, g.out.String(), "read p000.r1.r: returned")

	s.forget()
	_, err = g.tickAt(t, 1) // inside the retry time: still held, not begun
	require.NoError(t, err)
	assert.Equal(t, []string{"queue --as r --json --packets 1 --have p000.r1.r"}, s.queues())
	assert.Equal(t, []string{"p000.r1.r"}, g.r.started(), "not begun again before the retry time")

	s.forget()
	_, err = g.tickAt(t, int64(ReadStageRetry/time.Second))
	require.NoError(t, err)
	assert.Equal(t, []string{"queue --as r --json --packets 1"}, s.queues(), "past the retry time it is no longer named as held")
	assert.Equal(t, []string{"p000.r1.r", "p000.r1.r"}, g.r.started(), "its packet came and it was begun again")
}

// TestARestartedMemberAsksForItsWorkingCardsAndRecoversThem: a member that starts with three
// cards working at the server and no child names none in --have, so its first queue hands
// the three packets and it runs them again; no ready card's packet is asked (its take hands
// them). The pass after, holding all three, it is handed none.
func TestARestartedMemberAsksForItsWorkingCardsAndRecoversThem(t *testing.T) {
	t.Parallel()
	s := &packetServer{epoch: 7}
	for i := range 3 {
		s.cards = append(s.cards, workCard(i, "working"))
	}
	for i := 3; i < 8; i++ {
		s.cards = append(s.cards, workCard(i, "ready"))
	}
	g := packetRig(Config{As: "m", Width: 4}, s)

	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"queue --as m --json --packets 0"}, s.queues())
	assert.Equal(t, 3, s.lastHanded(), "the three working cards' packets, no ready card's")
	assert.Equal(t, []string{"p000.w1", "p001.w1", "p002.w1", "p003.w1"}, g.r.started(), "the three recovered, then one taken for the free lane")
	assert.Contains(t, s.verbs(), "take --as m --limit 1 --json --epoch 7")

	s.forget()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"queue --as m --json --packets 0 --have p000.w1,p001.w1,p002.w1,p003.w1"}, s.queues())
	assert.Zero(t, s.lastHanded())
	assert.Len(t, g.r.started(), 4)
}

// TestAMovedClaimIsNoticedOnACardListedWithoutItsPacket: the queue lists a card the member
// runs without its packet, so the claim is read off the card (the answer's epoch, its gen; a
// read's attempt): a card dealt again at another generation, or a clear's new epoch, is
// reaped when its child ends and never reported, and the pass after asks for the new claim's
// packet and runs it.
func TestAMovedClaimIsNoticedOnACardListedWithoutItsPacket(t *testing.T) {
	t.Parallel()
	for name, move := range map[string]func(s *packetServer){
		"a redeal: the generation moved": func(s *packetServer) {
			s.set("p000.w1", func(c *queueCard) { c.Gen, c.Packet.Gen = 2, 2 })
		},
		"a clear: the epoch moved": func(s *packetServer) {
			s.epoch = 8
			s.set("p000.w1", func(c *queueCard) { c.Packet.Epoch = 8 })
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &packetServer{epoch: 7, cards: []queueCard{workCard(0, "working")}}
			g := packetRig(Config{As: "m", Width: 2}, s)
			_, err := g.tick(t)
			require.NoError(t, err)
			require.Equal(t, []string{"p000.w1"}, g.r.started())

			g.r.child("p000.w1").end(Result{Ran: true, OK: true, Head: "h", Report: "done"})
			move(s)
			s.forget()
			_, err = g.tick(t)
			require.NoError(t, err)
			assert.Equal(t, []string{"queue --as m --json --packets 0 --have p000.w1"}, s.queues())
			assert.Zero(t, s.lastHanded(), "listed without its packet")
			assert.Contains(t, g.out.String(), FinishReaped+" p000.w1: the claim moved")
			for _, v := range s.verbs() {
				assert.NotContains(t, v, "finish", "a moved claim's result is nobody's")
			}

			s.forget()
			_, err = g.tick(t)
			require.NoError(t, err)
			assert.Equal(t, []string{"queue --as m --json --packets 0"}, s.queues(), "the new claim is not the member's: it asks its packet")
			assert.Equal(t, 1, s.lastHanded())
			require.Len(t, g.r.packets, 2)
			again := g.r.packets[1]
			assert.Equal(t, "p000.w1", again.Card)
			assert.Equal(t, s.epoch, again.Epoch)
			assert.Equal(t, s.cards[0].Gen, again.Gen, "run at the new claim")
		})
	}
}

// TestAMemberAheadOfItsServerAsksTheOldWay: a server from before --packets refuses it
// (`unknown flag --packets`); the member asks its queue again without it in the same pass and
// goes on, every packet handed as before. A reader's beat does the same.
func TestAMemberAheadOfItsServerAsksTheOldWay(t *testing.T) {
	t.Parallel()
	s := &packetServer{epoch: 7, old: true}
	for i := range 20 {
		s.cards = append(s.cards, readCard(i))
	}
	g := packetRig(Config{As: "r", Width: 8, Reader: true}, s)
	_, err := g.tick(t)
	require.NoError(t, err)
	assert.Equal(t, []string{"queue --as r --json --packets 8", "queue --as r --json"}, s.queues())
	assert.Equal(t, 20, s.lastHanded())
	assert.Len(t, g.r.started(), 8)

	s.forget()
	require.NoError(t, g.m.Beat())
	assert.Equal(t, "queue --as r --json", s.queues()[len(s.queues())-1])
}

// TestAReadersBeatAsksForNoPacket: a reader's beat is its queue, whose answer it does not
// read: it names every card the last pass held, so it is handed no packet.
func TestAReadersBeatAsksForNoPacket(t *testing.T) {
	t.Parallel()
	s := &packetServer{epoch: 7}
	for i := range 20 {
		s.cards = append(s.cards, readCard(i))
	}
	g := packetRig(Config{As: "r", Width: 2, Reader: true}, s)
	_, err := g.tick(t)
	require.NoError(t, err)
	s.forget()
	require.NoError(t, g.m.Beat())
	assert.Equal(t, []string{"queue --as r --json --packets 0 --have p000.r1.r,p001.r1.r"}, s.queues())
	assert.Zero(t, s.lastHanded())
}

// While Room says no (the disk floor), a reader's beat and its pass's queue carry its word
// (--no-room), so the sprint asks it no read; the first tick Room says yes, they carry none.
func TestAReadersBeatCarriesItsNoRoom(t *testing.T) {
	t.Parallel()
	s := &packetServer{epoch: 7}
	room, why := false, "free disk 0.0 GiB under the floor of 10 GiB"
	g := packetRig(Config{As: "r", Width: 2, Reader: true, Room: func() (bool, string) { return room, why }}, s)
	s.forget()
	require.NoError(t, g.m.Beat())
	assert.Equal(t, []string{"queue --as r --json --packets 0"}, s.queues(), "no word before the first tick")
	_, err := g.tick(t)
	require.NoError(t, err)
	s.forget()
	require.NoError(t, g.m.Beat())
	assert.Equal(t, []string{"queue --as r --json --packets 0 --no-room " + why}, s.queues())
	s.forget()
	_, err = g.tick(t)
	require.NoError(t, err)
	assert.Contains(t, s.queues()[0], " --no-room "+why, "the pass's queue is its beat too")
	room = true
	_, err = g.tick(t)
	require.NoError(t, err)
	s.forget()
	require.NoError(t, g.m.Beat())
	assert.Equal(t, []string{"queue --as r --json --packets 0"}, s.queues(), "the word is gone once Room says yes")
}

// The brief a card file begins with is the brief alone: the sprint's mechanics, a read's
// worker's report among them, are cut off (the decide read asks over it, as its bars were
// calibrated on the work card alone).
func TestBriefOfIsTheBriefAlone(t *testing.T) {
	t.Parallel()
	brief := "c1: do it (s1) tier: flash\n\nThe task.\n"
	card := CardText(Packet{Card: "c1.r1", Kind: "read", Attempt: 1, Primary: "c1", Head: "abc", Report: "fine by me", Brief: brief})
	assert.Equal(t, brief, BriefOf(card))
	assert.Empty(t, BriefOf(CardText(Packet{Card: "c1.r1", Kind: "read", Attempt: 1})))
	assert.Equal(t, "no mechanics", BriefOf("no mechanics"))
}
