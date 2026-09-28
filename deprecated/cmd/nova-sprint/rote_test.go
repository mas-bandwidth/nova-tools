package main

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestRote is the DONE-WHEN of nova-tools #3110 (#2756 v6 4.11, 11.9): three
// `note --rote` lines of one class and two of another print the first as the
// top class, with counts 3 and 2. A control-verb receipt on cap:log is counted
// by verb for its mind and is not hand work.
func TestRote(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	addr := mr.Addr()
	for _, n := range []struct{ class, mech string }{
		{"issue-body-repair", ""},
		{"width-check", ""},
		{"issue-body-repair", "#3089"},
		{"width-check", "#3090"},
		{"issue-body-repair", ""},
	} {
		args := []string{"note", "--rote", n.class, "--as", "rowan", "--store", addr, "--what", "by hand"}
		if n.mech != "" {
			args = append(args, "--mech", n.mech)
		}
		code, stdout, stderr := runSprint(args...)
		if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "NOTE ROTE mind=rowan class="+n.class+" id=") {
			t.Fatalf("note %v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.XAdd(context.Background(), &redis.XAddArgs{Stream: "cap:log",
		Values: []string{"kind", "friend", "verb", "capacity", "actor", "rowan", "to", "16"}}).Err(); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSprint("rote", "--store", addr)
	if code != 0 || stderr != "" {
		t.Fatalf("rote: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{
		"ROTE VERB mind=rowan verb=capacity n=1\n",
		"ROTE NOTE mind=rowan class=issue-body-repair n=3 mech=#3089\n",
		"ROTE NOTE mind=rowan class=width-check n=2 mech=#3090\n",
		"ROTE MIND mind=rowan verbs=1 notes=5 share=83% top=issue-body-repair top_n=3\n",
		"ROTE NEXT class=issue-body-repair n=3 minds=1 mech=#3089\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("rote lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Index(stdout, "class=issue-body-repair n=3") > strings.Index(stdout, "class=width-check n=2") {
		t.Errorf("the top class prints first:\n%s", stdout)
	}
}

// A note without a class or a mind, or with a class that is not a class
// name, refuses and writes nothing; `note` without --rote refuses.
func TestRoteNoteRefuses(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	for _, args := range [][]string{
		{"note", "--rote", "width-check", "--store", mr.Addr()},
		{"note", "--as", "rowan", "--store", mr.Addr()},
		{"note", "--rote", "Width Check", "--as", "rowan", "--store", mr.Addr()},
		{"note", "--rote", "width-check", "--as", "rowan"},
		{"rote", "--since", "yesterday", "--store", mr.Addr()},
	} {
		code, stdout, stderr := runSprint(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "run: nova-sprint help") {
			t.Errorf("%v: exit %d stdout %q stderr %q, want a refusal", args, code, stdout, stderr)
		}
	}
	if mr.Exists("rote:log") {
		t.Fatalf("a refused note wrote rote:log")
	}
}

// --since keeps only the notes at or after the time; an empty window prints
// the NEXT line with no class rather than nothing.
func TestRoteSince(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	if code, _, stderr := runSprint("note", "--rote", "width-check", "--as", "stella", "--store", mr.Addr()); code != 0 {
		t.Fatalf("note: %s", stderr)
	}
	code, stdout, stderr := runSprint("rote", "--store", mr.Addr(), "--since", "2999-01-01T00:00:00Z")
	if code != 0 || stderr != "" {
		t.Fatalf("rote --since: exit %d %q", code, stderr)
	}
	if stdout != "ROTE NEXT class=- n=0 minds=0 mech=-\n" {
		t.Fatalf("rote --since in the future: %q", stdout)
	}
	code, stdout, _ = runSprint("rote", "--store", mr.Addr(), "--since", "2000-01-01T00:00:00Z")
	if code != 0 || !strings.Contains(stdout, "ROTE MIND mind=stella verbs=0 notes=1 share=100% top=width-check top_n=1\n") {
		t.Fatalf("rote --since in the past: exit %d %q", code, stdout)
	}
}
