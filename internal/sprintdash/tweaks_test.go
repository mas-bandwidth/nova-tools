package sprintdash

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPageCarriesGlennsTweaksOf20261004 asserts the 7 dashboard tweaks Glenn approved
// live on 2026-10-04 (2:41 PM and 2:55 PM):
//  1. The big tile numbers (.big) use the system font with tabular figures, not monospace.
//  2. The IN FLIGHT subline reads "<n> working · <n> verify" on one line.
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

	// Rig with fixture
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
	assert.NotContains(t, html, `getItem("sprint-theme")`, "the page never restores a light theme")
	assert.Contains(t, html, `<html lang="en" data-theme="dark">`, "the html element must stay dark")
	assert.NotContains(t, app, "syncThemeButton", "app.js must not carry syncThemeButton")

	// 1. The big tile numbers (.big) use system font with tabular figures
	bigRule := regexp.MustCompile(`\.big\s*\{[^}]*font-family:\s*-apple-system,\s*BlinkMacSystemFont,\s*"SF Pro Display",\s*"Inter",\s*system-ui,\s*sans-serif;[^}]*font-variant-numeric:\s*tabular-nums;[^}]*letter-spacing:\s*-\.01em;[^}]*\}`)
	assert.Regexp(t, bigRule, html, "index.html must define .big using system font with tabular figures")

	// 2. The IN FLIGHT subline markup
	assert.Contains(t, html, `.hero .sub { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }`, "hero subline must stay on one line")
	assert.Contains(t, html, `#inflight-sub .pn { color: var(--text); font-variant-numeric: tabular-nums; }`, "inflight numbers must use tabular figures")

	// 3. Cost breakdown legend in panel header on title's baseline markup
	spendPanel := doc.one(t, "spend panel", func(n *node) bool { return n.has("spend") })
	spendHead := spendPanel.one(t, "spend head", byClass("panel-head"))
	tierSub := spendHead.one(t, "tier legend", byID("tier-sub"))
	assert.True(t, tierSub.has("legend"), "tier-sub must have class legend")
	assert.Contains(t, html, `#tier-sub { flex-wrap: nowrap; white-space: nowrap; align-items: baseline; }`, "tier-sub must align to baseline")
	assert.Contains(t, html, `#tier-sub > span { align-items: baseline; }`, "tier-sub items must align to baseline")
	assert.Contains(t, html, `#tier-sub .ta { color: var(--text); font-family: var(--mono); font-variant-numeric: tabular-nums; }`, "tier-sub amount must be white tabular mono")

	// 6. Pie SVG markup
	assert.Contains(t, html, `<svg class="pie" id="pie" viewBox="0 0 100 100" aria-label="spend by tier"></svg>`, "pie SVG must have aria-label spend by tier")

	// Behavioral JS execution of app.js in Node
	jsRes := runJSTweaks(t, app)

	// 2 (behavior): In flight subline rendering
	assert.Equal(t, "14 working · 9 verify", jsRes.Inflight.Text)
	assert.Equal(t, "14 working, 6 review, 0 fix, 3 merging", jsRes.Inflight.Title)

	// 3, 4, 5, 6 (behavior): Cost breakdown with spend over $10:
	// - legend order: pro, flash (sorted descending by spend)
	// - $0 tier (heavy) and untiered omitted
	// - whole dollars rounded up ($15, $4)
	// - 4. Plain table headers: "stream", "pro", "flash", "total"
	// - 6. Pie slices drawn in legend order: pro then flash
	require.Len(t, jsRes.SpendOver10.TierSub, 2, "over-$10 legend must carry only tiers with >$0 spend")
	assert.Equal(t, "pro", jsRes.SpendOver10.TierSub[0].Name)
	assert.Equal(t, "$15", jsRes.SpendOver10.TierSub[0].Amount)
	assert.Equal(t, "flash", jsRes.SpendOver10.TierSub[1].Name)
	assert.Equal(t, "$4", jsRes.SpendOver10.TierSub[1].Amount)

	assert.Equal(t, []string{"stream", "pro", "flash", "total"}, jsRes.SpendOver10.HeadRow)

	require.Len(t, jsRes.SpendOver10.PiePaths, 2, "pie must draw slices for tiers with >$0 spend")
	assert.Equal(t, "var(--tier-pro)", jsRes.SpendOver10.PiePaths[0].Fill)
	assert.Equal(t, "pro $15 (79%)", jsRes.SpendOver10.PiePaths[0].Title)
	assert.Equal(t, "var(--tier-flash)", jsRes.SpendOver10.PiePaths[1].Fill)
	assert.Equal(t, "flash $4 (21%)", jsRes.SpendOver10.PiePaths[1].Title)

	// 5 (behavior): Cost breakdown with all tiers under $10:
	// - amounts formatted in cents ($4.10, $3.20)
	// - pie titles in cents
	require.Len(t, jsRes.SpendUnder10.TierSub, 2)
	assert.Equal(t, "pro", jsRes.SpendUnder10.TierSub[0].Name)
	assert.Equal(t, "$4.10", jsRes.SpendUnder10.TierSub[0].Amount)
	assert.Equal(t, "flash", jsRes.SpendUnder10.TierSub[1].Name)
	assert.Equal(t, "$3.20", jsRes.SpendUnder10.TierSub[1].Amount)

	assert.Equal(t, []string{"stream", "pro", "flash", "total"}, jsRes.SpendUnder10.HeadRow)

	require.Len(t, jsRes.SpendUnder10.PiePaths, 2)
	assert.Equal(t, "pro $4.10 (56%)", jsRes.SpendUnder10.PiePaths[0].Title)
	assert.Equal(t, "flash $3.20 (44%)", jsRes.SpendUnder10.PiePaths[1].Title)

	// 5 (behavior): dollars() rounding both ways (whole dollars rounded up)
	assert.Equal(t, "$1", jsRes.Dollars["1"])
	assert.Equal(t, "$1", jsRes.Dollars["100"])
	assert.Equal(t, "$2", jsRes.Dollars["101"])
	assert.Equal(t, "$10", jsRes.Dollars["999"])
	assert.Equal(t, "$10", jsRes.Dollars["1000"])
	assert.Equal(t, "$11", jsRes.Dollars["1001"])

	// 7 (behavior): machine pill states and the 60 s STALE threshold, through the real setMachine.
	// The page derives nothing from a clock: the threshold is the number in the machine line
	// ("tick late Ns"), so the cases are the lines either side of 60.
	cases := []struct {
		machineLine string
		wantPill    string
		wantStopped bool
		wantAlert   bool
	}{
		{machineLine: "machine: running (tick late 10s)", wantPill: "running"},
		{machineLine: "machine: running (tick late 59s)", wantPill: "running"},
		{machineLine: "machine: running (tick late 60s)", wantPill: "STALE"},
		{machineLine: "machine: running (tick late 120s)", wantPill: "STALE"},
		{machineLine: "machine: STOPPED", wantPill: "STOPPED", wantStopped: true, wantAlert: true},
		{machineLine: "machine: STOPPED (out of credit)", wantPill: "STOPPED", wantStopped: true, wantAlert: true},
	}
	require.Len(t, jsRes.Machines, len(cases))
	for i, cs := range cases {
		m := jsRes.Machines[i]
		require.Equal(t, cs.machineLine, m.Input)
		assert.Equal(t, cs.wantPill, m.Pill, "pill text for %s", cs.machineLine)
		assert.Equal(t, cs.wantStopped, m.OverallStopped, "overall stopped class for %s", cs.machineLine)
		assert.Equal(t, cs.wantAlert, m.ChipAlert, "chip alert for %s", cs.machineLine)
	}
}

type jsTweaksResult struct {
	Dollars  map[string]string `json:"dollars"`
	Machines []struct {
		Input          string `json:"input"`
		Pill           string `json:"pill"`
		ChipAlert      bool   `json:"chipAlert"`
		OverallStopped bool   `json:"overallStopped"`
	} `json:"machines"`
	Inflight struct {
		Text  string `json:"text"`
		Title string `json:"title"`
	} `json:"inflight"`
	SpendOver10 struct {
		TierSub []struct {
			Name   string `json:"name"`
			Amount string `json:"amount"`
		} `json:"tierSub"`
		HeadRow  []string `json:"headRow"`
		PiePaths []struct {
			Fill  string `json:"fill"`
			Title string `json:"title"`
		} `json:"piePaths"`
	} `json:"spendOver10"`
	SpendUnder10 struct {
		TierSub []struct {
			Name   string `json:"name"`
			Amount string `json:"amount"`
		} `json:"tierSub"`
		HeadRow  []string `json:"headRow"`
		PiePaths []struct {
			Fill  string `json:"fill"`
			Title string `json:"title"`
		} `json:"piePaths"`
	} `json:"spendUnder10"`
}

func runJSTweaks(t *testing.T, appJS string) *jsTweaksResult {
	t.Helper()

	// node runs the shipped app.js on a bench; the functional image does not carry it
	// (infra/functional-image/binaries.txt, row `node`), so absence skips outside NOVA_CI=1.
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}

	const runnerScript = jsDOMPrelude + `
// 1. dollars test
const dollarsCases = [1, 100, 101, 999, 1000, 1001];
const dollarsResults = {};
dollarsCases.forEach(c => { dollarsResults[c] = context.dollars(c); });

// 2. setMachine test
const machineCases = [
  "machine: running (tick late 10s)",
  "machine: running (tick late 59s)",
  "machine: running (tick late 60s)",
  "machine: running (tick late 120s)",
  "machine: STOPPED",
  "machine: STOPPED (out of credit)"
];
const machineResults = machineCases.map(line => {
  context.setMachine(line);
  return {
    input: line,
    pill: getEl("machine")._val || getEl("machine").textContent,
    chipAlert: getEl("machine-chip").classList.contains("alert"),
    overallStopped: getEl("overall").classList.contains("stopped")
  };
});

// 3. inflight test
context.renderInflight({ working: 14, review: 6, merging: 3 });
const inflightResult = {
  text: getEl("inflight-sub").textContent,
  title: getEl("inflight-sub").title
};

// 4. spendOver10 test
const fixtureOver10 = {
  tables: {
    work: {
      "stream-a": {
        cost: "$17.30",
        cost_by_tier: {
          flash: "$3.20",
          pro: "$14.10",
          heavy: "$0.00",
          untiered: "$99.99"
        }
      }
    }
  }
};
context.renderPie(fixtureOver10);
context.renderTopStreams(fixtureOver10);

const tierSubOver10 = getEl("tier-sub").children.map(sp => ({
  name: sp.children.find(c => c.className === "tn")?.textContent,
  amount: sp.children.find(c => c.className === "ta")?.textContent
}));

const headRowOver10 = getEl("top-streams").children[0].children.map(c => c.textContent);
const piePathsOver10 = getEl("pie").children
  .filter(c => c.tagName === "PATH")
  .map(p => ({
    fill: p.getAttribute("fill"),
    title: p.children[0]?.textContent
  }));

// 5. spendUnder10 test
const fixtureUnder10 = {
  tables: {
    work: {
      "stream-a": {
        cost: "$7.30",
        cost_by_tier: {
          flash: "$3.20",
          pro: "$4.10",
          heavy: "$0.00"
        }
      }
    }
  }
};
context.renderPie(fixtureUnder10);
context.renderTopStreams(fixtureUnder10);

const tierSubUnder10 = getEl("tier-sub").children.map(sp => ({
  name: sp.children.find(c => c.className === "tn")?.textContent,
  amount: sp.children.find(c => c.className === "ta")?.textContent
}));

const headRowUnder10 = getEl("top-streams").children[0].children.map(c => c.textContent);
const piePathsUnder10 = getEl("pie").children
  .filter(c => c.tagName === "PATH")
  .map(p => ({
    fill: p.getAttribute("fill"),
    title: p.children[0]?.textContent
  }));

process.stdout.write(JSON.stringify({
  dollars: dollarsResults,
  machines: machineResults,
  inflight: inflightResult,
  spendOver10: {
    tierSub: tierSubOver10,
    headRow: headRowOver10,
    piePaths: piePathsOver10
  },
  spendUnder10: {
    tierSub: tierSubUnder10,
    headRow: headRowUnder10,
    piePaths: piePathsUnder10
  }
}));
`

	inputData, err := json.Marshal(map[string]string{"appJS": appJS})
	require.NoError(t, err)

	cmd := exec.Command(nodePath, "-e", runnerScript)
	cmd.Stdin = bytes.NewReader(inputData)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	require.NoError(t, err, "node runner execution failed: %s", errBuf.String())

	var res jsTweaksResult
	err = json.Unmarshal(outBuf.Bytes(), &res)
	require.NoError(t, err, "unmarshaling node runner output failed: %s", outBuf.String())

	return &res
}

// jsDOMPrelude is the node script's start that loads app.js into a stubbed DOM: it reads
// {"appJS": ...} on stdin and leaves the page's functions on context and its elements by
// getEl. runJSTweaks and the stats reset's page test (stats_reset_test.go) run after it.
const jsDOMPrelude = `
const fs = require('fs');
const vm = require('vm');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const appCode = input.appJS;

function createDOMStub() {
  const elements = new Map();
  function createElement(tag, id = '') {
    const _classes = new Set();
    const el = {
      tagName: tag.toUpperCase(),
      id: id,
      _classes: _classes,
      get className() { return Array.from(_classes).join(' '); },
      set className(val) {
        _classes.clear();
        if (val) String(val).split(/\s+/).filter(Boolean).forEach(c => _classes.add(c));
      },
      get classList() {
        const self = this;
        return {
          add(...c) { c.forEach(x => _classes.add(x)); },
          remove(...c) { c.forEach(x => _classes.delete(x)); },
          toggle(c, force) {
            if (force === undefined) force = !_classes.has(c);
            if (force) _classes.add(c); else _classes.delete(c);
            return force;
          },
          contains(c) { return _classes.has(c); }
        };
      },
      children: [],
      style: {
        setProperty(k, v) { this[k] = v; },
        getPropertyValue(k) { return this[k] || ''; }
      },
      attributes: {},
      addEventListener() {},
      setAttribute(k, v) { this.attributes[k] = String(v); },
      getAttribute(k) { return this.attributes[k] || null; },
      appendChild(c) {
        if (typeof c === 'string') c = { textContent: c, children: [] };
        this.children.push(c);
        c.parentNode = this;
        return c;
      },
      insertBefore(c, ref) {
        if (c.parentNode) c.parentNode.removeChild(c);
        const idx = ref ? this.children.indexOf(ref) : -1;
        if (idx >= 0) this.children.splice(idx, 0, c); else this.children.push(c);
        c.parentNode = this;
        return c;
      },
      get nextSibling() {
        if (!this.parentNode) return null;
        const s = this.parentNode.children;
        return s[s.indexOf(this) + 1] || null;
      },
      get lastChild() { return this.children[this.children.length - 1] || null; },
      dataset: {},
      removeChild(c) {
        const idx = this.children.indexOf(c);
        if (idx >= 0) this.children.splice(idx, 1);
        c.parentNode = null;
        return c;
      },
      get firstChild() { return this.children[0] || null; },
      get innerHTML() { return ''; },
      set innerHTML(val) { this.children = []; },
      get textContent() {
        if (this.children.length > 0) return this.children.map(c => c.textContent).join('');
        return this._text || '';
      },
      set textContent(val) { this._text = val; this.children = []; },
      querySelector(sel) { return createElement('div'); },
      querySelectorAll(sel) { return []; },
      remove() { if (this.parentNode) this.parentNode.removeChild(this); },
      cloneNode(deep) { return createElement(tag, id); },
      scrollWidth: 50,
      clientWidth: 100
    };
    return el;
  }

  function getEl(id) {
    if (!elements.has(id)) {
      elements.set(id, createElement('div', id));
    }
    return elements.get(id);
  }

  const context = {
    window: { addEventListener() {} },
    document: {
      getElementById: getEl,
      createElement: createElement,
      createElementNS: (ns, tag) => createElement(tag),
      createTextNode: (t) => ({ textContent: t, children: [] }),
      documentElement: { fontSize: '16px' }
    },
    getComputedStyle: () => ({ fontSize: '16px' }),
    location: { search: '' },
    fetch: () => Promise.resolve({ ok: true, json: () => Promise.resolve({}) }),
    setInterval: () => {},
    console: console,
    Intl: Intl,
    Math: Math,
    Date: Date,
    String: String,
    Number: Number,
    parseInt: parseInt,
    parseFloat: parseFloat,
    isNaN: isNaN,
    Array: Array,
    Object: Object,
    RegExp: RegExp
  };

  vm.createContext(context);
  vm.runInContext(appCode, context);
  return { context, getEl };
}

const { context, getEl } = createDOMStub();
`
