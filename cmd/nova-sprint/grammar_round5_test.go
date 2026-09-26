package main

// #4399 round 5: the reader's four findings at 7c119d2db, each a test at
// the door it was found at.

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestConsumeListRefusesEveryFlag (owed 2): consume list took --zz-bogus,
// --id and --actor with exit 0; it has its own flag set with no flags, so
// each is the grammar's refusal, exit 2, and the bare list still exits 0.
func TestConsumeListRefusesEveryFlag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ args, want string }{
		{"--zz-bogus", "--zz-bogus is not a flag of nova-sprint consume list; it takes no flags"},
		{"--id x", "--id is retired and nova-sprint consume list has no --ids"},
		{"--actor x", "--actor is retired and nova-sprint consume list has no --as"},
		{"extra", "takes no arguments"},
	} {
		args := append([]string{"consume", "list"}, strings.Fields(tc.args)...)
		code, out, errOut := runSprint(args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "nova-sprint consume list: "+tc.want) || !strings.Contains(errOut, "usage: nova-sprint consume list") {
			t.Errorf("%v: exit %d stdout %q stderr %q; want 2 and %q", args, code, out, errOut, tc.want)
		}
	}
	if code, out, errOut := runSprint("consume", "list"); code != 0 || !strings.HasPrefix(out, "ok-to-friend duty=reconcile ") || errOut != "" {
		t.Fatalf("consume list: exit %d stdout %q stderr %q", code, out, errOut)
	}
	if code, out, _ := runSprint("consume", "list", "-h"); code != 0 || !strings.HasPrefix(out, "usage: nova-sprint consume list\n") {
		t.Fatalf("consume list -h: exit %d %q", code, out)
	}
}

// TestUsageRefusalExitsTwoOnEveryCardPath (owed 4): card run and card
// launched exited 1 on an unknown flag while every other path exits 2; a
// usage refusal is exit 2 on each, with the grammar's line.
func TestUsageRefusalExitsTwoOnEveryCardPath(t *testing.T) {
	t.Parallel()
	for _, path := range [][]string{{"card", "run"}, {"card", "launched"}, {"card", "end", "--sprint", "s"}, {"card", "beat", "--sprint", "s"}, {"card", "cut"}, {"card", "end"}, {"task", "push"}} {
		args := append(append([]string{}, path...), "--zz-bogus")
		code, out, errOut := runSprint(args...)
		want := "--zz-bogus is not a flag of nova-sprint " + path[0] + " " + path[1]
		if code != 2 || out != "" || !strings.Contains(errOut, want) {
			t.Errorf("%v: exit %d stdout %q stderr %q; want 2 and %q", args, code, out, errOut, want)
		}
	}
	if code, _, errOut := runSprint("card", "run"); code != 2 || !strings.Contains(errOut, "nova-sprint card run: wants --sprint") {
		t.Errorf("card run with no flags: exit %d %q; want 2", code, errOut)
	}
}

// TestCardEndRepoIsSpelledCheckout (owed 4): `card end --repo <dir>` and
// `friend done --repo <dir>` were refused "not a flag" (exit 2); the
// refusal now names --checkout with the whole corrected line.
func TestCardEndRepoIsSpelledCheckout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"card", "end", "--redis", "127.0.0.1:1", "--ids", "x~1", "--ok", "--pr", "nova-tools#1", "--head", "abc", "--repo", "/tmp/co"},
			"--repo is spelled --checkout; run: nova-sprint card end --redis 127.0.0.1:1 --ids x~1 --ok --pr nova-tools#1 --head abc --checkout /tmp/co"},
		{[]string{"friend", "done", "--redis", "127.0.0.1:1", "--as", "friend:rowan", "--ids", "x~1", "--ok", "--pr", "nova-tools#1", "--head", "abc", "--repo", "/tmp/co"},
			"--repo is spelled --checkout; run: nova-sprint friend done --redis 127.0.0.1:1 --as friend:rowan --ids x~1 --ok --pr nova-tools#1 --head abc --checkout /tmp/co"},
	} {
		code, out, errOut := runSprint(tc.args...)
		if code != 2 || out != "" || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: exit %d stdout %q stderr %q; want 2 and %q", tc.args, code, out, errOut, tc.want)
		}
	}
}

// TestCardFieldNeverCollapsesAStoreFailure (Stella's audit, Rowan's
// clause): only redis.Nil is absent; WRONGTYPE and a dial failure come
// back naming HGET, the key and the inspect line.
func TestCardFieldNeverCollapsesAStoreFailure(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if v, err := cardField(ctx, c, "absent~1", "repo"); v != "" || err != nil {
		t.Fatalf("absent: %q %v, want \"\" and no error", v, err)
	}
	mr.HSet("task:has~1", "repo", "mas-bandwidth/nova-tools")
	if v, err := cardField(ctx, c, "has~1", "repo"); v != "mas-bandwidth/nova-tools" || err != nil {
		t.Fatalf("present: %q %v", v, err)
	}
	mr.Set("task:wrong~1", "wrong-type")
	_, err := cardField(ctx, c, "wrong~1", "repo")
	if err == nil || !strings.Contains(err.Error(), "HGET task:wrong~1 repo failed: WRONGTYPE") || !strings.Contains(err.Error(), "next: nova-sprint redis TYPE task:wrong~1") {
		t.Fatalf("WRONGTYPE: %v", err)
	}
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = dead.Close() })
	_, err = cardField(ctx, dead, "has~1", "repo")
	if err == nil || !strings.Contains(err.Error(), "HGET task:has~1 repo failed: dial tcp 127.0.0.1:1: connect: connection refused") || strings.Contains(err.Error(), "names no repo") {
		t.Fatalf("a dial failure: %v", err)
	}
}

// TestCardEndNumericPRLookupNamesTheStoreFailure (Stella's audit at the
// door): `card end --ids <copy> --ok --pr 7 --head <sha>` on a WRONGTYPE
// record said only "--pr wants the PR number of a card that names its
// repo"; it names the copy, the HGET and WRONGTYPE, and an absent repo is
// still its own refusal naming the key.
func TestCardEndNumericPRLookupNamesTheStoreFailure(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.Set("task:wrong~1", "wrong-type")
	mr.HSet("task:norepo~1", "where", "working")
	head := strings.Repeat("a", 40)
	code, out, errOut := runSprint("card", "end", "--redis", mr.Addr(), "--ids", "wrong~1", "--ok", "--pr", "7", "--head", head)
	if code != 2 || !strings.Contains(errOut, "nova-sprint card end: --pr 7 of wrong~1: HGET task:wrong~1 repo failed: WRONGTYPE") || !strings.Contains(errOut, "next: nova-sprint redis TYPE task:wrong~1") {
		t.Fatalf("WRONGTYPE: exit %d stdout %q stderr %q", code, out, errOut)
	}
	code, _, errOut = runSprint("card", "end", "--redis", mr.Addr(), "--ids", "norepo~1", "--ok", "--pr", "7", "--head", head)
	if code != 2 || !strings.Contains(errOut, "--pr 7: task:norepo~1 names no repo; pass --pr <repo>#<n>") {
		t.Fatalf("no repo: exit %d stderr %q", code, errOut)
	}
}
