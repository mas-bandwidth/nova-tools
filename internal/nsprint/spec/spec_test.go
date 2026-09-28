package spec

import "testing"

func TestParseLine(t *testing.T) {
	t.Parallel()

	sha := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, c := range []struct {
		line string
		want Facts
		bad  bool
	}{
		{line: "SPEC who=stella rev=9 sha=" + sha + " score=10/10 gates=test:ok: fine", want: Facts{Who: "stella", Rev: 9, Score: 10}},
		{line: "SPEC who=rowan rev=r3 sha=" + sha + " score=7\nC1: gap -> work", want: Facts{Who: "rowan", Rev: 3, Score: 7}},
		{line: "SPEC who=emma rev=2 score=10 stream=nova-sprint", want: Facts{Who: "emma", Rev: 2, Score: 10, Stream: "nova-sprint"}},
		{line: "SPEC rev=2 score=10", bad: true},
		{line: "SPEC who=emma score=10", bad: true},
		{line: "SPEC who=emma rev=2", bad: true},
		{line: "SPEC who=emma rev=0 score=10", bad: true},
		{line: "SPEC who=emma rev=2 score=11", bad: true},
		{line: "SCORE who=emma rev=2 score=10", bad: true},
	} {
		got, err := ParseLine(c.line)
		if c.bad {
			if err == nil {
				t.Errorf("%q parsed as %+v; want refused", c.line, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q = %+v %v; want %+v", c.line, got, err, c.want)
		}
	}
}

func TestBlock(t *testing.T) {
	t.Parallel()

	got := Block([]Row{{Stream: "s1", Working: 2, Done: 1}})
	if got != "specs | working | done\ns1 | 2 | 1\n" {
		t.Fatalf("block = %q", got)
	}
}
