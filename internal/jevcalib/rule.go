package jevcalib

import (
	"crypto/sha256"
	"fmt"
	"math"
)

// PassScore is the Jev score that counts as a pass in the rule: 8 or more,
// the friends' landable bar.
const PassScore = 8

// MinHoldout is the smallest held-out set the rule judges: below 59 even zero
// false passes cannot bound the rate under 5%.
const MinHoldout = 60

// Held reports whether a pull request is on the held-out side: the first byte
// of sha256("<repo>#<n>") mod 3 == 0. Every head of a PR lands on one side, and
// the split does not move as the set grows.
func Held(repo string, pr int) bool {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s#%d", repo, pr)))
	return sum[0]%3 == 0
}

// Split is the pairs on each side.
func Split(pairs []Pair) (train, holdout []Pair) {
	for _, p := range pairs {
		if Held(p.Repo, p.PR) {
			holdout = append(holdout, p)
		} else {
			train = append(train, p)
		}
	}
	return train, holdout
}

// Stats are the numbers the rule reads.
type Stats struct {
	N, Within1, FalsePass, FalseFail int
	NotLand                          int     // pairs the friends did not land
	MeanMiss                         float64 // mean |jev - friend|
	MeanLand, MeanHold               float64 // mean Jev score on land and on hold pairs
	FPUpper                          float64 // one-sided 95% Clopper-Pearson bound on FalsePass/N
}

// Measure computes Stats over pairs. A false pass is a Jev 8+ where the
// friends did not land; a false fail a Jev under 8 where they did.
func Measure(pairs []Pair) Stats {
	s := Stats{N: len(pairs)}
	var miss, land, hold float64
	var nl, nh int
	for _, p := range pairs {
		d := p.Jev - p.FriendScore
		if d < 0 {
			d = -d
		}
		miss += float64(d)
		if d <= 1 {
			s.Within1++
		}
		if p.Land {
			land += float64(p.Jev)
			nl++
			if p.Jev < PassScore {
				s.FalseFail++
			}
		} else {
			hold += float64(p.Jev)
			nh++
			s.NotLand++
			if p.Jev >= PassScore {
				s.FalsePass++
			}
		}
	}
	if s.N > 0 {
		s.MeanMiss = miss / float64(s.N)
	}
	if nl > 0 {
		s.MeanLand = land / float64(nl)
	}
	if nh > 0 {
		s.MeanHold = hold / float64(nh)
	}
	s.FPUpper = UpperBound(s.FalsePass, s.N)
	return s
}

// UpperBound is the one-sided 95% Clopper-Pearson upper bound on a rate with k
// events in n trials: the p at which P(X <= k; n, p) = 0.05, found by
// bisection on the exact binomial CDF to 1e-9. n == 0 bounds nothing (1).
func UpperBound(k, n int) float64 {
	if n <= 0 || k >= n {
		return 1
	}
	lo, hi := 0.0, 1.0
	for hi-lo > 1e-9 {
		p := (lo + hi) / 2
		if binomCDF(k, n, p) > 0.05 {
			lo = p
		} else {
			hi = p
		}
	}
	return hi
}

// binomCDF is P(X <= k) for X ~ Binomial(n, p), summed in log space.
func binomCDF(k, n int, p float64) float64 {
	if p <= 0 {
		return 1
	}
	if p >= 1 {
		return 0
	}
	lnN, _ := math.Lgamma(float64(n + 1))
	sum := 0.0
	for i := 0; i <= k; i++ {
		a, _ := math.Lgamma(float64(i + 1))
		b, _ := math.Lgamma(float64(n - i + 1))
		sum += math.Exp(lnN - a - b + float64(i)*math.Log(p) + float64(n-i)*math.Log1p(-p))
	}
	return sum
}

// The verdicts and their exit codes (#2536 rev 3).
const (
	Adopt  = "ADOPT"
	Keep   = "KEEP"
	Refuse = "REFUSE"
)

// Verdict is the rule's answer for one candidate against one incumbent.
type Verdict struct {
	Word     string
	Reason   string
	Cand     Stats // the candidate on the held-out pairs
	Inc      Stats // the incumbent on the same side
	Exit     int   // 0 adopt, 3 keep or refuse, 4 hold-out under MinHoldout
	Holdout  int
	Exemplar string // the held-out exemplar, for reason=exemplar-in-holdout
}

// Decide applies the tuning rule on the held-out pairs. A candidate replaces
// the incumbent only when every line holds, checked in this order: the
// hold-out is big enough to judge; no exemplar is held out; the incumbent was
// scored; the false-pass bound is under 5%; the candidate is not inverse (its
// mean on land pairs is above its mean on hold pairs); its false passes and
// false fails are each no more than the incumbent's; within-1 is 85% or more;
// and at least one error class is strictly lower (else KEEP).
func Decide(cand, inc []Pair, exemplars []string) Verdict {
	_, ch := Split(cand)
	_, ih := Split(inc)
	v := Verdict{Cand: Measure(ch), Inc: Measure(ih), Holdout: len(ch), Exit: 3}
	refuse := func(reason string) Verdict { v.Word, v.Reason = Refuse, reason; return v }
	if len(ch) < MinHoldout {
		v.Exit = 4
		return refuse("holdout-lt-60")
	}
	for _, e := range exemplars {
		repo, n, ok := splitRef(e)
		if ok && Held(repo, n) {
			v.Exemplar = e
			return refuse("exemplar-in-holdout")
		}
	}
	if len(ih) == 0 {
		return refuse("incumbent-unscored")
	}
	c, i := v.Cand, v.Inc
	if c.FPUpper >= 0.05 {
		return refuse("falsepass-bound")
	}
	if c.MeanLand <= c.MeanHold {
		return refuse("inverse")
	}
	if c.FalsePass > i.FalsePass {
		return refuse("falsepass-above-incumbent")
	}
	if c.FalseFail > i.FalseFail {
		return refuse("falsefail-above-incumbent")
	}
	if c.N == 0 || float64(c.Within1) < 0.85*float64(c.N) {
		return refuse("within1-lt-85")
	}
	if c.FalsePass == i.FalsePass && c.FalseFail == i.FalseFail {
		v.Word, v.Reason = Keep, "not-better"
		return v
	}
	v.Word, v.Reason, v.Exit = Adopt, "", 0
	return v
}

// splitRef reads "<owner>/<repo>#<n>" (or "<repo>#<n>") as the pairs' repo
// name (the last path element) and the number.
func splitRef(ref string) (string, int, bool) {
	var n int
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '#' {
			if _, err := fmt.Sscanf(ref[i+1:], "%d", &n); err != nil || n <= 0 {
				return "", 0, false
			}
			repo := ref[:i]
			for j := len(repo) - 1; j >= 0; j-- {
				if repo[j] == '/' {
					repo = repo[j+1:]
					break
				}
			}
			return repo, n, repo != ""
		}
	}
	return "", 0, false
}

// Line is the one CALIB line the harness prints for a verdict.
func (v Verdict) Line(prompt8, incumbent8 string) string {
	c := v.Cand
	return fmt.Sprintf("CALIB prompt=%s incumbent=%s n=%d holdout=%d within1=%d falsepass=%d fp_ub=%.2f%% falsefail=%d meanmiss=%.2f verdict=%s reason=%s",
		prompt8, incumbent8, c.N, v.Holdout, c.Within1, c.FalsePass, 100*c.FPUpper, c.FalseFail, c.MeanMiss, v.Word, v.Reason)
}
