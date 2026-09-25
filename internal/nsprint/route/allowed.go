// Package route carries the router's allowed_routes table (#2895): which
// provider routes a card may run on, per rung (flash, pro) and work type,
// with the ranking numbers that put each route where it is. An overrides row
// is the one manual, interim exception: it replaces a rung's list outright
// for one work type from different evidence than the ranking, and may name a
// route the ranking holds or drops.
//
// The table is routes.yaml, embedded at build time, so a binary and the table
// it enforces are one artifact. It replaces the two text lists the bash
// launchers read (providers-flash.txt, providers-pro.txt). The file is a
// small, strict YAML subset: full-line comments, top-level scalars, and four
// block lists of flat maps (routes, types, overrides, spread); values are plain,
// "quoted" or a [flow, list]. Anything else is refused at load, never guessed.
package route

import (
	_ "embed"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

//go:embed routes.yaml
var routesYAML []byte

// State is where the ranking put a route.
type State string

const (
	Allowed State = "allowed" // in its rung's list for every work type
	Held    State = "held"    // out of the rung's list unless a types row adds it
	Dropped State = "dropped" // refused everywhere
)

// Numbers are the ranking row for the route's model.
type Numbers struct {
	Run, Scored, U8, U9 int
	USDPer8, Efficiency float64
	Q                   float64
	TokPer8             float64 // millions of tokens of all run cards per 8+ card
	WallS               int     // median wall seconds of run cards
}

// numberKeys is how many ranking numbers a row carries when it carries any.
const numberKeys = 9

// Flags a row may carry: a fact about the route that the numbers cannot show.
const (
	FlagBenched = "benched" // the route carries a ROUTE-BENCHED flag
	FlagDead    = "dead"    // the route's launches return nothing
)

// Row is one route.
type Row struct {
	Route, Rung, Model, Via string
	State                   State
	Flag                    string   // "", benched or dead
	Numbers                 *Numbers // nil when the route is not in the ranking
	Why                     string
	// Faults are the attribution's fault classes the route's preamble is
	// built from (#2498 S8), and FaultsFrom says whose they are: model or
	// rung. Both empty is a route with no preamble.
	Faults     []string
	FaultsFrom string
	// Pin is the one upstream provider an OpenRouter route's requests are
	// pinned to with fallbacks off (#3151, pin.go); "" off OpenRouter.
	Pin string
}

// TypeRow widens one rung for one work type by held routes.
type TypeRow struct {
	Type, Rung string
	Add        []string
	Why        string
}

// OverrideRow replaces one rung's list outright for one work type: a manual,
// interim exception (not derived by the rule) that may name a route this
// table's rule holds or drops. Source is the different evidence it is drawn
// from (never the ranking named at the top of the file, or it would belong
// there instead). It is reconciled or removed at the next full re-rank.
type OverrideRow struct {
	Type, Rung  string
	Routes      []string
	Source, Why string
}

// Card is what the router checks: the card's rung, work type and route.
type Card struct {
	Rung, Type, Route string
}

// Table is the parsed allowed_routes table.
type Table struct {
	Source, Rule string
	rows         []Row
	byRoute      map[string]int
	types        []TypeRow
	overrides    []OverrideRow
	spread       []SpreadRow
	rungs        []string
}

// Load parses the embedded routes.yaml.
func Load() (*Table, error) { return Parse(routesYAML) }

// Rows returns the routes in file order.
func (t *Table) Rows() []Row { return append([]Row(nil), t.rows...) }

// Types returns the work types that carry a types row, in file order.
func (t *Table) Types() []string {
	var out []string
	seen := map[string]bool{}
	for _, tr := range t.types {
		if !seen[tr.Type] {
			seen[tr.Type] = true
			out = append(out, tr.Type)
		}
	}
	return out
}

// Allowed lists the routes a card of this rung and work type may run on, in
// file order. workType "" is the rung's default list. A work type carrying
// an overrides row replaces that list outright, minus any benched or dead
// route it names (Check has the last word, even inside an override).
func (t *Table) Allowed(rung, workType string) []string {
	if o := t.overrideFor(rung, workType); o != nil {
		var out []string
		for _, route := range o.Routes {
			if i, ok := t.byRoute[route]; ok && t.rows[i].Flag != FlagBenched && t.rows[i].Flag != FlagDead {
				out = append(out, route)
			}
		}
		return out
	}
	var out []string
	for _, r := range t.rows {
		if r.Rung == rung && (r.State == Allowed || (r.State == Held && t.added(rung, workType, r.Route))) {
			out = append(out, r.Route)
		}
	}
	return out
}

// overrideFor returns the overrides row for this rung and work type, or nil.
func (t *Table) overrideFor(rung, workType string) *OverrideRow {
	if workType == "" {
		return nil
	}
	for i := range t.overrides {
		if t.overrides[i].Type == workType && t.overrides[i].Rung == rung {
			return &t.overrides[i]
		}
	}
	return nil
}

func (t *Table) added(rung, workType, route string) bool {
	if workType == "" {
		return false
	}
	for _, tr := range t.types {
		if tr.Type == workType && tr.Rung == rung {
			for _, a := range tr.Add {
				if a == route {
					return true
				}
			}
		}
	}
	return false
}

// Check returns nil when the card may run on its route, and otherwise an
// error whose text starts with REFUSED and names the rule that refused it.
func (t *Table) Check(c Card) error {
	refuse := func(why string) error {
		typ := c.Type
		if typ == "" {
			typ = "-"
		}
		return fmt.Errorf("REFUSED route=%s rung=%s type=%s: %s", field(c.Route), field(c.Rung), field(typ), why)
	}
	knownRung := false
	for _, r := range t.rungs {
		knownRung = knownRung || r == c.Rung
	}
	if !knownRung {
		return refuse(fmt.Sprintf("rung %s is not in the table (%s)", field(c.Rung), strings.Join(t.rungs, ", ")))
	}
	i, ok := t.byRoute[c.Route]
	if !ok {
		return refuse(fmt.Sprintf("route %s is not in the table; a new route enters by a measured A/B row", field(c.Route)))
	}
	row := t.rows[i]
	if row.Rung != c.Rung {
		return refuse(fmt.Sprintf("%s is a rung %s route", row.Route, row.Rung))
	}
	if o := t.overrideFor(c.Rung, c.Type); o != nil {
		if row.Flag == FlagBenched || row.Flag == FlagDead {
			return refuse(fmt.Sprintf("%s is %s: %s", row.Route, row.Flag, row.Why))
		}
		for _, or := range o.Routes {
			if or == row.Route {
				return nil
			}
		}
		return refuse(fmt.Sprintf("%s is not on the %s override for rung %s: %s", row.Route, c.Type, c.Rung, o.Why))
	}
	switch row.State {
	case Dropped:
		return refuse(fmt.Sprintf("%s is dropped: %s", row.Route, row.Why))
	case Held:
		if !t.added(c.Rung, c.Type, c.Route) {
			return refuse(fmt.Sprintf("%s is held: %s", row.Route, row.Why))
		}
	}
	return nil
}

func field(s string) string {
	if s == "" {
		return `""`
	}
	return strings.Join(strings.Fields(s), "_")
}

// Render prints the table with the numbers, the types rows and the resulting
// allowed list per rung.
func (t *Table) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ROUTES source=%q rule=%q\n", t.Source, t.Rule)
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "rung\troute\tmodel\tvia\tstate\trun\tscored\t8+\t9+\t$/8+\teff\tQ\twhy")
	for _, r := range t.rows {
		nums := "-\t-\t-\t-\t-\t-\t-"
		if n := r.Numbers; n != nil {
			nums = fmt.Sprintf("%d\t%d\t%d\t%d\t$%.2f\t%.2f\t%+.2f", n.Run, n.Scored, n.U8, n.U9, n.USDPer8, n.Efficiency, n.Q)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Rung, r.Route, r.Model, r.Via, r.State, nums, r.Why)
	}
	_ = tw.Flush()
	for _, tr := range t.types {
		adds := make([]string, len(tr.Add))
		for i, a := range tr.Add {
			adds[i] = "+" + a
		}
		fmt.Fprintf(&b, "TYPE %s %s %s  %s\n", tr.Type, tr.Rung, strings.Join(adds, " "), tr.Why)
	}
	for _, r := range t.rows {
		if len(r.Faults) > 0 {
			fmt.Fprintf(&b, "PREAMBLE %s from=%s %s\n", r.Route, r.FaultsFrom, strings.Join(r.Faults, ","))
		}
	}
	for _, o := range t.overrides {
		fmt.Fprintf(&b, "OVERRIDE %s %s %s  source=%q  %s\n", o.Type, o.Rung, strings.Join(o.Routes, " "), o.Source, o.Why)
	}
	for _, rung := range t.rungs {
		fmt.Fprintf(&b, "ALLOWED %s %s\n", rung, strings.Join(t.Allowed(rung, ""), " "))
	}
	return b.String()
}

// Parse reads the strict YAML subset described in the package comment.
func Parse(src []byte) (*Table, error) {
	t := &Table{byRoute: map[string]int{}}
	section := ""
	var items []map[string]string
	var itemLines []int
	var sections []string
	for n, raw := range strings.Split(string(src), "\n") {
		lineNo := n + 1
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "\t") {
			return nil, fmt.Errorf("routes.yaml:%d: tab in indentation or value", lineNo)
		}
		indent := len(line) - len(trimmed)
		switch {
		case indent == 0:
			key, val, err := keyValue(trimmed)
			if err != nil {
				return nil, fmt.Errorf("routes.yaml:%d: %v", lineNo, err)
			}
			switch key {
			case "source", "rule":
				s, err := scalar(val)
				if err != nil {
					return nil, fmt.Errorf("routes.yaml:%d: %v", lineNo, err)
				}
				if key == "source" {
					t.Source = s
				} else {
					t.Rule = s
				}
				section = ""
			case "routes", "types", "overrides", "spread":
				if val != "" {
					return nil, fmt.Errorf("routes.yaml:%d: %s wants a block list below it", lineNo, key)
				}
				for _, s := range sections {
					if s == key {
						return nil, fmt.Errorf("routes.yaml:%d: %s appears twice", lineNo, key)
					}
				}
				sections = append(sections, key)
				section = key
			default:
				return nil, fmt.Errorf("routes.yaml:%d: unknown top-level key %q", lineNo, key)
			}
		case indent == 2 && strings.HasPrefix(trimmed, "- "):
			if section == "" {
				return nil, fmt.Errorf("routes.yaml:%d: list item outside routes, types, overrides or spread", lineNo)
			}
			key, val, err := keyValue(strings.TrimPrefix(trimmed, "- "))
			if err != nil {
				return nil, fmt.Errorf("routes.yaml:%d: %v", lineNo, err)
			}
			items = append(items, map[string]string{"\x00section": section, key: val})
			itemLines = append(itemLines, lineNo)
		case indent == 4 && len(items) > 0 && section != "":
			key, val, err := keyValue(trimmed)
			if err != nil {
				return nil, fmt.Errorf("routes.yaml:%d: %v", lineNo, err)
			}
			cur := items[len(items)-1]
			if _, dup := cur[key]; dup {
				return nil, fmt.Errorf("routes.yaml:%d: key %s twice in one item", lineNo, key)
			}
			cur[key] = val
		default:
			return nil, fmt.Errorf("routes.yaml:%d: unexpected indentation or shape: %q", lineNo, trimmed)
		}
	}
	for i, it := range items {
		var err error
		switch it["\x00section"] {
		case "routes":
			err = t.addRow(it)
		case "types":
			err = t.addType(it)
		case "spread":
			err = t.addSpread(it)
		default:
			err = t.addOverride(it)
		}
		if err != nil {
			return nil, fmt.Errorf("routes.yaml:%d: %v", itemLines[i], err)
		}
	}
	if len(t.rows) == 0 {
		return nil, fmt.Errorf("routes.yaml: no routes")
	}
	for _, tr := range t.types {
		for _, a := range tr.Add {
			i, ok := t.byRoute[a]
			if !ok {
				return nil, fmt.Errorf("routes.yaml: types %s adds unknown route %s", tr.Type, a)
			}
			row := t.rows[i]
			if row.Rung != tr.Rung {
				return nil, fmt.Errorf("routes.yaml: types %s adds %s, a rung %s route, to rung %s", tr.Type, a, row.Rung, tr.Rung)
			}
			if row.State == Dropped {
				return nil, fmt.Errorf("routes.yaml: types %s adds %s, which is dropped; a dropped route re-enters by a measured A/B", tr.Type, a)
			}
		}
	}
	// An overrides row may legitimately name a dropped or held route (that is
	// the point of the escape hatch), but every route it names must still be
	// a real, same-rung row, and two overrides may not both claim one
	// (rung, type) pair -- that would make Allowed/Check order-dependent.
	seenOverride := map[string]bool{}
	for _, o := range t.overrides {
		key := o.Rung + "\x00" + o.Type
		if seenOverride[key] {
			return nil, fmt.Errorf("routes.yaml: overrides %s on rung %s appears twice", o.Type, o.Rung)
		}
		seenOverride[key] = true
		for _, route := range o.Routes {
			i, ok := t.byRoute[route]
			if !ok {
				return nil, fmt.Errorf("routes.yaml: overrides %s names unknown route %s", o.Type, route)
			}
			if t.rows[i].Rung != o.Rung {
				return nil, fmt.Errorf("routes.yaml: overrides %s names %s, a rung %s route, on rung %s", o.Type, route, t.rows[i].Rung, o.Rung)
			}
		}
	}
	if err := t.checkSpread(); err != nil {
		return nil, fmt.Errorf("routes.yaml: %v", err)
	}
	return t, nil
}

func (t *Table) addRow(it map[string]string) error {
	var r Row
	var nums Numbers
	have := 0
	for key, raw := range it {
		if key == "\x00section" {
			continue
		}
		var err error
		switch key {
		case "route", "rung", "model", "via", "state", "flag", "why", "pin":
			var s string
			if s, err = scalar(raw); err != nil {
				break
			}
			switch key {
			case "route":
				r.Route = s
			case "rung":
				r.Rung = s
			case "model":
				r.Model = s
			case "via":
				r.Via = s
			case "state":
				r.State = State(s)
			case "flag":
				r.Flag = s
			case "why":
				r.Why = s
			case "pin":
				r.Pin = s
			}
		case "faults":
			r.Faults, err = parseFaults(raw)
		case "faults_from":
			r.FaultsFrom, err = scalar(raw)
		case "run", "scored", "u8", "u9", "wall_s":
			var v int
			if v, err = strconv.Atoi(raw); err != nil || v < 0 {
				return fmt.Errorf("%s %q is not a count", key, raw)
			}
			have++
			switch key {
			case "run":
				nums.Run = v
			case "scored":
				nums.Scored = v
			case "u8":
				nums.U8 = v
			case "u9":
				nums.U9 = v
			case "wall_s":
				nums.WallS = v
			}
		case "usd_per_8", "efficiency", "q", "tok_per_8":
			var v float64
			if v, err = strconv.ParseFloat(raw, 64); err != nil {
				return fmt.Errorf("%s %q is not a number", key, raw)
			}
			have++
			switch key {
			case "usd_per_8":
				nums.USDPer8 = v
			case "efficiency":
				nums.Efficiency = v
			case "q":
				nums.Q = v
			case "tok_per_8":
				nums.TokPer8 = v
			}
		default:
			return fmt.Errorf("unknown route key %q", key)
		}
		if err != nil {
			return err
		}
	}
	if r.Route == "" || r.Rung == "" || r.Model == "" {
		return fmt.Errorf("a route needs route, rung and model")
	}
	if strings.ContainsAny(r.Route, " \t") || strings.ContainsAny(r.Rung, " \t") {
		return fmt.Errorf("route and rung are single words")
	}
	switch r.State {
	case Allowed, Held, Dropped:
	default:
		return fmt.Errorf("route %s: state %q, want allowed, held or dropped", r.Route, r.State)
	}
	switch r.Flag {
	case "", FlagBenched, FlagDead:
	default:
		return fmt.Errorf("route %s: flag %q, want benched or dead", r.Route, r.Flag)
	}
	if r.State != Allowed && r.Why == "" {
		return fmt.Errorf("route %s is %s without a why", r.Route, r.State)
	}
	switch {
	case len(r.Faults) > 0 && r.FaultsFrom == "", len(r.Faults) == 0 && r.FaultsFrom != "":
		return fmt.Errorf("route %s: faults and faults_from go together", r.Route)
	case r.FaultsFrom != "" && r.FaultsFrom != FromModel && r.FaultsFrom != FromRung:
		return fmt.Errorf("route %s: faults_from %q, want model or rung", r.Route, r.FaultsFrom)
	}
	if err := checkPin(r); err != nil {
		return err
	}
	switch have {
	case 0:
	case numberKeys:
		r.Numbers = &nums
	default:
		return fmt.Errorf("route %s carries %d of the %d ranking numbers; give all or none", r.Route, have, numberKeys)
	}
	if _, dup := t.byRoute[r.Route]; dup {
		return fmt.Errorf("route %s appears twice", r.Route)
	}
	t.byRoute[r.Route] = len(t.rows)
	t.rows = append(t.rows, r)
	known := false
	for _, g := range t.rungs {
		known = known || g == r.Rung
	}
	if !known {
		t.rungs = append(t.rungs, r.Rung)
	}
	return nil
}

func (t *Table) addType(it map[string]string) error {
	var tr TypeRow
	for key, raw := range it {
		if key == "\x00section" {
			continue
		}
		switch key {
		case "type", "rung", "why":
			s, err := scalar(raw)
			if err != nil {
				return err
			}
			switch key {
			case "type":
				tr.Type = s
			case "rung":
				tr.Rung = s
			case "why":
				tr.Why = s
			}
		case "add":
			if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
				return fmt.Errorf("add wants a [flow, list] of routes")
			}
			for _, a := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"), ",") {
				if a = strings.TrimSpace(a); a != "" {
					tr.Add = append(tr.Add, a)
				}
			}
		default:
			return fmt.Errorf("unknown types key %q", key)
		}
	}
	if tr.Type == "" || tr.Rung == "" || len(tr.Add) == 0 || tr.Why == "" {
		return fmt.Errorf("a types row needs type, rung, add and why")
	}
	t.types = append(t.types, tr)
	return nil
}

func (t *Table) addOverride(it map[string]string) error {
	var o OverrideRow
	for key, raw := range it {
		if key == "\x00section" {
			continue
		}
		switch key {
		case "type", "rung", "source", "why":
			s, err := scalar(raw)
			if err != nil {
				return err
			}
			switch key {
			case "type":
				o.Type = s
			case "rung":
				o.Rung = s
			case "source":
				o.Source = s
			case "why":
				o.Why = s
			}
		case "routes":
			if !strings.HasPrefix(raw, "[") || !strings.HasSuffix(raw, "]") {
				return fmt.Errorf("routes wants a [flow, list] of routes")
			}
			for _, a := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"), ",") {
				if a = strings.TrimSpace(a); a != "" {
					o.Routes = append(o.Routes, a)
				}
			}
		default:
			return fmt.Errorf("unknown overrides key %q", key)
		}
	}
	if o.Type == "" || o.Rung == "" || len(o.Routes) == 0 || o.Source == "" || o.Why == "" {
		return fmt.Errorf("an overrides row needs type, rung, routes, source and why")
	}
	t.overrides = append(t.overrides, o)
	return nil
}

func keyValue(s string) (string, string, error) {
	i := strings.Index(s, ":")
	if i <= 0 {
		return "", "", fmt.Errorf("want key: value, got %q", s)
	}
	key := s[:i]
	if strings.ContainsAny(key, " \"'") {
		return "", "", fmt.Errorf("bad key %q", key)
	}
	rest := s[i+1:]
	if rest != "" && !strings.HasPrefix(rest, " ") {
		return "", "", fmt.Errorf("want a space after %s:", key)
	}
	return key, strings.TrimSpace(rest), nil
}

func scalar(v string) (string, error) {
	if strings.HasPrefix(v, `"`) {
		s, err := strconv.Unquote(v)
		if err != nil {
			return "", fmt.Errorf("bad quoted string %s", v)
		}
		return s, nil
	}
	if strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") || strings.HasPrefix(v, "'") || strings.Contains(v, " #") {
		return "", fmt.Errorf("value %q is outside the subset; quote it", v)
	}
	return v, nil
}

// Derived is what the table's rule gives one row: the state and the step of
// the rule that decided it.
type Derived struct {
	State  State
	Reason string // dead route, not in the ranking, quality rule, best third, benched, Pareto-dominated, too few, second tier
}

// Derive applies the rule written at the top of routes.yaml (and in its rule
// line) to the rows' numbers and flags, first match wins, and returns the
// state per route. The persisted states must equal it (TestTheTableFollowsItsRule).
func (t *Table) Derive() map[string]Derived {
	out := map[string]Derived{}
	for _, rung := range t.rungs {
		var rows []Row
		for _, r := range t.rows {
			if r.Rung == rung {
				rows = append(rows, r)
			}
		}
		for k, v := range deriveRung(rows) {
			out[k] = v
		}
	}
	return out
}

func deriveRung(rows []Row) map[string]Derived {
	useful := func(n *Numbers) float64 { return float64(n.U8) / float64(n.Scored) }
	measured := func(r Row) bool {
		return r.Flag != FlagDead && r.Numbers != nil && r.Numbers.U8 >= 5 && r.Numbers.Scored > 0
	}
	// Rows sharing a model carry the model's numbers; count each model once.
	onePerModel := func(keep func(Row) bool) []Row {
		var out []Row
		seen := map[string]bool{}
		for _, r := range rows {
			if keep(r) && !seen[r.Model] {
				seen[r.Model] = true
				out = append(out, r)
			}
		}
		return out
	}
	var us []float64
	for _, r := range onePerModel(measured) {
		us = append(us, useful(r.Numbers))
	}
	farBelow := 0.75 * median(us)
	quality := func(r Row) bool {
		n := r.Numbers
		return n != nil && n.Scored >= 8 && n.Q <= 0 && useful(n) < farBelow
	}
	var effs []float64
	for _, r := range onePerModel(func(r Row) bool { return measured(r) && !quality(r) }) {
		effs = append(effs, r.Numbers.Efficiency)
	}
	sort.Float64s(effs)
	cut := math.Inf(-1)
	if len(effs) > 0 {
		cut = effs[(len(effs)+2)/3-1] + 0.01 + 1e-9
	}
	dominated := func(r Row) bool {
		a := r.Numbers
		for _, o := range rows {
			b := o.Numbers
			if o.Route == r.Route || b == nil || b.U8 < 1 || a.U8 < 1 {
				continue
			}
			ra, rb := float64(a.U8)/float64(a.Run), float64(b.U8)/float64(b.Run)
			noWorse := b.USDPer8 <= a.USDPer8 && b.TokPer8 <= a.TokPer8 && b.WallS <= a.WallS && rb >= ra
			better := b.USDPer8 < a.USDPer8 || b.TokPer8 < a.TokPer8 || b.WallS < a.WallS || rb > ra
			if noWorse && better {
				return true
			}
		}
		return false
	}
	out := map[string]Derived{}
	for _, r := range rows {
		n := r.Numbers
		var d Derived
		switch {
		case r.Flag == FlagDead:
			d = Derived{Dropped, "dead route"}
		case n == nil:
			d = Derived{Held, "not in the ranking"}
		case quality(r):
			d = Derived{Dropped, "quality rule"}
		case measured(r) && n.Efficiency <= cut && r.Flag == FlagBenched:
			d = Derived{Held, "benched"}
		case measured(r) && n.Efficiency <= cut:
			d = Derived{Allowed, "best third"}
		case n.Q <= 0 && dominated(r):
			d = Derived{Dropped, "Pareto-dominated"}
		case n.U8 < 5:
			d = Derived{Held, "too few"}
		default:
			d = Derived{Held, "second tier"}
		}
		out[r.Route] = d
	}
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// Tier lists the allowed routes of one rung, the rung a card names with its
// ROUTE: pro|flash line, in file order, which is the table's efficiency order
// within a rung (best first). A held or dropped route is never in it, nor a
// benched or dead one, and no types row or override widens it: the bench
// harness runs a card on the first row (nova-sprint routes --tier).
func (t *Table) Tier(rung string) []Row {
	var out []Row
	for _, r := range t.rows {
		if r.Rung == rung && r.State == Allowed && r.Flag != FlagBenched && r.Flag != FlagDead {
			out = append(out, r)
		}
	}
	return out
}

// Launch is the model string the bench harness passes to nova-swarm native
// --model: <via>/<model>, e.g. opencode/qwen3.6-plus,
// openrouter/qwen/qwen3.8-flash or deepseek/deepseek-v4-pro. The datacenter
// via is the one exception: its model already names the harness's provider
// (inception/mercury-2.5), so the model is the launch string as it stands.
func (r Row) Launch() string {
	if r.Via == ViaDatacenter {
		return r.Model
	}
	return r.Via + "/" + r.Model
}
