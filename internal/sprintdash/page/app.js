// Sprint dashboard: keeps /events open (each new copy of the server's cached
// `where --json` pushed as it is read) and patches the DOM in place; while the
// stream is not open it polls /api/sprint every second on a fixed timer, never
// after an answer. Nothing is blanked on a failed poll; the page holds the last
// data and says nothing.
"use strict";

// Left to right in the bars: done first, so the bar fills like progress.
var STATES = ["landed", "merging", "review", "working", "ready", "waiting"];
// Columns of the streams table, in flow order.
var FLOW = ["waiting", "ready", "working", "review", "merging", "landed"];
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
["all", "all2", "pct", "eta", "eta-at", "cost", "cost-per", "inflight", "inflight-sub", "tput", "coord", "epoch", "machine",
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
// The header rows of Work, Fleet and Friends are the page's own markup (index.html), the
// strings of docs/SPEC-SPRINT-DASHBOARD.md, which a test holds equal: each table's header is
// a clone of the row the page carries.
var HEADS = {};
["streams", "fleet", "friends"].forEach(function (id) {
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

// VU meter track: exactly `slots` cells (the machine's width), on a grid of
// `scale` columns (the widest member's width) so the cells line up down the
// column; the first `value` are lit, the rest dark; nothing past the width.
function setTrack(box, value, slots, scale) {
  var classes = [];
  for (var i = 0; i < slots; i++) classes.push(i < value ? "working" : "");
  setCells(box, classes, scale, TRACK_CELL);
  setTitle(box, value + " working of " + slots);
}
// "a / b" as a block of fixed width: a right-aligned in `digits` character widths, b
// left-aligned in as many, so the slash of every row in a column sits on one vertical line
// and the block can be right-aligned in its column like any number.
function frac(a, b, digits) {
  return "<span class=\"fa\" style=\"width:" + digits + "ch\">" + a + "</span><span class=\"fs\"> / </span><span class=\"fb\" style=\"width:" + digits + "ch\">" + b + "</span>";
}
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
  if (state === "stopped") return ["stopped", "critical"];
  if (total > 0 && c.waiting === total) return ["held", "warning"];
  if (c.ready + c.working + c.review + c.merging > 0) return ["working", "active"];
  if (state === "landed") return ["landed", "done"];
  return [state || "idle", "neutral"];
}

function renderStreams(d) {
  var box = $("streams"), work = d.tables.work || {}, merge = d.tables.merge || {};
  var states = {}; (d.streams || []).forEach(function (s) { states[s.Stream] = s; });
  // children: 1 stream, 2 status, 3 waiting, 4 ready, 5 working, 6 review, 7 merging, 8 landed, 9 cost
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
  var keys = streamOrder(d).sort(function (a, b) { return rank(a) - rank(b) || (a < b ? -1 : a > b ? 1 : 0); });
  var sum = { cost: 0 }, held = 0, landedStreams = 0, prevRank = null;
  var digits = digitsOf(keys.reduce(function (a, k) { return a + FLOW.reduce(function (b, st) { return b + int(work[k][st]); }, 0); }, 0));
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
    FLOW.forEach(function (st) { c[st] = int(w[st]); total += c[st]; sum[st] += c[st]; });
    var ct = cents(w.cost); if (ct) sum.cost += ct;
    var status = statusOf[k];
    if (status === "held") held++;
    if (status === "landed") landedStreams++;
    if (r.node.dataset.status !== status) r.node.dataset.status = status;
    r.node.classList.toggle("group-start", prevRank !== null && rank(k) !== prevRank);
    prevRank = rank(k);
    setText(r.nameT, k);
    var tone = { landed: "done", working: "active", held: "warning", stopped: "critical" }[status] || "neutral";
    setPill(r.pill, status, tone, status + (s.State ? " · stream " + s.State + (s.Since ? " since " + clockShort(new Date(s.Since)) : "") : ""));
    var tags = []; if (int(m.stuck) > 0) tags.push(m.stuck + " stuck"); if (m.ci === "red") tags.push("ci red");
    setText(r.tag, tags.join(" · "));
    FLOW.forEach(function (st) { if (st !== "landed") setNum(r.n[st], c[st]); });
    setHTML(r.n.landed, frac(c.landed, total, digits));
    setText(r.cost, ct === null ? "-" : money(ct)); setClass(r.cost, "num" + (ct === null ? " zero" : ""));
  }, box._total);
  var tc = box._total._c, all = 0;
  FLOW.forEach(function (st, i) { all += sum[st]; if (st !== "landed") setNum(tc[2 + i], sum[st]); });
  setHTML(tc[7], frac(sum.landed, all, digits));
  setText(tc[8], money(sum.cost));
  setText($("streams-sub"), keys.length + " streams · " + landedStreams + " landed · " + held + " held");
  // the "landed" header is centred over its n / total cell: same width as the cell, text centred
  var lw = tc[7].offsetWidth ? tc[7].offsetWidth + "px" : "";
  if (lw && box._head.children[7].style.width !== lw) box._head.children[7].style.width = lw;
  return { sum: sum, all: all };
}

var overallLast = null;
function renderOverall(sum, all) {
  overallLast = { sum: sum, all: all };
  var box = $("overall"), per = cardsPerCell(all, cellsThatFit(box)), n = Math.ceil(all / per);
  var share = allocate(sum, all, n), classes = [];
  STATES.forEach(function (st) { for (var i = 0; i < share[st]; i++) classes.push(st); });
  setCells(box, classes, n);
  setTitle(box, (per === 1 ? "one cell per card" : "one cell per " + per + " cards") + " · " +
    STATES.map(function (st) { return st + " " + sum[st]; }).join(", "));
  var lg = $("legend");
  if (!lg._items) {
    lg._items = {};
    STATES.forEach(function (st) {
      var it = el("span"); it.appendChild(el("i", "sw " + st)); var t = quiet(el("span")); it.appendChild(t);
      lg._items[st] = t; lg.appendChild(it);
    });
  }
  STATES.forEach(function (st) { setText(lg._items[st], st + " " + sum[st]); });
}
window.addEventListener("resize", function () { if (overallLast) renderOverall(overallLast.sum, overallLast.all); });

function fleetLike(box, table, withLoad) {
  var names = Object.keys(table || {});
  var rank = { up: 0, held: 1, down: 2 };
  function rk(n) { var r = rank[table[n].status]; return r == null ? 3 : r; }
  names.sort(function (a, b) { return rk(a) - rk(b) || a.localeCompare(b); });
  if (!box._head) {
    box._head = pageHead(box.id);
    box._total = el("div", "row total");
    box._total._c = [el("div", "", "Total"), el("div"), quiet(numCell()), el("div"), el("div"), quiet(numCell()), quiet(numCell())];
    if (withLoad) box._total._c.push(el("div"));
    box._total._c.forEach(function (c) { box._total.appendChild(c); });
  }
  var t = { ready: 0, done: 0, ok: 0, up: 0, held: 0, down: 0 };
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
    return r;
  }, function (r, k) {
    var m = table[k], working = int(m.working), width = int(m.width), done = int(m.done);
    var okv = m.okpct != null ? m.okpct : m["ok%"];
    t.ready += int(m.ready); t.done += done; t.ok += int(m.ok);
    if (m.status in t) t[m.status]++;
    setText(r.name, k);
    setPill(r.pill, m.status || "-", STATUS_TONE[m.status] || "neutral");
    setTrack(r.track, working, width, scale);
    setHTML(r.wf, frac(working, width, digits));
    setNum(r.ready, int(m.ready)); setNum(r.done, done);
    setOk(r.ok, pct(okv), done);
    if (r.load) { var lp = pct(m.load); setText(r.load, lp === null ? "-" : lp.toFixed(1) + "%"); setClass(r.load, "num" + (lp === null ? " zero" : "")); }
  }, box._total);
  var c = box._total._c;
  setNum(c[2], t.ready);
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

function renderHero(d, s) {
  var landed = int(d.landed), all = int(d.all);
  setText($("landed"), landed.toLocaleString("en-US")); setText($("all"), all.toLocaleString("en-US")); setText($("all2"), all.toLocaleString("en-US"));
  setText($("pct"), all ? (landed / all * 100).toFixed(1) + "%" : "-");
  var m = String(d.summary || "").match(/ETA\s+(\S+)/), at = new Date(d.at);
  if (m) {
    setText($("eta"), etaText(m[1]));
    var ms = etaMs(m[1]);
    setText($("eta-at"), ms != null && !isNaN(at) ? "around " + clockShort(new Date(at.getTime() + ms)) : " ");
  } else if (all && landed >= all) { setText($("eta"), "done"); setText($("eta-at"), " "); }
  else { setText($("eta"), "-"); setText($("eta-at"), "not in the summary"); }
  setText($("cost"), money(s.sum.cost));
  setHTML($("cost-per"), landed ? money(Math.ceil(s.sum.cost / landed)) + " per card" : " ");
  setText($("inflight"), s.sum.working + s.sum.review + s.sum.merging);
  setText($("inflight-sub"), ["working", "review", "merging"].filter(function (k) { return s.sum[k] > 0; })
    .map(function (k) { return s.sum[k] + " " + k; }).join(" · ") || "nothing in flight");
  // throughput: cards landed per hour over the last hour, from the server's samples
  setText($("tput"), throughput == null ? "\u2014" : String(Math.round(throughput)));
  setTitle($("tput"), throughput == null ? "needs ten minutes of samples" : "over the last " + Math.round(throughputMinutes) + " min");
  setText($("coord"), d.coordinator || "-"); setText($("epoch"), d.epoch != null ? d.epoch : "-");
  setText($("machine"), String(d.machine || "-").replace(/^machine:\s*/, ""));
}

// ---------- poll loop ----------
var lastGood = null, inFlight = false, build = null, throughput = null, throughputMinutes = 0;
// Readers and merge are hidden by default; ?all=1 shows them.
var SHOW_ALL = /(?:^|[?&])all=1(?:&|$)/.test(location.search);
if (SHOW_ALL) $("readers-panel").hidden = false;
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
function render(d) {
  var s = renderStreams(d);
  renderOverall(s.sum, s.all);
  renderFleet(d);
  renderFriends(d);
  if (SHOW_ALL) renderReaders(d);
  renderHero(d, s);
  fitTables();
}
var stream = null;
function poll() {
  if (inFlight || (stream && stream.readyState === 1)) return;
  inFlight = true;
  fetch("/api/sprint", { cache: "no-store" }).then(function (r) {
    if (!r.ok) throw new Error("HTTP " + r.status);
    return r.json();
  }).then(apply).catch(function () {
    // hold: nothing changes on the page
  }).then(function () { inFlight = false; });
}
function apply(j) {
  if (j.data) {
    throughput = j.throughput == null ? null : j.throughput; throughputMinutes = j.throughputMinutes || 0;
    if (!lastGood || lastGood.at !== j.data.at) { try { render(j.data); } catch (e) { console.error(e); } }
    lastGood = j.data;
    setLive(new Date(j.data.at));
  }
  if (j.build) { if (build == null) build = j.build; else if (build !== j.build) location.reload(); }
}

// theme: dark by default, light by the toggle only
function syncThemeButton() { setText($("theme"), document.documentElement.dataset.theme === "light" ? "Dark" : "Light"); }
$("theme").addEventListener("click", function () {
  var next = document.documentElement.dataset.theme === "light" ? "dark" : "light";
  document.documentElement.dataset.theme = next;
  try { localStorage.setItem("sprint-theme", next); } catch (e) {}
  syncThemeButton();
});
syncThemeButton();
// the stream reconnects on its own; while it is not open, the timer's poll runs
if (window.EventSource) {
  stream = new EventSource("/events");
  stream.addEventListener("sprint", function (e) { try { apply(JSON.parse(e.data)); } catch (x) { console.error(x); } });
}
poll();
setInterval(poll, POLL_MS);
