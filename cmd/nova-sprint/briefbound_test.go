package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
// real fail paths, which the source scan cannot see where the status is a variable).
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
