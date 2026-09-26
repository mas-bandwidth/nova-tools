//go:build functional

package stream

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func build(f *fixture, dir, test string) Build {
	return Build{Repo: repo, Remote: f.URL, Base: "dev", Branch: "stream/landing-streams-lander",
		Workdir: filepath.Join(dir, "clone"), Test: test, TestTimeout: time.Minute, Log: &bytes.Buffer{}}
}

func members(f *fixture, ns ...int) []Member {
	var out []Member
	for _, n := range ns {
		out = append(out, Member{Task: "t" + string(rune('0'+n)), Stream: strm, N: n, Head: f.Head[n], Who: "rowan", Score: 10})
	}
	return out
}

func TestBuildParksTheRedMemberByBisect(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	b := build(f, t.TempDir(), testCmd)
	res, err := b.Run(context.Background(), members(f, 1, 2, 5))
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict != nil || res.BaseRed {
		t.Fatalf("result %+v", res)
	}
	if len(res.Kept) != 2 || res.Kept[0].N != 1 || res.Kept[1].N != 5 {
		t.Fatalf("kept %+v, want #1 #5", res.Kept)
	}
	if len(res.Parked) != 1 || res.Parked[0].N != 2 || res.Parked[0].Why != "red:batch-test" {
		t.Fatalf("parked %+v, want #2 red", res.Parked)
	}
	// batch red, base green, then one test per member
	if res.Tests != 5 || res.BaseSHA != f.Base {
		t.Fatalf("tests=%d base=%s", res.Tests, res.BaseSHA)
	}
	// The branch is the base plus two --no-ff merges, oldest first.
	log := gitT(t, b.Workdir, "log", "--format=%s", "--first-parent", res.BaseSHA+"..HEAD")
	if log != "Merge "+repo+"#5 at "+f.Head[5][:8]+" into stream/landing-streams-lander (read rowan 10)\nMerge "+repo+"#1 at "+f.Head[1][:8]+" into stream/landing-streams-lander (read rowan 10)" {
		t.Fatalf("log:\n%s", log)
	}
	if err := b.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.branchSHA("stream/landing-streams-lander"); got != res.Head {
		t.Fatalf("pushed %s, head %s", got, res.Head)
	}
}

func TestBuildStopsOnAConflict(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	b := build(f, t.TempDir(), testCmd)
	res, err := b.Run(context.Background(), members(f, 1, 3, 4, 5))
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict == nil || res.Conflict.Member.N != 4 || strings.Join(res.Conflict.Files, ",") != "a.txt" {
		t.Fatalf("conflict %+v, want #4 a.txt", res.Conflict)
	}
	if res.Tests != 0 || res.Head != "" {
		t.Fatalf("a conflicted build tested or finished: %+v", res)
	}
	// The merge was aborted: the tree is clean for Rowan to resolve.
	if st := gitT(t, b.Workdir, "status", "--porcelain"); st != "" {
		t.Fatalf("dirty after abort: %q", st)
	}
}

func TestBuildRedBaseStops(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	res, err := build(f, t.TempDir(), "false").Run(context.Background(), members(f, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !res.BaseRed || len(res.Kept) != 0 || len(res.Parked) != 0 || res.Tests != 2 {
		t.Fatalf("result %+v", res)
	}
}

func TestBuildSkipsAMovedHead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ms := members(f, 1, 5)
	ms[0].Head = strings.Repeat("e", 40) // the read was at another head
	res, err := build(f, t.TempDir(), testCmd).Run(context.Background(), ms)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 1 || res.Moved[0].N != 1 || len(res.Kept) != 1 || res.Kept[0].N != 5 {
		t.Fatalf("result %+v", res)
	}
}

func TestBuildTestTimeoutIsRed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	b := build(f, t.TempDir(), "sleep 30")
	b.TestTimeout = 200 * time.Millisecond
	green, line, err := func() (bool, string, error) {
		if _, err := b.Run(context.Background(), nil); err != nil {
			return false, "", err
		}
		return b.test(context.Background())
	}()
	if err != nil || green || !strings.HasPrefix(line, "timeout") {
		t.Fatalf("green=%t line=%q err=%v", green, line, err)
	}
}
