package main

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// openJudgment writes the judgment n open in the store as a step of the machine writes one:
// the class test below holds each kind's printed commands to the verbs on a store where the
// judgment is really open.
func (ta *testApp) openJudgment(n sprint.Note) {
	ta.t.Helper()
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	_, err := st.Run(context.Background(), store.Step{Verb: "judgment", Load: store.All, Plan: func(s *sprint.Snapshot) sprint.Plan {
		n.At = s.Now
		return sprint.Plan{Units: []sprint.Unit{{Key: n.Type, Stream: n.Stream, Notes: []sprint.Note{n}}}}
	}})
	require.NoError(ta.t, err)
}

// filled is a printed command with its placeholders given, as a coordinator fills them in:
// the corrected brief a file that passes the card lint, the rest a word the verb takes.
func filled(t *testing.T, line string) string {
	t.Helper()
	line = briefPlaceholder.ReplaceAllLiteralString(line, "--brief-file "+writeBrief(t, "the brief the coordinator writes"))
	for _, r := range []struct{ from, to string }{
		{"'<the corrected brief>'", writeBrief(t, "the corrected brief")},
		{"'<brief>'", "'a brief'"},
		{"'<a higher tier>'", "heavy"},
		{"'<member>'", "m1"},
		{"'<reader>'", "reader-c"},
		{"'<new id>'", "more-1"},
		{"'<fix id>'", "fix-1"},
		{"'<stream>'", "s9"},
		{"'<n>'", "2"},
		{"'<merge sha>'", "0123456789abcdef0123456789abcdef01234567"},
	} {
		line = strings.ReplaceAll(line, r.from, r.to)
	}
	for {
		i := strings.Index(line, "'<")
		j := strings.Index(line[max(i, 0):], ">'")
		if i < 0 || j < 0 {
			return line
		}
		line = line[:i] + "'filled in'" + line[i+j+2:]
	}
}

// briefPlaceholder is a brief the coordinator writes in place of the placeholder: a file
// that passes the card lint, as a coordinator gives one.
var briefPlaceholder = regexp.MustCompile(`--brief '<[^']*>'`)

// named is a judgment's decisions as the tick names them for the member, reader or stream
// the condition is of (overload.go, readers_behind.go, steps_tick.go's merge deadline): the
// table's words are the shape, and a raised judgment carries the names.
func named(ds []string) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = strings.NewReplacer("<m>", "m1", "<half>", "1", "<member>", "m1", "<r>", "reader-a", "<s>", "s1").Replace(d)
	}
	return out
}

// lies are the refusals that say a printed command is no command of the judgment: the verb
// refusing the judgment it was printed for, the shape it was printed in, or the card it names
// for its state alone, never for what the coordinator would fill in.
var lies = []string{
	"does not answer",                         // ack printed for a judgment ack does not answer
	"keeps its brief",                         // brief printed for a card in review
	"or say --one",                            // a single card's rework or drop printed inside a group
	"is not a condition the tick keeps",       // wait printed for a judgment wait does not hold
	"unknown flag",                            // a flag the verb does not take
	"wants one primary",                       // the verb's shape
	"the brief is wrong, not the worker; run", // rework printed for a card at its brief's bound
}

// A coordinator of any model follows the printed command, and when it is refused the machine
// has lied to it (the owner, 2026-10-05: the bound judgment printed brief and brief refused
// eleven cards, a card in review keeps its brief; the heavy-tier judgment printed ack and ack
// refused a condition the tick keeps; a rework printed for one card of a group was refused
// for want of --one). The class: every judgment kind (sprint.Decisions and
// sprint.TickDecisions), open on one card and on a group of two, every command the inbox
// prints for it run as printed, its placeholders filled in, against the store the judgment
// is open in; no command is refused for a reason that is the printed command's own, and no
// condition the tick keeps is offered ack. Then the bound itself, end to end.
func TestEveryPrintedDecisionCommandRuns(t *testing.T) {
	t.Parallel()
	kinds := map[string][]string{}
	for typ, ds := range sprint.Decisions {
		kinds[typ] = ds
	}
	for typ, ds := range sprint.TickDecisions {
		kinds[typ] = ds
	}
	types := make([]string, 0, len(kinds))
	for typ := range kinds {
		types = append(types, typ)
	}
	sort.Strings(types)
	stopped := []string{sprint.NConflict, sprint.NRed, sprint.NCross, sprint.NRejected, sprint.NBaseRed}
	built := judgmentApp(t)
	base := built.snapshot()
	for _, typ := range types {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.now = built.now // the base's clock, as its records were written at
			ran := 0
			for _, members := range [][]string{{"s1-1"}, {"s1-1", "s1-2"}} {
				n := sprint.Note{Kind: sprint.Judgment, Type: typ, Stream: "s1", Primaries: members, Count: len(members), Card: "s1-1", Other: "s2-1",
					Decisions: named(kinds[typ]), StreamLevel: slices.Contains(stopped, typ)}
				if typ == sprint.NRaiseReadTier {
					n.Tier, n.What = "pro", "raise the read tier of s1 to pro? two readers disagreed on one attempt"
				}
				if n.StreamLevel {
					n.Primaries, n.Count = []string{"s1-1"}, 1
				}
				ta.restore(base)
				ta.openJudgment(n)
				open := ta.snapshot()
				g := ta.group(typ, "s1")
				if sprint.TickKept(typ) {
					for _, c := range g.Commands {
						assert.NotEqual(t, "ack", c.Decision, "%s (%d): a condition the tick keeps is not offered ack: %+v", typ, len(members), g.Commands)
					}
				}
				// each decision from the store the judgment is open in, its lines run in order
				for _, c := range g.Commands {
					ta.restore(open)
					for _, line := range c.Lines {
						if !strings.HasPrefix(line, "nova-sprint ") {
							continue // another tool's: nova-config, nova-friend
						}
						code, out, errs := ta.do(filled(t, strings.TrimPrefix(line, "nova-sprint ")))
						ran++
						if code == 0 {
							continue
						}
						// exit 2 is a command that could not run: its shape is the printed line's own
						assert.NotEqual(t, 2, code, "%s (%d), decision %q: the printed command %q could not run: %s",
							typ, len(members), c.Decision, line, strings.TrimSpace(out+errs))
						for _, lie := range lies {
							assert.NotContains(t, out+errs, lie, "%s (%d), decision %q: the printed command %q is refused (exit %d): %s",
								typ, len(members), c.Decision, line, code, strings.TrimSpace(out+errs))
						}
					}
				}
				if len(members) == 1 || n.StreamLevel {
					continue
				}
				// one card of the group, as card prints its judgment's commands: its rework and
				// drop run with the group open
				ta.restore(open)
				var lines []string
				for _, l := range strings.Split(ta.ok("card s1-1"), "\n") {
					if i := strings.Index(l, ": nova-sprint "); i >= 0 && (strings.Contains(l, ": nova-sprint rework ") || strings.Contains(l, ": nova-sprint drop ")) {
						lines = append(lines, l[i+2:])
					}
				}
				grouped := false
				for _, c := range g.Commands {
					for _, l := range c.Lines {
						grouped = grouped || strings.HasPrefix(l, "nova-sprint rework --group ") || strings.HasPrefix(l, "nova-sprint drop --group ")
					}
				}
				if grouped {
					assert.NotEmpty(t, lines, "%s: card prints the group's rework and drop for its one card", typ)
				}
				for _, line := range lines {
					ta.restore(open)
					code, out, errs := ta.do(filled(t, strings.TrimPrefix(line, "nova-sprint ")))
					ran++
					assert.NotContains(t, out+errs, "or say --one", "%s, one card of the group as card prints it: %q (exit %d)", typ, line, code)
				}
			}
			assert.Positive(t, ran, "the kind's printed commands ran")
		})
	}
}

// snapshot is the store's whole state, and restore puts it back: each decision of the class
// runs from the same store.
func (ta *testApp) snapshot() []byte {
	ta.t.Helper()
	doc, err := ta.m.Snapshot()
	require.NoError(ta.t, err)
	return doc
}

func (ta *testApp) restore(doc []byte) {
	ta.t.Helper()
	require.NoError(ta.t, ta.m.Restore(doc))
}

// judgmentApp is a sprint with two cards of s1 in review, asked of their readers and with no
// judgment open, and one card of s2 waiting on a need: the subjects a judgment of any kind
// names, s1-1 alone or s1-1 and s1-2 as a group.
func judgmentApp(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.inReview(2)
	ta.ok("add --stream s2 --count 1 --one --needs s1-1")
	return ta
}

// The bound, end to end: a card failed the same way twice is at its brief's bound, in
// review, and the judgment prints brief; brief run as printed replaces the card by its twin
// with the new brief in one step (the old card dropped "replaced by <twin>", every card that
// needed it needing the twin), and the judgment is answered, never left open.
func TestBriefAtTheBoundCutsTheTwin(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s2 --count 1 --one --needs s1-1")
	const finding = "line 3: the brief names a file that is not there"
	ta.deal(1)
	for n, head := range []string{"0123456789abcdef0123456789abcdef01234567", "89abcdef0123456789abcdef0123456789abcdef"} {
		work := fmt.Sprintf("s1-1.w%d", n+1)
		ta.ok("take --as m1 " + work + "@" + ta.workGen("m1", work))
		ta.ok("finish --as m1 " + work + "@" + ta.workGen("m1", work) + " --head " + head + " --report 'pushed'")
		ta.ok("ask")
		var read string
		for _, r := range []string{"reader-a", "reader-b"} {
			for _, id := range ta.askedOf(r) {
				read = "read --as " + r + " --broken " + id + " --finding '" + finding + "'"
			}
		}
		require.NotEmpty(t, read, "attempt %d is asked of a reader", n+1)
		ta.ok(read)
		if n == 0 {
			ta.ok("rework s1-1")
		}
	}
	g := ta.group(sprint.NBriefWrong, "s1")
	require.Equal(t, sprint.Review, ta.primary("s1-1").Col)
	var brief []string
	for _, c := range g.Commands {
		if c.Decision == "brief" {
			brief = c.Lines
		}
	}
	require.Len(t, brief, 1, "%+v", g.Commands)
	require.True(t, strings.HasPrefix(brief[0], "nova-sprint brief s1-1 --brief-file "), brief[0])
	out := ta.ok(filled(t, strings.TrimPrefix(brief[0], "nova-sprint ")))
	assert.Contains(t, out, "replaces s1-1", out)
	require.True(t, ta.placed("s1-1b"), "the twin is on the table")
	twin := ta.primary("s1-1b")
	assert.Equal(t, strings.TrimSuffix(passingBrief("the corrected brief"), "\n"), twin.F("brief"), "the brief file's one trailing newline cut")
	assert.Equal(t, "s1-1", twin.F(sprint.FieldReplaces))
	assert.Equal(t, "s1-1b", ta.primary("s2-1").F("needs"), "the dependent needs the twin")
	assert.False(t, ta.placed("s1-1"), "the old card is off the table")
	answered := false
	for _, g := range ta.inboxGroups() {
		answered = answered || g.Kind == sprint.Decided && g.Type == sprint.NBriefWrong && strings.Contains(g.What, "brief: replaced by its twin s1-1b")
		if g.Kind != sprint.Judgment {
			continue
		}
		assert.NotEqual(t, sprint.NBriefWrong, g.Type, "the judgment is answered: %+v", g)
		assert.NotEqual(t, sprint.NBlocked, g.Type, "no blocked judgment is raised: %+v", g)
	}
	assert.True(t, answered, "the judgment's answer is recorded: %+v", ta.inboxGroups())
	ta.clean()
}
