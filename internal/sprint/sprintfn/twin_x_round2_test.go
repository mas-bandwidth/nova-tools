package sprintfn

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The second round of X's tests, from the cold read of the first: what it found, and
// the probes it ran that no test caught. Each test runs through the harness's
// mirror, so the Lua half is held to the twin's on every step, refusal and command.

// xMembers is how many members a command names.
func xMembers(c Cmd) int {
	if c.Argv[0] == "ZADD" || c.Argv[0] == "HSET" {
		return (len(c.Argv) - 2) / 2
	}
	return len(c.Argv) - 2
}

// xCmdsOn is the commands among cmds that are name on key.
func xCmdsOn(cmds []Cmd, name, key string) []Cmd {
	var out []Cmd
	for _, c := range cmds {
		if c.Argv[0] == name && c.Argv[1] == key {
			out = append(out, c)
		}
	}
	return out
}

// xNames is n ids, prefix0000 to prefix<n-1>.
func xNames(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%04d", prefix, i)
	}
	return out
}

// xBulkCreate creates ids in a cell of the work table, each scored below the counter.
func xBulkCreate(to string, ids []string, set map[string]string) tset.Entry {
	scores := make([]string, len(ids))
	for i := range ids {
		scores[i] = strconv.Itoa(1 + i%900)
	}
	return tset.Entry{Kind: "create", Table: sprint.Work, To: to, IDs: ids, Scores: scores, Set: set, About: ids}
}

// xSameSize holds every command to at most xPiece members and their total to want.
func xSameSize(t *testing.T, what string, cmds []Cmd, name, key string, want int) {
	t.Helper()
	total := 0
	for _, c := range xCmdsOn(cmds, name, key) {
		if n := xMembers(c); n > xPiece {
			t.Fatalf("%s: a %s of %s names %d members, over the bound of %d", what, name, key, n, xPiece)
		} else {
			total += n
		}
	}
	if total != want {
		t.Fatalf("%s: the %s commands of %s name %d members, want %d", what, name, key, total, want)
	}
}

// TestXPiecesAtTheBound: every command X writes and every read X.pre makes is cut
// at 1,000 members, in both halves, and a step past the bound is cut in pieces and
// not refused (L1 1.4: the store refuses a command of 1,001 members LIMIT): 1,500
// creates into one cell give elig two ZADDs; moving them gives elig two ZREMs and
// fresh two ZADDs; 1,200 quarantined ids give elig, fresh, again and askwait two
// ZREMs each; 1,200 done keys give the agenda two ZREMs; 1,200 requeued keys are
// read in two ZMSCOREs and added in two ZADDs. Its reads of the quarantine's marks
// are the ids 16 to a read once one card is marked.
func TestXPiecesAtTheBound(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"), Command("HSET", xp+"quarantine@0", kindHash, "zz", "DRIFT"))
	cards := xNames("b", 1500)

	h.applies("1,500 creates into one cell", xVerb("add", xBulkCreate("s1:waiting", cards, map[string]string{"kind": "primary", "open": "0"})))
	xSameSize(t, "1,500 creates", h.last, "ZADD", xp+"elig:s1@0", 1500)
	if len(h.last) != 2 {
		t.Fatalf("1,500 creates wrote %d commands, want the two pieces of elig: %v", len(h.last), h.last)
	}
	marks, pieces := 0, 0
	for _, r := range h.mirror.reads {
		if r.key != xp+"quarantine@0" || r.cmd != "HMGET" {
			continue
		}
		marks += r.members
		pieces++
		if r.members > xMarkPiece || r.reserve != r.members*xMarkValueCap {
			t.Fatalf("a read of the marks names %d ids and reserves %d bytes; want at most %d ids, %d bytes each", r.members, r.reserve, xMarkPiece, xMarkValueCap)
		}
	}
	if marks != 1500 || pieces != (1500+xMarkPiece-1)/xMarkPiece {
		t.Fatalf("the reads of the marks named %d ids in %d reads, want the 1,500 of the step in %d", marks, pieces, (1500+xMarkPiece-1)/xMarkPiece)
	}

	h.applies("moving them to ready", xVerb("rank", tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", To: "s1:ready", IDs: cards,
		Set: map[string]string{"attempt": "0"}, About: cards}))
	xSameSize(t, "1,500 moves", h.last, "ZREM", xp+"elig:s1@0", 1500)
	xSameSize(t, "1,500 moves", h.last, "ZADD", xp+"fresh:s1@0", 1500)
	if len(h.last) != 4 {
		t.Fatalf("1,500 moves wrote %d commands, want four: %v", len(h.last), h.last)
	}

	h.applies("1,200 quarantined ids", func() *Request {
		ids := map[string]string{}
		for _, id := range xNames("q", 1200) {
			ids[id] = "s1"
		}
		r := xQuarantineStep(1, ids)
		r.Body.Notes = nil // the note's subjects are J's and are bounded by its own test
		return r
	}())
	for _, key := range []string{"elig:s1@0", "fresh:s1@0", "again:s1@0", "askwait@0"} {
		xSameSize(t, "1,200 quarantined ids", h.last, "ZREM", xp+key, 1200)
	}

	agenda := xNames("ask@", 1200)
	seed := func(keys []string) Cmd {
		args := make([]string, 0, 2*len(keys))
		for i, k := range keys {
			args = append(args, strconv.Itoa(i+1), k)
		}
		return Command("ZADD", xp+"agenda@0", kindZSet, args...)
	}
	h.write(seed(agenda[:1000]), seed(agenda[1000:]))
	done := xTick(1)
	done.Body.Done = agenda
	h.applies("1,200 done keys", done)
	xSameSize(t, "1,200 done keys", h.last, "ZREM", xp+"agenda@0", 1200)

	requeue := make([]string, 1200)
	for i := range requeue {
		requeue[i] = fmt.Sprintf("resolve@%d+1", i+1)
	}
	req := xTick(1)
	req.Body.Requeue = requeue
	h.applies("1,200 requeued keys", req)
	xSameSize(t, "1,200 requeued keys", h.last, "ZADD", xp+"agenda@0", 1200)
	var scored []int
	for _, r := range h.mirror.reads {
		if r.cmd == "ZMSCORE" {
			scored = append(scored, r.members)
		}
	}
	if len(scored) != 2 || scored[0] != xPiece || scored[1] != 200 {
		t.Fatalf("1,200 requeued keys were read in pieces of %v, want [1000 200]", scored)
	}
	if got := h.zset("agenda@0"); len(got) != 1200 || got["resolve@1200+1"] != 1200 {
		t.Fatalf("the agenda holds %d keys, resolve@1200+1 at %v", len(got), got["resolve@1200+1"])
	}
}

// TestXQuarantinedChangesAreFoldedNotPerCard: what X writes for the quarantined
// cards of a step grows with the keys and the pieces, and not with the cards (the
// work is counted): 1,500 quarantined sentinels re-scored are two ZADDs to sent, and
// removed are two ZREMs, where a command a card would be 1,500.
func TestXQuarantinedChangesAreFoldedNotPerCard(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"))
	ids := xNames("g", 1500)
	h.applies("1,500 sentinels", xVerb("add", xBulkCreate("s1:waiting", ids, map[string]string{"kind": "sentinel"})))
	xSameSize(t, "1,500 sentinels", h.last, "ZADD", xp+"sent:s1@0", 1500)

	quarantine := map[string]string{}
	for _, id := range ids {
		quarantine[id] = "s1"
	}
	q := xQuarantineStep(1, quarantine)
	q.Body.Notes = nil
	h.applies("the quarantine of all 1,500", q)

	scores := make([]string, len(ids))
	for i := range scores {
		scores[i] = "3." + strconv.Itoa(i%9+1)
	}
	h.applies("1,500 quarantined sentinels re-scored", xVerb("rank", tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", IDs: ids, Scores: scores, About: ids}))
	xSameSize(t, "re-scored", h.last, "ZADD", xp+"sent:s1@0", 1500)
	if len(h.last) != 2 {
		t.Fatalf("1,500 quarantined sentinels re-scored wrote %d commands, want the two pieces of sent, not one a card", len(h.last))
	}

	h.applies("1,500 quarantined sentinels removed", xVerb("drop", tset.Entry{Kind: "remove", Table: sprint.Work, From: "s1:waiting", IDs: ids, About: ids}))
	xSameSize(t, "removed", h.last, "ZREM", xp+"sent:s1@0", 1500)
	if len(h.last) != 2 {
		t.Fatalf("1,500 quarantined sentinels removed wrote %d commands, want two", len(h.last))
	}
	if got := h.zset("sent:s1@0"); len(got) != 1 || got["g1"] == 0 {
		t.Fatalf("sent:s1 holds %v; want the fixture's g1 alone", got)
	}
}

// TestXQuarantinedRemovalLeavesEveryIndex: a quarantined card that a verb removes
// loses its derived due entries and its wait:<n> memberships in the same step, and a
// quarantined sentinel leaves sent, so no entry names a card that is not in its
// state (D1; I1: wait:<n> holds exactly the waiting cards naming n). The mark is
// cleared in the step. A quarantined card that changes without being removed leaves
// the indexes its change ends it in and is given no membership but sent; a
// quarantined card that leaves waiting leaves the wait:<n> it named. The same step
// run again is refused by Layer 1 and writes nothing.
func TestXQuarantinedRemovalLeavesEveryIndex(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.do(xVerb("seed",
		xCreate(sprint.Fleet, "m1:ready", "p3.w1", "3", map[string]string{"stream": "s1", "due_untaken": "9000"}),
		xCreate(sprint.Fleet, "m1:ready", "p4.w1", "4", map[string]string{"stream": "s1", "due_untaken": "9500"}),
		xCreate(sprint.Work, "s1:waiting", "p5", "5", map[string]string{"kind": "primary", "open": "1", "needs": "n9"}),
		xCreate(sprint.Work, "s1:waiting", "p6", "6", map[string]string{"kind": "primary", "open": "1", "needs": "n9,n8"})))
	h.write(xLease("1"), Command("ZADD", xp+"wait:n9@0", kindZSet, "0", "p5", "0", "p6"), Command("ZADD", xp+"wait:n8@0", kindZSet, "0", "p6"))
	h.applies("the quarantine", xQuarantineStep(1, map[string]string{"p3.w1": "s1", "p4.w1": "s1", "p5": "s1", "p6": "s1", "g1": "s1"}))
	if _, ok := h.zset("due@0")["untaken:p3.w1"]; !ok {
		t.Fatalf("the quarantine took p3.w1's due entry out: %v", h.zset("due@0"))
	}

	// a quarantined card that changes but stays: p4.w1 is taken (its old entry ends,
	// its new one is not derived) and p6 leaves waiting (its waits end)
	h.applies("quarantined cards that change", xVerb("take",
		xMove(sprint.Fleet, "m1:ready", "m1:working", "p4.w1", map[string]string{"due_unfinished": "17000"}),
		xMove(sprint.Work, "s1:waiting", "s1:ready", "p6", nil)))
	if got := h.zset("due@0"); len(got) != 1 || got["untaken:p3.w1"] != 9000 {
		t.Fatalf("after p4.w1 was taken the due set is %v; want its untaken entry gone, no unfinished one derived, and p3.w1's kept", got)
	}
	if got := h.zset("wait:n9@0"); len(got) != 1 || got["p5"] != 0 {
		t.Fatalf("wait:n9 is %v after p6 left waiting; want only p5", got)
	}
	if got := h.zset("wait:n8@0"); len(got) != 0 {
		t.Fatalf("wait:n8 is %v after p6 left waiting; want it empty", got)
	}
	for _, key := range []string{"elig:s1@0", "fresh:s1@0", "again:s1@0"} {
		if _, ok := h.zset(key)["p6"]; ok {
			t.Fatalf("the quarantined p6 is in %s", key)
		}
	}

	// the verb that removes the rest, and clears their marks in the same step
	rm := func() *Request {
		r := xVerb("drop", xRemove(sprint.Fleet, "m1:ready", "p3.w1"), xRemove(sprint.Work, "s1:waiting", "p5"), xRemove(sprint.Work, "s1:waiting", "g1"))
		r.Sprint = &SprintPart{}
		return r
	}
	h.extra = []Cmd{Command("HDEL", xp+"quarantine@0", kindHash, "p3.w1", "p5", "g1")}
	h.applies("the verb that removes quarantined cards", rm())
	if got := h.zset("due@0"); len(got) != 0 {
		t.Fatalf("due after the removal is %v; want no entry naming a card that is gone", got)
	}
	if got := h.zset("wait:n9@0"); len(got) != 0 {
		t.Fatalf("wait:n9 after the removal is %v; want it empty", got)
	}
	if got := h.zset("sent:s1@0"); len(got) != 0 {
		t.Fatalf("sent:s1 after the removal is %v; want the sentinel gone", got)
	}
	if got := h.keys()[xp+"quarantine@0"].Hash; len(got) != 2 || got["p4.w1"] == "" || got["p6"] == "" {
		t.Fatalf("the marks after the step are %v; want p4.w1 and p6 left", got)
	}

	// the step again: Layer 1 refuses the moves of cards that are gone, and nothing is written
	before := h.img()
	if ref := h.refused(rm()); ref.Code != "PLACE" {
		t.Fatalf("the same removal again is refused %s (%s); want PLACE", ref.Code, ref.Message)
	}
	if h.img() != before {
		t.Fatal("the same removal run again changed the twin")
	}
}

// xTwice sends a request built by build twice on the same state and holds the
// second run to writing nothing: the twin's whole image is what the first left, and
// what X wrote the second time adds nothing and removes nothing that was there.
func (h *xh) xTwice(what string, build func() *Request) {
	h.t.Helper()
	h.applies(what+", the first run", build())
	first, keys := h.img(), h.keys()
	h.last = nil
	h.applies(what+", the second run", build())
	if h.img() != first {
		h.t.Fatalf("%s: the second run changed the twin", what)
	}
	for _, c := range h.last {
		switch c.Argv[0] {
		case "ZREM":
			for _, m := range c.Argv[2:] {
				if _, there := keys[c.Argv[1]].ZSet[m]; there {
					h.t.Fatalf("%s: the second run removes %s from %s, which the first left there", what, m, c.Argv[1])
				}
			}
		default:
			h.t.Fatalf("%s: the second run wrote %v", what, c.Argv)
		}
	}
}

// TestXSecondRunWritesNothing: a step run twice on the same state writes nothing the
// second time (E7, idempotence): the agenda's edits, a quarantine, and a change that
// changes nothing. A requeued key that is queued is not written again; a finished key
// that is gone and an id already out of an index are removals of what is not there.
func TestXSecondRunWritesNothing(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"), Command("ZADD", xp+"agenda@0", kindZSet, "10", "ask@4", "11", "deal"))

	h.xTwice("the agenda's edits", func() *Request {
		r := xTick(1)
		r.Body.Done = []string{"ask@4"}
		r.Body.Requeue = []string{"ask@5+1", "deal"}
		return r
	})
	if got := h.zset("agenda@0"); len(got) != 2 || got["ask@5+1"] != 5 || got["deal"] != 11 {
		t.Fatalf("the agenda is %v", got)
	}
	h.xTwice("a quarantine", func() *Request {
		r := xTick(1)
		r.Body.Quarantine = []Quarantined{{ID: "p1", Stream: "s1", Code: "DRIFT", Rule: "resolve", Cells: []string{"s1:waiting"}}}
		r.Sprint = &SprintPart{Quarantine: append([]Quarantined(nil), r.Body.Quarantine...)}
		return r
	})
	h.xTwice("a change that changes nothing", func() *Request {
		return xVerb("rank", tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", IDs: []string{"p2"}, Set: map[string]string{"open": "0"}, About: []string{"p2"}})
	})
	if len(h.last) != 0 {
		t.Fatalf("a change that changes nothing wrote %v", h.last)
	}
}

// TestXCounterIsCompareAndSet: the counter is a compare-and-set on every field it
// writes (U1, U2): a change that sets a field it did not read is REQUEST, as the
// read cannot guard it; one planned on a value that has moved is COUNTER; one that
// would set the score below what the counter holds is COUNTER, since an add after it
// would land on a score already placed; a score that is not a whole number is
// REQUEST; the same change run again is refused COUNTER and writes nothing.
func TestXCounterIsCompareAndSet(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture() // the counter is 1000, streams 1; cards sit at 1 to 4
	create := func(id, score string) tset.Entry { return xCreate(sprint.Work, "s1:waiting", id, score, nil) }
	counter := func() map[string]string { return h.keys()[xp+"next@0"].Hash }

	// the probe of the cold read: a Set of score with no Read lowered it to 3
	h.wantRefusal(xCounterStep(map[string]string{}, map[string]string{"score": "3"}, create("a1", "2.5")), CodeRequest)
	h.wantRefusal(xCounterStep(nil, map[string]string{"score": "3"}, create("a1", "2.5")), CodeRequest)
	h.wantRefusal(xCounterStep(map[string]string{"score": "1000"}, map[string]string{"score": "1005", "streams": "2"}, create("a1", "1000")), CodeRequest)
	if counter()["score"] != "1000" {
		t.Fatalf("the counter is %v after refused steps", counter())
	}
	// read and set: lowering it is refused, however current the read
	ref := h.wantRefusal(xCounterStep(map[string]string{"score": "1000"}, map[string]string{"score": "3"}, create("a1", "2.5")), CodeCounter)
	if !strings.Contains(ref.Message, "only rises") {
		t.Fatalf("message %q does not say the counter only rises", ref.Message)
	}
	h.wantRefusal(xCounterStep(map[string]string{"score": "1000"}, map[string]string{"score": "999"}, create("a1", "2.5")), CodeCounter)
	// a score that is not a whole number of at most 15 digits
	for _, bad := range []string{"", "1000.5", "abc", "1e3", "NaN", "inf", " 1005", "1005 ", "+1005", "-5", "0x40", "1_0", "1234567890123456"} {
		h.wantRefusal(xCounterStep(map[string]string{"score": "1000"}, map[string]string{"score": bad}, create("a1", "2.5")), CodeRequest)
	}
	// the counter as it is: set to itself changes nothing and applies; raised applies once
	h.applies("a counter set to the value it holds", xCounterStep(map[string]string{"score": "1000"}, map[string]string{"score": "1000"}, create("a1", "2.5")))
	raise := func() *Request {
		return xCounterStep(map[string]string{"score": "1000", "streams": "1"}, map[string]string{"score": "1010", "streams": "2"}, create("a2", "1000"))
	}
	h.applies("a counter raised", raise())
	if got := counter(); got["score"] != "1010" || got["streams"] != "2" {
		t.Fatalf("the counter is %v after a raise", got)
	}
	// the same change again: the read has moved
	before := h.img()
	h.wantRefusal(raise(), CodeCounter)
	if h.img() != before {
		t.Fatal("the same counter change run again changed the twin")
	}
	// a counter that holds no number is the machine's own breakage: CONFIG
	h.write(Command("HSET", xp+"next@0", kindHash, "score", "ten"))
	h.wantRefusal(xCounterStep(map[string]string{"score": "ten"}, map[string]string{"score": "11"}, create("a3", "5")), CodeConfig)
	h.wantRefusal(xVerb("rank", tset.Entry{Kind: "move", Table: sprint.Work, From: "s1:waiting", IDs: []string{"p1"}, Scores: []string{"2.5"}, About: []string{"p1"}}), CodeConfig)
}

// TestXDoneAndRequeueNeverShareAKey: a key that one step both finishes and requeues
// is REQUEST, with the agenda as it was: the requeue finds the key queued and writes
// nothing, then the finish removes it, so the work the requeue owes would be lost. An
// agenda key that names nothing is REQUEST too, in both halves.
func TestXDoneAndRequeueNeverShareAKey(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"), Command("ZADD", xp+"agenda@0", kindZSet, "11", "deal"))
	both := xTick(1)
	both.Body.Done, both.Body.Requeue = []string{"deal"}, []string{"deal"}
	h.wantRefusal(both, CodeRequest)
	if got := h.zset("agenda@0"); len(got) != 1 || got["deal"] != 11 {
		t.Fatalf("the agenda is %v after the refused step", got)
	}
	offset := xTick(1)
	offset.Body.Done, offset.Body.Requeue = []string{"ask@5", "deal"}, []string{"ask@5+1", "ask@5"}
	h.wantRefusal(offset, CodeRequest)
	empty := xTick(1)
	empty.Body.Done = []string{""}
	h.wantRefusal(empty, CodeRequest)
	empty = xTick(1)
	empty.Body.Requeue = []string{"deal", ""}
	h.wantRefusal(empty, CodeRequest)
	disjoint := xTick(1)
	disjoint.Body.Done, disjoint.Body.Requeue = []string{"deal"}, []string{"deal@3+1"}
	h.applies("a finished key and another that continues it", disjoint)
}

// TestXRequeuedKeysNameALineAlike: what a requeued key must name to have an order
// is read alike by both halves: the digits after the first @, up to the end of the
// key or the first +, and no more than a line's seq. "ask@12x" is REQUEST in the
// twin and in the Lua, as are a key with no digits, a sign, a second @, a seq past
// 2^53 - 1 and digits that are not ASCII; what follows the + is not read.
func TestXRequeuedKeysNameALineAlike(t *testing.T) {
	t.Parallel()
	ok := map[string]float64{
		"ask@12+3": 12, "ask@0012": 12, "ask@7": 7, "@9+1": 9, "a+b@12": 12, "ask@12+x": 12, "ask@12+3@4": 12,
		"ask@9007199254740991": 9007199254740991, "ask@0000000000000000000000000000012": 12,
	}
	for key, order := range ok {
		h := newXHarness(t)
		h.fixture()
		h.write(xLease("1"))
		r := xTick(1)
		r.Body.Requeue = []string{key}
		h.applies(key, r)
		if got := h.zset("agenda@0")[key]; got != order {
			t.Fatalf("%q was queued at %v, want %v", key, got, order)
		}
	}
	for _, key := range []string{"ask@12x", "ask@", "ask@+1", "ask@-5", "ask@1 2", "ask@12@3", "ask@9007199254740992", "ask@99999999999999999999",
		"ask@" + strings.Repeat("9", 400), "ask", "ask:p9+10", "ask@١٢", "ask@1_0", "ask@12@3+4", "ask@0x10", "ask@1e3", "ask@1.5"} {
		h := newXHarness(t)
		h.fixture()
		h.write(xLease("1"))
		r := xTick(1)
		r.Body.Requeue = []string{key}
		h.wantRefusal(r, CodeRequest)
	}
}

// TestXScoreSpellingAgrees: a score is spelled for ZADD by both halves as the
// shortest decimal that reads back as the same number, with no exponent, so that the
// argv bytes the store is sent are the ones the coster counted: 2.1 is "2.1" and not
// "2.1000000000000001". The Lua's spelling is held to the twin's over whole numbers,
// the scores a rank between neighbours makes, random doubles of every magnitude the
// scores take, and the boundary cases.
func TestXScoreSpellingAgrees(t *testing.T) {
	t.Parallel()
	m := newXLuaMirror(t)
	spell := m.sp.RawGetString("x_score_text")
	if _, ok := spell.(*lua.LFunction); !ok {
		t.Fatalf("the Lua exposes no x_score_text: %v", spell)
	}
	check := func(f float64) {
		t.Helper()
		if err := m.L.CallByParam(lua.P{Fn: spell.(*lua.LFunction), NRet: 1, Protect: true}, lua.LNumber(f)); err != nil {
			t.Fatalf("x_score_text(%v): %v", f, err)
		}
		got := m.L.Get(-1).String()
		m.L.Pop(1)
		if want := xScore(f); got != want {
			t.Fatalf("x_score_text(%v) is %q, the twin spells it %q", f, got, want)
		}
		if back, err := strconv.ParseFloat(got, 64); err != nil || back != f {
			t.Fatalf("x_score_text(%v) is %q, which reads back as %v (%v)", f, got, back, err)
		}
	}
	for _, f := range []float64{0, 1, 4, 999999999999999, 1e15, 1e16, 123456789012345678, 1e17, 1e21, 1e22, 2.1, 0.1, 0.1 + 0.2, 1.0 / 3, 2.0 / 3, 1e-4, 1e-5, 1.5e-7,
		12.345, 4.5, 1000.000001, 9007199254740991, 9007199254740992, 1e15 + 0.5, 4503599627370495.5, 1e300, 123456.789e-10} {
		check(f)
		check(-f) // and minus zero, which is spelled "0"
	}
	for k := -60; k <= 60; k++ {
		check(math.Ldexp(1, k)) // the powers of two, whose rounding interval is not symmetric
		check(math.Ldexp(1, k) * 3)
	}
	rng := rand.New(rand.NewPCG(7, 11))
	for i := 0; i < 20000; i++ {
		switch i % 4 {
		case 0:
			check(rng.Float64() * math.Pow(10, float64(rng.IntN(18)-4)))
		case 1:
			check(float64(rng.IntN(1_000_000)) / 1000) // the scores of the walk: three decimals
		case 2:
			check(float64(rng.IntN(1_000_000)) + math.Ldexp(float64(1+rng.IntN(1<<10)), -rng.IntN(40))) // a midpoint of midpoints
		default: // any normal double: a score is never NaN, infinite or subnormal
			f := math.Float64frombits(rng.Uint64() &^ (1 << 63))
			if !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 2.2250738585072014e-308 {
				check(f)
			}
		}
	}
}

// TestXQuarantineMarksAreReservedAtTheFieldCap: X reads the quarantine's marks for
// membership alone, but a hash answers with each value, which only the sprint part
// bounds: a mark of 10 KiB (code, rule, stream, the refusal's cells, a note) does
// not refuse a step that names its card, as a read that reserved 300 bytes an id
// would (the store refuses a reply larger than its reservation, DRIFT, naming no
// card). Each id is reserved at Layer 1's field cap for the read as a whole, so a
// value past the cap is refused only when nothing else of the read absorbs it, and the
// twin and the Lua agree on where that is.
func TestXQuarantineMarksAreReservedAtTheFieldCap(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"),
		Command("HSET", xp+"quarantine@0", kindHash, "p1", strings.Repeat("c", 10*1024), "p2", strings.Repeat("d", xMarkValueCap+1)))

	h.applies("a card whose mark is 10 KiB", xVerb("rank", xMoveWaitingReady("p1")))
	var saw []string
	for _, r := range h.mirror.reads {
		if r.key == xp+"quarantine@0" {
			saw = append(saw, fmt.Sprintf("%s of %d, reserving %d, returning %d", r.cmd, r.members, r.reserve, r.bytes))
		}
	}
	if want := fmt.Sprintf("HLEN of 0, reserving %d, returning 1|HMGET of 1, reserving %d, returning %d", RESERVE_ONE, xMarkValueCap, 10*1024); strings.Join(saw, "|") != want {
		t.Fatalf("the reads of the marks were %q; want %q", strings.Join(saw, "|"), want)
	}
	// with no card marked the read is the one HLEN, however many ids the step names
	none := newXHarness(t)
	none.fixture()
	none.applies("a step with nothing marked", xVerb("add", xBulkCreate("s1:waiting", xNames("b", 300), map[string]string{"kind": "primary", "open": "0"})))
	var reads []string
	for _, r := range none.mirror.reads {
		if r.key == xp+"quarantine@0" {
			reads = append(reads, r.cmd)
		}
	}
	if strings.Join(reads, " ") != "HLEN" {
		t.Fatalf("an empty quarantine was read by %v; want one HLEN", reads)
	}
	ref := h.wantRefusal(xVerb("rank", xMoveWaitingReady("p2")), "DRIFT")
	if len(ref.Detail.IDs) != 0 {
		t.Fatalf("the refusal names %v; a read reservation names no card", ref.Detail.IDs)
	}
	h.applies("two marked cards whose values fit the reservations of both",
		xVerb("take", xMove(sprint.Work, "s1:ready", "s1:working", "p1", nil), xMoveWaitingReady("p2")))
}

// RESERVE_ONE is what the Lua reserves for a read of one short value (sprint_x.lua).
const RESERVE_ONE = 1024

// TestXWholeFieldsAreReadAlike: a whole-number field the derivation reads is read
// alike by both halves: up to 18 characters, a sign allowed, nothing else; one of 19
// digits, which IT02's derivation would take as an int64, is refused as the Lua
// refuses it. One an entry sets is REQUEST; one already on the card is DRIFT.
func TestXWholeFieldsAreReadAlike(t *testing.T) {
	t.Parallel()
	good := []string{"0", "+5", "-5", "007", "123456789012345678", "-12345678901234567"}
	bad := []string{"1234567890123456789", "-1234567890123456789", "+123456789012345678", " 5", "5 ", "5x", "0x5", "1_0", "+", "-", "1.0", "1e3"}
	for _, v := range good {
		h := newXHarness(t)
		h.fixture()
		h.applies("open "+v, xVerb("add", xCreate(sprint.Work, "s1:waiting", "w1", "7", map[string]string{"kind": "primary", "open": v})))
		h.applies("attempt "+v, xVerb("add", xCreate(sprint.Work, "s1:ready", "w2", "8", map[string]string{"kind": "primary", "attempt": v})))
		h.applies("due "+v, xVerb("add", xCreate(sprint.Fleet, "m1:ready", "w3", "9", map[string]string{"stream": "s1", "due_untaken": v})))
	}
	for _, v := range bad {
		h := newXHarness(t)
		h.fixture()
		for _, req := range []*Request{
			xVerb("add", xCreate(sprint.Work, "s1:waiting", "w1", "7", map[string]string{"kind": "primary", "open": v})),
			xVerb("add", xCreate(sprint.Work, "s1:ready", "w2", "8", map[string]string{"kind": "primary", "attempt": v})),
			xVerb("add", xCreate(sprint.Fleet, "m1:ready", "w3", "9", map[string]string{"stream": "s1", "due_untaken": v})),
		} {
			ref := h.wantRefusal(req, CodeRequest)
			if len(ref.Detail.IDs) != 1 {
				t.Fatalf("%q: the refusal names %v, want the card", v, ref.Detail.IDs)
			}
		}
	}
	// a field the machine never wrote, already on a card: DRIFT naming the card
	h := newXHarness(t)
	h.fixture()
	for id, fields := range map[string]map[string]string{
		"d1": {"kind": "primary", "open": "1234567890123456789"},
		"d2": {"kind": "primary", "open": "0", "due_untaken": "1234567890123456789"},
	} {
		if err := h.mem.SeedMember(testPrefix, sprint.Work, "0", id, tset.MemRecord{Epoch: "0", Revision: "1", Row: "s1", Column: "waiting", Score: "9", Fields: fields}); err != nil {
			t.Fatal(err)
		}
	}
	// d2's due field is read only where a due kind reads it: the fleet table's ready column
	if err := h.mem.SeedMember(testPrefix, sprint.Fleet, "0", "d3", tset.MemRecord{Epoch: "0", Revision: "1", Row: "m1", Column: "ready", Score: "9",
		Fields: map[string]string{"stream": "s1", "due_untaken": "1234567890123456789"}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ table, from, id string }{{sprint.Work, "s1:waiting", "d1"}, {sprint.Fleet, "m1:ready", "d3"}} {
		ref := h.refused(xVerb("rank", xMove(c.table, c.from, strings.Replace(c.from, "ready", "working", 1), c.id, nil)))
		if ref.Code != "DRIFT" || len(ref.Detail.IDs) != 1 || ref.Detail.IDs[0] != c.id {
			t.Fatalf("a card of %s with a field of 19 digits: %s %+v", c.table, ref.Code, ref.Detail)
		}
	}
}

// TestXGuardsCompareScoresExactly: a due guard and a beat guard compare the entry's
// score as the number it is, in the twin as in the Lua: an entry at 5000.5 is not
// the entry a guard read at 5000, and a beat a half millisecond above R is above it
// (a score the machine never writes, which the two halves no longer disagree on).
func TestXGuardsCompareScoresExactly(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.write(xLease("1"), xRunning(0),
		Command("ZADD", xp+"due@0", kindZSet, "5000.5", "remind:alice", xMS(xNow)+".5", "beat:m1"))
	due := xTick(1)
	due.Body.Guards = []XGuard{{Kind: XGuardDue, Key: "remind:alice", Score: 5000}}
	h.wantRefusal(due, CodeXGuard)
	beat := xTick(1)
	beat.Body.Guards = []XGuard{{Kind: XGuardBeatStale, Member: "m1"}}
	h.wantRefusal(beat, CodeXGuard)
	absent := xTick(1)
	absent.Body.Guards = []XGuard{{Kind: XGuardDue, Key: "remind:alice", Score: XGuardAbsent}}
	h.wantRefusal(absent, CodeXGuard)
	h.write(Command("ZADD", xp+"due@0", kindZSet, "5000", "remind:alice", xMS(xNow), "beat:m1"))
	ok := xTick(1)
	ok.Body.Guards = []XGuard{{Kind: XGuardDue, Key: "remind:alice", Score: 5000}, {Kind: XGuardBeatStale, Member: "m1"}}
	h.applies("entries as read, and a beat at R", ok)
}

// TestXDroppingRefusesAnAckOnAFrozenCard: an ack that changes a card of a stream
// being dropped, whether it waives a need (an intent, which becomes an entry) or
// clears a refusal (an entry), is refused DROPPING by X, naming the stream and the
// card, with nothing written (errata 3, H8); an ack of a judgment that changes no card
// (notes only) applies, and so does an ack that changes a card of another stream.
func TestXDroppingRefusesAnAckOnAFrozenCard(t *testing.T) {
	t.Parallel()
	h := newXHarness(t)
	h.fixture()
	h.do(xVerb("seed", xCreate(sprint.Work, "s2:waiting", "q1", "9", map[string]string{"kind": "primary", "open": "0", "refused": "resolve: x"})))
	h.write(xLease("1"))
	h.applies("init --coordinator", &Request{Epoch: "0", Meta: Meta{Verb: "init"}, Sprint: &SprintPart{Coordinator: "c1"}})
	h.applies("the mark", &Request{Epoch: "0", Meta: Meta{Verb: "drop"}, Sprint: &SprintPart{Dropping: map[string]string{"s1": "op7"}}})

	ack := func(mut func(*Request)) *Request {
		r := &Request{Epoch: "0", Meta: Meta{Verb: "ack", Actor: "c1"}}
		mut(r)
		return r
	}
	waive := ack(func(r *Request) { r.Body.Intents = []Intent{{Kind: "waive", Card: "p1", Needs: []string{"n1"}}} })
	clear := ack(func(r *Request) {
		r.Body.Entries = []tset.Entry{{Kind: "move", Table: sprint.Work, From: "s1:waiting", IDs: []string{"p1"}, Unset: []string{"refused"}, About: []string{"p1"}}}
	})
	for name, req := range map[string]*Request{"an ack that waives a need of a frozen card": waive, "an ack that clears a refusal on a frozen card": clear} {
		ref := h.wantRefusal(req, CodeDropping)
		if len(ref.Detail.Rows) != 1 || ref.Detail.Rows[0] != "s1" || len(ref.Detail.IDs) != 1 || ref.Detail.IDs[0] != "p1" {
			t.Fatalf("%s: detail %+v does not name the stream and the card", name, ref.Detail)
		}
	}
	h.applies("an ack of a judgment that changes no card", ack(func(r *Request) {
		r.Body.Notes = []NoteReq{{Op: "close", Type: "stalled", Cause: "slow", Subjects: []string{"p1"}}}
	}))
	h.applies("an ack that clears a refusal on a card of another stream", ack(func(r *Request) {
		r.Body.Entries = []tset.Entry{{Kind: "move", Table: sprint.Work, From: "s2:waiting", IDs: []string{"q1"}, Unset: []string{"refused"}, About: []string{"q1"}}}
	}))
	again := ack(func(r *Request) {
		r.Meta.Actor = "anyone"
		r.Body.Notes = []NoteReq{{Op: "close", Type: "stalled", Cause: "slow", Subjects: []string{"p1"}}}
	})
	h.wantRefusal(again, CodeNotCoord)
}
