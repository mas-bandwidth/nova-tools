package main

// landbatch.go is the tree gate of a batch (docs/SPEC-SPRINT.md section 7, the tree gate;
// tla/LandBisect.tla). The batch's heads are merged first, each with the lander's checks
// by script (checkCard: PATHS and prose), and the gate runs once, on the batch's tip: the
// generated ledgers its merges took the tip's side of regenerated there (settleLedgers),
// then the module built and vetted and, when the batch changes a document, a Go file or
// testdata, the tree tests. The base's tip is gated before any merge (treeGateBase), so a
// red tip has a first head whose merge turned the tree red; it is found by halving: each
// probe gates one prefix of the batch, the span between the longest prefix known green and
// the shortest known red halves, and when they are one head apart that head is the one
// blamed, as the gate of every tip blamed it, and the green prefix before it is what is
// pushed. A batch that is green costs one gate where the gate of every tip cost one a
// head (2026-10-04 2:30-3:05 PM ET: a 19-card batch merged in 1418.9 s, ~75 s a card, the
// gate's runs); a red one costs the gate and about log2(n) probes more.
//
// What is held is what the gate of every tip held where it matters: every tip land pushes
// passed the gate, built, vetted and tree-tested on the merged tree it is; a head is blamed
// only when the prefix before it is green and its own prefix red; and no card after the
// first red one is blamed or landed, so the cards after it stay queued as before. The
// commits between are merges the gate passed through and did not stop at: they are not
// each gated.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// landBatchMax is the most cards one batch of a stream holds when land is given no
// --batch-max: a stream with more queued lands them as several batches in one run, each
// gated and pushed.
const landBatchMax = 40

// batchCut is how many of cards, from the first, make the first batch: the run of
// consecutive cards naming the first one's repository and base, at most max of them (0:
// no cap). A batch is never empty while cards are.
func batchCut(cards []landCard, max int) int {
	n := 1
	for n < len(cards) && cards[n].repo == cards[0].repo && cards[n].base == cards[0].base && (max <= 0 || n < max) {
		n++
	}
	return n
}

// bisectRed is the halving of a red batch (tla/LandBisect.tla): prefix 0 (the base) is
// green and prefix n red, and probe(k) gates prefix k, green or red, or stops the search
// (stop: a failure that is no card's). It returns lo, the longest prefix found green, and
// hi = lo + 1, a prefix found red: the head hi is the first whose merge turned a green
// prefix red. ok is false when a probe stopped it. It probes at most ceil(log2 n) times.
func bisectRed(n int, probe func(k int) (green, stop bool)) (lo, hi int, ok bool) {
	lo, hi = 0, n
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		green, stop := probe(mid)
		switch {
		case stop:
			return lo, hi, false
		case green:
			lo = mid
		default:
			hi = mid
		}
	}
	return lo, hi, true
}

// treeTestsWanted says a change of the paths (git diff --name-only, one a line) is one the
// tree tests read: a Go file, a document, or under testdata (treeTested).
func treeTestsWanted(changed string) bool {
	return slices.ContainsFunc(strings.Split(changed, "\n"), treeTested)
}

// prefixGate is one gate of a prefix of the batch: sha the prefix's tip as gated (its last
// merge, amended by the ledgers' regeneration), red why it is red ("" green), ledger that
// it is red in the regeneration of the ledgers and not in the gate's runs, env a git
// failure that is no card's.
type prefixGate struct {
	sha, red, env string
	ledger        bool
}

// batchRun is a batch as build merged it, for its gate: the base's tip, the batch
// branch's tip after each merge (tips[k] is the tip after k cards, tips[0] the base),
// the cards merged, and each card's ledgers whose regeneration waits for the gate (nil
// for a card whose merge took no tip's side).
type batchRun struct {
	dir     string
	tips    []string
	cards   []landCard
	defers  []*ledgerDefer
	t       *landTimes
	gateEnv string // the git failure a probe met, once one has
}

// gatePrefix gates the first k cards of the batch: the batch branch reset to their tip,
// the ledgers their merges deferred regenerated there, then the tree gate (treeGate), the
// tree tests too when the prefix changes a file they read.
func (l *lander) gatePrefix(ctx context.Context, b *batchRun, k int) prefixGate {
	head, err := l.git(ctx, b.dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return prefixGate{env: "the batch branch has no tip to gate: " + firstLine("", err)}
	}
	if head != b.tips[k] {
		if _, err := l.git(ctx, b.dir, "reset", "-q", "--hard", b.tips[k]); err != nil {
			return prefixGate{env: "the batch branch could not be put at the tip of its first " + strconv.Itoa(k) + " merges: " + firstLine("", err)}
		}
	}
	start := time.Now()
	card, env := l.settleLedgers(ctx, b.dir, b.cards[:k], b.defers[:k])
	since(&b.t.Ledger, start)
	switch {
	case env != "":
		return prefixGate{env: env}
	case card != "":
		return prefixGate{red: card, ledger: true}
	}
	sha, err := l.git(ctx, b.dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return prefixGate{env: "the batch branch has no tip to gate: " + firstLine("", err)}
	}
	changed, err := l.git(ctx, b.dir, "diff", "--name-only", "-M", b.tips[0], sha)
	if err != nil {
		return prefixGate{env: "the files the batch changed could not be listed: " + firstLine("", err)}
	}
	if l.a != nil && l.a.beforeGate != nil {
		l.a.beforeGate(k)
	}
	start = time.Now()
	why := l.treeGate(ctx, b.dir, treeTestsWanted(changed))
	since(&b.t.Gate, start)
	b.t.Gates++
	return prefixGate{sha: sha, red: why}
}

// gateBatch gates the batch's n merged cards once, and when that is red finds the first
// card whose merge turned it red (bisectRed). landed is how many cards land, the batch
// branch left at their gated tip; blamed is the card that ends the batch, with why and
// the conflict it is reported as (nil when every one of the n is green); env a git failure
// that is no card's, which refuses the batch.
func (l *lander) gateBatch(ctx context.Context, b *batchRun, n int) (landed int, blamed *conflictCard, env string) {
	g := l.gatePrefix(ctx, b, n)
	switch {
	case g.env != "":
		return 0, nil, g.env
	case g.red == "":
		l.noteLedgers(b, n)
		return n, nil, ""
	}
	greens := map[int]string{0: b.tips[0]}
	reds := map[int]prefixGate{n: g}
	lo, hi, ok := bisectRed(n, func(k int) (bool, bool) {
		g := l.gatePrefix(ctx, b, k)
		switch {
		case g.env != "":
			b.gateEnv = g.env
			return false, true
		case g.red == "":
			greens[k] = g.sha
			return true, false
		}
		reds[k] = g
		return false, false
	})
	if !ok {
		return 0, nil, b.gateEnv
	}
	if _, err := l.git(ctx, b.dir, "reset", "-q", "--hard", greens[lo]); err != nil {
		return 0, nil, "the batch branch could not be put back at its green prefix: " + firstLine("", err)
	}
	l.noteLedgers(b, lo)
	c, r := b.cards[hi-1], reds[hi]
	f := conflictCard{landCard: c, why: "the head " + c.head + " of " + c.id + " fails the tree gate: " + r.red}
	switch d := b.defers[hi-1]; {
	case r.ledger && d != nil && d.conflicted:
		// its own merge took the tip's side of the ledgers that did not regenerate: the
		// head does not merge, as a resolution that failed at its merge said it
		f.why, f.kind, f.paths = "the head "+c.head+" of "+c.id+" does not merge: "+d.merge+"; "+r.red, d.kind, d.all
	case r.ledger && d != nil:
		// its head changed ledgers the lander makes, and they did not regenerate
		f.why, f.kind, f.paths = "the head "+c.head+" of "+c.id+" changes the generated ledgers "+sprint.Preview(d.paths, ", ")+", which land regenerates and could not: "+r.red, "ledger", d.paths
	}
	return lo, &f, ""
}

// noteLedgers writes, on each of the first k cards whose merge took the tip's side of
// generated ledgers, what regenerated them (ledgerNote), after any note of its merge.
func (l *lander) noteLedgers(b *batchRun, k int) {
	if k == 0 {
		return
	}
	tip := b.cards[k-1].id
	for i, d := range b.defers[:k] {
		if d == nil {
			continue
		}
		at := tip
		if i == k-1 {
			at = ""
		}
		note := ledgerNote(d.paths, testsOf(d.owners), at, d.conflicted)
		if b.cards[i].resolved != "" {
			note = b.cards[i].resolved + "; " + note
		}
		b.cards[i].resolved = note
	}
}
