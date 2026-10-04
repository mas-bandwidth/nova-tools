package main

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

var quackID = regexp.MustCompile(`quack-[0-9a-f]{12}-[A-Za-z0-9_-]+?-[0-9]{3}`)

// quackCards runs one quack line and returns the ids it added, in order, with
// each card's brief as the work table holds it.
func (ta *testApp) quackCards(line string) ([]string, map[string]string) {
	ta.t.Helper()
	out := ta.ok(line)
	var ids []string
	briefs := map[string]string{}
	for _, id := range quackID.FindAllString(out, -1) {
		if _, seen := briefs[id]; seen {
			continue
		}
		_, _, _, f, ok := ta.m.Record(sprint.Work, id)
		require.True(ta.t, ok, "%s is on the work table", id)
		ids = append(ids, id)
		briefs[id] = f["brief"]
	}
	return ids, briefs
}

// quack cuts n cards per stream into a running store: ids that carry the run's
// stamp and so never repeat across passes, the tiers taken in turn down each
// stream, and a brief that names the card's own file, the repository and the
// base, and passes the card lint of the repository's own rules file. A quack
// card on a repository the members hold no rules file for carries the rules itself
// and names none; one on this repository is by reference: the stored brief does not
// carry the rules, and the card names the file (nova-tools#5174 rule 6).
func TestQuackCutsStampedCardsThatAlternateTiersAndPassTheLint(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	rules := filepath.Join("..", "..", "fleet", "child-rules.txt")
	ta.ok("init --readers reader-a,reader-b --members m1 --rules " + rules)
	const repo = "https://example.com/quack.git"
	first, briefs := ta.quackCards("quack --streams a,b --count 3 --repo " + repo)
	require.Len(t, first, 6)
	rs, err := swarm.ReadChildRules(rules)
	require.NoError(t, err)
	stamp := first[0][len("quack-") : len("quack-")+12]
	for _, s := range []string{"a", "b"} {
		for i, tier := range []string{"flash", "pro", "flash"} {
			id := "quack-" + stamp + "-" + s + "-00" + string(rune('1'+i))
			brief, ok := briefs[id]
			require.True(t, ok, "%s was cut: %v", id, first)
			m, why := cardhdr.ReadModel(brief)
			assert.Empty(t, why, id)
			assert.Equal(t, tier, m.Tier, "%s takes the tiers in turn", id)
			for _, want := range []string{"quacks/" + id + ".txt", "REPO: " + repo, "BASE: dev", "`quack: " + id + "`"} {
				assert.Contains(t, brief, want, id)
			}
			assert.Empty(t, swarm.LintCardChildWith([]byte(brief), rs), "%s passes the repository's card lint", id)
			assert.Empty(t, ta.cardRulesOf(id), "%s: a repository with no held rules file: the card carries its own", id)
		}
	}
	home, homeBriefs := ta.quackCards("quack --streams h --count 1 --repo https://example.com/mas-bandwidth/nova-tools.git")
	require.Len(t, home, 1)
	assert.NotContains(t, homeBriefs[home[0]], "RULES.", "a card on this repository does not carry the rules the member injects")
	assert.Empty(t, swarm.LintCardChildByReference([]byte(homeBriefs[home[0]]), rs))
	assert.Equal(t, swarm.DefaultRulesName, ta.cardRulesOf(home[0]))
	second, _ := ta.quackCards("quack --streams a --count 2 --tiers pro --base main --repo " + repo)
	require.Len(t, second, 2)
	for _, id := range second {
		assert.False(t, slices.Contains(first, id), "%s is new across passes", id)
	}
	assert.NotEqual(t, stamp, second[0][len("quack-"):len("quack-")+12], "each pass has its own stamp")
}

// A quack brief's read paragraph is for a read alone, and says how to tell: by JOB.md's
// first line, which every profile writes for a read and for no work card, a work card whose
// id begins "read" included. It judges the work's diff from the start JOB.md names, never
// against the base's tip. On the 5000-card load test (2026-10-01) the brief said "JOB.md
// says kind read", which no JOB.md says, and a work card ended "nothing to do: no PR to
// review yet"; readers that diffed against dev's tip took every card landed since for
// deletions in the work.
func TestQuackBriefTellsAReadByJobMdsFirstLine(t *testing.T) {
	t.Parallel()
	const id = "quack-0123456789ab-a-001"
	brief := quackBrief(id, "a", "flash", "https://example.com/quack.git", "dev", nil)
	title := "`" + cardcontract.ReadTitle + " <card>, attempt <n>`"
	require.Contains(t, brief, "As a read (only when JOB.md's first line is "+title)
	assert.Contains(t, brief, "from the start commit JOB.md names")
	assert.Contains(t, brief, "a diff against BASE's tip shows every card landed since as deleted")
	assert.NotContains(t, brief, "kind read", "no JOB.md says it")

	// the brief's title, <card> and <n> filled by any card id (no blank) and attempt
	readTitle := regexp.MustCompile(`^` + regexp.QuoteMeta(cardcontract.ReadTitle) + ` \S+, attempt [0-9]+$`)
	firstLine := func(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }
	st := cardcontract.Staged{Job: "/j", Repo: "/j/repo", Head: strings.Repeat("a", 40), Git: "/usr/bin/git", Start: strings.Repeat("b", 40)}
	for _, family := range cardcontract.Families {
		p := cardcontract.For(family)
		for _, card := range []string{id + ".w1", "readme-fix.w1", "read.w1"} {
			work := cardcontract.Frame{Kind: "work", Card: card, Attempt: 1, Repo: "https://example.com/quack.git", Branch: "sprint/w", BaseRef: "dev"}
			l := firstLine(p.JobText(work, st))
			assert.False(t, readTitle.MatchString(l), "%s: a work card's JOB.md never opens as a read's: %q", family, l)
		}
		read := cardcontract.Frame{Kind: "read", Card: id + ".r1.reader-a", Attempt: 1, Repo: "https://example.com/quack.git", Branch: "sprint/w", BaseRef: "dev", ReviewBase: "dev"}
		l := firstLine(p.JobText(read, st))
		assert.True(t, readTitle.MatchString(l), "%s: a read's JOB.md opens as the brief says: %q", family, l)
	}
}

// One run names every problem: no streams, no count, no repository, a tier
// that is no tier, an id given; and nothing is written.
func TestQuackRefusesEveryMissingInputAtOnce(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.applies()
	code, _, errs := ta.do("quack x1 --tiers flash,turbo")
	assert.Equal(t, 2, code)
	for _, want := range []string{"takes no ids", "--streams", "--count", "--repo", `"turbo" is not a tier`, "run: nova-sprint"} {
		assert.Contains(t, errs, want)
	}
	assert.Equal(t, before, ta.applies(), "a refusal writes nothing")
	assert.True(t, strings.HasPrefix(errs, "nova-sprint quack REFUSED: "), errs)
}

// The --op contract (docs/SPEC-SPRINT.md section 11): the same quack call
// retried with the same --op (a reply lost) replays the recorded result and
// changes nothing; other arguments under that op are refused; a fresh op is a
// new pass with new ids.
func TestQuackReviewRetriesTheSameOperation(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	const line = "quack --streams a,b --count 1 --repo https://example.com/quack.git --op quack-pass-one"
	first, _ := ta.quackCards(line)
	require.Len(t, first, 2)
	writes := ta.applies()
	again, _ := ta.quackCards(line)
	assert.Equal(t, first, again, "the retry names the recorded cards")
	assert.Equal(t, writes, ta.applies(), "the retry writes nothing")
	code, _, errs := ta.do("quack --streams a,b --count 2 --repo https://example.com/quack.git --op quack-pass-one")
	assert.NotEqual(t, 0, code, "other arguments under the same op are refused")
	assert.Contains(t, errs, "quack-pass-one")
	fresh, _ := ta.quackCards("quack --streams a,b --count 1 --repo https://example.com/quack.git --op quack-pass-two")
	require.Len(t, fresh, 2)
	for _, id := range fresh {
		assert.False(t, slices.Contains(first, id), "%s: a fresh op is a new pass", id)
	}
}

// A multi-stream quack is all or none: a legal 128-character stream beside a
// short one makes card ids over the 128 a card id may be, and the call writes
// nothing, refusing once with the stream and the id length (the reader's case).
func TestQuackIsAllOrNoneAcrossItsStreams(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	long := strings.Repeat("s", sprint.MaxIDLen)
	require.True(t, sprint.ValidID(long), "the stream itself is legal")
	before := ta.applies()
	code, out, errs := ta.do("quack --streams a," + long + " --count 1 --repo https://example.com/quack.git")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "stream "+long)
	assert.Contains(t, errs, "151 characters")
	assert.NotContains(t, out, "MOVED")
	assert.Equal(t, before, ta.applies(), "nothing is written")
	assert.Empty(t, quackID.FindAllString(out, -1), "no card was added")
}

// The reader's probes on #5021: an op id names a call in one store's record, not
// a sprint's incarnation. The same quack with the same --op after teardown and
// init, or in another store, has no recorded operation to replay and draws a new
// stamp, so its cards' files are new to the repository's history.
func TestQuackOpReusedInAnotherIncarnationDrawsANewStamp(t *testing.T) {
	t.Parallel()
	const line = "quack --streams a,b --count 1 --repo https://example.com/quack.git --op "
	t.Run("teardown then init", func(t *testing.T) {
		t.Parallel()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		first, _ := ta.quackCards(line + "reused-after-teardown")
		require.Len(t, first, 2)
		ta.ok("teardown --confirm sprint")
		ta.ok("init --readers reader-a,reader-b --members m1")
		again, _ := ta.quackCards(line + "reused-after-teardown")
		require.Len(t, again, 2)
		for _, id := range again {
			assert.False(t, slices.Contains(first, id), "%s: a torn-down store's op draws a new stamp", id)
		}
	})
	t.Run("two stores", func(t *testing.T) {
		t.Parallel()
		var passes [][]string
		for i := 0; i < 2; i++ {
			ta := newTestApp(t)
			ta.ok("init --readers reader-a,reader-b --members m1")
			ids, _ := ta.quackCards(line + "shared-operation-name")
			require.Len(t, ids, 2)
			passes = append(passes, ids)
		}
		for _, id := range passes[1] {
			assert.False(t, slices.Contains(passes[0], id), "%s: another store's op draws a new stamp", id)
		}
	})
}
