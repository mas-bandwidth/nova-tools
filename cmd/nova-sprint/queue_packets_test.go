package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A worker's queue hands the packets the worker asks for and no others (the fleet load test
// of 2026-10-01 20:18 ET: a reader's answer, its 150 asked reads each with its brief, was
// 579,181 bytes, every pass). These tests pin the rule on the server, over a twin, through
// the fleet listener (serverRig.one): --packets n is the first n cards the worker may start
// and every card in flight, each not named by --have; a card left without its packet still
// carries its id, column, attempt and gen, and the answer its epoch; no flag is the answer
// as it was.

// briefOf is a brief that passes the card lint, about 2.5 KB, as a fleet card's is.
func briefOf(i int) string {
	lead := fmt.Sprintf("Fix part %d. ", i)
	for len(passingBrief(lead)) < 2500 {
		lead += "The work, in the words a child reads. "
	}
	return lead
}

// workedRig is a server whose member m (width n) took n cards of n primaries, each with a
// 2.5 KB brief: the cards are working. ready is how many more primaries are dealt to it
// ready (at most n: the deal runs twice the width ahead).
func workedRig(t *testing.T, n, ready int) (*serverRig, []string) {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n+ready; i++ {
		writeNeedsBrief(t, dir, fmt.Sprintf("b%03d", i), briefOf(i), "")
	}
	r := newServerRig(t, fmt.Sprintf("nova-sprint init --readers r,r2 --members m:%d", n),
		"nova-sprint add --stream s --brief-dir "+dir, "nova-sprint start", "nova-sprint tick", "nova-sprint tick")
	cards := taken(t, r.one("take", "--as", "m", "--limit", fmt.Sprint(n), "--epoch", "0", "--json"))
	require.Len(t, cards, n)
	return r, cards
}

// askedRig is a server whose reader r holds n asked reads: n primaries worked and finished
// ok, and asked of the sprint's two readers.
func askedRig(t *testing.T, n int) *serverRig {
	t.Helper()
	r, cards := workedRig(t, n, 0)
	for _, c := range cards {
		res := r.one("finish", "--as", "m", c, "--epoch", "0", "--report", "done", "--head", "0123456789abcdef0123456789abcdef01234567")
		require.Equal(t, 0, res.Code, res.Stderr)
	}
	r.queue("r") // a reader's queue is its beat: a reader that never beat is asked nothing
	r.boss("nova-sprint tick")
	r.boss("nova-sprint tick")
	require.Len(t, r.queue("r")["asked"], n)
	return r
}

// queueCardOut is one card of a queue's answer, as a worker reads it.
type queueCardOut struct {
	ID      string          `json:"id"`
	Col     string          `json:"col"`
	Attempt int             `json:"attempt"`
	Gen     int             `json:"gen"`
	Packet  json.RawMessage `json:"packet"`
}

// queueOut is a queue's answer: its bytes, its epoch and its cards.
type queueAnswer struct {
	bytes int
	Epoch *uint64        `json:"epoch"`
	Cards []queueCardOut `json:"cards"`
}

func (r *serverRig) queueWith(words ...string) queueAnswer {
	r.t.Helper()
	res := r.one(append([]string{"queue"}, words...)...)
	require.Equal(r.t, 0, res.Code, res.Stderr)
	var q queueAnswer
	require.NoError(r.t, json.Unmarshal([]byte(res.Stdout), &q), res.Stdout)
	q.bytes = len(res.Stdout)
	return q
}

// packeted is the ids of the answer's cards that carry a packet, in order.
func (q queueAnswer) packeted() []string {
	var ids []string
	for _, c := range q.Cards {
		if len(c.Packet) > 0 {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

func (q queueAnswer) ids(col string) []string {
	var ids []string
	for _, c := range q.Cards {
		if c.Col == col {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// TestAQueueAskingForPacketsCarriesOnlyThose: a reader of width 8 holding 150 asked reads
// asks for the packets of its 8 free lanes and is handed those 8, the first 8 asked in
// queue order; every other card is listed with its id, column and attempt, and the answer
// its epoch. With all 8 lanes reading it asks for none and is handed none; a read in flight
// it does not name in --have (a restart) comes with its packet. The answer's bytes, before
// and after, are the measure.
func TestAQueueAskingForPacketsCarriesOnlyThose(t *testing.T) {
	t.Parallel()
	r := askedRig(t, 150)

	before := r.queueWith("--as", "r", "--json")
	require.Len(t, before.Cards, 150)
	require.Len(t, before.packeted(), 150, "no flag: every card carries its packet, as before")
	asked := before.ids("asked")

	after := r.queueWith("--as", "r", "--json", "--packets", "8")
	require.Len(t, after.Cards, 150, "every card is listed")
	assert.Equal(t, asked[:8], after.packeted(), "the packets of the first 8 asked, in queue order")
	require.NotNil(t, after.Epoch)
	assert.Equal(t, *before.Epoch, *after.Epoch)
	for _, c := range after.Cards {
		assert.Equal(t, "asked", c.Col, c.ID)
		assert.Equal(t, 1, c.Attempt, "%s carries its claim without its packet", c.ID)
	}
	t.Logf("a reader's queue, 150 asked reads, width 8: %d bytes before, %d bytes asking --packets 8", before.bytes, after.bytes)
	assert.Less(t, after.bytes*8, before.bytes, "the answer is an eighth of what it was, or less: 8 packets, and 150 cards listed")

	// the 8 begun: a pass with every lane busy asks for none, and is handed none
	res := r.one(append(append([]string{"read", "--as", "r", "--begin"}, asked[:8]...), "--epoch", "0")...)
	require.Equal(t, 0, res.Code, res.Stderr)
	have := strings.Join(asked[:8], ",")
	busy := r.queueWith("--as", "r", "--json", "--packets", "0", "--have", have)
	assert.Equal(t, asked[:8], busy.ids("reading"))
	assert.Empty(t, busy.packeted(), "all lanes busy, nothing asked: no packet")
	t.Logf("the same reader, its 8 lanes reading: %d bytes asking --packets 0 --have <its 8>", busy.bytes)

	// a read in flight the worker does not name comes with its packet (it holds no launch for it)
	recovered := r.queueWith("--as", "r", "--json", "--packets", "0", "--have", strings.Join(asked[:7], ","))
	assert.Equal(t, asked[7:8], recovered.packeted())
	// none named: every read in flight, and the first n asked
	restart := r.queueWith("--as", "r", "--json", "--packets", "2")
	assert.Equal(t, append(slices.Clone(asked[8:10]), asked[:8]...), restart.packeted(), "the asked column, then the reading")

	// no flag is the answer as it was: byte for byte the answer that asks for every packet
	plain := r.one("queue", "--as", "r", "--json")
	all := r.one("queue", "--as", "r", "--json", "--packets", "1024")
	require.Equal(t, 0, plain.Code, plain.Stderr)
	assert.Equal(t, all.Stdout, plain.Stdout)
}

// TestAMembersQueueCarriesNoPacketForACardItRuns: a member's ready cards come with their
// packets from its take, never its queue, so it asks for none: a member of width 32 with 32
// cards working and 32 ready is handed no packet for any it names, and the packet of every
// working card it does not (a restart).
func TestAMembersQueueCarriesNoPacketForACardItRuns(t *testing.T) {
	t.Parallel()
	r, cards := workedRig(t, 32, 32)
	before := r.queueWith("--as", "m", "--json")
	require.Len(t, before.Cards, 64)
	require.Len(t, before.packeted(), 64)
	working := before.ids("working")
	require.Len(t, working, 32)
	for i, c := range cards {
		cards[i], _, _ = strings.Cut(c, "@")
	}
	assert.ElementsMatch(t, cards, working)

	after := r.queueWith("--as", "m", "--json", "--packets", "0", "--have", strings.Join(working, ","))
	assert.Empty(t, after.packeted())
	for _, c := range after.Cards {
		assert.Equal(t, 1, c.Attempt, c.ID)
		if c.Col == "working" {
			assert.Equal(t, 1, c.Gen, "%s carries its gen without its packet", c.ID)
		}
	}
	t.Logf("a member's queue, 32 working and 32 ready: %d bytes before, %d bytes asking --packets 0 --have <its 32>", before.bytes, after.bytes)
	assert.Less(t, after.bytes*10, before.bytes)

	restart := r.queueWith("--as", "m", "--json", "--packets", "0")
	assert.Equal(t, working, restart.packeted(), "a restarted member names none: every working card's packet, no ready card's")
}

// TestTheServerRefusesAMalformedPacketsAsk: from the fleet, a queue's --packets is a count
// from 0 to 1024 and --have card ids, given once each, --have only with --packets; anything
// else is refused before the verb runs, exit 2, nothing changed. The same words run locally
// are refused by the verb.
func TestTheServerRefusesAMalformedPacketsAsk(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	for name, words := range map[string][]string{
		"a negative count":           {"--packets", "-1"},
		"not a number":               {"--packets", "all"},
		"past the bound":             {"--packets", "1025"},
		"no value":                   {"--packets"},
		"given twice":                {"--packets", "1", "--packets", "2"},
		"given twice, with =":        {"--packets=1", "-packets", "2"},
		"--have with no --packets":   {"--have", "s1-1.w1"},
		"--have of no card id":       {"--packets", "1", "--have", "s1-1.w1,../etc"},
		"--have with an empty id":    {"--packets", "1", "--have", "s1-1.w1,,s1-2.w1"},
		"--have with a space":        {"--packets", "1", "--have", "s1-1.w1 s1-2.w1"},
		"--have twice":               {"--packets", "1", "--have", "s1-1.w1", "--have", "s1-2.w1"},
		"--have swallowing a flag":   {"--packets", "1", "--have", "--json"},
		"a count with a fraction":    {"--packets", "1.5"},
		"--have with too many parts": {"--packets", "1", "--have", "a.b.c.d"},
	} {
		argv := append([]string{"queue", "--as", "m1", "--json"}, words...)
		res := r.one(argv...)
		assert.Equal(t, 2, res.Code, "%s: %v", name, argv)
		assert.Contains(t, res.Stderr, "nothing was changed", "%s: %v", name, argv)
		assert.Empty(t, res.Stdout, "%s: %v", name, argv)
	}
	ok := r.one("queue", "--as", "m1", "--json", "--packets=0", "--have=s1-1.w1,s1-2.w1")
	assert.Equal(t, 0, ok.Code, ok.Stderr)

	var out, errb strings.Builder
	assert.Equal(t, 2, r.a.run(split("queue --as m1 --json --packets 1 --have ../x --redis "+r.a.serveAddr), &out, &errb), "the verb refuses it too")
	assert.Contains(t, errb.String(), "--have is card ids")
	errb.Reset()
	assert.Equal(t, 2, r.a.run(split("queue --stream s1 --json --packets 1 --redis "+r.a.serveAddr), &out, &errb))
	assert.Contains(t, errb.String(), "--packets is a worker's")
}

// TestAServerRefusesAQueueFlagItDoesNotKnowInTheWordsAMemberReads: a server from before
// --packets refuses it as the verb's parse refuses any flag it does not define, and a
// member installed ahead of its server asks again the old way on those words
// (internal/member queueOf): they are pinned here.
func TestAServerRefusesAQueueFlagItDoesNotKnowInTheWordsAMemberReads(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, twoLanes()...)
	res := r.one("queue", "--as", "m1", "--json", "--nosuch", "8")
	assert.Equal(t, 2, res.Code)
	assert.Contains(t, res.Stderr, "unknown flag --nosuch", "an old server says `unknown flag --packets`")
}
