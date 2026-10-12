package friend

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// The names below are made up: no real friend's pronouns are set here, only the
// mechanism (the pronouns setting on a friend row) is held.

// TestSayRendersEachPronounSetAndTheDefault holds the one helper: the three sets
// a row may name, and the set a row that names none (the zero value) goes by.
func TestSayRendersEachPronounSetAndTheDefault(t *testing.T) {
	const text = "{They}/{they}/{them}/{their}/{theirs}/{themself}; {Their} lane, {Them}, {Theirs}, {Themself}"
	for _, tc := range []struct {
		name string
		p    Pronouns
		want string
	}{
		{"she/her", SheHer, "She/she/her/her/hers/herself; Her lane, Her, Hers, Herself"},
		{"he/him", HeHim, "He/he/him/his/his/himself; His lane, Him, His, Himself"},
		{"they/them", TheyThem, "They/they/them/their/theirs/themself; Their lane, Them, Theirs, Themself"},
		{"not stated: the zero value is they/them", Pronouns(""), "They/they/them/their/theirs/themself; Their lane, Them, Theirs, Themself"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.p.Say(text))
		})
	}
	assert.Equal(t, "no braces here", HeHim.Say("no braces here"))
	assert.Equal(t, "kept {literal}", SheHer.Say("kept {literal}"), "a brace word that is not a pronoun is left")
	assert.Equal(t, "his row of 3", HeHim.Sprintf("{their} row of %d", 3), "Sprintf says the pronouns, then formats")
	assert.Equal(t, "their row of {they}", TheyThem.Sprintf("{their} row of %s", "{they}"), "an argument is never read as a pronoun word")
}

func TestParsePronounsReadsTheThreeSetsAndNeverGuesses(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Pronouns
		ok   bool
	}{
		{"she/her", SheHer, true},
		{"he/him", HeHim, true},
		{"they/them", TheyThem, true},
		{" He/Him ", HeHim, true},
		{"", TheyThem, true},
		{"it/its", TheyThem, false},
		{"she", TheyThem, false},
		{"xe/xem", TheyThem, false},
	} {
		got, ok := ParsePronouns(tc.in)
		assert.Equal(t, tc.want, got, "%q", tc.in)
		assert.Equal(t, tc.ok, ok, "%q", tc.in)
	}
}

func TestTheBeatsAnswerNamesTheRowsPronouns(t *testing.T) {
	p, ok := RowPronouns("BEAT OK row_mode=one-shot row_width=2 row_pronouns=he/him row_config_dir=/x")
	assert.True(t, ok)
	assert.Equal(t, HeHim, p)
	_, ok = RowPronouns("BEAT OK row_mode=one-shot row_width=2")
	assert.False(t, ok, "an answer that names none leaves the flag's")
	_, ok = RowPronouns("BEAT OK row_pronouns=")
	assert.False(t, ok, "an empty word names none")
	p, ok = RowPronouns("BEAT OK row_pronouns=it/its")
	assert.False(t, ok, "a set that is not one of the three is not read")
	assert.Equal(t, TheyThem, p)
}

// TestPronounSetsMatchConfig holds the two lists equal: the sets nova-config
// accepts on a friend row and the sets this package renders.
func TestPronounSetsMatchConfig(t *testing.T) {
	var got []string
	for _, p := range AllPronouns {
		got = append(got, string(p))
	}
	assert.Equal(t, config.FriendPronounSets, got)
	assert.Equal(t, config.DefaultFriendPronouns, string(DefaultPronouns))
	assert.Equal(t, strings.Join(config.FriendPronounSets, ", "), PronounList())
}

func TestAPronounsRefIsTheDefaultUntilSetAndNilSafe(t *testing.T) {
	var nilRef *PronounsRef
	assert.Equal(t, TheyThem, nilRef.Get())
	nilRef.Set(HeHim) // a nil ref sets nothing and does not panic
	assert.Equal(t, TheyThem, nilRef.Get())
	var r PronounsRef
	assert.Equal(t, TheyThem, r.Get())
	r.Set(SheHer)
	assert.Equal(t, SheHer, r.Get())
	assert.Equal(t, HeHim, NewPronounsRef(HeHim).Get())
}

// pronounWords are every pronoun word a text about a friend must not carry
// for a set other than the friend's.
var pronounWords = map[Pronouns][]string{
	SheHer:   {"she", "her", "hers", "herself"},
	HeHim:    {"he", "him", "his", "himself"},
	TheyThem: {"they", "them", "their", "theirs", "themself"},
}

func wordsOf(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !(r >= 'a' && r <= 'z') }) {
		m[w] = true
	}
	return m
}

// TestTheTextsAboutAFriendSayTheFriendsPronouns holds every judgment text the
// daemon tells the seat: for each set (and the default, a row that names none)
// the text carries that set's words, never another set's, and no
// placeholder is left unsaid.
func TestTheTextsAboutAFriendSayTheFriendsPronouns(t *testing.T) {
	until := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	texts := map[string]func(p Pronouns) (string, string){
		"limit down":   func(p Pronouns) (string, string) { return LimitDownText("pat", p, until, "usage limit") },
		"limit unread": func(p Pronouns) (string, string) { return LimitUnreadText("pat", p, time.Hour, "some words") },
		"limit alike":  func(p Pronouns) (string, string) { return LimitAlikeText("pat", p, "same error") },
		"limit up":     func(p Pronouns) (string, string) { return LimitUpText("pat", p) },
		"rate":         func(p Pronouns) (string, string) { return RateJudgmentText("pat", p, 1, 2, "429") },
		"funds":        func(p Pronouns) (string, string) { return FundsJudgmentText("pat", p, "402") },
		"spend cap":    func(p Pronouns) (string, string) { return SpendCapJudgmentText("pat", p, "deepseek", 21, 20) },
		"fault down":   func(p Pronouns) (string, string) { return FaultDownText("pat", p, "env", until) },
		"lane fault":   func(p Pronouns) (string, string) { return LaneFaultText("pat", p, "lane-fault=2") },
	}
	for _, set := range []Pronouns{SheHer, HeHim, TheyThem, ""} {
		want := set
		if want == "" {
			want = TheyThem
		}
		for name, text := range texts {
			t.Run(string(set)+"/"+name, func(t *testing.T) {
				subject, body := text(set)
				all := subject + "\n" + body
				assert.NotContains(t, all, "{", "no placeholder is left unsaid")
				words := wordsOf(all)
				for other, forms := range pronounWords {
					if other == want {
						continue
					}
					for _, w := range forms {
						assert.False(t, words[w], "%s: another set's word %q in %s", name, w, all)
					}
				}
			})
		}
	}
	// the he/him and default cases name the friend's own words, not she/her's
	_, body := LimitDownText("pat", HeHim, until, "usage limit")
	assert.Contains(t, body, "His daemon beats down")
	assert.Contains(t, body, "on his row")
	_, body = LimitDownText("pat", "", until, "usage limit")
	assert.Contains(t, body, "Their daemon beats down")
	assert.Contains(t, body, "on their row")
	_, body = LimitDownText("pat", SheHer, until, "usage limit")
	assert.Contains(t, body, "Her daemon beats down")
	subject, _ := LimitDownText("pat", HeHim, until, "usage limit")
	assert.Contains(t, subject, "his harness is at its limit")
}

func TestADaemonSaysItsPronounsThroughTheOneHelper(t *testing.T) {
	d := &Daemon{Friend: "pat"}
	assert.Equal(t, "their row", d.say("{their} row"), "no pronouns held: they/them")
	d.Pro = NewPronounsRef(HeHim)
	assert.Equal(t, "his row for him", d.sayf("{their} row for %s", "him"))
	d.Pro.Set(SheHer)
	assert.Equal(t, "on her row", d.say("on {their} row"), "a beat that names other pronouns changes the next text")
}

func TestTheLaunchdAgentCarriesThePronounsOnlyWhenSet(t *testing.T) {
	a := Agent{Friend: "pat", Harness: "claude", Dir: "/d"}
	assert.NotContains(t, strings.Join(a.Args(), " "), "--pronouns")
	a.Pronouns = "he/him"
	args := a.Args()
	i := indexOf(args, "--pronouns")
	require.GreaterOrEqual(t, i, 0, "%v", args)
	assert.Equal(t, "he/him", args[i+1])
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// TestTheSeatIsToldOnTheBusInTheFriendsOwnPronouns runs the daemon's present with
// each pronoun set on the daemon: the line it sends the seat says that set's
// possessive, and the default (no pronouns held) says their.
func TestTheSeatIsToldOnTheBusInTheFriendsOwnPronouns(t *testing.T) {
	for _, tc := range []struct {
		name string
		pro  *PronounsRef
		want string
	}{
		{"she/her", NewPronounsRef(SheHer), "Every older message on her stream was marked superseded unread"},
		{"he/him", NewPronounsRef(HeHim), "Every older message on his stream was marked superseded unread"},
		{"they/them", NewPronounsRef(TheyThem), "Every older message on their stream was marked superseded unread"},
		{"none held", nil, "Every older message on their stream was marked superseded unread"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := presentRig(t)
			r.d.Pro = tc.pro
			r.send(t, "ada", "card old.w1 dealt: inbox/old.w1~14/BRIEF.md", "card old.w1 dealt to you\n")
			r.ping(t, "n-old")
			r.store.Advance(Window + time.Minute)
			r.ping(t, "n-fresh")
			r.send(t, "ada", "the live list", "newest note: QUEUE.json is the live list\n")
			r.run(t, 6)
			assert.Contains(t, strings.Join(r.adaGot(t), "\n"), tc.want)
		})
	}
}
