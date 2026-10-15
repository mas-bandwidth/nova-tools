package swarm

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// A CARD THAT CHANGES THE DASHBOARD PAGE QUOTES THE OWNER ASKING FOR IT.
//
// The owner, 2026-10-11, after the page gained a release picker, a seat chip and rows of its
// own nobody asked for, and lost the orange read cells: "don't add things to the dashboard
// unless I ask for them", and "NEVER REMOVE THINGS FROM THE DASHBOARD UNLESS I ASK". Memory
// did not hold the rule five times, so the card lint holds it: a card whose PATHS: or NEW:
// covers the page's files (internal/sprintdash/page/) or the code that draws it (the page's
// copy and envelope, pageview.go; the server that serves it, dashboard.go) is refused unless
// its SOURCE: line quotes the owner asking for that visible change, dated: the words "the
// owner", a date (YYYY-MM-DD) and the ask in double quotes. Adding, removing, hiding and
// filtering are held alike. The page lock (internal/sprintdash/pagelock.go) holds the same
// rule at the build.

// DashboardAskCheck is the check's token.
const DashboardAskCheck = "dashboard-asked-for"

// DashboardAskRemedy is what the check wants.
const DashboardAskRemedy = "a card that changes the dashboard page (adds, removes, hides or filters anything on it) carries the owner's ask on its SOURCE: line, `SOURCE: the owner, <YYYY-MM-DD>: \"<his exact words asking for this change>\"`; with no such ask, take the page's files and the code that draws it out of PATHS:, or ask the owner first"

// DashboardPageDir is the page's files' directory.
const DashboardPageDir = "internal/sprintdash/page"

// DashboardRenderCode is the code that decides what the page draws: the page's copy and
// envelope, and the server that serves the page.
var DashboardRenderCode = []string{"internal/sprintdash/pageview.go", "internal/sprintdash/dashboard.go"}

// dashboardPageFiles is a file of the page, named for the glob and directory entries that
// reach it.
var dashboardPageFiles = []string{
	DashboardPageDir + "/index.html", DashboardPageDir + "/app.js", DashboardPageDir + "/nunito-800.woff2",
	DashboardPageDir + "/OFL.txt", DashboardPageDir + "/logo-robot-192.png", DashboardPageDir + "/logo-robot-384.png",
}

var (
	ownerWords = regexp.MustCompile(`(?i)\bthe owner\b`)
	askDate    = regexp.MustCompile(`\b20\d\d-\d\d-\d\d\b`)
	askQuote   = regexp.MustCompile(`["\x{201C}][^"\x{201D}]{3,}["\x{201D}]`)
)

// DashboardAsked says a SOURCE: value quotes the owner asking: "the owner", a date, and the
// ask in double quotes.
func DashboardAsked(source string) bool {
	return ownerWords.MatchString(source) && askDate.MatchString(source) && askQuote.MatchString(source)
}

// dashboardEntries is the entries of scope that reach the page's files or its render code.
func dashboardEntries(scope []string) []string {
	targets := append(slices.Clone(dashboardPageFiles), DashboardRenderCode...)
	var hit []string
	for _, e := range scope {
		c := cleanEntry(e)
		reaches := c == DashboardPageDir || strings.HasPrefix(c, DashboardPageDir+"/") ||
			slices.ContainsFunc(targets, func(t string) bool { return covers([]string{e}, t) })
		if reaches && !slices.Contains(hit, c) {
			hit = append(hit, c)
		}
	}
	return hit
}

// LintDashboardAsk holds a card brief to the dashboard rule (above): one finding when its
// PATHS: or NEW: reaches the page and its SOURCE: quotes no ask of the owner's; none else,
// and none for a brief with no PATHS: line (no card brief).
func LintDashboardAsk(raw []byte) []CardHeaderFinding {
	h, _ := cardHeaderBlock(raw)
	pathsF := h["PATHS"]
	if !pathsF.found {
		return nil
	}
	hit := dashboardEntries(append(headerList(pathsF.value), headerList(h["NEW"].value)...))
	if len(hit) == 0 || DashboardAsked(h["SOURCE"].value) {
		return nil
	}
	why := "SOURCE quotes no ask of the owner's"
	if !h["SOURCE"].found {
		why = "the card has no SOURCE line"
	}
	return []CardHeaderFinding{{Check: DashboardAskCheck, Line: max(pathsF.line, 1), Excerpt: fmt.Sprintf(
		"PATHS reaches the dashboard page (%s) and %s for this visible change: the page gains, loses, hides or filters nothing the owner did not ask for (the owner, 2026-10-11: \"NEVER REMOVE THINGS FROM THE DASHBOARD UNLESS I ASK\")",
		strings.Join(hit, ", "), why)}}
}
