package swarm

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// UNKNOWNERROR AT ANY WALL IS THE PROVIDER'S, AND THE CARD GOES TO THE NEXT ROUTE (issue
// #2916).
//
// The launch grace (#900, launchfail.go) retries a provider server error that kills the
// harness inside its first 15 s. Past the grace the same words were read as the card's own
// failure: 264 cards of the 2026-09-22 sprint ended on
//
//	Error: {"name":"UnknownError","data":{"message":"Unexpected server error. ...","ref":"err_fb35c63e"}}
//
// at 75 s, 4 min, 11 min -- filed `NATIVE INCOMPLETE why=no-result`, scored against the
// model, and re-dealt to the route that had just failed them. The wall at which the provider
// answered 5xx says nothing about the card: the class is PROVIDER-5XX whenever the harness's
// own last words are the provider's, and the card is handed back to the NEXT route with the
// failed one named for the re-deal to avoid.
//
// This does not retry. A card that ran for minutes did work, and re-running it is a new
// deal, not a relaunch of the same request -- which is the launcher's decision, made on the
// line this writes.

// ProviderClass5xx is the class word the hand-back line carries in place of INCOMPLETE.
const ProviderClass5xx = "PROVIDER-5XX"

// RoutesEnv names the ordered route list (provider/model, comma or space separated) the
// launcher hands the run, so the run can name the route after the one that failed.
const RoutesEnv = "NOVA_SWARM_ROUTES"

// providerTailLines is how far back from the harness's end the provider's words are looked
// for. The harness prints its error last; the card's own tool output from earlier in the
// run (a grep that found the words in a fixture) is not the provider's.
const providerTailLines = 20

// providerAnyWallRE is the provider's own words for a server error: opencode's UnknownError
// envelope, the provider's `err_xxxxxxxx` reference, and the launch grace's server-error
// words (launchFailureRE), so one classifier covers the grace and everything past it.
var providerAnyWallRE = regexp.MustCompile(`(?i)\bUnknownError\b|\berr_[0-9a-f]{8}\b|unexpected server error|internal server error|\b(502|503|529)\b`)

// providerErrRefRE pulls the reference out of either spelling: `"ref":"err_..."` in the
// JSON envelope, or `ref=err_...` on a plain line.
var providerErrRefRE = regexp.MustCompile(`"ref"\s*:\s*"([A-Za-z0-9_.:/-]+)"|ref=([A-Za-z0-9_.:/-]+)|\b(err_[0-9a-f]{8})\b`)

// ProviderExit is what one finished harness exit is judged on.
type ProviderExit struct {
	Tail   []byte        // the harness's capture; only its last lines are read
	Job    string        // the job directory: a published RESULT.md is the card's own end
	RC     int           // the harness exit code; a clean exit is never the provider's
	Wall   time.Duration // how long the run took: carried on the line, never a condition
	Route  string        // the route that failed (provider/model)
	Routes []string      // the ordered route list; the next one is dealt after Route
}

// Handback is a card handed back to the next route.
type Handback struct {
	Class string        // always ProviderClass5xx
	Ref   string        // the provider's reference, "" when the words carried none
	Wall  time.Duration // the wall at which the provider failed the card
	Route string        // the failed route; the re-deal avoids it
	Next  string        // the route after it in list order, "" when there is none
}

// ProviderHandback classes one harness exit. ok is true when the harness exited non-zero,
// the card published no report, and the harness's last lines are the provider's server
// error -- at any wall.
func ProviderHandback(e ProviderExit) (Handback, bool) {
	if e.RC == 0 {
		return Handback{}, false
	}
	if strings.TrimSpace(e.Job) != "" {
		if _, published := FindCardResult(e.Job); published {
			return Handback{}, false
		}
	}
	last := lastLines(e.Tail, providerTailLines)
	if !providerAnyWallRE.MatchString(last) {
		return Handback{}, false
	}
	h := Handback{Class: ProviderClass5xx, Wall: e.Wall, Route: strings.TrimSpace(e.Route)}
	if m := providerErrRefRE.FindStringSubmatch(last); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				h.Ref = g
				break
			}
		}
	}
	h.Next = NextRoute(e.Routes, h.Route)
	return h, true
}

// NextRoute is the route after failed in list order, wrapping at the end, never failed
// itself; a failed route not on the list hands back to the list's first other route. ""
// when the list has nowhere else to go.
func NextRoute(routes []string, failed string) string {
	at := -1
	for i, r := range routes {
		if r == failed {
			at = i
			break
		}
	}
	for k := 1; k <= len(routes); k++ {
		r := routes[(at+k+len(routes))%len(routes)]
		if r != "" && r != failed {
			return r
		}
	}
	return ""
}

// Line is the one line the launcher reads:
//
//	NATIVE PROVIDER-5XX label=<l> ref=<ref|-> wall=<s>s route=<failed> next=<route|-> avoid=<failed>
func (h Handback) Line(label string) string {
	return fmt.Sprintf("NATIVE %s label=%s ref=%s wall=%.2fs route=%s next=%s avoid=%s",
		h.Class, oneline.Field(label), oneline.Field(dashIfEmpty(h.Ref)), h.Wall.Seconds(),
		oneline.Field(dashIfEmpty(h.Route)), oneline.Field(dashIfEmpty(h.Next)), oneline.Field(dashIfEmpty(h.Route)))
}

// ParseRouteList reads RoutesEnv's value: routes separated by commas or white space, empty
// entries dropped, first occurrence winning.
func ParseRouteList(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ' ' || c == '\t' || c == '\n' }) {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// lastLines is the last n non-empty lines of raw, joined.
func lastLines(raw []byte, n int) string {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	var keep []string
	for i := len(lines) - 1; i >= 0 && len(keep) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			keep = append(keep, lines[i])
		}
	}
	return strings.Join(keep, "\n")
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return Dash
	}
	return s
}
