// nova-swarm route picks the worker by a typed decision behind a floor.
//
// It sends the card's first 1500 characters as the state with four questions
// (kind, complexity, needs_strong, touches_private), reads a routes table of
// kind<TAB>complexity<TAB>worker<TAB>class rows, and prints one ROUTE line.
// A decision below the floor is a suggestion, never an authorization: below
// the kind floor the worker is the --default and the exit is 3.
package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// routeRow is one row of the routes table: the worker description path for
// (kind, complexity), where class is public (a free/contributor route) or
// paid. A public-class worker is skipped for private material.
type routeRow struct {
	kind       string
	complexity int
	worker     string
	class      string
}

// routeQuestions is the four questions route asks about the card text.
func routeQuestions() map[string]decide.Question {
	return map[string]decide.Question{
		"kind": {
			Instructions: "what kind of work is this card?",
			Choice: map[string]string{
				"probe": "a known-answer or reachability check, no code change",
				"read":  "read a spec, PR or code and report, no code change",
				"spec":  "write or amend a specification with at most a doc test",
				"fix":   "fix a named bug with a red test first",
				"feat":  "add a verb, flag or capability with tests",
				"port":  "replicate behaviour to another language or platform",
			},
		},
		"complexity": {
			Instructions: "how complex is this card?",
			Score: []string{
				"one file mechanical",
				"a few files one package",
				"several packages or a design choice",
				"cross-cutting or under-specified",
			},
		},
		"needs_strong":    {Instructions: "a cheap model would likely fail first attempt", Noul: true},
		"touches_private": {Instructions: "reads or writes private or secret material", Noul: true},
	}
}

// parseRoutes reads the routes table: kind<TAB>complexity<TAB>worker json
// path<TAB>class, one per line, class public|paid. Anything else is a
// refusal, never a guess.
func parseRoutes(path string) ([]routeRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--routes wants a readable TSV file: %w", err)
	}
	var rows []routeRow
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("--routes line %d wants 4 tab-separated fields (kind, complexity, worker, class), got %d", i+1, len(fields))
		}
		kind := strings.TrimSpace(fields[0])
		complexity, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil {
			return nil, fmt.Errorf("--routes line %d wants an integer complexity, got %q", i+1, fields[1])
		}
		worker := strings.TrimSpace(fields[2])
		class := strings.TrimSpace(fields[3])
		if kind == "" || worker == "" {
			return nil, fmt.Errorf("--routes line %d wants a nonempty kind and worker path", i+1)
		}
		if class != "public" && class != "paid" {
			return nil, fmt.Errorf("--routes line %d wants class public|paid, got %q", i+1, fields[3])
		}
		rows = append(rows, routeRow{kind: kind, complexity: complexity, worker: worker, class: class})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("--routes %s holds no route row; refusing to guess", path)
	}
	return rows, nil
}

// pickRoute chooses the worker description path for (kind, rounded
// complexity): rows of another kind never match, public-class rows are
// skipped when touches_private >= 0.5, an exact complexity wins, otherwise
// the nearest lower complexity for that kind, otherwise the default.
func pickRoute(rows []routeRow, kind string, rounded int, private float64, def string) string {
	skipPublic := private >= 0.5
	var cands []routeRow
	for _, r := range rows {
		if r.kind != kind {
			continue
		}
		if skipPublic && r.class == "public" {
			continue
		}
		cands = append(cands, r)
	}
	if len(cands) == 0 {
		return def
	}
	for _, r := range cands {
		if r.complexity == rounded {
			return r.worker
		}
	}
	best := -1 << 30
	bestWorker := ""
	found := false
	for _, r := range cands {
		if r.complexity <= rounded && (!found || r.complexity > best) {
			best, bestWorker, found = r.complexity, r.worker, true
		}
	}
	if found {
		return bestWorker
	}
	return def
}

func cmdRoute(args []string, stdout, stderr io.Writer) int {
	f := newFlags("route")
	card := f.fs.String("card", "", "")
	routes := f.fs.String("routes", "", "")
	floor := f.fs.Float64("floor", 0.9, "")
	def := f.fs.String("default", "", "")
	keyEnv := f.fs.String("key-env", decide.DefaultKeyEnv, "")
	baseURL := f.fs.String("base-url", decide.DefaultBaseURL, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*card, "card", "the path to the card file whose first 1500 characters are the state")
	f.want(*routes, "routes", "a TSV of kind<TAB>complexity<TAB>worker json path<TAB>class(public|paid)")
	if *floor < 0 || *floor > 1 {
		f.add(fmt.Sprintf("--floor is between 0 and 1, got %g; answers below it are a suggestion", *floor))
	}
	if f.refused(stderr) {
		return 2
	}
	raw, err := os.ReadFile(*card)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm route: --card wants a readable file: %s\n", oneline.Err(err))
		return 2
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		fmt.Fprintf(stderr, "nova-swarm route: --card %s is empty; it wants the card text\n", oneline.Field(*card))
		return 2
	}
	state := string(raw)
	if len(state) > 1500 {
		state = state[:1500]
	}
	rows, err := parseRoutes(*routes)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm route: %s\n", oneline.Err(err))
		return 2
	}
	client, err := decide.New(*baseURL, *keyEnv)
	if err != nil {
		fmt.Fprintf(stderr, "ROUTE REFUSED reason=no-key %s\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	answers, _, err := client.Decide(context.Background(), state, routeQuestions())
	if err != nil {
		fmt.Fprintf(stderr, "ROUTE REFUSED reason=provider-error %s\n", oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	kindAns, ok := answers["kind"]
	if !ok || strings.TrimSpace(kindAns.Choice) == "" {
		fmt.Fprintf(stderr, "ROUTE REFUSED reason=provider-error the decision named no kind\n")
		return 2
	}
	kind, kindConf := kindAns.Choice, kindAns.Confidence
	rounded := int(math.Round(answers["complexity"].Score))
	needsStrong := answers["needs_strong"].Noul
	private := answers["touches_private"].Noul
	worker := pickRoute(rows, kind, rounded, private, *def)
	code := 0
	if kindConf < *floor {
		worker = *def
		code = 3
	}
	var names []string
	for name := range answers {
		names = append(names, name)
	}
	sort.Strings(names)
	var below []string
	for _, name := range names {
		if answers[name].Confidence < *floor {
			below = append(below, name)
		}
	}
	belowWord := "-"
	if len(below) > 0 {
		escaped := make([]string, 0, len(below))
		for _, n := range below {
			escaped = append(escaped, oneline.Field(n))
		}
		belowWord = strings.Join(escaped, ",")
	}
	fmt.Fprintf(stdout, "ROUTE card=%s kind=%s conf=%.2f complexity=%d needs_strong=%.2f private=%.2f worker=%s floor=%.2f below=%s\n",
		oneline.Field(filepath.Base(*card)), oneline.Field(kind), kindConf, rounded,
		needsStrong, private, oneline.Field(dash(worker)), *floor, belowWord)
	return code
}
