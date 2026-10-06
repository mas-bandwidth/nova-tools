package sprintdash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scrollShim is the minimal DOM this test owns (no browser is available to the page tests): a
// node tree with identity, parents and siblings, write counting, and a viewer that holds a key
// down. The viewer models what a browser does to a held PageDown: the page scrolls while the
// node under the viewer (the anchor, a row it is looking at) stays attached; when a redraw
// replaces that node the key loses its target and the scroll restarts from the top.
const scrollShim = `
const fs = require('fs');
const vm = require('vm');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));

let writes = 0, redundant = 0;
class Node {
  constructor(tag, id) {
    this.tagName = String(tag).toUpperCase(); this.id = id || ''; this.parentNode = null; this.children = [];
    this._classes = []; this._text = ''; this.attributes = {}; this._title = ''; this._listeners = {};
    this.dataset = {}; this.style = { setProperty(k, v) { this[k] = v; }, getPropertyValue(k) { return this[k] || ''; } }; this.hidden = false;
    this.scrollWidth = 50; this.clientWidth = 100; this.offsetWidth = 0; this.offsetParent = {};
  }
  get className() { return this._classes.join(' '); }
  set className(v) { const n = String(v).split(/\s+/).filter(Boolean); if (n.join(' ') !== this.className) writes++; this._classes = n; }
  get classList() {
    const s = this;
    return {
      add(...c) { c.forEach(x => { if (!s._classes.includes(x)) { s._classes.push(x); writes++; } }); },
      remove(...c) { c.forEach(x => { const i = s._classes.indexOf(x); if (i >= 0) { s._classes.splice(i, 1); writes++; } }); },
      toggle(c, on) { if (on === undefined) on = !s._classes.includes(c); if (on) this.add(c); else this.remove(c); return on; },
      contains(c) { return s._classes.includes(c); },
    };
  }
  get title() { return this._title; }
  set title(v) { if (v !== this._title) writes++; this._title = v; }
  setAttribute(k, v) { v = String(v); if (this.attributes[k] !== v) writes++; this.attributes[k] = v; }
  getAttribute(k) { return k in this.attributes ? this.attributes[k] : null; }
  addEventListener(t, f) { (this._listeners[t] = this._listeners[t] || []).push(f); }
  removeEventListener() {}
  _detach() { if (this.parentNode) { const i = this.parentNode.children.indexOf(this); this.parentNode.children.splice(i, 1); this.parentNode = null; } }
  appendChild(c) { writes++; c._detach(); c.parentNode = this; this.children.push(c); return c; }
  insertBefore(c, ref) {
    writes++; c._detach(); c.parentNode = this;
    if (ref) this.children.splice(this.children.indexOf(ref), 0, c); else this.children.push(c);
    return c;
  }
  removeChild(c) { writes++; c._detach(); return c; }
  remove() { writes++; this._detach(); }
  get lastChild() { return this.children[this.children.length - 1] || null; }
  get firstChild() { return this.children[0] || null; }
  get nextSibling() { if (!this.parentNode) return null; const s = this.parentNode.children; return s[s.indexOf(this) + 1] || null; }
  get isConnected() { let n = this; while (n.parentNode) n = n.parentNode; return n === doc.root; }
  get textContent() { return this.children.length ? this.children.map(c => c.textContent).join('') : this._text; }
  _clear(v, html) {
    if (this.isConnected && (html === undefined ? this._html === undefined && this.textContent === v : this._html === html)) redundant++;
    this._html = html;
    writes++; this.children.forEach(c => { c.parentNode = null; }); this.children = []; this._text = v;
  }
  set textContent(v) { this._clear(String(v)); }
  get innerHTML() { return this._html === undefined ? this.textContent : this._html; }
  set innerHTML(v) { v = String(v); this._clear('', v); }
  cloneNode() { const n = new Node(this.tagName, this.id); n._classes = this._classes.slice(); n._text = this._text; n.children = this.children.map(c => { const k = c.cloneNode(); k.parentNode = n; return k; }); return n; }
  all() { return this.children.flatMap(c => [c, ...c.all()]); }
  querySelector(sel) { const cl = sel.split('.').filter(Boolean); return this.all().find(n => cl.every(c => n._classes.includes(c))) || null; }
  querySelectorAll() { return []; }
}
const doc = { root: new Node('html'), byId: new Map(), activeElement: null, _l: {}, documentElement: {} };
doc.body = doc.root.appendChild(new Node('body'));
doc.getElementById = id => {
  if (!doc.byId.has(id)) {
    const n = new Node('div', id); doc.byId.set(id, n); doc.body.appendChild(n);
    if (['streams', 'fleet', 'friends', 'lanes'].includes(id)) { const h = new Node('div'); h.className = 'row head'; h.appendChild(new Node('div')); n.appendChild(h); }
  }
  return doc.byId.get(id);
};
doc.createElement = t => new Node(t);
doc.createElementNS = (ns, t) => new Node(t);
doc.createTextNode = t => { const n = new Node('#text'); n._text = t; return n; };
doc.querySelector = () => null;
doc.addEventListener = (t, f) => { (doc._l[t] = doc._l[t] || []).push(f); };
doc.dispatch = (t, ev) => (doc._l[t] || []).forEach(f => f(ev));

let timer = null, nextBody = null;
const context = {
  window: { addEventListener() {} }, document: doc, getComputedStyle: () => ({ fontSize: '16px' }), location: { search: '' },
  fetch: () => Promise.resolve({ ok: true, json: () => Promise.resolve(nextBody) }),
  setInterval: f => { timer = f; }, console, Intl, Math, Date, String, Number, parseInt, parseFloat, isNaN, Array, Object, RegExp, Map, Set, JSON,
};
vm.createContext(context);
vm.runInContext(input.appJS, context);
const tick = async () => { timer(); await new Promise(r => setImmediate(r)); await new Promise(r => setImmediate(r)); };

let draws = 0; const render = context.render; context.render = d => { draws++; return render(d); };

const roots = ['streams', 'fleet', 'friends', 'lanes', 'top-streams', 'wall', 'wall-legend', 'tier-sub', 'inflight-sub', 'release'].map(doc.getElementById);
const nodes = () => new Set(roots.flatMap(r => r.all()));
const rowOf = (id, text) => doc.getElementById(id).children.find(r => r.textContent.startsWith(text));

// the viewer: holds PageDown; each repeat scrolls one page while its anchor is attached, else restarts at the top
const viewer = { scrollTop: 0, resets: 0, anchor: null };
doc.addEventListener('keydown', ev => {
  if (ev.key !== 'PageDown') return;
  if (viewer.anchor && viewer.anchor.isConnected) viewer.scrollTop += 600; else { viewer.scrollTop = 0; viewer.resets++; }
});

(async () => {
  const out = { steps: [] };
  nextBody = { build: 'b1', data: input.snaps[0] }; await tick();
  viewer.anchor = doc.activeElement = rowOf('top-streams', 'beta'); const streamAnchor = rowOf('streams', 'beta');
  out.anchorFound = !!viewer.anchor && !!streamAnchor;
  let before = nodes(); const drawsAt = () => draws;
  for (let i = 1; i < input.snaps.length; i++) {
    const w0 = writes, r0 = redundant, d0 = drawsAt(), s0 = viewer.scrollTop;
    nextBody = { build: 'b1', data: input.snaps[i] }; await tick();
    doc.dispatch('keydown', { key: 'PageDown' });
    const now = nodes(); const lost = [...before].filter(n => !n.isConnected && !n.parentNode);
    out.steps.push({
      name: input.names[i], draws: drawsAt() - d0, writes: writes - w0, redundant: redundant - r0,
      lost: lost.map(n => n.textContent.slice(0, 20)), scrollTop: viewer.scrollTop, advanced: viewer.scrollTop > s0,
      anchorKept: !!viewer.anchor && viewer.anchor.isConnected && doc.activeElement === viewer.anchor && !!streamAnchor && streamAnchor.isConnected,
    });
    before = now;
  }
  out.resets = viewer.resets;
  process.stdout.write(JSON.stringify(out));
})();
`

type scrollStep struct {
	Name       string   `json:"name"`
	Draws      int      `json:"draws"`
	Writes     int      `json:"writes"`
	Redundant  int      `json:"redundant"`
	Lost       []string `json:"lost"`
	ScrollTop  int      `json:"scrollTop"`
	Advanced   bool     `json:"advanced"`
	AnchorKept bool     `json:"anchorKept"`
}

type scrollRun struct {
	AnchorFound bool         `json:"anchorFound"`
	Steps       []scrollStep `json:"steps"`
	Resets      int          `json:"resets"`
}

// scrollSnapshots is a sequence of polls over the fixture: the first draw; the same snapshot
// again (byte-identical); a count change in one stream; a new stream; the new stream gone.
func scrollSnapshots(t *testing.T) (names []string, snaps []map[string]any) {
	t.Helper()
	var base map[string]any
	require.NoError(t, json.Unmarshal(fixture(t), &base))
	work := base["tables"].(map[string]any)["work"].(map[string]any)
	for _, n := range []string{"alpha", "beta", "gamma"} {
		work[n] = map[string]any{"cost": "$4.00", "landed": "1", "merging": "0", "ready": "1", "review": "0", "waiting": "2", "working": "1",
			"tiers": map[string]any{"pro": "1"}, "cost_by_tier": map[string]any{"pro": "$4.00"}}
	}
	clone := func(m map[string]any) map[string]any {
		b, err := json.Marshal(m)
		require.NoError(t, err)
		var c map[string]any
		require.NoError(t, json.Unmarshal(b, &c))
		return c
	}
	step := func(name string, mutate func(w map[string]any, d map[string]any)) {
		c := clone(snaps0(snaps, base))
		if mutate != nil {
			mutate(c["tables"].(map[string]any)["work"].(map[string]any), c)
		}
		names, snaps = append(names, name), append(snaps, c)
	}
	step("first", nil)
	step("identical", nil)
	step("count", func(w, d map[string]any) {
		w["beta"].(map[string]any)["working"] = "2"
		w["beta"].(map[string]any)["cost"] = "$4.50"
		d["at"] = "2026-10-03T11:20:01-04:00"
	})
	step("added", func(w, d map[string]any) {
		w["delta"] = map[string]any{"cost": "$1.00", "landed": "0", "merging": "0", "ready": "0", "review": "0", "waiting": "1", "working": "0",
			"cost_by_tier": map[string]any{"pro": "$1.00"}}
		d["at"] = "2026-10-03T11:20:02-04:00"
	})
	step("removed", func(w, d map[string]any) {
		delete(w, "delta")
		d["at"] = "2026-10-03T11:20:03-04:00"
	})
	return names, snaps
}

// snaps0 is the snapshot a step builds on: the last one so far, or the base for the first.
func snaps0(snaps []map[string]any, base map[string]any) map[string]any {
	if len(snaps) == 0 {
		return base
	}
	return snaps[len(snaps)-1]
}

// TestARedrawNeverInterruptsScrolling holds PageDown through the redraws of four polls
// (docs: Glenn, 2026-10-05, "it stops at random places"): no node the viewer is on is replaced,
// the tables are patched in place (a node survives a poll unless its row left; no write sets
// text a node already had), a poll with an identical snapshot draws nothing, and the scroll
// position advances at every repeat with no reset. The DOM is a minimal shim owned by this
// test (scrollShim); a node on the machine runs the shipped app.js against it.
func TestARedrawNeverInterruptsScrolling(t *testing.T) {
	t.Parallel()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("NOVA_CI") == "1" {
			require.NoError(t, err, "node is required for the dashboard JS behavioural test")
		}
		t.Skip("node is not installed on this machine; the dashboard JS behavioural test needs it")
	}
	names, snaps := scrollSnapshots(t)
	in, err := json.Marshal(map[string]any{"appJS": string(file("app.js")), "names": names, "snaps": snaps})
	require.NoError(t, err)

	cmd := exec.Command(nodePath, "-e", scrollShim)
	cmd.Stdin = bytes.NewReader(in)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	require.NoError(t, cmd.Run(), "node runner failed: %s", errBuf.String())
	var res scrollRun
	require.NoError(t, json.Unmarshal(outBuf.Bytes(), &res), outBuf.String())

	require.Empty(t, errBuf.String(), "app.js threw while drawing")
	require.True(t, res.AnchorFound, "the first draw must put beta in the streams table and the top-streams table")
	require.Len(t, res.Steps, 4)
	assert.Zero(t, res.Resets, "the scroll position was reset by a redraw")
	last := -1
	for _, s := range res.Steps {
		assert.Greater(t, s.ScrollTop, last, "%s: the scroll position must advance monotonically", s.Name)
		last = s.ScrollTop
		assert.True(t, s.Advanced, "%s: a held PageDown must advance", s.Name)
		assert.True(t, s.AnchorKept, "%s: the node the viewer is on was replaced", s.Name)
		assert.Zero(t, s.Redundant, "%s: a node was rewritten with the text it already had", s.Name)
		switch s.Name {
		case "identical":
			assert.Zero(t, s.Draws, "an identical snapshot must draw nothing")
			assert.Zero(t, s.Writes, "an identical snapshot must write nothing")
		case "removed":
			for _, l := range s.Lost {
				assert.Contains(t, l, "delta", "%s: only the removed stream's rows may leave: %q", s.Name, l)
			}
		default:
			assert.Empty(t, s.Lost, fmt.Sprintf("%s: nodes were replaced by the redraw", s.Name))
		}
	}
}
