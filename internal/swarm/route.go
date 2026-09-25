package swarm

// The typed-decision route (docs/SPEC-DECIDE.md): a card's worker is chosen by a
// small judgment behind a floor, and a decision below the floor is a suggestion,
// never an authorization -- the caller keeps the default worker.
//
// This is the shared core. `nova-swarm route` prints one ROUTE line and exits 3
// below the floor; `nova-pulse launch` calls the same function to pick each card's
// worker and logs the ROUTE line beside the card and the time (rule 8).

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// RouteRow is one row of the routes table: the worker description path for
// (kind, complexity), where class is public (a free/contributor route) or
// paid. A public-class worker is skipped for private material.
type RouteRow struct {
	Kind       string
	Complexity int
	Worker     string
	Class      string
}

// RouteQuestions is the four questions route asks about the card text.
func RouteQuestions() map[string]decide.Question {
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

// ParseRoutes reads the routes table: kind<TAB>complexity<TAB>worker json
// path<TAB>class, one per line, class public|paid. Anything else is a
// refusal, never a guess.
func ParseRoutes(path string) ([]RouteRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--routes wants a readable TSV file: %w", err)
	}
	var rows []RouteRow
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
		rows = append(rows, RouteRow{Kind: kind, Complexity: complexity, Worker: worker, Class: class})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("--routes %s holds no route row; refusing to guess", path)
	}
	return rows, nil
}

// PickRoute chooses the worker description path for (kind, rounded
// complexity): rows of another kind never match, public-class rows are
// skipped when touches_private >= 0.5, an exact complexity wins, otherwise
// the nearest lower complexity for that kind, otherwise the default.
func PickRoute(rows []RouteRow, kind string, rounded int, private float64, def string) string {
	skipPublic := private >= 0.5
	var cands []RouteRow
	for _, r := range rows {
		if r.Kind != kind {
			continue
		}
		if skipPublic && r.Class == "public" {
			continue
		}
		cands = append(cands, r)
	}
	if len(cands) == 0 {
		return def
	}
	for _, r := range cands {
		if r.Complexity == rounded {
			return r.Worker
		}
	}
	best := -1 << 30
	bestWorker := ""
	found := false
	for _, r := range cands {
		if r.Complexity <= rounded && (!found || r.Complexity > best) {
			best, bestWorker, found = r.Complexity, r.Worker, true
		}
	}
	if found {
		return bestWorker
	}
	return def
}

// RouteResult is one card's typed decision and the worker it names. BelowFloor
// is true when the kind's confidence was under the floor: the worker is then
// the default and the decision is a suggestion, never an authorization.
type RouteResult struct {
	Kind        string
	KindConf    float64
	Complexity  int
	NeedsStrong float64
	Private     float64
	Worker      string
	Below       []string
	BelowFloor  bool
}

// RouteError is a route refusal with a reason token: "no-key" when the named
// environment variable is unset, "provider-error" when the call could not be
// made or its answer was malformed. The caller prints it as ROUTE REFUSED.
type RouteError struct {
	Reason string
	Err    error
}

func (e *RouteError) Error() string { return e.Err.Error() }
func (e *RouteError) Unwrap() error { return e.Err }

// RouteText makes the typed decision over the card text (bounded to its first
// 1500 characters, the state rule 1 allows) and picks the worker. def is the
// worker a below-floor answer keeps. It returns a *RouteError for a missing key
// or a provider failure, and never prints anything: the caller owns the line.
func RouteText(text, baseURL, keyEnv string, rows []RouteRow, floor float64, def string) (RouteResult, error) {
	state := text
	if len(state) > 1500 {
		state = state[:1500]
	}
	client, err := decide.New(baseURL, keyEnv)
	if err != nil {
		return RouteResult{}, &RouteError{Reason: "no-key", Err: err}
	}
	answers, _, err := client.Decide(context.Background(), state, RouteQuestions())
	if err != nil {
		return RouteResult{}, &RouteError{Reason: "provider-error", Err: err}
	}
	kindAns, ok := answers["kind"]
	if !ok || strings.TrimSpace(kindAns.Choice) == "" {
		return RouteResult{}, &RouteError{Reason: "provider-error", Err: fmt.Errorf("the decision named no kind")}
	}
	res := RouteResult{
		Kind:        kindAns.Choice,
		KindConf:    kindAns.Confidence,
		Complexity:  int(math.Round(answers["complexity"].Score)),
		NeedsStrong: answers["needs_strong"].Noul,
		Private:     answers["touches_private"].Noul,
	}
	res.Worker = PickRoute(rows, res.Kind, res.Complexity, res.Private, def)
	if res.KindConf < floor {
		res.Worker = def
		res.BelowFloor = true
	}
	names := make([]string, 0, len(answers))
	for name := range answers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if answers[name].Confidence < floor {
			res.Below = append(res.Below, name)
		}
	}
	return res, nil
}

// Line renders the ROUTE line for a card: the same line `nova-swarm route`
// prints, with the worker the decision chose (the default below the floor) and
// the below list naming every answer under the floor. cardBase is the card
// file's base name.
func (r RouteResult) Line(cardBase string, floor float64) string {
	belowWord := "-"
	if len(r.Below) > 0 {
		escaped := make([]string, 0, len(r.Below))
		for _, n := range r.Below {
			escaped = append(escaped, oneline.Field(n))
		}
		belowWord = strings.Join(escaped, ",")
	}
	worker := r.Worker
	if strings.TrimSpace(worker) == "" {
		worker = "-"
	}
	return fmt.Sprintf("ROUTE card=%s kind=%s conf=%.2f complexity=%d needs_strong=%.2f private=%.2f worker=%s floor=%.2f below=%s",
		oneline.Field(cardBase), oneline.Field(r.Kind), r.KindConf, r.Complexity,
		r.NeedsStrong, r.Private, oneline.Field(worker), floor, belowWord)
}
