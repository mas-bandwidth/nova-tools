package main

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// Every command a judgment prints runs as printed (the owner, 2026-10-05: the bound's judgment
// printed `nova-sprint brief <id> --brief-file <path>` for eleven cards and brief refused every
// one, "a card in review keeps its brief"; a tick-kept judgment printed ack and ack refused it,
// "a condition the tick keeps"; "a coordinator of any model follows the printed command; when
// it is refused the machine has lied to it"). The class: every judgment kind with every
// decision it offers, the decisions the tick names per instance included, is opened on the
// twin store on a card in the state that kind is raised in; the inbox's own commands for it
// are taken from `inbox --json`, their placeholders filled as a coordinator fills them, and
// each line is run on its own fresh twin, in order. A decision that prints no command, a line
// the verb refuses, and a line naming a verb the binary lacks fail the test.

// judgmentKind is one judgment as the machine raises it: its type, its decisions as the
// raising code names them, and the state of its subject.
type judgmentKind struct {
	typ       string
	decisions []string
	stage     string // stageReview (the default), stageWaiting, stageStopped
	stream    string // the note's stream, "" for s1
	level     bool   // a stream-level (or member-, provider-, tier-level) condition
	card      string // the note's named card, "" for the subject's work card
	other     string // the note's other card (a cross stop)
	tier      string // the tier a raise-read-tier judgment proposes
	friend    bool   // about friend-a, on a twin whose friends table has her
	sentinel  bool   // about the sentinel s1-stop
}

const (
	stageReview  = "review"  // s1-1 in review at attempt 1, its work done
	stageWaiting = "waiting" // s1-2 waiting on s1-1, never dealt
	stageStopped = "stopped" // s1-1 in review, stream s1 stopped on it
	stageFailed  = "failed"  // s1-1 in review, its work came back failed with a report
	stageAsked   = "asked"   // s1-1 in review, one read asked
	stageBroken  = "broken"  // s1-1 in review, its one read broken with a finding
	stageReadOK  = "read-ok" // s1-1 in review, both its reads ok
	stageMerging = "merging" // s1-1 accepted, merging
)

// judgmentKinds is every judgment kind with every decision list it is raised with: the
// static lists (sprint.Decisions, sprint.TickDecisions) and the ones named per instance by
// the code that raises them (overload.go, readers_behind.go, promotion.go, provider_funds.go,
// remind.go, coordinator_pass.go, friend_stall.go, failure.go, cost_reconcile.go).
func judgmentKinds() []judgmentKind {
	var out []judgmentKind
	streamLevel := map[string]string{
		sprint.NOverloaded: sprint.MemberSubject("m1"), sprint.NReadersBehind: "", sprint.NDevBehind: "",
		sprint.NRaiseReadTier: "s1", sprint.NNoRoute: sprint.TierSubject("heavy"), sprint.NNoMember: "", sprint.NStarving: "",
		sprint.NFewReaders: "", sprint.NProviderFunds: sprint.ProviderSubject("p1"), sprint.NProviderLow: sprint.ProviderSubject("p1"),
		sprint.NProviderKey: sprint.ProviderSubject("p1"), sprint.NAllOutOfCredit: "", sprint.NMergeLate: "s1",
		sprint.NAlarmReview: "", sprint.NAlarmMerging: "", sprint.NAlarmReady: "", sprint.NAlarmFleet: "",
		sprint.NCoordinatorBehind: "", sprint.NRemindFailed: "", sprint.NCostGap: sprint.ProviderSubject("p1"),
	}
	stopped := map[string]bool{sprint.NConflict: true, sprint.NRed: true, sprint.NCross: true, sprint.NRejected: true, sprint.NBaseRed: true}
	waiting := map[string]bool{sprint.NBlocked: true, sprint.NMissingNeed: true}
	add := func(typ string, ds []string) {
		k := judgmentKind{typ: typ, decisions: ds, stage: stageReview}
		if st, ok := streamLevel[typ]; ok {
			k.stage, k.level, k.stream = stageReview, true, st
		}
		switch {
		case stopped[typ]:
			// the lander's stop: a stream-level judgment naming the cards it stopped on (steps_merge.go)
			k.stage, k.level, k.stream = stageStopped, true, "s1"
		case waiting[typ]:
			k.stage = stageWaiting
		case typ == sprint.NWorkFailed:
			k.stage = stageFailed
		case typ == sprint.NReadBroken || typ == sprint.NReadsExhausted:
			k.stage = stageBroken
		case typ == sprint.NReadLate:
			k.stage = stageAsked
		case typ == sprint.NReadyToAccept || typ == sprint.NReturned:
			k.stage = stageReadOK
		case typ == sprint.NMergeLate:
			k.stage = stageMerging
		}
		if typ == sprint.NCross {
			k.other = "s1-2"
		}
		if typ == sprint.NRaiseReadTier {
			k.tier = "pro"
		}
		out = append(out, k)
	}
	for _, typ := range sortedKeys(sprint.Decisions) {
		add(typ, sprint.Decisions[typ])
	}
	for _, typ := range sortedKeys(sprint.TickDecisions) {
		add(typ, sprint.TickDecisions[typ])
	}
	// named per instance
	add(sprint.NOverloaded, []string{"fleet up m1 --width 1", "wait 15m"})
	add(sprint.NReadersBehind, []string{"reader up reader-a", "restart reader-a", "wait 10m"})
	add(sprint.NDevBehind, []string{"promoted", "wait 30m"})
	add(sprint.NProviderFunds, []string{"funded p1", "ack", "wait"})
	add(sprint.NProviderLow, []string{"funded p1", "ack", "wait"})
	add(sprint.NRemindFailed, []string{"goal set friend-a --to <route>", "goal drop friend-a", "ack"})
	add(sprint.NStalled, []string{"friend take friend-a --all-unstarted", "friend down friend-a --reason 'stalled'"})
	out[len(out)-1].friend = true
	add(sprint.NFriendDeaf, []string{"nova-friend ping --as <coordinator> --to friend-a --wake", "debug: docs/SPEC-FRIEND.md, The harness check", "friend down friend-a --reason deaf", "ack", "wait"})
	out[len(out)-1].friend = true
	add(sprint.NFriendIdle, []string{"nova-friend ping --as <coordinator> --to friend-a --wake", "friend take friend-a --all-unstarted", "friend down friend-a --reason idle", "ack", "wait"})
	out[len(out)-1].friend = true
	for i := range out {
		if out[i].typ == sprint.NSentinelReached || out[i].typ == sprint.NStarving {
			out[i].sentinel = true
		}
		if out[i].typ == sprint.NFriendDeaf || out[i].typ == sprint.NFriendIdle {
			out[i].friend = true
		}
	}
	add(sprint.NBound, []string{"rework with a fix", "drop", "wait"})
	add(sprint.NBound, []string{sprint.ReworkOnAHigherTier, "drop", "wait"})
	add(sprint.NBound, sprint.Decisions[sprint.NBriefWrong]) // the bound at the brief's bound (steps_tick.go)
	add(sprint.NCostGap, sprint.CostGapDecisions)
	return out
}

// stageJudgment is a fresh twin with s1-1 and s1-2 (s1-2 needing s1-1) in the state the kind
// is raised in, and the kind's judgment open on its subject.
func stageJudgment(t *testing.T, k judgmentKind) *testApp {
	t.Helper()
	var ta *testApp
	if k.friend {
		ta, _ = friendApp(t, "friend-a")
		ta.ok("friend sync")
	} else {
		ta = newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
	}
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "the first work"))
	ta.ok("add --stream s1 s1-2 --one --needs s1-1 --brief-file " + writeBrief(t, "the second work"))
	subject := "s1-1"
	switch k.stage {
	case stageWaiting:
		subject = "s1-2"
	case stageReview:
		ta.deal(1)
		ta.ok("take --as m1 s1-1.w1@1")
		ta.ok("finish --as m1 s1-1.w1@1")
	case stageAsked, stageBroken, stageReadOK, stageMerging, stageStopped:
		ta.deal(1)
		ta.ok("take --as m1 s1-1.w1@1")
		ta.ok("finish --as m1 s1-1.w1@1")
		ta.ok("ask s1-1")
		if k.stage == stageBroken {
			if code, _, _ := ta.do("read --as reader-a --broken s1-1.r1.reader-a --finding 'f.go:1 the class'"); code != 0 {
				ta.ok("read --as reader-b --broken s1-1.r1.reader-b --finding 'f.go:1 the class'")
			}
		}
		if k.stage == stageReadOK || k.stage == stageMerging || k.stage == stageStopped {
			ta.ok("ask s1-1 --another")
			ta.ok("read --as reader-a --ok s1-1.r1.reader-a")
			ta.ok("read --as reader-b --ok s1-1.r1.reader-b")
		}
		if k.stage == stageMerging || k.stage == stageStopped {
			ta.ok("accept s1-1")
		}
	case stageFailed:
		ta.deal(1)
		ta.ok("take --as m1 s1-1.w1@1")
		ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'the tests went red'")
	}
	n := sprint.Note{Kind: sprint.Judgment, Type: k.typ, Stream: "s1", Primaries: []string{subject}, Count: 1, Decisions: k.decisions,
		Card: k.card, Other: k.other, StreamLevel: k.level, What: "a " + k.typ, Tier: k.tier, At: ta.a.now()}
	if k.typ == sprint.NFewReaders {
		ta.ok("reader away reader-b") // the reader the judgment would bring up
	}
	if k.typ == sprint.NRemindFailed {
		// the goal whose reminder failed
		ta.ok("goal set friend-a --file " + writeBrief(t, "the goal") + " --to file:" + filepath.Join(t.TempDir(), "route"))
	}
	switch {
	case k.sentinel:
		ta.ok("add --stream s1 --sentinel s1-stop --before s1-2") // reached: s1-1, before it, is in flight
		n.Primaries = []string{"s1-stop"}
	case k.typ == sprint.NStalled && k.friend:
		n.Primaries = []string{"friend-a"}
	case k.friend:
		n.Primaries = []string{sprint.FriendRow("friend-a")}
	}
	if n.Card == "" && k.stage != stageWaiting {
		n.Card = "s1-1"
	}
	if k.level {
		n.Stream, n.Primaries, n.Count = k.stream, nil, 0
		switch {
		case k.typ == sprint.NCross:
			n.Primaries, n.Count = []string{"s1-1", "s1-2"}, 2
		case k.stage == stageStopped && k.typ != sprint.NBaseRed:
			n.Primaries, n.Count = []string{"s1-1"}, 1
		}
		if k.typ == sprint.NNoRoute {
			// the cards it is about: the ready cards no route serves
			n.Primaries, n.Count = []string{"s1-1"}, 1
		}
		if k.sentinel {
			n.Primaries, n.Count = []string{"s1-stop"}, 1 // the held wave's sentinel
		}
	}
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	res, err := st.Run(context.Background(), store.Step{Verb: "raise", Load: store.All, Plan: func(s *sprint.Snapshot) sprint.Plan {
		p := sprint.Plan{Notes: []sprint.Note{n}}
		if k.typ == sprint.NProviderFunds || k.typ == sprint.NProviderLow {
			// the provider rests for its funds (route_rest.go), as the tick rests it
			p.Props = append(p.Props, sprint.PropWrite{Table: sprint.Fleet, Name: sprint.PropProviderRest("p1"), WasAbsent: true,
				Value: ta.a.now().UTC().Format(time.RFC3339) + " open - " + sprint.RestCredit + " out of credit"})
		}
		if k.stage == stageStopped {
			// the stream stopped on s1-1, as the lander stops it (steps_merge.go)
			ctl := s.StreamCtl("s1")
			p.Units = append(p.Units, sprint.Unit{Key: ctl.ID, Stream: "s1", Changes: []sprint.Change{{Table: sprint.Merge,
				Entry: ntable.BatchMemberEntry{ID: ctl.ID, Expect: &ntable.MemberExpect{Revision: strconv.FormatUint(uint64(ctl.Rev), 10), Place: &ntable.PlaceExpect{Row: ctl.Row, Col: ctl.Col}}, Set: map[string]string{"state": sprint.StreamStopped, "since": ta.a.now().UTC().Format(time.RFC3339), "cause": "conflict", "card": "s1-1"}}}}})
		}
		return p
	}})
	require.NoError(t, err, "%s: raising the judgment", k.typ)
	require.Empty(t, res.Refused, "%s: raising the judgment", k.typ)
	return ta
}

// fillPrinted is a printed line with its placeholders filled as a coordinator fills them.
func fillPrinted(t *testing.T, line string) string {
	brief := writeBrief(t, "the corrected work")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "s1-7", "The added work.", "")
	line = regexp.MustCompile(`'<path[^>]*>'`).ReplaceAllString(line, brief)
	line = regexp.MustCompile(`'<dir [^>]*>[^']*'`).ReplaceAllString(line, dir)
	r := strings.NewReplacer(
		"'<the corrected brief>'", brief, "<path>", brief,
		"'<why>'", "'the reason'", "'<fix>'", "'the fix'", "'<what you did>'", "'done'",
		"'<why nothing is to be done>'", "'seen'", "'<a higher tier>'", "heavy",
		"'<suspect>'", "s1-1", "'<new id>'", "s1-9", "'<fix id>'", "s1-8",
		"'<stream>'", "s1", "'<member>'", "m2",
		"'<merge sha>'", "0123456789abcdef0123456789abcdef01234567", "<route>", "file:/tmp/route",
		"'<what you looked at and found>'", "'looked'", "'<the payment made>'", "'paid'",
		"<member>", "m1", "<m>", "m1", "<half>", "1", "<r>", "reader-a", "<s>", "s1", "<coordinator>", "coordinator",
	)
	line = r.Replace(line)
	// a reader added is a new one; a reader brought up is one the table has, away
	if strings.HasPrefix(line, "nova-sprint reader add ") {
		return strings.ReplaceAll(line, "'<reader>'", "reader-c")
	}
	return strings.ReplaceAll(line, "'<reader>'", "reader-b")
}

func TestEveryPrintedDecisionCommandRuns(t *testing.T) {
	t.Parallel()
	for _, k := range judgmentKinds() {
		for i, d := range k.decisions {
			t.Run(k.typ+"/"+d, func(t *testing.T) {
				t.Parallel()
				k := k
				if d == "return" && k.stage == stageReview {
					k.stage = stageMerging // a card returns from merging
				}
				ta := stageJudgment(t, k)
				var g *sprint.Group
				for _, x := range ta.inboxGroups() {
					if x.Kind == sprint.Judgment && x.Type == k.typ {
						g = &x
						break
					}
				}
				require.NotNil(t, g, "%s: no inbox group", k.typ)
				var lines []string
				for _, c := range g.Commands {
					if c.Decision == k.decisions[i] {
						lines = c.Lines
					}
				}
				require.NotEmpty(t, lines, "%s: the decision %q prints no command (commands: %+v)", k.typ, d, g.Commands)
				if sprint.TickKept(k.typ) && !slices.Contains(k.decisions, "ack") && !slices.Contains(k.decisions, "keep") {
					for _, c := range g.Commands {
						for _, l := range c.Lines {
							require.NotContains(t, l, "nova-sprint ack ", "%s: a condition the tick keeps that does not list ack prints ack: %+v", k.typ, g.Commands)
						}
					}
				}
				for _, line := range lines {
					line = fillPrinted(t, line)
					args, ok := strings.CutPrefix(line, "nova-sprint ")
					if !ok {
						continue // another tool's command (nova-config, nova-friend): not this binary's to run
					}
					args, _, _ = strings.Cut(args, "  #")
					require.True(t, slices.ContainsFunc(verbNames(), func(v string) bool { return args == v || strings.HasPrefix(args, v+" ") }),
						"%s / %s: %q names no verb of nova-sprint", k.typ, d, line)
					code, out, errs := ta.do(args)
					switch {
					case code == 2:
						t.Fatalf("%s / %s: the verb refuses the printed command's shape: %s\n%s%s", k.typ, d, line, out, errs)
					case code != 0:
						t.Fatalf("%s / %s: the printed command was refused: %s\n%s%s", k.typ, d, line, out, errs)
					}
				}
			})
		}
	}
}

// brief on a card at its bound (a judgment offering brief open on it, in review) replaces it
// by its twin with the new brief, in one step: the twin placed with the brief, the old card
// off the table "replaced by" it, the card that needed it re-pointed to the twin, and the
// judgment answered; a card dealt that no such judgment holds still keeps its brief.
func TestBriefAtTheBoundCutsTheTwin(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{sprint.NBriefWrong, sprint.NBound} {
		ta := stageJudgment(t, judgmentKind{typ: typ, decisions: sprint.Decisions[sprint.NBriefWrong], stage: stageReview})
		out := ta.ok("brief s1-1 --brief-file " + writeBrief(t, "the corrected work"))
		require.Contains(t, out, "the brief's bound: s1-1 cut as its twin", "%s: %s", typ, out)
		twin := ta.primary("s1-1b")
		require.Equal(t, strings.TrimSuffix(passingBrief("the corrected work"), "\n"), twin.F("brief"), typ)
		require.Equal(t, "s1-1", twin.F(sprint.FieldReplaces), typ)
		require.Equal(t, "s1-1b", ta.primary("s1-2").F("needs"), "%s: the dependent is re-pointed to the twin", typ)
		for _, g := range ta.inboxGroups() {
			require.False(t, g.Kind == sprint.Judgment && g.Type == typ, "%s: the judgment is still open: %+v", typ, g)
		}
		st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
		s, err := st.Load(context.Background(), []string{sprint.Work}, nil)
		require.NoError(t, err)
		require.Nil(t, s.Work.Placed("s1-1"), "%s: the old card is off the table", typ)
		ta.clean()
	}
	// no judgment offering brief: a card dealt keeps its brief
	ta := stageJudgment(t, judgmentKind{typ: sprint.NWorkFailed, decisions: sprint.Decisions[sprint.NWorkFailed], stage: stageReview})
	code, _, errs := ta.do("brief s1-1 --brief-file " + writeBrief(t, "the corrected work"))
	require.Equal(t, 1, code, errs)
	require.Contains(t, errs, "keeps its brief")
}
