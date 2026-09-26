package ws_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// TestSplitPathsReadsAPathsLine pins the one reading of a PATHS line the
// stream compares (#4322): separators, quotes, parentheticals, markers,
// repo tags, ./ and trailing slashes, and a glob cut to its directory.
func TestSplitPathsReadsAPathsLine(t *testing.T) {
	t.Parallel()
	got := ws.SplitPaths("`cmd/a.go`, internal/b/; ./c.go (new) - none ~skip rowan-tools:bin/d internal/e/** internal/f/*.go *.md internal/b")
	want := "bin/d,c.go,cmd/a.go,internal/b,internal/e,internal/f"
	if ws.JoinPaths(got) != want {
		t.Fatalf("SplitPaths = %q; want %q", ws.JoinPaths(got), want)
	}
	if p := ws.ParsePaths(want); ws.JoinPaths(p) != want {
		t.Fatalf("ParsePaths round trip = %q", ws.JoinPaths(p))
	}
}

// TestPathsOverlapIsPrefixAtASlash: equal, or one a prefix of the other at
// a / boundary; a prefix inside a name is disjoint.
func TestPathsOverlapIsPrefixAtASlash(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		hit  bool
	}{
		{"internal/nsprint/ws", "internal/nsprint/ws", true},
		{"internal/nsprint/ws", "internal/nsprint/ws/check.go", true},
		{"internal", "internal/nsprint/ws/check.go", true},
		{"internal/nsprint/ws", "internal/nsprint/wsx", false},
		{"internal/nsprint/ws/a.go", "internal/nsprint/ws/b.go", false},
	} {
		if ws.PathsOverlap(c.a, c.b) != c.hit || ws.PathsOverlap(c.b, c.a) != c.hit {
			t.Errorf("PathsOverlap(%q, %q) = %v; want %v both ways", c.a, c.b, !c.hit, c.hit)
		}
	}
}

// TestPathsGateStreamsDisjoint is the invariant at push (#4322): a card
// whose PATHS overlap another open stream's is refused with the stream,
// the paths and the remedy named; --join <that stream> pushes it onto it;
// a parent dir of another stream's file is refused (the prefix rule); its
// own stream and disjoint paths pass.
func TestPathsGateStreamsDisjoint(t *testing.T) {
	t.Parallel()
	open := ws.StreamPaths{
		"work": {"internal/nsprint/ws"},
		"ci":   {"cmd/nova-sprint/ci.go"},
	}
	to, no := open.Gate("fleet", ws.SplitPaths("internal/nsprint/ws/paths.go"), "")
	want := `REFUSED PATHS overlap stream=work paths=internal/nsprint/ws,internal/nsprint/ws/paths.go remedy="--join work"`
	if no == nil || no.Receipt() != want || to != "" {
		t.Fatalf("overlap: to=%q refusal=%v; want %q", to, no, want)
	}
	if to, no := open.Gate("fleet", ws.SplitPaths("internal/nsprint/ws/paths.go"), "work"); no != nil || to != "work" {
		t.Fatalf("--join work: to=%q refusal=%v; want onto work", to, no)
	}
	to, no = open.Gate("fleet", ws.SplitPaths("cmd/nova-sprint/"), "")
	want = `REFUSED PATHS overlap stream=ci paths=cmd/nova-sprint,cmd/nova-sprint/ci.go remedy="--join ci"`
	if no == nil || no.Receipt() != want {
		t.Fatalf("parent dir: to=%q refusal=%v; want %q", to, no, want)
	}
	if to, no := open.Gate("work", ws.SplitPaths("internal/nsprint/ws/check.go"), ""); no != nil || to != "work" {
		t.Fatalf("own stream: to=%q refusal=%v; want work", to, no)
	}
	if to, no := open.Gate("fleet", ws.SplitPaths("internal/nsprint/card/push.go, internal/nsprint/wsx"), "ci"); no != nil || to != "fleet" {
		t.Fatalf("disjoint: to=%q refusal=%v; want fleet (a --join it does not overlap is not taken)", to, no)
	}
	// --join names one stream; an overlap with a second is still refused.
	_, no = open.Gate("fleet", ws.SplitPaths("internal/nsprint/ws cmd/nova-sprint/ci.go"), "work")
	if no == nil || no.Stream != "ci" {
		t.Fatalf("join work, overlap ci: refusal=%v; want stream=ci", no)
	}
	// A stream name with a space is one field, and the remedy pastes.
	spaced := ws.StreamPaths{"swarm: cards": {"a"}}
	if _, no := spaced.Gate("x", []string{"a/b"}, ""); no == nil ||
		no.Receipt() != `REFUSED PATHS overlap stream=swarm:\x20cards paths=a,a/b remedy="--join \"swarm: cards\""` {
		t.Fatalf("spaced stream: %v", no)
	}
}

// TestPathsOverlapsNamesEveryPair: ws check's PATHS OVERLAP lines, one per
// pair of streams sharing a path, in name order; Stale names a record that
// differs from its live cards.
func TestPathsOverlapsNamesEveryPair(t *testing.T) {
	t.Parallel()
	sp := ws.StreamPaths{"a": {"x"}, "b": {"x/y.go"}, "c": {"z"}}
	var lines []string
	for _, p := range sp.Overlaps() {
		lines = append(lines, p.Line())
	}
	if strings.Join(lines, "\n") != "PATHS OVERLAP stream=a other=b paths=x,x/y.go" {
		t.Fatalf("overlaps = %q", lines)
	}
	if got := ws.Stale(sp, ws.StreamPaths{"a": {"x"}, "b": {"q"}, "d": {"w"}}); strings.Join(got, ",") != "b,c,d" {
		t.Fatalf("stale = %q; want b,c,d", got)
	}
	sp.Add("c", []string{"z/q", "k"})
	if ws.JoinPaths(sp["c"]) != "k,z,z/q" {
		t.Fatalf("add = %q", sp["c"])
	}
}
