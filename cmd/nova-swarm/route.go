// nova-swarm route picks the worker by a typed decision behind a floor.
//
// It sends the card's first 1500 characters as the state with four questions
// (kind, complexity, needs_strong, touches_private), reads a routes table of
// kind<TAB>complexity<TAB>worker<TAB>class rows, and prints one ROUTE line.
// A decision below the floor is a suggestion, never an authorization: below
// the kind floor the worker is the --default and the exit is 3.
//
// The core lives in internal/swarm so `nova-pulse launch` picks each card's
// worker with the same function (docs/SPEC-DECIDE.md rule 8); this verb keeps
// its flags and its ROUTE REFUSED lines.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// routeRow, parseRoutes, pickRoute and routeQuestions are the shared core,
// named here so the verb's tests read as they did before the move.
type routeRow = swarm.RouteRow

func parseRoutes(path string) ([]routeRow, error) { return swarm.ParseRoutes(path) }

func pickRoute(rows []routeRow, kind string, rounded int, private float64, def string) string {
	return swarm.PickRoute(rows, kind, rounded, private, def)
}

func routeQuestions() map[string]decide.Question { return swarm.RouteQuestions() }

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
	rows, err := swarm.ParseRoutes(*routes)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm route: %s\n", oneline.Err(err))
		return 2
	}
	result, err := swarm.RouteText(string(raw), *baseURL, *keyEnv, rows, *floor, *def)
	if err != nil {
		reason := "provider-error"
		if re, ok := err.(*swarm.RouteError); ok {
			reason = re.Reason
		}
		fmt.Fprintf(stderr, "ROUTE REFUSED reason=%s %s\n", oneline.Field(reason), oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	belowWord := "-"
	if len(result.Below) > 0 {
		escaped := make([]string, 0, len(result.Below))
		for _, n := range result.Below {
			escaped = append(escaped, oneline.Field(n))
		}
		belowWord = strings.Join(escaped, ",")
	}
	worker := result.Worker
	if strings.TrimSpace(worker) == "" {
		worker = "-"
	}
	fmt.Fprintf(stdout, "ROUTE card=%s kind=%s conf=%.2f complexity=%d needs_strong=%.2f private=%.2f worker=%s floor=%.2f below=%s\n",
		oneline.Field(filepath.Base(*card)), oneline.Field(result.Kind), result.KindConf, result.Complexity,
		result.NeedsStrong, result.Private, oneline.Field(worker), *floor, belowWord)
	if result.BelowFloor {
		return 3
	}
	return 0
}
