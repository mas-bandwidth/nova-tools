// Package lineup holds the checks `nova-pulse lineup --profile coding` runs before the
// first card of a coding sprint is dealt (Glenn 2026-09-20: shares, guards, brakes, probes
// and supply are set BEFORE the first card; no changes under load).
//
// This file is the CARD half (#2944): every card queued for the sprint lints at its base,
// sits on a route in the allowed list, is one a ci card can be cut for, and carries
// NO-SUBAGENTS. The bench half is #2562. The two are separate so either lands first.
package lineup

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// The four card checks, in the order they are run and printed.
const (
	CheckLint        = "lint"
	CheckRoute       = "route"
	CheckCIDryRun    = "ci-dry-run"
	CheckNoSubagents = "no-subagents"
	// CheckQueue is a finding about the queue itself, not one card: no cards, or no
	// allowed list to check routes against. Its card is "-".
	CheckQueue = "queue"
)

// CardCheckNames is every per-card check, in order.
var CardCheckNames = []string{CheckLint, CheckRoute, CheckCIDryRun, CheckNoSubagents}

// Card is one queued card file.
type Card struct {
	Name string // the file's base name, which is what a RED line names
	Path string
	Raw  []byte
}

// Finding is one RED: the card it names (or "-" for the queue), the check, and why.
type Finding struct {
	Card   string
	Check  string
	Detail string
}

// CardChecks is what the card checks need from outside the card.
type CardChecks struct {
	// Lint runs the R4 lint (`nova-swarm lint --base-check`, #2636) on one card. It
	// returns "" for a clean card, else the first drift; an error means the lint could
	// not run, which is RED as well: no evidence is not a clean card.
	Lint func(c Card) (drift string, err error)
	// AllowedRoutes is the allowed list (#2895). A card's MODEL: must be on it; a card
	// with no MODEL: is dealt a route from the list, so it needs the list non-empty.
	AllowedRoutes []string
	// CIDryRun plans the ci card for this card without cutting it (#2842). Nil means
	// PlanCI, the plan this package can make from the card alone.
	CIDryRun func(c Card) (CIPlan, error)
}

// queueSubdirs are where a queue keeps cards waiting to be dealt: the queue root (a cut
// directory), pending/ (internal/pulse/wire.go) and front/ (the priority tier, #2417).
var queueSubdirs = []string{"", "front", "pending"}

// ReadQueue reads every card waiting in a queue directory, sorted by name within each
// tier, front first. A card is a *.md file; other files (ROUTES-code, pulse.log) are not.
func ReadQueue(dir string) ([]Card, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	var cards []Card
	for _, sub := range queueSubdirs {
		paths, _ := filepath.Glob(filepath.Join(dir, sub, "*.md"))
		sort.Strings(paths)
		for _, p := range paths {
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			cards = append(cards, Card{Name: filepath.Base(p), Path: p, Raw: raw})
		}
	}
	return cards, nil
}

// ReadRoutes reads an allowed list: one route per line, blank lines and # comments
// skipped. It is the shape of the queue's ROUTES-code file (internal/pulse/wire.go).
func ReadRoutes(file string) ([]string, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

// HeaderValue returns the value of the first `KEY: value` line for key, matched exactly
// (the darwin launchers read `base-sha:` case-sensitively, so this does too), and
// whether the line is there at all.
func HeaderValue(raw []byte, key string) (string, bool) {
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), key+":"); ok {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// CIPlan is what a ci card cut for this card's PR would run: the repo, the sha it starts
// from, and the Go packages its go test / gofmt / vet cover (#2842).
type CIPlan struct {
	Repo     string
	BaseSHA  string
	Leg      string
	Packages []string
}

// PlanCI makes the ci card's plan from the card alone. It refuses a card a ci card
// could not be cut for: no REPO, no base-sha, no PATHS, or a Go card whose PATHS name no
// Go package, whose ci card would test nothing and pass.
func PlanCI(c Card) (CIPlan, error) {
	var p CIPlan
	p.Repo, _ = HeaderValue(c.Raw, "REPO")
	if p.Repo == "" {
		return p, errors.New("no REPO: line, so no repo to cut the ci card on")
	}
	p.BaseSHA, _ = HeaderValue(c.Raw, "base-sha")
	if p.BaseSHA == "" {
		return p, errors.New("no base-sha: line, so no sha for the ci card to start from")
	}
	paths, _ := HeaderValue(c.Raw, "PATHS")
	// PATHS is written both ways on cards: space separated and comma separated
	// ("PATHS: a.go, b.go"). Split on both so a trailing comma never hides a .go file.
	fields := strings.FieldsFunc(paths, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if len(fields) == 0 {
		return p, errors.New("no PATHS: line, so the ci card has nothing to cover")
	}
	p.Leg, _ = HeaderValue(c.Raw, "LEG")
	seen := map[string]bool{}
	for _, f := range fields {
		if pkg := goPackageOf(f); pkg != "" && !seen[pkg] {
			seen[pkg] = true
			p.Packages = append(p.Packages, pkg)
		}
	}
	sort.Strings(p.Packages)
	if (p.Leg == "" || strings.EqualFold(p.Leg, "go")) && len(p.Packages) == 0 {
		return p, fmt.Errorf("PATHS %q name no Go package, so the ci card would test nothing", paths)
	}
	return p, nil
}

// goPackageOf is the package directory a PATHS entry lies in, as ./dir, or "" when the
// entry is not Go: a .go file, or a glob over a directory (dir/*, dir/...).
func goPackageOf(entry string) string {
	e := strings.TrimPrefix(entry, "./")
	switch {
	case strings.HasSuffix(e, ".go"):
		return "./" + path.Dir(e)
	case strings.HasSuffix(e, "/..."):
		return "./" + e
	case strings.HasSuffix(e, "/*"):
		return "./" + strings.TrimSuffix(e, "/*")
	}
	return ""
}

// Check runs the four checks on every card and returns every RED, in queue order and
// check order. No finding means the cards are lined up.
func (cc CardChecks) Check(cards []Card) []Finding {
	var out []Finding
	if len(cards) == 0 {
		out = append(out, Finding{Card: "-", Check: CheckQueue, Detail: "no cards queued"})
	}
	allowed := map[string]bool{}
	for _, r := range cc.AllowedRoutes {
		allowed[r] = true
	}
	if len(allowed) == 0 {
		out = append(out, Finding{Card: "-", Check: CheckQueue, Detail: "no allowed route list, so no card's route can be checked"})
	}
	plan := cc.CIDryRun
	if plan == nil {
		plan = PlanCI
	}
	for _, c := range cards {
		red := func(check, detail string) { out = append(out, Finding{Card: c.Name, Check: check, Detail: detail}) }

		switch {
		case cc.Lint == nil:
			red(CheckLint, "no lint to run, so the card is not known to lint at base")
		default:
			drift, err := cc.Lint(c)
			if err != nil {
				red(CheckLint, "lint could not run: "+err.Error())
			} else if drift != "" {
				red(CheckLint, drift)
			}
		}

		if model, ok := HeaderValue(c.Raw, "MODEL"); ok && model != "" {
			if len(allowed) > 0 && !allowed[model] {
				red(CheckRoute, fmt.Sprintf("MODEL: %s is not in the allowed list (%d routes)", model, len(allowed)))
			}
		}

		if _, err := plan(c); err != nil {
			red(CheckCIDryRun, err.Error())
		}

		if v, ok := HeaderValue(c.Raw, "NO-SUBAGENTS"); !ok || v == "" {
			red(CheckNoSubagents, "no NO-SUBAGENTS: line (the standard line on every card, #2533)")
		}
	}
	return out
}
