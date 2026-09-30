-- The tick's parts: lease (with the heartbeat as its field), pop, ingest,
-- beat, clock, and sprint with the tick end (upper design version 2.1, 1.0 to
-- 1.4 and 8.1's IT16, as errata 2, item 3 fixes the names; item IT16). Each is
-- registered through NS.SP.part (sprint_00_core.lua) and run by ns_sprint_step
-- (sprint_zz_fn.lua) in the parts phase: pre(ctx, sp, obs) reads, decides and
-- writes nothing, and cmds(ctx, plan, lp) hands back the descriptors pre built.
-- A skeleton that no store loads before gate G0 (Layer 1 revision 4 pinned,
-- Layer 2 accepted again against its hash); its Go twin is twin_parts.go in
-- internal/sprint/sprintfn, which this file follows function by function, so
-- the two emit the same commands in the same order and the same replies.
--
-- Every part obeys the step's own rule (A4): pre reads every key it will touch
-- with a command of its type (a TYPE probe first, so a key of another type is
-- WRONGTYPE before anything is planned), decides, and builds its whole list of
-- commands through S.command, which only describes them; nothing here writes.
-- A list of keys is read with one batched command for the list (HMGET and ZMSCORE
-- in pieces of at most MAX_PIECES), never one command for a key, so the cost of
-- a part grows with its input divided by a piece; the one key read for each key
-- is a beat's record, which is a key of its own (its type, like every key a step
-- writes). The parts of one step share one budget of planned commands and argv
-- bytes (finish), and a number this file names is the Go constant of the same
-- bound (sprintfn's TestPartsLuaConstantsMatchGo holds them equal).
-- Where two writes differ in kind, the write that records owed work comes
-- first and the write that forgets its trigger last (A1): the agenda's ZADDs
-- before the cursor's HSET and the due set's ZREM, the park before the agenda's
-- ZREM. The plan pre returns is the part's reply (encoded into the step's
-- reply under the part's name); the commands ride in ctx.sp_cmds under the
-- plan, which lives as long as the call.
--
-- Layer 1's NS.tset is resolved when a call runs, since table_set*.lua sorts
-- after this file (errata 2, item 8).
if NS.tset_profile then
do
  local SP = NS.SP

  -- The spans, caps and bounds of the parts, each with its section (twin_parts.go).
  local BEAT_FRESH_MS = 15000        -- 1.2, 1.4.4: beat:<m> is due at R + 15 s
  local BEHIND_SPAN_MS = 300000      -- 1.2, R18: behind is due at R + 5 min
  local MEMBERS_MAX = 250            -- 1.0 and 3 (F1-20): members and streams together at most 250
  local HELD_QUEUE_CAP = 100000      -- 1.1, Order: the held queue's cap
  local HELD_DROP_MAX = 2000         -- L1 7: a range head is at most 2,000
  local POP_MAX = 1000               -- 1.0, 1.2: K + K' at most 1,000
  local LEASE_HOLD_MAX = 3600000     -- the longest span a take or a renewal holds the lease: one hour
  local MAX_EXACT = 9007199254740991 -- L2 2: 2^53 - 1, exact as a score
  local NAME_BYTES = 256             -- L1 6: a name
  local FIELD_VALUE_BYTES = 65536    -- 1.0: one field value
  local RESULT_BYTES = 4096          -- 1.0: a caller result (the quarantine record)
  local MAX_PIECES = 1000            -- L1 1.4: collection elements of one command
  local SPRINT_KEYS_MAX = 1000       -- the most keys one list of the sprint part names (Park, Unpark): one piece
  local QUARANTINE_MAX = 2000        -- the most cards one request quarantines: the ids of one refusal
  local COUNTER_FIELDS_MAX = 502     -- the most fields of {p}next@e a change names: 2 + 2 x 250 streams
  local PARKED_VALUE_BYTES = 812     -- the longest record of one parked key: three names, two 20 digit sizes, four tabs
  local QUARANTINE_CHUNK = 250       -- quarantine ids one HMGET reads: their records are up to RESULT_BYTES each
  local COMMANDS_SHARE = 32768       -- L1 6: half of 65,536 planned commands, for all the parts together
  local ARGV_SHARE = 4194304         -- L1 6: half of 8 MiB of argv, for all the parts together
  local RANGE_RESERVE = 300          -- bytes one range member may take on the wire

  -- The fields of the heartbeat the holder's loop writes (1.4.1); owner and gen
  -- are replaced by the lease part's own, looked_at by the call's time when STOPPED.
  local HOLDER_HEARTBEAT = {agenda = true, backlog = true, due_now = true, error = true, failures = true,
    gen = true, heldq = true, looked_at = true, owner = true, rules = true, swept = true, tick_at = true,
    ticks = true}
  local LATE_KINDS = {untaken = true, unfinished = true, unbegun = true, unreported = true,
    mergeidle = true, idle = true}
  local CLOCK_FIELDS = {'stopped_ms', 'stopped_since_ms', 'stophold_ms', 'due_since_ms', 'stopraised_ms'}

  ---- shared helpers ------------------------------------------------------

  -- Layer 1's S, resolved when a call runs (errata 2, item 8).
  local function tset() return NS.tset end
  local function refuse(code, detail, message) return tset().refuse(code, detail, message) end
  local function request_refusal() return refuse('REQUEST') end
  local function stored_refusal() return refuse('CONFIG') end
  local function dec(n) return string.format('%.0f', n) end

  -- {p}<name>: a sprint key with no epoch; {p}<name>@<e>: a per-epoch key of
  -- the write epoch, "@0" included (0, row 1).
  local function skey(ctx, name) return ctx.space .. 'sprint:' .. name end
  local function ekey(ctx, name) return ctx.space .. 'sprint:' .. name .. '@' .. ctx.write_epoch end

  -- A name, key, note or cell the parts store: non-empty, at most one name
  -- long, valid UTF-8 and free of control characters, so the delimiters of a
  -- stored record can never be part of a value.
  local function valid_text(s)
    return type(s) == 'string' and #s >= 1 and #s <= NAME_BYTES and tset().utf8_valid(s) and
      not string.find(s, '%c')
  end
  -- sprint.ValidID: a member, stream, reader or card id.
  local function valid_id(s)
    return type(s) == 'string' and #s <= 128 and string.match(s, '^[A-Za-z0-9_][A-Za-z0-9_%-]*$') ~= nil
  end
  local function canonical_int(s)
    if type(s) ~= 'string' or #s > 16 or string.match(s, '^[0-9]+$') == nil or (#s > 1 and string.sub(s, 1, 1) == '0') then
      return nil
    end
    local n = tonumber(s)
    if n > MAX_EXACT then return nil end
    return n
  end
  -- The call's one TIME in ms, exact; a value that is not is the store's fault.
  local function part_now(ctx)
    local n = canonical_int(ctx.now_ms)
    if n == nil then return nil, stored_refusal() end
    return n, nil
  end
  local function arr(list)
    if #list == 0 then return tset().array() end
    return list
  end
  local function sorted_keys(t)
    local out = {}
    for k in pairs(t or {}) do out[#out + 1] = k end
    table.sort(out)
    return out
  end
  local function empty(t) return t == nil or next(t) == nil end
  -- A field the wire may omit, and that is a JSON object when it is there.
  local function map_ok(t) return t == nil or type(t) == 'table' end

  -- A key the part reads or writes must hold its own type or nothing.
  local function guard(ctx, list)
    local S = tset()
    for _, g in ipairs(list) do
      local t, err = S.rd(ctx, {'TYPE', g[1]}, 'any', 16, 'metadata')
      if err then return err end
      if type(t) == 'table' then t = t.ok end
      if t ~= 'none' and t ~= g[2] then return refuse('WRONGTYPE') end
    end
    return nil
  end

  -- Reads of one key, each through the checked helper (a typed command, its
  -- WRONGTYPE a refusal, its bytes and probes charged).
  local function hmget(ctx, key, fields)
    local argv = {'HMGET', key}
    for _, f in ipairs(fields) do argv[#argv + 1] = f end
    return tset().rd(ctx, argv, 'hash', #fields * (NAME_BYTES + 16), 'cell')
  end
  local function hget(ctx, key, field)
    local v, err = tset().rd(ctx, {'HGET', key, field}, 'hash', FIELD_VALUE_BYTES, 'cell')
    if err then return nil, err end
    if v == false then v = nil end
    return v, nil
  end
  -- HMGET of many fields in commands of at most `chunk` fields, each field
  -- reserving `reserve` bytes: a table from field to its value, absent fields
  -- missing. One command for each chunk, never one for each field.
  local function hmget_present(ctx, key, fields, reserve, chunk)
    local out = {}
    local i = 1
    while i <= #fields do
      local last = math.min(i + chunk - 1, #fields)
      local argv = {'HMGET', key}
      for j = i, last do argv[#argv + 1] = fields[j] end
      local vals, err = tset().rd(ctx, argv, 'hash', (last - i + 1) * reserve + 16, 'cell')
      if err then return nil, err end
      for j = i, last do
        local v = vals[j - i + 1]
        if v ~= false and v ~= nil then out[fields[j]] = v end
      end
      i = last + 1
    end
    return out, nil
  end
  -- ZMSCORE of many members in commands of at most MAX_PIECES: a table from
  -- member to its score string, absent members missing.
  local function zmscore(ctx, key, members)
    local out = {}
    local i = 1
    while i <= #members do
      local chunk = {'ZMSCORE', key}
      for j = i, math.min(i + MAX_PIECES - 1, #members) do chunk[#chunk + 1] = members[j] end
      local scores, err = tset().rd(ctx, chunk, 'zset', (#chunk - 2) * 40 + 16, 'cell')
      if err then return nil, err end
      for j = 3, #chunk do
        if scores[j - 2] then out[chunk[j]] = scores[j - 2] end
      end
      i = i + MAX_PIECES
    end
    return out, nil
  end
  local function zscore(ctx, key, member)
    local r, err = zmscore(ctx, key, {member})
    if err then return nil, err end
    return r[member], nil
  end
  -- The first `limit` members scored in [min, max], lowest first.
  local function zrange_by_score(ctx, key, min, max, limit)
    local S = tset()
    local res, err = S.rd(ctx, {'ZRANGEBYSCORE', key, min, max, 'LIMIT', '0', dec(limit)}, 'zset',
      limit * RANGE_RESERVE + 16, 'cell')
    if err then return nil, err end
    local _
    _, err = S.charge(ctx, 'range_id', #res)
    if err then return nil, err end
    return res, nil
  end

  -- The commands of a part: descriptors built through S.command, and the cost
  -- they add, held to the part's share of the step's shared bounds.
  local function new_out() return {list = {}, commands = 0, bytes = 0} end
  local function emit(ctx, out, command, key, kind, args)
    local d, err = tset().command(ctx, command, key, kind, args)
    if err then return err end
    out.list[#out.list + 1] = d
    out.commands = out.commands + 1
    for _, a in ipairs(d.argv) do out.bytes = out.bytes + #a end
    return nil
  end
  local function pieces(ctx, out, command, key, kind, flat, per)
    local i = 1
    while i <= #flat do
      local args = {}
      for j = i, math.min(i + per - 1, #flat) do args[#args + 1] = flat[j] end
      local err = emit(ctx, out, command, key, kind, args)
      if err then return err end
      i = i + per
    end
    return nil
  end
  local function hset(ctx, out, key, flat) return pieces(ctx, out, 'HSET', key, 'hash', flat, 2 * MAX_PIECES) end
  local function zadd(ctx, out, key, flat) return pieces(ctx, out, 'ZADD', key, 'zset', flat, 2 * MAX_PIECES) end
  local function zrem(ctx, out, key, members) return pieces(ctx, out, 'ZREM', key, 'zset', members, MAX_PIECES) end
  local function hdel(ctx, out, key, fields) return pieces(ctx, out, 'HDEL', key, 'hash', fields, MAX_PIECES) end
  -- The parts of one step share one budget: each part's list is added to the
  -- running total of the call, which is held to the parts' share of L1 6's
  -- bounds (ctx.sp_shares is a smaller budget a test gives the call).
  local function finish(ctx, out, plan)
    local shares = ctx.sp_shares
    local commands_share, bytes_share = COMMANDS_SHARE, ARGV_SHARE
    if shares then commands_share, bytes_share = shares.commands, shares.bytes end
    local commands, bytes = (ctx.sp_commands or 0) + out.commands, (ctx.sp_bytes or 0) + out.bytes
    if commands > commands_share then return refuse('LIMIT', {budget = 'planned_commands'}) end
    if bytes > bytes_share then return refuse('LIMIT', {budget = 'planned_argv_bytes'}) end
    ctx.sp_commands, ctx.sp_bytes = commands, bytes
    ctx.sp_cmds = ctx.sp_cmds or {}
    ctx.sp_cmds[plan] = out.list
    return nil
  end
  -- A step that writes nothing for a part (a loop that does not hold the lease):
  -- the plan is its reply and its list of commands is empty.
  local function writes_nothing(ctx, plan)
    local err = finish(ctx, new_out(), plan)
    if err then return nil, err end
    return plan, nil
  end
  local function handed_back(ctx, plan)
    local list = ctx.sp_cmds and ctx.sp_cmds[plan]
    if not list then return nil, stored_refusal() end
    return list, nil
  end

  ---- the clock (1.2): IT03's functions, stubbed as twin_parts_clockstub.go does ----

  local function read_clock(ctx)
    local key = skey(ctx, 'clock')
    local err = guard(ctx, {{key, 'hash'}})
    if err then return nil, err end
    local vals
    vals, err = hmget(ctx, key, CLOCK_FIELDS)
    if err then return nil, err end
    local c = {}
    for i, f in ipairs(CLOCK_FIELDS) do
      local v = vals[i]
      if v == false or v == nil or v == '' then
        c[f] = 0
      else
        c[f] = canonical_int(v)
        if c[f] == nil then return nil, stored_refusal() end
      end
    end
    return c, nil
  end
  local function clock_running(c, t)
    local r = t - c.stopped_ms
    if c.stopped_since_ms ~= 0 then r = r - (t - c.stopped_since_ms) end
    return r
  end
  local function clock_fields(c)
    local function blank(n) if n == 0 then return '' end return dec(n) end
    return {'stopped_ms', dec(c.stopped_ms), 'stopped_since_ms', blank(c.stopped_since_ms),
      'stophold_ms', blank(c.stophold_ms), 'due_since_ms', blank(c.due_since_ms),
      'stopraised_ms', blank(c.stopraised_ms)}
  end
  -- R of the call: the clock's R at the call's one TIME (1.2).
  local function running_time(ctx)
    local now, err = part_now(ctx)
    if err then return nil, nil, err end
    local c
    c, err = read_clock(ctx)
    if err then return nil, nil, err end
    return clock_running(c, now), now, nil
  end

  ---- the lease (1.1) --------------------------------------------------------

  -- Who holds the lease after the step: a pure function of the request and the
  -- lease as the pre stage reads it, so the parts only the holder may run know
  -- without the lease part's plan whether this step's loop holds it.
  local function decide_lease(ctx, sp)
    if ctx.sp_lease then return ctx.sp_lease, nil end
    local l = sp.lease
    if type(l) ~= 'table' or not valid_text(l.owner) or not valid_text(l.name) then
      return nil, request_refusal()
    end
    local hold = canonical_int(l.hold_ms)
    if hold == nil or hold < 1 or hold > LEASE_HOLD_MAX or not map_ok(l.heartbeat) then return nil, request_refusal() end
    for f, v in pairs(l.heartbeat or {}) do
      if not HOLDER_HEARTBEAT[f] or type(v) ~= 'string' or #v > FIELD_VALUE_BYTES or not tset().utf8_valid(v) then
        return nil, request_refusal()
      end
    end
    local key = skey(ctx, 'lease')
    local err = guard(ctx, {{key, 'hash'}})
    if err then return nil, err end
    local now
    now, err = part_now(ctx)
    if err then return nil, err end
    -- until_ms must stay exact: Lua's numbers are doubles, so a sum past 2^53 - 1
    -- would round, and a stored value that is not exact is refused CONFIG
    if hold > MAX_EXACT - now then return nil, request_refusal() end
    local vals
    vals, err = hmget(ctx, key, {'owner', 'name', 'until_ms', 'gen'})
    if err then return nil, err end
    local owner, name, until_raw, gen = vals[1], vals[2], vals[3], vals[4]
    if owner == false then owner = nil end
    if name == false then name = nil end
    local until_ms = 0
    if until_raw ~= false and until_raw ~= nil and until_raw ~= '' then
      until_ms = canonical_int(until_raw)
      if until_ms == nil then return nil, stored_refusal() end
    end
    if gen == false or gen == nil or gen == '' then gen = '0' end
    if not tset().uint(gen) then return nil, stored_refusal() end
    local name_l = l.name
    local d
    if owner == nil or owner == '' or until_ms <= now then
      -- free or expired: a take, at the next generation
      local nxt = tset().next(gen)
      if nxt == nil then return nil, refuse('OVERFLOW') end
      d = {held = true, took = true, gen = nxt, owner = l.owner, name = name_l, until_ms = now + hold}
    elseif owner == l.owner then
      -- its own token, still held: a renewal at the same generation
      d = {held = true, took = false, gen = gen, owner = l.owner, name = name_l, until_ms = now + hold}
    else
      d = {held = false, took = false, gen = gen, owner = owner, name = name or '', until_ms = until_ms}
    end
    ctx.sp_lease = d
    return d, nil
  end

  -- Whether a part only the lease holder may run is to run in this step (E4,
  -- T1): with a lease part the lease part decides (another loop's step is left
  -- to write the idle fields alone); without one the step's generation must be
  -- the stored one, and generation 0 is never current.
  local function tick_authority(ctx, sp)
    if ctx.sp_runs ~= nil then return ctx.sp_runs, nil end -- the parts of one step ask once
    if sp.lease ~= nil then
      local d, err = decide_lease(ctx, sp)
      if err then return false, err end
      ctx.sp_runs = d.held
      return d.held, nil
    end
    local key = skey(ctx, 'lease')
    local err = guard(ctx, {{key, 'hash'}})
    if err then return false, err end
    local vals
    vals, err = hmget(ctx, key, {'name', 'gen'})
    if err then return false, err end
    local gen = vals[2]
    if gen == false or gen == nil or gen == '' then gen = '0' end
    if not tset().uint(gen) then return false, stored_refusal() end
    local sent = sp.meta and sp.meta.gen
    if sent ~= nil and sent ~= '0' and sent == gen then
      ctx.sp_runs = true
      return true, nil
    end
    local holder = vals[1]
    if holder == false or holder == nil then holder = '' end
    return false, refuse('STALEGEN', nil,
      'the tick\'s lease is at generation ' .. gen .. ', held by ' .. holder .. '; this loop is not the tick')
  end

  SP.part('lease', {
    -- pre decides the lease and builds its commands: the holder writes the
    -- lease hash and its heartbeat fields by field (A3); another loop's step
    -- writes idle_loop and idle_at and nothing else, and its reply says who
    -- holds the lease.
    pre = function(ctx, sp, obs)
      local d, err = decide_lease(ctx, sp)
      if err then return nil, err end
      local hb = skey(ctx, 'heartbeat')
      err = guard(ctx, {{hb, 'hash'}})
      if err then return nil, err end
      local l = sp.lease
      local now = part_now(ctx)
      local plan = {held = d.held, took = d.took, gen = d.gen, owner = d.owner, name = d.name,
        until_ms = dec(d.until_ms)}
      local out = new_out()
      if not d.held then
        err = hset(ctx, out, hb, {'idle_loop', l.name or '', 'idle_at', dec(now)})
      else
        err = hset(ctx, out, skey(ctx, 'lease'), {'owner', d.owner, 'name', d.name, 'until_ms', dec(d.until_ms), 'gen', d.gen})
        if err then return nil, err end
        local fields = {}
        for f, v in pairs(l.heartbeat or {}) do fields[f] = v end
        fields.owner, fields.gen = d.owner, d.gen
        if l.stopped then fields.looked_at = dec(now) end
        local flat = {}
        for _, f in ipairs(sorted_keys(fields)) do flat[#flat + 1] = f; flat[#flat + 1] = fields[f] end
        err = hset(ctx, out, hb, flat)
      end
      if err then return nil, err end
      err = finish(ctx, out, plan)
      if err then return nil, err end
      return plan, nil
    end,
    cmds = function(ctx, plan, lp) return handed_back(ctx, plan) end})

  ---- the pop (1.2) -------------------------------------------------------------

  -- The agenda key a due entry queues (1.2).
  local function pop_key(member, from_cut)
    if from_cut then return 'late:' .. member end
    local i = string.find(member, ':', 1, true)
    if not i then return member end -- behind
    local kind, id = string.sub(member, 1, i - 1), string.sub(member, i + 1)
    if LATE_KINDS[kind] then return 'late:' .. member end
    if kind == 'beat' then return 'down:' .. id end
    return member -- overdue, hold, remind and seen, and any kind 1.2 does not know
  end

  SP.part('pop', {
    -- pre takes the due entries at or below R and the cut entries at or below
    -- wall time, at most the limit together, the cut entries first, and builds
    -- the commands in A1's order: every key added to the agenda scored by cur,
    -- then the due and cut entries removed. A key is added only when absent.
    pre = function(ctx, sp, obs)
      local limit = sp.pop and sp.pop.limit
      if type(limit) ~= 'number' or limit ~= math.floor(limit) or limit < 1 or limit > POP_MAX then
        return nil, request_refusal()
      end
      local run, err = tick_authority(ctx, sp)
      if err then return nil, err end
      local agenda, due, cut, tick = ekey(ctx, 'agenda'), ekey(ctx, 'due'), ekey(ctx, 'cut'), ekey(ctx, 'tick')
      local parked_key = ekey(ctx, 'parked')
      err = guard(ctx, {{agenda, 'zset'}, {due, 'zset'}, {cut, 'zset'}, {tick, 'hash'}, {parked_key, 'hash'}})
      if err then return nil, err end
      if not run then
        return writes_nothing(ctx, {skipped = true, popped = 0, due = 0, cut = 0, parked = 0})
      end
      local r, wall
      r, wall, err = running_time(ctx)
      if err then return nil, err end
      local cur
      cur, err = hget(ctx, tick, 'cur')
      if err then return nil, err end
      if cur == nil or cur == '' then cur = '0' end
      if not tset().uint(cur) then return nil, stored_refusal() end
      local cut_entries
      cut_entries, err = zrange_by_score(ctx, cut, '-inf', dec(wall), limit)
      if err then return nil, err end
      local due_entries = {}
      local room = limit - #cut_entries
      if room > 0 then
        due_entries, err = zrange_by_score(ctx, due, '-inf', dec(r), room)
        if err then return nil, err end
      end
      local queued, candidates = {}, {}
      for _, m in ipairs(cut_entries) do candidates[#candidates + 1] = pop_key(m, true) end
      for _, m in ipairs(due_entries) do candidates[#candidates + 1] = pop_key(m, false) end
      -- One batched read of the parked keys and one of the agenda, whatever the
      -- number of entries: a parked key is not queued (1.1).
      local parked_map
      parked_map, err = hmget_present(ctx, parked_key, candidates, PARKED_VALUE_BYTES + 16, MAX_PIECES)
      if err then return nil, err end
      local present
      present, err = zmscore(ctx, agenda, candidates)
      if err then return nil, err end
      local add, n_parked = {}, 0
      for _, k in ipairs(candidates) do
        if parked_map[k] ~= nil then
          n_parked = n_parked + 1
        elseif not present[k] and not queued[k] then
          queued[k] = true
          add[#add + 1] = cur
          add[#add + 1] = k
        end
      end
      local plan = {skipped = false, r = dec(r), popped = #cut_entries + #due_entries, due = #due_entries,
        cut = #cut_entries, parked = n_parked}
      local out = new_out()
      err = zadd(ctx, out, agenda, add) -- the keys first (A1)
      if err then return nil, err end
      err = zrem(ctx, out, due, due_entries)
      if err then return nil, err end
      err = zrem(ctx, out, cut, cut_entries) -- the entries last
      if err then return nil, err end
      err = finish(ctx, out, plan)
      if err then return nil, err end
      return plan, nil
    end,
    cmds = function(ctx, plan, lp) return handed_back(ctx, plan) end})

  ---- the ingest (1.1) -------------------------------------------------------------

  -- The rule of a key: its name before the first ':' or '@' (ingest.go).
  local function rule_of(key)
    local i = string.find(key, '[:@]')
    if i then return string.sub(key, 1, i - 1) end
    return key
  end

  SP.part('ingest', {
    -- pre refuses unless the loop may ingest (STALEGEN) and cur equals from
    -- (INGESTAT, whose detail carries cur as an exact decimal), then builds the
    -- commands in A1's order: every key added to the agenda or the held queue,
    -- the oldest held keys dropped past the queue's cap, and only then the
    -- cursor moved to `to`, so an error between the writes leaves the keys owed
    -- twice and never lost (E7). A key is added only when absent.
    pre = function(ctx, sp, obs)
      local S = tset()
      local ing = sp.ingest
      if type(ing) ~= 'table' or not S.uint(ing.from) or not S.uint(ing.to) or S.cmp(ing.to, ing.from) < 0 or
          S.cmp(ing.to, dec(MAX_EXACT)) > 0 then
        return nil, request_refusal()
      end
      local keys = ing.keys or {}
      if type(keys) ~= 'table' or (ing.from == ing.to and #keys ~= 0) then return nil, request_refusal() end
      local first, names = {}, {}
      for _, k in ipairs(keys) do
        if type(k) ~= 'table' or not valid_text(k.key) or not S.uint(k.seq) or S.cmp(k.seq, ing.from) <= 0 or
            S.cmp(k.seq, ing.to) > 0 then
          return nil, request_refusal()
        end
        if first[k.key] == nil then
          names[#names + 1] = k.key
          first[k.key] = k.seq
        elseif S.cmp(k.seq, first[k.key]) < 0 then
          first[k.key] = k.seq -- a key named twice keeps its earliest order
        end
      end
      local run, err = tick_authority(ctx, sp)
      if err then return nil, err end
      local agenda, heldq, tick, parked_key = ekey(ctx, 'agenda'), ekey(ctx, 'heldq'), ekey(ctx, 'tick'), ekey(ctx, 'parked')
      err = guard(ctx, {{agenda, 'zset'}, {heldq, 'zset'}, {tick, 'hash'}, {parked_key, 'hash'}})
      if err then return nil, err end
      local cur
      cur, err = hget(ctx, tick, 'cur')
      if err then return nil, err end
      if cur == nil or cur == '' then cur = '0' end
      if not S.uint(cur) then return nil, stored_refusal() end
      if not run then return writes_nothing(ctx, {skipped = true, cur = cur, added = 0, dropped = 0, parked = 0}) end
      if cur ~= ing.from then
        return nil, refuse('INGESTAT', {cur = cur},
          'the cursor is at ' .. cur .. ' and this ingest starts at ' .. ing.from .. ': another loop ingested')
      end
      table.sort(names, function(a, b)
        local c = S.cmp(first[a], first[b])
        if c ~= 0 then return c < 0 end
        return a < b
      end)
      -- One batched read of the parked keys for the whole page: a parked key is
      -- named, so the line is consumed and the key is not queued (1.1).
      local parked_map
      parked_map, err = hmget_present(ctx, parked_key, names, PARKED_VALUE_BYTES + 16, MAX_PIECES)
      if err then return nil, err end
      local to_agenda, to_held, n_parked = {}, {}, 0
      for _, k in ipairs(names) do
        if parked_map[k] ~= nil then
          n_parked = n_parked + 1
        elseif rule_of(k) == 'held' then
          to_held[#to_held + 1] = k
        else
          to_agenda[#to_agenda + 1] = k
        end
      end
      local in_agenda, in_held
      in_agenda, err = zmscore(ctx, agenda, to_agenda)
      if err then return nil, err end
      in_held, err = zmscore(ctx, heldq, to_held)
      if err then return nil, err end
      local add_agenda, add_held, new_held = {}, {}, 0
      for _, k in ipairs(to_agenda) do
        if not in_agenda[k] then add_agenda[#add_agenda + 1] = first[k]; add_agenda[#add_agenda + 1] = k end
      end
      for _, k in ipairs(to_held) do
        if not in_held[k] then
          add_held[#add_held + 1] = first[k]
          add_held[#add_held + 1] = k
          new_held = new_held + 1
        end
      end
      local plan = {skipped = false, cur = ing.to, added = (#add_agenda + #add_held) / 2, dropped = 0, parked = n_parked}
      local drop = {}
      local size
      size, err = S.rd(ctx, {'ZCARD', heldq}, 'zset', 32, 'cell')
      if err then return nil, err end
      local over = size + new_held - HELD_QUEUE_CAP
      if over > 0 then
        drop, err = zrange_by_score(ctx, heldq, '-inf', '+inf', math.min(over, HELD_DROP_MAX))
        if err then return nil, err end
        plan.dropped = #drop
      end
      local out = new_out()
      err = zadd(ctx, out, agenda, add_agenda) -- the keys first (A1)
      if err then return nil, err end
      err = zadd(ctx, out, heldq, add_held)
      if err then return nil, err end
      err = zrem(ctx, out, heldq, drop)
      if err then return nil, err end
      if ing.to ~= ing.from then
        err = hset(ctx, out, tick, {'cur', ing.to}) -- the cursor last
        if err then return nil, err end
      end
      err = finish(ctx, out, plan)
      if err then return nil, err end
      return plan, nil
    end,
    cmds = function(ctx, plan, lp) return handed_back(ctx, plan) end})

  ---- the beat (1.4.4) ---------------------------------------------------------------

  -- The id of a member's control card as the fleet table holds it at an epoch.
  local function stored_control_id(ctx, member)
    local id = 'ctl-' .. member
    if ctx.request_epoch == '0' then return id end
    return id .. '~' .. ctx.request_epoch
  end

  SP.part('beat', {
    -- pre writes, for each member of the beat, its record (store ms and load),
    -- moves beat:<m> to R + 15 s, and, when the member's control card says down
    -- (held does not count) or the member has no fleet row, enters seen:<m> at
    -- R; a stranger is also noted in {p}strangers once. Both "only when absent"
    -- decisions are made here, as ZADD NX and HSETNX would.
    pre = function(ctx, sp, obs)
      local S = tset()
      local b = sp.beat
      if type(b) ~= 'table' or type(b.members) ~= 'table' or #b.members == 0 or #b.members > MEMBERS_MAX then
        return nil, request_refusal()
      end
      local members = {}
      for _, m in ipairs(b.members) do
        if type(m) ~= 'table' then return nil, request_refusal() end
        members[#members + 1] = {member = m.member, load = m.load or ''}
      end
      table.sort(members, function(x, y) return tostring(x.member) < tostring(y.member) end)
      for i, m in ipairs(members) do
        if not valid_id(m.member) or type(m.load) ~= 'string' or #m.load > NAME_BYTES or not S.utf8_valid(m.load) or
            string.find(m.load, '[%z\r\n]') or (i > 1 and members[i - 1].member == m.member) then
          return nil, request_refusal()
        end
      end
      local due, strangers = ekey(ctx, 'due'), skey(ctx, 'strangers')
      local guards = {{due, 'zset'}, {strangers, 'hash'}}
      for _, m in ipairs(members) do guards[#guards + 1] = {skey(ctx, 'beat:' .. m.member), 'hash'} end
      local err = guard(ctx, guards)
      if err then return nil, err end
      local r, now
      r, now, err = running_time(ctx)
      if err then return nil, err end
      -- The control cards and their status, read through S.before (the Go twin
      -- takes them from its before hook, PartsBefore).
      local ids = {}
      for _, m in ipairs(members) do ids[#ids + 1] = stored_control_id(ctx, m.member) end
      local cards
      cards, err = S.before(ctx, 'fleet', ids, {'status'})
      if err then return nil, err end
      local seen_probe, known = {}, {}
      for _, m in ipairs(members) do seen_probe[#seen_probe + 1] = 'seen:' .. m.member; known[#known + 1] = m.member end
      local has_seen
      has_seen, err = zmscore(ctx, due, seen_probe)
      if err then return nil, err end
      local noted
      noted, err = hmget(ctx, strangers, known)
      if err then return nil, err end
      local seen, noticed_list = {}, {}
      local plan = {fresh_until = dec(r + BEAT_FRESH_MS), members = #members}
      local out = new_out()
      local zflat, hflat = {}, {}
      for i, m in ipairs(members) do
        local card = cards[ids[i]]
        if card == nil then return nil, stored_refusal() end
        err = hset(ctx, out, skey(ctx, 'beat:' .. m.member), {'at_ms', dec(now), 'load', m.load})
        if err then return nil, err end
        zflat[#zflat + 1] = dec(r + BEAT_FRESH_MS)
        zflat[#zflat + 1] = 'beat:' .. m.member
        local stranger = not card.exists
        local status = card.fields and card.fields.status
        local down = status ~= nil and status.present == true and status.value == 'down'
        if stranger or down then
          if not has_seen['seen:' .. m.member] then
            zflat[#zflat + 1] = dec(r)
            zflat[#zflat + 1] = 'seen:' .. m.member
            seen[#seen + 1] = m.member
          end
        end
        if stranger and (noted[i] == false or noted[i] == nil) then
          hflat[#hflat + 1] = m.member
          hflat[#hflat + 1] = dec(now)
          noticed_list[#noticed_list + 1] = m.member
        end
      end
      plan.seen, plan.strangers = arr(seen), arr(noticed_list)
      err = zadd(ctx, out, due, zflat)
      if err then return nil, err end
      err = hset(ctx, out, strangers, hflat)
      if err then return nil, err end
      err = finish(ctx, out, plan)
      if err then return nil, err end
      return plan, nil
    end,
    cmds = function(ctx, plan, lp) return handed_back(ctx, plan) end})

  ---- the clock part (1.2) ------------------------------------------------------------

  SP.part('clock', {
    -- pre decides the verb on the clock as the pre stage read it, so the guard
    -- is the apply's own (A2): stop while STOPPED and start while RUNNING are
    -- refused MACHINESTATE. init writes a clock that is STOPPED since the
    -- call's time and is refused over an existing one; clear runs only while
    -- STOPPED and leaves the clock as it is.
    pre = function(ctx, sp, obs)
      local verb = sp.clock and sp.clock.verb
      local key = skey(ctx, 'clock')
      local c, err = read_clock(ctx)
      if err then return nil, err end
      local now
      now, err = part_now(ctx)
      if err then return nil, err end
      local nxt
      if verb == 'init' then
        local t
        t, err = tset().rd(ctx, {'TYPE', key}, 'any', 16, 'metadata')
        if err then return nil, err end
        if type(t) == 'table' then t = t.ok end
        if t ~= 'none' then return nil, refuse('MACHINESTATE', nil, 'the sprint is already initialised') end
        nxt = {stopped_ms = 0, stopped_since_ms = now, stophold_ms = 0, due_since_ms = 0, stopraised_ms = 0}
      elseif verb == 'start' then
        if c.stopped_since_ms == 0 then return nil, refuse('MACHINESTATE', nil, 'the machine is already running') end
        nxt = {stopped_ms = c.stopped_ms + (now - c.stopped_since_ms), stopped_since_ms = 0, stophold_ms = c.stophold_ms,
          due_since_ms = 0, stopraised_ms = c.stopraised_ms}
      elseif verb == 'stop' then
        if c.stopped_since_ms ~= 0 then
          return nil, refuse('MACHINESTATE', nil, 'the machine is already stopped, since ' .. dec(c.stopped_since_ms))
        end
        nxt = {stopped_ms = c.stopped_ms, stopped_since_ms = now, stophold_ms = c.stophold_ms,
          due_since_ms = c.due_since_ms, stopraised_ms = c.stopraised_ms}
      elseif verb == 'clear' then
        if c.stopped_since_ms == 0 then return nil, refuse('MACHINESTATE', nil, 'the machine is already running') end
        nxt = c
      else
        return nil, request_refusal()
      end
      local plan = {verb = verb, running = nxt.stopped_since_ms == 0, r = dec(clock_running(nxt, now))}
      if nxt.stopped_since_ms ~= 0 then plan.stopped_since_ms = dec(nxt.stopped_since_ms) end
      local out = new_out()
      if verb ~= 'clear' then
        err = hset(ctx, out, key, clock_fields(nxt))
        if err then return nil, err end
      end
      err = finish(ctx, out, plan)
      if err then return nil, err end
      return plan, nil
    end,
    cmds = function(ctx, plan, lp) return handed_back(ctx, plan) end})

  ---- the sprint part (1.0) ------------------------------------------------------------

  -- {p}next@e's fields (1.3.1): score, streams, id:<s> or gate:<s>.
  local function counter_field(f)
    if f == 'score' or f == 'streams' then return true end
    local id = string.match(f, '^id:(.*)$') or string.match(f, '^gate:(.*)$')
    return id ~= nil and valid_id(id)
  end
  -- A counter value, where "" is a field that is not there yet.
  local function decimal_or_zero(s)
    if s == '' then return '0' end
    if tset().uint(s) then return s end
    return nil
  end
  local function size(t)
    local n = 0
    for _ in pairs(t) do n = n + 1 end
    return n
  end
  local function check_counter(c)
    local S = tset()
    if type(c) ~= 'table' or type(c.set) ~= 'table' or empty(c.set) or not map_ok(c.read) then return request_refusal() end
    if size(c.read or {}) > COUNTER_FIELDS_MAX or size(c.set) > COUNTER_FIELDS_MAX then return request_refusal() end
    for f, v in pairs(c.read or {}) do
      if decimal_or_zero(v) == nil or not counter_field(f) then return request_refusal() end
    end
    for f, v in pairs(c.set) do
      local read = (c.read or {})[f]
      if read == nil or not counter_field(f) or not S.uint(v) then return request_refusal() end
      if S.cmp(v, decimal_or_zero(read)) < 0 then return request_refusal() end
    end
    return nil
  end
  -- The record of a quarantined card (1.3.1, 1.3.5): the code, the rule, the
  -- stream, then the refusal's cells, separated by tabs. It holds no note: the
  -- judgment J opens in the same step gets its seq from Layer 2 after the part
  -- has decided, and the record is the refusal's own detail.
  local function quarantine_value(q)
    local parts = {q.code, q.rule or '', q.stream or ''}
    for _, c in ipairs(q.cells or {}) do parts[#parts + 1] = c end
    return table.concat(parts, '\t')
  end
  local function optional_text(s) return s == nil or s == '' or valid_text(s) end
  local function check_quarantine(list)
    if #list > QUARANTINE_MAX then return request_refusal() end
    for _, q in ipairs(list) do
      if type(q) ~= 'table' or not valid_text(q.id) or not valid_text(q.code) or not optional_text(q.rule) or
          not optional_text(q.stream) then
        return request_refusal()
      end
      for _, c in ipairs(q.cells or {}) do
        if not valid_text(c) then return request_refusal() end
      end
      if #quarantine_value(q) > RESULT_BYTES then return request_refusal() end
    end
    return nil
  end
  -- The body and the sprint part quarantine the same cards, in both
  -- directions (errata 2, item 6): a body quarantine no part writes would have
  -- X act on a card whose record is lost, since a part runs only when its field
  -- is set, and a part quarantine the body does not name would leave the card
  -- in the indexes X keeps, since X acts on the body's alone. The core checks it
  -- in its static phase, which runs before any validation, on whatever JSON the
  -- caller sent, so this answers false (the core refuses REQUEST) to a shape
  -- that is not a list of cards with string ids, and never raises: a script
  -- error would reach the caller in place of a refusal. When neither quarantines
  -- anything it holds, and a part's quarantine that is no list is left to the
  -- part's own check.
  function SP.quarantine_carried(sp)
    local body = sp.quarantine
    if body ~= nil and type(body) ~= 'table' then return false end
    body = body or {}
    local p = sp.sprint
    local part = type(p) == 'table' and p.quarantine or nil
    if #body == 0 and (type(part) ~= 'table' or #part == 0) then return true end
    if type(part) ~= 'table' then return false end
    local function ids_of(list)
      local set, n = {}, 0
      for _, q in ipairs(list) do
        if type(q) ~= 'table' or type(q.id) ~= 'string' then return nil, 0 end
        if not set[q.id] then set[q.id] = true; n = n + 1 end
      end
      return set, n
    end
    local named, nb = ids_of(body)
    local carried, np = ids_of(part)
    if not named or not carried or nb ~= np then return false end
    for id in pairs(named) do
      if not carried[id] then return false end
    end
    return true
  end
  -- The record of a parked key (1.1, 1.3.5): the refusal's code, the rule, the
  -- bound and the step's size and limit, separated by tabs.
  local function parked_value(k)
    return table.concat({k.code, k.rule or '', k.budget or '', k.actual or '', k.limit or ''}, '\t')
  end
  local function check_parked(park, unpark)
    local S = tset()
    if #park > SPRINT_KEYS_MAX or #unpark > SPRINT_KEYS_MAX then return request_refusal() end
    local named = {}
    for _, k in ipairs(park) do
      if type(k) ~= 'table' or not valid_text(k.key) or not valid_text(k.code) or not optional_text(k.rule) or
          not optional_text(k.budget) or named[k.key] then
        return request_refusal()
      end
      for _, d in ipairs({k.actual or '', k.limit or ''}) do
        if d ~= '' and not S.uint(d) then return request_refusal() end
      end
      named[k.key] = true
    end
    local unnamed = {}
    for _, k in ipairs(unpark) do
      if not valid_text(k) or named[k] or unnamed[k] then return request_refusal() end
      unnamed[k] = true
    end
    return nil
  end
  local function check_dropping(mark, unmark)
    if size(mark) + size(unmark) > MEMBERS_MAX then return request_refusal() end
    for s, op in pairs(mark) do
      if not valid_id(s) or not valid_text(op) or unmark[s] ~= nil then return request_refusal() end
    end
    for s, op in pairs(unmark) do
      if not valid_id(s) or not valid_text(op) then return request_refusal() end
    end
    return nil
  end
  local function list_ok(t) return t == nil or type(t) == 'table' end

  SP.part('sprint', {
    -- pre decides every write of the part on the state the pre stage read, and
    -- builds the commands: the counter guarded by the values the plan read
    -- (COUNTER when one moved), a stream marked only when no other op holds it
    -- and unmarked only by the op that holds it (DROPPING); a parked key keeps
    -- its first record, and a card already quarantined keeps its first record;
    -- a parked key is moved out of the agenda, the park written first and the
    -- agenda's ZREM last (A1). Every read of a list is one batched command for
    -- the list, and nothing is written that the key already holds. In a step
    -- that carries a lease part, a loop that does not hold the lease writes none
    -- of it. Goals and the sweep's position are not written by this part: a
    -- request that names either is refused REQUEST.
    pre = function(ctx, sp, obs)
      local S = tset()
      local p = sp.sprint
      if type(p) ~= 'table' or not map_ok(p.goals) or not map_ok(p.dropping) or not map_ok(p.undrop) or
          not list_ok(p.park) or not list_ok(p.unpark) or not list_ok(p.quarantine) or
          (p.tickend ~= nil and type(p.tickend) ~= 'table') then
        return nil, request_refusal()
      end
      if not empty(p.goals) or (p.sweep ~= nil and p.sweep ~= '') then return nil, request_refusal() end
      local err
      if p.counter ~= nil then
        err = check_counter(p.counter)
        if err then return nil, err end
      end
      local mark, unmark = p.dropping or {}, p.undrop or {}
      err = check_dropping(mark, unmark)
      if err then return nil, err end
      local park, unpark = p.park or {}, p.unpark or {}
      err = check_parked(park, unpark)
      if err then return nil, err end
      if #park ~= 0 and (sp.pop ~= nil or sp.ingest ~= nil) then
        return nil, request_refusal() -- the error step is a step of notes and sprint keys only
      end
      local quarantine = p.quarantine or {}
      err = check_quarantine(quarantine)
      if err then return nil, err end
      local coordinator = p.coordinator
      if coordinator ~= nil and coordinator ~= '' and not valid_text(coordinator) then return nil, request_refusal() end
      if coordinator == '' then coordinator = nil end
      local te = p.tickend
      if te ~= nil and not S.uint(te.backlog) then return nil, request_refusal() end

      local next_key, dropping_key, parked_key, agenda = ekey(ctx, 'next'), ekey(ctx, 'dropping'), ekey(ctx, 'parked'), ekey(ctx, 'agenda')
      local quarantine_key, coordinator_key = ekey(ctx, 'quarantine'), skey(ctx, 'coordinator')
      local tick, due = ekey(ctx, 'tick'), ekey(ctx, 'due')
      local n_drop = size(mark) + size(unmark)
      local guards = {}
      if p.counter ~= nil then guards[#guards + 1] = {next_key, 'hash'} end
      if n_drop ~= 0 then guards[#guards + 1] = {dropping_key, 'hash'} end
      if #park ~= 0 then
        guards[#guards + 1] = {parked_key, 'hash'}
        guards[#guards + 1] = {agenda, 'zset'}
      elseif #unpark ~= 0 then
        guards[#guards + 1] = {parked_key, 'hash'}
      end
      if #quarantine ~= 0 then guards[#guards + 1] = {quarantine_key, 'hash'} end
      if coordinator ~= nil then guards[#guards + 1] = {coordinator_key, 'string'} end
      if te ~= nil then
        guards[#guards + 1] = {tick, 'hash'}
        guards[#guards + 1] = {due, 'zset'}
      end
      err = guard(ctx, guards)
      if err then return nil, err end

      local plan = {skipped = false, counter = false, marked = tset().array(), unmarked = tset().array(),
        parked = tset().array(), unparked = tset().array(), quarantined = tset().array()}
      if sp.lease ~= nil then
        local held
        held, err = tick_authority(ctx, sp)
        if err then return nil, err end
        if not held then
          plan.skipped = true
          return writes_nothing(ctx, plan)
        end
      end
      local out = new_out()

      if p.counter ~= nil then
        local c = p.counter
        local fields = sorted_keys(c.read)
        local cur_of
        cur_of, err = hmget_present(ctx, next_key, fields, NAME_BYTES + 16, MAX_PIECES)
        if err then return nil, err end
        for _, f in ipairs(fields) do
          local cur = cur_of[f] or ''
          if cur ~= c.read[f] then
            return nil, refuse('COUNTER', nil, 'the counter moved since the read: ' .. f .. ' is "' .. cur ..
              '", read as "' .. c.read[f] .. '"')
          end
        end
        local flat = {}
        for _, f in ipairs(sorted_keys(c.set)) do
          if (cur_of[f] or '') ~= c.set[f] then flat[#flat + 1] = f; flat[#flat + 1] = c.set[f] end -- a field that holds the value is not written again
        end
        plan.counter = #flat ~= 0
        err = hset(ctx, out, next_key, flat)
        if err then return nil, err end
      end

      if n_drop ~= 0 then
        local streams = {}
        for _, s in ipairs(sorted_keys(mark)) do streams[#streams + 1] = s end
        for _, s in ipairs(sorted_keys(unmark)) do streams[#streams + 1] = s end
        local held_by
        held_by, err = hmget_present(ctx, dropping_key, streams, NAME_BYTES + 16, MAX_PIECES)
        if err then return nil, err end
        local put, del, marked, unmarked = {}, {}, {}, {}
        for _, s in ipairs(sorted_keys(mark)) do
          local held = held_by[s]
          if held ~= nil and held ~= mark[s] then
            return nil, refuse('DROPPING', {ids = {s}}, 'stream ' .. s .. ' is frozen by another op')
          elseif held == nil then
            put[#put + 1] = s; put[#put + 1] = mark[s]
            marked[#marked + 1] = s
          end
        end
        for _, s in ipairs(sorted_keys(unmark)) do
          local held = held_by[s]
          if held ~= nil then
            if held ~= unmark[s] then
              return nil, refuse('DROPPING', {ids = {s}}, 'stream ' .. s .. ' is frozen by another op')
            end
            del[#del + 1] = s
            unmarked[#unmarked + 1] = s
          end
        end
        plan.marked, plan.unmarked = arr(marked), arr(unmarked)
        err = hset(ctx, out, dropping_key, put)
        if err then return nil, err end
        err = hdel(ctx, out, dropping_key, del)
        if err then return nil, err end
      end

      if #park + #unpark ~= 0 then
        local names, park_keys = {}, {}
        for _, k in ipairs(park) do names[#names + 1] = k.key; park_keys[#park_keys + 1] = k.key end
        for _, k in ipairs(unpark) do names[#names + 1] = k end
        local held_by
        held_by, err = hmget_present(ctx, parked_key, names, PARKED_VALUE_BYTES + 16, MAX_PIECES)
        if err then return nil, err end
        local in_agenda
        in_agenda, err = zmscore(ctx, agenda, park_keys)
        if err then return nil, err end
        local sorted = {}
        for _, k in ipairs(park) do sorted[#sorted + 1] = k end
        table.sort(sorted, function(x, y) return x.key < y.key end)
        local put, leave, del, parked, unparked = {}, {}, {}, {}, {}
        for _, k in ipairs(sorted) do
          if held_by[k.key] == nil then put[#put + 1] = k.key; put[#put + 1] = parked_value(k) end -- an already parked key keeps its first record
          parked[#parked + 1] = k.key
          if in_agenda[k.key] ~= nil then leave[#leave + 1] = k.key end -- also finishes a move a fault left half done
        end
        local unpark_sorted = {}
        for _, k in ipairs(unpark) do unpark_sorted[#unpark_sorted + 1] = k end
        table.sort(unpark_sorted)
        for _, k in ipairs(unpark_sorted) do
          if held_by[k] ~= nil then del[#del + 1] = k; unparked[#unparked + 1] = k end
        end
        plan.parked, plan.unparked = arr(parked), arr(unparked)
        err = hset(ctx, out, parked_key, put) -- the park first (A1)
        if err then return nil, err end
        err = zrem(ctx, out, agenda, leave) -- the key leaves the agenda last
        if err then return nil, err end
        err = hdel(ctx, out, parked_key, del)
        if err then return nil, err end
      end

      if #quarantine ~= 0 then
        local sorted, ids = {}, {}
        for i, q in ipairs(quarantine) do sorted[i] = {q = q, index = i}; ids[i] = q.id end
        table.sort(ids)
        local held_by
        held_by, err = hmget_present(ctx, quarantine_key, ids, RESULT_BYTES + 16, QUARANTINE_CHUNK)
        if err then return nil, err end
        table.sort(sorted, function(x, y)
          if x.q.id ~= y.q.id then return x.q.id < y.q.id end
          return x.index < y.index
        end)
        local put, seen, list = {}, {}, {}
        for _, e in ipairs(sorted) do
          local q = e.q
          if not seen[q.id] then -- a card named twice keeps its first record
            seen[q.id] = true
            if held_by[q.id] == nil then -- already quarantined: the first refusal's evidence stands
              put[#put + 1] = q.id; put[#put + 1] = quarantine_value(q)
              list[#list + 1] = q.id
            end
          end
        end
        plan.quarantined = arr(list)
        err = hset(ctx, out, quarantine_key, put)
        if err then return nil, err end
      end

      if coordinator ~= nil then
        local cur
        cur, err = S.rd(ctx, {'GET', coordinator_key}, 'string', NAME_BYTES + 16, 'cell')
        if err then return nil, err end
        if cur == false then cur = nil end
        if cur ~= coordinator then -- the same coordinator is not written again
          plan.coordinator = coordinator
          err = emit(ctx, out, 'SET', coordinator_key, 'string', {coordinator})
          if err then return nil, err end
        end
      end

      if te ~= nil then
        local r, _now
        r, _now, err = running_time(ctx)
        if err then return nil, err end
        local behind_n
        behind_n, err = hget(ctx, tick, 'behind_n')
        if err then return nil, err end
        if behind_n == '' then behind_n = nil end
        if behind_n ~= nil and not S.uint(behind_n) then return nil, stored_refusal() end
        local entry
        entry, err = zscore(ctx, due, 'behind')
        if err then return nil, err end
        local is_set, has_entry = behind_n ~= nil, entry ~= nil
        if te.backlog == '0' and (is_set or has_entry) then
          plan.tickend = 'disarmed'
          if has_entry then
            err = zrem(ctx, out, due, {'behind'})
            if err then return nil, err end
          end
          if is_set then
            err = hdel(ctx, out, tick, {'behind_n'})
            if err then return nil, err end
          end
        elseif te.backlog == '0' then
          plan.tickend = 'quiet'
        elseif not is_set then
          plan.tickend = 'armed'
          if not has_entry then -- the entry is not moved while it stands
            err = zadd(ctx, out, due, {dec(r + BEHIND_SPAN_MS), 'behind'})
            if err then return nil, err end
          end
          err = hset(ctx, out, tick, {'behind_n', te.backlog})
          if err then return nil, err end
        else
          plan.tickend = 'held'
        end
      end

      err = finish(ctx, out, plan)
      if err then return nil, err end
      return plan, nil
    end,
    cmds = function(ctx, plan, lp) return handed_back(ctx, plan) end})
end
end
