// Sprint dashboard: keeps /events open (each new copy of the server's cached
// `where --json` pushed as it is read) and patches the DOM in place; while the
// stream is not open it polls /api/sprint every second on a fixed timer, never
// after an answer. Nothing is blanked on a failed poll; the page holds the last
// data and says nothing.
"use strict";

// Left to right in the bars: done first, so the bar fills like progress. fix is the cards awaiting
// rework, purple, between review and merging (docs/SPEC-SPRINT-DASHBOARD.md, "Fix"; the owner,
// 2026-10-07: "between review and merging"); the view (dashboard.go) counts it on the copy.
var STATES = ["landed", "merging", "fix", "review", "working", "ready", "waiting"];
// Columns of the streams table, in flow order.
var FLOW = ["waiting", "ready", "working", "review", "fix", "merging", "landed"];
// A Work row's children: stream, status, then FLOW's counts, then cost.
var LANDED_AT = 2 + FLOW.indexOf("landed"), COST_AT = 2 + FLOW.length;
var POLL_MS = 1000;
var MIN_CELL = 4;  // px: a cell never gets narrower; cards per cell grows instead
var GAP = 2;       // px between cells
var TRACK_CELL = 1.6875, TRACK_GAP = 0.25; // rem: a fleet track cell and its gap (27 px and 4 px on a desktop), never scaled
var $ = function (id) { return document.getElementById(id); };

// ---------- parsing (every value in the JSON is a string) ----------
function int(s) { var n = parseInt(s, 10); return isNaN(n) ? 0 : n; }
function pct(s) { var n = parseFloat(String(s || "").replace("%", "")); return isNaN(n) ? null : n; }
function cents(s) { // "$26.16" -> 2616, "-" -> null; rounded up to the cent
  var n = parseFloat(String(s || "").replace(/[$,]/g, ""));
  return isNaN(n) ? null : Math.ceil(n * 100 - 1e-6);
}
function money(c) { return "$" + (c / 100).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 }); }
// whole dollars rounded up, for the Cost breakdown panel only ("Round up to nearest $", "for this case"); money() keeps the cent everywhere else
function dollars(c) { var d = Math.ceil(c / 100 - 1e-9); return "$" + (d === 0 ? 0 : d).toLocaleString("en-US"); }
function zoneAbbr(d) {
  try { var p = new Intl.DateTimeFormat("en-US", { timeZoneName: "short" }).formatToParts(d).filter(function (x) { return x.type === "timeZoneName"; })[0]; return p ? p.value : ""; }
  catch (e) { return ""; }
}
function etaAround(when, ms) {
  var day = ms >= 86400000 ? when.toLocaleDateString("en-US", { weekday: "short" }) + " " : "";
  return ("around " + day + clockShort(when) + " " + zoneAbbr(when)).trim();
}
// a tile's subline stays on one line: when it does not fit the tile,
// the shorter forms in turn
function fits(e) { return e.scrollWidth <= e.clientWidth + 1; }
var etaAtLast = null;
function fitEtaAt() {
  var box = $("eta-at"); if (!box || !etaAtLast) return;
  var full = etaAround(etaAtLast[0], etaAtLast[1]), forms = [full, full.replace(/^around /, ""), full.replace(/^around /, "").replace(/ [A-Z]{2,5}$/, "")];
  for (var i = 0; i < forms.length; i++) { setText(box, forms[i]); if (fits(box)) return; }
}
function clock(d) { return d.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit", second: "2-digit" }); }
function clockShort(d) { return d.toLocaleTimeString("en-US", { hour: "numeric", minute: "2-digit" }); }
function etaMs(s) { // "2h20m" -> ms
  var m = String(s).match(/^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/);
  if (!m || !(m[1] || m[2] || m[3])) return null;
  return ((int(m[1]) * 60 + int(m[2])) * 60 + int(m[3])) * 1000;
}
function etaText(s) { return String(s).replace(/(\d+[hms])(?=\d)/g, "$1 "); }

// ---------- DOM helpers ----------
function el(tag, cls, text) {
  var e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}
// Emphasis: a value flashes only when it differs from the value rendered the
// refresh before. Each node keeps its last rendered value (e._val); the first
// render stores without flashing. The highlight lives on an inline span
// (.fv) inside the node, so the tint hugs the digits, not the cell. A track
// cell flashes only when it goes lit <-> unlit. The clock never flashes
// (setLiveHTML does not use these helpers).
["all", "all2", "pct", "eta", "eta-at", "cost", "cost-per", "cost-unreconciled", "inflight", "inflight-sub", "tput", "coord", "epoch", "machine",
 "streams-sub", "fleet-head", "friends-sub", "readers-sub"].forEach(function (id) { var e = document.getElementById(id); if (e) quiet(e); });
function valEl(e) {
  if (!e._fv) {
    var v = document.createElement("span"); v.className = "fv";
    while (e.firstChild) v.appendChild(e.firstChild);
    e.appendChild(v); e._fv = v;
  }
  return e._fv;
}
function flash(t) {
  t.classList.remove("flash"); void t.offsetWidth; t.classList.add("flash");
  if (!t._flashEnd) { t._flashEnd = function () { t.classList.remove("flash"); }; t.addEventListener("animationend", t._flashEnd); }
}
// What flashes: stream counts, the hero landed count, friends' counts, and
// track cells (the fleet's numbers never flash, only its cells). Everything else (ok%, load, the
// fleet's done, cost, the ETA, other hero tiles, legend, pills, subtitles) is
// marked quiet and updates without emphasis.
function quiet(e) { e._quiet = true; return e; }
function setText(e, s) {
  s = String(s);
  if (e._val === s) return;
  var first = e._val == null; e._val = s;
  if (e._quiet) { e.textContent = s; return; }
  valEl(e).textContent = s;
  if (!first) flash(e._fv);
}
function setHTML(e, s) {
  if (e._val === s) return;
  var first = e._val == null; e._val = s;
  if (e._quiet) { e.innerHTML = s; return; }
  valEl(e).innerHTML = s;
  if (!first) flash(e._fv);
}
function setClass(e, c) {
  if (e._cls === c) return;
  var first = e._cls == null; e._cls = c;
  var on = e.classList.contains("flash");
  e.className = c;
  if (on) e.classList.add("flash");
  if (!first && e.classList.contains("cell") && !(e.parentNode && e.parentNode.id === "overall")) flash(e);
}
function setTitle(e, t) { if (e.title !== t) e.title = t; }
function numCell(cls) { return el("div", cls === "frac" ? "frac" : "num " + (cls || "")); }
// A list of children patched in place: grown or shrunk at the end, each child reused by position
// and written only when its text or class changed (a redraw never replaces a node it can keep).
function setCount(box, n, make) {
  while (box.children.length < n) box.appendChild(make());
  while (box.children.length > n) box.lastChild.remove();
}
function putKid(box, i, cls, text) {
  var c = box.children[i];
  if (!c._quiet) quiet(c);
  setText(c, text); setClass(c, cls);
  return c;
}
function setNum(e, v, extra) { setText(e, v); setClass(e, "num " + (extra || "") + (String(v) === "0" ? " zero" : "")); }

// Keyed rows between a fixed header and an optional total row; nodes are
// reused and only moved when out of order, so nothing flickers.
function syncRows(box, header, keys, build, update, total) {
  if (!box._map) { box._map = new Map(); box.textContent = ""; box.appendChild(header); }
  var map = box._map, seen = new Set(), prev = header;
  keys.forEach(function (k) {
    var r = map.get(k);
    if (!r) { r = build(k); map.set(k, r); }
    update(r, k);
    seen.add(k);
    if (prev.nextSibling !== r.node) box.insertBefore(r.node, prev.nextSibling);
    prev = r.node;
  });
  map.forEach(function (r, k) { if (!seen.has(k)) { r.node.remove(); map.delete(k); } });
  if (total && prev.nextSibling !== total) box.insertBefore(total, prev.nextSibling);
}
// The header rows of Work, Fleet, Friends and Lanes are the page's own markup (index.html), the
// strings of docs/SPEC-SPRINT-DASHBOARD.md, which a test holds equal: each table's header is
// a clone of the row the page carries.
var HEADS = {};
["streams", "fleet", "friends", "lanes"].forEach(function (id) {
  var h = document.getElementById(id).querySelector(".row.head");
  if (h) { h.remove(); HEADS[id] = h; }
});
function pageHead(id) { return HEADS[id].cloneNode(true); }
function headRow(labels) {
  var h = el("div", "row head");
  labels.forEach(function (l) { h.appendChild(el("div", l[1] || "", l[0])); });
  return h;
}

// ---------- segmented bars ----------
// setCells(box, classes, columns): one grid cell per entry of classes ("" is
// an empty slot); cells are reused, so a refresh only repaints what changed.
function setCells(box, classes, columns, cellRem) {
  // cellRem: fixed cell width (the fleet track, so the figure can sit right after it); else cells share the box
  var cols = "repeat(" + Math.max(1, columns) + ", " + (cellRem ? cellRem + "rem" : "minmax(0, 1fr)") + ")";
  if (box.style.gridTemplateColumns !== cols) box.style.gridTemplateColumns = cols;
  while (box.children.length < classes.length) box.appendChild(el("div", "cell"));
  while (box.children.length > classes.length) box.lastChild.remove();
  classes.forEach(function (c, i) { setClass(box.children[i], c ? "cell " + c : "cell"); });
}
// How many cells fit across a box at MIN_CELL px each.
function cellsThatFit(box) {
  var w = box.clientWidth || 600;
  return Math.max(1, Math.floor((w + GAP) / (MIN_CELL + GAP)));
}
// Cards per cell from 1, 2, 3, 4, 5, 10, 20, 25, 50, 100: the smallest that fits.
function cardsPerCell(total, fit) {
  var ladder = [1, 2, 3, 4, 5, 10, 20, 25, 50, 100];
  for (var i = 0; i < ladder.length; i++) if (Math.ceil(total / ladder[i]) <= fit) return ladder[i];
  return Math.ceil(total / fit);
}
// Share n cells among the states by largest remainder; a state with any
// cards keeps at least one cell, so nothing in flight disappears.
function allocate(counts, total, n) {
  var out = {}, used = 0, rem = [];
  STATES.forEach(function (st) {
    var exact = total ? counts[st] * n / total : 0, f = Math.floor(exact);
    if (counts[st] > 0 && f === 0) f = 1;
    out[st] = f; used += f; rem.push([exact - Math.floor(exact), st]);
  });
  rem.sort(function (a, b) { return b[0] - a[0]; });
  for (var i = 0; used < n && i < rem.length; i++) { if (counts[rem[i][1]] > 0) { out[rem[i][1]]++; used++; } }
  while (used > n) { // the minimum-one rule overshot: take from the largest
    var big = STATES.reduce(function (a, b) { return out[a] >= out[b] ? a : b; });
    out[big]--; used--;
  }
  return out;
}
// Same share as allocate, over an explicit key list (the stopped share sits in the bar
// beside merging without joining STATES, which the legend and the spec keep to seven).
function allocateKeys(keys, counts, total, n) {
  var out = {}, used = 0, rem = [];
  keys.forEach(function (st) {
    var exact = total ? (counts[st] || 0) * n / total : 0, f = Math.floor(exact);
    if ((counts[st] || 0) > 0 && f === 0) f = 1;
    out[st] = f; used += f; rem.push([exact - Math.floor(exact), st]);
  });
  rem.sort(function (a, b) { return b[0] - a[0]; });
  for (var i = 0; used < n && i < rem.length; i++) { if ((counts[rem[i][1]] || 0) > 0) { out[rem[i][1]]++; used++; } }
  while (used > n) {
    var big = keys.reduce(function (a, b) { return out[a] >= out[b] ? a : b; });
    out[big]--; used--;
  }
  return out;
}
// Merging cards that sit in a stopped stream: the page's split of the merging count.
// where --json's stream State is the word "stopped" (the cause is not on the row).
function stoppedMerging(d) {
  var work = (d && d.tables && d.tables.work) || {};
  var merge = (d && d.tables && d.tables.merge) || {};
  var state = {};
  (d && d.streams || []).forEach(function (s) { if (s && s.Stream) state[s.Stream] = String(s.State || ""); });
  var cards = 0, streams = 0;
  Object.keys(work).forEach(function (k) {
    var st = state[k] || String((merge[k] || {}).state || "");
    if (st.indexOf("stopped") !== 0) return;
    var m = int(work[k].merging);
    if (m > 0) { cards += m; streams++; }
  });
  return { cards: cards, streams: streams };
}
function stoppedLabel(n, streams) {
  return n + " in a stopped stream" + ((streams || 1) === 1 ? "" : "s");
}

// VU meter track: exactly `slots` cells (the machine's width), on a grid of
// `scale` columns (the widest member's width) so the cells line up down the
// column; the lit cells first, the rest dark; nothing past the width. The lit cells run in the
// priority ladder, highest on the left (docs/SPEC-SPRINT-DASHBOARD.md, "Fix"; the owner,
// 2026-10-07: "to the left of read cards, and to the right of critical cards"): blocker,
// critical, fix (purple), reads (one orange cell per two reads, a lone read a whole cell), then
// the working blue; a row's counts are where's <level>_working and the view's fix.
var TRACK_LEVELS = [["blocker_working", "p-blocker", "blocker"], ["critical_working", "p-critical", "critical"], ["fix", "p-fix", "fix"]];
function trackSegs(m) {
  var working = int(m.working), segs = [], words = [], cards = 0;
  TRACK_LEVELS.forEach(function (l) {
    var n = int(m[l[0]]); cards += n;
    for (var j = 0; j < n; j++) segs.push(l[1]);
    if (n) words.push(n + " " + l[2]);
  });
  var reads = int(m.reads_working); cards += reads;
  for (var j = 0; j < Math.ceil(reads / 2); j++) segs.push("p-reader");
  if (reads) words.push(reads + " read" + (reads === 1 ? "" : "s"));
  for (var k = cards; k < working; k++) segs.push("working");
  return { segs: segs, words: words };
}
function setTrack(box, value, slots, scale, m) {
  var t = m ? trackSegs(m) : { segs: [], words: [] }, classes = [];
  if (!m) for (var j = 0; j < value; j++) t.segs.push("working");
  for (var i = 0; i < slots; i++) classes.push(t.segs[i] || "");
  setCells(box, classes, scale, TRACK_CELL);
  setTitle(box, value + " working of " + slots + (t.words.length ? ": " + t.words.join(", ") : ""));
}
// "a / b" as a block of fixed width: a right-aligned in `digits` character widths, b
// left-aligned in as many, so the slash of every row in a column sits on one vertical line
// and the block can be right-aligned in its column like any number.
function frac(a, b, digits) {
  return "<span class=\"fa\" style=\"width:" + digits + "ch\">" + a + "</span><span class=\"fs\"> / </span><span class=\"fb\" style=\"width:" + digits + "ch\">" + b + "</span>";
}
function escHTML(s) { return String(s).replace(/[&<>"']/g, function (c) { return "&#" + c.charCodeAt(0) + ";"; }); }
function digitsOf(n) { return String(Math.max(0, n)).length; }
function makePill() { var p = el("span", "pill neutral"); p.appendChild(el("span", "dot")); p._t = quiet(el("span")); p.appendChild(p._t); return p; }
function setPill(p, text, tone, title) { setText(p._t, text); setClass(p, "pill " + tone); setTitle(p, title || text); }
function setOk(o, p, done) { setText(o, p === null ? "-" : p.toFixed(1) + "%"); setClass(o, "num" + (done ? "" : " zero")); }
var STATUS_TONE = { up: "good", held: "warning", down: "critical" };

// ---------- sections ----------
function streamOrder(d) {
  var work = d.tables.work || {}, keys = [], seen = {};
  (d.streams || []).forEach(function (s) { if (work[s.Stream] && !seen[s.Stream]) { keys.push(s.Stream); seen[s.Stream] = 1; } });
  Object.keys(work).sort().forEach(function (k) { if (!seen[k]) keys.push(k); });
  return keys;
}
function streamStatus(state, c, total) {
  if (total > 0 && c.landed === total) return ["landed", "done"];
  if (String(state || "").indexOf("stopped") === 0) return ["stopped", "critical"];
  if (total > 0 && c.waiting === total) return ["held", "warning"];
  if (c.ready + c.working + c.review + (c.fix || 0) + c.merging > 0) return ["working", "active"];
  if (state === "landed") return ["landed", "done"];
  return [state || "idle", "neutral"];
}

// The archived streams (stream archive; the owner, 2026-10-05: "I would like you to remove all
// the already landed work streams"): off the table by default, one line saying how many, the
// cards landed in them and their cost, which shows them or hides them again when clicked. The
// total row, the progress bar and the hero count only the streams on the table, shown or not
// (the owner, 2026-10-06: "I really don't think we have 2.8k cards post-archive..."); the
// archived line carries theirs.
var showArchived = false, lastStreams = null;
function archivedSet(d) { var a = {}; ((d.archived || {}).streams || []).forEach(function (s) { a[s] = 1; }); return a; }
function renderArchived(d) {
  var b = $("streams-archived"), a = d.archived;
  if (!b._on) { b._on = 1; b.addEventListener("click", function () { showArchived = !showArchived; if (lastStreams) render(lastStreams); }); }
  b.hidden = !a;
  if (!a) return;
  var n = a.streams.length;
  setText(b, n + " archived stream" + (n === 1 ? "" : "s") + ", " + a.landed + " card" + (a.landed === 1 ? "" : "s") + " landed, " + a.cost + " · " + (showArchived ? "hide" : "show"));
}

function renderStreams(d) {
  lastStreams = d;
  renderArchived(d);
  var box = $("streams"), work = d.tables.work || {}, merge = d.tables.merge || {}, arch = archivedSet(d);
  var states = {}; (d.streams || []).forEach(function (s) { states[s.Stream] = s; });
  // children: 1 stream, 2 status, 3 waiting, 4 ready, 5 working, 6 review, 7 fix, 8 merging, 9 landed, 10 cost
  if (!box._head) {
    box._head = pageHead("streams");
    box._total = el("div", "row total");
    box._total._c = [el("div", "", "Total"), el("div")];
    FLOW.forEach(function (st) { box._total._c.push(numCell(st === "landed" ? "frac" : "")); });
    box._total._c.push(quiet(numCell()));
    box._total._c.forEach(function (c) { box._total.appendChild(c); });
  }
  // Rows are grouped by status (landed, working, stopped, held), by name within a group,
  // with a thin rule between groups; each row also carries its status pill. Rows are keyed by stream, so a reorder
  // only moves nodes and flashes nothing.
  var RANK = { landed: 0, working: 1, stopped: 2, held: 3 };
  var statusOf = {};
  streamOrder(d).forEach(function (k) {
    var c = {}, total = 0; FLOW.forEach(function (st) { c[st] = int(work[k][st]); total += c[st]; });
    statusOf[k] = streamStatus((states[k] || {}).State, c, total)[0];
  });
  var rank = function (k) { return RANK[statusOf[k]] == null ? 3.5 : RANK[statusOf[k]]; };
  var keys = streamOrder(d).filter(function (k) { return showArchived || !arch[k]; }).sort(function (a, b) { return rank(a) - rank(b) || (a < b ? -1 : a > b ? 1 : 0); });
  var sum = { cost: 0, totalCost: 0, workCost: 0, readCost: 0, unreconciled: 0, unpriced: 0 }, held = 0, landedStreams = 0, prevRank = null;
  // the epoch's spend, every stream's, the archived ones' too: the cost tile's scope once the
  // sprint is done (where --json's done), when the table's streams are all archived
  var epoch = { totalCost: 0, workCost: 0, readCost: 0, unpriced: 0 };
  streamOrder(d).forEach(function (k) {
    var sc = (d.stream_costs || {})[k] || {};
    var tc = cents(sc.total_cost); if (tc) epoch.totalCost += tc;
    var wc = cents(sc.work_cost); if (wc) epoch.workCost += wc;
    var rc = cents(sc.read_cost); if (rc) epoch.readCost += rc;
    epoch.unpriced += int(sc.unpriced_runs);
  });
  sum.epoch = epoch;
  var digits = digitsOf(streamOrder(d).reduce(function (a, k) { return a + FLOW.reduce(function (b, st) { return b + int(work[k][st]); }, 0); }, 0));
  FLOW.forEach(function (st) { sum[st] = 0; });
  syncRows(box, box._head, keys, function () {
    var r = { node: el("div", "row"), n: {} };
    r.name = el("div", "name"); r.nameT = el("span"); r.tag = el("span", "tag");
    r.name.appendChild(r.nameT); r.name.appendChild(r.tag);
    r.pill = makePill();
    r.node.appendChild(r.name); r.node.appendChild(r.pill);
    FLOW.forEach(function (st) { r.n[st] = numCell(st === "landed" ? "frac" : ""); r.node.appendChild(r.n[st]); });
    r.cost = quiet(numCell()); r.node.appendChild(r.cost);
    return r;
  }, function (r, k) {
    var w = work[k], m = merge[k] || {}, s = states[k] || {}, c = {}, total = 0;
    FLOW.forEach(function (st) { c[st] = int(w[st]); total += c[st]; });
    var ct = cents(w.cost);
    // an archived stream shown is drawn, and counted only on its archived line
    if (!arch[k]) {
      FLOW.forEach(function (st) { sum[st] += c[st]; });
      if (ct) sum.cost += ct;
      var sc = (d.stream_costs || {})[k] || {};
      var tc = cents(sc.total_cost); if (tc) sum.totalCost += tc;
      // the reads beside the work: the same total split by kind (sprint.TierCosts)
      var wc = cents(sc.work_cost); if (wc) sum.workCost += wc;
      var rc = cents(sc.read_cost); if (rc) sum.readCost += rc;
      sum.unpriced += int(sc.unpriced_runs);
    }
    var status = statusOf[k];
    if (status === "held") held++;
    if (status === "landed") landedStreams++;
    if (r.node.dataset.status !== status) r.node.dataset.status = status;
    r.node.classList.toggle("group-start", prevRank !== null && rank(k) !== prevRank);
    prevRank = rank(k);
    setText(r.nameT, k);
    var tone = { landed: "done", working: "active", held: "warning", stopped: "critical stopped" }[status] || "neutral";
    var label = status, raw = String(s.State || "");
    if (status === "stopped") {
      var rest = raw.replace(/^stopped:?\s*/, "");
      if (rest && rest !== "stopped") label = "stopped: " + rest;
    }
    setPill(r.pill, label, tone, label + (s.State ? " · stream " + s.State + (s.Since ? " since " + clockShort(new Date(s.Since)) : "") : ""));
    // an archived stream shown is marked: the total row leaves it out
    var tags = arch[k] ? ["archived"] : []; if (int(m.stuck) > 0) tags.push(m.stuck + " stuck"); if (m.ci === "red") tags.push("ci red");
    setText(r.tag, tags.join(" · "));
    FLOW.forEach(function (st) { if (st !== "landed") setNum(r.n[st], c[st]); });
    setHTML(r.n.landed, frac(c.landed, total, digits));
    setText(r.cost, ct === null ? "-" : money(ct)); setClass(r.cost, "num" + (ct === null ? " zero" : ""));
  }, box._total);
  // the sprint's unreconciled spend rides on every stream's record, an archived one's too:
  // read once, never summed
  Object.keys(d.stream_costs || {}).forEach(function (k) {
    var uc = cents(d.stream_costs[k].unreconciled); if (uc) sum.unreconciled = Math.max(sum.unreconciled, uc);
  });
  var tc = box._total._c, all = 0;
  FLOW.forEach(function (st, i) { all += sum[st]; if (st !== "landed") setNum(tc[2 + i], sum[st]); });
  setHTML(tc[LANDED_AT], frac(sum.landed, all, digits));
  setText(tc[COST_AT], money(sum.cost));
  setText($("streams-sub"), keys.length + " streams · " + landedStreams + " landed · " + held + " held");
  // the "landed" header is centred over its n / total cell: same width as the cell, text centred
  var lw = tc[LANDED_AT].offsetWidth ? tc[LANDED_AT].offsetWidth + "px" : "";
  if (lw && box._head.children[LANDED_AT].style.width !== lw) box._head.children[LANDED_AT].style.width = lw;
  return { sum: sum, all: all };
}

var overallLast = null;
var BAR_STOPPED = ["landed", "merging", "s-stopped", "fix", "review", "working", "ready", "waiting"];
function renderOverall(sum, all, stop) {
  stop = stop || { cards: 0, streams: 0 };
  var stopped = Math.min(stop.cards || 0, sum.merging || 0);
  overallLast = { sum: sum, all: all, stop: { cards: stopped, streams: stop.streams || 0 } };
  var box = $("overall"), per = cardsPerCell(all, cellsThatFit(box)), n = Math.ceil(all / per);
  var classes = [];
  if (stopped > 0) {
    var counts = {};
    STATES.forEach(function (st) { counts[st] = sum[st] || 0; });
    counts.merging -= stopped;
    counts["s-stopped"] = stopped;
    var share = allocateKeys(BAR_STOPPED, counts, all, n);
    BAR_STOPPED.forEach(function (st) { for (var i = 0; i < (share[st] || 0); i++) classes.push(st); });
  } else {
    var plain = allocate(sum, all, n);
    STATES.forEach(function (st) { for (var j = 0; j < plain[st]; j++) classes.push(st); });
  }
  setCells(box, classes, n);
  setTitle(box, (per === 1 ? "one cell per card" : "one cell per " + per + " cards") + " · " +
    STATES.map(function (st) { return st + " " + sum[st]; }).join(", ") +
    (stopped ? " · " + stoppedLabel(stopped, stop.streams) : ""));
  var lg = $("legend");
  if (!lg._items) {
    lg._items = {};
    STATES.forEach(function (st) {
      var it = el("span"); it.appendChild(el("i", "sw " + st)); var t = quiet(el("span")); it.appendChild(t);
      lg._items[st] = t; lg.appendChild(it);
    });
  }
  STATES.forEach(function (st) {
    var text = st + " " + sum[st];
    if (st === "merging" && stopped > 0) text = "merging " + sum.merging + " (" + stoppedLabel(stopped, stop.streams) + ")";
    setText(lg._items[st], text);
  });
}
window.addEventListener("resize", function () { if (overallLast) renderOverall(overallLast.sum, overallLast.all, overallLast.stop); });

// Where wall time goes: one stacked bar of the stages' medians over the cards landed in the
// last 24 h (where --json stage_times, docs/SPEC-SPRINT.md), shown once there is one.
var WALL_STAGES = [["needs", "waiting on needs"], ["deal", "ready, not dealt"], ["take", "dealt, not taken"], ["work", "work"],
  ["rework", "rework"], ["read_wait", "waiting for a read"], ["read", "read"], ["accept", "waiting to accept"], ["merge", "merge queue"]];
var WALL_HUES = [210, 190, 170, 140, 0, 45, 30, 280, 320];
function wallSpan(s) { return s < 90 ? Math.round(s) + " s" : s < 5400 ? Math.round(s / 60) + " min" : (s / 3600).toFixed(1) + " h"; }
function renderWall(d) {
  var panel = $("wall-panel"), st = d.stage_times && d.stage_times.all;
  var names = st ? WALL_STAGES.filter(function (x) { return st[x[0]]; }) : [];
  panel.hidden = names.length === 0;
  if (!names.length) return;
  var box = $("wall"), lg = $("wall-legend");
  var total = names.reduce(function (a, x) { return a + st[x[0]].median_s; }, 0);
  setCount(box, names.length, function () { return el("i"); });
  setCount(lg, names.length, function () { var it = el("span"); it.appendChild(el("i", "sw")); it.appendChild(quiet(el("span"))); return it; });
  names.forEach(function (x, i) {
    var k = x[0], m = st[k], hue = WALL_HUES[WALL_STAGES.findIndex(function (y) { return y[0] === k; })];
    var seg = box.children[i], grow = String(Math.max(m.median_s, total / 400)), bg = "hsl(" + hue + " 60% 55%)";
    if (seg.style.flexGrow !== grow) seg.style.flexGrow = grow;
    if (seg.style.background !== bg) seg.style.background = bg;
    setTitle(seg, x[1] + ": median " + wallSpan(m.median_s) + ", p90 " + wallSpan(m.p90_s) + " over " + m.n + " cards");
    var it = lg.children[i];
    if (it.children[0].style.background !== bg) it.children[0].style.background = bg;
    setText(it.children[1], x[1] + " " + wallSpan(m.median_s) + " (p90 " + wallSpan(m.p90_s) + ")");
  });
  setText(quiet($("wall-sub")), "median per stage, cards landed in the last 24 h · " + wallSpan(total) + " in all");
}

function fleetLike(box, table, withLoad) {
  var names = Object.keys(table || {});
  var rank = { up: 0, held: 1, down: 2 };
  function rk(n) { var r = rank[table[n].status]; return r == null ? 3 : r; }
  names.sort(function (a, b) { return rk(a) - rk(b) || a.localeCompare(b); });
  if (!box._head) {
    box._head = pageHead(box.id);
    box._total = el("div", "row total");
    box._total._c = [el("div", "", "Total"), el("div"), quiet(numCell()), quiet(el("div")), el("div"), quiet(numCell()), quiet(numCell())];
    // one more than the shared cells: the fleet's load, the friends' tokens
    box._total._c.push(el("div"));
    box._total._c.forEach(function (c) { box._total.appendChild(c); });
  }
  var t = { ready: 0, done: 0, ok: 0, up: 0, held: 0, down: 0, fix: 0 };
  var scale = Math.max(1, names.reduce(function (a, n) { return Math.max(a, int(table[n].width)); }, 0));
  // the track column is exactly the widest track, so the figure sits right after it
  var tw = (scale * (TRACK_CELL + TRACK_GAP) - TRACK_GAP).toFixed(3) + "rem";
  if (box.style.getPropertyValue("--track-w") !== tw) box.style.setProperty("--track-w", tw);
  var digits = Math.max(digitsOf(names.reduce(function (a, n) { return a + int(table[n].working); }, 0)), digitsOf(scale));
  syncRows(box, box._head, names, function () {
    var r = { node: el("div", "row") };
    r.name = el("div", "name"); r.pill = makePill(); r.track = el("div", "cells"); r.wf = numCell("frac");
    // fleet and friends: no numeric column flashes, only the cells
    r.wf = quiet(r.wf); r.ready = quiet(numCell()); r.done = quiet(numCell()); r.ok = quiet(numCell());
    [r.name, r.pill, r.ready, r.track, r.wf, r.done, r.ok].forEach(function (c) { r.node.appendChild(c); });
    if (withLoad) { r.load = quiet(numCell()); r.node.appendChild(r.load); }
    else { r.tokens = quiet(numCell()); r.node.appendChild(r.tokens); }
    return r;
  }, function (r, k) {
    var m = table[k], working = int(m.working), width = int(m.width), done = int(m.done);
    var okv = m.okpct != null ? m.okpct : m["ok%"];
    t.ready += int(m.ready); t.done += done; t.ok += int(m.ok);
    if (m.status in t) t[m.status]++;
    setText(r.name, k);
    setPill(r.pill, m.status || "-", STATUS_TONE[m.status] || "neutral");
    setTrack(r.track, working, width, scale, m);
    t.fix += int(m.fix);
    // a subscription friend's window use beside her width (docs/SPEC-SPRINT.md, the friends table)
    setHTML(r.wf, frac(working, width, digits) + (m.window ? "<span class=\"win\"> · " + escHTML(m.window) + "</span>" : ""));
    setNum(r.ready, int(m.ready)); setNum(r.done, done);
    setOk(r.ok, pct(okv), done);
    if (r.load) { var lp = pct(m.load); setText(r.load, lp === null ? "-" : lp.toFixed(1) + "%"); setClass(r.load, "num" + (lp === null ? " zero" : "")); }
    if (r.tokens) setText(r.tokens, m.tokens || "0");
  }, box._total);
  var c = box._total._c;
  setNum(c[2], t.ready);
  // the fix figure under the bars: the rows' fix cells summed, said only when there are any
  setText(c[3], t.fix ? t.fix + " fix" : ""); setClass(c[3], "num" + (t.fix ? "" : " zero"));
  setNum(c[5], t.done);
  setOk(c[6], t.done ? t.ok / t.done * 100 : null, t.done);
  return { t: t, n: names.length };
}

function renderFleet(d) {
  var r = fleetLike($("fleet"), d.tables.fleet || {}, true);
  setText($("fleet-head"), r.t.up + " up · " + r.t.held + " held · " + r.t.down + " down");
}

function renderFriends(d) {
  var box = $("friends"), table = d.tables.friends;
  if (!table || typeof table !== "object" || !Object.keys(table).length) {
    if (!box._empty) {
      box._map = null; box._head = null; box.textContent = "";
      var e = el("div", "empty");
      e.innerHTML = "No friends table in the sprint data. This panel fills itself when <code>where --json</code> carries <code>tables.friends</code>.";
      box.appendChild(e); box._empty = true;
    }
    setText($("friends-sub"), "");
    return;
  }
  if (box._empty) { box.textContent = ""; box._empty = false; }
  var r = fleetLike(box, table, false);
  setText($("friends-sub"), r.n + " friends");
}

// Lanes (docs/SPEC-SPRINT-DASHBOARD.md, "Lanes"): each machine's lane of a kind, the
// friends or machines that hold it, and those that wait, from where --json --cards's
// lanes array (verb-lane-take-give). One row a machine, by machine then kind.
function nameList(v) { return (v && v.length) ? v.join(", ") : "-"; }
function renderLanes(d) {
  var box = $("lanes"), lanes = Array.isArray(d.lanes) ? d.lanes.slice() : [];
  lanes.sort(function (a, b) { return String(a.machine).localeCompare(String(b.machine)) || String(a.kind).localeCompare(String(b.kind)); });
  if (!lanes.length) {
    if (!box._empty) {
      box._map = null; box._head = null; box.textContent = "";
      var e = el("div", "empty");
      e.innerHTML = "No lanes in the sprint data. This panel fills itself when <code>where --json --cards</code> carries <code>lanes</code>.";
      box.appendChild(e); box._empty = true;
    }
    setText($("lanes-sub"), "");
    return;
  }
  if (box._empty) { box.textContent = ""; box._empty = false; }
  if (!box._head) { box._head = pageHead("lanes"); }
  var byKey = {};
  lanes.forEach(function (l) { byKey[l.machine + "/" + l.kind] = l; });
  syncRows(box, box._head, lanes.map(function (l) { return l.machine + "/" + l.kind; }), function () {
    var r = { node: el("div", "row") };
    r.machine = el("div", "name"); r.kind = el("div"); r.width = numCell(); r.held = el("div"); r.waiting = el("div");
    [r.machine, r.kind, r.width, r.held, r.waiting].forEach(function (c) { r.node.appendChild(c); });
    return r;
  }, function (r, k) {
    var l = byKey[k];
    setText(r.machine, l.machine); setText(r.kind, l.kind); setNum(r.width, int(l.width));
    setText(r.held, nameList(l.held)); setText(r.waiting, nameList(l.waiting));
  });
  setText($("lanes-sub"), lanes.length + (lanes.length === 1 ? " lane" : " lanes"));
}

function renderReaders(d) {
  var box = $("readers"), table = d.tables.readers || {}, names = Object.keys(table).sort();
  var F = ["asked", "reading", "ok", "broken"];
  if (!box._head) {
    box._head = headRow([["Reader"], ["asked", "num"], ["reading", "num"], ["ok", "num"], ["broken", "num"]]);
    box._total = el("div", "row total"); box._total._c = [el("div", "", "All")];
    F.forEach(function () { box._total._c.push(numCell()); });
    box._total._c.forEach(function (c) { box._total.appendChild(c); });
  }
  var t = { asked: 0, reading: 0, ok: 0, broken: 0 };
  syncRows(box, box._head, names, function () {
    var r = { node: el("div", "row"), c: {} }; r.name = el("div", "name"); r.node.appendChild(r.name);
    F.forEach(function (f) { r.c[f] = numCell(); r.node.appendChild(r.c[f]); });
    return r;
  }, function (r, k) {
    setText(r.name, k.replace(/^reader-/, ""));
    F.forEach(function (f) { var v = int(table[k][f]); t[f] += v; setNum(r.c[f], v); });
  }, box._total);
  F.forEach(function (f, i) { setNum(box._total._c[i + 1], t[f]); });
  setText($("readers-sub"), names.length + " readers");

  // merge, summed over streams the way the terminal table does
  var merge = d.tables.merge || {}, q = 0, mg = 0, stuck = 0, stopped = 0, ci = "-";
  Object.keys(merge).forEach(function (k) {
    var m = merge[k]; q += int(m.queued); mg += int(m.merged); stuck += int(m.stuck);
    if (m.state === "stopped") stopped++;
    if (m.ci === "red") ci = "red"; else if (m.ci === "green" && ci !== "red") ci = "green";
  });
  var strip = $("merge");
  if (!strip._v) {
    strip._v = {};
    [["queued", "Queued"], ["merged", "Merged"], ["stuck", "Stuck"], ["ci", "CI"], ["stopped", "Stopped"]].forEach(function (p) {
      var c = el("div"); c.appendChild(el("div", "label", p[1])); var v = el("div", "v"); c.appendChild(v); strip._v[p[0]] = v; strip.appendChild(c);
    });
  }
  setText(strip._v.queued, q); setText(strip._v.merged, mg); setText(strip._v.stuck, stuck);
  setText(strip._v.ci, ci); setText(strip._v.stopped, stopped);
  strip._v.stuck.style.color = stuck > 0 ? "var(--warning)" : "";
  strip._v.stopped.style.color = stopped > 0 ? "var(--critical)" : "";
  strip._v.ci.style.color = ci === "red" ? "var(--critical)" : ci === "green" ? "var(--good)" : "var(--text-3)";
}

// the machine pill: red when the machine line says every provider is out of credit (SPEC.md)
function setMachine(line) {
  var text = String(line || "-").replace(/^machine:\s*/, "");
  // the human page shows RUNNING, STOPPED or STALE; tick lateness is the coordinator's view only
  var late = /^running\b.*tick late (\d+)s/i.exec(text);
  if (late) text = Number(late[1]) >= 60 ? "STALE" : "running";
  else if (/^running\b/i.test(text)) text = "running";
  setText($("machine"), text);
  var stopped = /STOPPED/.test(text) && /out of credit/i.test(text);
  setClass($("machine-chip"), "chip" + (stopped ? " alert" : ""));
  // the bar pulses only while the machine runs
  var running = /^(running|STALE)\b/.test(text);
  var box = $("overall"); if (box) box.classList.toggle("stopped", !running);
}

function renderHero(d, s) {
  var landed = int(d.landed), all = int(d.all);
  setText($("landed"), landed.toLocaleString("en-US")); setText($("all"), all.toLocaleString("en-US")); setText($("all2"), all.toLocaleString("en-US"));
  setText($("pct"), all ? (landed / all * 100).toFixed(1) + "%" : "-");
  var m = String(d.summary || "").match(/ETA\s+(\S+)/), at = new Date(d.at);
  if (m) {
    setText($("eta"), etaText(m[1]));
    var ms = etaMs(m[1]);
    var etaAt = new Date(at.getTime() + ms);
    if (ms != null && !isNaN(at)) { etaAtLast = [etaAt, ms]; fitEtaAt(); } else { etaAtLast = null; setText($("eta-at"), "\u00a0"); }
  } else if ((all && landed >= all) || / done$/.test(String(d.summary || ""))) { setText($("eta"), "done"); setText($("eta-at"), " "); }
  else { setText($("eta"), "-"); setText($("eta-at"), "not in the summary"); }
  // the cost tile and its tooltip cover one scope (docs/SPEC-SPRINT.md, the summary line): the
  // streams on the table, or, the sprint done, the epoch's every stream, as the hero's count
  // is. The cost is every recorded take and read of their cards in any column; the cost per
  // card is that over the cards that landed
  var c = d.done ? s.sum.epoch : s.sum, recorded = c.totalCost;
  setText($("cost"), money(recorded));
  // the reads are their own number beside the work, with their share of the two:
  // "$0.42 per card · $310 work · $96 reads (24%)"
  var both = c.workCost + c.readCost;
  var split = both ? money(c.workCost) + " work \u00b7 " + money(c.readCost) + " reads (" + Math.round(100 * c.readCost / both) + "%)" : "";
  var per = landed ? money(Math.ceil(recorded / landed)) + " per card" : "";
  var unpriced = c.unpriced ? c.unpriced + " runs unpriced" : "";
  setHTML($("cost-per"), [per, split, unpriced].filter(Boolean).join(" \u00b7 ") || " ");
  setTitle($("cost-per"), readerSpendTitle(d, d.done ? null : archivedSet(d)));
  // what the providers counted beyond the records is the epoch's (sprint.UnreconciledSpend,
  // every day since the epoch began), never added into the tile: its own line, its scope named
  setText($("cost-unreconciled"), money(s.sum.unreconciled) + " unreconciled since " + epochStart(d));
  setText($("inflight"), s.sum.working + (s.sum.fix || 0) + s.sum.review + s.sum.merging);
  inflightLast = s.sum; renderInflight(s.sum);
  // throughput: cards landed per hour over the last hour, from the server's samples
  setText($("tput"), throughput == null ? "\u2014" : String(Math.round(throughput)));
  setTitle($("tput"), throughput == null ? "needs ten minutes of samples" : "over the last " + Math.round(throughputMinutes) + " min");
  setText($("coord"), d.coordinator || "-"); setText($("epoch"), d.epoch != null ? d.epoch : "-");
  setMachine(d.machine);
}

// readerSpendTitle is the cost tile's tooltip: each reader's spend, all time and the last hour,
// summed over every stream's where record (stream_costs[s].readers, sprint.ReaderSpend: exact
// dollars), most first; "" with no reader priced
// epochStart is the day the epoch began, UTC as the providers' days are, from where --json's
// cleared; "the epoch began" when it is not known.
function epochStart(d) {
  var t = new Date(d.cleared || "");
  return isNaN(t) || t.getUTCFullYear() < 2000 ? "the epoch began" : t.toISOString().slice(0, 10);
}

// readerSpendTitle is each reader's spend over the streams but those in skip (null skips none).
function readerSpendTitle(d, skip) {
  var by = {}, sc = d.stream_costs || {};
  Object.keys(sc).forEach(function (k) {
    if (skip && skip[k]) return;
    var rs = sc[k].readers || {};
    Object.keys(rs).forEach(function (r) {
      var b = by[r] || (by[r] = { usd: 0, hour: 0 });
      b.usd += parseFloat(rs[r].usd) || 0; b.hour += parseFloat(rs[r].hour_usd) || 0;
    });
  });
  var names = Object.keys(by).filter(function (r) { return by[r].usd > 0; });
  names.sort(function (a, b) { return by[b].usd - by[a].usd || (a < b ? -1 : 1); });
  return names.map(function (r) { return r + " " + money(cents(by[r].usd)) + " (" + money(cents(by[r].hour)) + " last hour)"; }).join("\n");
}

// the In flight tile's subline: one line, two parts,
// "<working> working · <review+merging> review + merge", each number white and its words grey;
// the separate review and merging counts are in the tooltip
var inflightLast = null;
function renderInflight(sum) {
  var box = $("inflight-sub"); if (!box) return;
  // the words shorten in turn when the line does not fit the tile (a phone)
  var forms = [["working", "review + merge"], ["work", "review + merge"], ["work", "rev + merge"], ["work", "rev+mrg"], ["wk", "r+m"]];
  // a card at fix is being worked again: it counts as working here, and the tooltip names it
  var fx = sum.fix || 0;
  var draw = function (w) {
    var parts = [[sum.working + fx, w[0]], [sum.review + sum.merging, w[1]]].filter(function (p) { return p[0] > 0; });
    var kids = [];
    if (!parts.length) kids.push(["pw", "nothing in flight"]);
    parts.forEach(function (p, i) {
      if (i) kids.push(["pw", " \u00b7 "]);
      kids.push(["pn", String(p[0])], ["pw", " " + p[1]]);
    });
    setCount(box, kids.length, function () { return el("span"); });
    kids.forEach(function (k, i) { putKid(box, i, k[0], k[1]); });
  };
  for (var i = 0; i < forms.length; i++) { draw(forms[i]); if (fits(box)) break; }
  setTitle(box, (sum.working + fx) + " working" + (fx ? " (" + fx + " fix)" : "") + ", " + sum.review + " review, " + sum.merging + " merging");
}
window.addEventListener("resize", function () { if (inflightLast) renderInflight(inflightLast); fitEtaAt(); });

// ---------- poll loop ----------
var lastSnap = null, lastGood = null, inFlight = false, build = null, throughput = null, throughputMinutes = 0;
// Readers and merge are hidden by default; ?all=1 shows them.
var SHOW_ALL = /(?:^|[?&])all=1(?:&|$)/.test(location.search);
if (SHOW_ALL) $("readers-panel").hidden = false;
// The page shows one release's streams: ?release=<name> or all; none is the server's
// current release. The switch in the header lists every release a stream carries, then all.
var RELEASE = (/(?:^|[?&])release=([^&]*)/.exec(location.search) || [])[1];
RELEASE = RELEASE ? decodeURIComponent(RELEASE) : "";
var RELEASE_Q = RELEASE ? "?release=" + encodeURIComponent(RELEASE) : "";
function renderRelease(j) {
  var box = $("release"); if (!box) return;
  var names = (j.releases || []).concat(j.releases && j.releases.length ? ["all"] : []);
  var key = names.join("|") + ">" + (j.release || "");
  if (box._key === key) return;
  box._key = key;
  box.textContent = "";
  box.hidden = !names.length;
  names.forEach(function (n) {
    var a = el("a", n === j.release ? "on" : "", n);
    a.setAttribute("href", "?release=" + encodeURIComponent(n) + (SHOW_ALL ? "&all=1" : ""));
    box.appendChild(a);
  });
}
// The clock shows the time of the data on screen. A failed read or a
// restarting server changes nothing on the page: the last data, clock and
// dot stay exactly as they were (failures are logged by the server only).
function setLive(since) {
  setClass($("live"), "live ok");
  setLiveHTML($("live-text"), "Updated <span class=\"mono\">" + clock(since) + "</span>");
}
function setLiveHTML(e, s) { if (e.innerHTML !== s) e.innerHTML = s; }
// The full form of a table, unless any of its rows or cells would overflow: then the compact form.
// Measured with the class removed and set again in the same task, so nothing flickers.
function fitTables() {
  ["streams", "fleet", "friends"].forEach(function (id) {
    var panel = document.querySelector(".panel." + id), head = panel && panel.querySelector(".row.head");
    if (!head) return;
    panel.classList.remove("compact");
    // every row, not only the header: a row's pill or name can be wider than its header
    var over = [].some.call(panel.querySelectorAll(".row"), function (r) {
      if (r.scrollWidth > r.clientWidth + 1) return true;
      // a number's flash tint bleeds 6 px into the gutter by design (.fv), so numbers get 7 px of slack
      return [].some.call(r.children, function (c) {
        var slack = (c.classList.contains("num") || c.classList.contains("frac")) ? 7 : 1;
        return c.offsetParent !== null && !c.classList.contains("cells") && c.scrollWidth > c.clientWidth + slack;
      });
    });
    if (over) panel.classList.add("compact");
  });
}
window.addEventListener("resize", fitTables);
// Spend row: the pie of cards by tier and the ten most expensive streams.
var TIERS = ["flash", "pro", "heavy", "frontier"], topN = 10, lastSpendData = null;
window.addEventListener("resize", function () { if (lastSpendData) renderTopStreams(lastSpendData); });
function tierColor(t) { return "var(--tier-" + (TIERS.indexOf(t) >= 0 ? t : "other") + ")"; }
function tierCounts(obj) { // {flash: "12", pro: 3} -> [[tier, n], ...] sorted by the ladder, others last
  var out = [];
  if (!obj) return out;
  Object.keys(obj).forEach(function (k) { var n = parseInt(obj[k], 10); if (n > 0) out.push([k, n]); });
  out.sort(function (a, b) { var ia = TIERS.indexOf(a[0]), ib = TIERS.indexOf(b[0]); if (ia < 0) ia = 99; if (ib < 0) ib = 99; return ia - ib || b[1] - a[1]; });
  return out;
}

// The Cost breakdown: the pie is the spend by tier and the
// tier line in the panel's header is its legend, open or folded. The spend per tier is summed
// over every stream (tables.work[s].cost_by_tier); a tier at $0 is left out of the pie, the
// legend and the table alike. One format for the whole panel, decided once: cents (rounded up)
// when every tier shown is under $10, else whole dollars rounded up ("so we don't get weird
// stuff where the tiers' rounded $ values don't match the pie"). The pie is drawn from the
// values the legend shows, in the legend's order: by spend, most first.
function tierSpend(d) {
  var work = (d.tables && d.tables.work) || {}, byTier = {};
  Object.keys(work).forEach(function (k) {
    var b = work[k].cost_by_tier; if (!b || typeof b !== "object") return;
    // the four tiers alone: a record with no tier ("untiered") is no tier and is left out
    TIERS.forEach(function (t) { var c = cents(b[t]); if (c) byTier[t] = (byTier[t] || 0) + c; });
  });
  var order = Object.keys(byTier).sort(function (a, b) { return byTier[b] - byTier[a] || a.localeCompare(b); });
  var inCents = order.length > 0 && order.every(function (t) { return byTier[t] < 1000; });
  var fmt = inCents ? money : dollars;
  // a value as shown: the cents, or the whole dollars rounded up, in cents
  var shown = function (c) { return inCents ? c : Math.ceil(c / 100 - 1e-9) * 100; };
  return { byTier: byTier, order: order, fmt: fmt, shown: shown };
}

function renderPie(d) {
  var svg = $("pie"); if (!svg) return; // no legend below the chart: its legend is the panel's header line
  var sp = tierSpend(d), vals = sp.order.map(function (t) { return [t, sp.shown(sp.byTier[t])]; });
  var total = vals.reduce(function (a, v) { return a + v[1]; }, 0);
  var key = JSON.stringify(vals) + "|" + sp.fmt(total);
  if (svg._key === key) return; // the same pie is not drawn again
  svg._key = key;
  while (svg.firstChild) svg.removeChild(svg.firstChild);
  svg.setAttribute("aria-label", "spend by tier");
  var ns = "http://www.w3.org/2000/svg";
  if (!total) { // nothing spent, or a build that serves no cost_by_tier
    var c = document.createElementNS(ns, "circle"); c.setAttribute("cx", 50); c.setAttribute("cy", 50); c.setAttribute("r", 48);
    c.setAttribute("fill", "var(--tier-other)"); c.setAttribute("opacity", ".35"); svg.appendChild(c);
    return;
  }
  var a0 = -Math.PI / 2;
  vals.forEach(function (v) {
    var frac = v[1] / total, a1 = a0 + frac * 2 * Math.PI, p = document.createElementNS(ns, "path");
    if (frac >= 0.9999) { p.setAttribute("d", "M50,2 A48,48 0 1 1 49.99,2 Z"); }
    else {
      var x0 = 50 + 48 * Math.cos(a0), y0 = 50 + 48 * Math.sin(a0), x1 = 50 + 48 * Math.cos(a1), y1 = 50 + 48 * Math.sin(a1);
      p.setAttribute("d", "M50,50 L" + x0.toFixed(2) + "," + y0.toFixed(2) + " A48,48 0 " + (frac > 0.5 ? 1 : 0) + " 1 " + x1.toFixed(2) + "," + y1.toFixed(2) + " Z");
    }
    p.setAttribute("fill", tierColor(v[0]));
    var tt = document.createElementNS(ns, "title"); tt.textContent = v[0] + " " + sp.fmt(v[1]) + " (" + Math.round(100 * frac) + "%)"; p.appendChild(tt);
    svg.appendChild(p);
    a0 = a1;
  });
}

function renderTopStreams(d) {
  var box = $("top-streams"); if (!box) return;
  lastSpendData = d;
  var work = (d.tables && d.tables.work) || {}, rows = [];
  var sp = tierSpend(d), byTier = sp.byTier, order = sp.order, fmt = sp.fmt;
  Object.keys(work).forEach(function (k) {
    var w = work[k], ct = cents(w.cost); if (!ct) return;
    var n = {}; FLOW.forEach(function (c) { n[c] = parseInt(w[c], 10) || 0; });
    rows.push({ name: k, cost: ct, n: n, per: n.landed ? Math.ceil(ct / n.landed) : null, tiers: tierCounts(w.tiers), byTier: w.cost_by_tier || null });
  });
  rows.sort(function (a, b) { return b.cost - a.cost; });
  var cols = String(order.length + 1); // the tiers with spend, then total
  if (box.style.getPropertyValue("--tier-cols") !== cols) box.style.setProperty("--tier-cols", cols);
  var pie = $("pie"), rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
  var n = 10;
  if (pie && pie.clientWidth) n = Math.max(3, Math.min(rows.length, Math.round((pie.clientWidth + 4 * rem - 4 * rem - 1 * rem) / (5 * rem))));
  topN = n;
  // the rows are keyed by stream and patched in place like every other table's; the header and
  // each row grow or lose cells at the end when the tiers with spend change
  if (!box._head) box._head = el("div", "row head");
  var head = box._head, cellCount = order.length + 2;
  setCount(head, cellCount, function () { return quiet(el("div")); });
  putKid(head, 0, "", "stream");
  order.forEach(function (t, i) { putKid(head, 1 + i, "num", t); }); // plain headers, as every table's: the color keys are the legend's alone
  putKid(head, cellCount - 1, "num", "total");
  var shown = rows.slice(0, n);
  var byName = {}; shown.forEach(function (r) { byName[r.name] = r; });
  syncRows(box, head, shown.map(function (r) { return r.name; }), function () {
    return { node: el("div", "row") };
  }, function (r, k) {
    var row = byName[k];
    setCount(r.node, cellCount, function () { return quiet(el("div")); });
    putKid(r.node, 0, "name", k);
    order.forEach(function (t, i) { // the stream's spend on the tier, in the panel's format
      var c = row.byTier && cents(row.byTier[t]);
      putKid(r.node, 1 + i, "num" + (c ? "" : " faint"), c ? fmt(c) : "-");
    });
    putKid(r.node, cellCount - 1, "num", fmt(row.cost));
  });
  if (!box._none) box._none = el("div", "row faint", "no stream has spent anything yet");
  if (!rows.length && box._none.parentNode !== box) box.appendChild(box._none);
  if (rows.length && box._none.parentNode === box) box._none.remove();
  // the pie's legend in the header: each tier as the state legend draws an item, its square in
  // the tier's color, the name grey and the amount white, in the pie's order, no separators
  var ts = $("tier-sub");
  if (ts) {
    if (!order.length) setText(quiet(ts), "-");
    else {
      if (ts._val != null) { ts.textContent = ""; ts._val = null; }
      setCount(ts, order.length, function () {
        var p = el("span"); p.appendChild(el("i", "sw")); p.appendChild(quiet(el("span", "tn"))); p.appendChild(quiet(el("span", "ta"))); return p;
      });
      order.forEach(function (t, i) {
        var p = ts.children[i], bg = tierColor(t);
        if (p.children[0].style.background !== bg) p.children[0].style.background = bg;
        setText(p.children[1], t); setText(p.children[2], fmt(sp.shown(byTier[t])));
      });
    }
  }
}

// Merge (docs/SPEC-SPRINT-DASHBOARD.md, "Merge"): where --json's merge_row, one row under the
// progress bar; a minute or a gate not known is "-". Nothing in it flashes.
function renderMerge(d, stop) {
  var m = d.merge_row || {};
  var mins = function (n, suffix) { return n == null ? "-" : n + "m" + (suffix || ""); };
  var count = function (n) { return n == null ? "-" : String(n); };
  var merging = count(m.merging);
  if (stop && stop.cards > 0 && m.merging != null) merging = m.merging + " (" + stoppedLabel(stop.cards, stop.streams) + ")";
  [["mr-merging", merging], ["mr-review", count(m.review)], ["mr-landed", count(m.landed_per_30m)],
   ["mr-oldest", mins(m.oldest_merging_min)],
   ["mr-drift", m.base_lacks == null ? "-" : "base lacks " + m.base_lacks + " · dev lacks " + m.dev_lacks],
   ["mr-sync", mins(m.sync_minutes, " ago")], ["mr-promoted", mins(m.promotion_minutes, " ago")]].forEach(function (f) {
    var e = $(f[0]); e._quiet = true; setText(e, f[1]);
  });
  var g = $("mr-gate");
  if (!g._pill) { g._pill = makePill(); g.appendChild(g._pill); }
  var gate = m.base_gate || "-", tone = gate === "red" ? "critical" : gate === "green" ? "good" : "neutral";
  setPill(g._pill, gate + (m.failing_test ? " · " + m.failing_test : ""), tone,
    gate === "-" ? "the base's gate is not known" : "the base's gate is " + gate + (m.failing_test ? ": " + m.failing_test : ""));
}

// ---------- priority marks ----------
// A card's priority by colour (docs/SPEC-SPRINT-DASHBOARD.md, "Priority"): one mark a card
// whose level is not normal (where --json's priorities) and one a read waiting (reads_waiting),
// in the ladder's order; a blocker bright red, a critical dark red, a card awaiting rework (fix,
// the view's priorities.fix) purple right of the reds, a read the orange of the robot's shoes,
// and every work card blue whatever its level (high, low). At most MARKS_MAX a level; the last
// says how many more.
var MARK_LEVELS = ["blocker", "critical", "critical (by weight, not yet ordered)", "fix", "high", "reader", "low"];
var MARKS_MAX = 40;
function priorityClass(level) {
  if (level === "fix") return "p-fix"; // a card awaiting rework, purple
  if (level.indexOf("critical") === 0) return "p-critical"; // a computed critical too, its title says so
  return level === "blocker" || level === "reader" ? "p-" + level : "p-work";
}
function renderPriorityMarks(d) {
  var box = $("priority-marks"); if (!box) return;
  var marks = [];
  MARK_LEVELS.forEach(function (l) {
    var ids = l === "reader" ? [] : ((d.priorities || {})[l] || []).slice();
    if (l === "reader") for (var i = 0; i < int(d.reads_waiting); i++) ids.push("a read waiting");
    ids.slice(0, MARKS_MAX).forEach(function (id, i) {
      var more = i === MARKS_MAX - 1 && ids.length > MARKS_MAX ? " (+" + (ids.length - MARKS_MAX) + " more)" : "";
      marks.push([l, id + more]);
    });
  });
  box.hidden = marks.length === 0;
  setCount(box, marks.length, function () { return el("span", "mark"); });
  marks.forEach(function (m, i) {
    var k = box.children[i];
    setClass(k, "mark " + priorityClass(m[0]));
    setTitle(k, m[0] + ": " + m[1]);
  });
}

function render(d) {
  var s = renderStreams(d);
  var stop = stoppedMerging(d);
  renderOverall(s.sum, s.all, stop);
  renderPriorityMarks(d);
  renderPie(d);
  renderTopStreams(d);
  renderMerge(d, stop);
  renderFleet(d);
  renderFriends(d);
  renderWall(d);
  renderLanes(d);
  if (SHOW_ALL) renderReaders(d);
  renderHero(d, s);
  fitTables();
}
var stream = null;
function poll() {
  if (inFlight || (stream && stream.readyState === 1)) return;
  inFlight = true;
  fetch("/api/sprint" + RELEASE_Q, { cache: "no-store" }).then(function (r) {
    if (!r.ok) throw new Error("HTTP " + r.status);
    return r.json();
  }).then(apply).catch(function () {
    // hold: nothing changes on the page
  }).then(function () { inFlight = false; });
}
function apply(j) {
  if (j.data) {
    throughput = j.throughput == null ? null : j.throughput; throughputMinutes = j.throughputMinutes || 0;
    // a snapshot byte-identical to the one on screen draws nothing
    var snap = JSON.stringify(j.data);
    if (snap !== lastSnap) { lastSnap = snap; try { render(j.data); } catch (e) { console.error(e); } }
    lastGood = j.data;
    renderRelease(j);
    setLive(new Date(j.data.at));
  }
  if (j.build) { if (build == null) build = j.build; else if (build !== j.build) location.reload(); }
}

// The dashboard is always dark (docs/SPEC-SPRINT-DASHBOARD.md, Page): there is no theme
// toggle and no light tokens.
// the stream reconnects on its own; while it is not open, the timer's poll runs
if (window.EventSource) {
  stream = new EventSource("/events" + RELEASE_Q);
  stream.addEventListener("sprint", function (e) { try { apply(JSON.parse(e.data)); } catch (x) { console.error(x); } });
}
poll();
setInterval(poll, POLL_MS);
