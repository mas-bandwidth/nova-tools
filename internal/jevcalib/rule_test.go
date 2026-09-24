package jevcalib

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

func readRows(t *testing.T, name string) []Row {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := ReadRows(f)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestTuningRuleOnFixtureDistribution is #2536's DONE-WHEN: the rule
// recomputes the 65 recorded pairs, adopts only the x3 candidate that meets
// every bar, refuses each named break, holds the confidence-bounded line at
// the boundary table, and scores today's head-matched set (397 pull requests
// at the friends' exact heads, three prompts) to the numbers the tuning
// ledger printed.
func TestTuningRuleOnFixtureDistribution(t *testing.T) {
	t.Run("pairs-2026-09-21", func(t *testing.T) {
		s := Measure(Join(readRows(t, "pairs-2026-09-21.tsv"), "cold0921"))
		if s.N != 65 || s.Within1 != 47 || s.FalsePass != 6 || s.FalseFail != 7 {
			t.Fatalf("n=%d within1=%d falsepass=%d falsefail=%d, want 65 47 6 7", s.N, s.Within1, s.FalsePass, s.FalseFail)
		}
	})

	x3 := readRows(t, "pairs-x3.tsv")
	inc := Join(x3, "cold0921")
	if _, h := Split(inc); len(h) < MinHoldout {
		t.Fatalf("the x3 incumbent holds out %d pairs, want >= %d", len(h), MinHoldout)
	}
	cases := []struct {
		cand      string
		exemplars []string
		word      string
		reason    string
		exit      int
	}{
		{"ok", nil, Adopt, "", 0},
		{"fpbound", nil, Refuse, "falsepass-bound", 3},
		{"small", nil, Refuse, "holdout-lt-60", 4},
		{"exemplar", []string{"mas-bandwidth/nova-tools#100003"}, Refuse, "exemplar-in-holdout", 3},
		{"ffabove", nil, Refuse, "falsefail-above-incumbent", 3},
		{"inverse", nil, Refuse, "inverse", 3},
	}
	adopted := 0
	for _, c := range cases {
		c := c
		t.Run("x3/"+c.cand, func(t *testing.T) {
			v := Decide(Join(x3, c.cand), inc, c.exemplars)
			t.Log(v.Line(c.cand, "cold0921"))
			if v.Word != c.word || v.Reason != c.reason || v.Exit != c.exit {
				t.Fatalf("verdict=%s reason=%s exit=%d, want %s %s %d", v.Word, v.Reason, v.Exit, c.word, c.reason, c.exit)
			}
			if v.Word == Adopt {
				adopted++
			}
		})
	}
	if adopted != 1 {
		t.Fatalf("%d x3 candidates adopted, want exactly one", adopted)
	}

	for _, b := range readBound(t) {
		b := b
		t.Run(fmt.Sprintf("bound/%d_%d", b.holdout, b.fp), func(t *testing.T) {
			cand, inc := boundPairs(b.holdout, b.fp)
			v := Decide(cand, inc, nil)
			got := "hold"
			if v.Reason == "falsepass-bound" {
				got = "falsepass-bound"
			}
			t.Log(v.Line("bound", "bound-inc"))
			if got != b.want {
				t.Fatalf("holdout=%d falsepass=%d: fp_ub=%.4f%% reason=%s, want %s", b.holdout, b.fp, 100*v.Cand.FPUpper, v.Reason, b.want)
			}
			if b.want == "hold" && v.Word != Adopt {
				t.Fatalf("a candidate inside the bound and better than the incumbent is %s %s, want ADOPT", v.Word, v.Reason)
			}
		})
	}

	// Today's set: nova-decide review --dry-run --pr-dir over 397 cached pull
	// requests at the friends' exact heads (iterations 1, 4 and 6 of the
	// tuning ledger). The numbers are the whole set, both sides.
	today := readRows(t, "pairs-2026-09-24.tsv")
	want := map[string][4]int{ // n, within1, falsepass, falsefail
		"fd94795e": {395, 133, 4, 261},
		"a2eaad4f": {395, 164, 4, 252},
		"6b7343c3": {395, 194, 13, 236},
	}
	for p8, w := range want {
		p8, w := p8, w
		t.Run("pairs-2026-09-24/"+p8, func(t *testing.T) {
			s := Measure(Join(today, p8))
			if got := [4]int{s.N, s.Within1, s.FalsePass, s.FalseFail}; got != w {
				t.Fatalf("n, within1, falsepass, falsefail = %v, want %v", got, w)
			}
		})
	}
	t.Run("pairs-2026-09-24/rule", func(t *testing.T) {
		v := Decide(Join(today, DefaultSha8), Join(today, SeedSha8), nil)
		t.Log(v.Line(DefaultSha8, SeedSha8))
		// The tuned prompt is not a landing reader: on the 133 held-out heads
		// it passes (8+) two the friends held against the seed's fewer, so
		// the rule refuses it even though its bound (4.66%) is under 5%.
		if v.Word != Refuse || v.Reason != "falsepass-above-incumbent" || v.Holdout != 133 {
			t.Fatalf("verdict=%s reason=%s holdout=%d, want REFUSE falsepass-above-incumbent on 133", v.Word, v.Reason, v.Holdout)
		}
	})
}

type boundRow struct {
	holdout, fp int
	want        string
}

func readBound(t *testing.T) []boundRow {
	t.Helper()
	raw, err := os.ReadFile("testdata/bound.tsv")
	if err != nil {
		t.Fatal(err)
	}
	var out []boundRow
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if i == 0 {
			if line != "holdout\tfalsepass\twant" {
				t.Fatalf("bound.tsv header %q", line)
			}
			continue
		}
		f := strings.Split(line, "\t")
		h, _ := strconv.Atoi(f[0])
		k, _ := strconv.Atoi(f[1])
		out = append(out, boundRow{h, k, f[2]})
	}
	if len(out) != 5 {
		t.Fatalf("bound.tsv has %d rows, want 5", len(out))
	}
	return out
}

// boundPairs is a held-out set of n pairs with k false passes (Jev 9 on a
// friend HOLD 5), and an incumbent that is the same set with one more false
// pass, so only the bound can refuse the candidate.
func boundPairs(n, k int) (cand, inc []Pair) {
	num := 0
	for len(cand) < n {
		num++
		if !Held("bound", num) {
			continue
		}
		i := len(cand)
		p := Pair{Repo: "bound", PR: num, Head: fmt.Sprint(num), Jev: 9, FriendScore: 9, Land: true}
		q := p
		switch {
		case i < k:
			p.FriendScore, p.Land = 5, false
			q = p
		case i == k:
			p.Jev, p.FriendScore, p.Land = 3, 3, false
			q = p
			q.Jev = 9
		case i < k+6:
			p.Jev, p.FriendScore, p.Land = 3, 3, false
			q = p
		}
		cand, inc = append(cand, p), append(inc, q)
	}
	return cand, inc
}
