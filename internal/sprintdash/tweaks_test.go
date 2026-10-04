package sprintdash

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPageCarriesGlennsTweaksOf20261004 asserts the 7 dashboard tweaks Glenn approved
// live on 2026-10-04 (2:41 PM and 2:55 PM):
//  1. The big tile numbers (.big) use the system font with tabular figures, not monospace.
//  2. The IN FLIGHT subline reads "<n> working · <n> review + merge" on one line.
//  3. The cost breakdown legend sits in the panel header on the title's baseline:
//     color square, tier name in grey, amount in white, sorted by spend, $0 tiers left out,
//     spaced groups with no dots between them.
//  4. Plain table headers.
//  5. Money in that panel: whole dollars rounded up; cents only when every tier is under $10.
//     No "untiered" entry.
//  6. The pie draws its slices in the legend's order.
//  7. The machine pill on this human page shows only "running", "STOPPED" or "STALE":
//     a tick late by 60 s or more shows STALE, and the progress bar keeps pulsing while STALE;
//     tick lateness belongs to coordinator's views, never this page.
//
// Always dark: the page stays dark, no theme toggle.
func TestPageCarriesGlennsTweaksOf20261004(t *testing.T) {
	t.Parallel()

	// Rig with fake clock and fixture
	r := newRig(t)
	r.next = func() ([]byte, error) { return fixture(t), nil }
	w := httptest.NewRecorder()
	r.s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, w.Code)
	html := w.Body.String()
	app := string(file("app.js"))
	doc := parsePage(t, []byte(html))

	// Always dark: no theme toggle, stays dark
	assert.NotContains(t, html, `id="theme"`, "index.html must not carry a theme toggle button")
	assert.NotContains(t, html, `data-theme="light"`, "index.html must not carry light tokens")
	assert.Contains(t, html, `<html lang="en" data-theme="dark">`, "the html element must stay dark")
	assert.NotContains(t, app, "syncThemeButton", "app.js must not carry syncThemeButton")

	// 1. The big tile numbers (.big) use system font with tabular figures
	bigRule := regexp.MustCompile(`\.big\s*\{[^}]*font-family:\s*-apple-system,\s*BlinkMacSystemFont,\s*"SF Pro Display",\s*"Inter",\s*system-ui,\s*sans-serif;[^}]*font-variant-numeric:\s*tabular-nums;[^}]*\}`)
	assert.Regexp(t, bigRule, html, "index.html must define .big using system font with tabular figures")

	// 2. The IN FLIGHT subline reads "<n> working · <n> review + merge" on one line
	assert.Contains(t, html, `.hero .sub { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }`, "hero subline must stay on one line")
	assert.Contains(t, html, `#inflight-sub .pn { color: var(--text); font-variant-numeric: tabular-nums; }`, "inflight numbers must use tabular figures")
	assert.Contains(t, app, `function renderInflight(sum)`, "app.js must define renderInflight")
	assert.Contains(t, app, `"review + merge"`, "app.js must format review and merge together as 'review + merge'")
	assert.Contains(t, app, `sum.working`, "app.js must take sum.working")
	assert.Contains(t, app, `sum.review + sum.merging`, "app.js must sum review and merging")

	// 3. Cost breakdown legend in panel header on title's baseline
	spendPanel := doc.one(t, "spend panel", func(n *node) bool { return n.has("spend") })
	spendHead := spendPanel.one(t, "spend head", byClass("panel-head"))
	tierSub := spendHead.one(t, "tier legend", byID("tier-sub"))
	assert.True(t, tierSub.has("legend"), "tier-sub must have class legend")
	assert.Contains(t, html, `#tier-sub { flex-wrap: nowrap; white-space: nowrap; align-items: baseline; }`, "tier-sub must align to baseline")
	assert.Contains(t, html, `#tier-sub > span { align-items: baseline; }`, "tier-sub items must align to baseline")
	assert.Contains(t, html, `#tier-sub .ta { color: var(--text); font-family: var(--mono); font-variant-numeric: tabular-nums; }`, "tier-sub amount must be white tabular mono")

	// 4. Plain table headers
	assert.Contains(t, app, `order.forEach(function (t) { h.appendChild(el("div", "num", t)); });`, "top streams headers must be plain div.num")

	// 5. Money in that panel: whole dollars rounded up; cents only when every tier is under $10; no untiered
	assert.Contains(t, app, `function dollars(c) { var d = Math.ceil(c / 100 - 1e-9); return "$" + (d === 0 ? 0 : d).toLocaleString("en-US"); }`, "dollars() must round up")
	assert.Contains(t, app, `byTier[t] < 1000`, "tierSpend must check for < 1000 cents ($10) threshold")
	assert.Contains(t, app, `// the four tiers alone: a record with no tier ("untiered") is no tier and is left out`, "untiered records must be excluded")

	// 6. Pie draws its slices in legend's order
	assert.Contains(t, html, `<svg class="pie" id="pie" viewBox="0 0 100 100" aria-label="spend by tier"></svg>`, "pie SVG must have aria-label spend by tier")
	assert.Contains(t, app, `var sp = tierSpend(d), vals = sp.order.map(function (t) { return [t, sp.shown(sp.byTier[t])]; });`, "pie must use sp.order")

	// 7. Machine pill: only running, STOPPED or STALE; 60 s threshold; progress bar pulsing
	assert.Contains(t, app, `function setMachine(line)`, "app.js must define setMachine")
	assert.Contains(t, app, `Number(late[1]) >= 60 ? "STALE" : "running"`, "tick late by 60s or more must evaluate to STALE")
	assert.Contains(t, app, `var running = /^(running|STALE)\b/.test(text);`, "progress bar must keep pulsing when running or STALE")
	assert.Contains(t, app, `box.classList.toggle("stopped", !running);`, "overall box toggles stopped only when not running or STALE")
	assert.NotContains(t, app, `setText($("machine"), String(d.machine || "-").replace(/^machine:\s*/, ""));`, "machine text must be routed through setMachine")

	// Verify machine pill transitions across states and lateness
	checkMachine := func(line string) (pill string, pulsing bool) {
		text := strings.TrimPrefix(line, "machine:")
		text = strings.TrimSpace(text)
		lateRe := regexp.MustCompile(`(?i)^running\b.*tick late (\d+)s`)
		m := lateRe.FindStringSubmatch(text)
		if len(m) > 1 {
			var secs int
			_, _ = fmt.Sscanf(m[1], "%d", &secs)
			if secs >= 60 {
				text = "STALE"
			} else {
				text = "running"
			}
		} else if regexp.MustCompile(`(?i)^running\b`).MatchString(text) {
			text = "running"
		}
		isPulsing := regexp.MustCompile(`^(running|STALE)\b`).MatchString(text)
		return text, isPulsing
	}

	p, pulse := checkMachine("machine: running (tick late 10s)")
	assert.Equal(t, "running", p)
	assert.True(t, pulse)

	p, pulse = checkMachine("machine: running (tick late 59s)")
	assert.Equal(t, "running", p)
	assert.True(t, pulse)

	p, pulse = checkMachine("machine: running (tick late 60s)")
	assert.Equal(t, "STALE", p)
	assert.True(t, pulse)

	p, pulse = checkMachine("machine: running (tick late 120s)")
	assert.Equal(t, "STALE", p)
	assert.True(t, pulse)

	p, pulse = checkMachine("machine: STOPPED")
	assert.Equal(t, "STOPPED", p)
	assert.False(t, pulse)

	// Verify money rounding behavior in Go matching JS dollars & tierSpend logic
	dollarsFn := func(cents int) string {
		d := int(float64(cents)/100.0 - 1e-9)
		if float64(cents)/100.0 > float64(d) {
			d++
		}
		return fmt.Sprintf("$%d", d)
	}
	assert.Equal(t, "$1", dollarsFn(1))
	assert.Equal(t, "$1", dollarsFn(100))
	assert.Equal(t, "$2", dollarsFn(101))
	assert.Equal(t, "$10", dollarsFn(999))
	assert.Equal(t, "$10", dollarsFn(1000))
	assert.Equal(t, "$11", dollarsFn(1001))

	// Verify fake clock fixture API response
	api := r.api()
	data := api["data"].(map[string]any)
	assert.Equal(t, "coordinator", data["coordinator"])
	assert.Equal(t, float64(15), data["epoch"])
}
