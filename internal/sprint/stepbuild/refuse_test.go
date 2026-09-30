package stepbuild

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Rule 4: a member whose own fields exceed a bound cannot be cut. The build
// refuses, before any step is returned, naming the member and the bound.

// fat is n fields of size bytes each, named f0..f(n-1).
func fat(n, size int) map[string]string {
	m := map[string]string{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("f%d", i)] = pad(size)
	}
	return m
}

func TestAMemberWhoseFieldsExceedTheRequestRefusesTheBuild(t *testing.T) {
	t.Parallel()
	bad := mv("work", []string{"m0", "m1", "m2"})
	// m1 carries 128 fields of 64 KiB: 8 MiB, in a step bounded at 4.
	bad.Each = []map[string]string{nil, fat(128, LimitFieldValueBytes), nil}
	entries := []Entry{mv("work", names("a", 3)), bad, mv("merge", names("z", 3))}
	le := refused(t, cfg(), entries)
	if le.Bound != boundRequest.name || le.Section != "section 6" || le.Limit != LimitRequestBytes || le.Actual <= LimitRequestBytes {
		t.Fatalf("the bound: %+v", le)
	}
	if le.Entry != 1 || le.Member != "m1" || le.Table != "work" || le.Note != -1 {
		t.Fatalf("the member: %+v", le)
	}
	// The message carries the member and the bound and nothing of its values.
	msg := le.Error()
	for _, want := range []string{"entry 1", `member "m1"`, "encoded request bytes", "bound 4194304", "nothing was built"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "ppppp") {
		t.Errorf("the message carries a field value: %.200s", msg)
	}
	if !errors.Is(le, ErrLimit) {
		t.Errorf("a LimitError is not ErrLimit")
	}
}

func TestAMemberWhoseLineExceedsTheLineRefusesTheBuild(t *testing.T) {
	t.Parallel()
	// 128 fields of 8 KiB is 1 MiB of fields: a request of a quarter of the
	// bound, and a generated line past 1 MiB with the rest of the event.
	e := mv("work", []string{"only"})
	e.Set = fat(128, 8192)
	le := refused(t, cfg(), []Entry{e})
	if le.Bound != boundLineBytes.name || le.Member != "only" || le.Limit != LimitLineBytes || le.Actual <= LimitLineBytes {
		t.Fatalf("%+v", le)
	}
	// The same fields on a guard's before_fields cost the request only.
	g := gd("work", []string{"only"})
	if steps := must(t, cfg(), []Entry{g}); len(steps) != 1 {
		t.Fatalf("a guard: %d steps", len(steps))
	}
}

func TestSharedFieldsThatNoMemberCanCarryRefuseNamingTheFirstMember(t *testing.T) {
	t.Parallel()
	e := mv("work", names("m", 5))
	e.Set = fat(128, LimitFieldValueBytes) // 8 MiB in the shared head
	le := refused(t, cfg(), []Entry{mv("merge", []string{"ok"}), e})
	if le.Bound != boundRequest.name || le.Entry != 1 || le.Member != "m0" {
		t.Fatalf("%+v", le)
	}
}

func TestGuardsThatNoStepCanCarryRefuse(t *testing.T) {
	t.Parallel()
	// A guard is never cut: more members than an entry may hold is refused.
	owner := mv("work", []string{"m"})
	owner.Guards = []Entry{gd("merge", names("g", LimitEntryIDs+1))}
	le := refused(t, cfg(), []Entry{owner})
	if le.Bound != boundEntryIDs.name || le.Field != "guards[0].ids" || le.Actual != LimitEntryIDs+1 || le.Entry != 0 {
		t.Fatalf("%+v", le)
	}
	// Three guards of 2,000 are 6,000 guard-only members: no step holds them.
	owner.Guards = []Entry{gd("merge", names("a", 2000)), gd("merge", names("b", 2000)), gd("merge", names("c", 2000))}
	le = refused(t, cfg(), []Entry{owner})
	if le.Bound != boundGuardOnly.name || le.Member != "m" || le.Actual != 6000 {
		t.Fatalf("%+v", le)
	}
}

func TestANoteThatNoStepCanCarryRefuses(t *testing.T) {
	t.Parallel()
	le := refused(t, cfg(), []Entry{{Kind: KindNote, Notes: []Note{
		{Meta: map[string]string{"a": "b"}},
		{Meta: map[string]string{"big": pad(LimitLineBytes)}},
	}}})
	if le.Bound != boundLineBytes.name || le.Note != 1 || le.Entry != 0 || le.Field != "notes" {
		t.Fatalf("%+v", le)
	}
}

func TestARefusalLateInALargeInputReturnsNoStep(t *testing.T) {
	t.Parallel()
	bad := mv("work", []string{"bad"})
	bad.Set = fat(128, LimitFieldValueBytes)
	entries := []Entry{mv("work", names("m", 5000)), mv("merge", names("n", 3000)), bad}
	le := refused(t, cfg(), entries) // refused() fails on any returned step
	if le.Entry != 2 || le.Member != "bad" {
		t.Fatalf("%+v", le)
	}
}

func TestARefusalNamesTheSameFaultEveryRun(t *testing.T) {
	t.Parallel()
	// Two fields over the value bound: the first in byte order is named, on
	// every run, whatever order the map is walked in.
	e := mv("work", []string{"m"})
	e.Set = map[string]string{"zz": pad(LimitFieldValueBytes + 1), "aa": pad(LimitFieldValueBytes + 1), "mm": "ok"}
	first := refused(t, cfg(), []Entry{e})
	if first.Name != "aa" || first.Field != "set" {
		t.Fatalf("%+v", first)
	}
	for i := 0; i < 20; i++ {
		if again := refused(t, cfg(), []Entry{e}); *again != *first {
			t.Fatalf("run %d named %+v, the first %+v", i, again, first)
		}
	}
}

// The member no step can hold is found before any step is opened, so before
// the identity of any step is asked for: an Ident that allocates an op, or
// writes a manifest, is not called for steps of a build that is then refused.
// A note is asked of an empty step as a member is (fitAlone): a note that only
// a lowered bound of the step refuses, after thousands of members, is refused
// before the first of their steps is opened.
func TestAMemberNoStepCanHoldIsRefusedBeforeAnyIdentityIsAsked(t *testing.T) {
	t.Parallel()
	bad := mv("work", []string{"bad"})
	bad.Set = fat(128, LimitFieldValueBytes)
	rows := Entry{Kind: KindRows, Table: "work", Add: []string{"r"}, Guards: []Entry{gd("merge", names("a", 2000)), gd("merge", names("b", 2000)), gd("merge", names("c", 2000))}}
	note := Entry{Kind: KindNote, Notes: []Note{{Meta: map[string]string{"big": pad(LimitLineBytes)}}}}
	// A note whose own line is inside every bound of the contract, so that only
	// the lowered bound of the step refuses it, and only fitAlone sees it.
	lowered := func(mod func(*Bounds)) func(Config) Config {
		return func(c Config) Config {
			c.Bounds = Contract()
			mod(&c.Bounds)
			return c
		}
	}
	smallNote := Entry{Kind: KindNote, Notes: []Note{{Meta: map[string]string{"big": pad(60000)}, About: []string{"p"}}}}
	for _, tc := range []struct {
		name    string
		config  func(Config) Config
		entries []Entry
		member  string
		bound   string
		note    int
	}{
		{"a member with 8 MiB of fields", nil, []Entry{mv("work", names("m", 5000)), mv("merge", names("n", 3000)), bad}, "bad", boundRequest.name, -1},
		{"guards no step can hold on a rows entry", nil, []Entry{mv("work", names("m", 5000)), rows}, "r", boundGuardOnly.name, -1},
		{"a note too large for a line", nil, []Entry{mv("work", names("m", 5000)), note}, "", boundLineBytes.name, 0},
		{"a note over a lowered request bound, after 3,000 members", lowered(func(b *Bounds) { b.RequestBytes = 50000 }),
			[]Entry{mv("work", names("m", 3000)), smallNote}, "", boundRequest.name, 0},
		{"a note over a lowered argv bound, after 3,000 members", lowered(func(b *Bounds) { b.PlannedArgvBytes = 60000 }),
			[]Entry{mv("work", names("m", 3000)), smallNote}, "", boundArgv.name, 0},
		{"a note over a lowered note-line bound on the second of two notes", lowered(func(b *Bounds) { b.RequestBytes = 50000 }),
			[]Entry{mv("work", names("m", 3000)), {Kind: KindNote, Notes: []Note{{About: []string{"p"}}, smallNote.Notes[0]}}}, "", boundRequest.name, 1},
	} {
		var asked []int
		c := cfg()
		if tc.config != nil {
			c = tc.config(c)
		}
		c.Ident = func(part int) Ident {
			asked = append(asked, part)
			return Ident{Op: fmt.Sprint("op", part), Intent: "i"}
		}
		le := refused(t, c, tc.entries)
		if len(asked) != 0 || le.Member != tc.member || le.Bound != tc.bound || le.Note != tc.note {
			t.Errorf("%s: %+v, the identity was asked for parts %v", tc.name, le, asked)
		}
	}
}

// The one refusal that can follow a call of Ident: a member that fits a step
// with no identity and, with the receipt an op reserves, does not.
func TestAnIdentityThatLeavesAMemberNoRoomRefusesAfterItIsAsked(t *testing.T) {
	t.Parallel()
	e := mv("work", []string{"m"})
	e.Set = map[string]string{"f": pad(100)}
	// A bound of exactly what the member plans without a receipt, margin included.
	c := cfg()
	c.Bounds = Contract()
	c.Bounds.PlannedArgvBytes = charged(measure(t, must(t, c, []Entry{e})[0].Encode()).argv)
	if steps := must(t, c, []Entry{e}); len(steps) != 1 {
		t.Fatalf("%d steps", len(steps))
	}
	var asked []int
	c.Ident = func(part int) Ident {
		asked = append(asked, part)
		return Ident{Op: "op", Intent: "i"}
	}
	le := refused(t, c, []Entry{e})
	if le.Bound != boundArgv.name || le.Member != "m" || len(asked) != 1 {
		t.Fatalf("%+v, identity asked for parts %v", le, asked)
	}
}
