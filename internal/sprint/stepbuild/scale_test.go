package stepbuild

import (
	"fmt"
	"testing"
)

// The size gate of the builder: 100,000 members of 1 KiB of fields are cut
// into the steps the bounds make of them. This file asserts counts and the
// exactness of what it cuts, never a time: the waits class test refuses a
// test that asserts elapsed time, and rule 4 a bound under ten seconds. The
// time is what BenchmarkBuild100kMembersOfOneKiB measures
// (go test -run '^$' -bench Build100k ./internal/sprint/stepbuild/).

// kibMembers is one move entry of n members, each with 1 KiB of fields; the
// value is one string shared by every member.
func kibMembers(n int) Entry {
	val := pad(1024)
	e := mv("work", names("m", n))
	e.Each = make([]map[string]string, n)
	for i := range e.Each {
		e.Each[i] = map[string]string{"f": val}
	}
	return e
}

func TestAHundredThousandMembersOfOneKiBAreCutIntoFiftySteps(t *testing.T) {
	t.Parallel()
	const n = 100000
	steps := must(t, cfg(), []Entry{kibMembers(n)})
	// 2,000 candidates a step is the bound that holds: a step's 2,000 members
	// are 2.1 MB of request, three generated lines of under 1 MiB and 4.7 MB
	// of planned argv bytes.
	if len(steps) != n/LimitCandidates {
		t.Fatalf("%d steps, want %d", len(steps), n/LimitCandidates)
	}
	next := 0
	for i, s := range steps {
		if s.Part != i+1 || s.Parts != len(steps) || s.Bytes > LimitRequestBytes {
			t.Fatalf("step %d: part %d of %d, %d bytes", i+1, s.Part, s.Parts, s.Bytes)
		}
		members := 0
		for _, p := range s.Entries {
			for _, id := range p.IDs {
				if want := fmt.Sprintf("m%d", next); id != want {
					t.Fatalf("step %d: member %q where %q is next", i+1, id, want)
				}
				next++
				members++
			}
		}
		if members != LimitCandidates || len(s.Entries) != 3 {
			t.Fatalf("step %d: %d members in %d wire entries", i+1, members, len(s.Entries))
		}
	}
	if next != n {
		t.Fatalf("%d members placed of %d", next, n)
	}
	// Three steps, the first, one in the middle and the last, counted from
	// their encoded bytes.
	for _, i := range []int{0, len(steps) / 2, len(steps) - 1} {
		raw := steps[i].Encode()
		if len(raw) != steps[i].Bytes {
			t.Fatalf("step %d: Bytes %d, encoded %d", i+1, steps[i].Bytes, len(raw))
		}
		if bad := measure(t, raw).within(Contract()); len(bad) > 0 {
			t.Fatalf("step %d: %v", i+1, bad)
		}
	}
	if last := steps[len(steps)-1].Cursor; last.Done != n || last.ID != fmt.Sprintf("m%d", n-1) {
		t.Fatalf("the last cursor: %+v", last)
	}
}

func BenchmarkBuild100kMembersOfOneKiB(b *testing.B) {
	entries := []Entry{kibMembers(100000)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		steps, err := Build(cfg(), entries)
		if err != nil || len(steps) != 50 {
			b.Fatalf("%d steps, %v", len(steps), err)
		}
	}
}

func BenchmarkBuild100kSmallMembers(b *testing.B) {
	entries := []Entry{mv("work", names("m", 100000))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Build(cfg(), entries); err != nil {
			b.Fatal(err)
		}
	}
}
