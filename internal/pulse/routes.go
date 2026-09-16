package pulse

// THE ROUTES ARE CONFIGURATION, AND `routes` IS THE ONLY EDITOR. Pit stop 3, class M (#828).
//
// A route lived in four places nobody could query: queue/ROUTES-text, queue/ROUTES-code, a
// queue/ROUTE-BENCHED-<model> marker file per benched route, and a sentence in POLICY.md
// saying which of them Glenn had locked that hour. Benching a route that a probe had just
// failed meant `touch`ing a file whose name encoded the model with its slash swapped for an
// underscore; unbenching it meant remembering that the file was there. Three of today's
// four route changes were made by hand, and the fourth was made twice.
//
// So: the routes are the [routes] table of pulse.toml (config.go), the live rotation is
// that table minus the benched ones, and `nova-pulse routes` prints it and is the only
// thing that edits it. One line out, whatever it did.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// RouteRRFile holds the round-robin counter: the number of routes handed out on this
// queue, so the next card takes the next route rather than the first one forever.
const RouteRRFile = "ROUTE-RR"

// RoutesInput is everything the routes verb takes. Bench and Unbench are the two edits, and
// exactly one of them may be set.
type RoutesInput struct {
	Queue   string
	Class   string // "", "text" or "code"; empty prints both
	Bench   string // a route to bench: it leaves every class's rotation until it is unbenched
	Unbench string // a benched route to return to the rotation
	Stdout  io.Writer
	Stderr  io.Writer
}

// Routes prints the live rotation and, when asked, edits it. It returns 0 when it printed
// or edited, and 2 when it refused.
func RoutesVerb(in RoutesInput) int {
	if problem := routesProblem(in); problem != "" {
		fmt.Fprintf(in.Stderr, "ROUTES REFUSED: %s\n", problem)
		return 2
	}
	cfg, err := LoadConfig(in.Queue, io.Discard, 0)
	if err != nil {
		fmt.Fprintf(in.Stderr, "ROUTES REFUSED: %s\n", oneline.Err(err))
		return 2
	}

	switch {
	case in.Bench != "":
		if !routeKnown(cfg.Routes, in.Bench) {
			fmt.Fprintf(in.Stderr, "ROUTES REFUSED: %s is not a route of this queue (the routes are %s; add it to routes.text or routes.code in %s first)\n",
				oneline.Field(in.Bench), oneline.Field(strings.Join(allRoutes(cfg.Routes), ",")), filepath.Join(in.Queue, ConfigFile))
			return 2
		}
		if contains(cfg.Routes.Benched, in.Bench) {
			fmt.Fprintf(in.Stderr, "ROUTES REFUSED: %s is already benched (unbench it with nova-pulse routes --unbench %s)\n",
				oneline.Field(in.Bench), oneline.Field(in.Bench))
			return 2
		}
		cfg.Routes.Benched = append(cfg.Routes.Benched, in.Bench)
	case in.Unbench != "":
		if !contains(cfg.Routes.Benched, in.Unbench) {
			fmt.Fprintf(in.Stderr, "ROUTES REFUSED: %s is not benched (the benched routes are %s)\n",
				oneline.Field(in.Unbench), oneline.Field(listOrDash(cfg.Routes.Benched)))
			return 2
		}
		cfg.Routes.Benched = without(cfg.Routes.Benched, in.Unbench)
	}

	// An edit that would leave a class with nothing to launch onto is refused BEFORE it is
	// written: a queue whose every route is benched cannot be read back (config.go refuses
	// it), and a tool that writes a file it can no longer read is a tool that needs a person.
	if in.Bench != "" || in.Unbench != "" {
		for _, class := range RouteClasses {
			if len(cfg.Routes.Live(class)) == 0 {
				fmt.Fprintf(in.Stderr, "ROUTES REFUSED: benching %s leaves routes.%s with no live route (%s); bench it only with another route in that class\n",
					oneline.Field(in.Bench), class, oneline.Field(strings.Join(cfg.Routes.ForClass(class), ",")))
				return 2
			}
		}
		if err := SetConfigValue(in.Queue, "routes.benched", strings.Join(cfg.Routes.Benched, ",")); err != nil {
			fmt.Fprintf(in.Stderr, "ROUTES REFUSED: %s\n", oneline.Err(err))
			return 2
		}
	}

	fmt.Fprintln(in.Stdout, routesLine(in, cfg.Routes, RouteCounter(in.Queue)))
	return 0
}

// routesLine is the one line this verb prints: what happened, what is live, and what the
// next card takes. With --class it is that class alone.
func routesLine(in RoutesInput, r Routes, rr int) string {
	did := "OK"
	switch {
	case in.Bench != "":
		did = "BENCHED " + oneline.Field(in.Bench)
	case in.Unbench != "":
		did = "UNBENCHED " + oneline.Field(in.Unbench)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ROUTES %s", did)
	for _, class := range RouteClasses {
		if in.Class != "" && in.Class != class {
			continue
		}
		live := r.Live(class)
		fmt.Fprintf(&b, " %s=%s next.%s=%s", class, oneline.Field(listOrDash(live)), class, oneline.Field(NextRoute(live, rr)))
	}
	fmt.Fprintf(&b, " benched=%s rewrite=%s rr=%d",
		oneline.Field(listOrDash(r.Benched)), oneline.Field(dashIfEmpty(r.RewriteModelTo)), rr)
	return b.String()
}

// NextRoute is the route the next card of this class takes: the rotation advanced once from
// the counter, which is what modelFor does when it hands one out. An empty rotation is "-",
// never a silent default.
func NextRoute(live []string, rr int) string {
	if len(live) == 0 {
		return "-"
	}
	return live[(rr+1)%len(live)]
}

// RouteCounter reads the queue's round-robin counter. A queue that has handed out no route
// is at zero, which is the same answer a missing file gives.
func RouteCounter(queue string) int {
	raw, err := os.ReadFile(filepath.Join(queue, RouteRRFile))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// AdvanceRouteCounter hands out the next route of a rotation and records that it did.
func AdvanceRouteCounter(queue string, live []string) string {
	n := RouteCounter(queue) + 1
	_ = os.WriteFile(filepath.Join(queue, RouteRRFile), []byte(strconv.Itoa(n)+"\n"), 0o644)
	if len(live) == 0 {
		return DefaultRoute
	}
	return live[n%len(live)]
}

// RewriteModelLine is the rule of 2026-09-16 17:55Z as a function: a card's MODEL: line
// becomes the configured route, whatever the template wrote. An empty `to` is no rewrite at
// all, and a card with no MODEL: line is untouched -- the route it takes is then the
// rotation's, which is the same value by another road.
func RewriteModelLine(card, to string) (string, bool) {
	if strings.TrimSpace(to) == "" {
		return card, false
	}
	lines := strings.Split(card, "\n")
	changed := false
	for i, l := range lines {
		m, ok := strings.CutPrefix(strings.TrimSpace(l), "MODEL: ")
		if !ok || strings.TrimSpace(m) == to {
			continue
		}
		lines[i] = "MODEL: " + to
		changed = true
	}
	if !changed {
		return card, false
	}
	return strings.Join(lines, "\n"), true
}

// SetConfigValue writes one key of <queue>/pulse.toml in place, keeping every other line,
// every comment and the file's own spelling. A key under a table is written in that table;
// a key whose table is not there gets the table appended. It is the only writer of the
// configuration, so that "how do I bench a route" has one answer.
func SetConfigValue(queue, key, value string) error {
	if !slicesContains(configKeys, key) {
		return fmt.Errorf("%s: unknown key %q; the keys are %s", ConfigFile, key, strings.Join(configKeys, ", "))
	}
	path := filepath.Join(queue, ConfigFile)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %s", path, oneline.Err(err))
	}
	table, leaf, hasTable := strings.Cut(key, ".")
	if !hasTable {
		table, leaf = "", key
	}

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		lines = nil
	}
	section, wroteAt, tableAt := "", -1, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = strings.TrimSpace(t[1 : len(t)-1])
			if section == table {
				tableAt = i
			}
			continue
		}
		k, _, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		full := strings.TrimSpace(k)
		if section != "" {
			full = section + "." + full
		}
		if full == key {
			wroteAt = i
		}
	}

	line := leaf + " = " + value
	switch {
	case wroteAt >= 0:
		lines[wroteAt] = line
	case tableAt >= 0:
		lines = append(lines[:tableAt+1], append([]string{line}, lines[tableAt+1:]...)...)
	case table != "":
		lines = append(lines, "["+table+"]", line)
	default:
		lines = append(lines, line)
	}

	body := strings.Join(lines, "\n") + "\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %s", path, oneline.Err(err))
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("cannot write %s: %s", path, oneline.Err(err))
	}
	return nil
}

// routesProblem is every refusal this verb has, each naming its remedy.
func routesProblem(in RoutesInput) string {
	switch {
	case strings.TrimSpace(in.Queue) == "":
		return "--queue is required (pass the queue directory " + ConfigFile + " lives in)"
	case in.Class != "" && !contains(RouteClasses, in.Class):
		return fmt.Sprintf("--class %s is not one of %s (a card is text or code)", oneline.Field(in.Class), strings.Join(RouteClasses, "|"))
	case in.Bench != "" && in.Unbench != "":
		return "--bench and --unbench are one edit at a time (run the verb twice)"
	}
	return ""
}

func routeKnown(r Routes, route string) bool {
	return contains(r.Text, route) || contains(r.Code, route)
}

func allRoutes(r Routes) []string {
	out := append([]string{}, r.Text...)
	for _, c := range r.Code {
		if !contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func contains(list []string, s string) bool { return slicesContains(list, s) }

func without(list []string, s string) []string {
	var out []string
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func listOrDash(list []string) string {
	if len(list) == 0 {
		return "-"
	}
	return strings.Join(list, ",")
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
