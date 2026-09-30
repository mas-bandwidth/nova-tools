-- X: the sprint's own checks and the derivation of its indexes (upper design
-- version 2.1, 1.0's phase list, 1.3.2, 1.3.5; errata 1 E3 and E4; item IT13).
-- A skeleton in the sense of IT12's: written to the interfaces, parsed, and not
-- loaded by any store before gate G0 (Layer 1 revision 4 pinned, Layer 2 accepted
-- again against its hash). Its Go twin is internal/sprint/sprintfn/twin_x.go and
-- twin_x_cmds.go, and the two are compared on the same steps after G0.
--
--   NS.SP.x_pre(ctx, sp, obs)  -> true, refusal   the pre stage: reads, decides, writes nothing
--   NS.SP.x_cmds(ctx, tp, lp)  -> plan            commands only: {commands = {...}}
--
-- x_pre holds the step to the lease generation (STALEGEN), STOPPED, DROPPING,
-- NOTCOORD, COUNTER, the guards of the body (XGUARD) and the clock's state
-- (MACHINESTATE), in that order, and validates every field the derivation will
-- read so that x_cmds, which has no refusal channel in IT12's core, cannot meet a
-- malformed one. x_cmds derives the indexes and the card kinds of the due set from
-- each changed card's before and after (1.3.2) (for a quarantined card, every removal
-- its change makes and the additions of sent alone, so a verb that removes it takes it
-- out of every index and wait in the same step), takes each quarantined id out of
-- elig, fresh, again and askwait (1.3.5), and edits the agenda in A1's order.
--
-- Layer 1's helpers are resolved when a call runs (table_set*.lua sorts after this
-- file); `sp` is the decoded sprint half of the request (sprintfn/wire.go).
if NS.tset_profile then
do
  local SP = NS.SP

  -- The sprint's keys X reads and writes (1.0's {p} is ctx.space .. 'sprint:').
  local KEY_LEASE, KEY_CLOCK, KEY_COORD, KEY_STRANGERS = 'lease', 'clock', 'coordinator', 'strangers'
  local KEY_NEXT, KEY_DROPPING, KEY_QUARANTINE = 'next', 'dropping', 'quarantine'
  local KEY_DUE, KEY_CUT, KEY_AGENDA, KEY_HELDQ, KEY_ASKWAIT, KEY_JOPEN = 'due', 'cut', 'agenda', 'heldq', 'askwait', 'jopen:'
  -- {p}tver@e, HASH table -> version: X's count of the steps that changed a card
  -- of the table (errata 3 H17; sprintfn xKeyVersion), exact below 2^53.
  local KEY_VERSION, VERSION_MAX = 'tver', 9007199254740990
  local NOTICED = 'noticed'
  -- No running or wall time is negative, so -1 is a due or clock guard over an
  -- entry or field that was absent (sprintfn.XGuardAbsent).
  local ABSENT = -1
  -- The clock's fields, in 1.2's order (sprintfn clockFields).
  local CLOCK_FIELDS = {'stopped_ms', 'stopped_since_ms', 'stophold_ms', 'due_since_ms', 'stopraised_ms'}
  -- The verbs the design names as the coordinator's (sprintfn xCoordinatorVerbs).
  local COORD_VERBS = {release = true, ack = true, wait = true, accept = true, rework = true}
  -- Tables whose rows are streams (sprintfn streamTables).
  local STREAM_TABLES = {work = true, merge = true}
  -- The most members of one command (L1 1.4 pieces; sprint.IndexPiece).
  local PIECE = 1000
  -- Reserve for a read of one short value, and for each id of a batched read, in bytes.
  local RESERVE_ONE, RESERVE_EACH = 1024, 300
  -- The quarantine's marks are read for membership alone, but a hash answers with the
  -- value, which nothing but the sprint part bounds: each id is reserved at Layer 1's
  -- field cap (64 KiB), 16 ids to a read (1 MiB), after one HLEN that ends the read
  -- when no card is marked (sprintfn xMarkPiece, xMarkValueCap).
  local MARK_PIECE, MARK_VALUE_CAP = 16, 64 * 1024
  -- The most characters of a whole-number field of a card the derivation reads (a
  -- count or a due time): the Lua holds numbers as doubles (sprintfn xWholeChars).
  local WHOLE_CHARS = 18
  -- The most digits of the score counter, a whole number a double keeps exactly
  -- (sprintfn xScoreDigits), and the largest seq a line has: 2^53 - 1, Layer 2's
  -- LOGID bound (sprintfn xMaxSeq).
  local SCORE_DIGITS, MAX_SEQ = 15, 9007199254740991

  -- The definitions, rendered from sprint.IndexDefs, sprint.DueKinds and
  -- sprint.IndexFields: TestSprintXLuaDefsGolden holds this block equal to the
  -- rendering, so a changed row of the Go table is a failing test until the block
  -- is re-rendered. IT02's sprint_defs.lua carries the same tables once it merges
  -- (open question); until then this file carries its own.
  -- x_defs begin
  SP.x_defs = {
    index = {
      {index = 'sent', table = 'work', col = 'waiting', where = {{field = 'kind', test = 'is', value = 'sentinel'}}},
      {index = 'elig', table = 'work', col = 'waiting', where = {{field = 'kind', test = 'is not', value = 'sentinel'}, {field = 'open', test = '=', value = '0'}, {field = 'refused', test = 'unset', value = ''}}},
      {index = 'fresh', table = 'work', col = 'ready', where = {{field = 'kind', test = 'is not', value = 'sentinel'}, {field = 'attempt', test = '=', value = '0'}, {field = 'refused', test = 'unset', value = ''}}},
      {index = 'again', table = 'work', col = 'ready', where = {{field = 'attempt', test = '>=', value = '1'}, {field = 'bound', test = 'unset', value = ''}, {field = 'refused', test = 'unset', value = ''}}},
    },
    due = {
      {kind = 'untaken', table = 'fleet', col = 'ready', field = 'due_untaken', of_row = false},
      {kind = 'unfinished', table = 'fleet', col = 'working', field = 'due_unfinished', of_row = false},
      {kind = 'unbegun', table = 'readers', col = 'asked', field = 'due_unbegun', of_row = false},
      {kind = 'unreported', table = 'readers', col = 'reading', field = 'due_unreported', of_row = false},
      {kind = 'mergeidle', table = 'merge', col = 'ctl', field = 'due_mergeidle', of_row = true},
    },
    fields = {'attempt', 'bound', 'due_mergeidle', 'due_unbegun', 'due_unfinished', 'due_unreported', 'due_untaken', 'kind', 'needs', 'open', 'refused'},
  }
  -- x_defs end
  local NEEDS = 'needs'

  local function S() return NS.tset end
  local function is_null(v) return v == nil or v == cjson.null end
  local function tbl(v) if type(v) == 'table' then return v end return {} end

  -- {p}name and {p}name@e.
  local function skey(ctx, name) return ctx.space .. 'sprint:' .. name end
  local function ekey(ctx, name, e) return ctx.space .. 'sprint:' .. name .. '@' .. e end

  local function refuse(code, detail, fmt, ...)
    return S().refuse(code, detail, select('#', ...) > 0 and string.format(fmt, ...) or fmt)
  end

  -- The distinct strings of a list, sorted (sprintfn xDistinct): an empty string is a
  -- string like another, and what names nothing is refused where it is read.
  local function sorted_unique(list)
    local seen, out = {}, {}
    for _, v in ipairs(list) do
      if not seen[v] then seen[v] = true; out[#out + 1] = v end
    end
    table.sort(out)
    return out
  end

  -- A read of one sprint key through Layer 1's checked helper: a cell or key probe.
  local function rd(ctx, argv, kind, reserve)
    return S().rd(ctx, argv, kind, reserve, 'cell')
  end
  -- hmget reads several fields of one hash, in commands of at most `piece` fields
  -- (PIECE unless given), each one probe and each field reserved at `each` bytes
  -- (RESERVE_EACH unless given) (sprintfn xRead.hmgetIn): a map of the fields set.
  local function hmget(ctx, key, fields, piece, each)
    piece, each = piece or PIECE, each or RESERVE_EACH
    local out = {}
    for i = 1, #fields, piece do
      local last = math.min(i + piece - 1, #fields)
      local argv = {'HMGET', key}
      for j = i, last do argv[#argv + 1] = fields[j] end
      local vals, err = rd(ctx, argv, 'hash', each * (last - i + 1))
      if err then return nil, err end
      for j = i, last do
        local v = vals[j - i + 1]
        if v ~= false and v ~= nil then out[fields[j]] = v end
      end
    end
    return out, nil
  end
  -- zmscore reads the scores of several members of one sorted set, in commands of
  -- at most PIECE members, each one probe: a map of the members that are there.
  local function zmscore(ctx, key, members)
    local out = {}
    for i = 1, #members, PIECE do
      local last = math.min(i + PIECE - 1, #members)
      local argv = {'ZMSCORE', key}
      for j = i, last do argv[#argv + 1] = members[j] end
      local vals, err = rd(ctx, argv, 'zset', RESERVE_EACH * (last - i + 1))
      if err then return nil, err end
      for j = i, last do
        local v = vals[j - i + 1]
        if v ~= false and v ~= nil then out[members[j]] = tonumber(v) end
      end
    end
    return out, nil
  end

  -- A field of a before-state record: its value when present.
  local function field(rec, name)
    local f = rec and rec.fields and rec.fields[name]
    if f and f.present then return f.value end
    return nil
  end
  local function placed(rec) return rec and rec.exists and not is_null(rec.place) end

  -- whole is a field that holds a whole number: 0 when absent or empty; nil and a
  -- message when it holds anything else (sprint.IndexCard.whole).
  local function whole(card, name)
    local v = card.fields[name]
    if v == nil or v == '' then return 0, nil end
    if not string.match(v, '^[+-]?%d+$') or #v > WHOLE_CHARS then
      return nil, string.format('%s card %s: field %s is %q, not a whole number', card.table, card.id, name, v)
    end
    return tonumber(v), nil
  end

  -- card is a card as the derivation sees it: table, id, row, col, score and the
  -- fields that are present. A card with no col has no place and is in no index.
  local function card_of(table_name, id, rec, fields)
    local c = {table = table_name, id = id, row = '', col = '', score = 0, fields = fields}
    if placed(rec) then
      local score = tonumber(rec.score)
      if score == nil then
        return nil, string.format('%s card %s is scored %q, not a number', table_name, id, tostring(rec.score))
      end
      c.row, c.col, c.score = rec.place.row, rec.place.col, score
    end
    return c, nil
  end

  -- fields_of is the present fields of a record's observation.
  local function fields_of(rec)
    local out = {}
    for name, f in pairs(rec and rec.fields or {}) do
      if f.present then out[name] = f.value end
    end
    return out
  end
  local function unobserved(rec)
    local out = {}
    for _, name in ipairs(SP.x_defs.fields) do
      if not (rec and rec.fields and rec.fields[name] ~= nil) then out[#out + 1] = name end
    end
    return out
  end

  local function cond_holds(card, w)
    local v = card.fields[w.field] or ''
    if w.test == 'is' then return v == w.value, nil end
    if w.test == 'is not' then return v ~= w.value, nil end
    if w.test == 'unset' then return v == '', nil end
    local n, err = whole(card, w.field)
    if err then return nil, err end
    local want = tonumber(w.value)
    if w.test == '=' then return n == want, nil end
    return n >= want, nil
  end

  -- entries is the members of every derived index and of the due set from one
  -- card's own state (sprint.indexEntries): {index, arg, member, score}.
  local function entries_of(card)
    local out = {}
    if not card or card.col == '' then return out, nil end
    for _, d in ipairs(SP.x_defs.index) do
      if card.table == d.table and card.col == d.col then
        local all = true
        for _, w in ipairs(d.where) do
          local ok, err = cond_holds(card, w)
          if err then return nil, err end
          all = all and ok
        end
        if all then
          if card.row == '' then return nil, string.format('%s card %s is placed at %s with no row', card.table, card.id, card.col) end
          out[#out + 1] = {index = d.index, arg = card.row, member = card.id, score = card.score}
        end
      end
    end
    for _, k in ipairs(SP.x_defs.due) do
      if card.table == k.table and card.col == k.col and (card.fields[k.field] or '') ~= '' then
        local due, err = whole(card, k.field)
        if err then return nil, err end
        local of = card.id
        if k.of_row then
          if card.row == '' then return nil, string.format('%s card %s is placed at %s with no row', card.table, card.id, card.col) end
          of = card.row
        end
        out[#out + 1] = {index = 'due', arg = '', member = k.kind .. ':' .. of, score = due}
      end
    end
    return out, nil
  end

  local function in_waiting(card) return card ~= nil and card.col == 'waiting' and card.table == 'work' end

  local function split_needs(s)
    local out = {}
    for item in string.gmatch(s or '', '[^,]+') do
      item = string.match(item, '^%s*(.-)%s*$')
      if item ~= '' then out[#out + 1] = item end
    end
    return out
  end

  local function key_name(index, arg)
    if arg == '' then return index end
    return index .. ':' .. arg
  end

  -- card_ops is what one step does to the indexes for one card (sprint.IndexOps):
  -- the diff of its members before and after, and the removal of a card that leaves
  -- waiting from every wait:<n> it names. Returns {[keyname] = {index, arg, rem = {},
  -- add = {{member, score}}}}.
  local function card_ops(before, after)
    local was, err = entries_of(before)
    if err then return nil, err end
    local now
    now, err = entries_of(after)
    if err then return nil, err end
    local wasm, nowm = {}, {}
    for _, e in ipairs(was) do wasm[key_name(e.index, e.arg) .. '\0' .. e.member] = e.score end
    for _, e in ipairs(now) do nowm[key_name(e.index, e.arg) .. '\0' .. e.member] = e.score end
    local ops = {}
    local function op(index, arg)
      local k = key_name(index, arg)
      if not ops[k] then ops[k] = {index = index, arg = arg, rem = {}, add = {}} end
      return ops[k]
    end
    for _, e in ipairs(was) do
      if nowm[key_name(e.index, e.arg) .. '\0' .. e.member] == nil then
        local o = op(e.index, e.arg); o.rem[#o.rem + 1] = e.member
      end
    end
    for _, e in ipairs(now) do
      local prior = wasm[key_name(e.index, e.arg) .. '\0' .. e.member]
      if prior == nil or prior ~= e.score then
        local o = op(e.index, e.arg); o.add[#o.add + 1] = {member = e.member, score = e.score}
      end
    end
    if in_waiting(before) and not in_waiting(after) then
      local done = {}
      for _, n in ipairs(split_needs(before.fields[NEEDS])) do
        if not done[n] then
          done[n] = true
          local o = op('wait', n); o.rem[#o.rem + 1] = before.id
        end
      end
    end
    return ops, nil
  end

  -- fold is every card's ops folded into one op a key, members in order, cut in
  -- pieces of at most PIECE members (sprint.StepIndexOps); keys in the order
  -- index, then arg. For quarantined cards (sprintfn xQuarantinedOps) it keeps every
  -- removal and the additions of sent alone: a quarantined card is given no
  -- membership but sent (1.3.2; I1) and leaves every index its change ends it in (D1).
  local function fold(changes, quarantined)
    local seen, by_key = {}, {}
    for _, ch in ipairs(changes) do
      local c = ch.before or ch.after
      if c then
        local id = c.table .. '\0' .. c.id
        if seen[id] then return nil, string.format('%s card %s is changed twice in one step', c.table, c.id) end
        seen[id] = true
        local ops, err = card_ops(ch.before, ch.after)
        if err then return nil, err end
        for k, o in pairs(ops) do
          local add = o.add
          if quarantined and o.index ~= 'sent' then add = {} end
          if #o.rem > 0 or #add > 0 then
            local f = by_key[k]
            if not f then f = {index = o.index, arg = o.arg, rem = {}, add = {}}; by_key[k] = f end
            for _, m in ipairs(o.rem) do f.rem[#f.rem + 1] = m end
            for _, a in ipairs(add) do f.add[#f.add + 1] = a end
          end
        end
      end
    end
    local keys = {}
    for _, o in pairs(by_key) do
      table.sort(o.rem)
      table.sort(o.add, function(a, b) return a.member < b.member end)
      keys[#keys + 1] = o
    end
    table.sort(keys, function(a, b)
      if a.index ~= b.index then return a.index < b.index end
      return a.arg < b.arg
    end)
    local out = {}
    for _, o in ipairs(keys) do
      local i = 1
      while i <= #o.rem or i <= #o.add do
        local p = {index = o.index, arg = o.arg, rem = {}, add = {}}
        for j = i, math.min(i + PIECE - 1, #o.rem) do p.rem[#p.rem + 1] = o.rem[j] end
        for j = i, math.min(i + PIECE - 1, #o.add) do p.add[#p.add + 1] = o.add[j] end
        out[#out + 1] = p
        i = i + PIECE
      end
    end
    return out, nil
  end

  -- The queue a rule key lives in: the held rule's keys have a queue of their own
  -- (sprintfn xQueueOf; sprint.RuleOf).
  local function queue_of(key)
    local rule = string.match(key, '^[^:@]*')
    if rule == 'held' then return KEY_HELDQ end
    return KEY_AGENDA
  end
  -- The line a key names: <rule>@<seq> or <rule>@<seq>+<offset>: the digits after the
  -- first @, up to the end of the key or the first +, and no more than a line's seq
  -- can be (sprintfn xLineOf). What follows the + is the offset and is not read.
  local function line_of(key)
    local seq = string.match(key, '^[^@]*@(%d+)$') or string.match(key, '^[^@]*@(%d+)%+')
    if seq == nil then return nil end
    local n = tonumber(seq)
    if n == nil or n > MAX_SEQ then return nil end
    return n
  end

  -- A stored id of a member's control card at an epoch (sprint.StoredID(CtlID)).
  local function ctl_id(member, epoch)
    local id = 'ctl-' .. member
    if epoch ~= '0' then id = id .. '~' .. epoch end
    return id
  end

  -- The ids a request changes: the cards the derivation reads (sprintfn xChangedIDs).
  local function changed(e)
    return (e.kind == 'create' or e.kind == 'move' or e.kind == 'remove') and type(e.ids) == 'table' and #e.ids > 0
  end
  local function intent_ids(intent)
    local out = {}
    for _, id in ipairs({intent.card or '', intent.need or ''}) do out[#out + 1] = id end
    for _, id in ipairs(tbl(intent.needs)) do out[#out + 1] = id end
    for _, id in ipairs(tbl(intent.waiters)) do out[#out + 1] = id end
    return out
  end
  local function changed_ids(ctx, sp)
    local out = {}
    for _, e in ipairs(ctx.request.entries) do
      if changed(e) then for _, id in ipairs(e.ids) do out[#out + 1] = id end end
    end
    for _, in_ in ipairs(tbl(sp.intents)) do
      for _, id in ipairs(intent_ids(in_)) do
        if id ~= '' then out[#out + 1] = id end
      end
    end
    return sorted_unique(out)
  end

  -- before asks S.before for what X reads in the pre stage (sprintfn xBefore): the
  -- derivation's fields of every id a caller entry changes and every card an
  -- intent names, the stream field of cards not placed in a stream's row, and the
  -- control card of each member a guard names. The cache is Layer 1's own: S.plan
  -- reuses what is read here, so x_cmds finds the fields in tp.before.
  local function ask_before(ctx, sp)
    local asks, order = {}, {}
    local function add(t, ids, fields)
      local a = asks[t]
      if not a then a = {ids = {}, fields = {}}; asks[t] = a; order[#order + 1] = t end
      for _, id in ipairs(ids) do a.ids[#a.ids + 1] = id end
      for _, f in ipairs(fields) do a.fields[#a.fields + 1] = f end
    end
    for _, e in ipairs(ctx.request.entries) do
      if changed(e) then
        local fields = {}
        for _, f in ipairs(SP.x_defs.fields) do fields[#fields + 1] = f end
        if not STREAM_TABLES[e.t] then fields[#fields + 1] = 'stream' end
        add(e.t, e.ids, fields)
      end
    end
    for _, in_ in ipairs(tbl(sp.intents)) do
      local ids = {}
      for _, id in ipairs(intent_ids(in_)) do if id ~= '' then ids[#ids + 1] = id end end
      add('work', ids, SP.x_defs.fields)
    end
    for _, g in ipairs(tbl(sp.guards)) do
      if g.kind == 'memberup' and type(g.member) == 'string' and g.member ~= '' then
        add('fleet', {ctl_id(g.member, ctx.request_epoch)}, {'status'})
      end
    end
    table.sort(order)
    local recs = {}
    for _, t in ipairs(order) do
      local a = asks[t]
      local got, err = S().before(ctx, t, sorted_unique(a.ids), sorted_unique(a.fields))
      if err then return nil, err end
      recs[t] = got
    end
    return recs, nil
  end

  -- The score counter holds a whole number of at most SCORE_DIGITS digits (sprintfn
  -- xWholeScore): nil for anything else.
  local function whole_score(v)
    if type(v) ~= 'string' or v == '' or #v > SCORE_DIGITS or not string.match(v, '^%d+$') then return nil end
    return tonumber(v)
  end

  -- The shape of the parts X reads: REQUEST for the caller's fault (sprintfn
  -- xCheckShape): a guard of a kind 7 does not have, or without what its kind names;
  -- a counter change that sets a field it did not read, or a score that is not a
  -- whole number; an agenda key that names nothing, or that one step both finishes
  -- and requeues.
  -- A sent guard's max (sprintfn xSentBound): a decimal, an exponent of at most
  -- two digits, "(" before it for an open bound.
  local function sent_bound(b)
    if string.sub(b, 1, 1) == '(' then b = string.sub(b, 2) end
    local mant, exp = string.match(b, '^(-?[%d.]+)(.*)$')
    if mant == nil then return false end
    if not (string.match(mant, '^-?%d+$') or string.match(mant, '^-?%d+%.%d+$')) then return false end
    return exp == '' or string.match(exp, '^[eE][+-]?%d%d?$') ~= nil
  end
  -- A sent guard's key as its stream and max (sprintfn xSentKey): "sent:<stream>
  -- <max>", the stream a name with no blank or @; nil when it is not one.
  local function sent_key(key)
    if type(key) ~= 'string' or string.sub(key, 1, 5) ~= 'sent:' then return nil end
    local rest = string.sub(key, 6)
    local i = string.find(rest, ' ', 1, true)
    if i == nil then return nil end
    local stream, max = string.sub(rest, 1, i - 1), string.sub(rest, i + 1)
    if stream == '' or string.find(stream, '[ @]') or not sent_bound(max) then return nil end
    return stream, max
  end

  -- set_guard_of is a set guard's own shape (sprintfn xSetGuardOf; IT19): the JSON
  -- of a sprint.SetGuard of kind zguard over a stream's sent, elig, fresh or again,
  -- its bounds in the store's grammar and at least one count bound, each at most
  -- 2^53 - 1, and no other field. nil otherwise.
  local function set_guard_of(key)
    local ok, g = pcall(cjson.decode, key)
    if not ok or type(g) ~= 'table' or g.kind ~= 'zguard' or type(g.key) ~= 'string' then return nil end
    local idx, stream = string.match(g.key, '^(%a+):(.+)$')
    if not (idx == 'sent' or idx == 'elig' or idx == 'fresh' or idx == 'again') then return nil end
    if #stream > 128 or not string.match(stream, '^[%w_][%w_%-]*$') then return nil end
    if type(g.min) ~= 'string' or type(g.max) ~= 'string' or not S().bound(g.min) or not S().bound(g.max) then return nil end
    for k in pairs(g) do
      if k ~= 'kind' and k ~= 'key' and k ~= 'min' and k ~= 'max' and k ~= 'atleast' and k ~= 'atmost' then return nil end
    end
    local lo, hi = g.atleast, g.atmost
    if lo == cjson.null then lo = nil end
    if hi == cjson.null then hi = nil end
    if lo == nil and hi == nil then return nil end
    for _, v in ipairs({lo or 0, hi or 0}) do
      if type(v) ~= 'number' or v < 0 or v ~= math.floor(v) or v > 9007199254740991 then return nil end
    end
    if lo ~= nil and hi ~= nil and lo > hi then return nil end
    return {key = g.key, min = g.min, max = g.max, atleast = lo, atmost = hi}
  end

  local function check_shape(sp)
    local function bad(fmt, ...) return refuse('REQUEST', {}, fmt, ...) end
    for _, g in ipairs(tbl(sp.guards)) do
      local k = g.kind
      local member, key, score = g.member or '', g.key or '', tonumber(g.score or '0') or 0
      if k == 'memberup' or k == 'beatstale' or k == 'stranger' then
        if member == '' then return bad('a %s guard names no member', k) end
      elseif k == 'due' then
        if key == '' or (score < 0 and score ~= ABSENT) then return bad('a due guard names no entry, or a score that is neither a time nor XGuardAbsent') end
      elseif k == 'hold' then
        if member == '' or not string.find(key, '=', 1, true) then return bad('a hold guard names no subject, or a key that is not <type>|<cause>=<value>') end
      elseif k == 'clock' then
        local known = false
        for _, f in ipairs(CLOCK_FIELDS) do known = known or f == key end
        if not known or (score < 0 and score ~= ABSENT) then return bad('a clock guard names %q, which is not a clock field, or a score that is neither a time nor XGuardAbsent', key) end
      elseif k == 'sent' then
        if sent_key(key) == nil then return bad('a sent guard\'s key %q is not sent:<stream> <max>', key) end
      elseif k == 'counter' then
        if (key ~= 'score' and key ~= 'streams') or score < 0 then
          return bad('a counter guard names %q, which is not score or streams, or a value below zero', key)
        end
      elseif k == 'version' then
        if key == '' or string.find(key, '[%s@]') or score < 0 or score > VERSION_MAX then
          return bad('a version guard names %q, which is not a table, or a version that is not a whole number', key)
        end
      elseif k == 'setguard' then
        if set_guard_of(key) == nil then return bad('a set guard is not a zguard over a sprint index with bounds and a count: %q', key) end
      elseif k ~= 'coordinator' then
        return bad('%q is not a kind of guard', tostring(k))
      end
    end
    for _, q in ipairs(tbl(sp.quarantine)) do
      if q.id == nil or q.id == '' then return bad('a quarantine names no card') end
    end
    local counter = sp.sprint and sp.sprint.counter or nil
    if type(counter) == 'table' then
      local read, set, names = tbl(counter.read), tbl(counter.set), {}
      for f in pairs(set) do names[#names + 1] = f end
      table.sort(names)
      for _, f in ipairs(names) do
        if read[f] == nil then return bad('the counter sets %s and did not read it: a counter change guards every field it writes', f) end
      end
      if set.score ~= nil and whole_score(set.score) == nil then
        return bad('the score counter is set to %q, which is not a whole number of at most %d digits', tostring(set.score), SCORE_DIGITS)
      end
    end
    local requeued = {}
    for _, k in ipairs(tbl(sp.requeue)) do
      if k == '' then return bad('an agenda key is requeued and names nothing') end
      requeued[k] = true
    end
    for _, k in ipairs(tbl(sp.done)) do
      if k == '' then return bad('an agenda key is finished and names nothing') end
      if requeued[k] then return bad('the agenda key %s is both finished and requeued in one step', k) end
    end
    return nil
  end

  -- The streams a step's caller entries and intents change, with the cards that
  -- name each (sprintfn xStreamsTouched).
  local function streams_touched(ctx, sp, recs)
    local streams, cards = {}, {}
    local function add(stream, id)
      if stream == nil or stream == '' then return end
      if not cards[stream] then streams[#streams + 1] = stream; cards[stream] = {} end
      cards[stream][#cards[stream] + 1] = id
    end
    local function row_of(cell)
      if type(cell) ~= 'string' then return '' end
      local row = string.match(cell, '^(.*):[^:]*$')
      return row or ''
    end
    for _, e in ipairs(ctx.request.entries) do
      if changed(e) then
        for i, id in ipairs(e.ids) do
          if STREAM_TABLES[e.t] then
            add(row_of(e.from), id)
            if row_of(e.to) ~= row_of(e.from) then add(row_of(e.to), id) end
          else
            if e.kind ~= 'create' then
              local rec = recs[e.t] and recs[e.t][id]
              if rec == nil then return nil, nil, refuse('CONFIG', {}, 'X.pre was not given the before-state of %s card %s', e.t, id) end
              add(field(rec, 'stream'), id)
            end
            if e.set and e.set.stream ~= nil then add(e.set.stream, id) end
            if e.each and e.each[i] and e.each[i].stream ~= nil then add(e.each[i].stream, id) end
          end
        end
      end
    end
    for _, in_ in ipairs(tbl(sp.intents)) do
      local ids = {in_.card or ''}
      for _, id in ipairs(tbl(in_.waiters)) do ids[#ids + 1] = id end
      for _, id in ipairs(ids) do
        local rec = recs.work and recs.work[id]
        if id ~= '' and placed(rec) then add(rec.place.row, id) end
      end
    end
    return streams, cards, nil
  end

  -- An op identity is a part of the op a stream's mark names: <op>/p<k> or
  -- <op>/abort (sprintfn ownsPart).
  local function owns_part(op_id, mark)
    local prefix = mark .. '/'
    if string.sub(op_id, 1, #prefix) ~= prefix then return false end
    local rest = string.sub(op_id, #prefix + 1)
    if rest == 'abort' then return true end
    return string.match(rest, '^p%d+$') ~= nil
  end

  local function read_clock(ctx, now)
    local vals, err = hmget(ctx, skey(ctx, KEY_CLOCK), CLOCK_FIELDS)
    if err then return nil, err end
    local c = {vals = vals, now = now, stopped = false, since = 0, past = 0}
    for _, f in ipairs(CLOCK_FIELDS) do
      local v = vals[f]
      if v ~= nil and v ~= '' and not S().uint(v) then
        return nil, refuse('CONFIG', {}, "the clock's field %s holds %q, not a whole number", f, v)
      end
    end
    c.past = tonumber(vals.stopped_ms or '0') or 0
    if vals.stopped_since_ms ~= nil and vals.stopped_since_ms ~= '' then
      c.stopped = true
      c.since = tonumber(vals.stopped_since_ms)
    end
    return c, nil
  end
  local function clock_r(c)
    local r = c.now - c.past
    if c.stopped then r = r - (c.now - c.since) end
    return r
  end
  local function clock_value(c, name)
    local v = c.vals[name]
    if v == nil or v == '' then return ABSENT end
    return tonumber(v)
  end

  -- A stored version (sprintfn xVersionValue): 0 for none, CONFIG for one that is
  -- not a canonical whole number at most VERSION_MAX.
  local function version_value(t, v)
    if v == nil or v == '' then return 0, nil end
    if not S().uint(v) or #v > 16 or tonumber(v) > VERSION_MAX then
      return nil, refuse('CONFIG', {}, 'the version of table %s holds %q, not a whole number', t, v)
    end
    return tonumber(v), nil
  end

  local function guard(ctx, recs, g, clock)
    local function fail(fmt, ...) return refuse('XGUARD', {}, 'XGUARD: ' .. fmt, ...) end
    local e = ctx.request_epoch
    local k = g.kind
    if k == 'memberup' then
      local rec = recs.fleet and recs.fleet[ctl_id(g.member, e)]
      if rec == nil then return refuse('CONFIG', {}, 'X.pre was not given the control card of member %s', g.member) end
      if not rec.exists or field(rec, 'status') ~= 'up' then return fail('member %s is not up', g.member) end
    elseif k == 'beatstale' then
      local scores, err = zmscore(ctx, ekey(ctx, KEY_DUE, e), {'beat:' .. g.member})
      if err then return err end
      local score = scores['beat:' .. g.member]
      if score ~= nil and score > clock_r(clock) then
        return fail('a beat of member %s since the read moved its beat to %d, above R (%d)', g.member, score, clock_r(clock))
      end
    elseif k == 'due' then
      local set = KEY_DUE
      if string.sub(g.key, 1, 4) == 'cut:' then set = KEY_CUT end
      local scores, err = zmscore(ctx, ekey(ctx, set, e), {g.key})
      if err then return err end
      local score, want = scores[g.key], tonumber(g.score)
      if want == ABSENT and score ~= nil then
        return fail('the entry %s was absent when read and is at %d now', g.key, score)
      elseif want ~= ABSENT and (score == nil or score ~= want) then
        return fail('the entry %s moved since the read (it was %d)', g.key, want)
      end
    elseif k == 'hold' then
      local i = #g.key
      while i > 0 and string.sub(g.key, i, i) ~= '=' do i = i - 1 end
      local name, want = string.sub(g.key, 1, i - 1), string.sub(g.key, i + 1)
      local vals, err = hmget(ctx, ekey(ctx, KEY_JOPEN .. g.member, e), {name})
      if err then return err end
      if vals[name] ~= want then return fail('the field %s of %s no longer holds %s', name, g.member, want) end
    elseif k == 'clock' then
      if clock_value(clock, g.key) ~= tonumber(g.score) then return fail("the clock's %s moved since the read", g.key) end
    elseif k == 'coordinator' then
      local v, err = rd(ctx, {'GET', skey(ctx, KEY_COORD)}, 'string', RESERVE_ONE)
      if err then return err end
      if (v or '') ~= (g.member or '') then return fail('the coordinator is %q now, not %q', tostring(v or ''), g.member or '') end
    elseif k == 'stranger' then
      local vals, err = hmget(ctx, skey(ctx, KEY_STRANGERS), {g.member})
      if err then return err end
      if vals[g.member] == NOTICED then return fail('machine %s has been noticed already', g.member) end
    elseif k == 'sent' then
      -- Layer 1's S.zguard over the stream's sentinel index: RANGECOUNT when a
      -- sentinel is placed at or below max, which is X's XGUARD here.
      local stream, max = sent_key(g.key)
      local _, err = S().zguard(ctx, ekey(ctx, 'sent:' .. stream, e), {kind = 'rcount', min = '-inf', max = max, atmost = 0})
      if err then
        if err.code == 'RANGECOUNT' then return fail('a sentinel of %s is placed at or below %s since the read', stream, max) end
        return err
      end
    elseif k == 'counter' then
      local vals, err = hmget(ctx, ekey(ctx, KEY_NEXT, e), {g.key})
      if err then return err end
      local got = vals[g.key]
      if got == nil then got = '0'
      elseif not S().uint(got) then return refuse('CONFIG', {}, "the counter's %s holds %q, not a whole number", g.key, got) end
      local want = string.format('%d', tonumber(g.score or 0) or 0)
      if got ~= want then return fail("the counter's %s is %s now, read as %s", g.key, got, want) end
    elseif k == 'version' then
      local vals, err = hmget(ctx, ekey(ctx, KEY_VERSION, e), {g.key})
      if err then return err end
      local n
      n, err = version_value(g.key, vals[g.key])
      if err then return err end
      local want = tonumber(g.score or 0) or 0
      if n ~= want then return fail('the version of table %s is %d now, read as %d', g.key, n, want) end
    elseif k == 'setguard' then
      -- Layer 1's S.zguard over the index (1.5.4, 2.3 R3): RANGECOUNT when the count moved.
      local sg = set_guard_of(g.key)
      local _, err = S().zguard(ctx, ekey(ctx, sg.key, e), {kind = 'rcount', min = sg.min, max = sg.max, atleast = sg.atleast, atmost = sg.atmost})
      if err then return err end
    end
    return nil
  end

  -- x_pre: the pre stage (sprintfn XPre), in the design's order.
  function SP.x_pre(ctx, sp, obs)
    local meta = tbl(sp.meta)
    local e = ctx.request_epoch
    local now = tonumber(ctx.now_ms)
    local x = {}
    ctx.x = x

    -- The before-state X reads, once: Layer 1 caches it for the plan. The twin
    -- reads it before X.pre, so a refusal of a read comes before X's own.
    local recs, err = ask_before(ctx, sp)
    if err then return nil, err end
    err = check_shape(sp)
    if err then return nil, err end

    -- Lease generation (1.4.3, T1): a step with a lease part is the lease's own.
    if meta.tick and sp.lease == nil then
      local vals
      vals, err = hmget(ctx, skey(ctx, KEY_LEASE), {'gen', 'name'})
      if err then return nil, err end
      if vals.gen == nil or vals.gen ~= (meta.gen or '0') then
        if vals.gen == nil then return nil, refuse('STALEGEN', {}, 'STALEGEN: no lease is held; this loop is not the tick') end
        return nil, refuse('STALEGEN', {}, "STALEGEN: the tick's lease is at generation %s, held by %s; this loop is not the tick", vals.gen, tostring(vals.name or ''))
      end
    end

    -- The clock, read once: STOPPED, R and MACHINESTATE.
    local clock = {vals = {}, now = now, stopped = false, since = 0, past = 0}
    local need_clock = meta.tick or sp.clock ~= nil
    for _, g in ipairs(tbl(sp.guards)) do
      if g.kind == 'beatstale' or g.kind == 'clock' then need_clock = true end
    end
    if need_clock then
      clock, err = read_clock(ctx, now)
      if err then return nil, err end
    end

    -- STOPPED (1.4.3, T2).
    if meta.tick and clock.stopped then
      local touches = #tbl(sp.intents) > 0
      for _, en in ipairs(ctx.request.entries) do touches = touches or changed(en) end
      if touches then
        return nil, refuse('STOPPED', {}, 'STOPPED: the machine is STOPPED, since %d, and the tick does not move cards', clock.since)
      end
    end

    -- DROPPING (1.3.5, 1.5.4).
    local streams, cards
    streams, cards, err = streams_touched(ctx, sp, recs)
    if err then return nil, err end
    if #streams > 0 then
      local marks
      marks, err = hmget(ctx, ekey(ctx, KEY_DROPPING, e), streams)
      if err then return nil, err end
      for _, s in ipairs(streams) do
        local mark = marks[s]
        if mark ~= nil and not (ctx.op ~= nil and owns_part(ctx.op, mark)) then
          return nil, refuse('DROPPING', {ids = cards[s], rows = {s}}, 'DROPPING: stream %s is being dropped by op %s', s, mark)
        end
      end
    end

    -- NOTCOORD (1.5.3, 2.2).
    if COORD_VERBS[meta.verb or ''] then
      local coord
      coord, err = rd(ctx, {'GET', skey(ctx, KEY_COORD)}, 'string', RESERVE_ONE)
      if err then return nil, err end
      if coord == false or coord == nil or coord ~= (meta.actor or '') then
        return nil, refuse('NOTCOORD', {}, 'NOTCOORD: %s is a coordinator\'s verb and %q is not the coordinator', meta.verb, tostring(meta.actor or ''))
      end
    end

    -- COUNTER (1.3.1, U2): the counter as read, and every placed score below it.
    local counter = sp.sprint and sp.sprint.counter or nil
    if counter ~= nil and type(counter) ~= 'table' then counter = nil end
    local places_scores = false
    for _, en in ipairs(ctx.request.entries) do
      if en.t == 'work' and (en.kind == 'create' or en.kind == 'move') and type(en.scores) == 'table' and #en.scores > 0 then
        places_scores = true
      end
    end
    if counter ~= nil or places_scores then
      local want, names = {score = true}, {}
      local read = counter and tbl(counter.read) or {}
      for f in pairs(read) do want[f] = true end
      for f in pairs(want) do names[#names + 1] = f end
      table.sort(names)
      local stored
      stored, err = hmget(ctx, ekey(ctx, KEY_NEXT, e), names)
      if err then return nil, err end
      for _, f in ipairs(names) do
        if read[f] ~= nil and (stored[f] or '') ~= read[f] then
          return nil, refuse('COUNTER', {}, 'COUNTER: the counter %s moved since the read (it was %q, it is %q)', f, read[f], stored[f] or '')
        end
      end
      local limit_text, have = stored.score, stored.score ~= nil
      local set = counter and tbl(counter.set) or {}
      -- The counter only rises (U2): a step that sets it below the value it holds would
      -- let the next add land on a score already placed (sprintfn xCounterRises).
      if have and set.score ~= nil then
        local was = whole_score(stored.score)
        if was == nil then return nil, refuse('CONFIG', {}, 'the score counter holds %q, not a whole number', tostring(stored.score)) end
        if whole_score(set.score) < was then
          return nil, refuse('COUNTER', {}, 'COUNTER: the step sets the score counter to %s, below %s which it holds; the counter only rises', set.score, stored.score)
        end
      end
      if set.score ~= nil then limit_text, have = set.score, true end
      local limit = whole_score(limit_text)
      if have and limit == nil then return nil, refuse('CONFIG', {}, 'the score counter holds %q, not a whole number', tostring(limit_text)) end
      local over = {}
      for _, en in ipairs(ctx.request.entries) do
        if en.t == 'work' and (en.kind == 'create' or en.kind == 'move') then
          for i, s in ipairs(tbl(en.scores)) do
            local v = tonumber(s)
            if v == nil or not have or v >= limit then over[#over + 1] = en.ids[i] end
          end
        end
      end
      if #over > 0 then
        if not have then return nil, refuse('COUNTER', {ids = over}, 'COUNTER: the sprint has no score counter, so no score is below it') end
        return nil, refuse('COUNTER', {ids = over}, 'COUNTER: a score is at or above the counter %s and the step does not raise it', limit_text)
      end
    end

    -- XGUARD (1.3.5).
    for _, g in ipairs(tbl(sp.guards)) do
      err = guard(ctx, recs, g, clock)
      if err then return nil, err end
    end

    -- MACHINESTATE (1.2, A2; errata 1, the addendum: a clear runs only while STOPPED).
    local verb = sp.clock and sp.clock.verb or nil
    if verb == 'stop' and clock.stopped then
      return nil, refuse('MACHINESTATE', {}, 'MACHINESTATE: the machine is already stopped, since %d', clock.since)
    elseif verb == 'start' and not clock.stopped then
      return nil, refuse('MACHINESTATE', {}, 'MACHINESTATE: the machine is already running')
    elseif verb == 'clear' and not clock.stopped then
      return nil, refuse('MACHINESTATE', {}, 'MACHINESTATE: a clear runs only while the machine is STOPPED')
    end

    -- What the derivation reads: the quarantine of the step's cards (one probe a
    -- piece), the orders of the requeued keys, and every indexed field well formed.
    x.quarantined = {}
    local ids = changed_ids(ctx, sp)
    if #ids > 0 then
      -- One HLEN, which ends the read when no card is marked; then the ids, 16 to a
      -- read and each reserved at the field cap (sprintfn xReadMarks).
      local qkey = ekey(ctx, KEY_QUARANTINE, e)
      local marked
      marked, err = rd(ctx, {'HLEN', qkey}, 'hash', RESERVE_ONE)
      if err then return nil, err end
      if (tonumber(marked) or 0) > 0 then
        local marks
        marks, err = hmget(ctx, qkey, ids, MARK_PIECE, MARK_VALUE_CAP)
        if err then return nil, err end
        for id in pairs(marks) do x.quarantined[id] = true end
      end
    end
    x.requeue = {}
    local by_queue = {[KEY_AGENDA] = {}, [KEY_HELDQ] = {}}
    for _, k in ipairs(tbl(sp.requeue)) do
      local list = by_queue[queue_of(k)]
      list[#list + 1] = k
    end
    for _, queue in ipairs({KEY_AGENDA, KEY_HELDQ}) do
      local keys = sorted_unique(by_queue[queue])
      if #keys > 0 then
        local scores
        scores, err = zmscore(ctx, ekey(ctx, queue, e), keys)
        if err then return nil, err end
        for _, k in ipairs(keys) do
          if scores[k] == nil then
            local line = line_of(k)
            if line == nil then
              return nil, refuse('REQUEST', {}, 'the key %s is requeued, is not queued and names no line, so it has no order', k)
            end
            x.requeue[k] = line
          end
        end
      end
    end
    for _, en in ipairs(ctx.request.entries) do
      if changed(en) then
        for i, id in ipairs(en.ids) do
          local before
          if en.kind ~= 'create' then
            local rec = recs[en.t] and recs[en.t][id]
            if rec == nil then return nil, refuse('CONFIG', {}, 'X.pre was not given the before-state of %s card %s', en.t, id) end
            if rec.exists then
              local missing = unobserved(rec)
              if #missing > 0 then return nil, refuse('CONFIG', {}, 'X.pre was not given the fields %s of %s card %s', table.concat(missing, ','), en.t, id) end
              local cerr
              before, cerr = card_of(en.t, id, rec, fields_of(rec))
              if not cerr then local _, oerr = card_ops(before, before); cerr = oerr end
              if cerr then return nil, refuse('DRIFT', {table = en.t, ids = {id}}, 'DRIFT: %s', cerr) end
            end
          end
          local after = {table = en.t, id = id, row = '', col = '', score = before and before.score or 0, fields = {}}
          if before then
            after.row, after.col = before.row, before.col
            for k, v in pairs(before.fields) do after.fields[k] = v end
          end
          for k, v in pairs(tbl(en.set)) do after.fields[k] = v end
          if en.each and en.each[i] then for k, v in pairs(en.each[i]) do after.fields[k] = v end end
          for _, k in ipairs(tbl(en.unset)) do after.fields[k] = nil end
          local aerr
          if en.kind == 'remove' then
            after.row, after.col = '', ''
          else
            local cell = en.to or en.from
            if cell then
              local row, col = S().cell(cell)
              if not row then aerr = 'bad cell ' .. tostring(cell) else after.row, after.col = row, col end
            end
            if not aerr and en.scores and en.scores[i] ~= nil then
              local score = tonumber(en.scores[i])
              if score == nil then aerr = string.format('%s card %s is given the score %q, not a number', en.t, id, en.scores[i]) else after.score = score end
            end
          end
          if not aerr then local _, oerr = card_ops(after, after); aerr = oerr end
          if aerr then return nil, refuse('REQUEST', {table = en.t, ids = {id}}, '%s', aerr) end
        end
      end
    end
    for _, in_ in ipairs(tbl(sp.intents)) do
      for _, id in ipairs(intent_ids(in_)) do
        local rec = id ~= '' and recs.work and recs.work[id] or nil
        if rec and rec.exists then
          local missing = unobserved(rec)
          if #missing > 0 then return nil, refuse('CONFIG', {}, 'X.pre was not given the fields %s of card %s', table.concat(missing, ','), id) end
          local c, cerr = card_of('work', id, rec, fields_of(rec))
          if not cerr then local _, oerr = card_ops(c, c); cerr = oerr end
          if cerr then return nil, refuse('DRIFT', {table = 'work', ids = {id}}, 'DRIFT: %s', cerr) end
        end
      end
    end
    -- The versions (sprintfn xVersions): each table whose cards the step may
    -- change, a caller entry's or the work table for an intent, read in one
    -- probe and moved on by one in x_cmds (errata 3 H17).
    local tables, tseen = {}, {}
    for _, en in ipairs(ctx.request.entries) do
      if changed(en) and not tseen[en.t] then tseen[en.t] = true; tables[#tables + 1] = en.t end
    end
    if #tbl(sp.intents) > 0 and not tseen.work then tseen.work = true; tables[#tables + 1] = 'work' end
    x.versions = {}
    if #tables > 0 then
      table.sort(tables)
      local stored
      stored, err = hmget(ctx, ekey(ctx, KEY_VERSION, e), tables)
      if err then return nil, err end
      for _, t in ipairs(tables) do
        local n
        n, err = version_value(t, stored[t])
        if err then return nil, err end
        x.versions[#x.versions + 1] = {t, string.format('%d', n + 1)}
      end
    end
    x.sp = sp
    return true, nil
  end

  -- Score text for ZADD, the shortest decimal that reads back as the same number and
  -- has no exponent (sprintfn xScore): a whole number as itself; any other from the
  -- fewest of 15, 16 and 17 significant digits that read back equal.
  local function score_text(n)
    if n == math.floor(n) and math.abs(n) < 1e15 then return string.format('%d', n) end
    local a, sign = math.abs(n), ''
    if n < 0 then sign = '-' end
    local text
    for digits = 15, 17 do
      text = string.format('%.' .. (digits - 1) .. 'e', a)
      if tonumber(text) == a then break end
    end
    local first, rest, exp = string.match(text, '^(%d)%.?(%d*)e([+-]%d+)$')
    local digs = string.gsub(first .. rest, '0+$', '')
    if digs == '' then digs = '0' end
    local point = tonumber(exp) + 1
    if point <= 0 then return sign .. '0.' .. string.rep('0', -point) .. digs end
    if point >= #digs then return sign .. digs .. string.rep('0', point - #digs) end
    return sign .. string.sub(digs, 1, point) .. '.' .. string.sub(digs, point + 1)
  end
  SP.x_score_text = score_text

  -- x_cmds: commands only, in A1's order (sprintfn XCmds): the derivation, then the
  -- quarantine's removals, then the agenda's edits. It cannot refuse: if it cannot
  -- derive it returns the one descriptor S.prepare refuses REQUEST, so that nothing
  -- is written.
  function SP.x_cmds(ctx, tp, lp)
    local s = S()
    local plan = {commands = {}}
    local poison = {commands = {{}}}
    local x = ctx.x
    if x == nil or x.sp == nil then return poison end
    local sp = x.sp
    local e = ctx.write_epoch
    local function stage(command, key, kind, args)
      local ok, err = s.stage(ctx, plan, command, key, kind, args)
      return ok ~= nil and err == nil
    end

    local changes, quarantined = {}, {}
    for _, pe in ipairs(tp.entries) do
      -- a guard, rows, count, rcount, rowset or advance entry changes no card
      if (pe.kind == 'create' or pe.kind == 'move' or pe.kind == 'remove') and #pe.changed_ids > 0 then
        for j, id in ipairs(pe.changed_ids) do
          local rec = tp.before[pe.table] and tp.before[pe.table][id]
          local before
          if rec and rec.exists then
            local missing = unobserved(rec)
            if #missing > 0 then return poison end
            local cerr
            before, cerr = card_of(pe.table, id, rec, fields_of(rec))
            if cerr then return poison end
          end
          local fc = pe.field_changes[j] or {}
          local fields = {}
          if before then for k, v in pairs(before.fields) do fields[k] = v end end
          for k, v in pairs(tbl(fc.set)) do fields[k] = v end
          for _, k in ipairs(tbl(fc.unset)) do fields[k] = nil end
          local after = {table = pe.table, id = id, row = '', col = '', score = 0, fields = fields}
          if pe.kind ~= 'remove' and not is_null(pe.to) then
            local row, col = s.cell(pe.to)
            if not row then return poison end
            local score = tonumber(pe.after_scores[j])
            if score == nil then return poison end
            after.row, after.col, after.score = row, col, score
          end
          local ch = {before = before, after = after}
          if x.quarantined[id] then quarantined[#quarantined + 1] = ch else changes[#changes + 1] = ch end
        end
      end
    end
    local ops, err = fold(changes)
    if err then return poison end
    -- A quarantined card is given no membership but sent, and leaves every index its
    -- change ends it in (1.3.2; I1, D1): folded once, not a card at a time.
    local qops, qerr = fold(quarantined, true)
    if qerr then return poison end
    for _, o in ipairs(qops) do ops[#ops + 1] = o end
    for _, o in ipairs(ops) do
      local key = ctx.space .. 'sprint:' .. key_name(o.index, o.arg) .. '@' .. e
      if #o.rem > 0 and not stage('ZREM', key, 'zset', o.rem) then return poison end
      if #o.add > 0 then
        local args = {}
        for _, a in ipairs(o.add) do args[#args + 1] = score_text(a.score); args[#args + 1] = a.member end
        if not stage('ZADD', key, 'zset', args) then return poison end
      end
    end

    -- The quarantined ids leave elig, fresh and again of their stream, and askwait (1.3.5).
    local function zrem_pieces(key, members)
      for i = 1, #members, PIECE do
        local piece = {}
        for j = i, math.min(i + PIECE - 1, #members) do piece[#piece + 1] = members[j] end
        if not stage('ZREM', key, 'zset', piece) then return false end
      end
      return true
    end
    local q = tbl(sp.quarantine)
    if #q > 0 then
      local by_stream, all = {}, {}
      for _, c in ipairs(q) do
        all[#all + 1] = c.id
        if c.stream ~= nil and c.stream ~= '' then
          by_stream[c.stream] = by_stream[c.stream] or {}
          table.insert(by_stream[c.stream], c.id)
        end
      end
      local streams = {}
      for stream in pairs(by_stream) do streams[#streams + 1] = stream end
      table.sort(streams)
      for _, stream in ipairs(streams) do
        local ids = sorted_unique(by_stream[stream])
        for _, index in ipairs({'elig', 'fresh', 'again'}) do
          if not zrem_pieces(ctx.space .. 'sprint:' .. index .. ':' .. stream .. '@' .. e, ids) then return poison end
        end
      end
      if not zrem_pieces(ekey(ctx, KEY_ASKWAIT, e), sorted_unique(all)) then return poison end
    end

    -- The agenda: a requeued key is added before the key it continues is removed
    -- (A1); a done key the sprint part parks is the part's to remove.
    for _, queue in ipairs({KEY_AGENDA, KEY_HELDQ}) do
      local keys = {}
      for _, k in ipairs(sorted_unique(tbl(sp.requeue))) do
        if x.requeue[k] ~= nil and queue_of(k) == queue then keys[#keys + 1] = k end
      end
      for i = 1, #keys, PIECE do
        local args = {}
        for j = i, math.min(i + PIECE - 1, #keys) do args[#args + 1] = score_text(x.requeue[keys[j]]); args[#args + 1] = keys[j] end
        if not stage('ZADD', ekey(ctx, queue, e), 'zset', args) then return poison end
      end
    end
    local parked = {}
    for _, k in ipairs(tbl(sp.sprint and sp.sprint.park)) do parked[k.key] = true end
    local by_queue = {[KEY_AGENDA] = {}, [KEY_HELDQ] = {}}
    for _, k in ipairs(sorted_unique(tbl(sp.done))) do
      if parked[k] == nil then table.insert(by_queue[queue_of(k)], k) end
    end
    for _, queue in ipairs({KEY_AGENDA, KEY_HELDQ}) do
      if not zrem_pieces(ekey(ctx, queue, e), by_queue[queue]) then return poison end
    end
    -- each table a card of which the plan changes, one version on (errata 3 H17):
    -- x_pre read the version of every table the step could change
    local tchanged, nchanged = {}, 0
    for _, pe in ipairs(tp.entries) do
      if (pe.kind == 'create' or pe.kind == 'move' or pe.kind == 'remove') and #pe.changed_ids > 0 and not tchanged[pe.table] then
        tchanged[pe.table] = true
        nchanged = nchanged + 1
      end
    end
    local args = {}
    for _, v in ipairs(tbl(x.versions)) do
      if tchanged[v[1]] then
        args[#args + 1] = v[1]; args[#args + 1] = v[2]
        nchanged = nchanged - 1
      end
    end
    if nchanged ~= 0 then return poison end
    if #args > 0 and not stage('HSET', ekey(ctx, KEY_VERSION, e), 'hash', args) then return poison end
    return plan
  end

  SP.phase('x_pre', SP.x_pre)
  SP.phase('x_cmds', SP.x_cmds)
end
end
