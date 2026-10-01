package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// briefOfSize writes a brief of exactly n bytes that passes the card lint (the rules
// paragraph first, then plain padding words) under t.TempDir() and returns its path. The
// file is the brief and one trailing newline, which add cuts.
func briefOfSize(t *testing.T, n int) string {
	t.Helper()
	b := passingBrief("handle the empty case") + "\n"
	require.LessOrEqual(t, len(b), n)
	for len(b) < n {
		b += strings.Repeat("padding words", 7) + "\n"
	}
	b = b[:n]
	path := filepath.Join(t.TempDir(), "brief.md")
	require.NoError(t, os.WriteFile(path, []byte(b+"\n"), 0o600))
	return path
}

// A brief is a child's whole brief, so the bound is 16 KiB, over the card lint's 12000
// bytes of advice: a brief of 12000 bytes is admitted whole, a brief of 16384 is the last
// that is, and one more byte is refused, exit 1, nothing written, naming the field, its
// size, the bound and the remedy. A longer file is read whole: the refusal names its real
// size, never the front of it. The other text fields keep their 8 KiB (store.TextBound).
func TestAddTakesABriefUpToTheBriefBound(t *testing.T) {
	t.Parallel()
	require.Equal(t, 16384, store.MaxBriefBytes)
	require.Equal(t, 8192, store.TextBound("fix"))
	require.LessOrEqual(t, store.MaxBriefBytes, ntable.LimitFieldValueBytes, "the table layer's cell bound holds a brief")
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	for i, n := range []int{12000, store.MaxBriefBytes} {
		ta.ok(fmt.Sprintf("add --stream s%d --count 1 --brief-file %s", i+1, briefOfSize(t, n)))
	}
	ta.deal(2)
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 2)
	sizes := map[int]bool{}
	for _, c := range q.Cards {
		require.NotNil(t, c.Packet)
		sizes[len(c.Packet.Brief)] = true
	}
	require.Equal(t, map[int]bool{12000: true, store.MaxBriefBytes: true}, sizes, "the packets carry the briefs whole")

	before := ta.applies()
	for _, n := range []int{store.MaxBriefBytes + 1, 20000} {
		code, out, errs := ta.do("add --stream s3 --count 1 --brief-file " + briefOfSize(t, n))
		require.Equal(t, 1, code, "out %q err %q", out, errs)
		for _, want := range []string{
			fmt.Sprintf("field brief is %d bytes, over the bound of 16384 bytes", n),
			"a brief is a child's whole brief, up to 16 KiB, and the card lint advises at most 12000 bytes",
			"shorten it, or point to a file or a comment",
			"ADD FAIL moved=0 refused=1 notes=0",
		} {
			require.Contains(t, errs, want)
		}
	}
	require.Equal(t, before, ta.applies(), "a refused add wrote")

	// --brief over the flag takes the same bound
	code, _, errs := ta.do("add --stream s3 --count 1 --brief '" + strings.ReplaceAll(passingBrief(strings.Repeat("y", store.MaxBriefBytes)), "'", "") + "'")
	require.Equal(t, 1, code)
	require.Contains(t, errs, "over the bound of 16384 bytes")
}

var failLine = regexp.MustCompile(`(?m)^([A-Z][A-Z-]*) FAIL\b`)

// A verb whose work was refused is a failure, and a verb that prints `<TOKEN> FAIL` exits
// non-zero: no FAIL line with exit 0 (the mirror of TestNoOKOnFailure, over the verbs'
// real fail paths, which the source scan cannot see where the status is a variable). The
// verbs with a printer of their own (check, repair, tick, accept --group) are driven in
// TestCheckRepairTickAndGroupFailsExitNonZero.
// Every row names a verb refused whole; its line says FAIL on stderr, with its own token,
// and the exit is 1. A refusal in --json says it in the exit alone.
func TestEveryVerbThatPrintsFailExitsNonZero(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	over := filepath.Join(t.TempDir(), "over.md")
	rules := filepath.Join(t.TempDir(), "rules.txt")
	require.NoError(t, os.WriteFile(rules, []byte("Be careful.\n"), 0o600))
	require.NoError(t, os.WriteFile(over, []byte("Be careful.\n"+strings.Repeat("x", store.MaxBriefBytes)), 0o600))
	for _, c := range []struct{ line, token string }{
		{"add --stream s1 --count 1 --rules " + rules + " --brief-file " + over, "ADD"},
		{"add --stream s1 --count 1 --json --rules " + rules + " --brief-file " + over, ""},
		{"add --stream s1 s1-1", "ADD"},
		{"add --stream s1 --count 1 --needs nope", "ADD"},
		{"release s1-nope --reason x", "RELEASE"},
		{"resolve s1-nope", "RESOLVE"},
		{"take --as m1 s1-nope.w1@1", "TAKE-BY-ID"},
		{"finish --as m1 s1-nope.w1@1 --report x", "FINISH"},
		{"ask s1-nope", "ASK"},
		{"read --as reader-a --ok s1-nope", "READ"},
		{"accept s1-nope", "ACCEPT"},
		{"rework s1-nope --fix x", "REWORK"},
		{"return s1-nope --reason x", "RETURN"},
		{"drop s1-nope --reason x", "DROP"},
		{"rank s1-nope --first", "RANK"},
		{"merge --stream s9 --batch 1", "MERGE"},
		{"resume --stream s1 --did x", "RESUME"},
		{"ci s1-nope --red", "CI"},
		{"ack note-nope --reason x", "ACK"},
		{"drop s1-nope --reason x --json", ""},
	} {
		code, out, errs := ta.do(c.line)
		got := failLine.FindAllStringSubmatch(out+errs, -1)
		if c.token == "" {
			require.Empty(t, got, "%s: --json prints no FAIL line", c.line)
		} else {
			require.Len(t, got, 1, "%s: out %q err %q", c.line, out, errs)
			require.Equal(t, c.token, got[0][1], c.line)
		}
		require.Equal(t, 1, code, "%s: a refused step exits 1 (out %q err %q)", c.line, out, errs)
	}
}

// A file longer than the read cap is refused naming the cap and its true size, never cut:
// the file is read one byte past the cap, and a file of exactly the cap is read whole.
func TestAFileOverTheReadCapIsRefusedWithItsTrueSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name string, n int) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(strings.Repeat("x", n)), 0o600))
		return p
	}
	text, err := readTextFile(write("at.txt", briefReadCap), briefReadCap)
	require.NoError(t, err)
	require.Len(t, text, briefReadCap, "a file of exactly the cap is read whole")
	_, err = readTextFile(write("over.txt", briefReadCap+350), briefReadCap)
	require.ErrorContains(t, err, fmt.Sprintf("the file is %d bytes, over the %d bytes a file of this kind may be", briefReadCap+350, briefReadCap))

	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.applies()
	code, _, errs := ta.do("add --stream s1 --count 1 --brief-file " + write("brief.txt", briefReadCap+350))
	require.Equal(t, 2, code, errs)
	require.Contains(t, errs, fmt.Sprintf("the file is %d bytes, over the %d bytes", briefReadCap+350, briefReadCap))
	require.Equal(t, before, ta.applies())
	code, _, errs = ta.do("goal set friend-a --file " + write("goal.txt", store.MaxCardTextBytes+1))
	require.Equal(t, 2, code)
	require.Contains(t, errs, fmt.Sprintf("the file is %d bytes, over the %d bytes", store.MaxCardTextBytes+1, store.MaxCardTextBytes))
}

// bare is one command line with no beat before it, for a test that has set a fault in the
// store the beat would meet.
func (ta *testApp) bare(line string) (int, string, string) {
	var out, errb bytes.Buffer
	code := ta.a.run(ta.withEpoch(split(line)), &out, &errb)
	return code, out.String(), errb.String()
}

// requireFailExit holds the one rule: a line `<token> FAIL` on the output is an exit that
// is not 0, and the line is the verb's own.
func requireFailExit(t *testing.T, line, token string, code int, out, errs string) {
	t.Helper()
	got := failLine.FindAllStringSubmatch(out+errs, -1)
	require.NotEmpty(t, got, "%s: no FAIL line: out %q err %q", line, out, errs)
	require.Equal(t, token, got[0][1], line)
	require.NotZero(t, code, "%s: printed %s FAIL and exited 0 (out %q err %q)", line, token, out, errs)
}

// The verbs that print their own FAIL line, not the step report's: check (a rule broken),
// repair (an operation it cannot finish), tick (the store failed under it) and an
// accept --group the group's size changed under. Each is driven to its FAIL here, and
// exits non-zero.
func TestCheckRepairTickAndGroupFailsExitNonZero(t *testing.T) {
	t.Parallel()

	t.Run("check", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		ta.ok("add --stream s1 --count 1")
		ta.deal(1)
		// an outside writer moves a fleet card: a move no log line records (rule 13)
		st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
		require.NoError(t, err)
		snap, err := st.Load(context.Background(), store.All, nil)
		require.NoError(t, err)
		wc := snap.Fleet.Card("s1-1.w1")
		require.NotNil(t, wc)
		_, err = ta.m.Apply(context.Background(), ntable.BatchManifest{Schema: 1, Table: st.Names.Table(sprint.Fleet), Epoch: "0",
			ExpectedTableRevision: fmt.Sprint(snap.Fleet.Revision), OperationID: "outside-move",
			Members: []ntable.BatchMemberEntry{{ID: wc.ID, Expect: &ntable.MemberExpect{Revision: fmt.Sprint(wc.Rev)},
				Move: &ntable.MemberMoveOp{Row: wc.Row, Col: sprint.Withdrawn}}}})
		require.NoError(t, err)
		code, out, errs := ta.do("check")
		requireFailExit(t, "check", "CHECK", code, out, errs)
		require.Contains(t, out+errs, "CHECK FAIL violations=")
	})

	t.Run("repair", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		ta.ok("add --stream s1 --count 1")
		ta.deal(1)
		ta.ok("take --as m1 s1-1.w1@1")
		// a finish cut after its first manifest applied leaves its operation open; the
		// store still failing, repair cannot finish it
		applied := 0
		ta.m.Fail = func(p string) error {
			if strings.HasPrefix(p, "apply ") && strings.HasSuffix(p, " before") {
				if applied++; applied > 1 {
					return errors.New("cut")
				}
			}
			return nil
		}
		code, out, errs := ta.bare("finish --as m1 s1-1.w1@1 --failed --report x")
		require.NotZero(t, code, "the cut finish: %s%s", out, errs)
		require.NotNil(t, ta.m.Pending(), "the cut left no operation open")
		ta.a.sleep(2 * time.Minute) // past the writer's grace
		ta.m.Fail = func(p string) error {
			if strings.HasPrefix(p, "apply ") {
				return errors.New("still cut")
			}
			return nil
		}
		code, out, errs = ta.bare("repair")
		requireFailExit(t, "repair", "REPAIR", code, out, errs)
	})

	t.Run("tick", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		ta.ok("add --stream s1 --count 1")
		ta.ok("start")
		ta.m.Fail = func(p string) error { return errors.New("the store went away") }
		code, out, errs := ta.bare("tick")
		requireFailExit(t, "tick", "TICK", code, out, errs)
	})

	t.Run("accept --group", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		ta.ok("add --stream s1 --count 3")
		ta.deal(3)
		ta.failOnce("m1", "s1-1.w1@1", "tests red")
		g := ta.group(sprint.NWorkFailed, "s1")
		ta.failOnce("m1", "s1-2.w1@1", "tests red")
		line := "accept --group " + g.ID + " --expect 1"
		code, out, errs := ta.do(line)
		requireFailExit(t, line, "ACCEPT", code, out, errs)
	})
}
