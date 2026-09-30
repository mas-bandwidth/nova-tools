package sprintfn

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// resultKind names which of a result's slots is set.
func resultKind(r Result) string {
	switch {
	case r.Err != nil:
		return "err"
	case r.Refusal != nil:
		return "refusal:" + r.Refusal.Code
	case r.Step != nil:
		return "step"
	case r.Read != nil:
		return "read"
	case r.Page != nil:
		return "page"
	}
	return "none"
}

func pagePlan() *tset.ReadPlan {
	return &tset.ReadPlan{Epoch: "0", Mode: "page", Queries: []tset.ReadQuery{{Kind: "lines", AfterSeq: "0", Limit: 100}}}
}

// TestPipelineAligned: a pipeline of steps, reads and pages, one of them
// refused, comes back one result a slot, in order, on the store's client (one
// flush) and on the twin (each item atomic alone, in order: the read and the
// page see the steps before them and not the one after).
func TestPipelineAligned(t *testing.T) {
	t.Parallel()
	count := &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{{Kind: "count", Table: sprint.Work, Cells: []string{"s1:waiting"}}}}
	items := []Item{{Step: seedRequest()}, {Step: moveRequest("ready", "working")}, {Read: count}, {Page: pagePlan()},
		{Step: moveRequest("waiting", "ready")}}
	want := []string{"step", "refusal:PLACE", "read", "page", "step"}

	t.Run("redis", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConn{replies: []fakeReply{{val: okEnvelope("1", "2", 2, `{}`)}, {val: refusedPlace}, {val: countReply},
			{val: pageReply}, {val: okEnvelope("3", "3", 1, `{}`)}}}
		results, err := newRedisWithClient(fake, testNames).Pipeline(context.Background(), items)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(results))
		for i, r := range results {
			got[i] = resultKind(r)
		}
		if !reflect.DeepEqual(got, want) || fake.pipelines != 1 || fake.execs != 1 {
			t.Fatalf("results %v (flushes %d), want %v in one flush", got, fake.execs, want)
		}
		fns := []string{fnStep, fnStep, fnRead, fnRead, fnStep}
		for i, c := range fake.calls {
			if c.fn != fns[i] || c.ro != (fns[i] == fnRead) {
				t.Fatalf("call %d is %s (ro %v), want %s", i, c.fn, c.ro, fns[i])
			}
		}
		if results[4].Step.Reply.FirstSeq != "3" {
			t.Fatalf("the last slot holds %+v", results[4].Step.Reply)
		}
	})

	t.Run("twin", func(t *testing.T) {
		t.Parallel()
		tw, _, _ := newTestTwin(t, passX())
		results, err := tw.Pipeline(context.Background(), items)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(results))
		for i, r := range results {
			got[i] = resultKind(r)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("results %v, want %v", got, want)
		}
		if results[2].Read.Tset[0].Sum != 2 || len(results[3].Page.Items) != 2 || results[4].Step.Reply.FirstSeq != "3" {
			t.Fatalf("read %+v, page %d lines, last step %+v", results[2].Read.Tset[0], len(results[3].Page.Items), results[4].Step.Reply)
		}
	})
}

// TestPartsRegistryRefusesDuplicate: a part name registers once; a second
// registration of it, a name that is not one of 1.0's parts, and a nil part
// are refused, and RegisterPart panics on each rather than start a build
// with two writers of one part.
func TestPartsRegistryRefusesDuplicate(t *testing.T) {
	t.Parallel()
	p := PartFuncs{PreFunc: func(*State, *Request, *Before) (any, *Refusal) { return nil, nil },
		CmdsFunc: func(*State, any, LogPlan) ([]Cmd, *Refusal) { return nil, nil }}
	r := NewPartRegistry()
	if err := r.Register(PartLease, p); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(PartLease, p); !errors.Is(err, ErrPartTwice) {
		t.Fatalf("a second lease: %v, want ErrPartTwice", err)
	}
	if err := r.Register("tickend", p); !errors.Is(err, ErrPartUnknown) {
		t.Fatalf("a part 1.0 does not have: %v, want ErrPartUnknown", err)
	}
	if err := r.Register(PartPop, nil); !errors.Is(err, ErrPartNil) {
		t.Fatalf("a nil part: %v, want ErrPartNil", err)
	}
	if got, ok := r.Lookup(PartLease); !ok || got == nil {
		t.Fatal("the registered lease is not found")
	}
	if _, ok := r.Lookup(PartPop); ok {
		t.Fatal("a refused registration was kept")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("registering a part twice did not panic")
		}
	}()
	mustRegister(r, PartLease, p)
}

// nilPointerPart, nilMapPart and nilFuncPart are parts whose methods a nil
// receiver cannot serve.
type nilPointerPart struct{ p PartFuncs }

func (n *nilPointerPart) Pre(st *State, req *Request, obs *Before) (any, *Refusal) {
	return n.p.Pre(st, req, obs)
}

func (n *nilPointerPart) Cmds(st *State, plan any, lp LogPlan) ([]Cmd, *Refusal) {
	return n.p.Cmds(st, plan, lp)
}

type nilMapPart map[string]bool

func (nilMapPart) Pre(*State, *Request, *Before) (any, *Refusal) { return nil, nil }
func (nilMapPart) Cmds(*State, any, LogPlan) ([]Cmd, *Refusal)   { return nil, nil }

type nilFuncPart func()

func (nilFuncPart) Pre(*State, *Request, *Before) (any, *Refusal) { return nil, nil }
func (nilFuncPart) Cmds(*State, any, LogPlan) ([]Cmd, *Refusal)   { return nil, nil }

// TestPartsRegistryRefusesTypedNil: a part that cannot be called is refused,
// not only the nil interface: an interface holding a nil pointer, map or
// function, a nil *PartFuncs, and a PartFuncs missing either function. Each
// would panic inside a step, after the twin's Mem had written. The refusal is
// ErrPartNil, nothing is registered, and a real part still registers after.
func TestPartsRegistryRefusesTypedNil(t *testing.T) {
	t.Parallel()
	whole := PartFuncs{PreFunc: func(*State, *Request, *Before) (any, *Refusal) { return nil, nil },
		CmdsFunc: func(*State, any, LogPlan) ([]Cmd, *Refusal) { return nil, nil }}
	cases := map[string]Part{
		"a nil interface":          nil,
		"a typed nil pointer":      (*nilPointerPart)(nil),
		"a nil *PartFuncs":         (*PartFuncs)(nil),
		"a nil map":                nilMapPart(nil),
		"a nil function":           nilFuncPart(nil),
		"a PartFuncs without Pre":  PartFuncs{CmdsFunc: whole.CmdsFunc},
		"a PartFuncs without Cmds": PartFuncs{PreFunc: whole.PreFunc},
		"an empty PartFuncs":       PartFuncs{},
		"a *PartFuncs without Pre": &PartFuncs{CmdsFunc: whole.CmdsFunc},
	}
	for name, p := range cases {
		r := NewPartRegistry()
		if err := r.Register(PartLease, p); !errors.Is(err, ErrPartNil) {
			t.Errorf("%s: Register returned %v, want ErrPartNil", name, err)
		}
		if _, ok := r.Lookup(PartLease); ok {
			t.Errorf("%s: the refused part was kept", name)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: RegisterPart did not panic", name)
				}
			}()
			mustRegister(r, PartLease, p)
		}()
	}
	// A non-nil map or function is a part: only nil is refused.
	r := NewPartRegistry()
	for name, p := range map[string]Part{"a real PartFuncs": whole, "a pointer to a real PartFuncs": &whole,
		"an empty map that is not nil": nilMapPart{}, "a real pointer part": &nilPointerPart{p: whole}} {
		r = NewPartRegistry()
		if err := r.Register(PartLease, p); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
