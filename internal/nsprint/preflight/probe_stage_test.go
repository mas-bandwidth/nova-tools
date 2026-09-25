package preflight

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// probeStages are the stages a probe card passes on its way to dev (#2946),
// in order; putProbe writes a probe's state up to and including one of them.
var probeStages = []string{"cut", "run", "pr", "ci", "read", "land"}

// putProbe writes probe label of sprint s1 through stage upTo: the card
// record (cut), its end (run), its PR and head (pr), ci green at the head
// (ci), a typed non-author read at the head (read) and the landed index
// (land). "" writes nothing. The head is per probe so no probe's CI or read
// can stand in for another's.
func putProbe(t *testing.T, c *redis.Client, label, upTo string) {
	t.Helper()
	if upTo == "" {
		return
	}
	ctx := context.Background()
	head := strings.Repeat(label[len(label)-1:], 8) + strings.Repeat("a", 32)
	card := "s:s1:card:" + label
	for _, st := range probeStages {
		var err error
		switch st {
		case "cut":
			err = c.HSet(ctx, card, "label", label, "repo", "mas-bandwidth/nova-tools", "where", "working").Err()
		case "run":
			err = c.HSet(ctx, card, "where", "done", "outcome", "DONE").Err()
		case "pr":
			err = c.HSet(ctx, card, "pr", "37"+label[len(label)-1:], "head", head).Err()
		case "ci":
			err = c.HSet(ctx, "ci:nova-tools:"+head, "ci", "green", "sha", head).Err()
		case "read":
			err = addReads(ctx, c, "pr:nova-tools:37"+label[len(label)-1:],
				"SCORE who=rowan-claude head="+head[:8]+" 9/10: one file, PATHS = CHANGED")
		case "land":
			err = c.SAdd(ctx, "s:s1:idx:card:landed", label).Err()
		}
		if err != nil {
			t.Fatal(err)
		}
		if st == upTo {
			return
		}
	}
}

// addReads appends typed lines to a pr record's reads field, newline-joined,
// as read post (ns_read_post) writes them.
func addReads(ctx context.Context, c *redis.Client, key string, lines ...string) error {
	old, err := c.HGet(ctx, key, "reads").Result()
	if err != nil && err != redis.Nil {
		return err
	}
	all := append(strings.Split(old, "\n"), lines...)
	if old == "" {
		all = lines
	}
	return c.HSet(ctx, key, "reads", strings.Join(all, "\n")).Err()
}

func probeInput(t *testing.T, c *redis.Client) LineupInput {
	t.Helper()
	ctx := context.Background()
	c.HSet(ctx, "s:s1", "status", "lining-up")
	if err := CutProbes(ctx, c, "s1", probes); err != nil {
		t.Fatal(err)
	}
	in, err := GatherLineup(ctx, c, "s1")
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// DONE-WHEN (#2946): any probe that misses is a RED line naming the stage it
// stopped at, and 7.20 is GREEN only when all five reach a merge on dev with
// ci:<repo>:<head> green and one typed non-author read at head. Here each
// probe stops at a different stage.
func TestProbeMissNamesTheStage(t *testing.T) {
	_, c := fixture(t)
	stops := map[string]string{"probe-1": "", "probe-2": "cut", "probe-3": "run", "probe-4": "pr", "probe-5": "ci"}
	for p, st := range stops {
		putProbe(t, c, p, st)
	}
	l := CheckProbes(probeInput(t, c))
	if !l.Red {
		t.Fatalf("7.20 GREEN with no probe landed: %v", l)
	}
	for _, want := range []string{
		"0 of 5 probes landed",
		"probe-1 RED at cut: no card s:s1:card:probe-1",
		"probe-2 RED at run: card where=working",
		"probe-3 RED at pr: card has no PR",
		"probe-4 RED at ci: ci:nova-tools:",
		"probe-5 RED at read: no typed non-author SCORE at head",
	} {
		if !strings.Contains(l.Why, want) {
			t.Fatalf("7.20 does not say %q:\n%s", want, l.Why)
		}
	}
	if !strings.Contains(l.Why, "=MISSING") {
		t.Fatalf("probe-4's ci miss does not say MISSING:\n%s", l.Why)
	}
}

// A red CI verdict at head is RED at ci (the 2026-09-25 quack-0925e shape:
// landed by hand, every card head red on the fleet's own CI).
func TestProbeLandedWithRedCIIsRed(t *testing.T) {
	_, c := fixture(t)
	for _, p := range probes {
		putProbe(t, c, p, "land")
	}
	in := probeInput(t, c)
	head := in.Trails["probe-3"].Head
	c.HSet(context.Background(), "ci:nova-tools:"+head, "ci", "red")
	l := CheckProbes(probeInput(t, c))
	if !l.Red || !strings.Contains(l.Why, "probe-3 RED at ci: ci:nova-tools:"+head[:8]+"=red") {
		t.Fatalf("a landed probe with red CI is not RED at ci: %v", l)
	}
	if !strings.Contains(l.Why, "4 of 5 probes landed") {
		t.Fatalf("tally: %v", l)
	}
}

// Jev lines, author lines, lines at another head and untyped lines are not a
// read; the landed index alone is not a landing.
func TestProbeReadMustBeTypedNonAuthorAtHead(t *testing.T) {
	_, c := fixture(t)
	ctx := context.Background()
	for _, p := range probes {
		putProbe(t, c, p, "land")
	}
	in := probeInput(t, c)
	head := in.Trails["probe-2"].Head
	k := "pr:nova-tools:372"
	c.HDel(ctx, k, "reads")
	c.HSet(ctx, k, "who", "stella")
	addReads(ctx, c, k,
		"SCORE who=jev head="+head[:8]+" 9/10",
		"SCORE who=stella head="+head[:8]+" 10/10",
		"SCORE who=emma head=0123456789 9/10",
		"looks fine to me, head="+head[:8]+" who=emma",
		"CLOSE who=rowan head="+head[:8]+" landed")
	l := CheckProbes(probeInput(t, c))
	if !l.Red || !strings.Contains(l.Why, "probe-2 RED at read: no typed non-author SCORE at head "+head[:8]) {
		t.Fatalf("a non-read counted as a read: %v", l)
	}
	addReads(ctx, c, k, "SCORE who=emma head="+head[:8]+" 9/10: fine")
	if l := CheckProbes(probeInput(t, c)); l.Red {
		t.Fatalf("a typed non-author read at head did not count: %v", l)
	}
	c.SRem(ctx, "s:s1:idx:card:landed", "probe-5")
	if l := CheckProbes(probeInput(t, c)); !l.Red || !strings.Contains(l.Why, "probe-5 RED at land: not in s:s1:idx:card:landed") {
		t.Fatalf("an unlanded probe with green CI and a read is not RED at land: %v", l)
	}
}

// Positive control: five probes through every stage are GREEN.
func TestProbeAllStagesGreen(t *testing.T) {
	_, c := fixture(t)
	for _, p := range probes {
		putProbe(t, c, p, "land")
	}
	if l := CheckProbes(probeInput(t, c)); l.Red || !strings.Contains(l.Why, "5 of 5 probes landed") {
		t.Fatalf("five landed probes with green CI and a read at head: %v", l)
	}
}
