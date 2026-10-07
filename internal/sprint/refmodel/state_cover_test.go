package refmodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The tests in this file cover the identities, views, roundings and notes of
// state.go that had no unit test at all: each pins one function's main path
// and its refusal, the guard the model answers false, empty or unchanged.
// Every test is named TestStateCover... so -run TestStateCover selects them.
// Nothing here sleeps, tells the time, forks, opens a socket or needs a
// store: a State is a plain struct, and the seams are New and the fields.

// coverState is a sprint as New leaves it: readers r1, r2 and r3 in row
// order, members m1 and m2 both down, the machine STOPPED.
func coverState() State {
	return New([]string{"r1", "r2", "r3"}, []string{"m1", "m2"}, "coord")
}

// coverRead places one read card of p at its attempt with reader r in place.
func coverRead(s *State, p string, attempt int, r, place string) {
	id := RC(p, attempt, r)
	s.Reads[id] = ReadCard{Primary: p, Attempt: attempt, Reader: r, Place: place}
}

// TestStateCoverJudgmentString covers Judgment.String (state.go:225): a
// judgment prints its type and subject divided by a bar; the empty judgment
// still prints its bar.
func TestStateCoverJudgmentString(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		j    Judgment
		want string
	}{
		"a failed note on a primary":        {j: Judgment{JFailed, "p1"}, want: "failed|p1"},
		"a stop on a stream":                {j: Judgment{JConflict, StreamSubject("s1")}, want: "stopped:conflict|stream:s1"},
		"the empty judgment prints its bar": {j: Judgment{}, want: "|"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.j.String())
		})
	}
}

// TestStateCoverRefusalError covers (*Refusal).Error (state.go:314): a
// refusal names why, in the words refuse gave it; a reason-less refusal is
// still a refusal.
func TestStateCoverRefusalError(t *testing.T) {
	t.Parallel()
	err := refuse("no up member for stream %s", "s1")
	assert.ErrorAs(t, err, new(*Refusal))
	for name, tc := range map[string]struct {
		err  *Refusal
		want string
	}{
		"refuse formats its reason": {err: &Refusal{Why: "no up member for stream s1"}, want: "refused: no up member for stream s1"},
		"the error refuse returned": {err: err.(*Refusal), want: "refused: no up member for stream s1"},
		"a refusal with no reason":  {err: &Refusal{}, want: "refused: "},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}

// TestStateCoverChoiceErrorError covers (*ChoiceError).Error (state.go:324):
// a choice the model does not allow names why in its words; a reason-less
// choice error is still one.
func TestStateCoverChoiceErrorError(t *testing.T) {
	t.Parallel()
	err := badChoice("%s is not the next member", "m2")
	assert.ErrorAs(t, err, new(*ChoiceError))
	for name, tc := range map[string]struct {
		err  *ChoiceError
		want string
	}{
		"badChoice formats its reason":  {err: &ChoiceError{Why: "m2 is not the next member"}, want: "choice not allowed: m2 is not the next member"},
		"the error badChoice returned":  {err: err.(*ChoiceError), want: "choice not allowed: m2 is not the next member"},
		"a choice error with no reason": {err: &ChoiceError{}, want: "choice not allowed: "},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}

// TestStateCoverWC covers WC (state.go:331): a work card is its primary's id
// with .w and the attempt, attempt zero and all.
func TestStateCoverWC(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		p    string
		a    int
		want string
	}{
		"an attempt as it runs": {p: "p1", a: 2, want: "p1.w2"},
		"attempt zero prints":   {p: "p1", a: 0, want: "p1.w0"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, WC(tc.p, tc.a))
		})
	}
}

// TestStateCoverRC covers RC (state.go:334): a read card is its primary's id
// with .r, the attempt, and the reader, attempt zero and all.
func TestStateCoverRC(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		p    string
		a    int
		r    string
		want string
	}{
		"a reader at an attempt": {p: "p1", a: 2, r: "r3", want: "p1.r2.r3"},
		"attempt zero prints":    {p: "p1", a: 0, r: "r1", want: "p1.r0.r1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, RC(tc.p, tc.a, tc.r))
		})
	}
}

// TestStateCoverInWork covers InWork (state.go:339): a primary stands in the
// cell it is in. The refusals are a primary in another cell and a stranger.
func TestStateCoverInWork(t *testing.T) {
	t.Parallel()
	s := coverState()
	s.Primaries["p1"] = Primary{Stream: "s", State: Ready}
	for name, tc := range map[string]struct {
		p, c string
		want bool
	}{
		"in its cell":             {p: "p1", c: Ready, want: true},
		"another cell says no":    {p: "p1", c: Working, want: false},
		"no such primary says no": {p: "p9", c: Ready, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.InWork(tc.p, tc.c))
		})
	}
}

// TestStateCoverPlacedp covers Placedp (state.go:345): an admitted primary
// still on the table is placed. The refusals are one dropped to Off and a
// stranger.
func TestStateCoverPlacedp(t *testing.T) {
	t.Parallel()
	s := coverState()
	s.Primaries["p1"] = Primary{Stream: "s", State: Review}
	s.Primaries["p2"] = Primary{Stream: "s", State: Off}
	for name, tc := range map[string]struct {
		p    string
		want bool
	}{
		"on the table":   {p: "p1", want: true},
		"dropped to Off": {p: "p2", want: false},
		"never admitted": {p: "p9", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.Placedp(tc.p))
		})
	}
}

// TestStateCoverNeedsMet covers NeedsMet (state.go:352): every need landed,
// or dropped and waived, with nothing waited for by position. The refusal is
// a need still climbing.
func TestStateCoverNeedsMet(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(s *State)
		want  bool
	}{
		"every need landed": {
			build: func(s *State) {
				s.Primaries["q"] = Primary{Stream: "s", State: Landed, Score: 0}
				s.Primaries["p"] = Primary{Stream: "s", State: Ready, Score: 1, Needs: []string{"q"}}
			},
			want: true,
		},
		"a dropped need waived": {
			build: func(s *State) {
				s.Primaries["q"] = Primary{Stream: "s", State: Off}
				s.Primaries["p"] = Primary{Stream: "s", State: Ready, Score: 1, Needs: []string{"q"}, Waived: []string{"q"}}
			},
			want: true,
		},
		"a need not landed is refused": {
			build: func(s *State) {
				s.Primaries["q"] = Primary{Stream: "s", State: Review}
				s.Primaries["p"] = Primary{Stream: "s", State: Ready, Score: 1, Needs: []string{"q"}}
			},
			want: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			assert.Equal(t, tc.want, s.NeedsMet("p"))
		})
	}
}

// TestStateCoverHasBefore covers HasBefore (state.go:365): a sentinel has
// something to be reached after it when it names a need, or a primary of its
// stream scores before it. The refusal is one with nothing before it.
func TestStateCoverHasBefore(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(s *State)
		want  bool
	}{
		"a named need is before it": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Needs: []string{"q"}}
			},
			want: true,
		},
		"a lower score in its stream is before it": {
			build: func(s *State) {
				s.Primaries["x"] = Primary{Stream: "s", State: Ready, Score: 1}
				s.Primaries["p"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Score: 2}
			},
			want: true,
		},
		"first in its stream has nothing before it": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Score: 1}
				s.Primaries["x"] = Primary{Stream: "s", State: Ready, Score: 2}
			},
			want: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			assert.Equal(t, tc.want, s.HasBefore("p"))
		})
	}
}

// TestStateCoverReachable covers Reachable (state.go:380): a sentinel with
// its needs met is reached when something came before it, or when no other
// work of the sprint is in flight. The refusal is nothing before it with a
// card still up for work.
func TestStateCoverReachable(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(s *State)
		want  bool
	}{
		"something before it reaches": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Needs: []string{"q"}}
			},
			want: true,
		},
		"work in flight with nothing before it is refused": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Score: 1}
				s.Primaries["q"] = Primary{Stream: "t", State: Ready, Score: 1}
			},
			want: false,
		},
		"nothing before it and nothing in flight reaches": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Score: 1}
				s.Primaries["q"] = Primary{Stream: "t", State: Landed, Score: 1}
			},
			want: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			assert.Equal(t, tc.want, s.Reachable("p"))
		})
	}
}

// TestStateCoverPositionWaits covers PositionWaits (state.go:397): a
// sentinel waits by its place for every primary before it, a waiting primary
// for the latest unlanded sentinel before it. The refusals are a ready
// primary, a landed one and a stranger, all waiting for nothing.
func TestStateCoverPositionWaits(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		p     string
		build func(s *State)
		want  []string
	}{
		"a sentinel waits for all before it": {
			p: "p",
			build: func(s *State) {
				s.Primaries["a"] = Primary{Stream: "s", State: Review, Score: 1}
				s.Primaries["b"] = Primary{Stream: "s", State: Landed, Score: 1.5}
				s.Primaries["c"] = Primary{Stream: "s", State: Ready, Score: 2}
				s.Primaries["p"] = Primary{Stream: "s", Kind: KindSentinel, State: Waiting, Score: 3}
			},
			want: []string{"a", "c"},
		},
		"a waiting primary waits for the latest sentinel before it": {
			p: "p",
			build: func(s *State) {
				s.Primaries["x"] = Primary{Stream: "s", State: Working, Score: 0.5}
				s.Primaries["sa"] = Primary{Stream: "s", Kind: KindSentinel, State: Review, Score: 1}
				s.Primaries["sb"] = Primary{Stream: "s", Kind: KindSentinel, State: Working, Score: 2}
				s.Primaries["p"] = Primary{Stream: "s", State: Waiting, Score: 3}
			},
			want: []string{"sb"},
		},
		"a ready primary waits for nothing": {
			p: "p",
			build: func(s *State) {
				s.Primaries["sa"] = Primary{Stream: "s", Kind: KindSentinel, State: Working, Score: 1}
				s.Primaries["p"] = Primary{Stream: "s", State: Ready, Score: 2}
			},
		},
		"a landed primary waits for nothing": {
			p: "p",
			build: func(s *State) {
				s.Primaries["sa"] = Primary{Stream: "s", Kind: KindSentinel, State: Working, Score: 1}
				s.Primaries["p"] = Primary{Stream: "s", State: Landed, Score: 2}
			},
		},
		"a stranger has no waits": {
			p:     "p9",
			build: func(s *State) {},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			assert.Equal(t, tc.want, s.PositionWaits(tc.p))
		})
	}
}

// TestStateCoverDroppedNeeds covers DroppedNeeds (state.go:425): the needs
// of p dropped and not waived. The refusal is waiving them away.
func TestStateCoverDroppedNeeds(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(s *State)
		want  []string
	}{
		"a dropped need names itself": {
			build: func(s *State) {
				s.Primaries["q1"] = Primary{Stream: "s", State: Off}
				s.Primaries["q2"] = Primary{Stream: "s", State: Landed}
				s.Primaries["p"] = Primary{Stream: "s", State: Waiting, Needs: []string{"q1", "q2"}}
			},
			want: []string{"q1"},
		},
		"a waived dropped need drops out": {
			build: func(s *State) {
				s.Primaries["q1"] = Primary{Stream: "s", State: Off}
				s.Primaries["p"] = Primary{Stream: "s", State: Waiting, Needs: []string{"q1"}, Waived: []string{"q1"}}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			assert.Equal(t, tc.want, s.DroppedNeeds("p"))
		})
	}
}

// TestStateCoverRL covers RL (state.go:459): the member's ready queue. The
// refusals are a working card, which is not in the queue, and a member with
// no cards at all.
func TestStateCoverRL(t *testing.T) {
	t.Parallel()
	s := coverState()
	s.Work["p1.w1"] = WorkCard{Primary: "p1", Attempt: 1, Member: "m1", Place: FReady}
	s.Work["p2.w1"] = WorkCard{Primary: "p2", Attempt: 1, Member: "m1", Place: FReady}
	s.Work["p3.w1"] = WorkCard{Primary: "p3", Attempt: 1, Member: "m1", Place: FWorking}
	s.Work["p4.w1"] = WorkCard{Primary: "p4", Attempt: 1, Member: "m2", Place: FReady}
	for name, tc := range map[string]struct {
		m    string
		want int
	}{
		"its two ready cards, the working one aside": {m: "m1", want: 2},
		"one ready":                           {m: "m2", want: 1},
		"a member with no cards has no queue": {m: "m9", want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.RL(tc.m))
		})
	}
}

// TestStateCoverStreamOrderPrivate covers streamOrder (state.go:510): the
// streams the sprint has, in name order, an extra stream not yet on it taken
// in once and in order. The refusals are an extra the sprint already has,
// and no extra at all.
func TestStateCoverStreamOrderPrivate(t *testing.T) {
	t.Parallel()
	s := coverState()
	s.Streams["b"] = Stream{State: SWaiting}
	s.Streams["a"] = Stream{State: SMerging}
	for name, tc := range map[string]struct {
		extra []string
		want  []string
	}{
		"an extra joins in name order":            {extra: []string{"c", "a"}, want: []string{"a", "b", "c"}},
		"an extra the sprint has changes nothing": {extra: []string{"a"}, want: []string{"a", "b"}},
		"no extra keeps the sprint's own":         {extra: nil, want: []string{"a", "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.streamOrder(tc.extra...))
		})
	}
}

// TestStateCoverReworkChoice covers ReworkChoice (state.go:563): a rework
// deals its next attempt round the fleet off the member holding the
// attempt's card. The refusal is no member up.
func TestStateCoverReworkChoice(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(s *State)
		want  string
	}{
		"the next up member, not the one holding it": {
			build: func(s *State) {
				s.Members["m1"], s.Members["m2"] = Up, Up
				s.Primaries["p"] = Primary{Stream: "s", State: Review, Attempt: 1}
				s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Member: "m1", Place: FWorking}
			},
			want: "m2",
		},
		"a card gone from the table keeps its round": {
			build: func(s *State) {
				s.Members["m1"], s.Members["m2"] = Up, Up
				s.Primaries["p"] = Primary{Stream: "s", State: Review, Attempt: 2}
			},
			want: "m1",
		},
		"no member up is refused": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", State: Review, Attempt: 1}
			},
			want: "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			assert.Equal(t, tc.want, s.ReworkChoice("p"))
		})
	}
}

// TestStateCoverNextReaders covers NextReaders (state.go:583): the ask's k
// readers, from just past AskLast in name order, wrapping, each without a
// read card at the attempt. The refusal is no reader left free.
func TestStateCoverNextReaders(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(s *State)
		k     int
		want  []string
	}{
		"the first k free round from the last ask": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Attempt: 1}
				coverRead(s, "p", 1, "r1", Asked)
			},
			k:    2,
			want: []string{"r2", "r3"},
		},
		"AskLast names where the round starts": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Attempt: 1}
				s.AskLast = "r2"
			},
			k:    2,
			want: []string{"r3", "r1"},
		},
		"no reader free is refused": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Attempt: 1}
				for _, r := range []string{"r1", "r2", "r3"} {
					coverRead(s, "p", 1, r, Asked)
				}
			},
			k: 3,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			assert.Equal(t, tc.want, s.NextReaders("p", tc.k))
		})
	}
}

// TestStateCoverAskedLen covers AskedLen (state.go:607): the reader's cards
// still asked. The refusals are a card past asked and a reader with none.
func TestStateCoverAskedLen(t *testing.T) {
	t.Parallel()
	s := coverState()
	coverRead(&s, "p1", 1, "r1", Asked)
	coverRead(&s, "p2", 1, "r1", Asked)
	coverRead(&s, "p3", 1, "r1", Reading)
	coverRead(&s, "p4", 1, "r2", Asked)
	for name, tc := range map[string]struct {
		r    string
		want int
	}{
		"two asked, its reading card aside":   {r: "r1", want: 2},
		"one asked":                           {r: "r2", want: 1},
		"a reader with no cards asks nothing": {r: "r3", want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.AskedLen(tc.r))
		})
	}
}

// TestStateCoverOutOf covers OutOf (state.go:618): p's read cards asked or
// reading, in id order. The refusals are a card already judged, another
// primary's cards, and a primary with none.
func TestStateCoverOutOf(t *testing.T) {
	t.Parallel()
	s := coverState()
	coverRead(&s, "p", 1, "r2", Asked)
	coverRead(&s, "p", 1, "r1", Reading)
	coverRead(&s, "p", 1, "r3", OK)
	coverRead(&s, "q", 1, "r1", Asked)
	s.Primaries["r"] = Primary{Stream: "s"}
	s.Primaries["j"] = Primary{Stream: "s"}
	coverRead(&s, "j", 1, "r1", OK)
	for name, tc := range map[string]struct {
		p    string
		want []string
	}{
		"asked and reading, in id order":            {p: "p", want: []string{"p.r1.r1", "p.r1.r2"}},
		"another primary's card stays out":          {p: "q", want: []string{"q.r1.r1"}},
		"a primary with no cards is out of nothing": {p: "r"},
		"a primary whose only card was judged":      {p: "j"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.OutOf(tc.p))
		})
	}
}

// TestStateCoverLiveReadsOf covers LiveReadsOf (state.go:631): p's read
// cards on the table in any cell but retired, in id order. The refusal is a
// primary whose only card retired.
func TestStateCoverLiveReadsOf(t *testing.T) {
	t.Parallel()
	s := coverState()
	coverRead(&s, "p", 1, "r3", Asked)
	coverRead(&s, "p", 1, "r1", OK)
	coverRead(&s, "p", 2, "r2", Retired)
	coverRead(&s, "q", 1, "r1", Reading)
	coverRead(&s, "r", 1, "r1", Retired)
	for name, tc := range map[string]struct {
		p    string
		want []string
	}{
		"asked and ok live, the retired and q's aside": {p: "p", want: []string{"p.r1.r1", "p.r1.r3"}},
		"another primary's card stays out":             {p: "q", want: []string{"q.r1.r1"}},
		"a retired card is not live":                   {p: "r"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.LiveReadsOf(tc.p))
		})
	}
}

// TestStateCoverOkReaders covers OkReaders (state.go:644): the readers with
// an ok read card at p's head, in reader row order. The refusals are a card
// not ok, one at an older attempt, and a primary with no head yet.
func TestStateCoverOkReaders(t *testing.T) {
	t.Parallel()
	s := coverState()
	s.Primaries["p"] = Primary{Stream: "s", Head: 2}
	coverRead(&s, "p", 2, "r1", OK)
	coverRead(&s, "p", 2, "r2", OK)
	coverRead(&s, "p", 2, "r3", Broken)
	coverRead(&s, "p", 1, "r3", OK)
	s.Primaries["q"] = Primary{Stream: "s"}
	coverRead(&s, "q", 0, "r1", OK)
	for name, tc := range map[string]struct {
		p    string
		want []string
	}{
		"the two ok at the head, in row order": {p: "p", want: []string{"r1", "r2"}},
		"no head yet refuses its ok card":      {p: "q"},
		"a stranger has no ok readers":         {p: "p9"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.OkReaders(tc.p))
		})
	}
}

// TestStateCoverAcceptable covers Acceptable (state.go:656): two ok readers
// at the head accept it. The refusal is one short.
func TestStateCoverAcceptable(t *testing.T) {
	t.Parallel()
	build := func(broken bool) State {
		s := coverState()
		s.Primaries["p"] = Primary{Stream: "s", Head: 1}
		coverRead(&s, "p", 1, "r1", OK)
		if broken {
			coverRead(&s, "p", 1, "r2", Broken)
		} else {
			coverRead(&s, "p", 1, "r2", OK)
		}
		return s
	}
	for name, tc := range map[string]struct {
		s    State
		want bool
	}{
		"two ok readers at the head": {s: build(false), want: true},
		"one ok reader is refused":   {s: build(true), want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.s.Acceptable("p"))
		})
	}
}

// TestStateCoverFailed covers Failed (state.go:659): the current attempt's
// work card came back failed. The refusals are an ok card and a primary with
// no card at its attempt.
func TestStateCoverFailed(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(s *State)
		p     string
		want  bool
	}{
		"the attempt's card failed": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Attempt: 2}
				s.Work[WC("p", 2)] = WorkCard{Primary: "p", Attempt: 2, OK: "failed"}
			},
			p: "p", want: true,
		},
		"an ok card has not failed": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", Attempt: 2}
				s.Work[WC("p", 2)] = WorkCard{Primary: "p", Attempt: 2, OK: "ok"}
			},
			p: "p", want: false,
		},
		"a primary with no card at its attempt has not failed": {
			build: func(s *State) {
				s.Primaries["q"] = Primary{Stream: "s", Attempt: 1}
			},
			p: "q", want: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			assert.Equal(t, tc.want, s.Failed(tc.p))
		})
	}
}

// TestStateCoverAskedNow covers AskedNow (state.go:665): a read card of p's
// attempt was made, even one since retired. The refusal is cards only of an
// older attempt.
func TestStateCoverAskedNow(t *testing.T) {
	t.Parallel()
	s := coverState()
	s.Primaries["p"] = Primary{Stream: "s", Attempt: 2}
	coverRead(&s, "p", 2, "r1", Retired)
	s.Primaries["q"] = Primary{Stream: "s", Attempt: 3}
	coverRead(&s, "q", 2, "r1", OK)
	s.Primaries["r"] = Primary{Stream: "s", Attempt: 1}
	for name, tc := range map[string]struct {
		p    string
		want bool
	}{
		"a retired card of the attempt was still made":   {p: "p", want: true},
		"only an older attempt's cards asks nothing now": {p: "q", want: false},
		"no cards at all asks nothing":                   {p: "r", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.AskedNow(tc.p))
		})
	}
}

// TestStateCoverOpenOn covers OpenOn (state.go:676): a judgment is open on
// the subject. The refusal is a subject with none.
func TestStateCoverOpenOn(t *testing.T) {
	t.Parallel()
	s := coverState()
	s.Open[Judgment{JFailed, "p1"}] = true
	for name, tc := range map[string]struct {
		subject string
		want    bool
	}{
		"its note is open on the primary": {subject: "p1", want: true},
		"nothing is open on another":      {subject: "p2", want: false},
		"nothing is open on the sprint":   {subject: SprintSubject, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.OpenOn(tc.subject))
		})
	}
}

// TestStateCoverStreamOrder covers StreamOrder (state.go:687): the stream's
// primaries on the table in work order, score first and id on a tie. The
// refusals are one dropped, another stream's, and a stream with nobody.
func TestStateCoverStreamOrder(t *testing.T) {
	t.Parallel()
	s := coverState()
	s.Primaries["p1"] = Primary{Stream: "s", State: Ready, Score: 2}
	s.Primaries["p2"] = Primary{Stream: "s", State: Working, Score: 1}
	s.Primaries["p3"] = Primary{Stream: "s", State: Review, Score: 1}
	s.Primaries["p4"] = Primary{Stream: "s", State: Off, Score: 0}
	s.Primaries["t1"] = Primary{Stream: "t", State: Ready, Score: 1}
	for name, tc := range map[string]struct {
		stream string
		want   []string
	}{
		"by score, id on the tie, the dropped one left out": {stream: "s", want: []string{"p2", "p3", "p1"}},
		"another stream's primary stays out":                {stream: "t", want: []string{"t1"}},
		"a stream with nobody on the table has no order":    {stream: "u"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.StreamOrder(tc.stream))
		})
	}
}

// TestStateCoverStreamTurns covers streamTurns (state.go:712): one id of
// each stream in turn from the first past last, wrapping, a stream with none
// skipped at no cost of a turn, within a stream by score; a stream not yet
// on the sprint joins the names. The refusal is no ids at all.
func TestStateCoverStreamTurns(t *testing.T) {
	t.Parallel()
	s := coverState()
	s.Streams["s1"] = Stream{State: SMerging}
	s.Streams["s2"] = Stream{State: SWaiting}
	s.Primaries["x1"] = Primary{Stream: "s1", Score: 1}
	s.Primaries["x2"] = Primary{Stream: "s1", Score: 2}
	s.Primaries["y1"] = Primary{Stream: "s2", Score: 1}
	s.Primaries["z1"] = Primary{Stream: "s0", Score: 1}
	ids := []string{"x2", "y1", "x1"}
	for name, tc := range map[string]struct {
		ids  []string
		last string
		want []string
	}{
		"the turn starts at the first stream": {ids: ids, last: "", want: []string{"x1", "y1", "x2"}},
		"the turn wraps past last":            {ids: ids, last: "s1", want: []string{"y1", "x1", "x2"}},
		"a stream with none skips its turn":   {ids: []string{"y1", "x1"}, last: "s2", want: []string{"x1", "y1"}},
		"a stream the sprint has no record of joins the names": {
			ids:  append(append([]string{}, ids...), "z1"),
			last: "",
			want: []string{"z1", "x1", "y1", "x2"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, s.streamTurns(tc.ids, tc.last))
		})
	}
	assert.Empty(t, s.streamTurns(nil, ""), "no ids make no turns")
}

// TestStateCoverAddSorted covers addSorted (state.go:763): the added names
// land in sorted order on a copy of the base. The refusal is a name already
// held, which is not added twice.
func TestStateCoverAddSorted(t *testing.T) {
	t.Parallel()
	base := []string{"b", "d"}
	for name, tc := range map[string]struct {
		xs   []string
		add  []string
		want []string
	}{
		"new names sort in":                      {xs: base, add: []string{"a", "c"}, want: []string{"a", "b", "c", "d"}},
		"a name already held is not added twice": {xs: base, add: []string{"b", "b"}, want: []string{"b", "d"}},
		"nothing added from no base":             {xs: nil, add: nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, addSorted(tc.xs, tc.add...))
		})
	}
	assert.Equal(t, []string{"b", "d"}, base, "addSorted leaves its argument alone")
}

// TestStateCoverSetPrimary covers setPrimary (state.go:774): the setter runs
// on the primary's record and writes it back, making the record when the
// primary is not on the table yet. The refusal is a setter that changes
// nothing: the record still lands on the table as it was.
func TestStateCoverSetPrimary(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		before func(s *State)
		set    func(pr *Primary)
		want   Primary
	}{
		"a stranger is made by the setter alone": {
			set:  func(pr *Primary) { pr.Stream, pr.State, pr.Score = "s", Ready, 3 },
			want: Primary{Stream: "s", State: Ready, Score: 3},
		},
		"the setter edits in place, the rest is kept": {
			before: func(s *State) { s.Primaries["p"] = Primary{Stream: "s", State: Ready, Score: 3} },
			set:    func(pr *Primary) { pr.Attempt = 2 },
			want:   Primary{Stream: "s", State: Ready, Score: 3, Attempt: 2},
		},
		"a setter that changes nothing keeps the record": {
			before: func(s *State) { s.Primaries["p"] = Primary{Stream: "s", State: Ready, Score: 3, Attempt: 2} },
			set:    func(pr *Primary) {},
			want:   Primary{Stream: "s", State: Ready, Score: 3, Attempt: 2},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			if tc.before != nil {
				tc.before(&s)
			}
			s.setPrimary("p", tc.set)
			assert.Equal(t, tc.want, s.Primaries["p"])
		})
	}
}

// TestStateCoverCloseOn covers closeOn (state.go:784): every open judgment
// on the subject closes, or only those of the named types. The refusals are
// a type not named, another subject's note, and a subject with nothing open.
func TestStateCoverCloseOn(t *testing.T) {
	t.Parallel()
	base := map[Judgment]bool{{JCI, "p"}: true, {JFailed, "p"}: true, {JFailed, "q"}: true}
	kept := func(judgments ...Judgment) map[Judgment]bool {
		out := map[Judgment]bool{}
		for _, j := range judgments {
			out[j] = true
		}
		return out
	}
	for name, tc := range map[string]struct {
		subject string
		types   []string
		want    map[Judgment]bool
	}{
		"every type on the subject closes": {
			subject: "p",
			want:    kept(Judgment{JFailed, "q"}),
		},
		"only the named type closes on it": {
			subject: "p", types: []string{JCI},
			want: kept(Judgment{JFailed, "p"}, Judgment{JFailed, "q"}),
		},
		"a type not named refuses to close": {
			subject: "p", types: []string{JBroken},
			want: base,
		},
		"a subject with nothing open closes nothing": {
			subject: "r",
			want:    base,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			s.Open = map[Judgment]bool{{JCI, "p"}: true, {JFailed, "p"}: true, {JFailed, "q"}: true}
			s.closeOn(tc.subject, tc.types...)
			assert.Equal(t, tc.want, s.Open)
		})
	}
}

// TestStateCoverJoin covers Join (state.go:807): a list joins with commas
// for printing. The refusals are the empty list, joining to the empty
// string, and one name standing alone.
func TestStateCoverJoin(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		xs   []string
		want string
	}{
		"several names":          {xs: []string{"a", "b"}, want: "a,b"},
		"one name stands alone":  {xs: []string{"a"}, want: "a"},
		"nothing joins to empty": {xs: nil, want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Join(tc.xs))
		})
	}
}

// TestStateCoverAtBound covers AtBound (state.go:811): the ready primary's
// withdrawn, take-ended card at the redeal bound names itself. The refusals
// are one redeal short, a take that did not end, a primary not ready, and no
// card at the attempt.
func TestStateCoverAtBound(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build func(s *State)
		want  string
	}{
		"at the bound the card names itself": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", State: Ready, Attempt: 1}
				s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Place: FWithdrawn, TakeEnded: true, Redeals: MaxRedeals}
			},
			want: "p.w1",
		},
		"one redeal short is not at the bound": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", State: Ready, Attempt: 1}
				s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Place: FWithdrawn, TakeEnded: true, Redeals: MaxRedeals - 1}
			},
		},
		"a withdrawn card whose take did not end is not at the bound": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", State: Ready, Attempt: 1}
				s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Place: FWithdrawn, Redeals: MaxRedeals}
			},
		},
		"a primary not ready is not at the bound": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", State: Review, Attempt: 1}
				s.Work[WC("p", 1)] = WorkCard{Primary: "p", Attempt: 1, Place: FWithdrawn, TakeEnded: true, Redeals: MaxRedeals}
			},
		},
		"no card at the attempt is no bound": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", State: Ready, Attempt: 2}
			},
		},
		"a stranger is at no bound": {
			build: func(s *State) {},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			assert.Equal(t, tc.want, s.AtBound("p"))
		})
	}
}

// TestStateCoverAcceptHeld covers AcceptHeld (state.go:826): why the
// machine's accept leaves an acceptable primary in review for the
// coordinator: its CI red at its head, or returned to review at its
// attempt. The refusals are both held at the wrong attempt: the machine
// accepts.
func TestStateCoverAcceptHeld(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		pr   Primary
		want string
	}{
		"ci red at its head": {
			pr:   Primary{CI: "red", CIHead: 2, Head: 2},
			want: "ci red at its head",
		},
		"returned at its attempt": {
			pr:   Primary{ReturnedAt: 3, Attempt: 3},
			want: "returned at its attempt",
		},
		"red ci at an older head does not hold it": {
			pr: Primary{CI: "red", CIHead: 1, Head: 2},
		},
		"returned at an older attempt does not hold it": {
			pr: Primary{ReturnedAt: 2, Attempt: 3},
		},
		"a primary never held": {
			pr: Primary{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			s.Primaries["p"] = tc.pr
			assert.Equal(t, tc.want, s.AcceptHeld("p"))
		})
	}
}

// TestStateCoverAcceptNote covers acceptNote (state.go): a step that leaves
// an acceptable primary in review opens the ready to accept judgment only
// when the pump holds it (AcceptHeld). The refusals are a machine, RUNNING or
// STOPPED, whose tick takes it (since 2026-10-06: the tick accepts, no hand
// step), a primary not in review or not acceptable, and the returned judgment
// already open on it.
func TestStateCoverAcceptNote(t *testing.T) {
	t.Parallel()
	acceptableReview := func(s *State) {
		s.Primaries["p"] = Primary{Stream: "s", State: Review, Head: 1}
		coverRead(s, "p", 1, "r1", OK)
		coverRead(s, "p", 1, "r2", OK)
	}
	for name, tc := range map[string]struct {
		build func(s *State)
		want  []Judgment
	}{
		"a stopped machine whose tick takes it is refused": {
			build: acceptableReview,
		},
		"a stopped machine holding it notes it": {
			build: func(s *State) {
				acceptableReview(s)
				pr := s.Primaries["p"]
				pr.CI, pr.CIHead = "red", pr.Head
				s.Primaries["p"] = pr
			},
			want: []Judgment{{JAccept, "p"}},
		},
		"a running machine holding it notes it": {
			build: func(s *State) {
				s.Machine = Running
				acceptableReview(s)
				pr := s.Primaries["p"]
				pr.Attempt, pr.ReturnedAt = 3, 3
				s.Primaries["p"] = pr
			},
			want: []Judgment{{JAccept, "p"}},
		},
		"a running machine that takes it is refused": {
			build: func(s *State) {
				s.Machine = Running
				acceptableReview(s)
			},
		},
		"a primary not in review is refused": {
			build: func(s *State) {
				acceptableReview(s)
				pr := s.Primaries["p"]
				pr.State = Ready
				s.Primaries["p"] = pr
			},
		},
		"a primary not acceptable is refused": {
			build: func(s *State) {
				s.Primaries["p"] = Primary{Stream: "s", State: Review, Head: 1}
				coverRead(s, "p", 1, "r1", OK)
			},
		},
		"the returned judgment open already refuses a new note": {
			build: func(s *State) {
				acceptableReview(s)
				s.Open[Judgment{JReturned, "p"}] = true
			},
			want: []Judgment{{JReturned, "p"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := coverState()
			tc.build(&s)
			s.acceptNote("p")
			if len(tc.want) > 0 {
				assert.Contains(t, s.Open, tc.want[0], "the judgment it expects is open")
			}
			assert.Len(t, s.Open, len(tc.want), "the notes it leaves open")
		})
	}
}
