-- The derive phase: intents to Layer 1 entries (the upper design,
-- EVENT-DRIVEN-TICK version 2.1, 1.3.3; item IT14). A skeleton written to the
-- interfaces of sprint_00_core.lua and Layer 1's S: no store loads it before
-- gate G0 (Layer 1 revision 4 pinned, Layer 2 accepted again against its
-- hash). Like every sprint file it runs only when the tset assembler set
-- NS.tset_profile; in the legacy library it defines nothing. Its Go twin is
-- internal/sprint/sprintfn/twin_intents.go, and a test runs both on the same
-- scenarios and holds their entries, notes, commands and refusals equal.
--
-- Four kinds of effect cannot be planned from a read: waitfor, needmet,
-- needgone and waive. They travel as intents, and NS.SP.derive(ctx, intents,
-- obs) turns each into what the real before-state says, inside the call:
--   * the field changes of the waiting cards, one entry a card, folded, with
--     revs equal to the revision just read (Layer 1's guards agree with it by
--     construction), returned for the core to append to ctx.request.entries;
--   * the commands on the sprint's own keys wait:<n> and missing, staged on
--     ctx with S.command (nothing runs before commit) and read back by
--     NS.SP.intent_commands(ctx), which X.plan's x_cmds appends to its plan;
--   * the requests to J for the judgments that open and close, returned as
--     the note requests the core gives j_decide.
--
-- The cost of a step grows with its input, never with the number of its
-- admissions: the needs walk expands each reachable card once a step (one
-- depth-first search with a step-wide memo of the strongly connected
-- components it has found, shared by every admission and need); the request's
-- entries are read into one index once; and the probes of the sprint's own keys
-- are batched by key (wait:<n>, missing, quarantine) and, for the judgments a
-- waive reads, by card (one HMGET of {p}jopen:<card>@e). ctx.intent_work counts
-- the first two, and a test holds the counts equal to the Go twin's.
--
-- Where the phase interface cannot carry the item (listed in twin_intents.go
-- and on the pull request): the commands have no slot in derive's return, so
-- they wait on ctx for x_cmds; the walk reads records it cannot name in
-- advance, so it calls S.before as it goes (S.before caches per call, AL1);
-- an admission's open "goes into the add's own create entry" but a preplan may
-- only append entries (L1 1.1) and a second entry on a created card is TWICE,
-- so the create entry carries the card's needs and open as the builder read
-- them and derive refuses XGUARD when the real count differs; and the phase
-- sees the request through ctx.request.entries, read and never changed.
if NS.tset_profile then
do
  local SP = NS.SP
  local WAITING, LANDED = 'waiting', 'landed'
  -- 1.3.3: at most 2,000 records a step; 3: at most 64 needs on a card; 1.0:
  -- the chunk bounds the waiters of a step.
  local WALK_MAX, NEEDS_MAX, WAITERS_MAX = 2000, 64, 2000
  -- 1.3.1 gives the members of wait:<n> no score; a sorted set needs one.
  local WAIT_SCORE = '0'
  -- The cards of a cycle's chain its message names before "..." (1.3.3).
  local CHAIN_NAMED = 8
  -- The pieces of one command (L1 1.4).
  local PIECE = 1000
  -- The bytes a checked read of the sprint's own keys reserves (L1 1.1,
  -- S.readcmd): a score or a count, the clock's two fields, and the fixed
  -- shell of a reply.
  local RESERVE_SCORE, RESERVE_COUNT, RESERVE_CLOCK, RESERVE_SHELL = 64, 32, 128, 16
  -- The bytes one member of a batched read of {p}quarantine@e reserves: the
  -- value is the code, the rule, the stream and the refusal's cells.
  local RESERVE_QUARANTINE = 512
  -- The bytes one field of a batched read of {p}jopen:<card>@e reserves: the
  -- value is the id of the note that opened the judgment, or a hold's mark.
  local RESERVE_JUDGMENT = 512
  -- The judgment types, the words of sprint.NMissingNeed and sprint.NBlocked.
  local TYPE_MISSING = 'a primary is blocked on something missing'
  local TYPE_DROPPED = 'a primary is blocked on something dropped'
  local TEXT_MISSING = '%s has no record yet; the cards that wait for it wait for it to be created'
  local TEXT_DROPPED = '%s was dropped; the cards that wait for it cannot go on until the coordinator waives it or drops them'
  -- The fields of a card the indexes and the due entries read before a step
  -- (1.3.2): sprint.IndexFields(). X.plan derives from a changed card's
  -- before-state, and the entries of this phase are appended after the
  -- builder's, so they declare them. A test holds this list equal to Go's.
  local INDEX_FIELDS = {'attempt', 'bound', 'due_mergeidle', 'due_unbegun', 'due_unfinished', 'due_unreported',
    'due_untaken', 'kind', 'needs', 'open', 'refused'}

  local function work() return SP.work_table or 'work' end

  -- sprint.Split: a comma list, each item trimmed, without empty items.
  local function split(s)
    local out = {}
    for item in string.gmatch(s or '', '[^,]+') do
      item = string.match(item, '^%s*(.-)%s*$')
      if item ~= '' then out[#out + 1] = item end
    end
    return out
  end
  local function contains(list, x)
    for _, v in ipairs(list) do if v == x then return true end end
    return false
  end
  -- dedup: the ids without repeats, in the order they first appear.
  local function dedup(ids)
    local seen, out = {}, {}
    for _, id in ipairs(ids) do
      if not seen[id] then seen[id] = true; out[#out + 1] = id end
    end
    return out
  end
  -- key: a per-epoch key of the sprint, {p}<name>@<e> (1.0).
  local function key(ctx, name) return ctx.space .. 'sprint:' .. name .. '@' .. ctx.request_epoch end

  local function refuse(r, code, ids, fmt, ...)
    return r.S.refuse(code, {ids = ids or {}}, string.format(fmt, ...))
  end
  local function drift(r, card, why) return refuse(r, 'DRIFT', {card}, '%s %s', card, why) end

  -- check: the static shape of a step's intents (3, 1.0, 1.3.3).
  local function check(S, intents)
    local waiters, admitted = 0, {}
    for i, it in ipairs(intents) do
      local kind = it.kind
      local function bad(fmt, ...)
        return S.refuse('REQUEST', {ids = {}}, string.format('intent %d (%s): %s', i - 1, tostring(kind), string.format(fmt, ...)))
      end
      if kind == 'waitfor' or kind == 'waive' then
        if not it.card or it.card == '' then return bad('names no card') end
        local needs = dedup(it.needs or {})
        if #needs == 0 or #needs > NEEDS_MAX then return bad('names %d needs; a card names 1 to %d', #needs, NEEDS_MAX) end
        if contains(needs, '') then return bad('names a need with no id') end
        if kind == 'waitfor' then
          if admitted[it.card] then return bad('admits %s twice in one step', it.card) end
          admitted[it.card] = true
        end
      elseif kind == 'needmet' or kind == 'needgone' then
        if not it.need or it.need == '' then return bad('names no need') end
        if contains(it.waiters or {}, '') then return bad('names a waiter with no id') end
        waiters = waiters + #dedup(it.waiters or {})
        if waiters > WAITERS_MAX then return bad("the step's intents name %d waiters; at most %d", waiters, WAITERS_MAX) end
      else
        return bad('is not a kind of intent (waitfor, needmet, needgone, waive)')
      end
    end
    return nil
  end

  -- One run: what it read, and what it has decided. Every list is an array,
  -- so the order of everything it emits is the order the intents name things.
  local function new_run(S, ctx)
    local r = {S = S, ctx = ctx, recs = {}, own = {}, walked = {}, nwalked = 0,
      visit = {}, low = {}, onstack = {}, comp = {}, tstack = {}, nvisit = 0, ncomp = 0,
      work = {expanded = 0, edges = 0, indexed = 0},
      wait = {}, removed = {}, added = {}, nadded = {}, nremoved = {},
      pend_wait = {}, pend_missing = {}, missing_pending = {}, quar = {}, jopen = {},
      folds = {}, fold_order = {}, ops = {}, op_index = {}, notes = {}, note_index = {},
      missing_added = {}, waived_missing = {}, r_known = false, r = 0}
    return r
  end

  -- index_request: the request's entries of the work table read once a step:
  -- which ids its create entries make (with the entry that sets their fields),
  -- and which ids any entry names, since a card is named once in a step or
  -- Layer 1 refuses TWICE. Counts the ids it takes in (ctx.intent_work.indexed).
  local function index_request(r)
    local made, named = {}, {}
    for ei, e in ipairs(r.ctx.request.entries or {}) do
      local kind = e.kind
      if e.t == work() and (kind == 'create' or kind == 'move' or kind == 'remove' or kind == 'guard') then
        for pos, id in ipairs(e.ids or {}) do
          r.work.indexed = r.work.indexed + 1
          if named[id] == nil then named[id] = {entry = ei - 1, kind = kind} end
          if kind == 'create' and made[id] == nil then made[id] = {entry = e, pos = pos} end
        end
      end
    end
    r.made, r.named = made, named
  end

  -- rd: a checked read of the sprint's own key (S.readcmd; the store's answer
  -- for a missing value is false).
  local function rd(r, argv, kind, k, reserve)
    return r.S.readcmd(r.ctx, {argv = argv, access = {{key = k, kind = kind, mode = 'read'}}}, reserve, 'metadata')
  end

  -- wait_load: which of the cards are in wait:<n> in the store, one ZMSCORE in
  -- pieces of at most 1,000 (a batch, never a probe a card).
  local function wait_load(r, k, cards)
    local seen = r.wait[k]
    if not seen then seen = {}; r.wait[k] = seen end
    local ask = {}
    for _, c in ipairs(cards) do
      if seen[c] == nil then seen[c] = false; ask[#ask + 1] = c end
    end
    for first = 1, #ask, PIECE do
      local argv = {'ZMSCORE', k}
      local last = math.min(first + PIECE - 1, #ask)
      for i = first, last do argv[#argv + 1] = ask[i] end
      local vals, err = rd(r, argv, 'zset', k, RESERVE_SCORE * (last - first + 1) + RESERVE_SHELL)
      if err then return err end
      for i = first, last do seen[ask[i]] = vals[i - first + 1] ~= false and vals[i - first + 1] ~= nil end
    end
    return nil
  end
  -- in_wait: the card is in wait:<n> as the store has it and this step has not
  -- yet removed it (a card this step admits is not: no other intent names it).
  local function in_wait(r, k, card)
    if r.removed[k] and r.removed[k][card] then return false end
    return r.wait[k] ~= nil and r.wait[k][card] == true
  end
  local function wait_size(r, k)
    local n, err = rd(r, {'ZCARD', k}, 'zset', k, RESERVE_COUNT)
    if err then return nil, err end
    return n + (r.nadded[k] or 0) - (r.nremoved[k] or 0), nil
  end

  -- quarantine_load: which of the cards are in {p}quarantine@e, in one HMGET in
  -- pieces of at most 1,000 (a batch, never a probe a card); one HLEN says
  -- whether anything is (normally nothing is), and then no HMGET is sent.
  local function quarantine_load(r, cards)
    local k = key(r.ctx, 'quarantine')
    local ask = {}
    for _, c in ipairs(cards) do
      if r.quar[c] == nil then r.quar[c] = false; ask[#ask + 1] = c end
    end
    if #ask == 0 then return nil end
    if r.qlen == nil then
      local n, err = rd(r, {'HLEN', k}, 'hash', k, RESERVE_COUNT)
      if err then return err end
      r.qlen = n
    end
    if r.qlen == 0 then return nil end
    for first = 1, #ask, PIECE do
      local argv = {'HMGET', k}
      local last = math.min(first + PIECE - 1, #ask)
      for i = first, last do argv[#argv + 1] = ask[i] end
      local vals, err = rd(r, argv, 'hash', k, RESERVE_QUARANTINE * (last - first + 1) + RESERVE_SHELL)
      if err then return err end
      for i = first, last do r.quar[ask[i]] = vals[i - first + 1] ~= false and vals[i - first + 1] ~= nil end
    end
    return nil
  end
  -- quarantined: the card is in {p}quarantine@e; every derivation leaves it
  -- out. The cards the intents name were loaded together (prefetch); one the
  -- batch did not name is loaded alone.
  local function quarantined(r, card)
    if r.quar[card] == nil then
      local err = quarantine_load(r, {card})
      if err then return nil, err end
    end
    return r.quar[card], nil
  end
  -- jopen_load: which of the fields (<type>|<cause>) of {p}jopen:<card>@e the
  -- store has, in one HMGET in pieces of at most 1,000 fields (a batch, never a
  -- probe a field; the key is the card's, so a command reads one card).
  local function jopen_load(r, card, fields)
    local k = key(r.ctx, 'jopen:' .. card)
    local seen = r.jopen[card]
    if not seen then seen = {}; r.jopen[card] = seen end
    local ask = {}
    for _, f in ipairs(fields) do
      if seen[f] == nil then seen[f] = false; ask[#ask + 1] = f end
    end
    for first = 1, #ask, PIECE do
      local argv = {'HMGET', k}
      local last = math.min(first + PIECE - 1, #ask)
      for i = first, last do argv[#argv + 1] = ask[i] end
      local vals, err = rd(r, argv, 'hash', k, RESERVE_JUDGMENT * (last - first + 1) + RESERVE_SHELL)
      if err then return err end
      for i = first, last do seen[ask[i]] = vals[i - first + 1] ~= false and vals[i - first + 1] ~= nil end
    end
    return nil
  end
  -- judgment_open: the card has an open judgment of the type for the cause, the
  -- field <type>|<cause> of {p}jopen:<card>@e (1.3.1). The judgments of the
  -- cards the waives name were loaded together (prefetch); one the batch did
  -- not name is loaded alone.
  local function judgment_open(r, card, typ, cause)
    local f = typ .. '|' .. cause
    local seen = r.jopen[card]
    if seen == nil or seen[f] == nil then
      local err = jopen_load(r, card, {f})
      if err then return nil, err end
    end
    return r.jopen[card][f], nil
  end

  -- field_of: a record's field as text, empty when absent or not read.
  local function field_of(rec, name)
    local f = rec.fields and rec.fields[name]
    if f and f.present then return f.value end
    return ''
  end
  -- open_of: a waiting card's open (I2); an absent or empty field is 0.
  local function open_of(rec)
    local v = field_of(rec, 'open')
    if v == '' then return 0, true end
    if not string.match(v, '^[+-]?%d+$') then return 0, false end
    local n = tonumber(v)
    return n, n >= 0
  end
  local function is_waiting(rec)
    return rec.exists and type(rec.place) == 'table' and rec.place.col == WAITING
  end
  local function has_fields(rec, fields)
    for _, f in ipairs(fields) do
      if rec.fields[f] == nil then return false end
    end
    return true
  end

  -- fetch: the records of ids, with the fields named, that the run has not
  -- read with them, in one S.before (which caches per call and reads the
  -- fields in one command a card, AL1 and AL8).
  local function fetch(r, ids, fields)
    local ask, seen = {}, {}
    for _, id in ipairs(ids) do
      if not seen[id] then
        seen[id] = true
        local rec = r.recs[id]
        if not (rec and (not rec.exists or has_fields(rec, fields))) then ask[#ask + 1] = id end
      end
    end
    if #ask == 0 then return nil end
    local got, err = r.S.before(r.ctx, work(), ask, fields)
    if err then return err end
    for _, id in ipairs(ask) do
      local new, cur = got[id], r.recs[id]
      if cur == nil or cur == new then
        r.recs[id] = new
      else
        for f, v in pairs(new.fields or {}) do cur.fields[f] = v end
      end
    end
    return nil
  end

  -- makes: a create entry of the work table makes the card.
  local function makes(r, card) return r.made[card] ~= nil end
  -- created: the create entry of the work table that names the card, its
  -- column and the fields it sets, the shared set overridden by the card's each.
  local function created(r, card)
    local m = r.made[card]
    if not m then return nil end
    local e = m.entry
    local fields = {}
    for k, v in pairs(e.set or {}) do fields[k] = v end
    local each = e.each and e.each[m.pos]
    for k, v in pairs(each or {}) do fields[k] = v end
    local col = e.to or ''
    local at = string.find(col, ':[^:]*$')
    if at then col = string.sub(col, at + 1) end
    return {col = col, fields = fields}
  end

  -- op: the command a step sends one key, the members (and scores) in the
  -- order they were decided; commands are cut into pieces at the end.
  local function op(r, cmd, k)
    local id = cmd .. '\0' .. k
    local o = r.op_index[id]
    if not o then
      o = {cmd = cmd, key = k, members = {}, scores = {}}
      r.op_index[id] = o
      r.ops[#r.ops + 1] = o
    end
    return o
  end
  -- want_wait: the card is to be in wait:<n>; flush puts it there, unless it
  -- is there already, once the probes of the step are batched.
  local function want_wait(r, need, card)
    r.pend_wait[#r.pend_wait + 1] = {need = need, card = card}
  end
  -- want_missing: the need, which has no record, is to be in missing; flush
  -- enters it once, unless it is there.
  local function want_missing(r, need)
    if not r.missing_pending[need] then
      r.missing_pending[need] = true
      r.pend_missing[#r.pend_missing + 1] = need
    end
  end
  local function remove_wait(r, need, card)
    local k = key(r.ctx, 'wait:' .. need)
    local o = op(r, 'ZREM', k)
    o.members[#o.members + 1] = card
    r.removed[k] = r.removed[k] or {}
    r.removed[k][card] = true
    r.nremoved[k] = (r.nremoved[k] or 0) + 1
  end
  -- note: a request to J to open or close the judgment of the type for the
  -- cause on the subject, grouped by its operation, type and cause (1.3.4).
  local function note(r, o, typ, cause, subject)
    local id = o .. '\0' .. typ .. '\0' .. cause
    local n = r.note_index[id]
    if not n then
      n = {op = o, type = typ, cause = cause, subjects = {}, named = {}}
      r.note_index[id] = n
      r.notes[#r.notes + 1] = n
    end
    if not n.named[subject] then
      n.named[subject] = true
      n.subjects[#n.subjects + 1] = subject
    end
  end
  -- fold: what the step does to an existing card's fields, for the intent of
  -- the kind given: one entry a card, however many intents name it.
  local function fold(r, card, cause)
    local f = r.folds[card]
    if not f then
      f = {delta = 0, waived = {}, causes = {}}
      r.folds[card] = f
      r.fold_order[#r.fold_order + 1] = card
    end
    if not contains(f.causes, cause) then f.causes[#f.causes + 1] = cause end
    return f
  end

  -- running: R, from the call's one TIME and the clock's fields (1.2):
  -- t - stopped_ms - (stopped_since_ms is "" ? 0 : t - stopped_since_ms).
  local function running(r)
    if r.r_known then return r.r, nil end
    local ctx = r.ctx
    if not string.match(ctx.now_ms, '^%d+$') then
      return nil, refuse(r, 'CONFIG', {}, "the call's time %s is not a whole number", '"' .. ctx.now_ms .. '"')
    end
    local now = tonumber(ctx.now_ms)
    local k = ctx.space .. 'sprint:clock'
    local vals, err = rd(r, {'HMGET', k, 'stopped_ms', 'stopped_since_ms'}, 'hash', k, RESERVE_CLOCK)
    if err then return nil, err end
    local stopped, since = 0, nil
    local names = {'stopped_ms', 'stopped_since_ms'}
    for i = 1, 2 do
      local v = vals[i]
      if v ~= false and v ~= nil and v ~= '' then
        if not string.match(v, '^[+-]?%d+$') then
          return nil, refuse(r, 'CONFIG', {}, "the clock's %s is %s, not a whole number", names[i], '"' .. v .. '"')
        end
        if i == 1 then stopped = tonumber(v) else since = tonumber(v) end
      end
    end
    local at = now - stopped
    if since then at = at - (now - since) end
    r.r, r.r_known = at, true
    return at, nil
  end

  -- flush_waits: put the admissions in wait:<n>, unless they are there. The
  -- store's answer is read once a key, for every admitted card that waits for
  -- it, in pieces of at most 1,000 (a step of 2,000 admissions that wait for one
  -- need asks wait:<n> twice, not 2,000 times); the decisions are then made in
  -- the order the intents were decided.
  local function flush_waits(r)
    local by_key, order = {}, {}
    for _, p in ipairs(r.pend_wait) do
      local k = key(r.ctx, 'wait:' .. p.need)
      if not by_key[k] then by_key[k] = {}; order[#order + 1] = k end
      local list = by_key[k]
      list[#list + 1] = p.card
    end
    for _, k in ipairs(order) do
      local err = wait_load(r, k, by_key[k])
      if err then return err end
    end
    for _, p in ipairs(r.pend_wait) do
      local k = key(r.ctx, 'wait:' .. p.need)
      if not (r.wait[k][p.card] or (r.added[k] and r.added[k][p.card])) then
        local o = op(r, 'ZADD', k)
        o.scores[#o.scores + 1] = WAIT_SCORE
        o.members[#o.members + 1] = p.card
        r.added[k] = r.added[k] or {}
        r.added[k][p.card] = true
        r.nadded[k] = (r.nadded[k] or 0) + 1
      end
    end
    return nil
  end
  -- flush_missing: enter the needs with no record in missing at R, each once,
  -- unless it is there: the store has no ZADD NX, so the phase reads the scores,
  -- one ZMSCORE in pieces, and decides (errata 2, item 5).
  local function flush_missing(r)
    local needs = r.pend_missing
    if #needs == 0 then return nil end
    local k = key(r.ctx, 'missing')
    local there = {}
    for first = 1, #needs, PIECE do
      local argv = {'ZMSCORE', k}
      local last = math.min(first + PIECE - 1, #needs)
      for i = first, last do argv[#argv + 1] = needs[i] end
      local vals, err = rd(r, argv, 'zset', k, RESERVE_SCORE * (last - first + 1) + RESERVE_SHELL)
      if err then return err end
      for i = first, last do there[needs[i]] = vals[i - first + 1] ~= false and vals[i - first + 1] ~= nil end
    end
    for _, n in ipairs(needs) do
      if not (there[n] or r.missing_added[n]) then
        local at, err = running(r)
        if err then return err end
        local o = op(r, 'ZADD', k)
        o.scores[#o.scores + 1] = string.format('%.0f', at)
        o.members[#o.members + 1] = n
        r.missing_added[n] = true
      end
    end
    return nil
  end

  -- lookup: what the walk goes on through from the card c: the needs of a card
  -- this step admits, or of an open waiting card. Any other card ends the walk
  -- there. Looking a card up counts one of the step's WALK_MAX, whatever the
  -- card turns out to be (waiting or not, present or not). The walk looks each
  -- card up once; a card looked up again (by cycle) is counted and read once.
  local function lookup(r, c, head)
    if r.own[c] then return r.own[c], nil end
    if not r.walked[c] then
      if r.nwalked >= WALK_MAX then
        return nil, r.S.refuse('XGUARD', {ids = {head}, budget = 'walk_records', limit = WALK_MAX, actual = WALK_MAX + 1},
          string.format('the needs walk from %s went past %d records; a needs cycle could not be ruled out', head, WALK_MAX))
      end
      r.walked[c] = true
      r.nwalked = r.nwalked + 1
    end
    local err = fetch(r, {c}, {'needs', 'open'})
    if err then return nil, err end
    local needs = {}
    local rec = r.recs[c]
    if is_waiting(rec) then
      local open, ok = open_of(rec)
      if not ok then return nil, drift(r, c, 'has an open that is not a whole number') end
      if open > 0 then needs = split(field_of(rec, 'needs')) end
    end
    return needs, nil
  end
  -- cycle: the refusal of a need that reaches its waiter w: the chain from the
  -- need start to w, found by a depth-first search from start, taking the needs
  -- of a card in the order it names them, over the cards the walk has read (a
  -- card it has not is looked up as the walk would). Run once, for the one
  -- refusal of a step (1.3.3).
  local function cycle(r, w, start)
    local chain = {start, w}
    if start ~= w then
      local parent = {[start] = ''}
      local stack = {start}
      local found = false
      while #stack > 0 and not found do
        local c = stack[#stack]
        stack[#stack] = nil
        local needs, err = lookup(r, c, start)
        if err then return err end
        for i = #needs, 1, -1 do
          local e = needs[i]
          if parent[e] == nil then
            parent[e] = c
            if e == w then found = true; break end
            stack[#stack + 1] = e
          end
        end
      end
      if not found then
        return refuse(r, 'CONFIG', {w}, 'the needs walk found a cycle through %s and %s that it cannot name', start, w)
      end
      chain = {w}
      local c = parent[w]
      while c ~= nil and c ~= '' do chain[#chain + 1] = c; c = parent[c] end
      local n = #chain
      for i = 1, math.floor(n / 2) do chain[i], chain[n + 1 - i] = chain[n + 1 - i], chain[i] end
    end
    local through = ''
    if #chain > 2 then
      local mid = {}
      for i = 2, #chain - 1 do mid[#mid + 1] = chain[i] end
      if #mid > CHAIN_NAMED then
        through = ' through ' .. table.concat(mid, ', ', 1, CHAIN_NAMED) .. ', ...'
      else
        through = ' through ' .. table.concat(mid, ', ')
      end
    end
    return refuse(r, 'XGUARD', chain, '%s needs %s%s; a needs cycle', start, w, through)
  end
  -- explore: expand the cards reachable from start that no earlier search of the
  -- step expanded, once each, as Tarjan's algorithm does: number the cards in the
  -- order the search meets them, keep the lowest number each can reach, and give
  -- every card the number of its component when the card that opened the
  -- component finishes. Refuses at once when a need of a card it expands is w,
  -- the waiter whose need it searches from: that is the cycle, found where the
  -- search first meets it. One expansion a card and one look at each of its
  -- needs, however many admissions and needs of the step name the card.
  local function explore(r, start, w)
    if r.visit[start] then return nil end
    local frames = {}
    local function enter(c)
      local needs, err = lookup(r, c, start)
      if err then return err end
      r.nvisit = r.nvisit + 1
      r.visit[c], r.low[c] = r.nvisit, r.nvisit
      r.tstack[#r.tstack + 1] = c
      r.onstack[c] = true
      r.work.expanded = r.work.expanded + 1
      frames[#frames + 1] = {node = c, needs = needs, next = 1}
      return nil
    end
    local err = enter(start)
    if err then return err end
    while #frames > 0 do
      local top = #frames
      local f = frames[top]
      if f.next <= #f.needs then
        local e = f.needs[f.next]
        f.next = f.next + 1
        r.work.edges = r.work.edges + 1
        if e == w then return cycle(r, w, start) end
        if not r.visit[e] then
          err = enter(e)
          if err then return err end
        elseif r.onstack[e] and r.visit[e] < r.low[f.node] then
          r.low[f.node] = r.visit[e]
        end
      else
        local c = f.node
        frames[top] = nil
        if r.low[c] == r.visit[c] then
          r.ncomp = r.ncomp + 1
          while true do
            local x = r.tstack[#r.tstack]
            r.tstack[#r.tstack] = nil
            r.onstack[x] = false
            r.comp[x] = r.ncomp
            if x == c then break end
          end
        end
        if top > 1 then
          local p = frames[top - 1].node
          if r.low[c] < r.low[p] then r.low[p] = r.low[c] end
        end
      end
    end
    return nil
  end
  -- reaches: the cycle check of one need of a waitfor (1.3.3): the need n of the
  -- card w reaches w when w is on a cycle through n. The search is the step's,
  -- shared by every admission and need: a card it has expanded is not expanded
  -- again, and what it knows of that card (its component) answers for every later
  -- need that reaches it. A need that reaches w is refused XGUARD naming the
  -- chain, and a walk that would read more than WALK_MAX records in the step is
  -- refused XGUARD naming its head, the need it began at.
  local function reaches(r, w, n)
    if n == w then return cycle(r, w, n) end
    local err = explore(r, n, w)
    if err then return err end
    -- w has a need n, so n reaches w exactly when the two are in one strongly
    -- connected component. Both are finished: the search that expanded n ended
    -- above, and w is expanded by it or by an earlier one when n reaches it, and
    -- unseen (no component) when it does not.
    local cn, cw = r.comp[n], r.comp[w]
    if cn ~= nil and cw ~= nil and cn == cw then return cycle(r, w, n) end
    return nil
  end

  -- waitfor (1.3.3): admit a card that waits for needs. It first walks the
  -- needs graph, then decides the card goes in wait:<n> of each need that is
  -- open or has no record, that a need with no record goes in missing, and asks
  -- J to open the judgments (flush puts the first two in the commands). The card
  -- is created by this step; its create entry carries its needs and its open,
  -- and derive checks them against the real state. A need the same step creates
  -- is open: it will be a card of the table.
  local function wait_for(r, it)
    local w, needs = it.card, dedup(it.needs or {})
    local made = created(r, w)
    if not made then
      return refuse(r, 'REQUEST', {w}, '%s waits for %s and no create entry of the step makes it', w, table.concat(needs, ', '))
    end
    if made.col ~= WAITING then
      return refuse(r, 'REQUEST', {w}, '%s has needs and is created in %s; a card with needs is created in waiting', w, made.col)
    end
    for _, n in ipairs(needs) do
      local err = reaches(r, w, n)
      if err then return err end
    end
    local err = fetch(r, needs, {})
    if err then return err end
    local state, open = {}, 0
    for i, n in ipairs(needs) do
      local rec = r.recs[n]
      if makes(r, n) then
        state[i] = 'open'
      elseif not rec.exists then
        state[i] = 'missing'
      elseif type(rec.place) ~= 'table' then
        state[i] = 'dropped'
      elseif rec.place.col == LANDED then
        state[i] = 'landed'
      else
        state[i] = 'open'
      end
      if state[i] ~= 'landed' then open = open + 1 end
    end
    local named = {}
    for _, n in ipairs(split(made.fields.needs)) do named[n] = true end
    for _, n in ipairs(needs) do
      if not named[n] then
        return refuse(r, 'REQUEST', {w}, '%s waits for %s and its create entry does not name it in needs', w, n)
      end
    end
    local have = 0
    local v = made.fields.open
    if v ~= nil and v ~= '' then
      if not string.match(v, '^[+-]?%d+$') or tonumber(v) < 0 then
        return refuse(r, 'REQUEST', {w}, '%s is created with open %s, not a whole number', w, '"' .. v .. '"')
      end
      have = tonumber(v)
    end
    if have ~= open then
      return refuse(r, 'XGUARD', {w}, 'the needs of %s changed since the plan read them: its create says open %d, the needs say %d',
        w, have, open)
    end
    for i, n in ipairs(needs) do
      if state[i] == 'open' then
        want_wait(r, n, w)
      elseif state[i] == 'missing' then
        want_wait(r, n, w)
        want_missing(r, n)
        note(r, 'open', TYPE_MISSING, n, w)
      elseif state[i] == 'dropped' then
        note(r, 'open', TYPE_DROPPED, n, w)
      end
    end
    return nil
  end

  -- needmet (1.3.3): lower the open of each card still waiting for the landed
  -- need by one, take it out of wait:<n>, and ask J to close its judgment
  -- "blocked on something missing" on the need; J closes it only if it is open
  -- (1.3.4). A card not in wait:<n>, or quarantined, is left out. Two needs of
  -- one waiter landing in one step lower its open by two, in one entry.
  local function need_met(r, it)
    local n = it.need
    local k = key(r.ctx, 'wait:' .. n)
    local waiters = dedup(it.waiters or {})
    local err = wait_load(r, k, waiters)
    if err then return err end
    local live = {}
    for _, w in ipairs(waiters) do
      if in_wait(r, k, w) then
        local q
        q, err = quarantined(r, w)
        if err then return err end
        if not q then live[#live + 1] = w end
      end
    end
    if #live == 0 then return nil end
    err = fetch(r, live, {'open'})
    if err then return err end
    for _, w in ipairs(live) do
      local rec = r.recs[w]
      if not is_waiting(rec) then return drift(r, w, 'is in wait:' .. n .. ' and is not a waiting card') end
      local open, ok = open_of(rec)
      local f = fold(r, w, 'needmet')
      if not ok or open + f.delta < 1 then
        return drift(r, w, 'is in wait:' .. n .. ' and its open does not count the need')
      end
      f.delta = f.delta - 1
      remove_wait(r, n, w)
      note(r, 'close', TYPE_MISSING, n, w)
    end
    return nil
  end

  -- needgone (1.3.3): take each card still waiting for the removed need out of
  -- wait:<n>, so the head of the set moves, and ask J to open "blocked on
  -- something dropped" on the need. The open of the card stays: the judgment
  -- counts in it (I2).
  local function need_gone(r, it)
    local n = it.need
    local k = key(r.ctx, 'wait:' .. n)
    local waiters = dedup(it.waiters or {})
    local err = wait_load(r, k, waiters)
    if err then return err end
    for _, w in ipairs(waiters) do
      if in_wait(r, k, w) then
        local q
        q, err = quarantined(r, w)
        if err then return err end
        if not q then
          remove_wait(r, n, w)
          note(r, 'open', TYPE_DROPPED, n, w)
        end
      end
    end
    return nil
  end

  -- waive (1.3.3): take the needs of a blocked judgment out of a waiting card's
  -- count. A need whose judgment "blocked on something dropped" is open on the
  -- card is waived: the judgment closes, open is lowered by one, and the need
  -- enters waived. A need whose judgment "blocked on something missing" is open
  -- is waived only while it has no record: it leaves wait:<n>, and missing when
  -- wait:<n> is then empty; with a record now it is refused XGUARD, since a live
  -- prerequisite is never waived. A need with no open judgment on the card was
  -- settled since the read and is left out.
  local function waive(r, it)
    local w = it.card
    local q, err = quarantined(r, w)
    if err then return err end
    if q then return nil end
    local needs = dedup(it.needs or {})
    err = fetch(r, {w}, {'open', 'waived'})
    if err then return err end
    local rec = r.recs[w]
    local waived = split(field_of(rec, 'waived'))
    for _, n in ipairs(needs) do
      local blocked
      blocked, err = judgment_open(r, w, TYPE_DROPPED, n)
      if err then return err end
      local unmade = false
      if not blocked then
        unmade, err = judgment_open(r, w, TYPE_MISSING, n)
        if err then return err end
      end
      if blocked or unmade then
        local f = fold(r, w, 'waive')
        if not (contains(waived, n) or contains(f.waived, n)) then
          if not is_waiting(rec) then
            return drift(r, w, 'has a judgment open on ' .. n .. ' and is not a waiting card')
          end
          local open, ok = open_of(rec)
          if not ok or open + f.delta < 1 then
            return drift(r, w, 'has a judgment open on ' .. n .. ' and its open does not count it')
          end
          if unmade then
            err = fetch(r, {n}, {})
            if err then return err end
            if r.recs[n].exists then
              return refuse(r, 'XGUARD', {n}, '%s exists now; %s waits for %s to land; drop %s or wait', n, w, n, w)
            end
            local k = key(r.ctx, 'wait:' .. n)
            err = wait_load(r, k, {w})
            if err then return err end
            if not in_wait(r, k, w) then
              return drift(r, w, 'has a judgment open on ' .. n .. ' and is not in wait:' .. n)
            end
            remove_wait(r, n, w)
            r.waived_missing[#r.waived_missing + 1] = n
            note(r, 'close', TYPE_MISSING, n, w)
          else
            note(r, 'close', TYPE_DROPPED, n, w)
          end
          f.delta = f.delta - 1
          f.waived[#f.waived + 1] = n
        end
      end
    end
    return nil
  end

  -- finish: the entries folded to one a card, grouped by the cell the cards
  -- are in; the requests to J; and the commands, each key's members in pieces
  -- of at most 1,000 (L1 1.4), staged with S.command.
  local function finish(r)
    local S, ctx = r.S, r.ctx
    for _, n in ipairs(dedup(r.waived_missing)) do
      local mk = key(ctx, 'missing')
      local size, err = wait_size(r, key(ctx, 'wait:' .. n))
      if err then return nil, nil, nil, err end
      if size == 0 then
        local there
        there, err = rd(r, {'ZSCORE', mk, n}, 'zset', mk, RESERVE_SCORE)
        if err then return nil, nil, nil, err end
        if (there ~= false and there ~= nil) or r.missing_added[n] then
          local o = op(r, 'ZREM', mk)
          o.members[#o.members + 1] = n
        end
      end
    end

    local entries, by_cell = {}, {}
    for _, id in ipairs(r.fold_order) do
      local f = r.folds[id]
      if f.delta ~= 0 or #f.waived > 0 then
        -- One entry names a card once (Layer 1 refuses TWICE, a bug code that parks
        -- the key): a card this phase folds is not also named by the request's own entries.
        local at = r.named[id]
        if at then
          return nil, nil, nil, refuse(r, 'REQUEST', {id},
            '%s is changed by the %s of this step and named by entry %d (%s) of the request; a card is named once in a step',
            id, table.concat(f.causes, ' and '), at.entry, at.kind)
        end
        local rec = r.recs[id]
        local open = open_of(rec)
        local each = {open = string.format('%d', open + f.delta)}
        if #f.waived > 0 then
          local all = split(field_of(rec, 'waived'))
          for _, n in ipairs(f.waived) do all[#all + 1] = n end
          each.waived = table.concat(dedup(all), ',')
        end
        local cell = rec.place.row .. ':' .. rec.place.col
        local e = by_cell[cell]
        if not e then
          -- Layer 1 validates the combined step as JSON (json_tree): every list
          -- of an entry is an array, never a Lua table of integer keys, which
          -- reads as an object and is refused REQUEST
          local fields = S.array()
          for i, name in ipairs(INDEX_FIELDS) do fields[i] = name end
          e = {kind = 'move', t = work(), from = cell, ids = S.array(), revs = S.array(), each = S.array(), about = S.array(),
            before_fields = fields}
          by_cell[cell] = e
          entries[#entries + 1] = e
        end
        e.ids[#e.ids + 1] = id
        e.revs[#e.revs + 1] = rec.revision
        e.each[#e.each + 1] = each
        e.about[#e.about + 1] = id
      end
    end

    local notes = {}
    for _, n in ipairs(r.notes) do
      local req = {op = n.op, type = n.type, cause = n.cause, subjects = n.subjects}
      if n.op == 'open' then
        if n.type == TYPE_MISSING then req.text = string.format(TEXT_MISSING, n.cause)
        elseif n.type == TYPE_DROPPED then req.text = string.format(TEXT_DROPPED, n.cause) end
      end
      notes[#notes + 1] = req
    end

    local cmds = {}
    for _, o in ipairs(r.ops) do
      for first = 1, #o.members, PIECE do
        local args = {}
        for i = first, math.min(first + PIECE - 1, #o.members) do
          if o.cmd == 'ZADD' then args[#args + 1] = o.scores[i] end
          args[#args + 1] = o.members[i]
        end
        local d, err = S.command(ctx, o.cmd, o.key, 'zset', args)
        if err then return nil, nil, nil, err end
        cmds[#cmds + 1] = d
      end
    end
    return entries, notes, cmds, nil
  end

  -- prefetch_waives: the probes of the waives, read together once the quarantine
  -- is known. A card that is not quarantined is asked for its judgments open, one
  -- HMGET of its jopen key for both types of every need any waive of it names
  -- (jopen_load). Then wait:<n> is read, once a key across the step, for every
  -- card that has "missing" open on the need and not "dropped" (the judgment the
  -- waive takes first): those are the cards a waive of a need with no record
  -- looks up in its set. A card that turns out to be refused before it asks is
  -- read for nothing, in a step that is refused anyway.
  local function prefetch_waives(r, intents)
    local fields, order = {}, {}
    for _, it in ipairs(intents) do
      if it.kind == 'waive' and not r.quar[it.card] then
        local list = fields[it.card]
        if not list then list = {}; fields[it.card] = list; order[#order + 1] = it.card end
        for _, n in ipairs(dedup(it.needs or {})) do
          list[#list + 1] = TYPE_DROPPED .. '|' .. n
          list[#list + 1] = TYPE_MISSING .. '|' .. n
        end
      end
    end
    for _, card in ipairs(order) do
      local err = jopen_load(r, card, fields[card])
      if err then return err end
    end
    local asks, korder = {}, {}
    for _, it in ipairs(intents) do
      if it.kind == 'waive' and not r.quar[it.card] then
        local judged = r.jopen[it.card]
        for _, n in ipairs(dedup(it.needs or {})) do
          if judged[TYPE_MISSING .. '|' .. n] and not judged[TYPE_DROPPED .. '|' .. n] then
            local k = key(r.ctx, 'wait:' .. n)
            if not asks[k] then asks[k] = {}; korder[#korder + 1] = k end
            local list = asks[k]
            list[#list + 1] = it.card
          end
        end
      end
    end
    for _, k in ipairs(korder) do
      local err = wait_load(r, k, asks[k])
      if err then return err end
    end
    return nil
  end

  -- prefetch: the probes of the sprint's own keys the intents will need that
  -- the intents themselves name, read together before any is decided: wait:<n>
  -- of the waiters of every needmet and needgone, one key at a time across
  -- the step's intents, and {p}quarantine@e of those that are in it and of the
  -- card of every waive, in one batch (quarantine_load); then the waives'
  -- (prefetch_waives). The decisions are made afterwards, in the order the
  -- intents name things, from what was read.
  local function prefetch(r, intents)
    local asks, order = {}, {}
    for _, it in ipairs(intents) do
      if it.kind == 'needmet' or it.kind == 'needgone' then
        local k = key(r.ctx, 'wait:' .. it.need)
        if not asks[k] then asks[k] = {}; order[#order + 1] = k end
        local list = asks[k]
        for _, w in ipairs(it.waiters or {}) do list[#list + 1] = w end
      end
    end
    for _, k in ipairs(order) do
      local err = wait_load(r, k, asks[k])
      if err then return err end
    end
    local ids = {}
    for _, it in ipairs(intents) do
      if it.kind == 'needmet' or it.kind == 'needgone' then
        local k = key(r.ctx, 'wait:' .. it.need)
        for _, w in ipairs(it.waiters or {}) do
          if r.wait[k][w] then ids[#ids + 1] = w end
        end
      elseif it.kind == 'waive' then
        ids[#ids + 1] = it.card
      end
    end
    local err = quarantine_load(r, ids)
    if err then return err end
    return prefetch_waives(r, intents)
  end

  -- derive(ctx, intents, obs) -> entries, notes, refusal (sprint_00_core.lua's
  -- phase 'derive'). obs is the before-state of the ids the request names; the
  -- phase reads what it needs through S.before, which serves the cache.
  local function derive(ctx, intents, obs)
    local S = NS.tset
    local err = check(S, intents)
    if err then return nil, nil, err end
    local r = new_run(S, ctx)
    index_request(r)
    for _, it in ipairs(intents) do
      if it.kind == 'waitfor' then r.own[it.card] = dedup(it.needs or {}) end
    end
    err = prefetch(r, intents)
    if err then return nil, nil, err end
    for _, it in ipairs(intents) do
      if it.kind == 'waitfor' then err = wait_for(r, it)
      elseif it.kind == 'needmet' then err = need_met(r, it)
      elseif it.kind == 'needgone' then err = need_gone(r, it)
      else err = waive(r, it) end
      if err then return nil, nil, err end
    end
    err = flush_waits(r)
    if err then return nil, nil, err end
    err = flush_missing(r)
    if err then return nil, nil, err end
    local entries, notes, cmds
    entries, notes, cmds, err = finish(r)
    if err then return nil, nil, err end
    ctx.intent_cmds, ctx.intent_cmds_taken = cmds, false
    ctx.intent_work = r.work
    return entries, notes, nil
  end
  -- intent_commands: the commands derive staged for this call, for x_cmds to
  -- append to its plan (derive returns entries and notes, and has no slot for
  -- commands). Taking them is recorded, so that the core can tell commands left
  -- behind (intent_commands_pending) and refuse the step before prepare rather
  -- than drop them: open would be lowered with the card still in wait:<n> (I2).
  local function intent_commands(ctx)
    ctx.intent_cmds_taken = true
    return ctx.intent_cmds or {}
  end
  -- intent_commands_pending: derive staged commands and nothing has taken them.
  local function intent_commands_pending(ctx)
    local cmds = ctx.intent_cmds
    return cmds ~= nil and #cmds > 0 and not ctx.intent_cmds_taken
  end

  SP.derive = derive
  SP.intent_commands = intent_commands
  SP.intent_commands_pending = intent_commands_pending
  SP.phase('derive', derive)
end
end
