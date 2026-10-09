package sprintdash

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// headerDotCaseResult records the header state rendered by app.js for a machine line.
type headerDotCaseResult struct {
	Name        string `json:"name"`
	Input       string `json:"input"`
	LiveActive  bool   `json:"liveActive"`
	MachineText string `json:"machineText"`
	ChipClass   string `json:"chipClass"`
	ChipAlert   bool   `json:"chipAlert"`
	LiveClass   string `json:"liveClass"`
	LiveOk      bool   `json:"liveOk"`
	LiveStopped bool   `json:"liveStopped"`
	DotClass    string `json:"dotClass"`
	DotStopped  bool   `json:"dotStopped"`
}

// TestHeaderDotTurnsRedWhenMachineStopped asserts Glenn's request of 2026-10-09:
// When the machine is STOPPED, the green dot before "Updated <time>" turns red,
// reusing the machine STOPPED badge red (var(--critical)).
// When running and fresh, the dot is green (var(--good)).
// When unreachable or connecting, existing unlit/stale color is kept as it is.
func TestHeaderDotTurnsRedWhenMachineStopped(t *testing.T) {
	t.Parallel()

	app := string(file("app.js"))
	results := runHeaderDotTest(t, app)
	byName := make(map[string]headerDotCaseResult)
	for _, r := range results {
		byName[r.Name] = r
	}

	// 1. Stopped machine: the badge is red (.alert) and the live dot is red (.stopped)
	stopped, ok := byName["stopped"]
	require.True(t, ok, "stopped case must be present")
	assert.Equal(t, "STOPPED", stopped.MachineText)
	assert.True(t, stopped.ChipAlert, "chip alert must be true when machine is STOPPED")
	assert.Contains(t, stopped.ChipClass, "alert")
	assert.True(t, stopped.LiveOk, "live ok must be true when data has arrived")
	assert.True(t, stopped.LiveStopped, "live stopped must be true when machine is STOPPED")
	assert.Equal(t, "live ok stopped", stopped.LiveClass)
	assert.True(t, stopped.DotStopped, "dot must have stopped class when machine is STOPPED")
	assert.Equal(t, "dot stopped", stopped.DotClass)

	// 2. Stopped machine with out-of-credit note: badge and dot both red
	stoppedCredit, ok := byName["stopped_credit"]
	require.True(t, ok, "stopped_credit case must be present")
	assert.Equal(t, "STOPPED", stoppedCredit.MachineText)
	assert.True(t, stoppedCredit.ChipAlert)
	assert.True(t, stoppedCredit.LiveStopped)
	assert.True(t, stoppedCredit.DotStopped)
	assert.Equal(t, "dot stopped", stoppedCredit.DotClass)

	// 3. Running machine: dot is green (no .stopped class)
	running, ok := byName["running"]
	require.True(t, ok, "running case must be present")
	assert.Equal(t, "running", running.MachineText)
	assert.False(t, running.ChipAlert, "chip alert must be false when machine is running")
	assert.True(t, running.LiveOk, "live ok must be true")
	assert.False(t, running.LiveStopped, "live stopped must be false when machine is running")
	assert.Equal(t, "live ok", running.LiveClass)
	assert.False(t, running.DotStopped, "dot stopped must be false when machine is running")
	assert.Equal(t, "dot", running.DotClass)

	// 4. Stale machine: still running, not stopped
	stale, ok := byName["stale"]
	require.True(t, ok, "stale case must be present")
	assert.Equal(t, "STALE", stale.MachineText)
	assert.False(t, stale.ChipAlert)
	assert.True(t, stale.LiveOk)
	assert.False(t, stale.LiveStopped)
	assert.False(t, stale.DotStopped)
	assert.Equal(t, "dot", stale.DotClass)

	// 5. Unreachable machine before data arrives: dot keeps neutral / unreachable color
	unreachable, ok := byName["unreachable_stopped"]
	require.True(t, ok, "unreachable_stopped case must be present")
	assert.False(t, unreachable.LiveOk, "live ok must be false when unreachable")
	assert.False(t, unreachable.LiveStopped, "live stopped must not be active without ok")
	assert.False(t, unreachable.DotStopped, "dot stopped must not be active without ok")
	assert.Equal(t, "live", unreachable.LiveClass)
	assert.Equal(t, "dot", unreachable.DotClass)
}

// TestHeaderDotCssAndMarkupIntegrity verifies CSS and HTML rules:
// - Reusing badge red (var(--critical)) for the stopped dot
// - Dark mode stays the default
// - No unasked text or panels added to the header
func TestHeaderDotCssAndMarkupIntegrity(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.next = func() ([]byte, error) { return fixture(t), nil }
	w := httptest.NewRecorder()
	r.s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, w.Code)
	html := w.Body.String()

	// Dark mode stays the default
	assert.Contains(t, html, `<html lang="en" data-theme="dark">`, "html element stays dark")

	// Machine badge uses var(--critical)
	assert.Contains(t, html, ".chip.alert { border-color: var(--critical); background: var(--critical-tint); }")
	assert.Contains(t, html, ".chip.alert b { color: var(--critical); }")

	// Green dot rule
	assert.Contains(t, html, ".live.ok .dot { background: var(--good); }")

	// Stopped dot rule reuses var(--critical) matching the badge
	assert.Contains(t, html, ".live.ok.stopped .dot")
	assert.Contains(t, html, "background: var(--critical);")

	// Header markup preserves clean connecting text without extra labels
	assert.Contains(t, html, `<span class="live" id="live"><span class="dot"></span><span id="live-text">Connecting</span></span>`)
}

func runHeaderDotTest(t *testing.T, appJS string) []headerDotCaseResult {
	t.Helper()

	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}

	const runnerScript = `
const fs = require('fs');
const vm = require('vm');

const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const appCode = input.appJS;

function createDOMStub() {
  const elements = new Map();
  function createElement(tag, id = '') {
    const _classes = new Set();
    const children = [];
    const el = {
      tagName: tag.toUpperCase(),
      id: id,
      _classes: _classes,
      children: children,
      get className() { return Array.from(_classes).join(' '); },
      set className(val) {
        _classes.clear();
        if (val) String(val).split(/\s+/).filter(Boolean).forEach(c => _classes.add(c));
      },
      get classList() {
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
        children.push(c);
        c.parentNode = el;
        return c;
      },
      insertBefore(c, ref) {
        if (c.parentNode) c.parentNode.removeChild(c);
        const idx = ref ? children.indexOf(ref) : -1;
        if (idx >= 0) children.splice(idx, 0, c); else children.push(c);
        c.parentNode = el;
        return c;
      },
      removeChild(c) {
        const idx = children.indexOf(c);
        if (idx >= 0) children.splice(idx, 1);
        c.parentNode = null;
        return c;
      },
      querySelector(sel) {
        const cls = sel.replace(/^\./, '');
        return children.find(c => c._classes && c._classes.has(cls)) || null;
      },
      get firstChild() { return children[0] || null; },
      get textContent() { return children.map(c => c.textContent).join('') || el._text || ''; },
      set textContent(val) { el._text = val; children.length = 0; }
    };
    if (id) elements.set(id, el);
    return el;
  }
  function getEl(id) {
    if (!elements.has(id)) {
      elements.set(id, createElement('div', id));
    }
    return elements.get(id);
  }

  // Pre-populate header elements matching index.html
  const live = getEl('live');
  live.className = 'live';
  const dot = createElement('span');
  dot.className = 'dot';
  live.appendChild(dot);
  const liveText = createElement('span', 'live-text');
  liveText.textContent = 'Connecting';
  live.appendChild(liveText);

  getEl('machine-chip').className = 'chip';

  const context = {
    window: { addEventListener() {} },
    document: {
      getElementById: getEl,
      createElement: createElement,
      createElementNS: (ns, t) => createElement(t),
      createTextNode: (t) => ({ textContent: t, children: [] }),
      documentElement: { fontSize: '16px' }
    },
    getComputedStyle: () => ({ fontSize: '16px' }),
    location: { search: '' },
    fetch: () => Promise.resolve({ ok: true, json: () => Promise.resolve({}) }),
    setInterval: () => {},
    console: console,
    Intl, Math, Date, String, Number, parseInt, parseFloat, isNaN, Array, Object, RegExp, Map, Set, JSON
  };
  vm.createContext(context);
  vm.runInContext(appCode, context);
  return { context, getEl };
}

const cases = [
  { name: 'stopped', line: 'machine: STOPPED', live: true },
  { name: 'stopped_credit', line: 'machine: STOPPED (out of credit)', live: true },
  { name: 'running', line: 'machine: running', live: true },
  { name: 'stale', line: 'machine: running (tick late 60s)', live: true },
  { name: 'unreachable_stopped', line: 'machine: STOPPED', live: false }
];

const results = cases.map(c => {
  const { context, getEl } = createDOMStub();
  context.setMachine(c.line);
  if (c.live) {
    context.setLive(new Date('2026-10-09T18:00:00Z'));
  }
  const live = getEl('live');
  const dot = live.querySelector('.dot') || live.children[0];
  const chip = getEl('machine-chip');
  const machine = getEl('machine');
  return {
    name: c.name,
    input: c.line,
    liveActive: c.live,
    machineText: machine._val || machine.textContent,
    chipClass: chip.className,
    chipAlert: chip.classList.contains('alert'),
    liveClass: live.className,
    liveOk: live.classList.contains('ok'),
    liveStopped: live.classList.contains('stopped'),
    dotClass: dot ? dot.className : '',
    dotStopped: dot ? dot.classList.contains('stopped') : false
  };
});

process.stdout.write(JSON.stringify(results));
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

	var res []headerDotCaseResult
	err = json.Unmarshal(outBuf.Bytes(), &res)
	require.NoError(t, err, "unmarshaling node runner output failed: %s", outBuf.String())

	return res
}
