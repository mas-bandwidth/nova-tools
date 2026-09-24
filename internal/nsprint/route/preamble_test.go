package route

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// attributionLever is one row of testdata/attribution-2026-09-21.tsv.
type attributionLever struct {
	model, lever string
	n            int
}

func readAttribution(t *testing.T) []attributionLever {
	t.Helper()
	f, err := os.Open("testdata/attribution-2026-09-21.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []attributionLever
	sc := bufio.NewScanner(f)
	header := true
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if header {
			header = false
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 4 {
			t.Fatalf("attribution row %q: want 4 columns", line)
		}
		n, err := strconv.Atoi(cols[3])
		if err != nil {
			t.Fatalf("attribution row %q: n: %v", line, err)
		}
		out = append(out, attributionLever{model: cols[0], lever: cols[2], n: n})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// shortModel is the attribution's spelling of a route's model: the part after
// the provider's slash (moonshotai/kimi-k3 -> kimi-k3).
func shortModel(m string) string {
	if i := strings.LastIndex(m, "/"); i >= 0 {
		return m[i+1:]
	}
	return m
}

// rankClasses orders the actionable classes of counts by n, most first, then
// by name; floor drops classes seen fewer than floor times.
func rankClasses(counts map[string]int, floor int) []string {
	var out []string
	for c, n := range counts {
		if _, ok := faultSentences[c]; ok && n >= floor {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] > counts[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// The DONE-WHEN control for #2498 S8: every route a pro card can run on
// carries a preamble, one paragraph, built from the attribution's per-model
// fault classes. A route whose model the attribution charged with an
// actionable class carries exactly those classes (faults_from: model); a
// route whose model has none carries the pro rung's pooled classes seen at
// least rungFloor times (faults_from: rung). The expected classes are
// recomputed here from the fixture, so the table cannot drift from the
// attribution it cites.
func TestProRoutesCarryAPreamble(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	attr := readAttribution(t)
	byModel := map[string]map[string]int{}
	proModels := map[string]bool{}
	for _, r := range tab.Rows() {
		if r.Rung == "pro" {
			proModels[shortModel(r.Model)] = true
		}
	}
	pool := map[string]int{}
	for _, a := range attr {
		if byModel[a.model] == nil {
			byModel[a.model] = map[string]int{}
		}
		byModel[a.model][a.lever] += a.n
		if proModels[a.model] {
			pool[a.lever] += a.n
		}
	}
	rungWant := rankClasses(pool, rungFloor)
	if len(rungWant) == 0 {
		t.Fatal("precondition: the pro rung pools no actionable class; the fixture or the floor moved")
	}

	reach := tab.Reachable("pro")
	if len(reach) == 0 {
		t.Fatal("no route can run a pro card")
	}
	rows := map[string]Row{}
	for _, r := range tab.Rows() {
		rows[r.Route] = r
	}
	for _, name := range reach {
		row := rows[name]
		levers := byModel[shortModel(row.Model)]
		for lever := range levers {
			_, act := faultSentences[lever]
			if !act && !harnessSide[lever] {
				t.Errorf("%s: the attribution charges %s with %s, which has no preamble sentence and is not harness-side; add one", name, row.Model, lever)
			}
		}
		want, from := rankClasses(levers, 1), FromModel
		if len(want) == 0 {
			want, from = rungWant, FromRung
		}
		if row.FaultsFrom != from || strings.Join(row.Faults, ",") != strings.Join(want, ",") {
			t.Errorf("%s (%s): faults_from %q faults %v, want %q %v from the attribution", name, row.Model, row.FaultsFrom, row.Faults, from, want)
		}
		p, err := tab.Preamble(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.ContainsAny(p, "\n\r") || !strings.HasSuffix(p, ".") || len(p) > maxPreamble {
			t.Errorf("%s: preamble is not one paragraph under %d bytes ending in a full stop (%d bytes): %q", name, maxPreamble, len(p), p)
		}
		for _, c := range want {
			if !strings.Contains(p, faultSentences[c]) {
				t.Errorf("%s: preamble lacks the %s sentence: %q", name, c, p)
			}
		}
	}
}

// A route no pro card can reach carries no preamble, and asking for one is
// an error rather than an empty paragraph.
func TestPreambleRefusesARouteWithoutOne(t *testing.T) {
	tab, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ormuse", "no-such-route"} {
		if p, err := tab.Preamble(name); err == nil {
			t.Errorf("%s: preamble %q, want an error", name, p)
		}
	}
}

// The parser refuses a fault class it has no sentence for, and faults
// without faults_from (or the reverse).
func TestParseRefusesUnknownFaults(t *testing.T) {
	base := "routes:\n  - route: r1\n    rung: pro\n    model: m\n    state: held\n    why: \"x\"\n"
	for _, tc := range []struct{ extra, want string }{
		{"    faults: [CARD-NOPE]\n    faults_from: model\n", "CARD-NOPE"},
		{"    faults: [CARD-WIRE]\n", "faults_from"},
		{"    faults_from: model\n", "faults_from"},
		{"    faults: [CARD-WIRE]\n    faults_from: guess\n", "guess"},
	} {
		_, err := Parse([]byte(base + tc.extra))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err %v, want one naming %q", tc.extra, err, tc.want)
		}
	}
	if _, err := Parse([]byte(base + "    faults: [CARD-WIRE, CARD-GATE]\n    faults_from: model\n")); err != nil {
		t.Errorf("a valid faults row was refused: %v", err)
	}
}
